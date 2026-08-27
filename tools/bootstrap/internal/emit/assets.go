package emit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
)

// defaultFactoryURL is the Talos Image Factory, which serves prebuilt
// kernel and initramfs assets per schematic and version.
const defaultFactoryURL = "https://factory.talos.dev"

// digestSuffix names the sidecar recording an asset's SHA-256 at
// download time.
const digestSuffix = ".sha256"

// bootAsset is one file staged under the boot dir for booty to serve.
type bootAsset struct {
	// Path is the tree-relative destination, e.g.
	// boot/talos/v1.13.8/vmlinuz.
	Path string
	// URL is where it is fetched from.
	URL string
}

// bootAssets lists the assets for a cluster's Talos version: one
// kernel/initramfs pair per unique schematic, staged under a
// schematic-scoped directory — the path the catalog's per-class
// profiles reference. The names on disk are the ones booty serves;
// the names at the factory are architecture-suffixed, so the two
// differ on purpose.
func bootAssets(factoryURL string, cfg *config.Talos, schematics []string) []bootAsset {
	if factoryURL == "" {
		factoryURL = defaultFactoryURL
	}
	assets := make([]bootAsset, 0, 2*len(schematics))
	for _, schematic := range schematics {
		base := fmt.Sprintf("%s/image/%s/%s",
			strings.TrimSuffix(factoryURL, "/"), schematic, cfg.Version)
		dir := path.Join("boot", "talos", cfg.Version, schematic)
		assets = append(assets,
			bootAsset{Path: path.Join(dir, "vmlinuz"), URL: base + "/kernel-amd64"},
			bootAsset{Path: path.Join(dir, "initramfs.xz"), URL: base + "/initramfs-amd64.xz"},
		)
	}
	return assets
}

// assetsReady reports whether every boot asset is present and still
// matches the digest recorded when it was downloaded.
//
// The Image Factory publishes no authoritative checksum alongside these
// assets, so the sidecar is trust-on-first-use: authenticity on the
// first fetch rests on TLS to the factory, and the digest catches
// anything that changes the file afterwards — bit rot, a half-finished
// manual copy, a swap. Downloads land atomically, so "present" already
// means "complete"; the digest is the check that survives the move to
// the booty host.
func assetsReady(root string, assets []bootAsset) (bool, error) {
	for _, a := range assets {
		ready, err := assetReady(root, a)
		if err != nil || !ready {
			return false, err
		}
	}
	return true, nil
}

// assetReady is assetsReady for a single asset.
func assetReady(root string, a bootAsset) (bool, error) {
	full := filepath.Join(root, filepath.FromSlash(a.Path))
	want, err := os.ReadFile(full + digestSuffix)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read digest for %s: %w", a.Path, err)
	}
	got, err := fileDigest(full)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if got != strings.TrimSpace(string(want)) {
		return false, fmt.Errorf(
			"%s does not match the digest recorded when it was downloaded: "+
				"delete it and re-run to fetch a fresh copy", a.Path)
	}
	return true, nil
}

// fileDigest returns the hex SHA-256 of a file.
func fileDigest(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // read-only file, nothing to flush

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", name, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// downloadAssets fetches every missing or mismatched boot asset. An
// asset already present with a matching digest is left alone: these are
// hundreds of megabytes and re-emitting must stay cheap.
func (e *Emitter) downloadAssets(ctx context.Context, assets []bootAsset) error {
	for _, a := range assets {
		ok, err := assetReady(e.Root, a)
		if err != nil {
			return err
		}
		if ok {
			e.logger().Debug("boot asset already present", "path", a.Path)
			continue
		}
		e.logger().Info("downloading boot asset", "path", a.Path, "url", a.URL)
		if err := e.download(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// download fetches one asset into the tree, writing it atomically so a
// partial transfer can never be mistaken for a staged asset, and
// records its digest.
func (e *Emitter) download(ctx context.Context, a bootAsset) error {
	full := filepath.Join(e.Root, filepath.FromSlash(a.Path))
	if err := os.MkdirAll(filepath.Dir(full), dirMode); err != nil {
		return fmt.Errorf("create directory for %s: %w", a.Path, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, http.NoBody)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", a.URL, err)
	}
	resp, err := e.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", a.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: unexpected status %s", a.URL, resp.Status)
	}

	digest, err := e.writeAtomic(full, resp.Body, resp.ContentLength)
	if err != nil {
		return fmt.Errorf("stage %s: %w", a.Path, err)
	}
	if err := os.WriteFile(full+digestSuffix, []byte(digest+"\n"), defaultFileMode); err != nil {
		return fmt.Errorf("record digest for %s: %w", a.Path, err)
	}
	return nil
}

// writeAtomic copies src to a temp file beside dst and renames it into
// place, returning the content's hex SHA-256. The rename is what makes
// "the file exists" mean "the download finished" — so the size check
// has to happen before it, while a short transfer is still discardable.
// wantSize of 0 or less means the server announced no length.
func (e *Emitter) writeAtomic(dst string, src io.Reader, wantSize int64) (digest string, err error) {
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".partial-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		// The download already failed; these cleanups are best effort.
		// Log rather than swallow, so a stray .partial left behind is
		// explained rather than mysterious.
		if cerr := tmp.Close(); cerr != nil {
			e.logger().Debug("closing partial download", "path", tmp.Name(), "err", cerr)
		}
		if rerr := os.Remove(tmp.Name()); rerr != nil {
			e.logger().Warn("could not remove partial download", "path", tmp.Name(), "err", rerr)
		}
	}()

	h := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, h), src)
	if err != nil {
		return "", fmt.Errorf("copy: %w", err)
	}
	if wantSize > 0 && written != wantSize {
		err = fmt.Errorf("got %d bytes, server announced %d — transfer was truncated", written, wantSize)
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp file: %w", err)
	}
	if err = os.Chmod(tmp.Name(), defaultFileMode); err != nil {
		return "", fmt.Errorf("chmod temp file: %w", err)
	}
	if err = os.Rename(tmp.Name(), dst); err != nil {
		return "", fmt.Errorf("rename into place: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
