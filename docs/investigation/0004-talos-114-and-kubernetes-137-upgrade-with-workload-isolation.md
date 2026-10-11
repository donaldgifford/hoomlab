---
id: INV-0004
title: "Talos 1.14 and Kubernetes 1.37 upgrade with workload isolation"
status: Concluded
author: Donald Gifford
created: 2026-10-11
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0004: Talos 1.14 and Kubernetes 1.37 upgrade with workload isolation

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [F1. Version targets and the upgrade path](#f1-version-targets-and-the-upgrade-path)
  - [F2. What 1.14 breaks](#f2-what-114-breaks)
  - [F3. Kubernetes 1.37 and the Cilium gate](#f3-kubernetes-137-and-the-cilium-gate)
  - [F4. Storage: in-tree volume plugins and democratic-csi](#f4-storage-in-tree-volume-plugins-and-democratic-csi)
  - [F5. Multi-document configuration and the bootstrap CLI](#f5-multi-document-configuration-and-the-bootstrap-cli)
  - [F6. Talos workload isolation (sandboxd)](#f6-talos-workload-isolation-sandboxd)
  - [F7. gVisor and Kata Containers](#f7-gvisor-and-kata-containers)
  - [F8. Native BGP vs Cilium BGP](#f8-native-bgp-vs-cilium-bgp)
  - [F9. Pod Security and seccomp](#f9-pod-security-and-seccomp)
  - [F10. Image signatures, image cache, pull-through cache](#f10-image-signatures-image-cache-pull-through-cache)
  - [F11. Logging, etcd metrics, metrics-server, HPA, KEDA](#f11-logging-etcd-metrics-metrics-server-hpa-keda)
  - [F12. Argo CD guide](#f12-argo-cd-guide)
  - [F13. Proxmox VE](#f13-proxmox-ve)
- [Conclusion](#conclusion)
  - [Rebuild or upgrade in place?](#rebuild-or-upgrade-in-place)
  - [gVisor as the default, Kata for jobs, or gVisor alone?](#gvisor-as-the-default-kata-for-jobs-or-gvisor-alone)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

Can the live cluster move from Talos 1.13 / Kubernetes 1.36 to Talos
1.14 / Kubernetes 1.37 **in place** — without redoing storage
(democratic-csi iSCSI), networking (Cilium, BGP), or security — while
picking up the 1.14 features worth having (multi-document config,
workload isolation, native BGP, image verification, caching, logging,
etcd metrics)? And for workload isolation specifically: should gVisor
be the cluster-wide default runtime, should Kata Containers back a
dedicated node class for Temporal and CI jobs (GitHub Actions,
Buildkite), or is gVisor alone enough?

## Hypothesis

The Talos and Kubernetes upgrades are in-place (`talosctl upgrade`,
`talosctl upgrade-k8s`) and the cluster does not need a rebuild;
runtime isolation (gVisor, Kata) is additive — new extensions on new
or re-imaged nodes plus RuntimeClasses — so it only "rebuilds" the
nodes that host it. gVisor as a blanket default is probably the wrong
trade for a cluster whose platform layer is host-coupled (CNI, CSI,
databases).

## Context

Talos 1.14.0 shipped 2026-09-03 with Kubernetes 1.37 as its default.
1.14 is a large release: most of the remaining `v1alpha1` surface
splits into standalone documents, a new `sandboxd` workload-isolation
plane, embedded GoBGP, and several breaking changes. The cluster also
runs CI (ARC runner scale sets, BuildKit, Buildkite) and Temporal
workers, which today execute under `runc` in `privileged` Pod Security
namespaces — the isolation question is about real workloads, not a
future one.

**Triggered by:** the Talos 1.14 release; follows
[INV-0003](0003-platform-reuse-for-hoomlab.md) (the T3 GitOps layer
lives in the private homelab repo) and
[IMPL-0003](../impl/0003-talos-storage-plane.md) (storage plane, the
`setInterfaces` legacy-network decision).

## Approach

1. Read-only facts from the live cluster: Talos/Kubernetes/Cilium
   versions, CPU flags inside the guests, StorageClasses / CSIDrivers
   / PV sources, RuntimeClasses, SELinux state, Pod Security namespace
   labels, etcd scrape targets.
2. Read the bootstrap CLI for every surface the upgrade touches:
   machinery version, installer image reference, `setInterfaces`,
   the completion surface (`completion.go`), catalog classes.
3. Inspect the vendored `pkg/machinery` v1.13.9 for the multi-doc
   network types the `setInterfaces` comment says don't exist.
4. Primary-source research (Talos 1.14 release notes and docs,
   Kubernetes 1.37 changelog, Cilium/KEDA/metrics-server/democratic-csi
   compatibility, gVisor/Kata/Proxmox docs, GKE/ARC/Buildkite isolation
   guidance), including the Sidero guides named in the request.

Claims marked **[unverified]** could not be confirmed from a primary
source and need a test before they are relied on.

## Environment

| Component | Version / Value |
| --------- | --------------- |
| Talos (cluster, talosctl) | v1.13.8 |
| Talos machinery (bootstrap) | v1.13.9 |
| Kubernetes | v1.36.3 |
| Cilium | v1.20.1 (kube-proxy replacement, BGP control plane v2, Gateway API v1.6.1) |
| CSI | democratic-csi, 4 iSCSI drivers; every PV is CSI, none in-tree |
| Schematic extensions | iscsi-tools, qemu-guest-agent, util-linux-tools |
| Nodes | 3 control plane + 5 workers, PXE-booted Proxmox VMs, `cpu = host` |
| Guest CPU | Xeon Gold 6132 (Skylake-SP); `vmx` visible inside guests |
| SELinux (Talos) | enabled, permissive |
| RuntimeClasses | none |
| Pod Security | Talos default (enforce baseline); CI/monitoring/CSI namespaces labelled `privileged` |
| etcd scraping | none (no etcd Service / ServiceMonitor) |
| Proxmox VE | not checked — run `pveversion -v` on each host (F13) |

## Findings

### F1. Version targets and the upgrade path

- **Talos v1.14.2** (2026-09-29) is current: Linux 6.18, containerd
  2.3.6, runc 1.5.2, etcd 3.7.1, default Kubernetes 1.37.1. Talos 1.14
  supports Kubernetes 1.33–1.37; 1.13 supports 1.31–1.36. The latest
  1.13 patch is v1.13.11.
- **Path:** the upgrade guide requires the latest patch of the current
  minor first: 1.13.8 → **1.13.11** → **1.14.2**, staying on
  Kubernetes 1.36 (supported by both), then `talosctl upgrade-k8s` to
  1.37 as a separate step (F3 gates it). Talos upgrades never change
  the Kubernetes version and preserve the machine config.
- **Kubernetes 1.37.1** is current (1.37.0 released 2026-08-26); the
  latest 1.36 patch is 1.36.5.

### F2. What 1.14 breaks

Breaking changes from the 1.14.0 release notes, filtered to what
touches this cluster or the bootstrap CLI:

| Change | Impact here |
| ------ | ----------- |
| `ghcr.io/siderolabs/installer` no longer published | None for images — bootstrap already emits `factory.talos.dev/installer/<schematic>:<ver>`. The 1.14 docs reference `factory.talos.dev/metal-installer/…`; confirm the `installer/` path is still served for 1.14 before upgrading **[unverified]**. |
| `apply-config --mode=reboot` removed | Runbooks only; config applies live, reboot explicitly with `talosctl reboot`. |
| etcd `/metrics` + `/health` move to port **2383**; 2379 is gRPC-only; etcd 3.6+ only; TLS 1.3 minimum | Nothing scrapes etcd today, so nothing breaks. Shapes how F11 exposes it. |
| FlexVolume host path removed | None — no FlexVolume. |
| ICMP `send_redirects=0` default, Flannel nftables | None (Cilium). |
| multipathd `ExtensionServiceConfig` must move before upgrade | None (no multipath). |

Deprecated `v1alpha1` fields keep working (F5). Nothing in this list
forces a rebuild.

### F3. Kubernetes 1.37 and the Cilium gate

- No stable or beta API removals; the only group dropped is the alpha
  `scheduling.k8s.io/v1alpha2`. GA in 1.37: `metrics.k8s.io` v1, Pod
  Certificates, ClusterTrustBundle, DRA device taints. User namespaces
  (`hostUsers: false`) and ImageVolume went GA in **1.36** — already
  available.
- Urgent notes that apply:
  - **SELinuxMount GA, on by default.** Talos runs SELinux
    permissive, so relabel failures shouldn't block iSCSI mounts —
    still verify one CSI attach after the upgrade.
  - **cAdvisor flags removed**, and the metrics
    `container_cpu_load_average_10s` / `container_tasks_state` are
    gone — audit kube-prometheus-stack dashboards and alerts.
- **Cilium is the gate.** Cilium 1.20 is tested against Kubernetes
  1.33–1.36; 1.37 is first tested in **1.21**, which is at
  `v1.21.0-pre.3` (2026-10-02). The current stable is 1.20.2. KEDA
  2.21 likewise lists 1.34–1.36. Running Talos 1.14 on Kubernetes 1.36
  is fully supported, so the Kubernetes bump waits for Cilium 1.21 GA
  without holding up Talos.
- Cilium BGP: `CiliumBGPPeeringPolicy` (v1) was removed in 1.19; the
  cluster is already on the v2 CRDs, so no BGP migration rides along.

### F4. Storage: in-tree volume plugins and democratic-csi

- **The in-tree plugin deprecations don't touch this cluster.** All 4
  StorageClasses provision via `org.democratic-csi.*`, every PV is a
  CSI PV, and in-tree `iscsi` never had a CSI-migration gate to remove.
  (In-tree `iscsi`/`nfs`/`fc` are actually still documented in 1.37;
  Portworx and `gitRepo` went in 1.36; cephfs/rbd/glusterfs and the
  cloud plugins are long gone.)
- **The node still needs `iscsi-tools`.** democratic-csi's node plugin
  reaches the host's `iscsiadm`/`iscsid` with `hostPID: true` and
  `ISCSIADM_HOST_STRATEGY=nsenter` — keep the extension in every
  worker schematic, including any new runtime profile (F7).
- democratic-csi v1.9.5 / chart 0.15.1 (2026-01) is the latest; no
  1.37-specific testing or issues found. It relies on stable CSI and
  `storage.k8s.io/v1` APIs, so expect it to work **[unverified]**. The
  chart's default sidecars are old (provisioner v3.6, attacher v4.4,
  registrar v2.9) — worth a separate sidecar bump in GitOps.
- **The real storage risk is F6, not the in-tree deprecation**:
  `sandboxd` may sever the `nsenter` path democratic-csi depends on.

### F5. Multi-document configuration and the bootstrap CLI

- **Deprecated, not removed.** The 1.14 upgrade guide: every listed
  `v1alpha1` field "is deprecated, but still supported, so a machine
  configuration which only uses v1alpha1 keeps working after the
  upgrade." A deprecated field and its replacement document are
  **mutually exclusive** — a config setting both is rejected. No
  conversion tool was found; migrations are manual.
- 1.14 moves the Kubernetes surface into documents: `KubeletConfig`,
  `KubeAPIServerConfig` + `KubeAdmissionControlConfig`,
  `KubeInlineManifestConfig`, `KubeExternalManifestConfig`,
  `KubeProxyConfig`, `KubePrismConfig`, `KubeNodeConfig` (node
  labels/taints), `UnattendedInstallConfig` (`machine.install`),
  `ImageCacheConfig`, `SysctlConfig`, `SecurityProfileConfig`, and
  more. JSON6902 patches still can't target multi-doc configs.
- **Correction to an existing code comment.** `setInterfaces`
  (`tools/bootstrap/internal/talos/machineconfig.go`) justifies the
  legacy `machine.network.interfaces` with "machinery v1.13 ships no
  multi-doc network device type". That is wrong: the vendored
  machinery v1.13.9 has `LinkAliasConfig` (CEL selector, e.g.
  `mac(link.permanent_addr) == "…"`), `LinkConfig` (with `addresses`,
  `mtu`, `routes`), and `DHCPv4Config` — the network documents landed
  in Talos 1.12. The multi-doc form of our shape is: one
  `LinkAliasConfig` per slot selecting by MAC, `DHCPv4Config` for the
  primary, `LinkConfig` with address + MTU for static slots. Gotchas:
  explicit link config disables default DHCP on physical links (the
  primary needs its own `DHCPv4Config`), and 1.14.1 fixed link-alias
  change notification.
- **What the CLI must change for 1.14** (none of it urgent — the 1.13
  output keeps working on 1.14 nodes):
  1. Bump `pkg/machinery` to v1.14 and the default Talos/Kubernetes
     versions. Under the 1.14 version contract `generate` will likely
     emit new-style documents itself — including
     **`SecurityProfileConfig` with `workloadIsolation: true`** and a
     `KubeProxyConfig` that defaults to **enabled** **[unverified]**.
     Both collide with our completion surface: kube-proxy must stay
     disabled for Cilium KPR, and isolation must not switch on
     silently (F6).
  2. Rework `completion.go` onto documents: kubelet
     `rotate-server-certificates` → `KubeletConfig`; `extraManifests`
     (cert-approver, Gateway API CRDs) → `KubeExternalManifestConfig`;
     Cilium inline manifests → `KubeInlineManifestConfig`;
     `assertKubePrism` must read `KubePrismConfig`. Because old and new
     forms are mutually exclusive, this is a whole-surface move, not
     piecemeal.
  3. Migrate `setInterfaces` to the network documents (above). Doing
     it against the live fleet is a network change applied to running
     nodes — one worker first.
  4. Emit's byte-diff Check will flag every node once the documents
     change shape; that is expected and needs a runbook note, same as
     the IMPL-0003 storage-plane rollout.

### F6. Talos workload isolation (sandboxd)

- **It is not gVisor or Kata, and not user namespaces.** 1.14 runs
  "the container runtime plane — CRI containerd, the kubelet, and all
  pods" inside a dedicated PID + mount namespace anchored by a new
  `sandboxd` service in its own SELinux domain (`sandboxd_t`),
  isolating the runtime from `machined` (PID 1) and its file
  descriptors. If `sandboxd` dies, Talos recreates the namespace and
  relaunches CRI, kubelet, and pods without a reboot.
- **Enabled by a `SecurityProfileConfig` document**
  (`workloadIsolation: true`). `gen config` for 1.14 emits it on by
  default for **new** clusters; upgraded clusters stay non-isolated
  ("upgrades change nothing on their own").
- **Documented breakage:** in-tree iSCSI (kubelet can't reach host
  `iscsid`) and FlexVolume. Sidero says CSI node plugins are
  unaffected — but democratic-csi's Talos mode `nsenter`s into the
  host via `hostPID`, and under `sandboxd` "host PID" is the sandbox's
  PID namespace. Whether `iscsiadm` still reaches the real host is an
  **open community question [unverified]**. There is also a known
  boot-time CRI restart loop (talos#14374) and conflicting reports on
  whether enabling requires a reboot.
- It hardens the node against container escapes reaching Talos
  internals — complementary to, not a substitute for, per-pod
  sandboxes (F7).

### F7. gVisor and Kata Containers

**On Talos.** Both are system extensions baked into the schematic:
`siderolabs/gvisor` (handlers `runsc`, `runsc-kvm`) and
`siderolabs/kata-containers` (handlers `kata` = Cloud Hypervisor,
`kata-qemu`). Each ships its own containerd handler config, so the
cluster side is just RuntimeClasses. gVisor needs the sysctl
`user.max_user_namespaces: "11255"` (Talos defaults it to 0) — the
same sysctl `hostUsers: false` pods need, and the README notes it
relaxes KSPP hardening. The Kata RuntimeClasses declare pod overhead
of 130Mi/250m (CLH) and 160Mi/250m (QEMU). Kata 4.x is upstream; the
Talos extension tracks 3.x **[unverified pin]**.

**Bootstrap fit.** The CLI already has the right primitive: `profile`
blocks are named extension sets, nodes reference them, emit flattens
each node's profiles into its own schematic, and the catalog groups
nodes by (role, schematic) class with a per-node `install_image`. A
"sandbox worker" class is therefore a new profile plus nodes that
reference it. The **gap** is node labels and taints: templates are
per role, and there is no per-node or per-class `nodeLabels` /
`nodeTaints` surface, so a Kata pool can't be fenced off from
config alone.

**Nested virtualisation.** Kata needs `/dev/kvm` in the guest. The
guests already see `vmx` because bootstrap creates VMs with
`cpu = host`, which implies nested virt is enabled on the hosts;
whether `/dev/kvm` appears inside Talos once the Kata extension is
present is **[unverified]** — check on the first node. Costs:
Proxmox can't live-migrate a VM with nesting active, and the nested
layer adds latency on exits and page faults.

**gVisor tradeoffs.**

- Default platform `systrap` needs no virtualisation; gVisor's own
  docs recommend it inside VMs. CPU-bound work runs near native;
  file I/O and networking (netstack) carry the overhead. Databases are
  a poor fit (one benchmark: +125% disk-I/O time vs +17% for Kata;
  large `/dev/shm` copies 1.7–4.5x slower).
- Not supported: privileged pods, `hostNetwork`, `hostPath` (on GKE),
  raw block devices, custom seccomp/AppArmor profiles, eBPF tooling;
  io_uring off by default; partial FUSE/iptables.
- Docker-in-gVisor works with flags (`--net-raw`, dockerd
  `--iptables=false`); rootless BuildKit in gVisor on Kubernetes is
  undocumented **[unverified]**.
- gVisor's production guide: sandboxing "should generally not be
  used to sandbox every application".

**Kata tradeoffs.**

- A real VM boundary with a full guest kernel: near-complete Linux
  compatibility, and `privileged: true` applies inside the guest — the
  documented way to run privileged Docker/BuildKit safely.
  `/var/lib/docker` must be an in-memory `emptyDir` or a loop-mounted
  ext4 image (virtio-fs can't be an overlayfs upper layer).
- Costs: per-pod overhead (~130–160Mi + 250m), 1–2s start, virtio-fs
  slow for metadata-heavy work, one report of a 46% cross-node
  throughput drop with Cilium.

**Cilium interaction (both).** Cilium's socket LB can't hook inside a
gVisor netstack or a Kata guest kernel; the documented fix is
`socketLB.hostNamespaceOnly=true` (ClusterIP translation falls back to
tc at the veth). Without it, ClusterIPs — including kube-dns — are
untranslated in sandboxed pods. That is a **cluster-wide** Cilium
setting with open caveats (cilium#46680 intermittent pod→host TCP).

**Industry practice.** GKE Sandbox is opt-in per pod
(`runtimeClassName`) on dedicated node pools, not a cluster default;
gVisor for untrusted apps, microVMs for unsupervised code execution.
ARC's docs: workflows "run arbitrary code", run them isolated from
production; DinD needs privileged. Buildkite agent-stack-k8s: contain
untrusted builds in containers or VMs, privileged BuildKit only in
trusted environments, `prohibit-kubernetes-plugin` to stop pipelines
overriding the pod spec (where `runtimeClassName` would be pinned).
Hosted CI runs every job in a fresh VM.

### F8. Native BGP vs Cilium BGP

Talos 1.14 embeds GoBGP via `BGPInstanceConfig`: numbered and
unnumbered (RFC 8950) peering, ECMP, BFD (default domain only),
`installRoutes`, VRFs. Its purpose is **host/fabric routing** —
advertising node loopback and link addresses as host routes and
learning routes from the fabric. The documented coexistence pattern
runs Cilium (or MetalLB) as the workload speaker peering over a veth
into a VRF-bound Talos instance, with the default-domain instance
importing selected prefixes. Limits: one instance per routing domain,
no policy engine (talos#14252), ~0.5s route flap on ECMP path add
(talos#14502).

For this cluster — Cilium already peers with the router for
LoadBalancer/pod prefixes, nodes are single-homed on the primary
plane — **Talos BGP adds nothing today**, and running both speakers in
the default netns risks a port-179 clash **[unverified]**. It becomes
interesting only for routed node loopbacks, BFD, or ECMP to the fabric.

### F9. Pod Security and seccomp

- **Pod Security** — Talos's default admission config enforces
  `baseline`, audits and warns `restricted`, exempts `kube-system`.
  In 1.14 the field moves to `KubeAdmissionControlConfig`.
  Per-namespace labels (already in use: CI, CSI, monitoring, alloy,
  tailscale are `privileged`) belong to GitOps. The default is right;
  the improvement is shrinking the `privileged` set — Kata CI pods can
  drop host-level privilege even while running privileged *inside*
  the guest, though PSA still evaluates the pod spec, so those
  namespaces stay `privileged` at the label level.
- **seccomp** — Talos already turns on the kubelet's
  `defaultRuntimeSeccompProfileEnabled` (the `seccompDefault`
  equivalent), so every pod gets `RuntimeDefault` unless it opts out;
  in 1.14 that is a `KubeletConfig` field. Custom profiles go in
  `machine.seccompProfiles` (written to
  `/var/lib/kubelet/seccomp/profiles/`, referenced as
  `Localhost` / `profiles/<name>.json`). gVisor pods don't honour
  seccomp profiles (the Sentry is the filter).
- **User namespaces** (`hostUsers: false`, GA in 1.36) are the
  cheapest broad hardening — container root is not host root — once
  the `user.max_user_namespaces` sysctl is set. Incompatible with
  `hostNetwork`/`hostPID`/`hostIPC`.

### F10. Image signatures, image cache, pull-through cache

- **Image signature verification** — `ImageVerificationConfig` landed
  in Talos **1.13.0**, so it can be trialled before the upgrade.
  Ordered rules matched by `image` glob, first match wins:
  `keyless` (cosign issuer + subject/subjectRegex; needs internet for
  TUF), public key, `skip`, `deny`; unmatched images are allowed (a
  closed policy ends with `image: "*"`, `deny: true`). It covers both
  Talos system images and workload images; cached images aren't
  re-verified. Documented signers exist for `registry.k8s.io`,
  `ghcr.io/siderolabs`, and `factory.talos.dev`. Known issue:
  verifying `registry.k8s.io/*` broke etcd startup on 1.13.2
  (talos#13342; fixes in 1.13.6/1.13.10). Talos-side, likely live
  **[unverified]**.
- **Image cache** — an offline per-node cache served by `registryd`,
  built with `talosctl images cache-create` and baked into
  **ISO/USB/disk-image** boot media, with an `IMAGECACHE` volume.
  PXE boot isn't a documented carrier, so it doesn't fit this
  cluster's boot path. Skip.
- **Pull-through cache** — `RegistryMirrorConfig` (+
  `RegistryAuthConfig`, `RegistryTLSConfig`/`TrustedRootsConfig`)
  points docker.io/ghcr.io/etc. at a mirror (Harbor needs
  `overridePath: true`). This is the useful half: it cuts upstream
  rate-limit exposure for CI-heavy pulls and makes rebuilds faster.
  The mirror itself (Harbor, zot, or `registry:2` in proxy mode)
  should live off-cluster so the cluster can pull its own images
  during recovery.

### F11. Logging, etcd metrics, metrics-server, HPA, KEDA

- **Logging** — `machine.logging.destinations` ships Talos service
  logs (UDP/TCP, `json_lines`) and `KmsgLogConfig` ships kernel logs
  (or `talos.logging.kernel=` for early boot). Pod logs stay with the
  in-cluster collector (Alloy is already deployed); point the Talos
  destinations at an Alloy/Vector listener. Talos-side, live.
- **etcd metrics** — two options under 1.14: scrape port 2383 with
  etcd client mTLS (needs client certs in the monitoring namespace),
  or set `cluster.etcd.extraArgs.listen-metrics-urls:
  http://<node-ip>:2381` for plain HTTP on control-plane nodes (keeps
  its port across the 2379→2383 move). The plain-HTTP listener is the
  simpler fit for kube-prometheus-stack's `kubeEtcd` scrape; bind it
  to the node IP, not `0.0.0.0`.
- **metrics-server** — prerequisites already exist: bootstrap emits
  kubelet `rotate-server-certificates` plus the
  kubelet-serving-cert-approver. metrics-server itself (v0.9.0,
  compatible with 1.34+) is a GitOps deploy; no `--kubelet-insecure-tls`
  needed.
- **HPA guide** — a generic CPU-HPA walkthrough with metrics-server as
  the prerequisite; nothing Talos- or KEDA-specific.
- **KEDA for Temporal** — KEDA's `temporal` scaler (since 2.17)
  scales on task-queue backlog (`targetQueueSize`,
  `activationTargetQueueSize`, `queueTypes`, worker-deployment
  versioning, `includeRunningWorkflowCount` to keep workers alive
  while workflows run). KEDA serves `external.metrics.k8s.io` itself;
  metrics-server is needed only for CPU/memory triggers. KEDA 2.21
  dropped some Temporal scaler fields (`buildId`, `selectAllActive`,
  `selectUnversioned`) and doesn't yet list Kubernetes 1.37. Watch
  scale-to-zero killing in-flight work (keda#7368).

### F12. Argo CD guide

The Sidero guide installs Argo CD from the control-plane machineconfig
as an inline manifest (or Omni manifest sync, `mode: one-time`). The
cluster already has Argo CD, self-managed from the private GitOps
repo, and bootstrap deliberately stops at "Cilium ready" (DESIGN-0002).
Embedding Argo CD in machineconfig would make Talos re-apply a
manifest Argo CD also owns. **Not useful here.** The one idea worth
keeping: if a zero-touch first sync is ever wanted, a one-time
`KubeInlineManifestConfig` carrying only the root Application is the
narrowest form.

### F13. Proxmox VE

- **Proxmox VE 9.2** (2026-05-21) is current: Debian 13.5, kernel
  7.0, QEMU 11.0, a Dynamic Load Balancer that live-migrates HA
  guests, SDN WireGuard/BGP fabrics, a custom CPU model GUI, HA
  arm/disarm for maintenance. **9.1** added a fine-grained nested-virt
  vCPU flag usable on named CPU models instead of `host`.
- Implications: Kata nodes can't live-migrate with nesting active, so
  they must be excluded from the 9.2 load balancer / HA migration.
  The cluster's PVE version wasn't checked in this investigation —
  run `pveversion -v` on each host; an 8→9 move has its own upgrade
  guide and would need a proxmox-go-sdk compatibility check against
  the PVE 9 API **[unverified]**. Nothing in the Talos 1.14 upgrade
  requires a PVE change.

## Conclusion

**Answer: Yes — in place, no rebuild.** Talos 1.14 and Kubernetes
1.37 are rolling upgrades; runtime isolation is additive to selected
nodes.

### Rebuild or upgrade in place?

| Area | Verdict |
| ---- | ------- |
| Talos 1.13 → 1.14 | In place: `talosctl upgrade` per node (1.13.11 first). Config preserved; deprecated fields still accepted. |
| Kubernetes 1.36 → 1.37 | In place: `talosctl upgrade-k8s`, **after** Cilium 1.21 GA. |
| Storage | No change. CSI-only, so the in-tree deprecations are moot. The one risk is opting into `sandboxd` (F6) — that is opt-in and testable on one node. |
| Networking | No change. Cilium BGP v2 stays; Talos BGP not needed. `socketLB.hostNamespaceOnly` is a live Cilium values change when sandboxes arrive. |
| Security | Additive: PSA default unchanged, seccomp already default, user namespaces via one sysctl, image verification via one document. |
| gVisor / Kata | New extensions → new schematic → `talosctl upgrade --image` re-images the target node in place (the same mechanism used for the `88d1f7a5` schematic), or new worker VMs via bootstrap. Only those nodes change. |
| Bootstrap CLI | Its 1.13 output keeps working on 1.14 nodes. The machinery-1.14 move is a deliberate whole-surface rework (F5), with explicit knobs for anything the 1.14 contract would otherwise default. |

### gVisor as the default, Kata for jobs, or gVisor alone?

**Don't make gVisor the default.** Pros: blanket syscall isolation
for everything. Cons outweigh it here: Cilium, the democratic-csi node
plugins, Alloy, node-exporter, and every `hostNetwork`/privileged
DaemonSet can't run under gVisor, so the "default" needs a `runc`
carve-out for exactly the most privileged pods — inverting the safe
default. CNPG, OpenBao, and Prometheus take the I/O penalty for no
threat-model gain (they are our own trusted platform). gVisor's own
guidance says not to sandbox everything, and GKE doesn't.

**gVisor alone isn't enough for CI.** It can't run privileged
Docker/BuildKit, and rootless BuildKit inside gVisor is unproven. CI
runners execute code from PRs and from dependencies — the case where
every vendor reaches for a VM boundary.

**Recommended model — three tiers, runc stays the default:**

| Tier | Workloads | Runtime | Notes |
| ---- | --------- | ------- | ----- |
| Platform | CNI, CSI, observability, databases, OpenBao, Argo CD | `runc` | PSA baseline/restricted where charts allow; `hostUsers: false` where the pod doesn't need host namespaces. |
| Semi-trusted | Temporal workers running our own code, internal services | `runc` + `hostUsers: false`; **gVisor opt-in** for activities that execute user-supplied or generated code | gVisor fits here: CPU-bound, modest syscall surface, no privilege. |
| Untrusted | ARC runners, Buildkite agents/job pods, BuildKit, Temporal activities that build or run arbitrary code | **Kata** (`kata` CLH, or `kata-qemu`) on a tainted, labelled worker class | Privileged DinD/BuildKit inside the guest; nested virt; no live migration. |

Enforcement (RuntimeClass required in the untrusted namespaces,
tolerations only there) belongs to GitOps — Kyverno/VAP, and
Buildkite's `prohibit-kubernetes-plugin`.

## Recommendation

Sequenced so each step is independently reversible:

1. **On 1.13 now (no version change).** Upgrade Talos to v1.13.11.
   Optionally trial `ImageVerificationConfig` (start with `skip`/allow
   rules plus one `keyless` rule for `ghcr.io/siderolabs/*`), Talos
   logging destinations to Alloy, and the etcd plain-HTTP metrics
   listener — all live patches. Check `pveversion -v` on each host.
2. **Fix the record.** Correct the `setInterfaces` comment and
   IMPL-0003's Phase 2 note ("machinery v1.13 ships no multi-doc
   device type"): the network documents exist in machinery 1.13.
3. **DESIGN-0005 — bootstrap on Talos 1.14 multi-doc.** Machinery
   bump; completion surface onto `KubeletConfig` /
   `KubeExternalManifestConfig` / `KubeInlineManifestConfig` /
   `KubePrismConfig`; explicit `KubeProxyConfig` disabled for Cilium;
   `setInterfaces` → `LinkAliasConfig` + `LinkConfig` +
   `DHCPv4Config`; an explicit `workload_isolation` knob (default
   false) instead of inheriting the 1.14 `gen config` default; and a
   per-class (profile-scoped) node labels/taints surface for sandbox
   workers (moving to `KubeNodeConfig`). Validate the installer path
   (`installer/` vs `metal-installer/`).
4. **Talos 1.14.2 rollout** on Kubernetes 1.36.5: workers first, then
   control planes, `talosctl health` between nodes; rewrite runbooks
   that use `--mode=reboot`.
5. **Kubernetes 1.37** once Cilium 1.21 is GA (and KEDA lists 1.37):
   bump Cilium first, then `talosctl upgrade-k8s --dry-run` → apply;
   audit dashboards for the removed cAdvisor metrics; verify a CSI
   attach/detach.
6. **`sandboxd` spike** on one worker: enable `SecurityProfileConfig`,
   then exercise democratic-csi attach, detach, resize, and node
   reboot. Adopt fleet-wide only if iSCSI survives; otherwise wait
   for democratic-csi guidance.
7. **Sandbox worker class.** Profile with `siderolabs/kata-containers`
   (+ gVisor if wanted) + `iscsi-tools` + `qemu-guest-agent`; set
   `user.max_user_namespaces`; confirm `/dev/kvm` in the guest; pin
   the VMs out of PVE HA/load-balancer migration. GitOps:
   RuntimeClasses, Cilium `socketLB.hostNamespaceOnly=true` (test
   first — it is cluster-wide), move ARC/Buildkite/BuildKit job pods
   onto `runtimeClassName: kata` with in-memory `/var/lib/docker`.
8. **GitOps follow-ups** (private homelab repo): metrics-server,
   KEDA with the `temporal` scaler, a pull-through registry mirror +
   `RegistryMirrorConfig`, kube-prometheus-stack `kubeEtcd`,
   democratic-csi sidecar bump, `hostUsers: false` on charts that
   allow it.
9. **Skip:** gVisor as the default runtime, Talos image cache (no PXE
   carrier), Talos native BGP (no use case yet), the Argo CD-in-
   machineconfig guide.

## References

- [INV-0003: Platform reuse for hoomlab](0003-platform-reuse-for-hoomlab.md)
- [IMPL-0003: Talos storage plane](../impl/0003-talos-storage-plane.md)
- [DESIGN-0002: Talos cluster completion scope](../design/0002-talos-cluster-completion-scope-for-bootstrap.md)
- [DESIGN-0004: Network planes and interfaces](../design/0004-network-planes-and-interfaces-for-the-bootstrap-cli.md)
- Talos v1.14.0 / v1.14.2 releases —
  <https://github.com/siderolabs/talos/releases/tag/v1.14.0>,
  <https://github.com/siderolabs/talos/releases/tag/v1.14.2>
- What's new in Talos 1.14 —
  <https://docs.siderolabs.com/talos/v1.14/getting-started/what's-new-in-talos>
- Support matrix —
  <https://docs.siderolabs.com/talos/v1.14/getting-started/support-matrix>
- Upgrading Talos —
  <https://docs.siderolabs.com/talos/v1.14/configure-your-talos-cluster/lifecycle-management/upgrading-talos>
- Upgrading Kubernetes —
  <https://docs.siderolabs.com/kubernetes-guides/advanced-guides/upgrading-kubernetes>
- SecurityProfileConfig —
  <https://docs.siderolabs.com/talos/v1.14/reference/configuration/runtime/securityprofileconfig>
- LinkAliasConfig —
  <https://docs.siderolabs.com/talos/v1.14/reference/configuration/network/linkaliasconfig>
- Talos BGP —
  <https://docs.siderolabs.com/talos/v1.14/networking/bgp/overview>
- Verifying image signatures —
  <https://docs.siderolabs.com/talos/v1.14/security/verifying-image-signatures>
- Image cache / pull-through cache —
  <https://docs.siderolabs.com/talos/v1.14/configure-your-talos-cluster/images-container-runtime/image-cache>,
  <https://docs.siderolabs.com/talos/v1.14/configure-your-talos-cluster/images-container-runtime/pull-through-cache>
- Logging —
  <https://docs.siderolabs.com/talos/v1.14/configure-your-talos-cluster/logging-and-telemetry/logging>
- Sidero guides: Argo CD, HPA, Pod Security, seccomp, etcd metrics,
  metrics-server, user namespaces —
  <https://docs.siderolabs.com/kubernetes-guides/advanced-guides/deploy-argocd>,
  <https://docs.siderolabs.com/kubernetes-guides/advanced-guides/hpa>,
  <https://docs.siderolabs.com/kubernetes-guides/security/pod-security>,
  <https://docs.siderolabs.com/kubernetes-guides/security/seccomp-profiles>,
  <https://docs.siderolabs.com/kubernetes-guides/monitoring-and-observability/etcd-metrics>,
  <https://docs.siderolabs.com/kubernetes-guides/monitoring-and-observability/deploy-metrics-server>,
  <https://docs.siderolabs.com/kubernetes-guides/security/usernamespace>
- Talos extensions (gVisor, Kata) —
  <https://github.com/siderolabs/extensions/tree/main/container-runtime>
- Talos issues: #13342, #14252, #14374, #14502
- Kubernetes 1.37 release —
  <https://kubernetes.io/blog/2026/08/26/kubernetes-v1-37-release/>;
  changelog
  <https://github.com/kubernetes/kubernetes/blob/master/CHANGELOG/CHANGELOG-1.37.md>
- Kubernetes volumes —
  <https://kubernetes.io/docs/concepts/storage/volumes/>
- Cilium compatibility and kube-proxy-free (socket LB) —
  <https://docs.cilium.io/en/latest/network/kubernetes/compatibility/>,
  <https://docs.cilium.io/en/latest/network/kubernetes/kubeproxy-free/>;
  cilium#46680
- democratic-csi — <https://github.com/democratic-csi/democratic-csi>,
  <https://github.com/democratic-csi/charts>
- KEDA Temporal scaler — <https://keda.sh/docs/2.21/scalers/temporal/>;
  keda#7368
- metrics-server — <https://github.com/kubernetes-sigs/metrics-server>
- gVisor — <https://gvisor.dev/docs/user_guide/production/>,
  <https://gvisor.dev/docs/user_guide/compatibility/>,
  <https://gvisor.dev/docs/architecture_guide/performance/>,
  <https://gvisor.dev/docs/tutorials/docker-in-gvisor/>
- Kata — <https://github.com/kata-containers/kata-containers/blob/main/docs/Limitations.md>,
  <https://github.com/kata-containers/kata-containers/blob/main/docs/how-to/how-to-run-docker-with-kata.md>
- GKE Sandbox —
  <https://cloud.google.com/kubernetes-engine/docs/concepts/sandbox-pods>
- ARC / GitHub Actions hardening —
  <https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/deploying-runner-scale-sets-with-actions-runner-controller>,
  <https://docs.github.com/en/actions/security-for-github-actions/security-guides/security-hardening-for-github-actions>
- Buildkite agent-stack-k8s security —
  <https://buildkite.com/docs/agent/self-hosted/agent-stack-k8s/securing-the-stack>
- Proxmox — <https://pve.proxmox.com/wiki/Nested_Virtualization>,
  <https://pve.proxmox.com/wiki/Roadmap>
