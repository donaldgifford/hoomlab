package emit

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/steps"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
)

// assetTimeout bounds a boot-asset download. The Image Factory builds
// assets on demand for a cold schematic, so the first fetch of a new
// version is slow in a way the second never is.
const assetTimeout = 15 * time.Minute

// Emitter renders and converges the booty artifact tree for one
// cluster.
type Emitter struct {
	// Cluster is the validated bootstrap configuration.
	Cluster *config.Cluster
	// Bundle is the secrets bundle the machineconfig templates are
	// seeded from — the cluster identity from "talos secrets".
	Bundle *secrets.Bundle
	// Root is where the tree is written, e.g. <output>/booty.
	Root string
	// FactoryURL overrides the Talos Image Factory base URL. Empty
	// means the real factory; tests point it at a local server.
	FactoryURL string
	// HTTP fetches boot assets. Nil means a client with assetTimeout.
	HTTP *http.Client
	// Log receives progress. Nil means slog.Default().
	Log *slog.Logger
}

func (e *Emitter) logger() *slog.Logger {
	if e.Log == nil {
		return slog.Default()
	}
	return e.Log
}

func (e *Emitter) httpClient() *http.Client {
	if e.HTTP == nil {
		return &http.Client{Timeout: assetTimeout}
	}
	return e.HTTP
}

// Tree renders every artifact booty consumes except the boot assets,
// which are downloaded rather than rendered. Rendering is pure: the
// same config and secrets bundle always produce the same bytes, which
// is what lets the emit step's Check be a diff.
func (e *Emitter) Tree() (Tree, error) {
	templates, err := talos.RoleTemplates(e.Bundle, e.Cluster)
	if err != nil {
		return nil, err
	}
	for _, w := range templates.Warnings {
		e.logger().Warn("machineconfig validation warning", "warning", w)
	}

	catalog, err := renderCatalog(e.Cluster)
	if err != nil {
		return nil, err
	}
	chain, err := renderEmbedIPXE(e.Cluster.Talos.Booty.URL)
	if err != nil {
		return nil, err
	}
	runScript, err := renderRunScript(e.Cluster)
	if err != nil {
		return nil, err
	}

	tree := Tree{
		"templates/" + controlPlaneTemplatePath: {Data: templates.ControlPlane},
		"templates/" + workerTemplatePath:       {Data: templates.Worker},
		embedIPXEPath:                           {Data: chain},
		runScriptPath:                           {Data: runScript, Mode: 0o755},
	}
	for path, file := range catalog {
		tree[path] = file
	}
	return tree, nil
}

// Steps converges the emitted tree: render and write the artifacts,
// then stage the boot assets. Both steps are safe to re-run — the
// first diffs, the second skips assets already staged.
func (e *Emitter) Steps() ([]steps.Step, error) {
	tree, err := e.Tree()
	if err != nil {
		return nil, err
	}
	assets := bootAssets(e.FactoryURL, &e.Cluster.Talos)

	return []steps.Step{
		{
			Name: "emit-artifacts",
			Check: func(context.Context) (bool, error) {
				changed, err := tree.Diff(e.Root)
				if err != nil {
					return false, err
				}
				for _, p := range changed {
					e.logger().Debug("artifact differs", "path", p)
				}
				return len(changed) == 0, nil
			},
			Apply: func(context.Context) error {
				return tree.Write(e.Root)
			},
		},
		{
			Name: "boot-assets",
			Check: func(context.Context) (bool, error) {
				return assetsReady(e.Root, assets)
			},
			Apply: func(ctx context.Context) error {
				return e.downloadAssets(ctx, assets)
			},
		},
	}, nil
}
