---
id: IMPL-0001
title: "Bootstrap CLI"
status: In Progress
author: Donald Gifford
created: 2026-08-20
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0001: Bootstrap CLI

<!--toc:start-->
- [Objective](#objective)
  - [Current status (2026-08-21)](#current-status-2026-08-21)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Config Schema and validate](#phase-1-config-schema-and-validate)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Step Engine and pve form](#phase-2-step-engine-and-pve-form)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: pve certs](#phase-3-pve-certs)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: talos secrets and talos emit](#phase-4-talos-secrets-and-talos-emit)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: talos ipxe and talos vms](#phase-5-talos-ipxe-and-talos-vms)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: talos bootstrap, talos health, and the Full Drill](#phase-6-talos-bootstrap-talos-health-and-the-full-drill)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Implement the bootstrap CLI in `tools/bootstrap` exactly as designed in
DESIGN-0001: an operator-run, stage-by-stage tool that takes bare Proxmox
nodes (PVE installed, reachable) to a formed PVE cluster with a healthy
Talos Kubernetes cluster on it, driven by a single HCL config file. Every
stage converges (check world → apply delta → safe to re-run), and the
final phase proves RFC-0001's success criterion with a full
bare-nodes-to-healthy-cluster drill.

**Implements:** DESIGN-0001 (Bootstrap CLI) — per ADR-0001, RFC-0001
Phase 1.

Phases follow DESIGN-0001's Migration / Rollout Plan in stage order.
Each phase lands as one or more PRs gated by `tools-ci.yml`
(`dont-release` label; the tool is built locally via
`just bootstrap-build` until `v0.1.0` is cut after the Phase 6 drill —
OQ-5).

### Current status (2026-08-21)

**Code complete through Phase 6.** Every command in the DESIGN-0001
tree is implemented, tested, and lint-clean: `validate`, `pve form`,
`pve certs`, `talos secrets|emit|ipxe|vms|bootstrap|health`, `version`.
`just bootstrap-lint` / `bootstrap-test` / `bootstrap-build` pass;
`govulncheck` is clean.

**What remains is hardware-gated and operator-run**, exactly as
DESIGN-0001 intended — none of it can be discharged from a workstation:

| Task | Phase | Why it needs the lab |
| --- | --- | --- |
| `pve form` nested-lab spot-check | 2 | needs three PVE nodes to form and join |
| The acceptance drill | 6 | bare nodes → healthy cluster on real hardware |
| Post-drill no-op re-run | 6 | the convergence property, proven against a real cluster |
| Record drill results | 6 | follows the drill |
| Cut `tools/bootstrap/v0.1.0` | 6 | gated on the drill passing (OQ-5) |

Phase 4's booty container smoke **is** now discharged: booty loads the
emitted catalog and serves `/boot.ipxe`, `/ipxe?mac=…`,
`/machine-config?mac=…`, and the boot assets correctly, with an
unconfigured MAC returning 404. That run also turned up five defects in
artifacts the test suite had been happily pinning — see INV-0001, which
tracks the remaining work and holds the drill results.

The procedure the drill follows is
[`docs/runbook/bootstrap-cluster.md`](../runbook/bootstrap-cluster.md);
`tools/bootstrap/README.md` remains the CLI's operator reference. Until
the drill runs, this document stays **In Progress** and DESIGN-0001
stays **Approved** rather than Implemented — the criteria they gate on
are empirical, not code review.

## Scope

### In Scope

- The `tools/bootstrap` module: `cmd/` cobra tree, `internal/config`,
  `internal/steps`, `internal/pve`, `internal/talos`, `internal/emit`.
- All commands in the DESIGN-0001 command tree: `validate`,
  `pve form`, `pve certs`, `talos secrets`, `talos emit`, `talos ipxe`,
  `talos vms`, `talos bootstrap`, `talos health`, `version`.
- Global flags: `--config`, `--output`, `--secrets`, `--dry-run`,
  `--log-level` (flag/config split per DESIGN-0001 OQ-8).
- Emitted artifacts: booty catalog + templates overlay + `embed.ipxe` +
  `booty-run.sh`, Image Factory boot-asset downloads, the containerized
  `ipxe.efi` build, `talosconfig`/`kubeconfig`.
- Tests: config table tests, mockpve convergence/interruption tests,
  golden-file emission tests with in-process booty validation, mocked
  Talos client tests, and the Phase 6 real-lab drill.
- An operator runbook (`docs/runbook/bootstrap-cluster.md`), with
  `tools/bootstrap/README.md` as the CLI's own reference.

### Out of Scope

- Everything DESIGN-0001 lists as a non-goal: PVE OS installs, the
  Hoomlab service/database/state files, an unattended meta-command
  (OQ-7), day-2 management.
- Changes to booty, hclkit, or proxmox-go-sdk — this consumes released
  versions only. Gaps found here become upstream issues, not forks.
- The root `hoomlab` scaffold, chart, and release train.
- Releases before the Phase 6 drill — `v0.1.0` is cut only after the
  drill passes (OQ-5); the mechanism (`tools-release.yml`) already
  exists.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Every phase
ships usable on its own (DESIGN-0001 rollout plan) — a phase's commands
work end-to-end against their targets before the next phase starts.

---

### Phase 1: Config Schema and `validate`

`internal/config` + the `validate` command: the schema every later phase
consumes, the `env()` secrets mechanism, and the validators that make
config errors fail loudly before any API is touched.

#### Tasks

- [x] Define gohcl-tagged schema structs in `internal/config` mirroring
      the DESIGN-0001 Data Model: `cluster` label block containing `pve`
      (with labeled `node` blocks: `endpoint`, `address`, `primary`),
      `acme` (`email`, `domain`, `dns`, `token`), and `talos`
      (`version`, `endpoint`, optional `schematic_id` (OQ-1),
      `booty { url, version }` with `version` optional (OQ-4), labeled
      `node` blocks: `role`, `pve_node`, `vmid`, `mac`, `cores`,
      `memory`, `disk_gb`, `storage`, `bridge`)
- [x] Implement the `env(name)` function and register it via
      `hclkit.WithFunctions`; an unset/empty variable produces a
      diagnostic naming the variable, never a silent empty string
- [x] Implement the loader:
      `hclkit.New(WithFunctions(env), WithValidators(...)).LoadFile(path, &cfg)`
      with hclkit's GCC-style diagnostics rendered to stderr
- [x] Implement validators: MAC well-formed + normalized (single
      canonical form shared with `internal/emit` later) + unique across
      all `talos.node` blocks; VMID unique; exactly one `pve.node` with
      `primary = true`; `role` is `controlplane` or `worker` with at
      least one controlplane; every `talos.node.pve_node` names a
      declared `pve.node`; `endpoint`/`booty.url` parse as URLs
- [x] Wire `bootstrap validate` (cobra) to loader + validators; exit
      non-zero when any diagnostic is an error
- [x] Add an annotated example config at
      `tools/bootstrap/examples/bootstrap.hcl` (doubles as a test
      fixture)
- [x] Add `.gitignore` entries for the operator-owned files:
      `secrets.yaml`, `bootstrap-out/`
- [x] Write table tests over HCL fixtures: the valid example, one
      fixture per validator failure, `env()` present/missing/empty;
      assert rendered diagnostic text names the offending field or
      variable

#### Success Criteria

- `just bootstrap-test` and `just bootstrap-lint` pass; `tools-ci.yml`
  is green on the PR
- With the referenced `HOOMLAB_*` variables exported,
  `bootstrap validate --config examples/bootstrap.hcl` exits 0
- Each seeded failure (missing env var, duplicate MAC, duplicate VMID,
  zero/two primaries, unknown `pve_node`, bad role) exits non-zero with
  a diagnostic naming the field or variable — asserted in tests, not
  eyeballed
- No secret value appears in any output path (diagnostics included) —
  asserted in tests

---

### Phase 2: Step Engine and `pve form`

`internal/steps` — the whole convergence engine — plus the first real
stage: cluster formation, adapted from pvelab's `FormCluster` (copied
as functional documentation, never imported) and proven convergent
against mockpve.

#### Tasks

- [x] Implement `internal/steps`: `Step{Name, Check(ctx) (bool, error),
      Apply(ctx) error}` and a runner that checks each step, skips done
      ones, applies pending ones in order, logs progress via `slog`,
      and wraps failures with the step name (`%w`)
- [x] Implement `--dry-run` in the runner: print the pending-step list
      and stop — zero `Apply` calls, zero write requests
- [x] Add `github.com/donaldgifford/proxmox-go-sdk@v0.11.0` to
      `tools/bootstrap/go.mod`
- [x] Implement per-node client construction in `internal/pve` from the
      config (`proxmox.NewClient` with `api.TokenCredentials` from the
      resolved `env()` values; joining-node dials use root@pam
      credentials — API tokens do not survive a join, root@pam does)
- [x] Implement formation steps per DESIGN-0001 Stage 1:
      create-cluster on the primary (`CreateCluster` fire-and-poll on
      `ListConfigNodes`), then for each remaining node **serially**:
      `JoinInfo` fingerprint from the primary → `JoinCluster` issued on
      the joining node with the root@pam password → wait for membership
      → wait for quorum (pvelab's `waitForMember`/`waitForQuorum`
      pattern); corosync `link0` via `JoinSpec.Extra` when `address` is
      set; a final cluster-quorate step verifies the formed cluster on
      re-runs
- [x] Bound every apply behind its convergence signal: formation
      writes are fire-and-poll per the SDK contract (no UPID comes
      back), so membership/quorum polls are the "task wait" here;
      `tasks.Ref` waits enter with the Stage 2+ writes that do return
      UPIDs
- [x] Wire `bootstrap pve form` with `--dry-run`
- [x] Write mockpve tests: fresh 3-node formation converges; a second
      run reports every step done with zero write requests; an
      interruption matrix (stop the runner after each step boundary,
      re-run the full stage, assert convergence) — table-driven over
      the boundaries
- [ ] Spot-check `pve form` against a nested pvelab-style 3-node lab
      (OQ-2): formation, quorum, and a convergent re-run on real PVE
      *(operator-run: needs the lab hardware; everything up to it is
      mockpve-verified)*

#### Success Criteria

- `pve form` against seeded mockpve forms the cluster; the immediate
  re-run is a full no-op (asserted via mockpve request accounting)
- The interruption matrix passes for every step boundary — re-run
  converges from any partial state
- `--dry-run` performs zero write calls (asserted, not assumed)
- The nested-lab spot-check completes: cluster formed, quorate, re-run
  a no-op
- `just bootstrap-test` / `tools-ci.yml` green

---

### Phase 3: `pve certs`

The ACME stage on proxmox-go-sdk v0.11.0's plugin API: account,
Cloudflare DNS-01 plugin, per-node domain config, and expiry-driven
certificate ordering — all as convergent steps.

#### Tasks

- [x] Implement the ACME account step: `RegisterACMEAccount` from
      `acme.email` + directory (new optional `acme.directory` config
      field for the staging CA; TOS accepted from `GetACMEMeta` rather
      than a hardcoded URL); `Check` via `ListACMEAccounts`
- [x] Implement the Cloudflare plugin step: `CreateACMEPlugin` with the
      typed `ACMECloudflare` provider (scoped token from
      `env("HOOMLAB_CLOUDFLARE_API_TOKEN")`); `Check` via
      `GetACMEPlugin` comparing the stored payload against the
      config's encoding, so a rotated token reopens the step; drifted
      plugin config → `UpdateACMEPlugin`
- [x] Implement the per-node domain step: `SetNodeConfig` with an
      `ACMEDomain` entry for `<node>.<domain>`; `Check` via
      `GetNodeConfig`
- [x] Implement the per-node certificate step: `OrderNodeCertificate` +
      task wait; `Check` via `GetNodeCertificates` — done when an ACME
      cert covers the node FQDN with expiry beyond a renewal threshold
      (default 30 days), so renewal is the same command re-run
- [x] Wire `bootstrap pve certs` with `--dry-run`
- [x] Write mockpve tests using the native ACME routes: fresh run,
      idempotent re-run, drifted plugin update, near-expiry cert marks
      the order step pending
- [x] Write a redaction test: the Cloudflare token never appears in
      logs, step output, or error text (the SDK's redacting `String()`
      plus our own output paths)

#### Success Criteria

- `pve certs` on mockpve: fresh run applies all steps, re-run is a
  no-op, a near-expiry certificate triggers exactly the order step
- Redaction test passes — no token bytes in any captured output
- `just bootstrap-test` / `tools-ci.yml` green

---

### Phase 4: `talos secrets` and `talos emit`

The artifact stage: the once-only secrets bundle, then the full booty
tree — catalog, secret-bearing machineconfig templates, `embed.ipxe`,
`booty-run.sh`, and Image Factory boot assets — golden-file tested and
validated with booty's and machinery's own loaders.

#### Tasks

- [x] Add `github.com/siderolabs/talos/pkg/machinery` to
      `tools/bootstrap/go.mod`
- [x] Implement `bootstrap talos secrets`: generate a machinery secrets
      bundle and write it to `--secrets`; if the file exists, no-op
      with a message — **never overwrite** (DESIGN-0001 OQ-2)
- [x] Implement catalog emission in `internal/emit`:
      `00-variables.hcl` (cluster name, talos version, endpoint,
      boot base), `10-profiles.hcl` (`talos-control`/`talos-worker`
      with the mandatory metal cmdline
      `talos.platform=metal init_on_alloc=1 slab_nomerge pti=on` and
      `$${mac}` HCL escaping), `20-groups.hcl` (one group per node,
      `selector = { mac = … }` using the canonical MAC form from
      Phase 1)
      — every emitted variable is load-bearing, so the cluster name and
      endpoint live in the header comment and the baked machineconfig
      rather than in variables nothing reads
- [x] Implement machineconfig template emission: machinery config
      generation seeded by the secrets bundle → per-role templates
      under `templates/talos/{controlplane,worker}.yaml.tmpl` (family
      subdir mandatory) with exactly the two overlay edits from booty's
      walkthrough — hostname var and install-image var (OQ-1a)
- [x] Validate generated machineconfigs with machinery
      `Validate(ModeMetal)` before writing — machinery ships the
      `validation.RuntimeMode` interface but not the modes themselves
      (they live in the Talos runtime), so `internal/talos` carries a
      three-method `metalMode` mirroring `runtime.ModeMetal`
- [x] Implement `embed.ipxe` emission (three-line chain script, URL
      derived from `talos.booty.url`) — rendered through booty's own
      `Renderer.ChainScript` rather than a copied script, so the
      embedded chain and the one booty serves at `/boot.ipxe` cannot
      drift apart
- [x] Implement `booty-run.sh` emission encoding the sharp edges:
      `--net=host`, port-capable user, catalog/templates/boot mounts,
      the correct `--catalog` flag, `--proxydhcp --server-ip` from
      config, and the booty image pinned to `booty.version` —
      defaulting to the tested-against constant in the CLI (OQ-4)
- [x] Implement Image Factory asset download into
      `booty/boot/talos/<version>/` (`vmlinuz`, `initramfs.xz`):
      checksum-verified, skipped when already present (DESIGN-0001
      OQ-6a); schematic from the config's optional `schematic_id`,
      defaulting to the vanilla no-extensions schematic for the
      configured version (OQ-1)
      — the factory publishes no authoritative checksum, so integrity
      rests on an atomic temp-file rename plus a Content-Length check
      (a truncated transfer leaves nothing behind) and a
      trust-on-first-use `.sha256` sidecar that catches any change
      after staging; authenticity on the first fetch is TLS
- [x] Wire `bootstrap talos emit`: `Check` is a byte-diff of the
      on-disk tree against a fresh render; when anything changed, the
      step output ends with "restart the booty container"
- [x] Write golden-file tests for the entire emitted tree; byte-stable
      output is a test invariant (two renders identical) — the catalog,
      `embed.ipxe`, and `booty-run.sh` are golden-pinned under
      `internal/emit/testdata/golden` (`-update` regenerates); the
      machineconfig templates carry fresh secrets and are pinned by the
      contract test below instead
- [x] Write in-process booty contract tests: `catalog.DirSource.Load`
      over the emitted catalog, and a `Renderer.Config` dry-render for
      a synthetic node identity → parsed and re-validated with
      `Validate(ModeMetal)`

#### Success Criteria

- Two consecutive `talos emit` runs produce byte-identical trees; the
  second run's diff-`Check` reports nothing to do
- The emitted catalog loads through booty's own loader with zero
  diagnostics; the dry-rendered machineconfig for a synthetic MAC
  passes `Validate(ModeMetal)`
- `talos secrets` refuses to overwrite an existing bundle (tested)
- Manual smoke (documented in the runbook, not CI — OQ-3): a real
  booty container serves `/boot.ipxe`, `/ipxe?mac=…`, and
  `/machine-config?mac=…` from the emitted tree — **met**. booty loaded
  the emitted catalog (`profiles=2 groups=4`), matched `cp-01` by MAC
  to `talos-control`, served a `type: controlplane` /
  `hostname: cp-01` machineconfig with no template expressions left,
  served the worker its own, returned 404 for an unconfigured MAC, and
  served both boot assets at full length. Running it also exposed five
  defects in the emitted artifacts (INV-0001) that no test could see,
  because the tests stub the container runtime and pin the emitted
  text. The PXE handshake itself — proxyDHCP, TFTP, chainloading
  `ipxe.efi` — still needs a machine and lands in the drill.
- `just bootstrap-test` / `tools-ci.yml` green

---

### Phase 5: `talos ipxe` and `talos vms`

The boot-path binary and the VMs that consume it: booty's containerized
iPXE build wired into the convergence model, and VM creation with every
load-bearing Proxmox setting encoded and asserted.

#### Tasks

- [x] Implement `bootstrap talos ipxe`: run booty's containerized iPXE
      build via docker, dropping `booty/boot/ipxe.efi`; the step is
      pending only when the binary is missing or the on-disk
      `embed.ipxe` differs from a fresh render (OQ-9a — a changed
      `booty.url` triggers the rebuild; nothing else does)
      — **two deviations, both deliberate:**
      1. booty v0.2.1 ships *no* containerized iPXE build (its docs
         give the manual `make -C ipxe/src … EMBED=…` from an iPXE
         checkout). The CLI therefore runs that same build itself in a
         pinned `debian:bookworm-slim` container against a pinned iPXE
         ref, mounting the boot dir writable and `embed.ipxe`
         read-only. If booty later ships a builder image, switch to it.
      2. The convergence check compares the fresh render against a
         `boot/ipxe.efi.embed.sha256` stamp written at build time, not
         against the on-disk `embed.ipxe`. Comparing to the file
         literally can never trigger: `talos emit` runs first and
         rewrites `embed.ipxe`, so by the time `talos ipxe` looks, disk
         and render always agree and the rebuild would never fire. The
         embedded script is not readable back out of a compiled binary,
         so the stamp is the only way to distinguish "built from this
         URL" from "built from the previous one" — which is the stated
         intent (OQ-9a). Running `talos ipxe` against an `embed.ipxe`
         that is stale or missing is refused with a pointer to
         `talos emit` rather than baking in a wrong URL.
- [x] Implement VM creation steps in `internal/pve` per DESIGN-0001
      Stage 4: per-node `qemu.NewService(client, pveNode, caps)` and
      `Create` with the full load-bearing spec — `VMID`, `Name`,
      `Memory`, `Cores`, `SCSI0` (`<storage>:<disk_gb>`), `Net0`
      (`virtio,bridge=…,macaddr=<config MAC>,firewall=0`),
      `Boot: "order=scsi0;net0"` (disk first, PXE fallback), `Extra`:
      `bios=ovmf`, `efidisk0` **without** pre-enrolled Secure Boot keys
      (`pre-enrolled-keys=0`), `machine=q35`, `cpu=host`, VirtIO
      `rng0`, `serial0`
- [x] Implement the start step with task waits; `Check` via `qemu.Get`
      on the VMID at the target node (exists + running)
- [x] Wire `bootstrap talos vms` with `--dry-run`
- [x] Write mockpve tests: fresh create + start for all nodes;
      idempotent second run; partial-exists convergence (subset of VMs
      pre-seeded); a spec-assertion test proving every load-bearing
      field above lands on the wire exactly as designed
- [x] Write `talos ipxe` unit tests for the rebuild trigger: unchanged
      `embed.ipxe` + present binary ⇒ done; changed URL or missing
      binary ⇒ pending (docker invocation stubbed; the real build runs
      in the Phase 6 drill, not CI — OQ-3)

#### Success Criteria

- `talos vms` on mockpve: fresh run creates and starts every configured
  VM; re-run is a no-op; partial-exists runs create only the missing
  VMs
- The spec-assertion test pins all load-bearing settings — a regression
  in any of them (`order=scsi0;net0`, `pre-enrolled-keys=0`, `rng0`,
  `cpu=host`, `firewall=0`, MAC) fails the build
- `talos ipxe` is a no-op when `embed.ipxe` is unchanged and the binary
  exists
- `just bootstrap-test` / `tools-ci.yml` green

---

### Phase 6: `talos bootstrap`, `talos health`, and the Full Drill

The last mile — etcd bootstrap, cluster credentials, health — then the
acceptance test for the whole design: a full bare-nodes-to-healthy-
cluster drill on the real lab, re-run to prove end-to-end convergence.

#### Tasks

- [x] Define a narrow interface over the Talos client operations the
      CLI needs (bootstrap, kubeconfig/talosconfig retrieval, health);
      generate mockery v3 mocks — `internal/talos.Client`
      (Bootstrap/Kubeconfig/Health/Close), mocks in
      `internal/talos/mocks` via `.mockery.yml`
      (`mise x -- mockery` regenerates). Talosconfig generation needs
      no cluster, so it is a plain function, not an interface method.
      The health stream translation (progress messages, EOF-as-success,
      server errors) is additionally tested against a real
      ClusterService gRPC server on a unix socket rather than a mock
- [x] Implement `bootstrap talos bootstrap`: one-time etcd bootstrap
      against the first control-plane node — "already bootstrapped" is
      success (idempotency per DESIGN-0001); then write
      `<output>/out/talosconfig` and `<output>/out/kubeconfig`
      — "first control-plane node" resolves to the `talos.endpoint`
      host (the config pins no per-VM IPs; the runbook requires a DHCP
      reservation making the endpoint the first CP). Already-
      bootstrapped detection is gRPC FailedPrecondition/AlreadyExists.
      Both credential files are skip-if-present (their certs are
      freshly generated per call, so a byte-diff would always differ,
      and rotating working operator credentials on re-run would be
      wrong); the etcd step's Check probes with a Kubeconfig fetch,
      which only succeeds on a bootstrapped, serving cluster
- [x] Implement `bootstrap talos health`: block until the cluster
      reports healthy (bounded, configurable wait), usable as the
      standalone verification command — `--wait` (default 10m) becomes
      the server-side health-check wait; progress streams to the log
- [x] Write mock tests: first-call bootstrap sequencing,
      already-bootstrapped tolerance, health wait success/timeout
      (plus: real-failure stop, no-op re-run, never-overwrite
      credentials, dry-run touching nothing; health success/timeout run
      against a real in-process gRPC ClusterService)
- [x] Write the operator runbook
      (`docs/runbook/bootstrap-cluster.md`, with
      `tools/bootstrap/README.md` as the CLI reference): the full stage
      flow from DESIGN-0001's command tree, required `HOOMLAB_*`
      variables, prerequisites (trusted/isolated boot network, booty
      host with docker, moving the emitted tree and secrets bundle),
      and the "restart booty after re-emit" rule — includes the
      DHCP-reservation prerequisite (endpoint host must be the first
      control-plane node), the convergence re-run check, per-step
      expected output taken from the code, and a troubleshooting
      section covering the failure modes found so far. Steps that need
      the lab are marked unverified rather than implying confidence
      nobody has earned.
- [ ] Run the full drill on the homelab hardware (OQ-2; nested-lab
      rehearsals first as needed): bare PVE nodes → `validate →
      pve form → pve certs → talos secrets → talos emit → talos ipxe →
      [start booty] → talos vms → talos bootstrap → talos health` —
      executed from the runbook, not from memory
- [ ] Re-run every stage after the drill and confirm the full pass is a
      no-op (the "take ownership converges on no-op" property the
      Hoomlab service will later rely on)
- [ ] Record drill results and deviations in INV-0001; fold any fixes
      back into code and docs — INV-0001 is prepared with a 12-step
      result table, a per-stage convergence table, and a deviations
      table awaiting the run
- [ ] Cut `tools/bootstrap/v0.1.0` via `tools-release.yml` once the
      drill and the no-op re-run have passed (OQ-5)

#### Success Criteria

- `kubectl get nodes` via the emitted kubeconfig shows every configured
  node `Ready` — RFC-0001's Phase 1 success criterion, demonstrated
- The post-drill full re-run reports every step of every stage as done,
  with zero writes
- Mock tests green; `just bootstrap-test` / `tools-ci.yml` green
- The runbook alone was sufficient to drive the drill
- `tools/bootstrap/v0.1.0` is tagged and released via
  `tools-release.yml` (OQ-5)
- DESIGN-0001 moves to **Implemented**; this document moves to
  **Completed**

---

## File Changes

| File | Action | Description |
| ---- | ------ | ----------- |
| `tools/bootstrap/cmd/*.go` | Create | cobra tree: `validate`, `pve form/certs`, `talos secrets/emit/ipxe/vms/bootstrap/health`; global flags |
| `tools/bootstrap/internal/config/` | Create | gohcl schema structs, `env()` function, hclkit loader, validators (Phase 1) |
| `tools/bootstrap/internal/steps/` | Create | `Step`/runner convergence engine + dry-run printer (Phase 2) |
| `tools/bootstrap/internal/pve/` | Create | clients, formation steps, ACME steps, VM steps (Phases 2, 3, 5) |
| `tools/bootstrap/internal/emit/` | Create | catalog/templates/`embed.ipxe`/`booty-run.sh` writers, Image Factory downloads, tree differ (Phase 4) |
| `tools/bootstrap/internal/talos/` | Create | secrets bundle, machineconfig generation, bootstrap/health client wrap + mocks (Phases 4, 6) |
| `tools/bootstrap/examples/bootstrap.hcl` | Create | annotated example config, doubles as test fixture (Phase 1) |
| `tools/bootstrap/internal/emit/testdata/` | Create | golden files for the emitted tree (Phase 4) |
| `tools/bootstrap/README.md` | Create | operator runbook (Phase 6) |
| `tools/bootstrap/go.mod` | Modify | add proxmox-go-sdk ≥ v0.11.0, machinery, booty (test), mock deps |
| `.gitignore` | Modify | `secrets.yaml`, `bootstrap-out/` (Phase 1) |

## Testing Plan

Per DESIGN-0001's Testing Strategy, distributed across the phases:

- [x] `internal/config`: table tests over HCL fixtures — valid config,
      every validator failure, `env()` present/missing/empty, rendered
      diagnostics asserted (Phase 1)
- [x] `internal/steps` + `internal/pve`: mockpve end-to-end — fresh
      runs, no-op re-runs, interruption matrices, dry-run
      zero-write assertions, task-waiter paths, ACME seeding via
      `AddACMEPlugin`, VM spec assertions (Phases 2, 3, 5)
- [x] Secret redaction: no token/password bytes in any captured output
      (Phases 1, 3)
- [x] `internal/emit`: golden files, byte-stability invariant,
      in-process booty catalog load + dry-render, machinery
      `Validate(ModeMetal)` (Phase 4)
- [x] `internal/talos`: mockery v3 mocks for bootstrap/health
      sequencing (Phase 6) — bootstrap sequencing is mock-driven; the
      health stream runs against a real in-process ClusterService gRPC
      server instead, because streaming semantics (EOF-as-success,
      server-side deadline) are the thing a mock would assume rather
      than verify
- [x] Filesystem-touching tests use `t.TempDir()`; no test writes into
      the repo tree — the one exception is `-update`, which rewrites
      `internal/emit/testdata/golden` on demand
- [ ] e2e: the Phase 6 real-lab drill — deliberately not a merge gate
      (per DESIGN-0001), recorded in an INV/runbook appendix

## Dependencies

- `github.com/donaldgifford/proxmox-go-sdk` **≥ v0.11.0** — shipped and
  verified 2026-08-20; brings the ACME plugin API, `ACMECloudflare`,
  node config, qemu service, `mockpve`
- `github.com/donaldgifford/hclkit` — loader, `WithFunctions`,
  `WithValidators`, diagnostics
- `github.com/siderolabs/talos/pkg/machinery` (+ Talos client) —
  secrets bundle, config generation, `Validate(ModeMetal)`, bootstrap,
  health
- `github.com/donaldgifford/booty` — test-only Go import (catalog
  loader, renderer) plus the container + iPXE build image at runtime
- `mockery` v3 (mise) for Phase 6 mocks; docker on the operator machine
  for `talos ipxe` and the booty container
- A nested pvelab-style lab for the Phase 2 spot-check and drill
  rehearsals; the homelab hardware for the final Phase 6 drill (OQ-2)
- Existing repo plumbing: `tools-ci.yml`, `tools.just` recipes,
  `tools-release.yml` (`v0.1.0` cut after the Phase 6 drill — OQ-5)

## Open Questions

Format: **a** is my recommendation; **b**+ are alternatives; fill in
**other** to override with something not listed. All five were decided
**a** on 2026-08-20 and are folded into the phase tasks above.

**OQ-1 — Image Factory scope for v1: how is the schematic determined?**
DESIGN-0001 OQ-6 decided `talos emit` downloads prebuilt assets from
the Image Factory, keyed by schematic ID + version — but not where the
schematic ID comes from.

**Decided: a** (2026-08-20). An explicit optional `schematic_id` field
in the `talos` config block, defaulting to the well-known vanilla (no
extensions) schematic for the configured version. v1 does no schematic
creation; an operator who wants extensions creates the schematic at
factory.talos.dev and pastes the ID. Config stays the single source of
truth, the CLI stays a downloader, and the field is service-consumable
later (DESIGN-0001 OQ-8 rule).

- ~~b: config declares extensions and `talos emit` POSTs the schematic
  to the factory API~~
- ~~c: vanilla images only, no field~~

**OQ-2 — What lab verifies the real-PVE phases?** Phase 2 calls for
"mockpve, then a real 3-node lab"; Phase 6 is the full drill.

**Decided: a** (2026-08-20). A pvelab-style nested lab (virtualized PVE
nodes) for the Phase 2 formation spot-check and drill rehearsals; the
real homelab hardware only for the final Phase 6 drill. Cheap resets
while iterating, and the hardware run stays meaningful as the
acceptance test.

- ~~b: real hardware from Phase 2 onward~~
- ~~c: mockpve only until Phase 6~~

**OQ-3 — How much of the docker-dependent path runs in CI?**
`talos ipxe` (containerized iPXE build) and the booty container smoke
need docker and minutes of runtime; `tools-ci.yml` currently runs
lint/test/build/govulncheck.

**Decided: a** (2026-08-20). CI unit-tests the logic only (embed-script
rendering, rebuild-trigger decisions, `booty-run.sh` content); the real
iPXE build and booty smoke run in the Phase 6 drill and are documented
in the runbook. Keeps CI fast and hermetic; the drill covers the real
path before anything is released.

- ~~b: on-demand/scheduled workflow job for the real build + smoke~~
- ~~c: real build in every `tools-ci.yml` run~~

**OQ-4 — How is the booty container version pinned in `booty-run.sh`?**
The emitted run script names a booty image; DESIGN-0001 doesn't say
which tag.

**Decided: a** (2026-08-20). An optional `booty { version }` config
field defaulting to a constant in the CLI — the booty release this CLI
was developed and tested against. Reproducible by default, overridable
in config (and the field is config-worthy by the OQ-8 rule: the service
will care which booty it manages).

- ~~b: require the field explicitly~~
- ~~c: emit `latest`~~

**OQ-5 — When is the first `tools/bootstrap` release cut?**
`tools-release.yml` (dispatch, `tools/bootstrap/vX.Y.Z` tags) already
exists; until then the tool is `just bootstrap-build` local-only.

**Decided: a** (2026-08-20). Cut `v0.1.0` after the Phase 6 drill
passes — a release asserts "this took a bare lab to a healthy cluster",
and nothing earlier can honestly claim that. Phases 1–5 stay local
builds. The release cut is the closing task of Phase 6.

- ~~b: milestone releases at the end of each phase~~

## References

- DESIGN-0001: Bootstrap CLI — the design this implements (all OQ-1–9
  decisions recorded there)
- ADR-0001: Bootstrap CLI — placement (`tools/bootstrap`), CLI-only
  delivery, "re-runs must converge"
- RFC-0001: Hoomlab — Phase 1 success criterion (the Phase 6 drill)
- `github.com/donaldgifford/proxmox-go-sdk` v0.11.0 — `proxmox`
  package, `mockpve`, `cmd/pvelab/lab` (`FormCluster`, functional
  documentation to copy from)
- `github.com/donaldgifford/booty` — catalog contract, templates
  overlay, serve flags, containerized iPXE build,
  `docs/go-ipxe/10-talos-overlay-walkthrough.md`
- `github.com/donaldgifford/hclkit` — `pkg/hclkit` loader
- `github.com/siderolabs/talos/pkg/machinery` — secrets bundle, config
  generation, validation
- `.github/workflows/tools-ci.yml`, `.github/workflows/tools-release.yml`,
  `tools.just` — the CI/release plumbing the phases ride on
