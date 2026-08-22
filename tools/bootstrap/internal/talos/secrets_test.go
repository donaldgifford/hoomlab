package talos_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
)

const talosVersion = "v1.13.8"

func TestGenerateSecretsBundle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.yaml")

	if talos.SecretsBundleExists(path) {
		t.Fatalf("SecretsBundleExists(%s) = true before generation", path)
	}
	if err := talos.GenerateSecretsBundle(path, talosVersion); err != nil {
		t.Fatalf("GenerateSecretsBundle: %v", err)
	}
	if !talos.SecretsBundleExists(path) {
		t.Fatalf("SecretsBundleExists(%s) = false after generation", path)
	}

	// The bundle is a private key in all but name: 0600, owner-only.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %04o, want 0600", got)
	}

	// The written bundle must round-trip through machinery's own
	// loader — the same call the machineconfig stage uses to seed
	// config generation.
	bundle, err := secrets.LoadBundle(path)
	if err != nil {
		t.Fatalf("machinery LoadBundle: %v", err)
	}
	if bundle.Certs == nil || bundle.Certs.K8s == nil || bundle.Certs.OS == nil {
		t.Error("loaded bundle is missing CA certs")
	}
	if bundle.Cluster == nil || bundle.Cluster.ID == "" || bundle.Cluster.Secret == "" {
		t.Error("loaded bundle is missing cluster identity")
	}
	if bundle.TrustdInfo == nil || bundle.TrustdInfo.Token == "" {
		t.Error("loaded bundle is missing trustd token")
	}
}

func TestGenerateSecretsBundleNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.yaml")

	if err := talos.GenerateSecretsBundle(path, talosVersion); err != nil {
		t.Fatalf("first GenerateSecretsBundle: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}

	err = talos.GenerateSecretsBundle(path, talosVersion)
	if !errors.Is(err, talos.ErrSecretsExist) {
		t.Fatalf("second GenerateSecretsBundle error = %v, want ErrSecretsExist", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read bundle: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("existing bundle was modified — it must never be touched")
	}
}

func TestGenerateSecretsBundleBadVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.yaml")

	if err := talos.GenerateSecretsBundle(path, "not-a-version"); err == nil {
		t.Fatal("GenerateSecretsBundle with bad version succeeded, want error")
	}
	if talos.SecretsBundleExists(path) {
		t.Error("bundle file created despite version parse failure")
	}
}
