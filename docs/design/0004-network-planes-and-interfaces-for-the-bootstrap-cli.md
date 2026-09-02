---
id: DESIGN-0004
title: "Network planes and interfaces for the bootstrap CLI"
status: Approved
author: Donald Gifford
created: 2026-08-31
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN-0004: Network planes and interfaces for the bootstrap CLI

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [The two block types](#the-two-block-types)
  - [The XOR rule](#the-xor-rule)
  - [The resolution model](#the-resolution-model)
  - [Derivations](#derivations)
  - [Validation](#validation)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Replaces the config's implicit single-NIC model with an explicit
network surface: named `network` blocks declare the *planes* a
cluster's guests attach to (VLAN, addressing mode, which plane
boots), and per-node `network_interface` blocks declare each NIC's
*identity* (MAC, bridge, address). The design is primitives-first —
inline attributes are the ground truth, planes are the first
abstraction layered on them, and a strict XOR rule keeps the two
honest. First consumer: the fartlab storage plane per INV-0002 —
iSCSI over VLAN 14 at the switch, untagged at the guest on the
dedicated `storbr0` bridges, jumbo end-to-end; the surface itself
knows nothing about storage.

Status **Approved** on creation: the design was settled in the
INV-0002 / IMPL-0003 review (2026-08-31) across three shape
iterations; this document is the authoritative record. It flips to
**Implemented** when IMPL-0003 completes.

## Goals and Non-Goals

### Goals

- Any number of NICs per node, each an explicit, declared primitive —
  the CLI is the primitive layer a future service abstracts over,
  and the primitives stay reachable.
- Plane facts (VLAN, mode, boot role) declared once, not once per
  node — six copies of `vlan = 14` is six chances to typo one.
- Every downstream fact derivable from config alone: the PVE NIC
  strings, the boot order, booty's PXE identity, and the
  machineconfig `interfaces:` section — so runbook §15's rebirth
  invariant extends to multi-NIC nodes with zero hand steps.
- New plane capabilities land without consumer churn (the
  raw-vs-resolved split below).

### Non-Goals

- **Implicit defaults of any kind.** An early "default network"
  fallback for bare interfaces was considered and rejected: an
  interface that neither references a plane nor states its own facts
  is an error, permanently.
- **Override/precedence semantics.** Reference XOR inline; a merge
  engine is the door to unreviewable configs.
- **Purpose-awareness.** The tool renders NICs; that a plane is "the
  storage plane" is operator semantics (comments, doc tables), never
  a tool concept.
- ~~**MTU / jumbo frames.**~~ *Amended 2026-09-01 — now in scope.*
  Originally deferred (a one-sided 9000 is the classic silent iSCSI
  killer). The deliberate both-sided decision then arrived along
  with the fabric that makes it safe — dedicated `storbr0` bridges
  over the hosts' `stor0` NICs, storage-only switch port profiles —
  and `mtu` lands exactly as pre-designed: a plane attr through the
  resolver. See Detailed Design.
- **Routes, gateways, or DNS on secondary planes.** Their
  unroutability *is* the access boundary (a stray default route on
  the storage VLAN creates asymmetric-routing misery).

## Background

Through v0.2.0, a node's network is one implicit NIC: flat
`mac`/`vlan`/`bridge` attrs render `net0`, DHCP is assumed (the
machineconfig carries no `machine.network` section at all), and the
boot order hardcodes `scsi0;net0`. INV-0002 — triggered by
democratic-csi needing an iSCSI path on the Storage VLAN — found
that no configuration could produce a second NIC, and that every
hand-applied workaround survives re-runs but is erased by a §15
re-image: exactly the drift class the IMPL-0002 drill was run to
eliminate.

The shape went through three review iterations: a storage-specific
block (rejected — purpose-aware), a flat per-interface list
(rejected — repeats plane facts per node, and attribute-lists lose
per-NIC diagnostics), and finally planes + referencing interfaces,
sharpened by the operator's principle: *explicit primitives
underneath, abstractions on top, both always valid*.

## Detailed Design

### The two block types

Inside the `talos` block:

```hcl
network "servers" {
  vlan    = 11
  primary = true     # this plane boots: PXE, booty identity, DHCP
  dhcp    = true
}

network "storage" {
  dhcp = false             # no vlan: untagged — the switch port
  cidr = "10.10.13.0/24"   # profile owns membership
  mtu  = 9000              # jumbo — the fabric carries it end-to-end
}

node "ctrl01" {
  # ...
  network_interface "net0" {
    network = "servers"             # the load-bearing wire
    mac     = "02:50:99:a2:00:c9"
    bridge  = "vmbr1"
  }
  network_interface "net1" {
    network = "storage"
    mac     = "02:50:99:a2:14:c9"
    bridge  = "storbr0"             # dedicated storage bridge — untagged
    address = "10.10.13.51/24"      # static — the plane has no DHCP
  }
}
```

A `network` plane owns the shared facts: `vlan` (optional — omitted
means **untagged**: the interface's bridge and its switch port's
native VLAN own membership, as on the dedicated `storbr0` storage
bridge whose ports block all tagged frames), `dhcp` (required —
every plane states its mode), `primary` (at most one plane; its
member interface is the boot path), `cidr` (required iff
`dhcp = false`), and `mtu` (optional — omitted renders no override
anywhere, leaving the fabric default of 1500 in charge). A
`network_interface` owns identity: the label is
the PVE slot (`net0`, `net1`, …), `mac` and `bridge` are per-NIC,
`address` (CIDR form) is required on static planes and forbidden on
DHCP planes. The `network` attribute references a plane by name —
the same pattern node blocks already use for `pve_node` and
`storage`.

The fully-inline form is equally valid — the primitives are always
reachable:

```hcl
network_interface "net1" {
  mac     = "02:50:99:a2:14:c9"
  bridge  = "vmbr1"
  vlan    = 14
  dhcp    = false
  address = "10.10.13.51/24"
  cidr    = "10.10.13.0/24"   # optional inline; the address's prefix
                              # is the primitive
}
```

### The XOR rule

An interface takes exactly one of the two forms:

- **Referenced**: sets `network = "<plane>"` and *none* of the
  plane-owned attrs (`vlan`, `dhcp`, `primary`, `cidr`, `mtu`).
- **Inline**: sets no reference and *all* the mode facts itself.

Setting both is an **error, never an override** — there is no
precedence engine and there never will be one. Setting neither is an
error naming exactly what's missing. Both error paths point at the
offending `network_interface` block (labeled blocks carry per-block
source ranges; this is why the surface is blocks, not an attribute
list).

### The resolution model

The structural decision that keeps every future door open:

1. HCL decodes into **raw** types, where plane-owned attrs on an
   interface are optional (pointer-typed — absent vs set is
   meaningful, which is what the XOR check reads).
2. One **resolver** turns each raw interface into a **resolved
   interface**: every fact explicit — slot, MAC, bridge, VLAN, mode,
   primary, address, cidr — with no optionality left.
3. Validation, `VMSpec`, and emit consume **only the resolved form**.
   They never know whether a fact arrived inline or via a plane.

A future plane capability (MTU on the jumbo day, a new addressing
mode) is a plane attr plus a resolver line plus a resolved field —
zero consumer churn, no refactor.

### Derivations

Everything downstream reads the resolved interfaces:

- **PVE VM spec**: one `netN` entry per interface, in slot order —
  `virtio,bridge=<bridge>,macaddr=<mac>,firewall=0[,tag=<vlan>][,mtu=<mtu>]`.
  `tag=` appears only when a `vlan` is declared (tagged into a
  trunk, stripped before the guest, exactly as net0 works today);
  an untagged plane rides its bridge's native membership. `mtu=` is
  rendered explicitly when set — never PVE's `mtu=1`
  inherit-the-bridge magic.
- **Boot order**: `order=scsi0;<primary slot>` — only the
  primary-plane interface ever appears in boot order. booty serves
  one VLAN; a VM PXE-booting from a secondary NIC hangs in silence.
- **booty identity**: the primary interface's MAC is the catalog
  group selector. iPXE substitutes the *booting* NIC's MAC, which is
  the primary by construction.
- **Machineconfig**: nodes with only a primary interface emit **no**
  `machine.network` section — byte-identical artifacts to v0.2.0.
  Any second interface brings the `interfaces:` list, with every
  NIC then declared:

  ```yaml
  machine:
    network:
      interfaces:
        - deviceSelector:
            hardwareAddr: "<primary mac>"
          dhcp: true
        - deviceSelector:
            hardwareAddr: "<static mac>"
          dhcp: false
          mtu: 9000            # only when the plane sets one
          addresses:
            - "<address>"
  ```

  Selection is by `deviceSelector.hardwareAddr`, never interface
  names — virtio PCI enumeration order is not a contract. No routes,
  no gateway, no DNS on secondary planes.

### Validation

Flat rules over the resolved form, each with its own test:

1. Plane names unique; at most one plane `primary = true`.
2. `dhcp` required on every plane; `cidr` required iff
   `dhcp = false`.
3. Every `network` reference names a declared plane.
4. The XOR rule, both directions (conflict; incomplete).
5. Every node resolves to exactly one primary interface.
6. DHCP mode → `address` forbidden; static mode → `address`
   required, CIDR form, contained in the governing `cidr` when one
   exists.
7. MACs globally unique across every interface of every node.
8. Interface labels match `net\d+`, unique per node.
9. `mtu`, when set, within virtio's 576–65520. The fabric ceiling
   (aggregator jumbo, host bridge MTUs, the portal end) is the
   operator's to verify — the tool cannot see the wire.

Deliberately absent: any uniformity rule across nodes (a node with
only its primary interface is valid — the fleet table in IMPL-0003
is the operator's uniformity check), and any MAC scheme enforcement
(the fifth-octet-as-VLAN convention lives in docs; validation wants
only well-formed, unique MACs).

## API / Interface Changes

**Breaking config change**, accepted deliberately at v0.3.0 with one
config in existence. The flat node attrs are removed:

```hcl
# before (v0.2.0)                 # after (v0.3.0)
node "ctrl01" {                   node "ctrl01" {
  mac    = "02:50:99:a2:00:c9"      network_interface "net0" {
  bridge = "vmbr1"                    network = "servers"
  vlan   = 11                         mac     = "02:50:99:a2:00:c9"
  # ...                               bridge  = "vmbr1"
}                                   }
                                  }
```

Validation errors guide the migration. No CLI flag or stage surface
changes; `validate`, `talos emit`, and `pve|talos` stage behavior is
identical for single-interface configs.

## Data Model

```go
// Raw (HCL) — optionality is data.
type Network struct {
    Name    string `hcl:"name,label"`
    VLAN    int    `hcl:"vlan,optional"`
    DHCP    bool   `hcl:"dhcp"`
    Primary bool   `hcl:"primary,optional"`
    CIDR    string `hcl:"cidr,optional"`
    MTU     int    `hcl:"mtu,optional"`
}

type NetworkInterface struct {
    Name    string  `hcl:"name,label"`   // PVE slot: net0, net1, …
    Network string  `hcl:"network,optional"`
    MAC     string  `hcl:"mac"`
    Bridge  string  `hcl:"bridge"`
    VLAN    *int    `hcl:"vlan,optional"`    // inline form only
    DHCP    *bool   `hcl:"dhcp,optional"`
    Primary *bool   `hcl:"primary,optional"`
    Address string  `hcl:"address,optional"`
    CIDR    string  `hcl:"cidr,optional"`
    MTU     *int    `hcl:"mtu,optional"`
}

// Resolved — what validation, VMSpec, and emit consume.
type ResolvedInterface struct {
    Slot    string // net0, net1, …
    MAC     string
    Bridge  string
    VLAN    int    // 0 = untagged
    DHCP    bool
    Primary bool
    Address string // empty iff DHCP
    CIDR    string // empty when ungoverned
    MTU     int    // 0 = no override rendered
}
```

## Testing Strategy

- Load tests per validation rule, drill-style (one failing input
  each), covering both forms and both XOR error directions.
- `VMSpec` regressions: multi-NIC rendering in slot order; boot
  order pinned to `order=scsi0;net0` with a second NIC present.
- Machinery metal-mode round-trip for rendered multi-interface
  configs (the existing round-trip pattern).
- **Golden byte-identity for single-interface fixtures** — the
  back-compat contract as a test.
- Live acceptance stays out of CI (IMPL-0001's decision): IMPL-0003
  Phase 5 (zero-mutation convergence loop against the live cluster)
  and Phase 6 (single-worker §15 rebirth carrying its storage plane
  from config alone).

## Migration / Rollout Plan

IMPL-0003 is the rollout, in order: the operator's hand layer first
(its verified end state is the executable spec), then this surface
(Phases 3–4), `tools/bootstrap/v0.3.0`, the convergence acceptance,
and the rebirth proof. The hand-applied NICs and machineconfig
patches become disposable scaffolding the moment the config, the
live nodes, and the served artifacts agree.

## Open Questions

None blocking — all five IMPL-0003 OQs were decided 2026-08-31.
The first deliberately-left door was exercised 2026-09-01, one day
after Approval: plane-level `mtu` arrived for the storage plane
(jumbo, both sides at once — see the amended non-goal) as a plane
attr + a resolver line + a resolved field, zero consumer churn. The
extension path working exactly as designed. Doors still open:
whatever plane capability the second Talos cluster's topology
eventually wants.

## References

- INV-0002 — the investigation; Option C and the shape review
- IMPL-0003 — the phased rollout this design is executed by (its
  OQ-1 records the decision trail and superseded shapes)
- DESIGN-0001 / DESIGN-0002 — the CLI and its completion surface
- `docs/runbook/bootstrap-cluster.md` §15 — the rebirth invariant
  this design extends to multi-NIC nodes
- Talos machineconfig `deviceSelector` documentation
- DESIGN-0006 / DESIGN-0009 (homelab docs) — the storage address
  table and port map behind the first consumer
