// Command bootstrap is the Hoomlab bootstrap CLI: it takes bare Proxmox
// nodes to a formed Proxmox cluster with a Talos Kubernetes cluster
// running on it, driven entirely by HCL configuration files (ADR-0001).
package main

import (
	"log/slog"
	"os"
	"runtime/debug"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/cmd"
)

// Defaults for the version metadata when nothing stamps it.
const (
	defaultVersion = "dev"
	defaultCommit  = "none"
	defaultDate    = "unknown"
)

// Version metadata injected at build time via -ldflags.
var (
	version = defaultVersion
	commit  = defaultCommit
	date    = defaultDate
)

// buildInfo fills whatever -ldflags left at its default from the build
// info the Go toolchain embeds: `go install …@v0.3.0` stamps nothing,
// but records the module version, and local builds record the VCS
// revision and time. Stamped values always win.
func buildInfo(info *debug.BuildInfo, ok bool) (v, c, d string) {
	v, c, d = version, commit, date
	if !ok || info == nil {
		return v, c, d
	}
	if v == defaultVersion && info.Main.Version != "" && info.Main.Version != "(devel)" {
		v = info.Main.Version
	}
	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision" && c == defaultCommit:
			c = s.Value
		case s.Key == "vcs.time" && d == defaultDate:
			d = s.Value
		}
	}
	return v, c, d
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	v, c, d := buildInfo(debug.ReadBuildInfo())
	if err := cmd.Execute(v, c, d); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}
