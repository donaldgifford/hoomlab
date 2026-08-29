---
id: DESIGN-0003
title: "Nested test environment for the bootstrap CLI"
status: Draft
author: Donald Gifford
created: 2026-08-28
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN-0003: Nested test environment for the bootstrap CLI

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [What booty's environment needed](#what-bootys-environment-needed)
  - [What nested provisioning must provide](#what-nested-provisioning-must-provide)
  - [What a PVE-installer PXE profile would take](#what-a-pve-installer-pxe-profile-would-take)
  - [Validation reference](#validation-reference)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

The isolated nested test environment IMPL-0002 deliberately deferred:
a dedicated test VLAN, a hoomlab-controlled booty VM scoped to it, a
nested PVE cluster PXE-installed from that booty, and Talos on top —
all automated, so the full bootstrap drill can re-run unattended
without touching production hardware.

Per IMPL-0002's OQ-4 decision, the hardware drill (INV-0001) *was*
this environment's requirements gathering. This revision is the
requirements capture with the drill's real inputs in hand; the
detailed design fills in when the build starts, working backwards
from the known-good production state the drill created.

## Goals and Non-Goals

### Goals

- Re-run the full IMPL-0002 drill — formation through `talos health`
  through the convergence pass — unattended, on demand, without
  production hardware.
- Reproduce the production environment faithfully enough that the
  drill's evidence transfers: the nested run passes the same 12
  INV-0001 drill rows and converges to zero on re-run.
- Capture every environmental requirement the drill observed, so the
  build works from evidence rather than memory.

### Non-Goals

- Replacing the unit suite or mockpve — those stay the merge gate;
  this environment is the e2e layer above them (IMPL-0001 already
  decided the e2e drill is not a merge gate, and that holds).
- Extending pvelab. Hoomlab's test tooling is its own, not an
  extension of the SDK's harness.
- Day-2 operations or Hoomlab-service scenarios (RFC-0001 Phase 2+);
  this environment exists to exercise bootstrap.

## Background

The drill ran the bootstrap CLI against three real PVE hosts and left
behind a production PVE cluster and a six-node Talos cluster, with
every stage's real-world behavior recorded in INV-0001 (14 deviations,
all folded back) and a fully verified runbook. OQ-4 decided the nested
environment gets built **on the production cluster this run created**,
and that the production PVE + Talos clusters are the **reference the
test environment is validated against**: the nested env must reproduce
what prod demonstrably does.

Two capabilities the environment needs ship nowhere today — a
PVE-installer PXE profile for booty, and nested-PVE-VM provisioning
tooling in hoomlab — which is why this design comes after the manual
run, not before.

## Detailed Design

This revision is the requirements capture. Each subsection records
what the drill proved the environment must provide; the mechanism
design lands in a later revision.

### What booty's environment needed

Observed on ns1 (10.10.11.190, Servers VLAN 11) across drill Phases
3–5; the test environment must reproduce each on the test VLAN:

- A host with a static leg on the boot VLAN, running four listeners:
  HTTP 8080 (`/boot.ipxe`, `/ipxe?mac=`, `/machine-config?mac=`, and
  the boot-asset tree), proxyDHCP 67, PXE 4011, and TFTP 69.
- proxyDHCP alongside a real DHCP service that owns addressing — in
  the drill, MAC-pinned reservations. The test VLAN needs its own
  scope or reservations for the nested MACs; booty never hands out
  addresses.
- On a multi-homed booty host, the broadcast route pinned to the boot
  VLAN and **persisted** in the host's network config:
  `ip route add 255.255.255.255/32 dev <boot-vlan-iface>`. Without it
  proxyDHCP offers exit the default-route interface — firmware shows
  PXE-E16 while booty logs offers (runbook, INV-0001).
- The emitted artifact tree synced verbatim: `ipxe.efi` with the
  embedded two-hop `dhcp` + `chain` script (deviation 11 — the embed
  runs *in place of* autoboot and must configure the NIC itself),
  `boot.ipxe`, per-MAC machine-configs, and the schematic-pinned
  Talos factory kernel/initramfs paths.
- The accepted plaintext deviation carries over: `/machine-config`
  serves cluster PKI over plain HTTP. Production accepts this on the
  trusted Servers VLAN; the test VLAN must be scoped at least as
  tightly, or this environment becomes the place TLS gets added.

### What nested provisioning must provide

Per guest, the VM-config contract the drill proved load-bearing — now
pinned and regression-tested in `tools/bootstrap/internal/pve/vms.go`
(INV-0001 deviations 11/12/14; every item produces a VM that "looks
fine and never boots" when dropped):

- q35 + OVMF, EFI vars disk without pre-enrolled Secure Boot keys
- `virtio-scsi-single` (the API-default LSI controller has no Talos
  driver)
- guest agent channel (`agent: enabled=1` — without it the boot
  sequence never completes)
- VirtIO RNG (post-PixieFail EDK2 silently drops PXE without entropy)
- serial socket console, `cpu: host`, `boot: order=scsi0;net0`
- pinned MAC as the shared identity with the booty group, and the
  VLAN tag on the PVE side of `net0` (the guest trunk has no native
  VLAN)

New requirements specific to nesting:

- The nested PVE hosts are themselves VMs and must expose nested
  virtualization to run their own Talos guests (`cpu: host` provides
  this), with a bridge/trunk model that carries the test VLAN through
  to the nested layer.
- Nested disks stay confined to the designated VM pools; pool roots
  and the BOSS `rpool/data` datasets remain untouchable, exactly as
  in production.
- Provisioning the nested PVE VMs is **new tooling**: bootstrap
  provisions Talos VMs only. Whether this grows inside bootstrap or
  as a sibling tool is an open question below.

### What a PVE-installer PXE profile would take

booty serves Talos today; PXE-installing PVE itself is a new profile:

- The PVE installer's kernel/initrd extracted from the ISO and served
  over the same TFTP/HTTP path the Talos assets use.
- Fully unattended answers — `proxmox-auto-install-assistant` /
  `answer.toml` fetched over HTTP, plausibly per-MAC, mirroring the
  `machine-config?mac=` pattern.
- Post-install first boot must land on the bootstrap CLI's starting
  contract (runbook prerequisites): reachable PVE API on the test
  VLAN with `root@pam` credentials — whatever network and credential
  seeding that takes belongs to the profile.

### Validation reference

The production PVE cluster and Talos cluster are the reference. The
nested environment is accepted when the drill's own evidence
reproduces:

- all 12 INV-0001 drill-result rows pass, driven by the runbook alone
- the full-stage re-run converges to zero applies
- an unattended re-image round-trips (production reference: proxyDHCP
  offer to machine-config served in ~22 s)

## API / Interface Changes

Deferred to the design pass. Expected surface: a test-environment
config block or sibling config file consumed by whatever provisions
the nested PVE VMs, and a booty profile for the PVE installer.

## Data Model

Deferred to the design pass.

## Testing Strategy

The environment *is* the testing strategy for bootstrap's e2e layer.
Its own acceptance is the validation reference above; the unit suite
and mockpve remain the merge gate underneath (unchanged from
IMPL-0001's decision).

## Migration / Rollout Plan

Working backwards from known-good: build on the production cluster,
one layer at a time — test VLAN, booty VM, PVE-installer profile,
nested PVE cluster, then the bootstrap drill against it. Each layer
validates against its production counterpart before the next goes on.

## Open Questions

- Where do the nested PVE VMs run — as VMs on the production PVE
  hosts (simplest, test VLAN only), or on the production Talos
  cluster? OQ-4's "built on the production cluster" admits both.
- Does the PVE-installer profile land upstream in booty or stay a
  hoomlab-side overlay?
- Does the test VLAN accept the plaintext machine-config deviation,
  or is this where TLS gets added?
- How does the unattended drill get invoked — scheduled, manual
  dispatch, or release-candidate gate (still not a merge gate)?
- Where does nested-PVE-VM provisioning live — a bootstrap stage, a
  sibling tool under `tools/`, or the future Hoomlab service?

## References

- IMPL-0002 — the drill this environment replays; OQ-4 (decision and
  refinements), Out of Scope (the deferral this doc picks up)
- INV-0001 — the drill record: deviations 11/12/14 (guest-visible VM
  config), the broadcast-route and plaintext findings, the
  convergence table and re-image timing this environment must
  reproduce
- `docs/runbook/bootstrap-cluster.md` — the fully verified operator
  script; the nested drill must be drivable from it alone
- DESIGN-0001 / DESIGN-0002 — the CLI and its Talos completion
  surface, unchanged by this doc
- ADR-0001 — the bootstrap CLI decision
- booty's Proxmox+Talos overlay walkthrough (donaldgifford/booty,
  `docs/go-ipxe/10-talos-overlay-walkthrough.md`)
