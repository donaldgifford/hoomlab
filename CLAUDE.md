# CLAUDE.md

Per-repo orientation for `donaldgifford/hoomlab`.

## What this is

`hoomlab` is a Go service deployed to Kubernetes via a
first-party Helm chart. The container image and the chart are published
together as OCI artifacts on every release.

- Service entrypoint under `cmd/hoomlab/`; library code under
  `internal/` (private to the module).
- Built into a distroless container via `docker buildx bake`
  (`docker-bake.hcl` defines the local / ci / release targets).
- Helm chart in `charts/hoomlab/` with helm-unittest suites in
  `tests/` — the chart is the deployment contract, not an afterthought.

## Layout

```
cmd/hoomlab/      # main package — keep thin, call into internal/
internal/                 # library code; not importable outside this module
charts/hoomlab/   # Helm chart + unittest suites + values.schema.json
Dockerfile                # multi-stage distroless build (VERSION/COMMIT/DATE args)
docker-bake.hcl           # bake targets: default (local), ci, release
justfile                  # task runner; imports docker.just + helm.just
mise.toml                 # pinned toolchain: go, golangci-lint, helm, ct, k3d, ...
.github/workflows/        # ci.yml + release.yml (per-registry train)
```

## Workflows

- `just check` — lint + test (pre-commit gate)
- `just build` — binary into `build/bin/hoomlab`
- `just docker-build` — host-native image via bake
- `just helm-test` — chart lint (helm + ct) and helm-unittest suites
- `just helm-docs` — regenerate the chart README from README.md.gotmpl
- `just k3d-install` — build dev image → import into local k3d cluster
  → `helm upgrade --install` (the inner dev loop; `just k3d-down` to
  tear down)

## Configuration contract

The chart manages the container environment — the service must honor:

- `LISTEN_ADDR` / `METRICS_ADDR` / `LOG_LEVEL` (from `config.*` values)
  and `POD_NAME` (injected from the pod spec).
- `configMap.data` / `secrets.stringData` arrive via `envFrom`;
  `extraEnv` appends raw entries. Colliding with the chart-managed
  names fails the render (`validateEnvCollisions` helper).
- Probes hit `/healthz` and `/readyz` on the listen port.

## Releases

Releases happen on merge to `main`, not on manual tags, and versions
are **lockstep** — binary, image, and chart all carry the same tag:

- The merged PR's semver label (`major`/`minor`/`patch`, or
  `dont-release` to skip everything) drives `bump-version`, which
  creates and pushes the tag.
- goreleaser publishes the GitHub Release (binary archives, checksums,
  SBOMs); bake builds the multi-arch image (cosign + SLSA);
  `VERSION`/`COMMIT`/`DATE` are stamped into both.
- The chart publishes at the tag-derived version
  (`helm package --version <tag> --app-version <tag>`); `Chart.yaml`
  keeps a `0.0.0-dev` placeholder and is never bumped by hand.
- Chart-only changes ship with the next release — merge them with a
  `patch` label if they need to go out on their own.

Do NOT push tags by hand — the release train owns them.

## Conventions

- Conventional Commits; changelogs are git-cliff-generated (root
  `cliff.toml` for the repo, `charts/hoomlab/cliff.toml` for
  the chart-only changelog).
- Lint gates: `golangci-lint` (config in `.golangci.yml`), `yamllint`,
  `markdownlint-cli2`, `actionlint`. Run `just --list` for the menu.
