---
id: RFC-0001
title: "Hoomlab: A Self-Hosted Cloud for Homelab Environments"
status: Draft
author: Donald Gifford
created: 2026-08-17
---

<!-- markdownlint-disable-file MD025 MD041 -->

# RFC 0001: Hoomlab: A Self-Hosted Cloud for Homelab Environments

<!--toc:start-->
- [Summary](#summary)
- [Problem Statement](#problem-statement)
- [Proposed Solution](#proposed-solution)
- [Design](#design)
- [Alternatives Considered](#alternatives-considered)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Bootstrap](#phase-1-bootstrap)
  - [Phase 2: Hoomlab Service](#phase-2-hoomlab-service)
  - [Phase 3: Platform Features](#phase-3-platform-features)
- [Risks and Mitigations](#risks-and-mitigations)
- [Success Criteria](#success-criteria)
- [References](#references)
<!--toc:end-->

## Summary

Hoomlab is a self-hosted, cloud-like SaaS for homelab environments, built on
commonly used open-source technologies, services, and tools. It supports common
platforms — Proxmox, Kubernetes, and others — but it is not a solution that will
work for everyone. Hoomlab is very opinionated in what it provides, how it
provides those services, and how it is built, maintained, and run. The opinions
are the product.

## Problem Statement

Two problems, and a framing error that keeps them unsolved.

**IaC drifts.** Infrastructure-as-code at home is invoked, not resident. Between
invocations nothing watches, nothing reconciles, and state files add a third
version of the truth alongside the config and the world. Declared and actual
infrastructure diverge, and the cost of that divergence is paid again on every
change and in full on every rebuild.

**Production-like systems at home are a large investment.** The cloud gives you
images, managed clusters, autoscaling, and identity as first-class primitives.
At home, each of those is a bespoke assembly across many tools with ambiguous
seams between them, rebuilt alone by every operator who wants them, and
re-discovered from runbooks when something dies.

**People conflate simple with easy.** The existing answers pick one of two
escapes: easy — one-click panels that hide complexity until it leaks — or
general — IaC that makes no decisions for you and hands the whole investment
back. Neither is simple.

## Proposed Solution

Hoomlab is a simple solution to these problems, but it is not easy. Simple in
the sense that Hoomlab is an opinionated option for making homelab and
self-hosted infrastructure manageable, maintainable, and extensible: few moving
concepts, one way to do each thing, decisions made and documented rather than
deferred to the operator. Not easy in the sense that an operator is expected to
understand what they run; the tooling simplifies the work, it does not remove
it.

Hoomlab has two deliverables, in strict order:

**The bootstrap CLI.** A standalone tool in `tools/bootstrap`, run outside the
cluster it builds. From a set of configuration files, it takes bare Proxmox
nodes to a formed Proxmox cluster with a Talos Kubernetes cluster running on
it — the foundation Hoomlab itself will run on. Covered by ADR-0001 (the only
ADR written alongside this RFC).

**The Hoomlab service.** The primary, long-term way to run Hoomlab: a Kubernetes
deployment delivered by Helm chart onto the cluster bootstrap built, consuming
the same configuration files so that Hoomlab owns itself after the bootstrapping
process. Not built until bootstrap is proven and running.

## Design

Technology and delivery decisions are recorded as ADRs; this RFC only indexes
them.

| Decision                                                                                         | ADR      |
| ------------------------------------------------------------------------------------------------ | -------- |
| Bootstrap CLI (first deliverable; hclkit, proxmox-go-sdk, booty, Talos Go libraries)             | ADR-0001 |
| VM platform: Proxmox VE                                                                          | planned  |
| Kubernetes: Talos                                                                                | planned  |
| Configuration language: HCL                                                                      | planned  |
| Platform language: Go                                                                            | planned  |
| Bootstrap delivery: local CLI tool — no database, configuration-driven                           | planned  |
| Hoomlab service as a Kubernetes deployment via Helm chart                                        | planned  |

Owned primitives underneath: `github.com/donaldgifford/proxmox-go-sdk`,
`github.com/donaldgifford/booty`, `github.com/donaldgifford/hclkit`.

## Alternatives Considered

**Continue with general-purpose IaC (Terraform/OpenTofu plus Ansible).** Deeply
familiar, and structurally the source of the drift problem: invoked rather than
resident, state as a third truth, every decision deferred to the operator.
Rejected from experience.

**Adopt an existing management panel.** Easy, not simple: presentation over the
same unassembled primitives, deliberately unopinionated, and no help with the
investment problem. A different product, not a smaller version of this one.

**Cloud-native control planes (Crossplane, Cluster API).** Presume the
Kubernetes cluster Hoomlab needs to create, and import an ecosystem's weight in
exchange for generality Hoomlab deliberately refuses.

## Implementation Phases

### Phase 1: Bootstrap

Bootstrap CLI in `tools/bootstrap`: Proxmox cluster formation, certificates,
Talos cluster via PXE boot (the booty container providing the PXE and
cloud-init services). Proven and running before anything else is built.

### Phase 2: Hoomlab Service

Helm-delivered Kubernetes deployment; self-ownership handoff using the bootstrap
configuration files.

### Phase 3: Platform Features

The cloud-like primitives on top — deliberately unspecified here; each arrives
with its own documents.

## Risks and Mitigations

| Risk                                          | Impact | Likelihood | Mitigation                                                             |
| --------------------------------------------- | ------ | ---------- | ---------------------------------------------------------------------- |
| Opinionation narrows the audience             | Medium | Certain    | Accepted by design; documented as non-goal                             |
| Bootstrap scope creeps toward full automation | Medium | Medium     | "Simple, not easy" enforced in review; bootstrap stays operator-driven |
| Single maintainer                             | Medium | High       | Decisions live in ADRs, not heads; rebuild is a drill                  |

## Success Criteria

- One set of configuration files and the bootstrap tooling take bare Proxmox
  nodes to a running Talos cluster ready for Hoomlab.
- Hoomlab runs on the cluster it manages and owns itself using those same
  configuration files.
- A rebuild from configuration is a demonstrated drill, not an archaeology
  project.
- Every opinion Hoomlab holds is traceable to an ADR.

## References

- ADR-0001: Bootstrap CLI
- Planned ADRs indexed in Design
- `github.com/donaldgifford/proxmox-go-sdk`, `github.com/donaldgifford/booty`,
  `github.com/donaldgifford/hclkit`
