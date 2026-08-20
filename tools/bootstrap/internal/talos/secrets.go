package talos

import (
	"errors"
	"fmt"
	"os"

	machcfg "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"gopkg.in/yaml.v3"
)

// ErrSecretsExist reports that the secrets bundle file is already
// present. Callers treat it as "nothing to do", never as a reason to
// overwrite: regenerating the bundle makes a new cluster identity and
// orphans every node holding the old one (DESIGN-0001 OQ-2).
var ErrSecretsExist = errors.New("secrets bundle already exists")

// SecretsBundleExists reports whether a secrets bundle file is present
// at path. It only stats — the dry-run path uses it to report
// done/pending without opening the file.
func SecretsBundleExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// GenerateSecretsBundle creates a fresh machinery secrets bundle for
// the given Talos version and writes it to path with 0600 permissions.
// An existing file — readable or not — is never touched; the call
// fails with ErrSecretsExist.
func GenerateSecretsBundle(path, talosVersion string) error {
	contract, err := machcfg.ParseContractFromVersion(talosVersion)
	if err != nil {
		return fmt.Errorf("parse talos version %q: %w", talosVersion, err)
	}
	bundle, err := secrets.NewBundle(secrets.NewClock(), contract)
	if err != nil {
		return fmt.Errorf("generate secrets bundle: %w", err)
	}
	data, err := yaml.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("encode secrets bundle: %w", err)
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w at %s", ErrSecretsExist, path)
	}
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		werr := fmt.Errorf("write %s: %w", path, err)
		if cerr := f.Close(); cerr != nil {
			return errors.Join(werr, fmt.Errorf("close %s: %w", path, cerr))
		}
		return werr
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
