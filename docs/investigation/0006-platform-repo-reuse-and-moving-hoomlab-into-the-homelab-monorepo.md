---
id: INV-0006
title: "Platform-repo reuse and moving hoomlab into the homelab monorepo"
status: In Progress
author: Donald Gifford
created: 2026-11-02
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0006: Platform-repo reuse and moving hoomlab into the homelab monorepo

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1: platform moved and grew; three INV-0003 verdicts need amending](#observation-1-platform-moved-and-grew-three-inv-0003-verdicts-need-amending)
  - [Observation 2: the portal is half built, and the built half is generic](#observation-2-the-portal-is-half-built-and-the-built-half-is-generic)
  - [Observation 3: gRPC is a decision, not code; the Runtime seam is the reusable part](#observation-3-grpc-is-a-decision-not-code-the-runtime-seam-is-the-reusable-part)
  - [Observation 4: the chicken-and-egg answer is a rule hoomlab already has](#observation-4-the-chicken-and-egg-answer-is-a-rule-hoomlab-already-has)
  - [Observation 5: discovery and intent is a superset of steps, with a small portable core](#observation-5-discovery-and-intent-is-a-superset-of-steps-with-a-small-portable-core)
  - [Observation 6: the rest of platform-repo: utilities, test shapes, and a release path that stops at binaries](#observation-6-the-rest-of-platform-repo-utilities-test-shapes-and-a-release-path-that-stops-at-binaries)
  - [Observation 7: homelab's own records say public repositories stay out](#observation-7-homelabs-own-records-say-public-repositories-stay-out)
  - [Observation 8: what the move would give, and what it would break](#observation-8-what-the-move-would-give-and-what-it-would-break)
  - [Observation 9: the precedent for "homelab's build" is a copy, not a merge](#observation-9-the-precedent-for-homelabs-build-is-a-copy-not-a-merge)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
  - [The move](#the-move)
  - [Reuse, in rough order of value](#reuse-in-rough-order-of-value)
  - [Housekeeping](#housekeeping)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:question:start-->
## Question

Two questions, taken together because they share an answer about
where hoomlab lives:

1. Since INV-0003, `platform` has moved into the `platform-repo`
   monorepo and gained a web portal, a design system, SSO, a GitOps
   MVP, and a hub-and-spoke management-cluster path. What in
   `platform-repo` — `go/platform`, `web/platform`, and the Bazel +
   Buildkite setup around them — is worth copying into hoomlab and
   adapting? In particular: the web portal (hoomlab has no UI), the
   CLI's gRPC transport, the chicken-and-egg problem of a service
   that manages the cluster it runs on, and the "discovery and
   intent" model.
2. Should hoomlab move into the `homelab` monorepo, under its Bazel
   and Buildkite setup, next to the other Go projects?

<!--docz:question:end-->

<!--docz:hypothesis:start-->
## Hypothesis

For (1): the portal and design system are the one genuinely new,
non-AWS asset and should be liftable; the gRPC work will turn out to
be design rather than code; the chicken-and-egg rule will match
ADR-0001; and the intent engine's reusable part will be the same
small set of packages INV-0003 found, plus whatever ownership and
discovery code was added since.

For (2): a Go project with a Helm chart and a container image will
not fit the monorepo yet, because homelab released only binaries at
the time of INV-0003's snapshot; and hoomlab being public while
homelab is private will be the deciding constraint.

<!--docz:hypothesis:end-->

<!--docz:context:start-->
## Context

INV-0003 (2026-10-04 snapshot of `platform` at `9dc6780`) concluded
that platform's ideas transfer well and its code thinly, and
recommended fixing the T3 record, ownership tags, a testable `cmd/`,
copying four utilities, and an explainable dry-run. Since then:

- `platform` was imported into `platform-repo` on 2026-10-31
  (platform-repo IMPL-0001 Phase 7), a private Bazel + Buildkite
  monorepo laid out as a copy of homelab's. About 200 commits landed
  on `go/platform` after INV-0003's snapshot: the portal shell and
  design system, SSO, spoke attach, the GitOps MVP, an integration
  suite, and a service-foundation IMPL.
- homelab finished its own monorepo migration machinery (one Go
  module under `go/`, Bazel with a remote cache, a generated Buildkite
  pipeline, label-driven `<project>/vX.Y.Z` releases) and on
  2026-10-25 put the remaining private repositories, and "the public
  ones (hoomlab, …)", on hold pending "a decision on how public
  repositories are handled, if they move at all".
- hoomlab's IMPL-0004 is Paused for fleet patching, so this is the
  window to settle direction before the service phase (RFC-0001
  Phase 2) starts.

**Triggered by:** operator request (2026-11-02). Parent documents:
RFC-0001, ADR-0001, INV-0003.

<!--docz:context:end-->

<!--docz:approach:start-->
## Approach

1. Re-inventory `platform-repo`: commits and documents added since
   INV-0003's snapshot; what moved where (`go/platform/docs` vs
   `docs/archived/platform`).
2. Five parallel read-only reviews, each with a written brief and
   asked for paths, sizes, and an ADOPT/ADAPT/PATTERN/SKIP verdict:
   - the portal binary and `web/platform` (shell, design system,
     auth, build, CI gate);
   - `platformctl` ↔ service transport, and the bootstrap-mode /
     management-cluster lifecycle;
   - the intent → discover → plan → reconcile → status model, in
     docs and code;
   - everything else in `platform-repo`: utilities, charts, dev
     loop, release mechanics, `.claude/` playbooks;
   - the `homelab` monorepo: how Go projects live, import playbook,
     releases, CI, where hoomlab already appears, and its stated
     policy on public repositories.
3. Verify the load-bearing claims directly: `.proto` and gRPC
   imports, stub sizes, release targets, the deferred-release
   reasons, repository visibility, homelab's `.gitignore`, and the
   policy sentences cited.
4. Classify, and separate the two questions in the conclusion.

Only the ideas and the public-safe facts from homelab are recorded
here: document IDs, titles, tool names, and decisions. No site
details.

<!--docz:approach:end-->

<!--docz:environment:start-->
## Environment

| Component | Version / Value |
| --------- | --------------- |
| platform-repo | `main` @ `44d3cab` (2026-11-01); private; Go 1.26.10 (`go/go.mod`); Bazel 9.2.0, rules_go 0.64.1, gazelle 0.54.0; `go/platform` 536 Go files, 0 `.proto` |
| platform (old repo) | private; INV-0003's snapshot `9dc6780` is dated 2026-10-04; `feat/gitops-render` merged 2026-10-27; last standalone release v0.5.1 |
| web/platform | Bun 1.3.14, React 19.3, TypeScript 7.0, TanStack Router 1.170 / Query 5, Tailwind 4, shadcn on `@base-ui/react`, Biome 2.5, Storybook 10.6, Playwright 1.64; ~16.4k lines of TS/TSX source (apps/portal + packages/ui), ~21k with tests and stories |
| homelab | `main` @ `50e2f47` (2026-11-02); private; one Go module `go/` (48 requirements, Go 1.26.10); Bazel 9.2.0; generated Buildkite pipeline (`boilermate ci`); projects: boilermate, keycloak-cli, champs, sdk-booty-sh |
| hoomlab | `main` @ `5884c38`; **public**; root module (service stub, 0 requirements) + nested `tools/bootstrap` (73 requirements); bootstrap v0.6.0; GitHub Actions + goreleaser + `docker buildx bake` + helm |
| Shared | `donaldgifford/hclkit` in platform and hoomlab; homelab's Bazel/Buildkite shape copied into platform-repo; the Helm chart scaffold is the same lineage in all three |

<!--docz:environment:end-->

<!--docz:findings:start-->
## Findings

### Observation 1: platform moved and grew; three INV-0003 verdicts need amending

Since INV-0003's snapshot, `go/platform` gained `cmd/portal` and
`internal/portal/*`, `internal/charts`, `internal/argocd/install`,
`internal/kubernetes/core`, `internal/baseline`, `internal/idp`,
`internal/okta`, `internal/testfs`, and `images/demo`. Platform's
archived documents added ADR-0017 (the UI as its own service with a
Go backend-for-frontend), DESIGN-0005–0010 (static hosting, portal
shell, spoke attach, SSO, design system, Bazel migration),
IMPL-0004–0010, and INV-0007–0012. The snapshot-era DESIGN-0001,
IMPL-0001 and IMPL-0002 live in `go/platform/docs/` because tests read
them; everything else is under `docs/archived/platform/`, unindexed.

Three INV-0003 statements need amending, none of which changes its
recommendation:

| INV-0003 said | Now | Effect |
| --- | --- | --- |
| Snapshot "2026-10-10" | `9dc6780` is dated 2026-10-04; nothing landed between 10-04 and 10-15 | Date only |
| Observation 3: "platform persists a plan file … should not be taken" | Still true, but it is **optional** (`plan --out`, `apply plan.json`); the "never persist" rule is hoomlab's ADR-0001, not a platform ADR. `apply -f` re-plans every time | SKIP stands; the reasoning is hoomlab's own |
| Observation 1: "Web UI stack: DEFER, only once the service has an API" | The portal shell, auth, session and the design system are built and tested without any API (Observation 2) | DEFER becomes a lift decision independent of the API |

INV-0003's Observation 5 ("the service foundation is design, not
code") still holds: `cmd/platform/main.go` is the same 27-line stub
as `cmd/hoomlab/main.go`, platform IMPL-0008 (service foundation
A1–A7) is Draft, and the management-cluster IMPL is blocked on its
"H7: the service" task.

### Observation 2: the portal is half built, and the built half is generic

Platform's UI is two things: a Go **portal** binary (sign-in, session,
static assets, and a planned reverse proxy to backends) and a Bun
workspace `web/platform` with the portal app and `@platform/ui`, the
design system. DESIGN-0006's own rule — "nothing in it knows what a
cluster or a sandbox is" — is borne out in the code.

**Built and tested** (verified: `internal/portal/{assets,auth,config,
portaltest,problem,server,session}`, ~2.5k non-test Go lines plus
~2.6k of tests):

- `assets` (215 lines): `go:embed all:dist` with a placeholder page
  when the bundle is absent, `ASSETS_DIR` for disk or `none`, SPA
  fallback, immutable caching for hashed files, strict CSP, and a
  `/config.json` runtime config so nothing is baked into the bundle.
- `auth` + `session` (~1,050 lines): go-oidc authorization code with
  PKCE and nonce, single-use state, refresh under a per-session lock,
  RP-initiated logout, CSRF middleware; the session record in Valkey
  behind a `Store` interface, with only the session ID in an
  AES-256-GCM cookie.
- `server` (~430 lines with middleware, metrics, readiness): health,
  readiness that fails first on shutdown, drain, Prometheus,
  otelhttp.
- `problem` (60 lines): an RFC 9457 writer.
- A `shell/contract.json` shared by the TypeScript and Go sides,
  checked by `TestContractTable`.

**Not built** despite being in the design: the `/api/<backend>/*`
reverse proxy (`config/backends.go` only parses the list), telemetry,
the mock backend, generated API clients, and any real domain page.
The portal's routes are `/`, `/design`, `/design/showcase` and a 404.
Platform IMPL-0004 phases U0–U2 are done, U3 ("Proxy and data") and
U4 are open.

**The design system** (`web/platform/packages/ui`): 29 shadcn
primitives (three hand-edited), 18 blocks (data-table, details-grid,
diff-view, status-badge, stepper, timeline, topology, …), 5 shell
chrome components, a three-tier oklch token set with a DTCG export
and a staleness test, a story per component, and a Playwright + axe
visual baseline. It has no build step: consumers compile the source
through `exports`. Branding is neutral (graphite panels, sky accent,
IBM Plex Sans / JetBrains Mono); nothing AWS appears in the tokens.

**Coupling, by part:**

| Part | Lines | Verdict | Why |
| --- | --- | --- | --- |
| `assets`, `server`, `problem` | ~700 | ADOPT | Generic; swap `config.Config` for a small struct |
| `auth`, `session` | ~1,050 | ADAPT | Generic go-oidc, but `idp.Profile` rejects any issuer without Okta's `/oauth2/<id>` path and assumes Okta's `groups`/`uid` claims; make the groups claim configurable and drop the org-server check. `Store` is an interface, so a single-replica in-memory or SQLite store replaces Valkey |
| `internal/idp` (1,022) | — | SKIP | Okta app catalog and contract |
| `@platform/ui` | ~5.8k src | ADOPT with rename | `@hoomlab/ui`; `status-badge` hardcodes cluster/sandbox phases, `topology` is hub-and-spoke, `permission-matrix` uses accounts as columns — rewrite those three |
| Portal shell (`src/shell`, `frame`, routes, lib) | ~1.8k | ADAPT | Keep session/contract/config/settings/theme/frame/palette and the `AppManifest` registry; replace `access.ts` (teams, entitlements, quotas, modes) with "signed-in user + groups + admin" |
| `apps/home/showcase`, `/design` sample data | ~3k | SKIP | Platform mock pages |
| The proxy, telemetry, domain pages | — | BUILD | Do not exist in platform; DESIGN-0006 lines 309–337 specify the proxy (≈150–250 lines of Go) |

Two cautions. The UI has **no CI gate** anywhere, in the old repo or
the monorepo (bun is not in homelab's CI image;
`docs/deferred/platform-ci-gates.md`), so its visual tests have only
ever run on laptops. And the committed Bazel target for the embed
lists content-hashed, gitignored bundle files in `embedsrcs`, which
cannot survive a rebuild; it is a bug to avoid copying, not a pattern.

### Observation 3: gRPC is a decision, not code; the Runtime seam is the reusable part

Verified: `go/platform` has no `.proto` file, no `buf` config, and no
direct import of `google.golang.org/grpc` (the entry in `go/go.mod`
is `// indirect`, pulled by the OTLP exporter). Platform ADR-0004
(protobuf + gRPC), ADR-0005 (talosctl-shaped CLI with direct and
remote modes), INV-0001 (grpc-gateway over ConnectRPC) and DESIGN-0003
(the service API) are all design. `platformctl` never talks to a
service.

What exists instead, and is the thing to copy, is the **seam**
(`internal/cli/runtime/runtime.go`): a `Runtime` interface
(`ValidateCluster`, `DiscoverNetwork`, `DiscoverDNS`, `PlanBootstrap`,
`Preflight`, `ApplyBootstrap`, `StatusBootstrap`, `PlanDestroy`,
`DestroyBootstrap`, `Facts`, …), a `RuntimeFactory func(ctx, exec,
region) (Runtime, error)` injected into `NewRootCommand`, a
`DirectRuntime` that runs the library in process, and a `FakeRuntime`
for command tests. The file's own comment: "the future remote runtime
will back these same methods with generated gRPC clients." Contexts
(`cliconfig`, an HCL file with `mode = direct|remote`) parse a remote
context and return "not supported yet".

Two further design pieces are worth keeping as the blueprint for a
later hoomlab service API, if one is ever needed: DESIGN-0003's
`Operation` + `WatchOperation(after_sequence)` stream, where
`OperationEvent` carries the same `progress.Event` the CLI renders in
direct mode, so one renderer serves both; and its table mapping
`errorsx` classes to gRPC codes, HTTP codes and CLI exit codes. The
same portal that would front the UI would also be the HTTP face of
such an API (ADR-0017).

Verdicts: the transport is **PATTERN-only** (nothing to copy). The
`Runtime`/`Factory`/`Fake` seam and `NewRootCommand(factory)` are
**ADAPT**, and are INV-0003's recommendation 3 restated with the
current code. `cliconfig` is **ADAPT** (replace `AWSProfile` with a
PVE endpoint and a talosconfig context) when hoomlab has more than
one cluster to point at.

### Observation 4: the chicken-and-egg answer is a rule hoomlab already has

Platform's answer to "the service runs on a cluster it manages" is a
rule, not a mechanism:

- RFC-0001: "The platform runs on a management cluster, so it can't
  create that cluster itself." `platformctl` in bootstrap mode creates
  it, installs the baseline and GitOps tooling, and deploys the
  service; from then on the CLI is a client. "The platform never
  becomes the only way to recover itself."
- DESIGN-0003: "The management cluster stays CLI-owned. … The service
  reads it but never changes it, so it can never strand itself." The
  service creates and manages **spokes** only.
- DESIGN-0004: three writers with no overlap — the CLI via cloud
  APIs (tagged, never in git), the CLI via rendered files under
  `clusters/<c>/`, and people via `root.hcl`/`configs/`/`apps/`.
  "After bootstrap, Argo CD is the only writer of Kubernetes objects."

This is ADR-0001's rule — "Bootstrap runs outside the cluster it
builds, permanently" — with one sharpening hoomlab has not written
down: the service may *read* the cluster it runs on and *render* for
it, but mutating that cluster's substrate (PVE VMs, Talos machine
config) stays with the out-of-cluster CLI. For a one-cluster homelab
that means the hoomlab service's write scope is GitOps files and
other PVE resources, not its own nodes; node work (IMPL-0004, INV-0005
upgrades) belongs in `bootstrap`.

The **mechanics** of the handoff are the part worth copying:

1. Core components are rendered with the Helm SDK (template only, no
   release record) and server-side applied with a named field
   manager; Argo CD is installed from the same render its root
   Application later syncs, so the first sync is an empty diff and
   Argo adopts the fields, itself included. hoomlab's T3 note about
   removing the `cilium-install` Helm release record (INV-0003
   Observation 4) is this problem seen from the other side.
2. Until the operator commits the render, status reports
   `GitOpsSynced=False, Reason=PathNotFound` as a condition, not a
   failure.
3. Re-runs reapply core components only while Argo CD is not yet
   managing them (checked through the Application and field
   managers).

The packages behind it are cloud-agnostic and new since INV-0003:
`internal/charts` (266 lines; renders pinned HTTP or `oci://` charts
to ordered objects, CRDs then hooks then manifests, with a cached
`Fetcher`) and `internal/kubernetes/resources` (360 lines; SSA
applier with field manager and ownership labels, `ApplyAll` that
finds the kinds a just-applied CRD defines, wait-on-condition, and a
field-manager report). `argocd/install` (144) and the bearer-token
form of `argocd/registration` are generic too; `kubernetes/core` is
EKS-specific in its component list but is the shape of
"bootstrap installs, Argo adopts". Verdicts: `charts` and
`kubernetes/resources` **ADAPT** (high value for DESIGN-0002's
completion stage and the Phase 7 add-ons in IMPL-0004); the rule
**ADOPT** as an ADR-0001 amendment; hub-and-spoke stays **DEFER**.

### Observation 5: discovery and intent is a superset of steps, with a small portable core

The source of truth is platform's archived
`intent-discover-plan-reconcile-status-architecture.md` (821 lines,
Draft; §10 even sketches a Talos backend), implemented in
`go/platform/docs/design/0001-platformctl-cli.md`. The contracts:

| Stage | Answers | Writes | Output |
| --- | --- | --- | --- |
| Intent | What outcome was asked for? | No | Validated spec, incomplete on purpose |
| Discover | What exists; what can the environment support? | No | Facts with evidence, capabilities, findings |
| Plan | Given intent + facts + policy, what should happen? | No | Decisions with a reason each; digests of intent and discovery |
| Reconcile | How to move reality toward the plan safely? | Yes | Ordered idempotent steps, progress events |
| Status | What is true now? | No | Conditions; phase derived from them |

Compared with `tools/bootstrap/internal/steps` (112 lines: `Step{Name,
Check, Apply}`, `Runner{DryRun}`), platform's engine adds seven
things: discovery before planning, independent of steps; findings
that report every problem in one pass (hoomlab stops at the first
`Check` error); a pure, versioned planner with golden-plan tests;
per-resource `create / no-op / drift` with `FieldDiff{Path, Live,
Planned}`; an **ownership gate** before touching anything pre-existing
(owned / adopted / referenced / unknown, from tags; unknown is warned
about and never overwritten); conditions and a phase; and a one-shot
fingerprint check between "show the plan" and "confirm".

Discovery concretely: tags are the state (`managed-by`, `cluster`,
`config-hash` meaning "converged, skip the diff"); describe calls on
owned resources; evidence-carrying classification
(`network.ClassifySubnet` merges a tag, a route and a flag, and a
conflict yields role Unknown plus a finding that carries the
evidence); policy-ranked selection with a finding per rejected
option; a read-only Kubernetes health probe. Findings render as
`CODE severity message`, plans as a KIND / NAME / ACTION / CHANGES
table with up to three field diffs per row, and `-o json|yaml` is
versioned. A plan blocked by preflight prints only the findings.

**What is portable** (verified: no AWS imports):

| Package | Src lines | Verdict | hoomlab use |
| --- | --- | --- | --- |
| `finding` | 264 | ADOPT types; replace the 59 codes | `PVE0xx`, `TAL0xx`, `NET0xx`, `GIT0xx` codes as API contracts |
| `condition` | 75 | ADOPT struct, ADAPT constants | The service's status API; per-step conditions in `Runner` |
| `progress` | 43 | ADOPT | Replaces slog lines in `steps.Runner`; one renderer for CLI and a future API |
| `errorsx` + `cli/exitcode` | 163 + 72 | ADOPT | Error classes → hints → documented exit codes (INV-0003 rec. 4) |
| `texttable` | 54 | ADOPT (lipgloss) or `text/tabwriter` | The dry-run table |
| `cluster/hash.go` | 56 | ADOPT | Canonical JSON and config hashes |
| `bootstrap.Diff` + `leaves`/`flatten` | ~70 | ADOPT | JSON-pointer field diff; `Check` returns `Observation{Exists, Diff, Ownership}` instead of a bool |
| `preflight.runChecks`/`Blocking` | ~50 | ADOPT | Concurrent checks, sorted findings, error-severity blocks |
| `identity.classify` | ~40 of 130 | ADAPT | Ownership from PVE VM tags; closes INV-0003 Observation 8 (verified still open: `internal/pve` sets no tags) |
| `policy` | 1,020 | PATTERN | Versioned, embedded policy chosen by `policy_version` in HCL: Talos version, schematic, chart pins |
| `repoconf`, `environment` | 184, 106 | PATTERN | A repo-level `root.hcl` saying which environments may target what |
| `network`, `dns`, `baseline`, `planning` | ~2.6k | PATTERN | Facts → classify-with-evidence → findings; discovered facts rendered into GitOps |
| `reconcile`, `compute`, `ownership`, `cli/runtime`, `aws/*` | — | SKIP | Empty seam; EKS; Backstage tags; AWS wiring |

The PVE/Talos `Capabilities` this implies: PVE nodes, storage pools
and free space, bridges and VLANs, template and ISO presence,
existing VMs by name or tag, free VMIDs; Talos maintenance-mode vs
configured nodes, version, etcd membership; the GitOps tree's
missing/changed/stale files (`gitops/fsdiff`, 179 lines, already
models this). Whether hoomlab wants the full planner is a design
question for the service phase; the ADOPT rows are useful to
`bootstrap` today.

### Observation 6: the rest of platform-repo: utilities, test shapes, and a release path that stops at binaries

**Utilities** beyond Observation 5, all AWS-free: `retry` (70;
`Poll(ctx, Backoff, cond)` with jitter and a fake clock — INV-0003
rec. 4), `k8slabel` (41; the label checks `internal/config/labels.go`
reimplemented in Phase 4), `testfs.CopyDir` (71; symlink-following
copies for runfiles), `gitops/fsdiff` (179), `testutil/k3s.go` (43;
a testcontainers k3s `*rest.Config`). All imports point at
`platform-repo/go/platform/internal/...` and must be rewritten;
nothing needs more than Go 1.25.

**Test and dev-loop shapes** worth copying as patterns:

- `test/acceptance/` (1,895 lines, build-tagged): runs the real CLI
  binary through validate → plan → apply → status → tag check →
  no-op re-apply → refusal to destroy a protected cluster → destroy →
  sweep, with a cleanup hook that always destroys once apply has
  started, and steps declaring the capabilities they need so one
  suite runs against a real account or an emulator. This is the
  shape INV-0001's hardware drill and DESIGN-0003's nested test
  environment want.
- `scripts/gitops-smoke.sh` (k3d plus a local `git daemon`; Argo CD
  syncs the sample repo until Healthy) and `gitops-check.sh`
  (`gitops diff --ci`, kustomize, kubeconform with a pinned CRD
  catalog): an AWS-free smoke for the T3 handoff.
- `charts/platform`: same scaffold lineage as `charts/hoomlab`; take
  its `values_guard_test.yaml` (render fails if `extraEnv`,
  `configMap.data` or `secrets.stringData` shadow a chart-managed
  variable — hoomlab's `validateEnvCollisions` as a test) and the
  `ci/ci-values.yaml` busybox stand-in so `ct install` works before
  an image exists.
- `.claude/skills/release-label` (48 lines): a trimmed version fits
  hoomlab's `pr-labels.yml`. `import-project` and `bazel-hygiene`
  only matter if hoomlab adopts the monorepo shape.

**Release mechanics**: platform-repo has released exactly one thing,
`fwsync` binaries (the only `//go/<p>:release` target, verified).
Platform itself has **no release target on purpose**: adding one
would make the tag step tag a release that `publish.sh` cannot
publish, because Buildkite has no credential for a user account's
ghcr packages (`docs/deferred/platform-release.md`). Platform's last
release, v0.5.1 from the old repo, was goreleaser archives + a
`docker buildx bake` multi-arch distroless image with keyless cosign
and SLSA + the chart packaged at the tag version to
`oci://ghcr.io/donaldgifford/charts` — that is hoomlab's release
train, exactly. No images and no charts are published from
`platform-repo`; rules_oci "follows homelab when it adds them".

### Observation 7: homelab's own records say public repositories stay out

The question "should hoomlab move into homelab" is already answered
in homelab's documents, three ways, before any technical weighing:

- ADR-0062 (Buildkite with self-hosted agents) and DESIGN-0013 (the
  agent stack), Non-Goals: **"Public repositories. They keep
  GitHub-hosted runners."** — free for public repos.
- DESIGN-0016 (Go monorepo layout and import playbook), Non-Goals:
  **"Public repositories (docz, hclkit, repo-guardian, …) stay where
  they are; they are external dependencies."** ADR-0063 (single Go
  module under `go/`) lists as Neutral that public Go repositories
  remain separate and the monorepo is private by construction, and
  as Negative that `go install …@latest` stops working for moved
  repos — acceptable only because they were private.
- IMPL-0013 Phase 7 (2026-10-25): **"Not migrating now. These
  repositories, and the public ones in the wider scope (hoomlab, boop,
  repo-guardian and others), wait for a decision on how public
  repositories are handled, if they move at all."** Wave 3 waits on
  the same decision. No document records that decision. The only
  related note is a follow-up that a future public mirror (subtree
  split or Copybara) would need the history kept and the import path
  mapped back — nothing was built.

The one precedent of a public repository absorbed is keycloak-cli
(INV-0014 F2: "It is public, and ADR-0063 is about private
repositories. Nothing in homelab depends on that difference") — a
six-commit CLI with no image or chart, which stopped being public.

homelab's own view of hoomlab is consistent with this. It names
hoomlab as the **external owner** of the PVE API surface and the
Talos machine config (DESIGN-0028 "PVE cluster target state for the
hoomlab bootstrap", DESIGN-0029 "Bootstrap plane: booty and Caddy",
ADR-0002, and the "hoomlab owns Proxmox; Terraform never touches it"
line repeated across its RFCs and IMPLs); it supplies hoomlab's
inputs (host state from Ansible, booty on the bootstrap host, vault
items) and consumes its outputs (the `runtime.hoomlab.dev/kata` label
in its RuntimeClass). Its top-level `hoomlab/` directory is
**gitignored** — operator drill state and old booty trees, not code.
Its docz server ingests `donaldgifford/hoomlab` as a separate
repository. That is the relationship DESIGN-0016 prescribes for
public projects: "Releases are for everything outside homelab."

### Observation 8: what the move would give, and what it would break

Weighed anyway, in case the public-repository decision goes the other
way.

**It would give:** Bazel remote-cache CI with zero per-project step
code (test, Gazelle/tidy/contract check, golangci-lint, govulncheck,
trivy, trufflehog, nightly); label-driven `<p>/vX.Y.Z` releases with
stamped four-platform archives, SBOMs, GPG and keyless-cosign signed
checksums, no goreleaser; one Renovate stream and one lint config;
atomic changes beside the Ansible host inputs, booty config and k8s
manifests that reference hoomlab; and a tracked home for the drill
state.

**It would cost or break:**

| Area | What happens |
| --- | --- |
| Visibility | hoomlab becomes private: no public source, issues, PRs, `go install`, or public Releases. RFC-0001's "opinionated option for homelab … operators" has no audience. Keeping it public needs the unbuilt mirror |
| Image | Deleted at import (`.github/`, goreleaser, bake). No replacement: homelab's `MODULE.bazel` says "No rules_oci or rules_pkg: no Go project builds an image yet"; DESIGN-0017 D2/D3 (rules_oci to the in-cluster registry, amd64 only, nothing to ghcr) is unimplemented; IMPL-0013 Phase 5 "won't do" |
| Chart | No chart publishing, helm-unittest or ct step in the generated pipeline; DESIGN-0017 D5 (chart to the in-cluster OCI registry on release) unimplemented; its follow-up notes boilermate cannot read a chart by Git path |
| Lockstep binary + image + chart | Only the binary part exists. The two projects with hoomlab's shape (awsaccess, platform) were wave 2 — cancelled |
| Dropped checks | CodeQL (no Advanced Security on a private repo), dependabot/labeler, go-licenses, coverage gate, `-race` in CI |
| Nested module | `tools/bootstrap` folds into `go/go.mod`; its 73 requirements (Talos machinery, cosi, gRPC, proxmox-go-sdk, booty, hclkit) become everyone's versions by MVS, and every Talos bump re-tests every project. One command per project: `hoomlab` + `bootstrap` means two projects or boilermate work; `tools/bootstrap/main.go` moves to `cmd/<p>/` |
| Tags | `tools/bootstrap/vX.Y.Z` → `hoomlab/tools/bootstrap/vX.Y.Z`, which no longer matches the release pattern; history only |
| Docs | Every hoomlab ID collides with homelab's root series: renumber (pve-ansible precedent) or a second `.docz.yaml` (sdk-booty-sh precedent, which INV-0014 F4 disliked). The docz server's separate ingest changes |
| Tests | Integration and nested-PVE tiers need per-project steps that DESIGN-0015 D4 designed and nobody built |
| Import work | History secret-scan, path rewrite, shared-lint findings (keycloak-cli brought 61), and archiving the public repo |

### Observation 9: the precedent for "homelab's build" is a copy, not a merge

When platform needed homelab's build, it did not move into homelab.
`platform-repo` was created as a second private monorepo "laid out
and configured like homelab's" (platform-repo DESIGN-0001 D3, D9:
"Bazel workspace: copied from homelab", "Buildkite pipeline: copied
from homelab"), sharing homelab's remote cache, CI queue, CI image
and OpenBao roles, with homelab-side prerequisites listed as D12. The
shape that was copied is small: `MODULE.bazel`, `.bazelrc`,
`.bazelversion`, `.bazelignore`, the root `BUILD.bazel` with nogo,
`tools/bazel/`, `.buildkite/`, and the one-module `go/` layout.

Could hoomlab do the same while staying public? Mechanically yes, but
it would be against homelab's grain and with no benefit today:
homelab's agent stack is designed for its private repos (its Non-Goal
is explicit), the credentials flow through its vault, and the one
thing hoomlab's current train does that neither monorepo can — ship
an image and a chart — would still have to run on GitHub Actions.
Bazel's value is the dependency graph across many projects; hoomlab
has two small modules and a 71-file service tree.

What *is* worth taking from the monorepo shape, inside hoomlab's own
repo: the test-tier tags as Go build tags (unit / integration /
e2e / manual) so the acceptance suite of Observation 6 has a home;
`--latest=false` on tool releases; and the release-label skill.

<!--docz:findings:end-->

<!--docz:conclusion:start-->
## Conclusion

**Answer:** (1) yes, (2) no.

**(1) Yes, more than INV-0003 could offer — but still
ideas and a few thousand lines, not a dependency.** Confirmed with
one surprise. The portal and design system, deferred in INV-0003,
are now a liftable asset: ~700 lines of generic Go, ~1,050 to adapt
for a non-Okta issuer and a non-Valkey store, and a design system
whose only platform-specific parts are three blocks and the package
scope. The gRPC transport, as hypothesised, is a decision record;
the `Runtime` seam is the code. The chicken-and-egg answer is
ADR-0001's rule with one sharpening to write down. The intent engine
is a superset of `steps`, and its portable core (`finding`,
`condition`, `progress`, `errorsx`/`exitcode`, hashing, field diff,
the preflight runner, ownership classification) is what INV-0003
asked for, now with the ownership half present. The surprise is the
handoff mechanics: `charts` and `kubernetes/resources` are new,
cloud-agnostic, and fit DESIGN-0002's completion stage directly.

**(2) No, not now, and homelab says so itself.** homelab's
ADR-0062, DESIGN-0013 and DESIGN-0016 all exclude public
repositories; IMPL-0013 parked hoomlab by name pending a decision
that has not been made; the monorepo cannot build an image or publish
a chart; and homelab already models hoomlab as the external owner of
the PVE layer whose releases it consumes. The relationship is right
as it is. The one case for moving — the Bazel + Buildkite CI — has a
precedent (platform-repo) that copied homelab's shape rather than
merging, and even that repo cannot release what hoomlab releases.

<!--docz:conclusion:end-->

<!--docz:recommendation:start-->
## Recommendation

### The move

- **a. (recommended) hoomlab stays a public, standalone repository on
  GitHub Actions**, released as today; homelab consumes its releases
  (binary, chart, image) exactly as it consumes docz, boop and
  repo-guardian through its Kargo promotion pattern. Record this as
  an ADR-0001 amendment ("hoomlab is public and released
  independently; homelab is a consumer"), so IMPL-0004 Phase 8 and
  the service phase do not reopen it. Revisit only if homelab's
  public-repository decision is made *and* its image and chart path
  (DESIGN-0017 D2/D5) is built.
- b. Copy homelab's Bazel + Buildkite shape into hoomlab's own repo
  (the platform-repo precedent), keeping it public. Possible, no
  benefit at hoomlab's size, against homelab's stated Non-Goal, and
  still needs GitHub Actions for the image and chart.
- c. Move into homelab and go private, with a mirror later. Loses
  the public project; the mirror does not exist; the release train
  has no home.

### Reuse, in rough order of value

1. **Amend ADR-0001** with the service's write scope (Observation 4):
   the service reads the cluster it runs on and writes GitOps files
   and other PVE resources; node and VM mutation stays in
   `bootstrap`. One paragraph; it settles INV-0005's "who owns
   upgrades" question in favour of a `bootstrap` stage.
2. **The engine core into `tools/bootstrap`** (code, small, ~500
   lines copied with attribution): `finding`, `condition`,
   `progress`, `errorsx` + `exitcode`, `retry`, the canonical-hash
   helpers, `bootstrap.Diff`, `preflight.runChecks`/`Blocking`. Then
   `Check` returns an observation with a field diff and `--dry-run`
   prints KIND / NAME / ACTION / CHANGES. This is INV-0003's
   recommendations 4 and 5 with the exact files named.
3. **Ownership tags in PVE** (code, small): tag VMs and storage
   entries with the cluster identity, classify owned / adopted /
   unknown with `identity.classify`'s shape, refuse to touch unknown.
   INV-0003 recommendation 2, still open.
4. **`charts` + `kubernetes/resources`** (code, medium) for
   DESIGN-0002's completion stage and IMPL-0004 Phase 7's `cluster
   addons`: render pinned charts without a release record, SSA with
   a named field manager, wait on conditions, and let Argo CD adopt.
   Write the "bootstrap installs, Argo adopts, first sync is empty"
   contract into the T3 handoff.
5. **An acceptance suite shaped like `test/acceptance`** (test,
   medium), build-tagged, driving the real `bootstrap` binary against
   the mock PVE and the nested environment of DESIGN-0003, with
   per-step capability declarations. Adopt the four test-tier tags.
6. **The portal, when the service phase starts** (code, medium-large):
   lift `assets`/`server`/`problem` as-is; adapt `auth`/`session` for
   a generic OIDC issuer and a single-replica store; fork
   `@platform/ui` as `@hoomlab/ui` with the three domain blocks
   rewritten; keep the `contract.json` test; build the proxy from
   DESIGN-0006's spec; add a bun CI job from day one, since platform
   never did. Decide the issuer first — the homelab identity plane is
   the natural one.
7. **The `Runtime` seam and `cliconfig`** (code, small) when the
   service exists or a second cluster appears; before then, INV-0003's
   `NewRootCommand(factory)` is enough.
8. **Patterns, no code:** versioned embedded policy for Talos and
   chart pins; a repo-level `root.hcl`; the values-guard chart test
   and `ci-values.yaml`; the k3d + git-daemon GitOps smoke; the
   release-label skill trimmed to one project; `--latest=false` on
   tool releases.
9. **Skip:** Bazel, Buildkite, OpenBao and the monorepo release
   scripts; `internal/aws/*`, `cli/runtime`, `idp`/`okta`, `sandbox`,
   `devseed`, `kargo/promotion` (until hoomlab adopts Kargo), the
   showcase pages, and the saved-plan file.

### Housekeeping

- Amend INV-0003: snapshot date 2026-10-04; the plan-file note; the
  web UI verdict now points here. Mark it Concluded.
- When copying, rewrite the import path
  `github.com/donaldgifford/platform-repo/go/platform/internal/…` and
  keep an attribution comment, as INV-0003 recommended. Extract a
  shared library only when a third consumer appears.

<!--docz:recommendation:end-->

<!--docz:references:start-->
## References

- hoomlab: [RFC-0001](../rfc/0001-hoomlab-a-self-hosted-cloud-for-homelab-environments.md),
  [ADR-0001](../adr/0001-bootstrap-cli-and-service.md),
  [DESIGN-0002](../design/0002-talos-cluster-completion-scope-for-bootstrap.md) (T3),
  [DESIGN-0003](../design/0003-nested-test-environment-for-the-bootstrap-cli.md)
  (nested test environment),
  [IMPL-0004](../impl/0004-talos-11311-upgrade-with-kata-ci-isolation-and-cluster-options.md)
  (Phase 7 add-ons, Phase 8 close),
  [INV-0003](0003-platform-reuse-for-hoomlab.md),
  [INV-0005](0005-rolling-node-upgrades-managed-by-hoomlab.md)
- [platform-repo](https://github.com/donaldgifford/platform-repo) (private): `CLAUDE.md`,
  `go/platform/CLAUDE.md`, DESIGN-0001 (the monorepo), IMPL-0001
  (Phase 7 platform import, Phase 9 web), `docs/bazel-migration.md`,
  `docs/deferred/platform-release.md`,
  `docs/deferred/platform-ci-gates.md`,
  `docs/deferred/wiz-operator-release.md`
- platform, archived under [`platform-repo/docs/archived/platform/`](https://github.com/donaldgifford/platform-repo/tree/main/docs/archived/platform):
  RFC-0001 (bootstrap mode), ADR-0004, ADR-0005, ADR-0008, ADR-0017,
  DESIGN-0003 (service foundation), DESIGN-0004 (ownership, handoff),
  DESIGN-0006 (portal shell), DESIGN-0007 (spoke attach),
  DESIGN-0008 (SSO), DESIGN-0009 (design system), IMPL-0003 (H7),
  IMPL-0004 (U0–U4), IMPL-0007, IMPL-0008, IMPL-0009, INV-0001,
  INV-0006, INV-0007,
  `intent-discover-plan-reconcile-status-architecture.md`; in
  `go/platform/docs/`: DESIGN-0001 (platformctl), IMPL-0001
- platform code, under [`platform-repo/go/platform`](https://github.com/donaldgifford/platform-repo/tree/main/go/platform): `internal/cli/runtime/runtime.go`,
  `internal/cli/root.go`, `internal/cli/cliconfig`,
  `internal/bootstrap/{plan,apply}.go`, `internal/{finding,condition,
  progress,errorsx,retry,texttable,identity,policy,repoconf}`,
  `internal/charts`, `internal/kubernetes/resources`,
  `internal/argocd/install`, `internal/gitops/fsdiff`,
  `internal/portal/{assets,auth,session,server,problem}`,
  `web/platform/packages/ui`, `web/platform/apps/portal/src/shell`,
  `test/acceptance`, `scripts/gitops-smoke.sh`,
  `scripts/gitops-check.sh`
- [homelab](https://github.com/donaldgifford/homelab) (private; cited by ID and title only):
  ADR-0062 (Buildkite with self-hosted agents), ADR-0063 (Go projects
  as a single module under `go/`), DESIGN-0013 (Buildkite agent
  stack), DESIGN-0014 (Bazel workspace and remote cache), DESIGN-0015
  (generated Buildkite pipeline), DESIGN-0016 (Go monorepo layout and
  import playbook), DESIGN-0017 (release and deploy path), DESIGN-0021
  (a Bazel Go SDK for boilermate), DESIGN-0028 (PVE cluster target
  state for the hoomlab bootstrap), DESIGN-0029 (bootstrap plane:
  booty and Caddy), IMPL-0009, IMPL-0013 (migration waves; Phase 7),
  IMPL-0014, INV-0014 (monorepo beyond Go), `go/README.md`

<!--docz:references:end-->
