---
id: INV-0002
title: "Talos storage plane - second NIC and iSCSI readiness"
status: Open
author: Donald Gifford
created: 2026-08-31
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0002: Talos storage plane - second NIC and iSCSI readiness

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Requirements](#requirements)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [What v0.2.0 already covers](#what-v020-already-covers)
  - [What has no surface today](#what-has-no-surface-today)
  - [Convergence behavior that shapes the options](#convergence-behavior-that-shapes-the-options)
- [Options](#options)
- [Open Questions](#open-questions)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

Can the released bootstrap CLI (`tools/bootstrap/v0.2.0`), with **no
code changes**, give the six Talos nodes their storage plane — a
second NIC on the Storage VLAN (14) with static addressing and the
iSCSI extensions democratic-csi needs — and if not, what is the right
split between operator action now and tool fold-back?

## Hypothesis

Partially. The extensions half is config-only (the profile mechanism
already exists, and the base profile already carries iscsi-tools);
the NIC halves — the VM's `net1` and the machineconfig
`interfaces:` block — have no config surface and can only be reached
today by hand actions that the convergence model tolerates but the
§15 re-image erases. Expect the answer to be a phased hybrid.

## Context

First post-drill feature need. Storage for the fartlab cluster is
iSCSI over the Storage VLAN (14) to the portals; the requirements
spec below is the operator's, with the address table recorded in
DESIGN-0006 (homelab docs, alongside DESIGN-0009's port map). The
network side is already proven: the drill's Phase 1 verification
showed all three `vm0` trunks passing tagged 11 + 14 probes.

**Triggered by:** democratic-csi groundwork on the rebuilt fartlab
cluster; DESIGN-0006 (homelab docs).

## Requirements

Per-VM NIC shape, all six nodes uniformly:

| NIC | PVE config | Notes |
| --- | --- | --- |
| net0 | `virtio=<mac>,bridge=vmbr1,tag=11` | PXE + node network. The **only** NIC in `boot: order=` — booty serves VLAN 11 only; a VM PXE-booting from net1 hangs in silence. |
| net1 | `virtio=<mac>,bridge=vmbr1,tag=14` | iSCSI path to the portals. Never in boot order, `firewall=0`, no MTU override. |

- Both MACs deterministic from the bootstrap config, like today's
  net0 MACs. Suggested net1 scheme: same last octet, fifth octet
  `14` reading as the VLAN — e.g. `02:50:99:a2:14:c9` for ctrl01.
  Booty group matching is untouched: iPXE substitutes the *booting*
  NIC's MAC, which is always net0.
- VLAN 14 has no DHCP and no gateway → net1 is **static in the
  machineconfig**: ctrl01–03 = `10.10.13.51–.53`, work01–03 =
  `10.10.13.60–.62` (DESIGN-0006's table; see the mirror question
  below).
- Machineconfig gains a `machine.network.interfaces` section, with
  the load-bearing rules:
  - select by `deviceSelector.hardwareAddr`, never interface names
    (virtio PCI enumeration order is not a contract);
  - **no routes, no gateway, no DNS on net1** — its unroutability is
    the storage plane's access boundary, and a stray default route
    here creates asymmetric-routing misery;
  - once an `interfaces:` block exists, list net0 explicitly too
    (`dhcp: true`) so behavior is declared, not inherited;
  - MTU stays unset (1500) fleet-wide until a deliberate jumbo
    decision — a one-sided 9000 is the classic silent iSCSI killer.
- Image carries `siderolabs/iscsi-tools` **and**
  `siderolabs/util-linux-tools` (democratic-csi wants both).

## Approach

1. Read the v0.2.0 surfaces against each requirement: config schema
   (NIC and extension fields), `VMSpec`, the emit catalog's per-group
   vars, and the machineconfig templates' network content.
2. Determine what the convergence model does with hand-applied
   equivalents (a `qm set` NIC, a `talosctl patch` interfaces block)
   on re-run and on §15 re-image.
3. Verify the live image's extension inventory
   (`talosctl get extensions`).
4. Weigh the options and pick the split.

## Environment

| Component | Version / Value |
| --------- | --------------- |
| bootstrap | `tools/bootstrap/v0.2.0` |
| Cluster | fartlab (Talos) on shart (PVE), rebuilt 2026-08-30 per runbook §15 |
| Schematic | `dc7b152c…8586` — profile "base" |
| Network | Storage VLAN 14 on the `vm0` trunks, verified tagged in the drill prep |

## Findings

### What v0.2.0 already covers

- **iscsi-tools is already in the image.** The drill config's profile
  reads:

  ```hcl
  profile "base" {
    extensions = [
      "siderolabs/qemu-guest-agent",
      "siderolabs/iscsi-tools",
    ]
  }
  ```

  The schematic is derived from exactly this set, so `dc7b152c…`
  ships iscsid today. `util-linux-tools` is **absent** — adding it is
  a one-line profile edit: config-only, no code. The consequence is a
  new schematic → `talos emit` regenerates assets and the installer
  image reference → live nodes pick it up via a rolling
  `talosctl upgrade --image <new factory ref>` or the next re-image.
- **The MAC-pinned identity model extends cleanly.** Nothing about a
  second, never-booting NIC disturbs booty group matching.

### What has no surface today

- **VM `net1`**: `VMSpec` renders `Net0` only (`vms.go`); the config
  node block carries one `mac`/`vlan`. No amount of configuration
  produces a second NIC.
- **Machineconfig `interfaces:`**: the emitted role templates carry
  no `machine.network` section (nodes are implicit-DHCP), and the
  catalog's per-group vars are exactly `hostname` (+ the shared
  `install_image`). `storage_mac` / `storage_ip` vars and the
  interfaces section are new emit surface.

### Convergence behavior that shapes the options

- A hand-added `qm set <vmid> --net1 …` **survives** every bootstrap
  re-run: the vms Check is index-based existence and never retrofits
  or removes settings (the documented drill exception, INV-0001).
  It is **erased** by §15's destroy-and-rebuild.
- A `talosctl patch machineconfig` interfaces block **persists across
  reboots** (STATE partition) but is **erased by re-image** — the
  PXE-served machineconfig is the config source at install time, and
  it would not contain the block.
- Hand-editing the emitted tree is the worst of both: local edits are
  overwritten by the next `talos emit`, ns1 edits by the next rsync.
- Net effect: every no-code path works *now* and silently breaks the
  §15 rebirth invariant — a re-imaged node comes back with no
  storage NIC and no storage address, which is precisely the class of
  drift the drill was run to eliminate.

## Options

**A — no code, operator actions (works today, drifts tomorrow).**
Profile edit for util-linux-tools (config-only) + rolling upgrade;
`qm set --net1` ×6 with the MAC scheme; `talosctl patch` the
interfaces block ×6. Storage plane up in an afternoon. Cost: the VM
NIC and the interfaces block live outside the config's
single-source-of-truth, and §15 stops reproducing the nodes.

**B — fold the storage plane into the tool first.** Config: a
per-node storage NIC surface (mac + address); vms.go renders `net1`
(never in boot order); emit: catalog gains `storage_mac`/`storage_ip`
per group and the templates gain the `interfaces:` section with net0
declared `dhcp: true`. Validation: MAC/IP uniqueness across both NIC
sets. Deviation-fix-sized change, fully precedented. Cost: storage
work waits on a tool release.

**C — hybrid (A now, B before the next re-image).** Do A to unblock
democratic-csi groundwork — every piece of it is reversible and
invisible to bootstrap's checks. Fold B in behind it, then let the
next §15 window prove the tool reproduces the storage plane from
config alone, the same way the rename window proved the rebirth path.
The hand-applied layer becomes disposable scaffolding rather than
drift.

## Open Questions

- **The mirror discrepancy**: the spec says storage addresses mirror
  each node's Servers-VLAN octet, but work01–03 are `.61–.63` on
  VLAN 11 and the table gives `10.10.13.60–.62`. Typo in
  DESIGN-0006, or deliberate? Decide before anything is applied —
  renumbering a storage plane later is misery.
- Live verification pending: `talosctl get extensions -n 10.10.11.51`
  to confirm the image inventory matches the profile (iscsi-tools
  present, util-linux-tools absent).
- Timing: does net1 need to exist before democratic-csi lands, or is
  CSI the forcing function for the whole change set?
- MTU/jumbo stays a recorded deferral (1500 fleet-wide) until a
  deliberate, both-sided decision.

## Conclusion

**Answer:** <!-- pending — expected: partially; hybrid split -->

## Recommendation

<!-- pending the conclusion; expected shape is Option C with the
     mirror discrepancy resolved first -->

## References

- DESIGN-0006 / DESIGN-0009 (homelab docs) — storage address table,
  port map
- INV-0001 — the drill's convergence exceptions this analysis leans on
- `docs/runbook/bootstrap-cluster.md` §15 — the rebirth invariant at
  stake
- DESIGN-0002 — the completion surface the interfaces emit would
  extend
- democratic-csi extension requirements (iscsi-tools +
  util-linux-tools)
