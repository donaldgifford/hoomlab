package emit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/talos"
)

// SchematicResolver turns an extension set into an Image Factory
// schematic ID. The factory's IDs are content-addressed, so resolving
// the same set is idempotent and re-emits stay byte-stable.
type SchematicResolver interface {
	Resolve(ctx context.Context, extensions []string) (string, error)
}

// factoryResolver is the real thing: it POSTs the schematic to the
// factory's /schematics endpoint, which answers with the ID that
// derived images are then addressed by.
type factoryResolver struct {
	baseURL string
	client  *http.Client
}

// factorySchematic is the POST body shape the factory expects.
type factorySchematic struct {
	Customization struct {
		SystemExtensions struct {
			OfficialExtensions []string `yaml:"officialExtensions"`
		} `yaml:"systemExtensions"`
	} `yaml:"customization"`
}

func (f *factoryResolver) Resolve(ctx context.Context, extensions []string) (string, error) {
	var schematic factorySchematic
	schematic.Customization.SystemExtensions.OfficialExtensions = extensions
	body, err := yaml.Marshal(schematic)
	if err != nil {
		return "", fmt.Errorf("marshal schematic: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.baseURL+"/schematics", strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("build schematic request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("post schematic: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("post schematic: unexpected status %s", resp.Status)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode schematic response: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("factory answered with an empty schematic id")
	}
	return result.ID, nil
}

// resolver returns the configured SchematicResolver, defaulting to
// the real factory at FactoryURL.
func (e *Emitter) resolver() SchematicResolver {
	if e.Factory != nil {
		return e.Factory
	}
	base := e.FactoryURL
	if base == "" {
		base = defaultFactoryURL
	}
	return &factoryResolver{
		baseURL: strings.TrimSuffix(base, "/"),
		client:  e.httpClient(),
	}
}

// nodeSchematics resolves every node's boot image identity
// (DESIGN-0002 extensions model): the flattened profile set becomes a
// factory schematic, resolved once per unique set; a node without
// profiles gets the pinned schematic_id or the vanilla default.
// perNode is indexed like Cluster.Talos.Nodes; unique holds the
// distinct IDs in first-appearance order — the set of boot images the
// tree must stage.
func (e *Emitter) nodeSchematics(ctx context.Context) (perNode, unique []string, err error) {
	resolved := make(map[string]string)
	perNode = make([]string, 0, len(e.Cluster.Talos.Nodes))
	for i := range e.Cluster.Talos.Nodes {
		id, err := e.schematicFor(ctx, &e.Cluster.Talos.Nodes[i], resolved)
		if err != nil {
			return nil, nil, err
		}
		perNode = append(perNode, id)
		if !slices.Contains(unique, id) {
			unique = append(unique, id)
		}
	}
	return perNode, unique, nil
}

// schematicFor resolves one node's image identity, consulting the
// per-extension-set cache so each unique set hits the factory once.
func (e *Emitter) schematicFor(ctx context.Context, node *config.TalosNode, resolved map[string]string) (string, error) {
	exts := talos.NodeExtensions(&e.Cluster.Talos, node)
	if len(exts) == 0 {
		return talos.ResolveSchematicID(e.Cluster.Talos.SchematicID), nil
	}
	key := strings.Join(exts, "\n")
	if id, ok := resolved[key]; ok {
		return id, nil
	}
	id, err := e.resolver().Resolve(ctx, exts)
	if err != nil {
		return "", fmt.Errorf("resolve schematic for node %q: %w", node.Name, err)
	}
	e.logger().Info("resolved factory schematic", "extensions", exts, "schematic", id)
	resolved[key] = id
	return id, nil
}
