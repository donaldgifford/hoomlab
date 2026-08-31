---
id: IMPL-0003
title: "Talos storage plane"
status: In Progress
author: Donald Gifford
created: 2026-08-31
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

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

- [ ] Add `"siderolabs/util-linux-tools"` to `profile "base"` in
      `~/drill/bootstrap.hcl`
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

The tool learns the storage plane's shape. Blocked on Phase 2 and on
OQ-1–OQ-5 decisions.

#### Tasks

- [ ] Config surface per OQ-1: the storage plane declared once, node
      identity (MAC + address) per node
- [ ] Validation: MAC uniqueness across the net0 *and* storage sets
      together; address shape (OQ-4) and uniqueness; the
      all-or-none rule (OQ-3); plane block required iff any node
      declares storage
- [ ] Load tests: parse + defaults, and one test per validation
      error, drill-style
- [ ] `examples/bootstrap.hcl` documents the surface in place
- [ ] Confirm configs *without* storage load and behave
      byte-identically (no new required anything)

#### Success Criteria

- `just bootstrap-test` and `bootstrap-lint` green.
- A storage-less config produces identical behavior to v0.2.0 —
  proven by untouched goldens and passing existing tests, unmodified.
- The example config stays valid (`TestLoadExampleConfig`).

---

### Phase 4: VM and emit surface (code)

The two render paths — PVE VM and machineconfig — learn to produce
Phase 2's end state.

#### Tasks

- [ ] `VMSpec` renders `net1` from the storage surface (bridge/VLAN
      from the plane, MAC per node); **boot order unchanged** —
      regression test pinning `order=scsi0;net0` with net1 present
- [ ] Emit catalog: `storage_mac` / `storage_ip` per-group vars
      beside `hostname`
- [ ] Machineconfig templates gain the `machine.network.interfaces`
      section — net0 by deviceSelector with `dhcp: true`, net1 by
      deviceSelector with the static address, no routes — emitted
      only when storage is declared (OQ-2)
- [ ] Round-trip test: rendered storage configs re-validate in
      machinery metal mode (the existing round-trip pattern)
- [ ] Golden files: storage-less fixtures byte-identical (the
      back-compat proof); new goldens for a storage-enabled fixture
- [ ] Docs in the same change: runbook §1 (config surface), §10
      (vms expected fields), §15 (note that rebirth now covers the
      storage plane); example config final pass

#### Success Criteria

- Full battery green: race tests, lint, goldens, build.
- The emitted `interfaces:` block for a storage node is
  **shape-identical** to a live node's hand-patched section (diff
  against `talosctl -n <ip> get machineconfig` output for one node).
- Storage-less emit output is byte-identical to v0.2.0's.

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
| `internal/config/config.go` | Modify | storage plane + per-node identity surface (OQ-1) |
| `internal/config/validate.go` | Modify | cross-set MAC uniqueness, address rules, all-or-none |
| `internal/config/load_test.go` | Modify | parse/default/error coverage |
| `internal/pve/vms.go` | Modify | net1 rendering; boot order pinned |
| `internal/pve/vms_test.go` | Modify | net1 + boot-order regression tests |
| `internal/emit/catalog.go` | Modify | storage_mac / storage_ip group vars |
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

- **a (recommended):** the plane declared once, identity per node —
  `talos { storage_network { bridge = "vmbr1"  vlan = 14 } }` plus a
  per-node `storage { mac = "…"  address = "10.10.13.51/24" }`
  block. One place for the plane means no per-node VLAN typos;
  per-node blocks carry only what actually differs. Validation ties
  the two: `storage_network` required iff any node declares
  `storage`.
- b: fully per-node (mac, address, vlan, bridge on every node),
  mirroring how net0's bridge/vlan already work — more uniform with
  the existing surface, six chances to typo the VLAN.
- c: generic repeatable `nic "<name>" {}` blocks supporting arbitrary
  extra NICs — the most general shape, and speculative until a third
  NIC exists (out of scope by INV-0002).

**OQ-2 — What do storage-less configs emit once the feature
exists?**

- **a (recommended):** exactly what they emit today — no
  `interfaces:` block at all; the block (with net0 then declared
  explicitly, per the spec rule) appears only when storage is
  declared. Back-compat is provable as golden byte-identity, and
  existing clusters see zero re-image behavior change.
- b: always emit `interfaces:` with net0 `dhcp: true` declared, even
  without storage — more explicit everywhere, but churns every
  golden, changes re-image behavior for running clusters, and buys
  nothing functional.

**OQ-3 — Must every node declare storage, or may some?**

- **a (recommended):** all-or-none, enforced at validation — a
  half-storage fleet today is almost certainly a config mistake, and
  the error message costs nothing. Relax it when a genuinely mixed
  node class exists (that change is trivial; the reverse — cleaning
  up after a silent half-fleet — is not).
- b: allow partial from day one; nodes without `storage` simply get
  no interfaces block.

**OQ-4 — Address form in the config?**

- **a (recommended):** full CIDR (`"10.10.13.51/24"`) — it lands
  verbatim in the machineconfig `addresses:` list, no hidden prefix
  assumption, and a future /23 or /16 storage net needs no tool
  change.
- b: bare IP with `/24` hardcoded in the tool.
- c: bare IP per node + a `prefix` attribute on `storage_network`.

**OQ-5 — Does the tool enforce the net1 MAC scheme?**

- **a (recommended):** no — the fifth-octet-14 scheme stays a
  documented convention (this doc's table); validation requires only
  well-formed, unique MACs across both NIC sets, same rules as net0.
  Conventions in docs, invariants in code.
- b: derive net1 MACs from net0 automatically (fifth octet → 14) —
  less config, more magic, and a scheme change becomes a code
  change.

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
