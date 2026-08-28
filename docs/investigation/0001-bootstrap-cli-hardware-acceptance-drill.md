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
| bootstrap CLI | built from `a97c287` (`feat/impl-hardware-drill`, clean tree, **proxmox-go-sdk v0.12.0 released** — the workspace-override interlude from `056ff89`–`296f483` is over and the branch pushes again) via `just bootstrap-build` → `build/bin/bootstrap` (prior builds retired by fold-backs: `4b443ea`, `b7968c4`, `99b9416`, `f4ba8d5`, `797127e`, `6120bd1`, `528976d`, `7bf52e9`, `1215d93`, `39e3642`) |
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
| 2 | `bootstrap pve form` | cluster formed and quorate | pass (2026-08-25): cluster of one formed and quorate first, then grown to three by re-running against the expanded config — r640a and srv01 joined serially, first try, zero fold-backs; `pvecm status` 3/3 votes, quorate, membership `10.10.15.20/.21/.40` (all corosync link0 on sync0). Join-wipe verified: `/etc/pve` replaced wholesale, zero loss as predicted (joiners were installer-default) |
| 3 | `bootstrap pve certs` | certificates on all nodes | pass (2026-08-25/26): staging convergence on the primary, the staging→production flip as a real transition (deviations 3–7 en route), then the extension to both joiners first-try — all three nodes serve production certificates (`C=US, O=Let's Encrypt, CN=YR1`; subjects `r740a`/`r640a`/`srv01.shart.sh`, openssl-verified) |
| 4 | `bootstrap talos secrets` | `secrets.yaml`, 0600 | pass (2026-08-27): bundle generated 0600, driving every rendered machineconfig; backed up to 1Password (item `u4xvg44gysubpcn2coszsij36i`); re-run answers "already exists — leaving it alone" |
| 5 | `bootstrap talos emit` | full tree under `bootstrap-out/booty/` | pass (2026-08-27): catalog trio + both role templates + `embed.ipxe` + launcher; v1.13.8 assets downloaded with TOFU sidecars |
| 6 | `bootstrap talos ipxe` | `boot/ipxe.efi` built | pass (2026-08-27): built with embed stamp for the `10.10.11.190:8080` chain script |
| 7 | copy tree to booty host, `./booty-run.sh` | `/boot.ipxe` and `/machine-config?mac=…` answer | pass (2026-08-27), with a deliberate delivery substitution: the tree is served by the lab's ansible-managed compose (roles/booty) instead of `booty-run.sh` — flag-for-flag equivalent (host net, `0:0`, `:ro` mounts, same `serve` args), plus `restart: unless-stopped`. Full curl battery green: chain → per-MAC scripts (both roles) → complete machineconfigs, 404 for strangers, assets byte-exact (20455424 / 86170982) |
| 8 | `bootstrap talos vms` | every VM created and running | pass (2026-08-28): first contact died on the by-ID status GET (deviation 10); after the index-based fix, 12/12 steps applied in 40 s — six VMs created and started across the three nodes, interleaved create-then-start per node. Repeated in full after deviation 12's destroy-and-recreate, this time with `scsihw`/`agent` in the spec |
| 9 | (watch) | VMs PXE boot, install, reboot into Talos | pass (2026-08-28), the hard way: first cycle dropped all six to an iPXE shell (deviation 11, embed script without `dhcp`), second cycle installed nowhere (deviation 12, LSI controller invisible to Talos). Third cycle, fully unattended: proxyDHCP → TFTP (the rebuilt 983936-byte `ipxe.efi`) → `dhcp` → `/boot.ipxe` → per-MAC script → schematic-scoped kernel/initramfs → machine-config → install to `/dev/sda` → reboot from disk, booty quiet per MAC afterward — all six, ~90 s per node |
| 10 | `bootstrap talos bootstrap` | etcd bootstrapped, credentials written | pass (2026-08-28): after deviation 13's two rounds (false-done Check, then hanging probe), the fixed stage probed for 15 s, applied `etcd-bootstrap` in 12 ms, and the console confirmed `bootstrap request received` → etcd `Running` → `Health check successful`. `talosconfig`/`kubeconfig` 0600 under `bootstrap-out/out/`, never overwritten across the night's many re-runs |
| 11 | `bootstrap talos health` | cluster reports healthy | pass (2026-08-28): first run blocked at the boot-sequence phase with all six at `Booting` (deviation 14, guest-agent channel); after the rolling `qm set --agent` + restart, the full battery passed — etcd ×3 consistent, apid, kubelet, boot sequence, static pods, control-plane components, schedulable. `kube-proxy: SKIP` is itself delivery evidence: there is none to check, Cilium's replacement owns it. `✓ cluster "shart" is healthy` |
| 12 | `kubectl --kubeconfig bootstrap-out/out/kubeconfig get nodes` | every node `Ready` | pass (2026-08-28): 6/6 `Ready` — `ctrl01`–`ctrl03` `control-plane`, `work01`–`work03` — kubelet `v1.36.3`, Talos v1.13.8, kernel `6.18.42-talos`, containerd 2.2.6, internal IPs `.51–.53`/`.61–.63` exactly as reserved. Ready with no kube-proxy on the cluster is the Cilium delivery proven end to end. **RFC-0001's Phase 1 criterion, demonstrated** |

