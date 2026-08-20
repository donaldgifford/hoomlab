package emit_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/emit"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
)

const vanillaSchematic = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"

// factory is a stand-in for the Talos Image Factory: it serves the two
// asset paths the emitter fetches and counts the requests, so a test
// can assert that a second run downloads nothing.
type factory struct {
	kernel  []byte
	initrd  []byte
	hits    atomic.Int32
	status  int
	truncat bool
}

func (f *factory) start(t *testing.T) string {
	t.Helper()
	prefix := "/image/" + vanillaSchematic + "/" + talosVersion + "/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		var body []byte
		switch strings.TrimPrefix(r.URL.Path, prefix) {
		case "kernel-amd64":
			body = f.kernel
		case "initramfs-amd64.xz":
			body = f.initrd
		default:
			http.NotFound(w, r)
			return
		}
		if f.status != 0 && f.status != http.StatusOK {
			http.Error(w, "boom", f.status)
			return
		}
		if f.truncat {
			// Announce more than we send: exactly the half-finished
			// transfer that must never be mistaken for a staged asset.
			w.Header().Set("Content-Length", fmt.Sprint(len(body)+16))
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write(body); err != nil {
				t.Errorf("write truncated body: %v", err)
			}
			return
		}
		if _, err := w.Write(body); err != nil {
			t.Errorf("write body: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func newFactory() *factory {
	return &factory{
		kernel: []byte("fake talos kernel payload"),
		initrd: []byte("fake talos initramfs payload"),
	}
}

func assetEmitter(t *testing.T, f *factory) *emit.Emitter {
	t.Helper()
	e := testEmitter(t)
	e.FactoryURL = f.start(t)
	return e
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func runSteps(t *testing.T, e *emit.Emitter) steps.Result {
	t.Helper()
	stage, err := e.Steps()
	if err != nil {
		t.Fatalf("Steps: %v", err)
	}
	runner := steps.Runner{Log: discardLogger()}
	res, err := runner.Run(context.Background(), stage)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func TestEmitStepsConverge(t *testing.T) {
	f := newFactory()
	e := assetEmitter(t, f)

	if res := runSteps(t, e); res.Applied != 2 {
		t.Errorf("first run applied %d steps, want 2", res.Applied)
	}

	kernel := filepath.Join(e.Root, "boot", "talos", talosVersion, "vmlinuz")
	initrd := filepath.Join(e.Root, "boot", "talos", talosVersion, "initramfs.xz")
	for path, want := range map[string][]byte{kernel: f.kernel, initrd: f.initrd} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s content = %q, want %q", path, got, want)
		}
		digest, err := os.ReadFile(path + ".sha256")
		if err != nil {
			t.Fatalf("read digest for %s: %v", path, err)
		}
		if strings.TrimSpace(string(digest)) != digestOf(want) {
			t.Errorf("%s digest sidecar does not match the content", path)
		}
	}

	hits := f.hits.Load()
	if hits != 2 {
		t.Errorf("first run made %d factory requests, want 2", hits)
	}

	// Re-running must be a no-op: nothing re-rendered, and — the
	// expensive half — nothing re-downloaded.
	if res := runSteps(t, e); res.Applied != 0 {
		t.Errorf("second run applied %d steps, want 0", res.Applied)
	}
	if got := f.hits.Load(); got != hits {
		t.Errorf("second run made %d more factory requests, want 0", got-hits)
	}
}

// TestEmitStepsResumeAfterInterruption models an emit killed between
// its two steps: the artifacts are on disk, the assets are not, and the
// re-run finishes the job without redoing the first half.
func TestEmitStepsResumeAfterInterruption(t *testing.T) {
	f := newFactory()
	e := assetEmitter(t, f)

	tree, err := e.Tree()
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if err := tree.Write(e.Root); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if res := runSteps(t, e); res.Applied != 1 {
		t.Errorf("resumed run applied %d steps, want 1 (assets only)", res.Applied)
	}
}

// TestBootAssetTruncatedDownload is the failure that would otherwise
// hide: a short transfer leaves no file behind, so the next run
// retries rather than serving a kernel that will not boot.
func TestBootAssetTruncatedDownload(t *testing.T) {
	f := newFactory()
	f.truncat = true
	e := assetEmitter(t, f)

	stage, err := e.Steps()
	if err != nil {
		t.Fatalf("Steps: %v", err)
	}
	runner := steps.Runner{Log: discardLogger()}
	if _, err := runner.Run(context.Background(), stage); err == nil {
		t.Fatal("truncated download succeeded, want an error")
	}

	kernel := filepath.Join(e.Root, "boot", "talos", talosVersion, "vmlinuz")
	if _, err := os.Stat(kernel); !os.IsNotExist(err) {
		t.Errorf("truncated asset was left in place at %s", kernel)
	}
	entries, err := os.ReadDir(filepath.Dir(kernel))
	if err != nil {
		t.Fatalf("read asset dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".partial-") {
			t.Errorf("temp file %s was left behind", entry.Name())
		}
	}
}

func TestBootAssetServerError(t *testing.T) {
	f := newFactory()
	f.status = http.StatusInternalServerError
	e := assetEmitter(t, f)

	stage, err := e.Steps()
	if err != nil {
		t.Fatalf("Steps: %v", err)
	}
	runner := steps.Runner{Log: discardLogger()}
	_, err = runner.Run(context.Background(), stage)
	if err == nil {
		t.Fatal("failed download reported success")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not mention the status", err)
	}
}

// TestBootAssetDigestMismatch covers the case the sidecar exists for:
// an asset that changed after it was staged. Silently re-downloading
// would paper over a corrupt copy or a tampered one, so the check
// fails loudly instead.
func TestBootAssetDigestMismatch(t *testing.T) {
	f := newFactory()
	e := assetEmitter(t, f)
	runSteps(t, e)

	kernel := filepath.Join(e.Root, "boot", "talos", talosVersion, "vmlinuz")
	if err := os.WriteFile(kernel, []byte("swapped payload"), 0o600); err != nil {
		t.Fatalf("swap asset: %v", err)
	}

	stage, err := e.Steps()
	if err != nil {
		t.Fatalf("Steps: %v", err)
	}
	runner := steps.Runner{Log: discardLogger()}
	_, err = runner.Run(context.Background(), stage)
	if err == nil {
		t.Fatal("a changed asset checked as ready")
	}
	if !strings.Contains(err.Error(), "digest") {
		t.Errorf("error %q does not explain the digest mismatch", err)
	}
}

// TestBootAssetURLs pins the factory paths: the on-disk names are what
// the catalog references, the remote names are architecture-suffixed,
// and the schematic defaults to vanilla when the config pins none.
func TestBootAssetURLs(t *testing.T) {
	f := newFactory()
	e := assetEmitter(t, f)
	runSteps(t, e)

	for _, name := range []string{"vmlinuz", "initramfs.xz"} {
		path := filepath.Join(e.Root, "boot", "talos", talosVersion, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected asset at %s: %v", path, err)
		}
	}
}
