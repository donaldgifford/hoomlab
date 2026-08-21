package emit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
)

// The iPXE build inputs. booty documents the manual build (an iPXE
// checkout plus "make … EMBED=…") but ships no builder image, so the
// CLI runs that same build in a container: docker is already a
// prerequisite for the booty container itself, and a pinned image plus
// a pinned iPXE ref makes the result reproducible.
const (
	// ipxeBuilderImage is the toolchain container the build runs in.
	ipxeBuilderImage = "debian:bookworm-slim"
	// ipxeRef pins the iPXE source. iPXE has no release cadence to
	// speak of; this is the last tagged version.
	ipxeRef = "v1.21.1"
	// ipxeTarget is the UEFI x86-64 binary — the one a q35 + OVMF VM
	// loads. Talos on Proxmox is UEFI-only here (see the VM spec).
	ipxeTarget = "bin-x86_64-efi/ipxe.efi"
)

// The emitted binary and the record of what it was built from.
const (
	ipxeBinaryPath = "boot/ipxe.efi"
	// ipxeStampPath records the SHA-256 of the embed.ipxe the binary
	// was built with. The embedded script cannot be read back out of a
	// compiled binary, so this stamp is what makes the rebuild
	// convergent: it is the only way to tell "built from the current
	// booty.url" from "built from the previous one".
	ipxeStampPath = "boot/ipxe.efi.embed.sha256"
)

// buildScript is the container's whole job: fetch the pinned iPXE
// source, build the EFI binary with our chain script embedded, and
// drop it in the mounted output directory. iPXE's EMBED accepts an
// absolute path, so the script is mounted rather than copied in.
const buildScript = `set -eu
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends \
	git make gcc binutils perl liblzma-dev mtools >/dev/null
git clone --quiet --depth 1 --branch ` + ipxeRef + ` https://github.com/ipxe/ipxe.git /ipxe
make -C /ipxe/src ` + ipxeTarget + ` EMBED=/embed.ipxe -j"$(nproc)"
cp /ipxe/src/` + ipxeTarget + ` /out/ipxe.efi
`

// ErrEmbedScriptStale reports that the on-disk embed.ipxe does not
// match what the current config renders — the emit stage owns that
// file, so the fix is to re-run it rather than to build a binary
// around a stale script.
var ErrEmbedScriptStale = errors.New("embed.ipxe is missing or stale")

// Runner runs an external command. It is the test seam for the docker
// invocation: the real build needs a container and several minutes, so
// unit tests stub it and the genuine build runs in the drill.
type Runner func(ctx context.Context, name string, args ...string) error

// IPXEBuilder converges boot/ipxe.efi — the iPXE binary with the booty
// chain script baked in, which is what a PXE-booting VM loads first.
type IPXEBuilder struct {
	// Root is the emit root, e.g. <output>/booty.
	Root string
	// BootyURL is the base URL the embedded chain script points at.
	BootyURL string
	// Run executes the build. Nil means the real docker invocation.
	Run Runner
	// Log receives progress. Nil means slog.Default().
	Log *slog.Logger
}

func (b *IPXEBuilder) logger() *slog.Logger {
	if b.Log == nil {
		return slog.Default()
	}
	return b.Log
}

func (b *IPXEBuilder) runner() Runner {
	if b.Run == nil {
		return execRunner(b.logger())
	}
	return b.Run
}

// execRunner runs the command for real, streaming its output to the
// log — an iPXE build takes minutes and silence would be indistinguishable
// from a hang.
func execRunner(log *slog.Logger) Runner {
	return func(ctx context.Context, name string, args ...string) error {
		// The command is the literal "docker" and every argument is
		// built from compiled-in constants plus the emit root, which is
		// a CLI flag the operator sets — never untrusted input.
		cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // see above
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		if err != nil {
			log.Error("ipxe build failed", "output", out.String())
			return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		log.Debug("ipxe build output", "output", out.String())
		return nil
	}
}

// Steps returns the single iPXE step. It is pending when the binary is
// missing or when the chain script the config renders differs from the
// one the existing binary was built with — so a changed booty.url
// triggers a rebuild and nothing else does.
func (b *IPXEBuilder) Steps() []steps.Step {
	return []steps.Step{{
		Name:  "ipxe-build",
		Check: b.check,
		Apply: b.apply,
	}}
}

func (b *IPXEBuilder) check(context.Context) (bool, error) {
	want, err := renderEmbedIPXE(b.BootyURL)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(filepath.Join(b.Root, filepath.FromSlash(ipxeBinaryPath))); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", ipxeBinaryPath, err)
	}
	stamp, err := os.ReadFile(filepath.Join(b.Root, filepath.FromSlash(ipxeStampPath)))
	if errors.Is(err, os.ErrNotExist) {
		// A binary with no stamp was not built by us and cannot be
		// vouched for; rebuild rather than assume it embeds this URL.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", ipxeStampPath, err)
	}
	return strings.TrimSpace(string(stamp)) == digestOf(want), nil
}

func (b *IPXEBuilder) apply(ctx context.Context) error {
	want, err := renderEmbedIPXE(b.BootyURL)
	if err != nil {
		return err
	}
	embedPath := filepath.Join(b.Root, embedIPXEPath)
	onDisk, err := os.ReadFile(embedPath)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w at %s: run bootstrap talos emit first", ErrEmbedScriptStale, embedPath)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", embedPath, err)
	}
	if !bytes.Equal(onDisk, want) {
		return fmt.Errorf("%w at %s: run bootstrap talos emit first", ErrEmbedScriptStale, embedPath)
	}

	bootDir := filepath.Join(b.Root, "boot")
	if err := os.MkdirAll(bootDir, dirMode); err != nil {
		return fmt.Errorf("create %s: %w", bootDir, err)
	}
	b.logger().Info("building ipxe.efi", "image", ipxeBuilderImage, "ipxe_ref", ipxeRef,
		"booty_url", b.BootyURL)
	if err := b.runner()(ctx, "docker", dockerArgs(bootDir, embedPath)...); err != nil {
		return fmt.Errorf("build ipxe.efi: %w", err)
	}

	binary := filepath.Join(b.Root, filepath.FromSlash(ipxeBinaryPath))
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("build reported success but produced no %s: %w", ipxeBinaryPath, err)
	}
	stamp := filepath.Join(b.Root, filepath.FromSlash(ipxeStampPath))
	if err := os.WriteFile(stamp, []byte(digestOf(want)+"\n"), defaultFileMode); err != nil {
		return fmt.Errorf("record %s: %w", ipxeStampPath, err)
	}
	return nil
}

// dockerArgs builds the container invocation. The output directory is
// mounted writable and the chain script read-only; nothing else from
// the host is exposed.
func dockerArgs(bootDir, embedPath string) []string {
	return []string{
		"run", "--rm",
		"--volume", bootDir + ":/out",
		"--volume", embedPath + ":/embed.ipxe:ro",
		ipxeBuilderImage,
		"sh", "-c", buildScript,
	}
}

// digestOf is the hex SHA-256 of in-memory content.
func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
