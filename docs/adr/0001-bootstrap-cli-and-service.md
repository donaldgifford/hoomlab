---
id: ADR-0001
title: "Bootstrap CLI"
status: Proposed
author: Donald Gifford
created: 2026-08-17
---

<!-- markdownlint-disable-file MD025 MD041 -->

# 0001. Bootstrap CLI

<!--toc:start-->
- [Status](#status)
- [Context](#context)
- [Decision](#decision)
- [Consequences](#consequences)
  - [Positive](#positive)
  - [Negative](#negative)
  - [Neutral](#neutral)
- [Alternatives Considered](#alternatives-considered)
- [References](#references)
<!--toc:end-->

## Status

Proposed

## Context

Hoomlab's primary run mode is a Kubernetes deployment (Helm chart) on a Talos
cluster running on a Proxmox cluster. On day zero none of that exists: the
starting state is a set of Proxmox nodes that are not yet a cluster, and no
Kubernetes anywhere. Something outside the future cluster must build it — the
chicken-and-egg is structural, so the answer is a permanent out-of-cluster
actor, not a workaround.

Per the RFC, this is not a one-click install and not fully automated. It is a
bootstrapping process; the tooling simplifies it, it does not necessarily make
it easier.

The primitives already exist and are owned: `github.com/donaldgifford/hclkit`
(configuration), `github.com/donaldgifford/proxmox-go-sdk` (Proxmox API, with
`pvelab` as its example CLI and test harness), and
`github.com/donaldgifford/booty` (iPXE, cloud-init, and enough Talos machinery
to PXE boot instances as control-plane or worker node VMs, delivered as a
library package with a CLI/container as the opinionated default runtime). The
Talos Go libraries cover what booty does not.

## Decision

Build the bootstrap CLI as the first Hoomlab deliverable, on `hclkit`,
`proxmox-go-sdk`, `booty`, and the Talos Go libraries. Cobra for the CLI;
testing against the mock containers our packages already provide, with mockery
v3 where additional mocks are needed. It lives in `tools/bootstrap`, alongside
— not inside — the scaffold: `cmd/` and `charts/` remain the future Hoomlab
service, untouched by bootstrap.

**Delivery is a CLI tool, nothing more** — no service component, no database,
driven entirely by HCL configuration files. The schema is baked into the
tool's own `internal/config` for now; when the Hoomlab service needs the same
files, the schema is pulled out then. The first version is deliberately
self-contained for ease of work and test — reusable pieces are extracted for
the service later, not designed for reuse up front. Built and run locally for
now: releases use the `dont-release` label and the release train does not
publish it. Bootstrap runs outside the cluster it builds, permanently.

**Re-runs must converge.** With no database and no state file, the
configuration files and the world are the only two truths: every step reads
actual state from the APIs and reconciles toward the configuration, so a run
interrupted anywhere is simply re-run, not repaired. This is what keeps
disaster recovery equal to re-running bootstrap — and it is the contract the
Hoomlab service inherits when it takes over managing these resources from the
same files.

**First functionality — Proxmox cluster formation.** From HCL node definitions,
use `proxmox-go-sdk` to create the Proxmox cluster, set up ACME DNS-01
certificates for it through the SDK (Cloudflare is the blessed DNS provider),
and add the remaining nodes to the cluster. `pvelab` is functional
documentation for this: working code to copy from where it makes sense, never
to import.

**Second — Talos cluster via booty.** Booty runs as a Docker container outside
the target cluster, providing the iPXE boot and cloud-init services. The
bootstrap CLI:

1. Emits the output files booty needs to build the PXE-booted instances,
   generated from the CLI's own configuration files and settings.
2. Creates the Proxmox VMs via the SDK, configured to PXE boot from the running
   booty service as control-plane or worker Talos nodes.

Where booty's Talos machinery coverage ends, the CLI covers the remainder with
the Talos Go libraries directly. Hoomlab will eventually manage those nodes
directly, so booty's Talos role at that layer is transitional; its iPXE and
cloud-init role is not.

**Consumption pattern.** Booty is imported as a library where it makes sense —
not consumed wholesale into the CLI as a replacement for the booty service. The
primary integration is artifact emission: our config files in, booty's files
out. The `booty` CLI and service code serve as reference implementations, as
`pvelab` does for the SDK.

**Config continuity.** The same configuration files that drive bootstrap are
then used to run Hoomlab on the cluster it built, so that Hoomlab owns itself
after the bootstrapping process. Self-ownership is the defined end state of
bootstrap, not a separate migration project.

## Consequences

### Positive

- Configuration-file continuity from bare Proxmox nodes to a self-owned Hoomlab:
  one set of files describes the cluster before and after it exists.
- PXE-booted Talos nodes carry no image or template artifacts to build and
  manage during bootstrap; nodes are re-imageable by re-boot.
- Every layer reuses an owned, tested primitive with a reference CLI (`pvelab`,
  `booty`) already demonstrating the integration code.
- A permanent out-of-cluster rebuild path exists by construction — the tool that
  built the cluster can always rebuild it.
- No database and no server state in bootstrap: the configuration files are the
  whole input, which keeps disaster recovery equal to re-running bootstrap.

### Negative

- Bootstrap has real prerequisites by design: an operator-run booty container
  reachable from the VM network, and a working PXE path, before any Talos node
  exists. Simple, not easy — these are documented steps, not hidden ones.
- Booty's Talos machinery gaps land in this CLI's scope until Hoomlab manages
  nodes directly; that boundary must be tracked so the same function is not
  built twice.
- Deliberately operator-driven: no unattended end-to-end run, which trades
  convenience for a process the operator can see, interrupt, and understand.

### Neutral

- Bootstrap intentionally never grows a database or API; capability beyond
  configuration-driven one-shot runs belongs to the Hoomlab service.
- The Hoomlab service (Helm-delivered, the larger body of work) is explicitly
  sequenced after bootstrap is proven and running, and consumes the
  bootstrapping service rather than replacing it.
- Direct node management in Hoomlab later shrinks booty's Talos role without
  changing booty's iPXE/cloud-init contract.

## Alternatives Considered

**Prebuilt Talos images and template cloning** (factory image → Proxmox template
→ clone per node). Viable, and may return later as a Hoomlab-service capability.
Rejected for bootstrap: booty already exists and is owned, PXE keeps nodes
stateless with one provisioning mechanism, and image/template artifact
management is exactly the kind of state bootstrap should not carry.

**Consume booty wholesale into the CLI.** Rejected: booty remains a standalone
service with its own contract; the CLI emits its inputs and imports the library
only where it genuinely fits. Reimplementing it inside bootstrap doubles
maintenance and forks behavior.

**Fully automated one-click installer.** Rejected per the RFC: conflates simple
with easy, hides failure modes the operator must understand, and pushes the tool
toward carrying state it should not have.

**Terraform/Ansible for the bootstrap phase.** Rejected per the RFC:
invoked-not-resident, state custody at the worst possible moment, and unable to
express the full path from cluster formation through PXE-booted Talos without
the seams this project exists to remove.

## References

- RFC-0001: Hoomlab
- `github.com/donaldgifford/hclkit`
- `github.com/donaldgifford/proxmox-go-sdk` (`pvelab` reference CLI)
- `github.com/donaldgifford/booty` (`booty` reference CLI/container)
- Talos Go libraries (machineconfig generation and cluster bootstrap)
- Planned ADRs: platform choices (Proxmox VE, Talos, HCL, Go), tools/ release
  path, Hoomlab service via Helm
