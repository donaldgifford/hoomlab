// Command bootstrap is the Hoomlab bootstrap CLI: it takes bare Proxmox
// nodes to a formed Proxmox cluster with a Talos Kubernetes cluster
// running on it, driven entirely by HCL configuration files (ADR-0001).
package main

import (
	"log/slog"
	"os"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/cmd"
)

// Version metadata injected at build time via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := cmd.Execute(version, commit, date); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}
