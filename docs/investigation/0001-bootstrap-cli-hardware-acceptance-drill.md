---
id: INV-0001
title: "Bootstrap CLI hardware acceptance drill"
status: In Progress
author: Donald Gifford
created: 2026-08-21
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0001: Bootstrap CLI hardware acceptance drill

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
  - [Remaining pre-drill work](#remaining-pre-drill-work)
- [Environment](#environment)
- [Findings](#findings)
  - [Pre-drill: defects found by running the emitted artifacts](#pre-drill-defects-found-by-running-the-emitted-artifacts)
  - [Pre-drill: what has been verified without hardware](#pre-drill-what-has-been-verified-without-hardware)
  - [Phase 1 gate: go (2026-08-25)](#phase-1-gate-go-2026-08-25)
  - [Drill results](#drill-results)
  - [Convergence pass](#convergence-pass)
  - [Deviations from the runbook](#deviations-from-the-runbook)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

Does the `bootstrap` CLI take bare Proxmox hardware to a healthy Talos
Kubernetes cluster, following the runbook literally — and does a second
full pass apply nothing?

Concretely, two things must be true:

1. `kubectl get nodes` via the emitted kubeconfig shows every
   configured node `Ready` (RFC-0001's Phase 1 success criterion).
2. Re-running every stage afterwards reports every step already done —
   the no-op property the Hoomlab service will rely on when it takes
   ownership of the cluster.

## Hypothesis

The cluster comes up, and the failures that do occur are in the
*hardware-facing* stages rather than in convergence logic.

The reasoning: the stages that can be exercised without Proxmox
(`validate`, `talos secrets`, `talos emit`, `talos ipxe`, and booty
serving the emitted tree) are covered by tests and have now been run
for real. The stages that cannot (`pve form`, `pve certs`, `talos vms`,
`talos bootstrap`, `talos health`) are covered only by mocks — and a
mock encodes what we believed the API does, which is exactly the belief
a drill exists to test. The pre-drill findings below support this:
every defect found so far came from *running* something rather than
from reading it, and none were in the convergence engine.

Expected failure modes, in rough order of likelihood: PVE API shapes
that differ from `mockpve`'s; the PXE handshake (proxyDHCP alongside an
existing DHCP server, TFTP, the UEFI boot order); and DNS-01 timing.

## Context

IMPL-0001 is **code-complete**: all six phases are implemented, tested,
and linted, and every gate passes. Six tasks remain unchecked and all
six require the physical lab — they cannot be discharged from a
workstation at any level of effort:

| IMPL-0001 task | Why it needs hardware |
| --- | --- |
| Spot-check `pve form` against a nested 3-node lab | Needs real Proxmox nodes to form and join |
| Run the full drill on homelab hardware | The drill itself |
| Re-run every stage and confirm the pass is a no-op | Requires a cluster to have been built |
| Record drill results and deviations | Requires the drill |
| Cut `tools/bootstrap/v0.1.0` | IMPL-0001 OQ-5 gates the release on the drill passing |
| e2e real-lab drill (testing plan) | Same |

This investigation is the vehicle for that work: it holds the drill
results, and any code change the drill forces is tracked here and
folded back into IMPL-0001 and the runbook.

**Triggered by:** IMPL-0001 Phase 6 (bootstrap CLI), which implements
DESIGN-0001 / ADR-0001 for RFC-0001 Phase 1.

## Approach

1. **Pre-drill hardening** (in progress). Exercise everything that can
   be exercised without Proxmox, and fix what breaks — the drill costs
   hours of hardware time, so defects found at a workstation are the
   cheapest ones.
2. **First contact on the real hosts** (IMPL-0002's pragmatic
   revision, 2026-08-22: the originally planned nested pvelab
   rehearsal is deferred with the rest of the isolated test
   environment — the three hosts already have PVE installed, so every
   remaining step is API calls and VMs, and the bare-metal round-trip
   cost the nested lab existed to avoid isn't there). The hosts start
   *unclustered but configured*, not fresh — what joining preserves
   and destroys is itself under test.
3. **The drill.** Run the full flow on the real hardware **from
   [the runbook](../runbook/bootstrap-cluster.md), not from memory**.
   A step in the runbook that is wrong or missing is a finding, not
   something to work around silently.
4. **The convergence pass.** Re-run every stage and confirm each
   applies nothing.
5. **Fold back.** Every deviation becomes a code fix, a runbook fix, or
   a documented decision — then IMPL-0001's remaining boxes get checked
   and `tools/bootstrap/v0.1.0` is cut.

Use Let's Encrypt **staging** (`acme.directory`) for the drill so
failed orders don't burn production rate limits; re-run `pve certs`
against production once the flow is proven.

### Remaining pre-drill work

- [x] Smoke the emitted tree through a real booty container: catalog
      loads, `/boot.ipxe`, `/ipxe?mac=…`, `/machine-config?mac=…`, and
      the boot assets all answer correctly
- [x] Build `ipxe.efi` for real rather than through the test stub —
      produces a 984 KB x86-64 PE32+ EFI application with the chain
      script embedded and pointing at the configured booty URL; the
      stamp matches `embed.ipxe`, and a re-run converges without
      rebuilding
- [ ] Verify the built `ipxe.efi` actually chainloads — needs a machine
      to PXE boot, so it lands in the drill
- [ ] ~~Nested-lab rehearsal of `pve form` and `pve certs`~~ —
      superseded by IMPL-0002 (2026-08-22): first contact happens on
      the real hosts; the nested environment is deferred to a future
      design doc with this run as its requirements gathering

## Environment

| Component | Version / Value |
| --- | --- |
| bootstrap CLI | built from `b7968c4` (`feat/impl-hardware-drill`, clean tree) via `just bootstrap-build` → `build/bin/bootstrap` (first build `4b443ea` retired by the create-as-root fold-back) |
| Talos | `v1.13.8` (machinery `v1.13.9`) |
| Kubernetes | `v1.36.3` (machinery default) |
| booty | `v0.2.1` — image `ghcr.io/donaldgifford/booty:0.2.1` |
| iPXE | `v1.21.1`, built in `debian:bookworm-slim`, `linux/amd64` |
| Talos Image Factory schematic | `376567…b4ba` (vanilla, no extensions) |
| proxmox-go-sdk | `v0.11.0` |
| Proxmox VE | `9.2.10` on r740a (live 2026-08-22); r640a/srv01 confirm at drill time |
| Workstation | macOS 26.5.2, arm64 (Apple Silicon), Go 1.26.6; secrets injected via `op run --env-file` |
| Lab topology | Nodes: `r740a` 10.10.11.20 (primary, R740xd, `fast`/`tank` pools), `r640a` 10.10.11.21, `srv01` 10.10.11.40 (NVMe-mirror `local-zfs`). Networks: mgmt/API 10.10.11.0/24 (`vmbr0`), storage 10.10.13.0/24 (`stor0`), corosync 10.10.15.0/24 (`sync0`); guests on VLAN-aware `vmbr1` (10 GbE). Cluster `shart`, cert domain `shart.sh`, Talos domain `fartlab.dev`. Datasets `fast/vm`/`tank/vm` pre-created on both dells; pool roots (live Garage data) off-limits to PVE. Snapshot: [bootstrap-handoff](../runbook/bootstrap-handoff.md), verified 2026-08-22/23. DHCP: *(record at drill time)* |

## Findings

### Pre-drill: defects found by running the emitted artifacts

Six defects, all found by *executing* what the CLI produces rather
than reading it. Each would have failed during the drill, and five of
them at the same step — one after another, each hiding the next, so
discovering them on hardware would have cost five round-trips.

| # | Stage | Symptom | Cause | Fix |
| --- | --- | --- | --- | --- |
| 1 | `talos emit` → operator step | `docker: ... booty:v0.2.1: not found` when running the emitted `booty-run.sh` on the booty host | booty's git tags carry a leading `v`; its GHCR tags do not (`0.2.1`, never `v0.2.1`). The golden file had been pinning the broken reference. | Strip the `v` when building the image reference; test asserts the emitted tag has no `v` for any config spelling |
| 2 | `talos ipxe` | `docker: "bootstrap-out/booty/boot" includes invalid characters for a local volume name` | Relative `--volume` sources; docker reads anything without a leading separator as a *named volume*. `--output` defaults to the relative `./bootstrap-out`, so this was the ordinary flow, not an edge case. | Resolve both bind-mount sources with `filepath.Abs`; test runs from a relative root, which every prior test avoided by using `t.TempDir()` |
| 3 | `talos ipxe` | `fatal: unable to access 'https://github.com/ipxe/ipxe.git/': server certificate verification failed. CAfile: none` | `debian:bookworm-slim` ships no trust store, and the apt install list omitted `ca-certificates`. Surfaces *after* the apt step, so it reads like a network fault. | Add `ca-certificates` to the install list |
| 4 | `talos ipxe` | `gcc: error: unrecognized command-line option '-m64'` | On Apple Silicon the builder pulled the arm64 image, whose gcc cannot target x86-64. The artifact must be x86-64 regardless of the workstation. | Pin `--platform linux/amd64`; slower under emulation, correct everywhere |
| 5 | `talos ipxe` | `error: array subscript ... is partly outside array bounds [-Werror=array-bounds]`, build fails | iPXE v1.21.1 trips gcc-12 false positives, fatal under iPXE's default `-Werror` | Pass iPXE's own `NO_WERROR=1` |
| 6 | `talos ipxe` | `include/stdint.h:16:10: fatal error: bits/stdint.h: No such file or directory`, building `util/elf2efi64` | `--no-install-recommends` means `gcc` never pulls `libc6-dev`, so there is no `/usr/include/stdint.h`. iPXE's firmware objects are freestanding and never notice; only the *host* tool does, so it fails alone and late, and the error points at iPXE's own headers rather than the missing package. Reproduced identically on iPXE master, which ruled out the version. | Add `libc6-dev` |

The pattern is worth naming: **every one of these was invisible to the
test suite**, because the tests stub the container runtime and pin the
emitted text. They test that we emit what we meant to emit. None of
them could test whether what we meant to emit *works*. Tests of the
second kind require running the artifact.

### Pre-drill: what has been verified without hardware

Run on a workstation against the real Image Factory and a real booty
container:

- `validate` accepts the shipped example and reports a missing `env()`
  export with the offending line and column.
- `talos secrets` writes a `0600` bundle, and a re-run leaves it alone.
- `talos emit` renders the full tree and downloads the real Talos
  Image Factory kernel (20 MB) and initramfs (86 MB); a re-run reports
  nothing to do.
- **booty loads the emitted catalog**: `catalog loaded … profiles=2
  groups=4`.
- `GET /boot.ipxe` → 200, chains to `/ipxe?mac=${mac}&…`.
- `GET /ipxe?mac=02:50:99:a2:00:01` → 200, `booty: booting cp-01
  (profile talos-control)`, with `kernel`/`initrd` pointing at
  `/boot/talos/v1.13.8/…`.
- `GET /machine-config?mac=…` → 200; the control-plane node gets
  `type: controlplane` and `hostname: cp-01`, the worker gets
  `type: worker` and `hostname: worker-01`. No template expressions or
  placeholders remain, and the cluster secrets are seeded.
- An **unconfigured** MAC → `404`, so the catalog is not matching more
  broadly than intended.
- `GET /boot/talos/v1.13.8/{vmlinuz,initramfs.xz}` → 200 at full
  length.

That discharges IMPL-0001 Phase 4's manual-smoke success criterion.
What it does **not** cover is the actual PXE handshake — proxyDHCP,
TFTP, and a machine chainloading `ipxe.efi` — which needs a machine.

### Phase 1 gate: go (2026-08-25)

**Decision: GO** — bootstrap as it stands is acceptable to run against
the production nodes. Made on the evidence below, with the operator
running every command:

- OQ-1 through OQ-4 decided; OQ-5 (VLAN tag on `net0`) deferred with
  the boot-network task to IMPL-0002 Phase 3 — it gates nothing
  before `talos vms`.
- `/etc/pve` tarballs and `/etc/network/interfaces` copies pulled
  off-node for all three nodes.
- All three nodes verified guest-free with byte-identical
  installer-default `/etc/pve`; the predicted join loss on the
  joiners is *nothing* (same-content overwrite).
- Fleet-wide `root@pam` password confirmed; dedicated
  `root@pam!bootstrap` token (privsep=0) minted on r740a and proven
  against the live API.
- `<node>.shart.sh` A records resolving via 1.1.1.1; the zone-scoped
  Cloudflare token verified against the zones API.
- The primary-only config validates under the real `op run`
  injection; `pve form --dry-run` against r740a reported 2 of 2 steps
  pending, nothing applied.

### Drill results

Record each step as `pass`, or what actually happened. Steps 1–2 and
5–7 have workstation equivalents above; the point here is the lab.

| # | Step | Expected | Result |
| --- | --- | --- | --- |
| 1 | `bootstrap validate` | exit 0 | pass (2026-08-25, under the real `op run` injection) |
| 2 | `bootstrap pve form` | cluster formed and quorate | |
| 3 | `bootstrap pve certs` | certificates on all nodes | |
| 4 | `bootstrap talos secrets` | `secrets.yaml`, 0600 | |
| 5 | `bootstrap talos emit` | full tree under `bootstrap-out/booty/` | |
| 6 | `bootstrap talos ipxe` | `boot/ipxe.efi` built | |
| 7 | copy tree to booty host, `./booty-run.sh` | `/boot.ipxe` and `/machine-config?mac=…` answer | |
| 8 | `bootstrap talos vms` | every VM created and running | |
| 9 | (watch) | VMs PXE boot, install, reboot into Talos | |
| 10 | `bootstrap talos bootstrap` | etcd bootstrapped, credentials written | |
| 11 | `bootstrap talos health` | cluster reports healthy | |
| 12 | `kubectl --kubeconfig bootstrap-out/out/kubeconfig get nodes` | every node `Ready` | |

### Convergence pass

Every stage re-run; each should apply nothing.

| Stage | Steps applied on second pass | Notes |
| --- | --- | --- |
| `pve form` | | |
| `pve certs` | | |
| `talos secrets` | | |
| `talos emit` | | |
| `talos ipxe` | | |
| `talos vms` | | |
| `talos bootstrap` | | |
| `talos health` | | |

Any stage that applies something on the second pass is a convergence
bug: record which step re-fired and why.

### Deviations from the runbook

| Date | Step | What happened | Resolution |
| --- | --- | --- | --- |
| 2026-08-25 | `pve form --dry-run` | Reported `2 of 2 steps pending` while the token credential was invalid (a typo in the 1P env reference → HTTP 401 on every read). Checks deliberately swallow read errors as "pending" (the design routes real errors to apply), so dry-run cannot distinguish "step pending" from "cannot authenticate at all" — first contact started on false confidence. | Operator error fixed (the typo). The UX gap stands as a finding: a fail-fast credential check at stage start (or surfacing repeated check errors at info level) would have caught it. Candidate improvement, not yet scheduled. |
| 2026-08-25 | `pve form`, `create-cluster` | r740a rejected the create with `HTTP 403: Permission check failed (user != root@pam)`. PVE reserves `POST /cluster/config` for the literal root@pam user; the API token authenticates as `root@pam!bootstrap` and can never pass, privsep or not. DESIGN-0001's credential split (token for the create) was wrong — mockpve happily accepted token-authed creates, hiding it. | Code fix `b7968c4`: `applyCreate` dials with root@pam password credentials; the shared test dialer now enforces the root-only rule on formation writes (named regression fails pre-fix with the lab's exact error); DESIGN-0001 secrets table + example config amended. mockpve enforcement upstream in proxmox-go-sdk noted as an SDK follow-up. |

## Conclusion

**Answer:** *pending the drill.*

## Recommendation

*Pending.* On a passing drill and a clean convergence pass:

1. Check off IMPL-0001's six remaining tasks and move it to
   **Completed**.
2. Move DESIGN-0001 from **Approved** to **Implemented**.
3. Cut `tools/bootstrap/v0.1.0` via `tools-release.yml` (OQ-5).
4. Fold every deviation into the code and
   [the runbook](../runbook/bootstrap-cluster.md), and close this
   investigation as **Concluded**.

## References

- [RFC-0001](../rfc/0001-hoomlab-a-self-hosted-cloud-for-homelab-environments.md) — Phase 1 success
  criterion (`kubectl get nodes` all `Ready`)
- [ADR-0001](../adr/0001-bootstrap-cli-and-service.md) — the decision to build a
  bootstrap CLI
- [DESIGN-0001](../design/0001-bootstrap-cli.md) — convergence model,
  stage design, data model
- [IMPL-0001](../impl/0001-bootstrap-cli.md) — phase tracking
- [Runbook: bare Proxmox nodes → healthy Talos
  cluster](../runbook/bootstrap-cluster.md) — the procedure the drill
  follows
- [`tools/bootstrap/README.md`](../../tools/bootstrap/README.md) —
  operator reference for the CLI
