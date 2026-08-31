---
id: IMPL-0003
title: "Talos storage plane"
status: In Progress
author: Donald Gifford
created: 2026-08-31
---

<!-- markdownlint-disable-file MD024 MD025 MD041 MD046 -->

# IMPL-0003: Talos storage plane

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [The authoritative table](#the-authoritative-table)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Image — util-linux-tools (operator)](#phase-1-image--util-linux-tools-operator)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Hand layer — storage NICs live (operator)](#phase-2-hand-layer--storage-nics-live-operator)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Config surface (code)](#phase-3-config-surface-code)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: VM and emit surface (code)](#phase-4-vm-and-emit-surface-code)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Release and convergence acceptance](#phase-5-release-and-convergence-acceptance)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Rebirth proof and close](#phase-6-rebirth-proof-and-close)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Open Questions](#open-questions)
- [Dependencies](#dependencies)
- [References](#references)
<!--toc:end-->

## Objective

Give the six fartlab nodes their storage plane — `net1` on the
Storage VLAN (14) with static machineconfig addressing and the iSCSI
extensions — via INV-0002's Option C: the operator's hand layer
first, whose verified end state becomes the executable spec the
bootstrap CLI then learns to reproduce, proven by a §15 re-image that
needs no hands.

**Implements:** INV-0002 (Concluded 2026-08-31, Option C)

## Scope

### In Scope

- The base profile gaining `siderolabs/util-linux-tools` and the
  rolling upgrade that puts it on live nodes (config-only, Phase 1).
- The hand-applied storage plane on the live cluster (Phase 2) and
  the tool surface that makes it reproducible (Phases 3–4): per-node
  storage NIC in config, `net1` in the VM spec, storage vars and the
  `machine.network.interfaces` section in emit.
- `tools/bootstrap/v0.3.0` and the two acceptance proofs: the
  zero-mutation convergence loop and the single-worker rebirth.

### Out of Scope

- democratic-csi itself — this IMPL ends where the CSI's
  prerequisites (reachable portals, iscsid, util-linux-tools) begin.
- Jumbo frames. MTU stays unset (1500) fleet-wide; a jumbo decision
  is its own deliberate, both-sided change.
- Generic N-NIC support beyond the storage plane (see OQ-1's option
  c — deliberately not chosen until a third NIC exists).
- The storage portals and their VLAN-14 configuration (TrueNAS side).

## The authoritative table

The config-first rule from INV-0002: these values are the single
source. Phase 2's hand commands consume them verbatim, and Phase 5's
config adopts them verbatim — the two paths cannot disagree on a MAC.
net1 MACs follow the fifth-octet-14 scheme (VLAN readable in the
MAC), last octet preserved from net0.

| Node | VMID | Node IP (VLAN 11) | net0 MAC | net1 MAC | Storage address (VLAN 14) |
| --- | --- | --- | --- | --- | --- |
| ctrl01 | 201 | 10.10.11.51 | 02:50:99:a2:00:c9 | 02:50:99:a2:14:c9 | 10.10.13.51/24 |
| ctrl02 | 202 | 10.10.11.52 | 02:50:99:a2:00:ca | 02:50:99:a2:14:ca | 10.10.13.52/24 |
| ctrl03 | 203 | 10.10.11.53 | 02:50:99:a2:00:cb | 02:50:99:a2:14:cb | 10.10.13.53/24 |
| work01 | 301 | 10.10.11.61 | 02:50:99:a2:01:2d | 02:50:99:a2:14:2d | 10.10.13.61/24 |
| work02 | 302 | 10.10.11.62 | 02:50:99:a2:01:2e | 02:50:99:a2:14:2e | 10.10.13.62/24 |
| work03 | 303 | 10.10.11.63 | 02:50:99:a2:01:2f | 02:50:99:a2:14:2f | 10.10.13.63/24 |

Schematic after Phase 1: `<recorded on Phase 1 completion>`
(today: `dc7b152c…8586`).

Two MAC schemes coexist here on purpose: net0's last two octets are
`<vmid-hex>` (`00:c9` = 201, `01:2d` = 301), while net1 repurposes
the fifth octet as the VLAN (`14`). They can never collide — a vmid
would need to reach 0x1400 = 5120, and the VMID convention caps at
999.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all
its tasks are checked off and its success criteria are met.
**Phases 1–2 are the operator's and deliberately block Phases 3–4**:
their verified end state is the spec the code must reproduce.

---

### Phase 1: Image — util-linux-tools (operator)

The config-only half from INV-0002 — democratic-csi wants
util-linux-tools beside the iscsi-tools already aboard.

#### Tasks

- [ ] Split the profiles in `~/drill/bootstrap.hcl`: `base` keeps
      qemu-guest-agent only; a new `profile "iscsi"` carries
      iscsi-tools + util-linux-tools; every node's `profiles`
      becomes `["base", "iscsi"]`. (The schematic derives from each
      node's flattened union, so this produces the same new image as
      editing base — better organized, zero extra image impact.)
- [ ] `bootstrap talos emit` — a new schematic is derived and new
      boot assets download; record the new schematic ID in the table
      above
- [ ] rsync the tree to ns1 and restart booty (the served
      `install_image` and PXE assets now reference the new schematic)
- [ ] Rolling upgrade, one node at a time, control planes first,
      health green between nodes:

      ```sh
      talosctl -n <node-ip> upgrade \
        --image factory.talos.dev/installer/<new-schematic>:v1.13.8
      ```

- [ ] Verify on one control plane and one worker:
      `talosctl get extensions -n <ip>` lists iscsi-tools,
      util-linux-tools, qemu-guest-agent, and the new schematic ID

#### Success Criteria

- All six nodes report the new schematic; `util-linux-tools` present
  beside `iscsi-tools`.
- `bootstrap talos health` green.
- The emitted tree serves the new `install_image` — a §15 re-image
  would come back on the new image without further action.
- A full stage loop after the emit applies zero (the one apply was
  the emit that introduced the change).

---

### Phase 2: Hand layer — storage NICs live (operator)

Every value below comes from the authoritative table. This phase's
verified end state is the executable spec for Phases 3–4 — the code
is done when it reproduces exactly this, from config alone.

#### Tasks

- [ ] `qm set` on each VM's hosting PVE node (PVE hot-plugs NICs by
      default — confirm each with `talosctl -n <ip> get links`
      showing a new link with the net1 MAC; if a link doesn't
      appear, a rolling `talosctl reboot` picks it up):

      ```sh
      qm set <vmid> --net1 virtio=<net1-mac>,bridge=vmbr1,tag=14,firewall=0
      ```

- [ ] Patch each node's machineconfig — both NICs declared, selection
      by `deviceSelector.hardwareAddr` only, net0 explicit `dhcp`,
      net1 static with **no routes, no gateway, no DNS**:

      ```yaml
      # storage-patch-<node>.yaml — values from the table
      machine:
        network:
          interfaces:
            - deviceSelector:
                hardwareAddr: "<net0-mac>"
              dhcp: true
            - deviceSelector:
                hardwareAddr: "<net1-mac>"
              dhcp: false
              addresses:
                - "<storage-address>"
      ```

      ```sh
      talosctl -n <node-ip> patch machineconfig -p @storage-patch-<node>.yaml
      ```

- [ ] Verify per node: `talosctl -n <ip> get addresses` shows the
      table address on the net1 link; iscsid is present
      (`talosctl -n <ip> services` → ext service running)
- [ ] Verify the boundary held: `talosctl -n <ip> get routes` shows
      **no** default route via 10.10.13.0/24 — net1's unroutability
      is the storage plane's access boundary
- [ ] Verify reachability: a storage portal answers on VLAN 14 from
      at least one node (hostNetwork debug pod + `nc`, or the first
      democratic-csi session — either counts)
- [ ] Verify invisibility: a full bootstrap stage loop applies zero —
      the hand layer is invisible to the tool's checks

#### Success Criteria

- All six nodes hold their table address on the VLAN-14 NIC,
  selected by MAC, with cluster health green throughout.
- No routing change rode along (the unroutability boundary is
  intact); MTU untouched at 1500 everywhere.
- The convergence loop still applies zero.
- **Gate**: Phases 3–4 do not start until every box above is checked
  — this state is their spec.

---

### Phase 3: Config surface (code)

The tool learns the network surface OQ-1 decided. Blocked on
Phase 2's gate (the OQs are decided).

#### Tasks

- [ ] Cluster-level `network "<name>"` blocks: `vlan`, `dhcp`
      (required — every plane states its mode), `primary`, `cidr`
      (required iff `dhcp = false`)
- [ ] Per-node `network_interface "<netN>"` blocks: `network` (plane
      reference), `mac`, `bridge`, `address` — **replacing** the
      flat `mac`/`vlan`/`bridge` node attrs (the breaking change
      OQ-1 accepted; validation errors guide the migration)
- [ ] Validation, one rule at a time: unique plane names; exactly
      one plane `primary = true`; every interface references a
      declared plane; every node has exactly one interface on the
      primary plane; dhcp plane → `address` forbidden; static plane
      → `address` required, CIDR form, contained in the plane's
      `cidr`; global MAC uniqueness across all interfaces; interface
      labels `net\d+`, unique per node
- [ ] Load tests: parse + one test per validation error, drill-style
- [ ] Migrate `examples/bootstrap.hcl` and test fixtures to the new
      surface, documenting it in place

#### Success Criteria

- `just bootstrap-test` and `bootstrap-lint` green.
- A single-interface config (primary plane only) produces identical
  *behavior* to v0.2.0 — syntax broke, artifacts didn't.
- The example config stays valid (`TestLoadExampleConfig`).

---

### Phase 4: VM and emit surface (code)

The two render paths — PVE VM and machineconfig — learn to produce
Phase 2's end state.

#### Tasks

- [ ] `VMSpec` renders every declared interface in slot order —
      `bridge` from the interface, `tag=` from its plane's `vlan`;
      **boot order carries only the primary-plane interface's slot**
      — regression test pinning `order=scsi0;net0` with a second
      NIC present
- [ ] Emit catalog: per-group interface vars (each interface's MAC;
      static addresses) beside `hostname`
- [ ] Machineconfig templates: `machine.network.interfaces` rendered
      from the planes — the primary-plane interface by
      deviceSelector with `dhcp: true`, static-plane interfaces by
      deviceSelector with their address, no routes — emitted only
      when a node has more than its primary interface (OQ-2)
- [ ] Round-trip test: rendered multi-interface configs re-validate
      in machinery metal mode (the existing round-trip pattern)
- [ ] Golden files: single-interface fixtures byte-identical (the
      back-compat proof); new goldens for a multi-interface fixture
- [ ] Docs in the same change: runbook §1 (config surface), §10
      (vms expected fields), §15 (note that rebirth now covers the
      storage plane); example config final pass

#### Success Criteria

- Full battery green: race tests, lint, goldens, build.
- The emitted `interfaces:` block for a storage node is
  **shape-identical** to a live node's hand-patched section (diff
  against `talosctl -n <ip> get machineconfig` output for one node).
- Single-interface emit output is byte-identical to v0.2.0's.

---

### Phase 5: Release and convergence acceptance

The INV-0002 invariant, proven: running everything after the code
change must not break or change the cluster.

#### Tasks

- [ ] PR merged with `dont-release`; dispatch `tools-release.yml`
      tool=`bootstrap` version=`v0.3.0`; verify tag + archives
- [ ] Operator: add the storage surface to `~/drill/bootstrap.hcl`
      with the authoritative table's exact values
- [ ] Operator: full stage loop from the released v0.3.0 binary —
      expected shape: `emit` applies once (artifact drift only:
      machine-configs gain the interfaces block, catalog gains the
      vars), `ipxe` 0, `vms` 0 (no retrofit, by design),
      `bootstrap` skips, `health` green
- [ ] rsync + restart booty; re-run the loop — zero everywhere
- [ ] Confirm live cluster state untouched (nodes, workloads, ArgoCD
      apps — nothing restarted, nothing changed)

#### Success Criteria

- Zero cluster mutations across both loop runs; the only writes were
  artifact-tree files.
- booty now serves storage-aware machineconfigs — the config, the
  live nodes, and the served artifacts all agree.

---

### Phase 6: Rebirth proof and close

The real question from INV-0002, answered the way the rename window
answered the rebuild's.

#### Tasks

- [ ] §15 spot check on **one worker** (e.g. work03/303): stop,
      destroy, stage loop — the node must come back with its storage
      NIC, its table address, and iscsid, from config alone,
      indistinguishable from its hand-patched siblings
- [ ] Convergence loop after the rebirth: zero
- [ ] Any deviation found → recorded here and folded back
      (INV-0001 discipline; a substantial one opens its own INV)
- [ ] Runbook markers updated where this IMPL touched sections
- [ ] This doc: all boxes checked, status → **Completed**

#### Success Criteria

- The reborn worker's storage plane needed zero hand steps.
- `kubectl get nodes` 6/6 Ready; health green; workloads rescheduled.
- The doc chain is consistent: INV-0002 Concluded, this doc
  Completed, runbook current.

## File Changes

| File | Action | Description |
| ---- | ------ | ----------- |
| `internal/config/config.go` | Modify | `network` planes + `network_interface` surface (OQ-1) |
| `internal/config/validate.go` | Modify | plane/interface cross-rules, one-primary, cidr containment, MAC uniqueness |
| `internal/config/load_test.go` | Modify | parse/default/error coverage |
| `internal/pve/vms.go` | Modify | all-interface rendering; boot order pinned to the primary slot |
| `internal/pve/vms_test.go` | Modify | multi-NIC + boot-order regression tests |
| `internal/emit/catalog.go` | Modify | per-interface group vars |
| `internal/talos/machineconfig.go` | Modify | interfaces section in role templates |
| `internal/emit/testdata/golden/**` | Modify | storage fixture goldens; legacy untouched |
| `examples/bootstrap.hcl` | Modify | document the surface |
| `docs/runbook/bootstrap-cluster.md` | Modify | §1, §10, §15 notes |

## Testing Plan

- [ ] Load tests per validation rule (drill-style, one failing input
      each)
- [ ] `VMSpec` regression tests: net1 fields, boot order unchanged
- [ ] Template round-trip in machinery metal mode with storage vars
      substituted
- [ ] Golden byte-identity for storage-less fixtures — the
      back-compat contract as a test
- [ ] The two live proofs (Phases 5–6) stay out of CI, recorded here
      (IMPL-0001's decision: the e2e drill is not a merge gate)

## Open Questions

Numbered for decision; **a is the recommendation**, later letters are
alternatives, "other" is yours to write in.

**OQ-1 — Where does the storage surface live in the config?**

**Decided (2026-08-31): d** — a fourth shape reached in review,
combining a's declare-the-plane-once with c's generality, on the
operator's principle that the CLI is explicit primitives (the future
service abstracts on top, and the primitives stay reachable). Named
`network` blocks carry the plane facts; per-node `network_interface`
blocks carry only identity, referencing their plane the same way
nodes already reference `pve_node` and `storage`:

```hcl
network "servers" {
  vlan    = 11
  primary = true
  dhcp    = true
}

network "storage" {
  vlan = 14
  dhcp = false
  cidr = "10.10.13.0/24"
}

node "ctrl01" {
  # …
  profiles = ["base", "iscsi"]

  network_interface "net0" {
    network = "servers"              # PXE, booty identity, DHCP
    mac     = "02:50:99:a2:00:c9"
    bridge  = "vmbr1"
  }
  network_interface "net1" {
    network = "storage"
    mac     = "02:50:99:a2:14:c9"
    bridge  = "vmbr1"
    address = "10.10.13.51/24"       # static — VLAN 14 has no DHCP
  }
}
```

The `network` reference on each interface is the load-bearing wire:
`tag=` derives from the plane's vlan, boot order and booty identity
from whichever interface sits on the primary plane, machineconfig
`dhcp: true` vs `addresses:` from the plane's mode. This is a
**breaking config change** — the flat per-node `mac`/`vlan`/`bridge`
attrs are replaced — accepted deliberately at v0.3.0 with one config
in existence.

Superseded options, for the record:

- ~~a: `storage_network` + per-node `storage` block — hardcoded
  storage semantics into a tool that shouldn't know what a NIC is
  for.~~
- ~~b: fully per-node attrs — six chances to typo the VLAN.~~
- ~~c: generic `nic` blocks without the plane concept — genericity
  without the declare-once property.~~

**OQ-2 — What do storage-less configs emit once the feature
exists?**

**Decided (2026-08-31): a**, restated for the generic model — a node
whose only interface sits on the primary (dhcp) plane emits **no**
`interfaces:` block: behaviorally byte-identical artifacts to v0.2.0
despite the config-syntax break, provable as golden identity. The
block appears — with the primary interface declared `dhcp: true` in
it — as soon as a node has any second interface.

- ~~b: always emit the block — golden churn and re-image behavior
  change for nothing functional.~~

**OQ-3 — Must every node declare storage, or may some?**

**Dissolved by OQ-1's decision (2026-08-31)**: under the generic
model there is no "storage plane" for the tool to enforce uniformity
on — a node with only its primary interface is valid config, and
partial fleets are legal. The trade is accepted eyes-open: the
authoritative table above is now the fleet-uniformity check, held by
the operator, not the tool.

**OQ-4 — Address form in the config?**

**Decided (2026-08-31): a**, folded into the OQ-1 shape — full CIDR
on interface `address`, and a `cidr` on each static plane that
validation checks containment against. No hidden prefix anywhere; a
future /23 storage net is a config edit.

- ~~b: bare IP + hardcoded /24.~~ ~~c: bare IP + prefix attr.~~

**OQ-5 — Does the tool enforce the net1 MAC scheme?**

**Decided (2026-08-31): a** — the fifth-octet-14 scheme stays a
documented convention (this doc's table); validation requires only
well-formed, globally unique MACs. Conventions in docs, invariants
in code.

- ~~b: derive net1 MACs automatically — magic, and a scheme change
  becomes a code change.~~

## Dependencies

- INV-0002 Concluded (Option C) — the analysis this executes.
- The live fartlab cluster (rebuilt 2026-08-30) and its ArgoCD state
  — Phase 6 deliberately re-images only one worker.
- DESIGN-0006's corrected address table (homelab docs) — the
  authoritative table above is the in-repo copy.
- Storage portals live on VLAN 14 (TrueNAS side) for Phase 2's
  reachability check.

## References

- INV-0002 — the investigation and Option C decision
- DESIGN-0002 — the completion surface the interfaces emit extends
- `docs/runbook/bootstrap-cluster.md` §13 (convergence), §15
  (re-image — the rebirth invariant)
- DESIGN-0006 / DESIGN-0009 (homelab docs) — address table, port map
- Talos machineconfig `deviceSelector` documentation
- democratic-csi prerequisites: `siderolabs/iscsi-tools`,
  `siderolabs/util-linux-tools`
