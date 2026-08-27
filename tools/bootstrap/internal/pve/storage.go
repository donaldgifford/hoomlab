package pve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/pverr"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/storage"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/types"
)

// Declarer builds the pve storage stage: one step per declared
// storage block, each converging the cluster entry — created when
// missing, updated in place when a declared field drifted. Storage
// writes are Datastore.Allocate-gated, a regular privilege check, so
// the whole stage rides the API token (unlike formation and ACME
// account writes, which PVE reserves for root@pam).
//
// The comparison discipline comes from INV-0001 deviation 6: PVE
// treats list-valued options (nodes, content) as sets and does not
// preserve submission order or byte shape on read-back, so declared
// state is compared structurally — sets as sets, never strings.
type Declarer struct {
	Cluster *config.Cluster
	Storage *storage.Service
	// Log receives progress. Nil means slog.Default().
	Log *slog.Logger
}

func (d *Declarer) logger() *slog.Logger {
	if d.Log == nil {
		return slog.Default()
	}
	return d.Log
}

// Steps returns one convergent step per declared storage block, in
// config order. No blocks means no steps — the stage is opt-in.
func (d *Declarer) Steps() []steps.Step {
	list := make([]steps.Step, 0, len(d.Cluster.PVE.Storage))
	for i := range d.Cluster.PVE.Storage {
		decl := &d.Cluster.PVE.Storage[i]
		list = append(list, steps.Step{
			Name:  "storage-" + decl.Name,
			Check: func(ctx context.Context) (bool, error) { return d.storageCheck(ctx, decl) },
			Apply: func(ctx context.Context) error { return d.applyStorage(ctx, decl) },
		})
	}
	return list
}

// storageCheck reports done when the entry exists and no declared
// field drifted. A type or path mismatch is an error, not pending:
// both are fixed at creation, so converging them would mean deleting
// an entry that may back live VM disks — that decision is the
// operator's, never a side effect.
func (d *Declarer) storageCheck(ctx context.Context, decl *config.PVEStorage) (bool, error) {
	got, err := d.Storage.GetDatastore(ctx, decl.Name)
	if errors.Is(err, pverr.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read storage %q: %w", decl.Name, err)
	}
	if err := identityMismatch(decl, got); err != nil {
		return false, err
	}
	return storageUpdateFor(decl, got) == nil, nil
}

func (d *Declarer) applyStorage(ctx context.Context, decl *config.PVEStorage) error {
	got, err := d.Storage.GetDatastore(ctx, decl.Name)
	switch {
	case errors.Is(err, pverr.ErrNotFound):
		d.logger().Info("creating storage", "storage", decl.Name,
			"type", decl.Type, "pool", decl.Pool, "nodes", decl.Nodes)
		if _, err := d.Storage.CreateDatastore(ctx, storageSpecFor(decl)); err != nil {
			return fmt.Errorf("create storage %q: %w", decl.Name, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("read storage %q: %w", decl.Name, err)
	}

	if err := identityMismatch(decl, got); err != nil {
		return err
	}
	update := storageUpdateFor(decl, got)
	if update == nil {
		return nil // converged between check and apply
	}
	d.logger().Info("updating storage", "storage", decl.Name,
		"nodes", decl.Nodes, "content", decl.Content)
	if _, err := d.Storage.UpdateDatastore(ctx, decl.Name, update); err != nil {
		return fmt.Errorf("update storage %q: %w", decl.Name, err)
	}
	return nil
}

// identityMismatch guards the create-fixed fields. PVE's update
// schema has no type or path parameter — changing either means
// delete-and-recreate, which could orphan VM disks, so it is
// surfaced as an error naming the conflict.
func identityMismatch(decl *config.PVEStorage, got *storage.Datastore) error {
	if got.Type != decl.Type {
		return fmt.Errorf(
			"storage %q exists with type %q, config declares %q: type is fixed at creation — delete the entry yourself if the config is right",
			decl.Name, got.Type, decl.Type)
	}
	if decl.Path != "" && got.Path != decl.Path {
		return fmt.Errorf(
			"storage %q exists with path %q, config declares %q: path is fixed at creation — delete the entry yourself if the config is right",
			decl.Name, got.Path, decl.Path)
	}
	return nil
}

// storageSpecFor renders a declared block as a create spec. Only
// opinionated fields are sent; the server fills its defaults for the
// rest.
func storageSpecFor(decl *config.PVEStorage) *storage.DatastoreSpec {
	return &storage.DatastoreSpec{
		Storage: decl.Name,
		Type:    decl.Type,
		Pool:    decl.Pool,
		Path:    decl.Path,
		Content: decl.Content,
		Nodes:   decl.Nodes,
		Sparse:  types.PVEBool(decl.Sparse),
		Disable: types.PVEBool(decl.Disable),
	}
}

// storageUpdateFor compares the declared block against the read
// entry and returns the partial update converging it, or nil when
// nothing declared drifted. Empty lists and false bools are "no
// opinion" and never generate a write; the update carries the read's
// digest so a concurrent edit fails the write instead of being
// clobbered.
func storageUpdateFor(decl *config.PVEStorage, got *storage.Datastore) *storage.DatastoreUpdate {
	update := &storage.DatastoreUpdate{Digest: got.Digest}
	drifted := false

	if decl.Pool != "" && got.Pool != decl.Pool {
		update.Pool = decl.Pool
		drifted = true
	}
	if len(decl.Content) > 0 && !sameSet(decl.Content, splitCSV(got.Content)) {
		update.Content = decl.Content
		drifted = true
	}
	if len(decl.Nodes) > 0 && !sameSet(decl.Nodes, splitCSV(got.Nodes)) {
		update.Nodes = decl.Nodes
		drifted = true
	}
	if decl.Sparse && !storedSparse(got) {
		v := types.PVEBool(true)
		update.Sparse = &v
		drifted = true
	}
	if decl.Disable && !got.Disable.Bool() {
		v := types.PVEBool(true)
		update.Disable = &v
		drifted = true
	}
	if !drifted {
		return nil
	}
	return update
}

// storedSparse reads the zfspool sparse flag, which the Datastore
// read type does not model — it rides Extra as PVE's "1"/"0".
func storedSparse(got *storage.Datastore) bool {
	return got.Extra["sparse"] == "1"
}

// splitCSV splits PVE's comma-joined list rendering into its items;
// an empty string is the empty set.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// sameSet reports whether two slices carry the same items regardless
// of order or duplication — the shape PVE's list-valued options have.
func sameSet(a, b []string) bool {
	as := slices.Compact(slices.Sorted(slices.Values(a)))
	bs := slices.Compact(slices.Sorted(slices.Values(b)))
	return slices.Equal(as, bs)
}
