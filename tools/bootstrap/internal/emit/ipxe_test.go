package emit_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/emit"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
)

// fakeDocker stands in for the container build: it records the
// invocation and writes a placeholder binary where the real build
// would drop one. The genuine build needs docker and several minutes,
// so it runs in the drill, not here.
type fakeDocker struct {
	calls int
	name  string
	args  []string
	fail  error
	// silent skips writing the binary, modelling a build that reports
	// success but produces nothing.
	silent bool
	root   string
}

func (f *fakeDocker) run(_ context.Context, name string, args ...string) error {
	f.calls++
	f.name, f.args = name, args
	if f.fail != nil {
		return f.fail
	}
	if f.silent {
		return nil
	}
	return os.WriteFile(filepath.Join(f.root, "boot", "ipxe.efi"), []byte("fake ipxe.efi"), 0o600)
}

// newBuilder wires a builder over an emitted tree, so embed.ipxe is on
// disk exactly as talos emit would have left it.
func newBuilder(t *testing.T) (*emit.IPXEBuilder, *fakeDocker) {
	t.Helper()
	e := testEmitter(t)
	tree, err := e.Tree()
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if err := tree.Write(e.Root); err != nil {
		t.Fatalf("Write: %v", err)
	}
	docker := &fakeDocker{root: e.Root}
	return &emit.IPXEBuilder{
		Root:     e.Root,
		BootyURL: bootyURL,
		Run:      docker.run,
		Log:      discardLogger(),
	}, docker
}

func runIPXE(t *testing.T, b *emit.IPXEBuilder) steps.Result {
	t.Helper()
	r := steps.Runner{Log: discardLogger()}
	res, err := r.Run(context.Background(), b.Steps())
	if err != nil {
		t.Fatalf("run ipxe: %v", err)
	}
	return res
}

func TestIPXEBuildsThenSkips(t *testing.T) {
	b, docker := newBuilder(t)

	if res := runIPXE(t, b); res.Applied != 1 {
		t.Errorf("first run applied %d steps, want 1", res.Applied)
	}
	if docker.calls != 1 {
		t.Fatalf("docker ran %d times, want 1", docker.calls)
	}
	if _, err := os.Stat(filepath.Join(b.Root, "boot", "ipxe.efi")); err != nil {
		t.Errorf("ipxe.efi was not produced: %v", err)
	}

	// The binary is built for this booty url; nothing should rebuild it.
	if res := runIPXE(t, b); res.Applied != 0 {
		t.Errorf("second run applied %d steps, want 0", res.Applied)
	}
	if docker.calls != 1 {
		t.Errorf("docker ran %d times total, want 1", docker.calls)
	}
}

// TestIPXERebuildsOnChangedURL is the whole point of the stamp: the
// embedded script cannot be read back out of a compiled binary, so
// without a record of what it was built from, a changed booty.url would
// silently keep serving a binary pointing at the old address.
func TestIPXERebuildsOnChangedURL(t *testing.T) {
	b, docker := newBuilder(t)
	runIPXE(t, b)

	// A changed booty.url means emit rewrites embed.ipxe; model that,
	// since emit owns the file.
	b.BootyURL = "http://10.0.10.9:8080"
	e := &emit.Emitter{Cluster: testCluster(), Bundle: testBundle(t), Root: b.Root}
	e.Cluster.Talos.Booty.URL = b.BootyURL
	tree, err := e.Tree()
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if err := tree.Write(b.Root); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if res := runIPXE(t, b); res.Applied != 1 {
		t.Errorf("changed url applied %d steps, want 1 (rebuild)", res.Applied)
	}
	if docker.calls != 2 {
		t.Errorf("docker ran %d times, want 2", docker.calls)
	}
}

// TestIPXEStaleEmbedScript covers running talos ipxe against a tree
// talos emit has not caught up with: building a binary around a stale
// script would bake in the wrong URL, so it refuses.
func TestIPXEStaleEmbedScript(t *testing.T) {
	b, docker := newBuilder(t)
	b.BootyURL = "http://10.0.10.9:8080" // config moved on; embed.ipxe did not

	r := steps.Runner{Log: discardLogger()}
	_, err := r.Run(context.Background(), b.Steps())
	if !errors.Is(err, emit.ErrEmbedScriptStale) {
		t.Fatalf("error = %v, want ErrEmbedScriptStale", err)
	}
	if !strings.Contains(err.Error(), "talos emit") {
		t.Errorf("error %q does not point at talos emit", err)
	}
	if docker.calls != 0 {
		t.Errorf("docker ran %d times on a stale script, want 0", docker.calls)
	}
}

func TestIPXEMissingEmbedScript(t *testing.T) {
	b, _ := newBuilder(t)
	if err := os.Remove(filepath.Join(b.Root, "embed.ipxe")); err != nil {
		t.Fatalf("remove embed.ipxe: %v", err)
	}

	r := steps.Runner{Log: discardLogger()}
	_, err := r.Run(context.Background(), b.Steps())
	if !errors.Is(err, emit.ErrEmbedScriptStale) {
		t.Fatalf("error = %v, want ErrEmbedScriptStale", err)
	}
}

