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

**Implements:** INV-0002 (Concluded 2026-08-31, Option C) —
executing **DESIGN-0004** (network planes and interfaces, the
authoritative design record)

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
- ~~Jumbo frames.~~ *Amended 2026-09-01 — in scope.* The
  storbr0/port-profile decision (see the authoritative table's
  amendment) made jumbo both-sided and safe: Phase 2 carries the
  fabric work, Phases 3–4 the `mtu` surface.
- Generic N-NIC support beyond the storage plane (see OQ-1's option
  c — deliberately not chosen until a third NIC exists).
- The storage portals and their configuration (ZFS on r740a — the
  PVE host serves the portals over its `storbr0` address).

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

Schematic after Phase 1 (recorded 2026-09-01):
`88d1f7a5c4f1d3aba7df787c448c1d3d008ed29cfb34af53fa0df4336a56040b`
— flattened union `[iscsi-tools, qemu-guest-agent,
util-linux-tools]`. The pre-split image was `dc7b152c…8586`.

Two MAC schemes coexist here on purpose: net0's last two octets are
`<vmid-hex>` (`00:c9` = 201, `01:2d` = 301), while net1 repurposes
the fifth octet as the VLAN (`14`). They can never collide — a vmid
would need to reach 0x1400 = 5120, and the VMID convention caps at
999.

**Amended 2026-09-01 — the fabric decision.** net1 does not ride
the vmbr1 trunk: each host gained a dedicated `storbr0` bridge
enslaving its `stor0` NIC (done on r740a/r640a/srv01), and the
stor0 switch ports carry a storage-only profile — native storage
VLAN, **block all tagged**. So net1 renders **no `tag=`** (the port
profile is the access control, enforced at the switch, and a stray
tag would be dropped outright, not flooded), and the storage path
runs **MTU 9000** end-to-end — the deliberate both-sided jumbo
decision INV-0002's deferral was waiting on, made safe precisely
because the NIC, bridge, and ports are storage-only. Jumbo enables
at the UniFi aggregator (switch-wide), then each port/server end
opts in explicitly. The primary plane (net0, vmbr1, VLAN 11) stays
untouched at 1500.

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

- [x] Split the profiles in `~/drill/bootstrap.hcl`: `base` keeps
      qemu-guest-agent only; a new `profile "iscsi"` carries
      iscsi-tools + util-linux-tools; every node's `profiles`
      becomes `["base", "iscsi"]`. (The schematic derives from each
      node's flattened union, so this produces the same new image as
      editing base — better organized, zero extra image impact.)
- [x] `bootstrap talos emit` — a new schematic is derived and new
      boot assets download; record the new schematic ID in the table
      above (done 2026-09-01, after first re-verifying the
      reconstructed config against the served tree — schematic
      identity + checksum rsync — following the config-overwrite
      recovery)
- [x] rsync the tree to ns1 and restart booty (the served
      `install_image` and PXE assets now reference the new schematic).
      **Deviation found and fixed (2026-09-01)**: ns1's compose
      mounted a role-owned catalog frozen at `/etc/booty/catalog`
      (Aug 29 vintage — also the root cause of the rename-day
      mixed tree), so rsync + restart didn't update what booty
      served. Switched the mount to the emit-managed
      `/root/booty/catalog` (single owner; the role's catalog
      deploy dropped) — `rsync -a` + restart is now the entire
      update contract. Verified by `/ipxe?mac=` rendering the new
      schematic.
- [x] Rolling upgrade, one node at a time, control planes first,
      health green between nodes (done overnight 2026-09-01/02;
      full health battery green after):

      ```sh
      talosctl -n <node-ip> upgrade \
        --image factory.talos.dev/installer/<new-schematic>:v1.13.8
      ```

