---
id: DESIGN-0002
title: "Talos cluster completion scope for bootstrap"
status: Draft
author: Donald Gifford
created: 2026-08-26
---

<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN-0002: Talos cluster completion scope for bootstrap

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
  - [How the previous cluster did it](#how-the-previous-cluster-did-it)
  - [Lessons the archive teaches](#lessons-the-archive-teaches)
- [Detailed Design](#detailed-design)
  - [Completion tiers](#completion-tiers)
  - [The ownership boundary](#the-ownership-boundary)
  - [Cilium delivery](#cilium-delivery)
  - [Extensions and schematics](#extensions-and-schematics)
  - [Node labels](#node-labels)
  - [The T2→T3 handoff contract](#the-t2t3-handoff-contract)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Defines what **done** means for the Talos half of bootstrap and where
bootstrap hands off to the gitops/IaC layer. Cilium (kube-proxy
replacement, BGP control plane, Gateway API) is the cluster's network
by policy, and its bootstrapping crosses the machine-config boundary —
so part of it is bootstrap's job and part of it must never be.
This doc draws that line, informed by a survey of how the previous
cluster (`~/code/homelab-archive`) built the same thing twice.

Status **Draft** by intent: IMPL-0002's drill exercises these
decisions and this doc is amended as the drill teaches.

## Goals and Non-Goals

### Goals

- Define completion tiers for `bootstrap` so "the cluster is up" is a
  checkable claim, not vibes.
- Specify the machine-config surface bootstrap must emit for a
  Cilium-networked Talos cluster (CNI none, kube-proxy disabled,
  manifest injection).
- Give extensions and node labels a first-class, per-node-class
  config model (the boot image is decided at image-build time —
  nothing downstream can fix it).
- Name what bootstrap hands the gitops layer and what it guarantees
  at that moment.

### Non-Goals

- **Cilium configuration**: BGP peering (`CiliumBGPClusterConfig` and
  friends), LB IP pools, Gateway/HTTPRoute objects, L2 announcements
  — k8s API objects, owned by the k8s IaC (ArgoCD/Kargo) and the
  UniFi IaC beside it. Bootstrap never knows the ASN.
- **GPU passthrough** (srv01's RTX 4060, `hostpci` in the VM spec,
  nvidia extensions and labels) — deferred; needs its own VM-spec +
  SDK work and is not on the drill's critical path.
- **Bare-metal Talos** (the future minis m02/m03 with
  `amd-ucode`/`amdgpu`) — bootstrap's node model is PVE VMs today;
  a bare-metal node class is future work.
- ArgoCD/Kargo installation, DNS, cert-manager, CSI — day-2 platform,
  gitops-owned.
- Hosting registry mirrors (a Harbor successor). The *machine-config
  surface* for mirrors may land in bootstrap; the mirror itself is
  infrastructure bootstrap consumes, not provides.

## Background

The bootstrap CLI (ADR-0001, DESIGN-0001) currently ends its Talos
story at: VMs PXE-boot via booty, join, `talos health` passes,
`kubectl get nodes` all Ready — with whatever CNI the emitted configs
default to. IMPL-0002 decided (2026-08-26) that the drill includes
the Cilium part, because kube-proxy replacement is a *machine-config*
decision (`cluster.proxy.disabled`) that cannot be deferred to a
later layer without a flannel→cilium migration nobody wants.

### How the previous cluster did it

The archive holds two generations. Gen 1 was hand-driven
(`talosctl gen config` + `helm install` + `kubectl apply` from a
README); Gen 2 was Terragrunt-driven Terraform with a key move:
**Cilium installed itself from inside the machine config**. Facts
that inform this design (paths under `~/code/homelab-archive`):

- **Machine-config knobs** (both generations):
  `cluster.network.cni.name: none` + `cluster.proxy.disabled: true`;
  kubelet `rotate-server-certificates: true` (paired with a
  kubelet-serving-cert-approver manifest); topology labels
  region=cluster / zone=node on every node;
  `allowSchedulingOnControlPlanes: false` and
  `node.kubernetes.io/exclude-from-external-load-balancers` on CPs;
  hostDNS `forwardKubeDNSToHost: true`; a tuned sysctls block.
- **KubePrism is load-bearing**: the Cilium values set
  `k8sServiceHost: localhost` / `k8sServicePort: 7445`, which only
  works because Talos's KubePrism listens there. The pairing was
  implicit in the archive; here it becomes an encoded invariant.
- **Gen 2 install mechanism** (`modules/talos/machine-config`):
  `cluster.extraManifests` carries the Gateway API CRDs (six pinned
  URLs at v1.4.1, standard channel + experimental TLSRoute — CRDs
  deliberately land *before* Cilium), cert-approver, metrics-server;
  `cluster.inlineManifests` carries a values ConfigMap plus a
  `cilium-install` Job (cilium-cli image, `backoffLimit: 10`,
  API server reached via `status.podIP:6443` because neither CNI nor
  kube-proxy exists yet). Talos applies all of it at bootstrap; the
  cluster converges to networked with zero operator action.
- **Key values**: `kubeProxyReplacement: true`, `ipam.mode:
  kubernetes`, `bgpControlPlane.enabled: true`,
  `gatewayAPI.enabled: true` (+ `enableAlpn`),
  `cgroup.autoMount.enabled: false` / `hostRoot: /sys/fs/cgroup`,
  `bpf.hostLegacyRouting: true` (the hostDNS pairing),
  `securityContext.capabilities` lists per Talos docs.
- **BGP reality** (for the IaC layers, recorded here to kill a
  misremembering): there was **no VLAN** for the Cilium network. The
  LB pool `172.20.10.0/24` was a *routed* range advertised over BGP
  sessions riding the ordinary node network (cluster ASN 65200 →
  UniFi 65100 at 10.10.11.1, MD5-authed). The UniFi/FRR side listed
  every node IP as an explicit neighbor — meaning node addresses are
  BGP session identity, which is why the DHCP reservations for the
  Talos VMs (IMPL-0002 TASK-4) matter beyond booty.
- **Extensions**: packer used composable profiles — `base` =
  qemu-guest-agent + iscsi-tools; `amd`/`intel` = ucode (+amdgpu);
  `nvidia`; `gvisor`; `kata-containers`; `zfs` — flattened into a
  factory schematic per node class, POSTed to
  `factory.talos.dev/schematics`, image pulled by schematic ID.

### Lessons the archive teaches

1. **The BGP CRs were ungoverned.** The four objects that made LB IPs
   actually route lived in a directory belonging to no kustomization,
   no ApplicationSet, no Terraform. The single biggest process gap —
   fixed here by *assigning* them (Non-Goals: k8s IaC owns them, as
   governed resources).
2. **Unvalidated values rot silently.** The Gen 2 values file lost
   the indentation under `loadBalancer:`, so `algorithm: maglev`
   became a bogus top-level key and maglev silently never applied.
   Values must be validated at emit time, not trusted.
3. **Two generations drifted** (discovery registry on in one, off in
   the other; three near-identical values files). One rendering
   authority — bootstrap's emit — with everything else consuming it.
4. **Pinned-by-image extensions are the anti-pattern.** Gen 1's
   nvidia patch pinned extension *images* and required a manual
   patch+upgrade dance; the schematic/profile model is the right
   shape and bootstrap adopts it.

## Detailed Design

### Completion tiers

| Tier | Claim | Verified by | Owner |
| --- | --- | --- | --- |
| **T1 — PVE ready** | Cluster formed and quorate, VM storage declared and node-restricted, production certificates on every node UI | `pve form` / `pve storage` / `pve certs` all report 0 applied | bootstrap |
| **T2 — Talos cluster up** | Every configured VM PXE-booted, joined, CNI present (Cilium when configured), all nodes `Ready`, credentials written | `talos health` passes; `kubectl get nodes` all Ready; re-runs apply nothing | bootstrap |
| **T3 — platform ready** | BGP routes advertised, Gateways serving, gitops reconciling, DNS/certs/CSI live | the IaC layers' own checks | k8s IaC + UniFi IaC |

**bootstrap's definition of done is T2.** The drill's Phase 4/5
gates map to T2 with Cilium in scope: "all nodes Ready" *implies*
the CNI came up, so the existing gate already verifies the Cilium
delivery without new machinery.

### The ownership boundary

The principle, stated once and applied everywhere:

> If it must be decided at image-build time or machine-config time,
> bootstrap owns it. If it is expressible as a Kubernetes API object
> on a running cluster, the IaC layer owns it.

Extensions, schematics, node labels, CNI choice, kube-proxy
disablement, manifest injection, registry mirrors: bootstrap.
BGP config, IP pools, Gateways, workloads, operators: IaC.
Cilium *installation* is the one deliberate boundary-crosser: it is
delivered by machine config (bootstrap) and adopted/reconfigured by
gitops afterward (IaC) — the same split Gen 2 proved.

### Cilium delivery

When the config selects Cilium, `talos emit` renders machine configs
with:

- `cluster.network.cni.name: none`, `cluster.proxy.disabled: true`.
- `cluster.extraManifests`: the Gateway API CRD set (version-pinned
  URLs, standard channel + experimental TLSRoute) and the
  kubelet-serving-cert-approver (required by
  `rotate-server-certificates`, which the emitted configs also set).
- `cluster.inlineManifests`: the values ConfigMap and the
  `cilium-install` Job, adopted from Gen 2 (pinned cilium-cli image,
  `backoffLimit` retry-until-API-server, `status.podIP` bootstrap
  workaround), running `cilium install --version=<pinned>` with the
  rendered values.
- The KubePrism invariant: emitted values say
  `k8sServiceHost: localhost:7445`; emit fails if the machine config
  would disable KubePrism.

Values come from an operator-supplied file referenced by the config
(the archive's proven values as the starting example), structurally
validated at emit: YAML-parse, and reject keys at the top level that
are not in the chart's value surface — the maglev lesson (OQ-B
considers embedding a default).

When the config selects `flannel` (the Talos default) or `none`
(operator brings their own CNI), none of the above renders; with
`none`, `talos health`'s node-Ready gate is relaxed to the checks
that don't require a CNI (OQ-F).

### Extensions and schematics

bootstrap adopts the packer generation's **profile model**:

- Named profiles in config map to extension lists. The drill needs
  exactly one: `base` = `siderolabs/qemu-guest-agent` +
  `siderolabs/iscsi-tools` (guest agent because every node is a PVE
  VM; iscsi-tools on *every* node because the democratic-csi node
  plugin mounts volumes wherever pods land).
- Each Talos node references profiles; emit/ipxe flattens them,
  builds the factory schematic, and booty's per-MAC assets serve the
  right image per node class — the per-MAC design already supports
  this.
- Deliberately *not* in the drill's profile set, with reasons that
  outlive the drill: `intel-ucode`/`amd-ucode` (microcode loads on
  the host; inert inside a KVM guest — bare-metal-only), `zfs` (the
  zfs-over-iscsi CSI path keeps ZFS on the PVE hosts; Talos nodes
  are initiators only), `nvidia-*` (gated on GPU passthrough,
  deferred), `gvisor`/`kata-containers` (wanted later; add as
  profiles when a workload needs them, not speculatively).

### Node labels

Emitted machine configs set `machine.nodeLabels`:

- Universal: `topology.kubernetes.io/region: <cluster>` and
  `topology.kubernetes.io/zone: <node>` (zone-per-node makes
  topology spread constraints meaningful on a small cluster).
- Control planes:
  `node.kubernetes.io/exclude-from-external-load-balancers`.
- Per-node extra labels from config (free-form map), so
  hardware-tied labels (`nvidia.com/gpu.present`) ride the same
  profiles that bake the hardware's extensions when those return.

### The T2→T3 handoff contract

At T2, bootstrap guarantees the gitops layer:

- API server reachable via the configured endpoint; `kubeconfig` +
  `talosconfig` written 0600.
- Gateway API CRDs present at the pinned version (they precede any
  Argo app that ships Gateways).
- Cilium running with `bgpControlPlane` and `gatewayAPI` enabled but
  **unconfigured** — no peers, no pools, no Gateways. The first BGP
  session is the IaC layer's first act, in the same change set as
  the UniFi side of the session (the archive's ungoverned-CRs gap,
  closed by assignment).
- Nothing on the cluster that gitops would have to adopt besides the
  Cilium release itself (and metrics-server/cert-approver if OQ-D
  keeps them in bootstrap).

## API / Interface Changes

Sketch, to be settled at implementation (schema names illustrative):

```hcl
talos {
  version = "v1.13.8"

  cluster {
    cni = "cilium" # "cilium" | "flannel" (default) | "none"
    cilium {
      version             = "v1.18.5"
      values              = "cilium-values.yaml" # operator file
      gateway_api_version = "v1.4.1"
    }
  }

  profile "base" {
    extensions = [
      "siderolabs/qemu-guest-agent",
      "siderolabs/iscsi-tools",
    ]
  }

  node "cp-01" {
    role     = "controlplane"
    profiles = ["base"]
    labels   = { "example.com/rack" = "r740a" }
    # existing: pve_node, vmid, mac, cores, memory, disk_gb,
    # storage, bridge
  }
}
```

- `talos emit` grows manifest rendering (extraManifests URL list,
  inlineManifests ConfigMap + Job) and label emission.
- `talos ipxe` / emit grow schematic resolution (profiles → factory
  schematic ID → per-class boot assets in the booty tree).
- `validate` grows: profile references resolve; the values file
  exists and parses; version pins present when `cni = "cilium"`.
- A raw machine-config **patch escape hatch** (per-node or per-role
  operator-supplied strategic patches) covers the long tail —
  sysctls, registry mirrors — without modeling every Talos knob;
  first-class knobs stay reserved for the load-bearing set above.

## Data Model

No new persistent state. Schematic IDs are derived (POST to the
factory is idempotent for a given schematic body) and recorded in
the emitted tree for the drill record; the config remains the single
source of truth.

## Testing Strategy

- **Unit (emit)**: golden-file tests on rendered machine configs —
  CNI none + proxy disabled present iff `cni = "cilium"`; manifest
  lists exactly as pinned; labels correct per role. The KubePrism
  invariant has a named test.
- **Unit (validate)**: values-file rejection cases, including a
  regression seeded with the archive's actual maglev indentation bug.
- **Schematic**: profile flattening is pure logic — table tests;
  factory interaction behind an interface with a fake.
- **e2e**: the drill itself (IMPL-0002 Phases 4–5) — nodes Ready is
  the Cilium delivery proof; convergence re-run applies nothing.

## Migration / Rollout Plan

1. This doc rides the drill branch and is amended as Phases 3–5
   teach; decisions that survive get folded back into DESIGN-0001
   where they touch its surfaces.
2. Emit-side work (CNI knobs, manifests, labels, profiles) lands
   before the drill's Phase 4 gate — it is on the critical path.
3. `pve storage` (proxmox-go-sdk#28) proceeds in parallel;
   unrelated surface.
4. GPU passthrough, bare-metal minis, gvisor/kata profiles: post-
   drill revisions of this doc, prioritized by actual workloads.

## Open Questions

- **OQ-A — image pull path at bootstrap.** *Resolved 2026-08-27*:
  the install Job pulls cilium-cli from quay.io and Cilium images
  from the VM network before any cluster infra exists, and the
  drill's boot network (Servers VLAN) has that egress —
  `https://quay.io/v2/` answers 401 from the booty host. The
  registry-mirrors surface stays future work, not a blocker.
- **OQ-B — embedded default values.** Ship a known-good values file
  inside bootstrap (operator file optional override), or require the
  operator file always? Leaning embedded-default once the drill
  proves a baseline.
- **OQ-C — version pin cadence.** Talos, Cilium, cilium-cli, and
  Gateway API CRDs are four independent pins with compatibility
  coupling. Where does the compatibility statement live?
- **OQ-D — metrics-server and cert-approver ownership.** Bootstrap
  (archive precedent, keeps `rotate-server-certificates` honest from
  minute one) or gitops (purer boundary)? Default: bootstrap ships
  cert-approver (it is machine-config-coupled), gitops ships
  metrics-server.
- **OQ-E — schematic granularity.** Per-profile-set images (fewer
  factory builds, classes share images) vs per-node. Default:
  per-unique-profile-set.
- **OQ-F — health semantics under `cni = "none"`.** Which
  `talos health` checks remain meaningful without a CNI, and does
  T2 still claim "Ready"? Only matters for the escape-hatch path.

## References

- ADR-0001, DESIGN-0001, IMPL-0002, INV-0001 (this repo)
- [proxmox-go-sdk#28](https://github.com/donaldgifford/proxmox-go-sdk/issues/28)
  — storage writes (T1 dependency)
- Prior-art survey source: `~/code/homelab-archive` — notably
  `modules/talos/machine-config` (Gen 2 inline-manifest install),
  `talos/gateway-api/` (BGP v2 CRs, CRD ordering, values),
  `packer/talos/` (profile model), `network/unifi/bgp/` (FRR peer
  config). Local reference configs: `docs/examples/` (gitignored —
  prior-cluster material, some archive files carry live PKI).
- Talos: KubePrism, host DNS, and Cilium-without-kube-proxy docs;
  Cilium BGP control plane v2 and Gateway API docs.
