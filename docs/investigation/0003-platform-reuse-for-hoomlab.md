---
id: INV-0003
title: "Platform reuse for hoomlab"
status: In Progress
author: Donald Gifford
created: 2026-10-10
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0003: Platform reuse for hoomlab

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1: the visions share a spine; the substrate and scale differ](#observation-1-the-visions-share-a-spine-the-substrate-and-scale-differ)
  - [Observation 2: little platform code is AWS-free, but what is, is useful](#observation-2-little-platform-code-is-aws-free-but-what-is-is-useful)
  - [Observation 3: the engine models agree; platform's adds output, not truth](#observation-3-the-engine-models-agree-platforms-adds-output-not-truth)
  - [Observation 4: platform's GitOps came from the homelab, and T3 already exists](#observation-4-platforms-gitops-came-from-the-homelab-and-t3-already-exists)
  - [Observation 5: the service foundation is design, not code](#observation-5-the-service-foundation-is-design-not-code)
  - [Observation 6: Temporal does not fit hoomlab now](#observation-6-temporal-does-not-fit-hoomlab-now)
  - [Observation 7: sandboxes map to PVE, but nothing asks for them yet](#observation-7-sandboxes-map-to-pve-but-nothing-asks-for-them-yet)
  - [Observation 8: ownership tags are hoomlab's most concrete gap](#observation-8-ownership-tags-are-hoomlabs-most-concrete-gap)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

How much of the `platform` repo — its ideas, its designs, and its Go
code — can hoomlab reuse, given that platform targets AWS (EKS,
Lambda MicroVMs, RDS) and hoomlab targets Proxmox VE + Talos on a
homelab? Which parts are taken as-is, which are adapted with the AWS
pieces swapped for PVE/Talos equivalents, which are deferred to
hoomlab's service phase, and which don't fit at all?

## Hypothesis

Same author, same instincts: the ideas will transfer well, the code
much less, because platform's code is organized around AWS resource
types. Expect a handful of generic utilities, the GitOps approach, and
a lot of design to carry over; expect the AWS planners and adapters to
be useless here.

## Context

hoomlab has a working bootstrap CLI (DESIGN-0001, DESIGN-0002,
DESIGN-0004 — Implemented) and a stub service (`cmd/hoomlab`). Its
next phases are the GitOps handoff (DESIGN-0002's T3) and the Hoomlab
service RFC-0001 describes. platform has explored the same ground for
AWS: a `platformctl` CLI, a service foundation design, GitOps
rendering, and agent sandboxes. Before designing hoomlab's next
phases from scratch, this measures what can be taken.

**Triggered by:** operator request (2026-10-10), ahead of hoomlab's
T3 and service design work.

## Approach

1. Inventory platform's docz docs and code (status, size, AWS
   imports).
2. Four parallel read-only reviews, each comparing one area against
   hoomlab's RFC-0001, ADR-0001, DESIGN-0001/0002, and the bootstrap
   code:
   - architecture and ideas (RFCs, ADRs, the loose design docs);
   - the CLI and the cloud-agnostic engine packages;
   - cluster baseline, GitOps rendering, and the management cluster;
   - the service foundation, control plane, sandboxes, and dev loop.
3. Verify the load-bearing claims directly (AWS imports, shared
   dependencies, stub status, provenance comments).
4. Classify each component: **ADOPT/COPY** (as-is), **ADAPT** (swap
   the AWS parts), **PATTERN** (take the idea, not the code),
   **DEFER** (fits hoomlab's service phase), **SKIP** (doesn't fit).

## Environment

| Component | Version / Value |
| --------- | --------------- |
| platform | `feat/gitops-render` @ `9dc6780` (tags v0.1.0, v0.2.0); Go 1.26.8; ~56k lines of Go |
| platform docs | 3 RFCs (Draft), 16 ADRs (Proposed), 4 designs (1 Implemented), 3 IMPLs (2 Completed), 6 INVs |
| hoomlab | `main` @ `d48300a` (bootstrap v0.3.2) |
| Shared dependency | `donaldgifford/hclkit` v0.2.0 in both |

Note the platform branch: the GitOps findings include its unmerged
`feat/gitops-render` work.

## Findings

### Observation 1: the visions share a spine; the substrate and scale differ

Both projects make the same core move: an out-of-cluster operator CLI
reads HCL through `hclkit`, keeps no state file, converges toward the
config, builds the first cluster, and stays the permanent recovery
path while a service later runs on that cluster (platform RFC-0001
§bootstrap mode; hoomlab ADR-0001). Platform layers enterprise
multi-tenancy on top: hub-and-spoke clusters, gated environment
promotion, Temporal, a Postgres + Valkey control-plane store, IAM/OIDC,
and agent sandboxes.

Maturity matters when weighing what to take. Of platform's docs, only
DESIGN-0001 (`platformctl`) is Implemented and only IMPL-0001 (EKS
cluster MVP) and IMPL-0002 (MicroVM direct mode) are Completed and
live-validated. Every ADR is Proposed and every RFC Draft. Platform's
roadmap also defers any Talos backend until EKS is proven, so there is
no Talos code to take — hoomlab is ahead there.

| Concept (platform source) | Verdict | hoomlab equivalent or reason |
| --- | --- | --- |
| No state file; live APIs + tags are truth (DESIGN-0001) | ADOPT | Already decided (ADR-0001); tags are the missing half — Observation 8 |
| CLI builds, service runs, CLI stays the recovery path (RFC-0001) | ADOPT | Already decided (ADR-0001) |
| HCL intent, "incomplete on purpose" | ADOPT | Already decided; same library |
| Intent → discover → plan → reconcile → status | ADAPT | hoomlab's `steps` is a collapsed form; take findings, explainable dry-run, conditions (Observation 3) |
| Argo CD + Kargo + Rollouts (ADR-0003) | ADOPT for T3 | DESIGN-0002 already names them; proven on fartlab (Observation 4) |
| stdlib + slog + gRPC + OTel (ADR-0006) | ADOPT | Same chart environment contract hoomlab already has |
| Domain/adapter layering + architecture test (ADR-0007) | ADAPT | Take the import rule; keep hoomlab's nested-module layout |
| Single-module monorepo (ADR-0007) | SKIP | Conflicts with hoomlab's separately released `tools/bootstrap` |
| talosctl-modeled CLI with contexts (ADR-0005) | ADAPT | Bootstrap already uses cobra; contexts belong to a future service CLI |
| Protobuf/gRPC APIs, AIP resources, long-running operations (ADR-0004) | DEFER | Service phase; bootstrap never grows an API (ADR-0001) |
| Hub-and-spoke (ADR-0002) | DEFER | One site; a second Talos cluster on the same PVE is the most it needs |
| Gated dev/stage/prod promotion (RFC-0001) | DEFER | A homelab rarely runs three environments |
| Temporal control plane (RFC-0002, ADR-0011) | DEFER, lean SKIP | Observation 6 |
| RDS Postgres / ElastiCache Valkey (ADR-0009/0010) | ADAPT/DEFER | CNPG on Talos with democratic-csi volumes; keep "Valkey never holds durable state" |
| Hub observability with Alloy (ADR-0014) | ADAPT | One cluster; the homelab already runs a Prometheus/Loki stack |
| Lambda MicroVM sandboxes (ADR-0012, RFC-0003) | DEFER | Observation 7 |
| Web UI stack (ADR-0008) | DEFER | Only once the service has an API |
| LLM observability, kagent (ADR-0013/0015) | SKIP | No LLM workloads in hoomlab's scope |
| IAM / Pod Identity / Identity Center | ADAPT | PVE tokens and roles; in-cluster OIDC; secrets from 1Password/OpenBao |
| Route53 / ACM | ADAPT | Cloudflare DNS-01 — already decided (ADR-0001) |
| Floci + testcontainers test tiers (ADR-0016) | ADAPT | Same unit/integration/contract split: `mockpve` + mockery, the hardware drill as the contract tier |

### Observation 2: little platform code is AWS-free, but what is, is useful

Platform's ~35 internal packages are mostly AWS domain logic
(`planning`, `network`, `dns`, `aws/**`, the CLI command families) or
reserved stubs. What has no AWS imports — verified — is small and
generic:

| Package | Lines (src) | Verdict | Use in hoomlab |
| --- | --- | --- | --- |
| `errorsx` | 163 | COPY | Error classes with hints and a single renderer |
| `cli/exitcode` | 72 | COPY | Error class → documented exit code (its test imports `awserr`; drop two cases) |
| `retry` | 63 | COPY | `Poll(ctx, backoff, condition)` with jitter and a fake sleep; replaces the hand-rolled poll loops in `pve/form.go` and `talos/client.go` |
| `progress` | 43 | COPY | Started/Done/Skipped/Failed events behind a nil-safe reporter |
| `cli/progress` | 212 | COPY (optional) | TTY spinner, plain lines otherwise; adds the charm libraries |
| `texttable`, `k8slabel` | 95 | COPY when needed | Tables; label validation for the service |
| `finding`, `condition` | 238 | ADAPT | Keep the types, replace the IAM/EKS codes and constants |
| `cli/output` | 316 | ADAPT | Keep the printer interfaces; drop the domain printers |
| `preflight`, `cli/runtime`, `reconcile`, `bootstrap/apply` | — | PATTERN | Good shapes, typed to AWS |

The copyable set is about 340 lines for the core (`errorsx`,
`exitcode`, `retry`, `progress`). Platform is a single module with
everything under `internal/`, so hoomlab cannot import it: the options
are copy-with-attribution, or extracting a small shared library the
way `hclkit` already is.

### Observation 3: the engine models agree; platform's adds output, not truth

Platform's real apply loop (`internal/bootstrap/apply.go`) and
hoomlab's `steps.Runner` are the same idea: ordered idempotent steps,
stop at the first failure, resume on re-run. Platform's is a superset
in four ways worth noting:

- **Progress events** — a renderer-independent reporter, instead of
  hoomlab's `slog` lines.
- **Findings** — a preflight that reports every problem in one pass
  instead of failing on the first.
- **Field-level drift** — `FieldDiff{Path, Live, Planned}`, so a dry
  run could say *which* fields drifted, not just `pending`. In
  hoomlab this means `Check` returns a diff rather than a bool.
- **Conditions** — per-step status with a reason; little value in a
  CLI, but the natural status API for the service.

One part conflicts and should not be taken: platform persists a plan
file with a fingerprint and refuses stale plans. That is a third
truth beside "the config and the world", which ADR-0001 rules out. A
dry run can *show* an explainable plan without *saving* one.

The CLI pattern most worth copying is platform's testable command
seam: `NewRootCommand(factory)` with the API clients injected, and a
`run(t, factory, args...)` helper that returns stdout, stderr, and the
exit code. hoomlab's `cmd/` has 0% coverage for exactly one reason —
its commands call `pve.NewClient` and `talos.NewClient` directly. The
`talos.Client` interface and its mocks already exist.

### Observation 4: platform's GitOps came from the homelab, and T3 already exists

Platform's GitOps renderer (`internal/gitops/estate`) states in its
own header that it began as the homelab's boilermate pointer model,
and its Argo CD bootstrap follows the homelab's bootstrap script. Most
of platform's other in-cluster Go (`argocd/applicationset`,
`kargo/promotion`, `management`, `kubernetes/bootstrap`) is still
unimplemented stubs, and its Argo CD install is still a shell script.

More important: the gitops layer DESIGN-0002 calls T3 — and says
"doesn't exist yet" — **already runs on fartlab**, from the operator's
private homelab repository. Its bootstrap checklist is complete:
Argo CD adopting Cilium and itself, the BGP objects adopted from the
hand-applied set (they match hoomlab's `examples/bgp/` except the
auth secret), cert-manager with Cloudflare DNS-01, external-dns,
Cilium Gateway API, democratic-csi on the storage plane, CNPG, an
identity provider, OpenBao, and a Prometheus/Loki stack.

So hoomlab's T3 gap is a documentation and ownership gap, not a build
gap. The AWS add-ons platform renders map onto what fartlab already
runs:

| Platform (AWS) | hoomlab / fartlab equivalent |
| --- | --- |
| VPC CNI + kube-proxy | Cilium with kube-proxy replacement, delivered by bootstrap, adopted by Argo CD |
| AWS Load Balancer Controller | Cilium Gateway API + BGP control plane + LB-IPAM |
| ACM, cert-manager on Route53 | cert-manager, Cloudflare DNS-01 |
| external-dns → Route53 | external-dns → the internal DNS server |
| EBS CSI | democratic-csi over iSCSI to the PVE ZFS storage plane (IMPL-0003) |
| Pod Identity / IRSA, External Secrets | Secrets from 1Password Connect / OpenBao |
| Karpenter / managed node groups | Static VMs declared in bootstrap HCL |
| Gateway API CRDs (Argo-owned) | Talos `extraManifests`, bootstrap-owned (DESIGN-0002) |

Worth borrowing from platform's version for the hoomlab side: the
`root.hcl` repository settings, complete per-cluster app folders, and
the single-renderer rule (install Argo CD from the same rendered
directory its Application later syncs, so the first sync is a no-op).
One handoff step deserves to be written into the contract: the
`cilium-install` Job leaves a Helm release record that must be removed
for Argo CD to adopt Cilium.

### Observation 5: the service foundation is design, not code

Platform's service is as unbuilt as hoomlab's: `cmd/platform/main.go`
and `cmd/hoomlab/main.go` are the same 27-line stub, `telemetry` is a
`doc.go`, and platform DESIGN-0003 lists protobuf, gRPC, pgx,
migrations, and the Temporal SDK as not yet present. Both repos came
from the same scaffold: the chart templates, `docker.just`, and
`helm.just` are identical.

What transfers is platform DESIGN-0003, a well-specified blueprint
hoomlab can adapt section by section:

| Section | Verdict | Note |
| --- | --- | --- |
| Process, config, lifecycle (typed env, `/readyz` vs `/healthz`, drain on SIGTERM) | ADAPT | Drop the IAM database auth |
| gRPC + gateway, buf layout, interceptors, error mapping | ADAPT | Decide first whether hoomlab's service needs an API before its later phases |
| Operations (op ID, ordered events, watch, request-ID idempotency) | PATTERN | Fits long PVE tasks; works without Temporal |
| Persistence (pgx, goose embedded, pre-upgrade migration Job) | ADAPT | CNPG with a managed secret; Postgres holds operations and audit, never infrastructure truth |
| Telemetry (`Setup()` returning shutdown, OTLP opt-in, Prometheus) | ADAPT | The chart already ships the ServiceMonitor |
| AuthN/Z, teams, quotas | DEFER | One operator |
| Valkey | SKIP | Platform's own design says the foundation doesn't need it |
| Integration tests (`-tags integration`, testcontainers Postgres) | COPY | hoomlab has no `test-integration` recipe yet |

### Observation 6: Temporal does not fit hoomlab now

Platform's case for Temporal (RFC-0002) is multi-team: self-service
namespaces, per-team isolation, approvals that wait for days. A
single-operator homelab has none of that. Worse, Temporal and its
Postgres would run on the cluster the service manages — a second
stateful system to restore before anything can reconcile — which
platform's own "recovery can't depend on itself" guardrail warns
against, and which cuts against ADR-0001's converge-from-config model.
A level-triggered reconcile loop gets crash safety for free. Because
platform's operations contract hides the engine behind a start call,
Temporal can be added later without an API change if long, human-gated
workflows ever appear.

### Observation 7: sandboxes map to PVE, but nothing asks for them yet

hoomlab's RFC-0001 doesn't mention sandboxes. Platform's
`internal/sandbox` engine is SDK-free behind two seams, but the seam
types mirror Lambda MicroVMs (execution role ARNs, image versions,
records keyed for DynamoDB), so it is a pattern, not a drop-in. If a
later hoomlab phase wants sandboxes, the backends rank:

1. **PVE QEMU VMs** from a template — strongest isolation, and every
   operation maps onto `proxmox-go-sdk` and its `mockpve`; the shell
   maps to PVE's termproxy (a websocket client would need writing).
2. **PVE LXC** — same API shape, fast start, shared kernel (fine for
   trusted workloads, not hostile code).
3. **Kata Containers** via a Talos extension — a RuntimeClass on the
   existing cluster; needs nested virtualization on the PVE hosts.
4. **Firecracker on a Talos node** — no Talos-native management;
   platform's ADR-0012 rejected it even on bare metal.

### Observation 8: ownership tags are hoomlab's most concrete gap

Platform's no-state model rests on two halves: live APIs are the
truth, *and* every resource it creates carries ownership tags, with a
tag audit and a leftover sweep. hoomlab adopted the first half only —
the bootstrap tags nothing it creates in PVE (verified: no tags or
descriptions in `internal/pve`). Without tags, "is this VM ours?" is
answered by VMID convention alone, which is unsafe for teardown and
for handing ownership to the service later.

## Conclusion

**Answer:** Yes for ideas and a small set of utilities; mostly no for
code. Confirmed with one reversal of the premise.

- **Ideas transfer well.** The architectural spine is already shared;
  ADR-0001 and platform's bootstrap-mode RFC make the same promise.
  Platform's design docs — especially DESIGN-0003 (service
  foundation) and the intent/plan/reconcile/status contracts — are the
  most valuable thing to take, adapted.
- **Code transfers thinly.** About 340 lines of generic utilities copy
  as-is; a few types adapt; everything typed to AWS resources stays.
  Nothing can be imported directly.
- **The GitOps question reverses.** Platform's GitOps is the
  homelab's, and hoomlab's T3 already runs on fartlab. The work is
  documenting and owning the handoff, not building it.
- **Two things don't fit:** Temporal (now) and a persisted plan file
  (ever, under ADR-0001).

## Recommendation

In rough order of value:

1. **Fix the T3 record** (docs). Amend DESIGN-0002 and runbook §14:
   T3 exists and its source of truth is the operator's private GitOps
   repository; mark `tools/bootstrap/examples/bgp/` superseded (Argo CD
   now governs those objects). Write the Cilium-adoption step into the
   handoff contract. Decide whether hoomlab should eventually own a
   public GitOps template or keep pointing at the private repo.
2. **Ownership tags on everything bootstrap creates** (code, small).
   Tag the VMs (and storage entries) with the cluster identity; add
   the check to `validate`/dry-run so an untagged VMID collision is
   caught before a create.
3. **Testable `cmd/`** (code, small). Inject client factories into
   `newRootCmd` and add a `run()` test helper, following platform's
   root-command seam.
4. **Copy the core utilities** (code, small) when the next change
   needs them: `retry` first (it replaces hand-rolled polls), then
   `errorsx` + `exitcode` + `progress`. Copy with an attribution
   comment; extract a shared library only when a third consumer
   appears.
5. **Explainable dry-run** (code, medium). Let `Check` return field
   diffs so `--dry-run` says what would change. Show plans; never
   persist them.
6. **Service design** (later). When the service phase starts, write
   hoomlab's foundation design by adapting platform DESIGN-0003, with
   CNPG for operations and audit only, and a reconcile loop instead of
   Temporal.
7. **Defer** hub-and-spoke, promotion gates, sandboxes (QEMU-first if
   ever), the web UI, and auth beyond a single OIDC issuer.

## References

- platform (`~/code/platform`): RFC-0001/0002/0003; ADR-0001–0016;
  DESIGN-0001 (platformctl), DESIGN-0003 (service foundation),
  DESIGN-0004 (cluster baseline and GitOps rendering); IMPL-0001–0003;
  INV-0006 (management cluster live test); `internal/gitops/estate`,
  `internal/bootstrap/apply.go`, `internal/cli/root.go`,
  `internal/sandbox/seams.go`
- hoomlab: RFC-0001, ADR-0001, DESIGN-0001, DESIGN-0002 (T1–T3),
  DESIGN-0004, runbook §14
- `donaldgifford/hclkit` — the existing shared-library precedent