- [x] Verify on one control plane and one worker:
      `talosctl get extensions -n <ip>` lists iscsi-tools,
      util-linux-tools, qemu-guest-agent, and the new schematic ID
      (exceeded: all six nodes swept, uniform `88d1f7a5…`;
      ctrl01's full list shows util-linux-tools 2.42.2 aboard)

#### Success Criteria

- All six nodes report the new schematic; `util-linux-tools` present
  beside `iscsi-tools`.
- `bootstrap talos health` green.
- The emitted tree serves the new `install_image` — a §15 re-image
  would come back on the new image without further action.
- A full stage loop after the emit applies zero (the one apply was
  the emit that introduced the change).

**Phase 1 complete (2026-09-02).** All six nodes uniform on
`88d1f7a5…`; health battery green. The closing loop applied zero
cluster mutations — emit and vms nothing-to-do, `etcd-bootstrap`
skipped. Two local-only reconciliation applies rode along, both
artifacts of the workspace being rebuilt after the config
overwrite: `ipxe-build` (no stamp in the fresh tree; same booty
URL, equivalent binary) and the talosconfig/kubeconfig credential
writes (empty `out/`). Cluster state untouched.

---

### Phase 2: Hand layer — storage NICs live (operator)

Every value below comes from the authoritative table. This phase's
verified end state is the executable spec for Phases 3–4 — the code
is done when it reproduces exactly this, from config alone.

#### Tasks

- [x] Fabric first — jumbo is end-to-end or it is a silent iSCSI
      killer, so the wire is proven before any VM touches it
      (verified 2026-09-01; live state recorded in the fleet NIC
      map — stor0 native-storage/block-tagged on Agg 4/1/5,
      storbr0 at 9000 holding 10.10.13.20/.21/.40):
  - [x] UniFi: storage-only port profile (native storage VLAN,
        block all tagged) on the three stor0 ports (the portal end
        is r740a itself — ZFS on the PVE host — so its port is
        already in the set); jumbo frames enabled on the
        aggregator (a switch-wide setting — ports then opt in per
        end)
  - [x] Each host, **one at a time** (`ifreload -a` blips the
        host's storage IP — live iSCSI/ZFS sessions hiccup):
        `mtu 9000` on both the `stor0` and `storbr0` stanzas in
        `/etc/network/interfaces`, then verify
        `ip link show storbr0` reports 9000
  - [x] Prove the path: host ↔ host and host ↔ portal
        `ping -M do -s 8972 <storage-ip>` — do-not-fragment at
        9000-byte frames; silence means a 1500 link is lurking
  - [x] Harden the trunk: VLAN 14 dropped from `pve-guest-trunk`
        (now tagged 11 only, native None) — `storbr0` is the
        **only** guest path to storage, and an untagged or
        `tag=14` guest NIC on vmbr1 dies at the switch instead of
        landing somewhere surprising (2026-09-01)
- [x] `qm set` on each VM's hosting PVE node (PVE hot-plugs NICs by
      default — confirm each with `talosctl -n <ip> get links`
      showing a new link with the net1 MAC; if a link doesn't
      appear, a rolling `talosctl reboot` picks it up). Done
      2026-09-02: hot-plug took on all six — every node shows
      `ens18` = net0 MAC and `ens19` = net1 MAC, link up, zero
      reboots:

      ```sh
      qm set <vmid> --net1 virtio=<net1-mac>,bridge=storbr0,firewall=0,mtu=9000
      ```

- [x] **Deviation found and fixed (2026-09-02)** — the live Storage
      network contradicted the "no DHCP, no gateway" premise: the
      UCG ran a DHCP server on it (pool `.6–.254`, containing every
      planned static and host address), offered `10.10.13.1` via
      Auto Default Gateway, and routed the VLAN as an L3 interface
      with internet access allowed. Surfaced by work01's
      implicit-DHCP lease (`10.10.13.222`) on the hot-plugged NIC —
      Talos runs DHCP on any unconfigured link, so the whole fleet
      had silently joined. Fixed at the UCG: DHCP off, internet
      access off, network converted to third-party gateway —
      **L2-only like sync, unroutable by construction**. The
      documented access boundary is now structurally true rather
      than firewall-dependent.
- [x] Patch each node's machineconfig — both NICs declared, selection
      by `deviceSelector.hardwareAddr` only, net0 explicit `dhcp`,
      net1 static with **no routes, no gateway, no DNS**. Done
      2026-09-02, with a recorded stumble: all six patch files were
      first applied to work03 (`-n` didn't move with the filename),
      and Talos **strategic-merges** the interfaces list keyed by
      selector — so they appended silently, inert (an unmatched
      deviceSelector is ignored, and `patch` reports success either
      way; the tell was DHCP leases surviving on ens19). Cleaned
      via `talosctl edit machineconfig` (6902 replace is refused on
      multi-doc configs), then re-applied with the correct
      node/file pairing and verified per node:

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
              mtu: 9000
              addresses:
                - "<storage-address>"
      ```

      ```sh
      talosctl -n <node-ip> patch machineconfig -p @storage-patch-<node>.yaml
      ```

- [x] Verify per node: `talosctl -n <ip> get addresses` shows the
      table address on the net1 link; iscsid is present
      (`talosctl -n <ip> services` → ext service running). All six
      statics exact; `ext-iscsid` Running ×6 (2026-09-02).
- [x] Verify the boundary held: `talosctl -n <ip> get routes` shows
      **no** default route via 10.10.13.0/24 — net1's unroutability
      is the storage plane's access boundary. Verified ×6: only the
      connected `10.10.13.0/24` on ens19 plus each node's ens18
      default — and the boundary is now structural (L2-only
      network, no gateway exists to leak).
- [x] Verify reachability and the jumbo path: proven from the
      portal host itself — `ping -M do -s 8972` from r740a
      (`10.10.13.20`) answered by **all six** nodes, sub-ms, zero
      loss: bidirectional L2 + 9000 end-to-end in one sweep. The
      `nc` to an iSCSI target deferred to democratic-csi's first
      session (no target listening yet).
- [x] Verify invisibility: a full bootstrap stage loop applies zero —
      the hand layer is invisible to the tool's checks (2026-09-02:
      health green, emit/ipxe/vms/bootstrap all nothing-to-do, 0
      steps applied anywhere)

#### Success Criteria

- All six nodes hold their table address on the VLAN-14 NIC,
  selected by MAC, with cluster health green throughout.
- No routing change rode along (the unroutability boundary is
  intact); the storage path carries 9000 end-to-end (proven by
  do-not-fragment ping), the primary plane untouched at 1500.
- The convergence loop still applies zero.
- **Gate**: Phases 3–4 do not start until every box above is checked
  — this state is their spec.

**Phase 2 complete — gate open (2026-09-02).** All six nodes carry
their exact table state: static storage address by MAC selector,
MTU 9000, defaults intact, iscsid running, jumbo proven from the
portal host, and the whole hand layer invisible to the convergence
loop. Three deviations were found and fixed along the way (the
frozen role-owned booty catalog, the Storage network's
DHCP/gateway/routing contradicting the design premise, and the
patch-file/node mismatch) — each recorded in its task above. This
verified end state is now the executable spec for Phases 3–4.

---

### Phase 3: Config surface (code)

The tool learns the network surface OQ-1 decided. Blocked on
Phase 2's gate (the OQs are decided).

#### Tasks

- [x] The raw-vs-resolved split (OQ-1 layering addendum): raw HCL
      types with optional mode attrs; one resolver producing a
      fully-explicit resolved interface per NIC; everything
      downstream consumes only the resolved form
      *(`internal/config/resolve.go`: `ResolvedInterface`,
      `Cluster.ResolveInterfaces()` — exported so struct-literal
      test fixtures resolve the same way `Load` does; resolved
      interfaces stored per node, read via
      `TalosNode.ResolvedInterfaces()`/`PrimaryInterface()`)*
- [x] Cluster-level `network "<name>"` blocks: `vlan` (optional —
      omitted renders untagged; the bridge and switch port profile
      own membership), `dhcp` (required — every plane states its
      mode), `primary`, `cidr` (required iff `dhcp = false`), `mtu`
      (optional — rendered into the VM NIC and machineconfig when
      set)
- [x] Per-node `network_interface "<netN>"` blocks — **replacing**
      the flat `mac`/`vlan`/`bridge` node attrs (the breaking change
      OQ-1 accepted): `mac`, `bridge`, plus either `network` (plane
      reference) or the inline mode facts, per the XOR rule
      *(decode-level MAC uniqueness validator moved from
      `("node","mac")` to `("network_interface","mac")` — hclkit's
      walk recurses into nested blocks)*
- [x] Validation on the resolved form, one rule at a time: unique
      plane names; at most one plane `primary = true`; referenced
      planes must be declared; the XOR rule (reference + inline
      mode attr → error; neither → error naming what's missing);
      every node resolves to exactly one primary interface; dhcp →
      `address` forbidden; static → `address` required, CIDR form,
      contained in the governing `cidr` when one exists; `mtu`
      within virtio's 576–65520 when set; global MAC uniqueness
      across all interfaces; interface labels `net\d+`, unique per
      node
      *(one added rule beyond the design list, per the unreferenced-
      profile doctrine: a plane no interface references is an error,
      not silently inert config — recorded as rule 10 in
      DESIGN-0004)*
- [x] Load tests: parse + one test per validation error, drill-style,
      covering both forms and the XOR conflicts
      *(24 new mutation-table cases in `load_test.go`: both XOR
      directions, every plane rule, every interface rule, the
      fartlab storage-plane shape accepted end to end, and
      `TestLoadResolvesAndNormalizes` now pins canonical MACs in
      both the raw and resolved layers)*
- [x] Migrate `examples/bootstrap.hcl` and test fixtures to the new
      surface, documenting both forms in place
      *(example: `servers` plane + referenced form on the control
      planes, inline form on worker-01, storage plane and net1 shown
      commented; fixtures: `load_test.go` validHCL exercises both
      forms, emit/pve test clusters build `network_interface` blocks
      and call `ResolveInterfaces()`; Phase 3 shims keep `VMSpec`
      net0 and the catalog group selector reading the resolved
      primary interface — emit goldens byte-identical)*

#### Success Criteria

- `just bootstrap-test` and `bootstrap-lint` green.
- A single-interface config (primary plane only) produces identical
  *behavior* to v0.2.0 — syntax broke, artifacts didn't.
- The example config stays valid (`TestLoadExampleConfig`).

**Phase 3 complete (2026-09-02).** All criteria verified: race
tests and lint at 0 issues; the emit goldens never changed across
the surface swap — the single-interface byte-identity proof came
for free from the shim; `TestLoadExampleConfig` pins the migrated
example. Style review pass produced two hardening fixes (accessor
returns a copy; `VMSpec` errors on an unresolved node instead of
rendering an empty NIC). Commits `0b773c6`, `53bc0ae`, `e847b6c`.

---

### Phase 4: VM and emit surface (code)

The two render paths — PVE VM and machineconfig — learn to produce
Phase 2's end state.

#### Tasks

- [x] `VMSpec` renders every declared interface in slot order —
      `bridge` from the interface, `tag=` only when a `vlan` is
      declared, `mtu=` when the plane sets one;
      **boot order carries only the primary-plane interface's slot**
      — regression test pinning `order=scsi0;net0` with a second
      NIC present
      *(`netN` renders any slot — net0 via the SDK's typed field,
      the rest via Extra; `bootOrder` derives from the primary slot;
      `TestVMSpecRendersAllInterfaces` pins the struct,
      `TestVMsMultiNICWireFields` pins it through a real create and
      read-back)*
- [x] Emit catalog: per-group interface vars (each interface's MAC;
      static addresses) beside `hostname`
      *(`net0_mac`/`net1_mac`/`net1_address` per group, keys derived
      from `talos.MACVarKey`/`AddressVarKey` — the same source the
      template expressions come from; single-interface nodes emit no
      extra vars, keeping their groups byte-identical)*
- [x] Machineconfig templates: `machine.network.interfaces` rendered
      from the planes — the primary-plane interface by
      deviceSelector with `dhcp: true`, static-plane interfaces by
      deviceSelector with their address (and `mtu` when the plane
      sets one), no routes — emitted only when a node has more than
      its primary interface (OQ-2)
      *(implementation decision: one machineconfig template per role
      is booty's overlay contract, so the section renders from a
      per-role interface **shape** — slot/dhcp/mtu, identity via
      group vars — and nodes of a role with divergent shapes are an
      emit error naming both sides. Placeholder identity is
      machinery-validated then swapped: marker strings for
      hardwareAddr, TEST-NET-3 addresses for the CIDR-checked
      addresses. The deprecated v1alpha1 section is deliberate —
      machinery v1.13 ships no multi-doc device type and the live
      fleet carries exactly this shape)*
- [x] Round-trip test: rendered multi-interface configs re-validate
      in machinery metal mode (the existing round-trip pattern)
      *(`TestRoleTemplatesMultiNICRoundTrip`)*
- [x] Golden files: single-interface fixtures byte-identical (the
      back-compat proof); new goldens for a multi-interface fixture
      *(existing `testdata/golden` untouched through the whole
      change; `testdata/golden-multinic` pins the vars-bearing
      groups; `TestRoleTemplatesSingleNICByteIdentical` proves the
      template layer emits no `machine.network` for primary-only
      nodes)*
- [x] Docs in the same change: runbook §1 (config surface), §10
      (vms expected fields), §15 (note that rebirth now covers the
      storage plane); example config final pass
      *(§15's storage-plane note carries an explicit
      not-yet-executed-live marker — Phase 6 is that proof; the
      example config got its full two-form pass in Phase 3)*

#### Success Criteria

- Full battery green: race tests, lint, goldens, build.
- The emitted `interfaces:` block for a storage node is
  **shape-identical** to a live node's hand-patched section (diff
  against `talosctl -n <ip> get machineconfig` output for one node).
- Single-interface emit output is byte-identical to v0.2.0's.

**Phase 4 code complete (2026-09-02).** Battery green (race tests,
lint 0 issues, goldens, build); single-interface output
byte-identical (the golden set never changed;
`TestRoleTemplatesSingleNICByteIdentical` proves the template
layer). The live shape-diff criterion is
`deferred - human required`: run, from a machine with the
talosconfig, e.g.

```sh
talosctl -n 10.10.11.63 get machineconfig -o yaml | rg -A 14 'interfaces:'
```

and compare against the fields the round-trip test renders
(deviceSelector/hardwareAddr per NIC; `dhcp: true` on net0;
`dhcp: false`, `mtu: 9000`, `addresses: [<addr>/24]` on net1 — no
routes). `TestRoleTemplatesMultiNICRoundTrip` pins exactly this
shape in CI; the live diff is the operator's confirmation on real
metal. Style review pass produced three fixes (error wrapping,
named substitution fields, doc repair). Commits `4f8e5c5`,
`6547181`, `c9c0ad0`, `c8f0768`.

---

### Phase 5: Release and convergence acceptance

The INV-0002 invariant, proven: running everything after the code
change must not break or change the cluster.

#### Tasks

- [ ] PR merged with `dont-release`; dispatch `tools-release.yml`
      tool=`bootstrap` version=`v0.3.0`; verify tag + archives
      *(code side done — PR opened from `feat/network-planes`; merge
      and dispatch are the operator's)*
- [ ] Operator: add the storage surface to `~/drill/bootstrap.hcl`
      with the authoritative table's exact values
      *(the verified draft already exists as
      `~/drill/bootstrap.hcl.next` — servers plane `vlan = 11`,
      storage plane with **no vlan** + `mtu = 9000` +
      `cidr = "10.10.13.0/24"`, per-node net0/net1 blocks)*
- [ ] Operator: full stage loop from the released v0.3.0 binary —
      expected shape: `emit` applies once (artifact drift only:
      machine-configs gain the interfaces block, catalog gains the
      vars), `ipxe` 0, `vms` 0 (no retrofit, by design),
      `bootstrap` skips, `health` green
- [ ] rsync + restart booty; re-run the loop — zero everywhere
- [ ] Confirm live cluster state untouched (nodes, workloads, ArgoCD
      apps — nothing restarted, nothing changed)

**Phase 5 status (2026-09-02): `deferred - human required`.** All
code-side work is complete and committed; every remaining step
needs the release train or the live cluster, which the operator
drives (IMPL-0002 operating rule).

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
      *(§1/§10/§15 already describe the new surface; the §15
      storage-plane note carries a not-yet-executed-live marker that
      flips when the rebirth runs)*
- [ ] DESIGN-0004 status → **Implemented**
- [ ] This doc: all boxes checked, status → **Completed**

**Phase 6 status (2026-09-02): `deferred - human required`.** The
rebirth is a live-cluster window; the two status flips are gated on
its outcome, so nothing here can move until the operator runs it.

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
| `internal/config/resolve.go` | Add | the resolver: raw → `ResolvedInterface`, XOR + per-interface rules |
| `internal/emit/testdata/golden-multinic/**` | Add | multi-interface goldens; the legacy `golden/**` set untouched |
| `examples/bootstrap.hcl` | Modify | document the surface |
| `docs/runbook/bootstrap-cluster.md` | Modify | §1, §10, §15 notes |

## Testing Plan

- [x] Load tests per validation rule (drill-style, one failing input
      each)
- [x] `VMSpec` regression tests: net1 fields, boot order unchanged
- [x] Template round-trip in machinery metal mode with storage vars
      substituted (`TestRoleTemplatesMultiNICRoundTrip`)
- [x] Golden byte-identity for storage-less fixtures — the
      back-compat contract as a test (`testdata/golden/**` unchanged
      through the whole change; `TestRoleTemplatesSingleNICByteIdentical`)
- [ ] The two live proofs (Phases 5–6) stay out of CI, recorded here
      (IMPL-0001's decision: the e2e drill is not a merge gate)

## Open Questions

Numbered for decision; **a is the recommendation**, later letters are
alternatives, "other" is yours to write in.

**OQ-1 — Where does the storage surface live in the config?**

**Decided (2026-08-31): d** — a fourth shape reached in review,
combining a's declare-the-plane-once with c's generality, on the
operator's principle that the CLI is explicit primitives (the future
service abstracts on top, and the primitives stay reachable). The
full design is promoted to **DESIGN-0004** — that document is the
authoritative record; this OQ keeps the decision trail. Named
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

**Layering addendum (decided 2026-08-31):** the inline attributes
are the primitives; `network` planes are the first abstraction on
top of them — so both forms ship, governed by the **XOR rule**:

- An interface either sets `network = "<plane>"` and none of the
  plane-owned attrs (`vlan`, `dhcp`, `primary`, `cidr`), **or** sets
  no reference and declares all its mode facts inline. Setting both
  is an **error, never an override** — no precedence engine, ever.
- An interface with neither a reference nor complete inline mode
  facts is an error naming what's missing. There is **no implicit
  default network** — rejected explicitly; explicit is the way.
- `cidr` on an inline interface is optional (the address's prefix is
  the primitive; plane-level `cidr` earns its keep as a
  cross-fleet typo check).

Implementation structure: a **raw-vs-resolved split**. HCL decodes
into raw types where mode attrs are optional; one resolver produces
a fully-explicit resolved interface per NIC; validation, `VMSpec`,
and emit consume only the resolved form and never know how a fact
arrived. Future plane features (MTU on the jumbo day, new modes)
are resolver-and-plane changes with zero consumer churn.

Superseded options, for the record:

- ~~a: `storage_network` + per-node `storage` block — hardcoded
  storage semantics into a tool that shouldn't know what a NIC is
  for.~~
- ~~b: fully per-node attrs — six chances to typo the VLAN.~~
- ~~c: generic `nic` blocks without the plane concept — genericity
  without the declare-once property.~~
- ~~An implicit `network "default"` fallback for bare interfaces —
  hidden behavior; the error stays an error.~~

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
- Storage portals are ZFS on r740a, served over its `storbr0`
  address (`10.10.13.20`) — so the portal end of the jumbo chain
  was covered by the host fabric work, and VMs hosted on r740a
  reach their portal without touching a wire.
- `storbr0` bridges on all three hosts, enslaving `stor0`; port
  profiles, aggregator jumbo, and host MTU stanzas all verified
  2026-09-01 (the Phase 2 fabric tasks — done). Caution for any
  future MTU work: r640a's qede SFP+ NICs can wedge into a carrier
  flap loop on **live** MTU changes (hit 2026-09-01; recovery
  documented in the network role) — no remaining phase touches a
  host NIC MTU.

## References

- DESIGN-0004 — the network planes/interfaces design this executes
- INV-0002 — the investigation and Option C decision
- DESIGN-0002 — the completion surface the interfaces emit extends
- `docs/runbook/bootstrap-cluster.md` §13 (convergence), §15
  (re-image — the rebirth invariant)
- DESIGN-0006 / DESIGN-0009 (homelab docs) — address table, port map
- Talos machineconfig `deviceSelector` documentation
- democratic-csi prerequisites: `siderolabs/iscsi-tools`,
  `siderolabs/util-linux-tools`