// TestIPXEUnstampedBinary covers a binary we did not build — an
// operator's manual copy, say. We cannot tell what URL it embeds, so
// the honest answer is to rebuild rather than to assume.
func TestIPXEUnstampedBinary(t *testing.T) {
	b, docker := newBuilder(t)
	runIPXE(t, b)
	if err := os.Remove(filepath.Join(b.Root, "boot", "ipxe.efi.embed.sha256")); err != nil {
		t.Fatalf("remove stamp: %v", err)
	}

	if res := runIPXE(t, b); res.Applied != 1 {
		t.Errorf("unstamped binary applied %d steps, want 1 (rebuild)", res.Applied)
	}
	if docker.calls != 2 {
		t.Errorf("docker ran %d times, want 2", docker.calls)
	}
}

// TestIPXEBuildProducesNothing guards the failure mode where the
// container exits 0 but the binary never lands: reporting success and
// stamping it would make every later run skip a build that never
// happened.
func TestIPXEBuildProducesNothing(t *testing.T) {
	b, docker := newBuilder(t)
	docker.silent = true

	r := steps.Runner{Log: discardLogger()}
	if _, err := r.Run(context.Background(), b.Steps()); err == nil {
		t.Fatal("a build that produced no binary reported success")
	}
	if _, err := os.Stat(filepath.Join(b.Root, "boot", "ipxe.efi.embed.sha256")); !os.IsNotExist(err) {
		t.Error("a failed build left a stamp behind")
	}
}

// TestIPXEDockerInvocation pins the container contract: the output
// directory writable, the chain script read-only, and the build script
// carrying the pinned iPXE ref and EFI target.
func TestIPXEDockerInvocation(t *testing.T) {
	b, docker := newBuilder(t)
	runIPXE(t, b)

	if docker.name != "docker" {
		t.Errorf("ran %q, want docker", docker.name)
	}
	joined := strings.Join(docker.args, " ")
	for _, want := range []string{
		"run --rm",
		// The artifact is an x86-64 EFI binary, so the toolchain must be
		// x86-64 too — an arm64 workstation would otherwise build with a
		// gcc that rejects iPXE's -m64.
		"--platform linux/amd64",
		filepath.Join(b.Root, "boot") + ":/out",
		filepath.Join(b.Root, "embed.ipxe") + ":/embed.ipxe:ro",
		"debian:bookworm-slim",
		"EMBED=/embed.ipxe",
		"bin-x86_64-efi/ipxe.efi",
		"--branch v1.21.1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("docker invocation is missing %q\ngot: %s", want, joined)
		}
	}
}

func TestIPXEDryRunBuildsNothing(t *testing.T) {
	b, docker := newBuilder(t)

	var out strings.Builder
	r := steps.Runner{DryRun: true, Out: &out, Log: discardLogger()}
	res, err := r.Run(context.Background(), b.Steps())
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.Pending != 1 {
		t.Errorf("dry run reported %d pending, want 1", res.Pending)
	}
	if docker.calls != 0 {
		t.Errorf("dry run ran docker %d times, want 0", docker.calls)
	}
}

// TestIPXEDockerMountsAreAbsolute pins the one thing docker will not
// forgive about a bind mount. Anything without a leading separator is
// read as a *named volume*, not a host path, so a relative source
// fails with "includes invalid characters for a local volume name" —
// and --output defaults to the relative ./bootstrap-out, so this is
// the ordinary flow, not an edge case.
//
// The other tests here all run from t.TempDir(), which is already
// absolute, so none of them can see this.
func TestIPXEDockerMountsAreAbsolute(t *testing.T) {
	t.Chdir(t.TempDir())

	root := filepath.Join("bootstrap-out", "booty")
	e := &emit.Emitter{Cluster: testCluster(), Bundle: testBundle(t), Root: root}
	tree, err := e.Tree()
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if err := tree.Write(root); err != nil {
		t.Fatalf("Write: %v", err)
	}

	docker := &fakeDocker{root: root}
	b := &emit.IPXEBuilder{Root: root, BootyURL: bootyURL, Run: docker.run, Log: discardLogger()}
	runIPXE(t, b)

	var mounts int
	for i, arg := range docker.args {
		if arg != "--volume" || i+1 >= len(docker.args) {
			continue
		}
		mounts++
		source, _, _ := strings.Cut(docker.args[i+1], ":")
		if !filepath.IsAbs(source) {
			t.Errorf("--volume source %q is relative; docker reads that as a named volume", source)
		}
	}
	if mounts != 2 {
		t.Errorf("found %d --volume flags, want 2 (the boot dir and embed.ipxe)", mounts)
	}
}
