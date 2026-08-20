package emit

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// defaultFileMode is what an emitted file gets when File leaves Mode
// unset; dirMode is used for every directory the tree creates.
const (
	defaultFileMode fs.FileMode = 0o644
	dirMode         fs.FileMode = 0o750
)

// File is one emitted file: its content and the mode it is written
// with. Mode is explicit because booty-run.sh must land executable.
type File struct {
	Data []byte
	Mode fs.FileMode
}

// mode resolves the zero value to the default rather than writing an
// unreadable 0000 file.
func (f File) mode() fs.FileMode {
	if f.Mode == 0 {
		return defaultFileMode
	}
	return f.Mode
}

// Tree is a rendered artifact set keyed by slash-separated path
// relative to the emit root. Rendering to a map instead of straight to
// disk is what makes the emit step convergent: Diff compares a fresh
// render against the filesystem without writing anything.
type Tree map[string]File

// Paths returns the tree's paths in sorted order so callers iterate
// and report deterministically.
func (t Tree) Paths() []string {
	return slices.Sorted(maps.Keys(t))
}

// Write creates root as needed and writes every file, replacing what is
// already there. Re-emitting is always safe — the tree is a pure
// function of the config and the secrets bundle.
func (t Tree) Write(root string) error {
	for _, p := range t.Paths() {
		f := t[p]
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), dirMode); err != nil {
			return fmt.Errorf("create directory for %s: %w", p, err)
		}
		if err := os.WriteFile(full, f.Data, f.mode()); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
		// WriteFile only applies the mode when it creates the file; an
		// existing one keeps whatever mode it had.
		if err := os.Chmod(full, f.mode()); err != nil {
			return fmt.Errorf("chmod %s: %w", p, err)
		}
	}
	return nil
}

// Diff returns the tree paths whose on-disk content differs, missing
// files included. An empty result means the tree is already on disk
// byte for byte — the emit step's Check.
//
// Files under root that the tree does not name are ignored: the boot
// assets live there too, and an operator's own additions are not ours
// to delete.
func (t Tree) Diff(root string) ([]string, error) {
	var changed []string
	for _, p := range t.Paths() {
		full := filepath.Join(root, filepath.FromSlash(p))
		got, err := os.ReadFile(full)
		switch {
		case errors.Is(err, os.ErrNotExist):
			changed = append(changed, p)
		case err != nil:
			return nil, fmt.Errorf("read %s: %w", p, err)
		case !bytes.Equal(got, t[p].Data):
			changed = append(changed, p)
		}
	}
	return changed, nil
}