### Convergence pass

Every stage re-run; each should apply nothing.

| Stage | Steps applied on second pass | Notes |
| --- | --- | --- |
| `pve form` | 0 of 4 (2026-08-26) | create + both joins + quorate all skip against the live three-node cluster |
| `pve storage` | 0 of 2 (2026-08-27) | first live re-run after the applying run — zero rotation on real PVE's read-back (server-materialized `mountpoint`, set-ordered lists, sparse via Extra) on the stage's first day; local-workspace build against SDK PR #30 |
| `pve certs` | 0 of 8 (2026-08-26) | account, plugin, and per-node config/cert ×3 all skip with production certificates installed; run back to back with `pve form`. One root@pam login per run remains (the account-directory read fallback) — expected, by design |
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
| 2026-08-25 | `pve certs`, `acme-account` | Same 403, second organ: `POST /cluster/acme/account` carries no permissions block, and PVE defaults such endpoints to root@pam only. Verified in pve-manager source that it is the *only* reserved call in the stage — plugins, node config, and certificate orders are `Sys.Modify`-gated, so the token covers everything else, renewals included. A root-equivalent user/token cannot work around it: the check is identity, not privilege. | Code fix `99b9416`: lazy `DialRoot` session used by exactly the account write (a converged re-run or renewal never authenticates as root); two-mock regression pins the registration to the root session; DESIGN-0001 table + example config amended again. |
| 2026-08-25 | `pve certs`, `acme-plugin-cloudflare` | Real PVE answers a by-ID GET on a missing ACME plugin with `HTTP 500: ACME plugin 'cloudflare' not defined`, not a 404 — `pluginCheck`'s `ErrNotFound` branch never matched, so "not created yet" read as a fatal server error. mockpve returns a clean 404 for the same case: the predicted real-vs-mock discrepancy class, first sighting. | Code fix `f4ba8d5`: plugin existence resolved through `ListACMEPlugins` (the index carries every needed field, digest included) at both call sites — the by-ID wart is sidestepped structurally. Mirroring PVE's 500-on-missing in mockpve noted upstream as an SDK follow-up. |
| 2026-08-25 | `pve certs`, staging→production flip | **Predicted before the run, confirmed by it:** flipping `acme.directory` converged on nothing — `accountCheck` was name-only and `certCheck` expiry-only, so neither noticed the CA changed; the staging account and staging certificate satisfied every check and the cluster kept serving staging. | Code fix `797127e`: `accountCheck` compares the registered account's CA directory against the config (token-first read, root-fallback on 403); a mismatch deactivates and re-registers through the root session. `certCheck` rejects staging-issued certs when the desired CA is LE production. Flip regression drives the full path against mockpve; both fail pre-fix. |
| 2026-08-25 | `pve certs`, `acme-plugin-cloudflare` re-run | Bonus find inside the flip attempt: the plugin step **rotated identical credentials** on a run where nothing changed — real PVE round-trips the stored payload in its own rendering, not the SDK's byte-exact encoding, so the byte-for-byte comparison never matched and the step would re-apply on every run (a permanent false-pending the convergence gate would have flunked). | Took three rounds to kill. `797127e` made the comparison structural (decode, parse `KEY=value` lines, compare maps) — still rotated. `528976d` theorized unpadded base64 from the stored `len=62` and added a raw-base64 fallback plus Warn/Info drift logging — still rotated, but the new Warn surfaced the truth: `illegal base64 data at input byte 2` — byte 2 of `CF_Token` is `_`. **Real PVE returns the `data` field as DECODED plaintext `KEY=value` lines**; it was never base64 in either rendering, and mockpve echoing the submitted base64 verbatim is exactly why every encoding-shaped comparison passed the mock and failed the lab. Final fix `7bf52e9`: `decodePluginData` tries padded then raw base64 and otherwise takes the payload as the plaintext it is (unambiguous — credential lines carry mid-string `=` and `_`, both illegal in base64); the error path is gone, so no decode failure can ever be swallowed as "drifted" again. Regression seeds the plugin exactly as real PVE renders it and fails pre-fix (rotation observed). mockpve read-shape parity (return decoded plaintext, as real PVE does) noted upstream as an SDK follow-up. |
| 2026-08-25 | `pve certs`, `acme-cert-r740a` (flip reorder) | The production reorder over the installed staging certificate failed: `HTTP 400 … Custom certificate exists but 'force' is not set` — PVE's order endpoint refuses while a frontend certificate file exists, and the SDK exposes `force` on neither order nor renew. mockpve does not model the refusal. Bonus fact from the same run: the per-account GET is root-reserved (the token-first read logged its root@pam fallback), so the directory verification costs one root login per certs run. | Code fix `6120bd1`: `applyOrder` picks its path from the existing `pveproxy-ssl.pem` — absent → order; present and otherwise right → **renew** (no force needed inside the renewal window); present and wrong (CA flip, SAN change) → **delete the frontend cert, then order** (brief self-signed window, only entered when the served cert is already wrong). Force turns out unnecessary. SDK follow-ups noted: `force` params + mockpve modeling the refusal. |
| 2026-08-27 | `pve storage --dry-run` (first contact, local-SDK build) | Real PVE answers `GET /storage/{id}` for a missing entry with `HTTP 500: storage 'fast' does not exist` — not a 404. The identical wart as the missing ACME plugin GET (deviation 4), hidden the identical way: mockpve's clean 404 satisfied the `ErrNotFound` branch in every test. The dry-run UX held up this time — the check error surfaced as a WARN + `unknown`, not a fake `pending`. | Code fix `39e3642` (local, unpushed — rides the PR #30 workspace): existence resolves through `ListDatastores` at both call sites, the same structural sidestep as `f4ba8d5`; the index carries every compared field, digest included. mockpve 500-on-missing parity reported on proxmox-go-sdk PR #30 so the release ships with the mock telling the truth. |
| 2026-08-27 | Phase 3 boot-network setup | **Prerequisite deviation, not a failure**: the runbook demands "a trusted, isolated boot network", but no such segment exists — `10.10.14.x` was never real. The boot network is the Servers VLAN (11, `10.10.11.0/24`), shared with the PVE hosts, booty on `ns1` (`10.10.11.190`), and the Talos VMs' reservations (`.51–.53`, `.61–.63`). Accepted deliberately with both consequences understood: `/machine-config` serves cluster PKI and join tokens over plaintext HTTP on that VLAN, and booty's proxyDHCP answers **every** PXE attempt on the segment (unconfigured MACs chainload `ipxe.efi`, get a 404, and drop to an iPXE shell — no other machine on Servers can netboot for its own purposes while booty runs). | Runbook prerequisite amended from "isolated" to the real requirement — a segment trusted end-to-end, with the two consequences spelled out. The Phase 3 delivery comparison also landed: the lab serves the emitted tree via an ansible-managed compose that is flag-for-flag equivalent to `booty-run.sh`; the runbook now states the launcher's flag table is the contract and any delivery preserving it is equivalent. |
| 2026-08-28 | `bootstrap talos vms` (first contact) | Real PVE answers `GET /nodes/<node>/qemu/<vmid>/status/current` for a missing VM with `HTTP 500: Configuration file 'nodes/r740a/qemu-server/201.conf' does not exist` — not a 404. **Third instance of the deviation 4/8 class** (by-ID GET on a missing resource → 500), hidden the identical way: mockpve's clean 404 satisfied the `ErrNotFound` branch in every test, so the first live Check died before creating anything. | Code fix: VM existence and power state both resolve through the node's `qemu` index (`List`) via a shared `findVM` — the same structural sidestep as the ACME-plugin and storage fixes; the index entry carries the power state, so the wart-prone by-ID GET is gone from the stage entirely. Regression test wraps mockpve in a wart shim that reproduces PVE's 500-on-missing for `status/current` and fails pre-fix with character-for-character the live error. mockpve 500-on-missing parity for `status/current` queued for the Phase 6 SDK filing alongside deviations 4 and 8. |
| 2026-08-28 | First VM boot after `talos vms` | All six VMs PXE'd cleanly (proxyDHCP offer → boot-ack → TFTP `ipxe.efi`, every VMSpec setting doing its job) and then dropped to an iPXE shell: `Network unreachable` (`ENETUNREACH`, iPXE `280a6090`) fetching from booty, no `Configuring (net0 …)` line ever printed. The emit stage had embedded the **wrong script**: booty's *served* identity-forwarding chain script (`/ipxe?mac=…`, via `render.ChainScript`), chosen so the embedded and served scripts could never disagree. That script is correct only in its own delivery mode — fetched after DHCP has run. An embedded script runs **in place of** iPXE's autoboot, the thing that would have configured the NIC, so net0 had no address and the first fetch died. booty's walkthrough documents the real embed — `dhcp`, then `chain …/boot.ipxe` — and calls the dhcp line load-bearing; its failure catalog even scripts the recovery ("you are *in* the debugger — `dhcp`, then `chain` the URL by hand"). Never caught earlier because nothing before this moment executed the embedded script: unit tests compare bytes, and the step-7 curls exercised booty's endpoints, not iPXE's boot path. | Code fix: `renderEmbedIPXE` now renders booty's documented two-hop embed — `dhcp \|\| goto failed`, `chain <booty>/boot.ipxe \|\| goto failed` — instead of embedding the served script. The agreement rationale inverted rather than weakened: chaining to `/boot.ipxe` makes it structural, since the identity script that actually runs is always the one booty serves. Regression `TestEmbedScriptConfiguresNICBeforeFetch` pins dhcp-before-first-fetch and fails pre-fix; golden embed.ipxe updated. Recovery on the live VMs: manual `dhcp` + `chain http://10.10.11.190:8080/boot.ipxe` at the shell (the catalog's own procedure), then re-emit → `talos ipxe` rebuild → re-sync → reset the rest. |
| 2026-08-28 | First VM boot, install phase | The manually-chained ctrl01 fetched everything, applied its config — and 48 seconds after `machine-config served` was back at proxyDHCP. Console: `error running phase 2 in install sequence: task 1/1: failed, lstat /dev/sda: no such file or directory` → `rebooting in 10 seconds`. The disk does not exist inside the guest: `VMSpec` never set `scsihw`, and PVE's **API** default is the emulated LSI 53C895A, for which the Talos kernel ships no driver (`# CONFIG_SCSI_SYM53C8XX_2 is not set` in siderolabs/pkgs, vs `CONFIG_SCSI_VIRTIO=m`). A **new deviation class**, distinct from 4/8/10: the API's defaults diverge from the UI wizard's — booty's walkthrough VM was built in the UI, which silently defaults to VirtIO SCSI, so the reference procedure could never expose it, and no host-side check can either (the VM reads as healthy from PVE; only a live guest falsifies the spec). Triple-confirmed: kernel config, PVE hardware panel showing the LSI, and the console lstat. | Code fix: `VMSpec` pins `scsihw=virtio-scsi-single` (the wizard's default, the configuration the walkthrough was actually validated on). Regression `TestVMSpecPinsVirtIOSCSI` fails pre-fix. Live remediation: the six VMs' disks are still empty, so destroy and let the fixed create path rebuild them — better acceptance evidence than `qm set` patching, since the re-run exercises the shipped spec end-to-end. Lesson for the spec's comment block: every VMSpec field exists because its PVE default breaks the boot invisibly; `scsihw` joins the list. |
| 2026-08-28 | `bootstrap talos bootstrap` | The stage reported "✓ cluster bootstrapped (0 steps applied)" against a cluster that was **never bootstrapped**: the `etcd-bootstrap` Check probed by fetching a kubeconfig on the assumption it "only succeeds once etcd is up and the API server answers." Talos actually **generates** the kubeconfig locally from the cluster PKI in the machine config — apid signs an admin certificate; no etcd, no API server — so on any healthy waiting node the Check read "done" and the stage skipped the one step it exists to run. The full-night consequence: the first run silently skipped, `talos health` hung at 01:07 on etcd `Preparing` ×3, the control planes spent ~6 h reboot-cycling in an un-bootstrapped boot sequence, and a retry that happened to land in a reboot slice (06:58, `connection refused`) was the only run that even *tried* to apply. Every unit test had seeded the probe with a failing Kubeconfig — the same blindness shape as the mockpve deviations, this time home-grown: no test modeled the true Talos behavior. | Code fix: `Client` grows `EtcdMemberList` (the machinery call verbatim); the Check now probes etcd membership, which only a bootstrapped etcd can answer — and a false *pending* stays harmless because Apply already treats `FailedPrecondition: etcd data directory is not empty` as success. The interface's Kubeconfig comment now states the local-generation fact so the next probe author doesn't repeat it. Regression `TestBootstrapProbesEtcdNotKubeconfig` models the real cluster (kubeconfig always available, etcd never bootstrapped) and fails pre-fix with the live symptom: bootstrap never issued. Confirmed live before the fix with raw `talosctl health`: etcd `Preparing` on all three CPs while the CLI claimed completion. **Second round:** the fixed probe's first live run hung silently after `write-talosconfig` — on an un-bootstrapped node machined's internal etcd client retries its local dial until the *caller's* deadline, and the Check passed none (the old kubeconfig probe could never hang: generation is local and instant, which is how the gap stayed invisible). Fix: the probe runs under a bounded context (`ProbeTimeout`, default 15 s); expiry reads as "pending", and Apply's `FailedPrecondition`-is-success rule keeps a false pending harmless. Regression `TestBootstrapProbeCannotHang` models the blocking server and fails pre-fix via a 5 s watchdog. Along the way the console's repeating `KubeletStaticPodController … tls: internal error` was identified as the *expected* pre-bootstrap consequence of `rotate-server-certificates` (the kubelet's serving cert needs CSR approval; the approver is a Kubernetes deployment; Kubernetes does not exist yet) — noise that clears once the approver deploys, not a finding. |
| 2026-08-28 | `bootstrap talos health` after etcd | Predicted at first boot, confirmed by the health gate: the base extension profile ships **qemu-guest-agent**, whose service talks to a virtio-serial channel that only exists when the VM config enables the agent — and `VMSpec` never set `agent`. The service can never start, `startAllServices` never completes, every node's machine stage sticks at `Booting`, and health fails its boot-sequence phase against an otherwise **fully healthy** cluster (etcd ×3, API server/controller-manager/scheduler Healthy, nodes `READY True` — Cilium demonstrably delivering, since a CNI-less node cannot go Ready; kubelet TLS noise ended the moment the cert-approver deployed). Second member of the deviation-12 class: a guest-visible VM-config gap no host-side check or mock can see — the walkthrough never hit it because its VM had no guest-agent extension at all; this drill's base profile added the extension without the channel it needs. | Code fix: `VMSpec` pins `agent=enabled=1` (property form deliberately: a bare `1` round-trips out of the config read as a JSON number and the SDK types the field as string — caught by the existing wire round-trip test against mockpve). Regression `TestVMSpecEnablesGuestAgent` fails pre-fix. Live remediation differs from deviation 12's destroy-recreate because the nodes now hold etcd and cluster state: `qm set <vmid> --agent enabled=1` plus a cold stop/start per VM (the channel is cold-plug), control planes rolled one at a time to hold etcd quorum, workers freely. Convergence note recorded: the CLI's `vm-create` Check is existence-based and will not retrofit the setting onto pre-fix VMs — this cluster's six are hand-patched; any future create carries it. |

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
