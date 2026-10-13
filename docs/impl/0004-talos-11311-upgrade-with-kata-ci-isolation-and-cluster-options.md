---
id: IMPL-0004
title: "Talos 1.13.11 upgrade with Kata CI isolation and cluster options"
status: Draft
author: Donald Gifford
created: 2026-10-11
---

<!-- markdownlint-disable-file MD024 MD025 MD036 MD041 MD046 -->

# IMPL-0004: Talos 1.13.11 upgrade with Kata CI isolation and cluster options

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Phase map](#phase-map)
- [The record table](#the-record-table)
- [Implementation Phases](#implementation-phases)
  - [Phase 0: Stage 1 — record fix, Talos 1.13.11, Kata in the worker image](#phase-0-stage-1--record-fix-talos-11311-kata-in-the-worker-image)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 1: Kubernetes 1.36.5 (operator)](#phase-1-kubernetes-1365-operator)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 2: Stage 2 design gate](#phase-2-stage-2-design-gate)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 3: talos apply stage (code)](#phase-3-talos-apply-stage-code)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 4: Node labels and taints (code + operator)](#phase-4-node-labels-and-taints-code--operator)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 5: Kata runtime label, Cilium rule, CI onto kata-ci](#phase-5-kata-runtime-label-cilium-rule-ci-onto-kata-ci)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 6: Talos-side options](#phase-6-talos-side-options)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 7: Cluster add-on options](#phase-7-cluster-add-on-options)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
  - [Phase 8: Close](#phase-8-close)
    - [Tasks](#tasks-8)
    - [Success Criteria](#success-criteria-8)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Open Questions](#open-questions)
- [Dependencies](#dependencies)
- [References](#references)
<!--toc:end-->

## Objective

Move the live cluster to Talos v1.13.11 with Kata Containers baked into
every worker image, and prove Kata on these nested-virtualisation hosts
with a hand-applied test (Stage 1). Then build the Stage 2 surfaces on
what that proof shows:

- a day-2 `talos apply` stage;
- node labels and registration taints;
- a derived Kata runtime label;
- the Cilium validation rule;
- CI jobs moved onto `kata-ci`;
- opt-in Talos options and cluster add-ons.

runc stays the default runtime throughout.

**Implements:** DESIGN-0005 (Draft; Stage 1 decisions recorded
2026-10-11), from INV-0004.

## Scope

### In Scope

- **Stage 1** (Phase 0):
  - the `setInterfaces` / IMPL-0003 record fix, first and before
    anything else;
  - machinery v1.13.11 and the version references;
  - the operator's HCL edits (version, `kata` profile on workers);
  - the rolling re-image to 1.13.11;
  - the hand-applied Kata test kit, with its findings recorded here.
- **Between the stages** (Phase 1): Kubernetes 1.36.3 → 1.36.5 and the
  HCL pin.
- **Stage 2** (Phases 2–7): the design gate, then the DESIGN-0005
  Phases 2–6 surfaces. Stage 2 task lists are **provisional**. Phase 2
  re-cuts them against Stage 1's findings before any is started.
- The GitOps-side changes this work depends on, tracked here as
  operator tasks: Cilium values, RuntimeClasses, CI pod specs, and the
  receivers and scrapes for the options. They are carried out in the
  private homelab repo; no site detail is recorded here.

### Out of Scope

- gVisor, crun, NVIDIA (DESIGN-0005 non-goals; deferred, not
  installed).
- Talos 1.14, the multi-document migration, and `sandboxd`
  (DESIGN-0006).
- Kubernetes 1.37 (gated on Cilium 1.21 GA — DESIGN-0006).
- Changing the default runtime (it stays runc), and PVE HA or
  load-balancer automation.

## Phase map

| IMPL phase | DESIGN-0005 phase | Stage | Mostly |
| ---------- | ----------------- | ----- | ------ |
| 0 | 0 + 1 | 1 | record fix (code), then operator |
| 1 | — (between stages) | — | operator |
| 2 | — (design gate) | — | docs |
| 3 | 2 (apply stage) | 2 | code |
| 4 | 3 (labels, taints) | 2 | code + operator |
| 5 | 4 (Kata runtime) | 2 | code + GitOps |
| 6 | 5 (Talos options) | 2 | code + operator + GitOps |
| 7 | 6 (add-ons) | 2 | code + operator |
| 8 | — | — | docs |

## The record table

Filled in as the work lands. These are the authoritative values for
this IMPL.

| Fact | Value |
| ---- | ----- |
| Control-plane schematic (`base` + `iscsi`) | `88d1f7a5c4f1d3aba7df787c448c1d3d008ed29cfb34af53fa0df4336a56040b` (unchanged: schematic IDs depend on extensions, not version) |
| Worker schematic (`base` + `iscsi` + `kata`) | `95025911f4700432cab85474232840e2e25477bf0c9e0db8f24bd8c396cc8a56` (iscsi-tools, kata-containers, qemu-guest-agent, util-linux-tools; resolved 2026-10-11) |
| Kata extension version | *(from `talosctl get extensions`; expected 3.32.0)* |
| Talos | v1.13.8 → *v1.13.11* |
| Kubernetes | v1.36.3 → *v1.36.5* |
| CI workers | `work04`, `work05` (OQ-4) |
| Label and taint key | `hoomlab.io/workload=ci` (DESIGN-0005 OQ-6) |
| bootstrap release with the apply stage | *(Phase 3)* |

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Operator tasks
are run by hand by the operator; code tasks land as PRs labelled
`dont-release` unless the phase says otherwise.

---

### Phase 0: Stage 1 — record fix, Talos 1.13.11, Kata in the worker image

One chore branch (OQ-1). The record fix is the **first** commit,
before any upgrade or config work (DESIGN-0005 OQ-1). Everything after
it is HCL edits and operator steps: the current v0.3.2 binary already
renders 1.13.11 configs, because the contract derives from
`talos.version`.

#### Tasks

**0.1 Record fix (code, first)**

- [x] Cut the chore branch (OQ-1), e.g. `chore/talos-1.13.11-kata`.
      *(cut from `main` at the PR #16 merge, 2026-10-11)*
- [x] `tools/bootstrap/internal/talos/machineconfig.go`: rewrite the
      `setInterfaces` doc comment and the `nolint:staticcheck`
      reason. Drop the false "machinery v1.13 ships no multi-doc
      network device type". State the real reasons:
      - the legacy section is the live fleet's exact shape;
      - fields and their documents are mutually exclusive, so a switch
        is a whole-config replacement;
      - the migration is DESIGN-0006's.
- [x] `docs/impl/0003-talos-storage-plane.md` Phase 2 note: append a
      dated correction (2026-10-11) with the same facts and a link to
      INV-0004 F5. Amend the record; don't rewrite it.
- [x] `just bootstrap-lint` and `just bootstrap-test` pass.
- [x] Commit:
      `docs(bootstrap): correct the setInterfaces legacy-network rationale`.
      *(`c6be73f`, first on the branch)*

**0.2 CLI hygiene (code)**

- [x] `go get github.com/siderolabs/talos/pkg/machinery@v1.13.11` in
      `tools/bootstrap`, then `go mod tidy`. Keep the local `go.work` in
      step; it is untracked.
      *(Transitive updates rode along: testify 1.12.1, cosi runtime
      1.16.3, grpc-gateway 2.30.0, zap 1.28.0. The local `go.work`
      needed no change.)*
- [x] `internal/emit/emit_test.go`: `talosVersion = "v1.13.11"`.
      Regenerate the goldens with `go test ./internal/emit -update` and
      review the diff: only version strings and boot-asset URLs.
      *(Confirmed: only the catalog `talos_version` default and the
      `install_image` tags moved. The machineconfig templates are
      byte-identical under machinery v1.13.11.)*
- [x] `examples/bootstrap.hcl`: set `version = "v1.13.11"`. Add a
      commented `profile "kata"` example with the worker-only note.
- [x] `tools/bootstrap/README.md` and `docs/runbook/bootstrap-cluster.md`:
      update the version references.
      *(The README has none. The runbook's one boot-asset `curl`
      example moved to v1.13.11. Unit-test fixtures outside emit keep
      v1.13.8: they test logic, not the pinned release.)*
- [x] Add a load test asserting that example-style node names pass
      1.13.11's stricter hostname validation (machinery
      `Validate`, metal mode).
      *(Deviation: no new test. The existing
      `TestRoleTemplatesMultiNICRoundTrip` already renders a real
      hostname (`cp-01`) into each role template and fails on any
      machinery `Validate` warning in metal mode. Under v1.13.11 that
      is exactly the stricter check, `ValidateDNSNameChars`, and it
      passes.)*
- [x] `just bootstrap-lint`, `just bootstrap-test`, and govulncheck are
      green. Push and open the PR **only when the operator asks**
      (OQ-2).
      *(lint 0 issues and all packages ok. govulncheck: 0 called
      vulnerabilities. Two uncalled module findings predate this bump:
      cel-go GO-2026-6094, fixed in v0.30.0, and x/crypto GO-2026-5932,
      no fix yet.)*

**0.3 HCL and emit (operator)**

- [x] Back up `~/drill/bootstrap.hcl` alongside the existing
      `bootstrap.hcl.v0.2.0` copy. *(`bootstrap.hcl.pre-1.13.11`)*
- [x] Set `talos.version = "v1.13.11"`.
- [x] Add `profile "kata" { extensions = ["siderolabs/kata-containers"] }`.
      Append `"kata"` to the `profiles` of the **five workers only**;
      control planes stay `["base", "iscsi"]`.
- [x] `bootstrap validate` is clean.
      *(The secrets are `env()` references, so every stage, even
      talos-only ones, needs them exported. The working form is
      `op run --env-file ~/code/hoomlab/.env -- bootstrap … --config
      ~/drill/bootstrap.hcl`.)*
- [x] `bootstrap talos emit --dry-run`: expect template and catalog
      changes, a second schematic (workers) with new boot assets, the
      control-plane schematic unchanged at the new version, and
      nothing else.
      *(It resolved `88d1f7a5…` for the control planes and `95025911…`
      for the workers. Both `emit-artifacts` and `boot-assets` were
      pending.)*
- [x] `bootstrap talos emit`, then `bootstrap talos ipxe`. Record the
      worker schematic ID in the record table.
- [x] Sync the booty tree to the booty host and restart booty
      (the sync-and-restart update contract from IMPL-0003 Phase 1).
- [x] Verify that `/ipxe?mac=<worker MAC>` renders the worker
      schematic at v1.13.11, and that a control-plane MAC renders
      `88d1f7a5…` at v1.13.11.
      *(Verified 2026-10-11 on work01 and ctrl01: HTTP 200, and work01
      matches profile `talos-worker`, still one worker class since all
      five workers share the schematic. An empty repeat earlier was an
      interrupted curl, not booty.)*

**0.4 Rolling re-image (operator)**

- [x] Pre-flight:
      - `talosctl health` is green;
      - etcd shows 3/3 members;
      - every democratic-csi node pod is Ready;
      - take an etcd snapshot (OQ-5): `talosctl etcd snapshot` to
        `~/drill/backups/`.
- [x] Workers first, one at a time (OQ-3):

      ```sh
      talosctl -n <worker-ip> upgrade \
        --image factory.talos.dev/installer/<worker-schematic>:v1.13.11
      ```

      Then on each worker, before moving to the next:
      - `talosctl -n <ip> version` shows v1.13.11;
      - `talosctl -n <ip> get extensions` lists `kata-containers`;
      - `talosctl health` is green;
      - the node's democratic-csi pod is Ready and its PVC workloads
        are Running;
      - the cilium#48420 check:
        `bpftool cgroup show /sys/fs/cgroup effective` from a
        `kube-system` host-netns debug pod, plus a ClusterIP curl
        from host network.

      *(2026-10-11, finding — drain blockers: every CNPG cluster is
      `instances: 1` (primary PDB `minAvailable: 1`) and OpenBao is one
      replica (PDB `maxUnavailable: 0`), so no eviction ever succeeds
      and the upgrade's drain loops. In 1.13 the installer writes the
      new UKI first and the client drains and reboots after, so a loop
      that is abandoned leaves the new version staged for the next
      boot. The working per-node procedure is to empty the node before
      the upgrade: `kubectl cordon`, then `kubectl delete pod` each
      `*-db-1` / `*-postgres-1` / `openbao-0` on it (never `--force`;
      data stays on the iSCSI PVCs and the pods reattach elsewhere),
      wait until only DaemonSets remain, then upgrade. CNPG pods take
      minutes to go: a 1800s grace period and a smart shutdown that
      waits on client connections. OpenBao auto-unseals after the move.
      Follow-ups, outside this IMPL: no CNPG cluster has backups
      configured, and iSCSI-backed ext4 volumes log `Aborting journal`
      on detach, on work01 since 2026-09-27.)*

      *(2026-10-11, work01 done: v1.13.11, kernel 6.18.54, containerd
      2.2.9; extensions kata-containers 3.32.0, iscsi-tools,
      qemu-guest-agent, util-linux-tools under schematic `95025911…`;
      `/dev/kvm`, `vhost-net`, `vhost-vsock` present; every pod in the
      cluster Running; OpenBao unsealed; democratic-csi node pods Ready.
      cilium#48420 check passes: all 11 socket-LB
      hooks (`cil_sock4/6_connect`, `sendmsg`, `recvmsg`, `getpeername`,
      `post_bind`, `cil_sock_release`) attached at the root cgroup, and
      a host-network curl to the `kubernetes` ClusterIP got the API
      server's 401.)*

      *(2026-10-11, workers done: work01–05 all on v1.13.11 (kernel
      6.18.54, containerd 2.2.9), each labelled
      `extensions.talos.dev/kata-containers=3.32.0`, each with 11
      socket-LB hooks at the root cgroup. work02–05 were upgraded after
      the drain blockers were fixed at the workloads (INV-0005 Observation 7): every CNPG cluster now
      has `enablePDB: false` and `smartShutdownTimeout: 15`, and
      OpenBao has no PDB, so plain drains succeed. After the roll,
      `ts-atlantis` (tailscale) crash-loops: its operator-generated
      `ts-atlantis-egress` CiliumNetworkPolicy allows only atlantis,
      DNS and `world`, not the `kube-apiserver` entity, so it can't
      reach 10.96.0.1. It's a policy gap the restart exposed, not an
      upgrade regression: `ts-repo-guardian` on the same node has no
      policy and runs.)*
- [x] Control planes, one at a time:

      ```sh
      talosctl -n <cp-ip> upgrade \
        --image factory.talos.dev/installer/88d1f7a5c4f1d3aba7df787c448c1d3d008ed29cfb34af53fa0df4336a56040b:v1.13.11
      ```

      Between each: etcd 3/3 healthy, `talosctl health` green, the
      cilium#48420 check.

      *(2026-10-13, done with `~/drill/upgrade-node.sh` in the order
      ctrl02, ctrl03, ctrl01. ctrl01 went last because both the
      kubeconfig and the talosconfig point at it alone, and its run
      used `TALOS_ENDPOINT` set to ctrl02. Each node: etcd snapshot
      first (`~/drill/backups/etcd-pre-v1.13.11-<node>-<ts>.db`, on top
      of the fleet-wide `etcd-pre-1.13.11.db`), drained one ReplicaSet
      pod, upgraded, back Ready on v1.13.11, etcd 3/3, 11 socket-LB
      hooks, host-network ClusterIP 401. Leadership moved ctrl03 →
      ctrl02. All 8 nodes now report v1.13.11, kernel 6.18.54.)*
- [x] Verify the stored `install.image` on all 8 nodes by **field
      extraction only**, never a full config dump. Workers carry the
      worker schematic and control planes `88d1f7a5…`, both at
      v1.13.11.

      *(2026-10-13, finding: `talosctl upgrade --image` installs the
      image but does not write it back to the stored config. All 8
      nodes still carry `install.image` =
      `…/88d1f7a5…:v1.13.8`, workers included, since before this IMPL
      every node shared one schematic. The running OS is correct, and
      re-PXE gets the right per-class image from booty's emitted
      configs. The stale field only matters when Talos reinstalls from
      the stored config. Fix by a no-reboot patch of
      `/machine/install/image` per node; Phase 3's `talos apply` would
      also converge it, and its schematic check would flag it.)*

      *(2026-10-13, fixed: a no-reboot strategic-merge patch,
      `{"machine":{"install":{"image":"<class image>"}}}`, on each
      node. A dry-run diff showed exactly the one `image:` line. Field
      extraction now shows `88d1f7a5…:v1.13.11` on ctrl01–03 and
      `95025911…:v1.13.11` on work01–05. The live configs are
      multi-document (three documents per node), so talosctl refuses
      JSON6902 patches ("not supported for multi-document machine
      configuration"). Day-2 hand patches must be strategic-merge, and
      Phase 3's renderer and apply must treat the live config as
      multi-document.)*
- [ ] Closing loop: `validate`, `talos emit --dry-run`,
      `talos ipxe --dry-run`, and `talos vms --dry-run` all report 0
      to apply.

**0.5 Kata test kit (operator; hand-applied, not GitOps)**

- [ ] Label and taint the first CI worker, `work04` (OQ-4), with the final keys:

      ```sh
      kubectl label node work04 hoomlab.io/workload=ci
      kubectl taint node work04 hoomlab.io/workload=ci:NoSchedule
      ```

- [ ] Apply a test RuntimeClass `kata`:
      - `handler: kata`;
      - `overhead.podFixed` 130Mi / 250m;
      - `scheduling.nodeSelector`
        `extensions.talos.dev/kata-containers: "<version>"`, the exact
        value from the record table. This is a test object only;
        Phase 5 replaces it.
- [ ] **T1 kernel boundary:** a `runtimeClassName: kata` pod's
      `uname -r` differs from the host's.
- [ ] **T2 networking:** from the Kata pod, `nslookup
      kubernetes.default` works, and an HTTP request to a ClusterIP
      Service and to a pod IP on another node both work.
- [ ] **T3 storage:** a pod with a democratic-csi PVC (`iscsi-ssd-*`
      class) under `kata` writes, reads, and survives a pod delete and
      recreate. Note the mount path (virtio-fs or block).
- [ ] **T4 privileged DinD:** a privileged `docker:dind` pod under
      `kata`, with `/var/lib/docker` on `emptyDir` `medium: Memory`,
      runs `docker build` of a small Dockerfile. Repeat with a
      loop-mounted ext4 image if `emptyDir` fails.
- [ ] **T5 BuildKit:** `buildkitd`, privileged or rootless (record
      which), under `kata` builds the same image.
- [ ] **T6 placement:** a pod tolerating the taint with
      `runtimeClassName: kata` runs on the CI worker. A plain pod
      without a toleration never lands there. A `kata` pod on a plain
      worker also runs, since every worker has Kata.
- [ ] **T7 cost:** per Kata pod, record the host RSS of
      `cloud-hypervisor` plus `virtiofsd` (from `talosctl -n <ip>
      processes`), the node's `MemAvailable` delta, and the cold-start
      time to Running. Compare with runc.
- [ ] **T8 reboot:** reboot the CI worker. Kata pods come back, and the
      cilium#48420 check passes.
- [ ] Write the findings into the [record table](#the-record-table)
      and a Phase 0 findings note under this phase. Clean up the test
      pods. Keep the test RuntimeClass until Phase 5 (OQ-6).

#### Success Criteria

- All 8 nodes are on v1.13.11, workers on the new worker schematic
  with `kata-containers` and control planes on `88d1f7a5…`.
  `talosctl health` is green and etcd is 3/3.
- The closing loop applies 0.
- The record-fix commit is first on the branch; lint and test are
  green.
- T1–T8 have recorded results, pass or fail with notes. Any failure
  is captured as a finding for Phase 2, not papered over.
- No GitOps change has been made for Kata yet.

---

### Phase 1: Kubernetes 1.36.5 (operator)

After Phase 0 is complete (DESIGN-0005 OQ-2).

#### Tasks

- [ ] `talosctl -n <cp-ip> upgrade-k8s --to 1.36.5 --dry-run`, and
      review it.
- [ ] `talosctl -n <cp-ip> upgrade-k8s --to 1.36.5`.
- [ ] Verify:
      - `kubectl get nodes` shows v1.36.5 everywhere;
      - `talosctl health` is green;
      - CI and CSI workloads are healthy.
- [ ] Set `kubernetes_version = "1.36.5"` in `~/drill/bootstrap.hcl`.
      Run `talos emit --dry-run` (kubelet and control-plane image refs
      change), then `talos emit`, and sync and restart booty.
- [ ] Update the example's commented `kubernetes_version` hint to
      1.36.5 (code, on the Phase 0 branch if it is still open,
      otherwise the next PR).

#### Success Criteria

- Every node reports v1.36.5, and health is green.
- The emitted templates pin 1.36.5, and `talos emit --dry-run` reports
  0 afterwards.

---

### Phase 2: Stage 2 design gate

Nothing in Stage 2 starts until this phase closes.

#### Tasks

- [ ] Amend DESIGN-0005 with the Phase 0 findings: sizing, the
      storage path, the DinD/BuildKit layout, networking, and anything
      contradicted.
- [ ] Re-answer the remaining DESIGN-0005 open questions (OQ-4, 5, 7,
      8, 9, 12–19, 21) in light of the findings.
- [ ] Re-cut Phases 3–7 of this IMPL: adjust, reorder, or drop tasks.
      Resolve this document's Stage 2 open questions (OQ-7 to OQ-12).
- [ ] Move DESIGN-0005 to **Approved**.

#### Success Criteria

- DESIGN-0005 is Approved, with no open question that blocks the next
  phase.
- Phases 3–7 here reflect the approved design.

---

### Phase 3: talos apply stage (code)

*Provisional — re-cut in Phase 2.* DESIGN-0005 Phase 2. Every later
live change depends on it.

#### Tasks

- [ ] `internal/talos/client.go`: extend `Client`, then regenerate the
      mockery mocks. New calls:
      - read a node's live machine config (the `MachineConfig`
        resource);
      - `ApplyConfiguration` with mode `NO_REBOOT`;
      - list a node's extensions.
- [ ] Node addressing (OQ-7). Resolve each config node to a Talos API
      address. A node the CLI can't resolve is an error naming it.
- [ ] Renderer: a per-node config is the class template plus the
      node's vars, rendered with the same `text/template` and
      `.Vars` contract booty uses. Share the var assembly with
      `internal/emit/catalog.go`, so emit and apply can't diverge.
- [ ] Normalisation: decode live and rendered configs through
      machinery's config loader and re-encode, then compare. Print a
      unified diff of the normalised YAML in `--dry-run`, with secrets
      redacted.
- [ ] One `Step` per node, `apply-<node>`:
      - `Check` reports equal or differs;
      - `Apply` sends `NO_REBOOT`;
      - a reboot-required refusal becomes an error naming the node and
        the change.
- [ ] Schematic check: a mismatch between the rendered `install.image`
      and the running extensions is reported with the exact
      `talosctl upgrade --image` command. It is never applied.
- [ ] `cmd/talos.go`: add `bootstrap talos apply [--dry-run] [--node
      NAME]`. It is not part of any default chain, and it reads
      `out/talosconfig`.
- [ ] Tests:
      - table tests with the mock client for equal, differs, apply,
        refusal, unresolvable node, and schematic mismatch;
      - a render round-trip that machinery `Validate` accepts.
- [ ] Docs: a README stage section, a runbook section ("day-2 apply"),
      and an example invocation.
- [ ] Release (OQ-9): the operator runs `tools-release.yml` once the PR
      merges.
- [ ] **Live (operator):** `bootstrap talos apply --dry-run` across all
      8 nodes.

#### Success Criteria

- `--dry-run` against the live fleet reports **0 to apply** on all 8
  nodes. That proves render equals live; any diff is investigated
  before anything is applied.
- `just bootstrap-lint` and `just bootstrap-test` are green, and the
  new code is covered by mock-client tests.
- A tools release containing the stage exists, and the operator's
  binary reports it.

---

### Phase 4: Node labels and taints (code + operator)

*Provisional — re-cut in Phase 2.* DESIGN-0005 Phase 3.

#### Tasks

- [ ] `internal/config/config.go` — add to `TalosNode`:
      - `Labels map[string]string`;
      - `Taints []Taint` (labeled `taint` blocks: `value`, `effect`);
      - `WorkloadClass string`.

      Hold the `hoomlab.io/` prefix in one constant.
- [ ] `internal/config/resolve.go`: resolve the explicit labels and
      taints plus the `workload_class` sugar into the resolved form.
      The sugar and an explicit key together are an XOR error.
- [ ] `internal/config/validate.go`:
      - qualified-name syntax;
      - reject the NodeRestriction-restricted prefixes
        (`node-role.kubernetes.io/`,
        `node-restriction.kubernetes.io/`, and other `kubernetes.io/`
        or `k8s.io/` prefixes off the allow-list), with the error
        naming NodeRestriction;
      - completion-label collisions;
      - the effect enum;
      - duplicate `key:effect` pairs.
- [ ] `internal/talos/completion.go`:
      - labels go to `machine.nodeLabels`, merged with the completion
        labels;
      - taints go to `machine.kubelet.extraConfig.registerWithTaints`,
        never `machine.nodeTaints`.
- [ ] `internal/talos/machineconfig.go` and `internal/emit/catalog.go`:
      key templates by (role, scheduling set). Class names are derived
      only when a role splits; single-class roles keep today's names.
- [ ] Apply stage: report taint drift between declared and live Node
      taints, and print the exact `kubectl taint` command (OQ-8).
- [ ] Tests:
      - load tests per rule, with positive controls;
      - goldens for a split worker role;
      - **byte-identity**: a config without the new attributes emits
        exactly today's tree;
      - `Validate` passes on every class template.
- [ ] Docs: the example HCL and the README.
- [ ] Release (OQ-9).
- [ ] **Live (operator):**
      - set `workload_class = "ci"` on both CI workers (OQ-4);
      - run `talos emit` and sync booty;
      - run `talos apply --node <each CI worker>`;
      - run the printed `kubectl taint` for the second worker (the
        first matches from Phase 0).

#### Success Criteria

- Both CI workers carry `hoomlab.io/workload=ci` and the `NoSchedule`
  taint. `talos apply --dry-run` reports 0, with no taint drift, on all
  8 nodes.
- Configs without the new attributes are byte-identical.

---

### Phase 5: Kata runtime label, Cilium rule, CI onto kata-ci

*Provisional — re-cut in Phase 2.* DESIGN-0005 Phase 4.

#### Tasks

- [ ] Runtime table, e.g. in `internal/config`: a `kata` row (extension
      `siderolabs/kata-containers`, handler `kata`, worker-only).
- [ ] Resolver: a node whose flattened extensions include a runtime's
      extension derives the label `runtime.hoomlab.io/kata=true`. An
      explicit label on that key is an XOR error.
- [ ] Validation: the Kata extension on a control-plane profile is an
      error.
- [ ] `internal/config/ciliumvalues.go`: if any node carries Kata,
      require explicit `socketLB.hostNamespaceOnly: true`, and make
      `bandwidthManager.enabled: true` an error.
- [ ] Tests: the derived label, the XOR, Kata on a control plane, both
      Cilium checks, and positive controls. Goldens carry the label in
      the worker classes.
- [ ] Docs: the example HCL `kata` profile is live, not commented, and
      the README covers it.
- [ ] Release (OQ-9).
- [ ] **Operator / GitOps:**
      - [ ] Add `socketLB.hostNamespaceOnly: true` explicitly to
            `~/drill/cilium-values.yaml` and the GitOps Cilium values.
            It is already in effect, so expect a no-op agent restart.
            Run the cilium#48420 check after.
      - [ ] Run `talos emit`, sync booty, then `talos apply` on all
            workers so the derived label goes live. `--dry-run` reports
            0 after.
      - [ ] GitOps: add the `kata` and `kata-ci` RuntimeClasses
            (DESIGN-0005 contract) and delete the Phase 0 test class.
      - [ ] Re-run T1–T6 against `kata-ci`.
      - [ ] Move one ARC runner scale set to `runtimeClassName:
            kata-ci`, with `/var/lib/docker` per the Phase 0 finding.
            Watch it for a few days.
      - [ ] Move the remaining ARC scale sets, then the Buildkite job
            pods (with `prohibit-kubernetes-plugin` on), then BuildKit.

#### Success Criteria

- Every CI pod (ARC, Buildkite, BuildKit) runs with `runtimeClassName:
  kata-ci` on the two CI workers.
- One week of normal CI runs green on Kata.
- Non-CI workloads run under runc and never on the CI workers.
- Validation rejects a values file missing `hostNamespaceOnly` while
  Kata is present.

---

### Phase 6: Talos-side options

*Provisional — re-cut in Phase 2.* DESIGN-0005 Phase 5. Each option is
its own PR, and each is enabled live on one node before the fleet, in
OQ-10 order.

#### Tasks

**6.1 etcd metrics**

- [ ] Add `etcd_metrics { listen_url }` to `talos.cluster`. It renders
      `cluster.etcd.extraArgs.listen-metrics-urls` into the
      control-plane template only. Validate a URL with scheme `http`
      and an explicit port.
- [ ] Tests, docs, and a release.
- [ ] **Live:** `talos apply` on one control plane; reboot it if the
      apply is refused. Check `curl <cp>:2381/metrics`, then roll to
      the other two.
- [ ] **GitOps:** kube-prometheus-stack `kubeEtcd` endpoints and port
      2381, with the target UP.

**6.2 Logging**

- [ ] Add `logging { destination { endpoint, format } kernel "<name>"
      { url } }`. It renders `machine.logging.destinations` and the
      `KmsgLogConfig` documents. `format` is required (`json_lines`);
      the endpoint must be `tcp://` or `udp://`.
- [ ] Tests, docs, and a release.
- [ ] **GitOps first:** the receiver, as DESIGN-0005 OQ-15 decides.
- [ ] **Live:** one worker, check lines arrive in Loki; then the fleet.

**6.3 Registry mirror**

- [ ] Add `registry_mirror "<registry>" { endpoints, skip_fallback,
      override_path }`. It renders `RegistryMirrorConfig` documents,
      never `machine.registries`. `skip_fallback` is required.
- [ ] Tests, docs, and a release.
- [ ] **Operator first:** stand up the mirror, as DESIGN-0005 OQ-16
      decides.
- [ ] **Live:** one worker; pull a fresh image and confirm it was
      served from the mirror (mirror logs); then the fleet.

**6.4 Image verification**

- [ ] Add `image_verification { rule { image, keyless{…} | public_key{…}
      | skip | deny } }`. It renders `ImageVerificationConfig`.
      Validation:
      - exactly one action per rule;
      - keyless takes an issuer plus `subject` XOR `subject_regex`;
      - the `public_key.certificate` path is resolved and read at load;
      - **the terminal rule must be `image = "*"`**.
- [ ] Tests, docs, and a release.
- [ ] **Live:** apply the initial policy (DESIGN-0005 OQ-17) on one
      worker. Check `talosctl image ls` shows `talos.dev/verified` on
      siderolabs and factory images, and that workloads still pull.
      Then the fleet, with control planes last, one at a time
      (talos#13342).

#### Success Criteria

- Each option, once enabled, is verified live (metrics scraped, logs
  received, pulls served by the mirror, images verified), and
  `talos apply --dry-run` reports 0 afterwards.
- With no option declared, emit output is byte-identical to before.

---

### Phase 7: Cluster add-on options

*Provisional — re-cut in Phase 2. Gated on DESIGN-0005 OQ-18.* If that
resolves to "keep in GitOps", this phase reduces to the GitOps tasks.

#### Tasks

- [ ] Kubernetes access (OQ-11): add the client dependency and a
      kubeconfig loader from `out/kubeconfig`.
- [ ] `addon "<name>" { version }`, a closed set mapping names to
      pinned upstream manifest URLs: `metrics-server` (components.yaml)
      and `keda` (release YAML). An unknown name or a bad version is a
      load error.
- [ ] `bootstrap cluster addons [--dry-run]`:
      - one `Step` per add-on;
      - `Check` compares the Deployment image tag with the pinned
        version;
      - `Apply` server-side-applies with field manager
        `hoomlab-bootstrap`;
      - not in the default chain.
- [ ] Tests (fake client), docs (including "don't declare an add-on
      GitOps owns"), and a release.
- [ ] **Live / GitOps:**
      - metrics-server: `kubectl top nodes` works, with no
        `--kubelet-insecure-tls`;
      - KEDA: install, then a `ScaledObject` for one Temporal worker
        Deployment using the `temporal` trigger (DESIGN-0005 pattern,
        `minReplicaCount: 1`) in GitOps;
      - verify scale-out under a synthetic task backlog and scale-in
        after.

#### Success Criteria

- metrics-server serves `metrics.k8s.io`, and KEDA scales one Temporal
  worker Deployment on queue backlog, end to end.
- Re-running `cluster addons --dry-run` reports 0.

---

### Phase 8: Close

#### Tasks

- [ ] README stage list, runbook sections, and example HCL are all
      current.
- [ ] DESIGN-0005 → **Implemented**, with an as-built note on any
      deviations.
- [ ] Point DESIGN-0006's DESIGN-0005 surfaces table at the as-built
      forms.
- [ ] This IMPL → **Completed**.

#### Success Criteria

- The docs match the shipped behaviour. DESIGN-0005 is Implemented and
  IMPL-0004 is Completed.

## File Changes

| File | Action | Description | Phase |
| ---- | ------ | ----------- | ----- |
| `tools/bootstrap/internal/talos/machineconfig.go` | Modify | `setInterfaces` rationale; per-class templates | 0, 4 |
| `docs/impl/0003-talos-storage-plane.md` | Modify | dated correction note | 0 |
| `tools/bootstrap/go.mod`, `go.sum` | Modify | machinery v1.13.11 (+ k8s client in Phase 7) | 0, 7 |
| `tools/bootstrap/internal/emit/emit_test.go` + `testdata/**` | Modify | version bump; class goldens | 0, 4, 5 |
| `tools/bootstrap/examples/bootstrap.hcl` | Modify | version, `kata` profile, new surfaces | 0, 4–7 |
| `tools/bootstrap/README.md`, `docs/runbook/bootstrap-cluster.md` | Modify | versions, apply stage, add-ons | 0, 3–8 |
| `tools/bootstrap/internal/talos/client.go` (+ mocks) | Modify | machine config read, apply, extensions | 3 |
| `tools/bootstrap/internal/talos/apply.go` | Create | the apply stage: render, normalise, diff, steps | 3 |
| `tools/bootstrap/cmd/talos.go` | Modify | `talos apply` | 3 |
| `tools/bootstrap/internal/emit/catalog.go` | Modify | shared var assembly; class keys | 3, 4 |
| `tools/bootstrap/internal/config/config.go` | Modify | labels, taints, `workload_class`, options, add-ons | 4, 6, 7 |
| `tools/bootstrap/internal/config/resolve.go` | Modify | scheduling resolution; derived runtime label | 4, 5 |
| `tools/bootstrap/internal/config/validate.go` | Modify | label, taint, runtime, and option rules | 4–7 |
| `tools/bootstrap/internal/config/runtime.go` | Create | runtime table | 5 |
| `tools/bootstrap/internal/config/ciliumvalues.go` | Modify | `hostNamespaceOnly` / bandwidth-manager checks | 5 |
| `tools/bootstrap/internal/talos/completion.go` | Modify | labels, `registerWithTaints`, option rendering | 4–6 |
| `tools/bootstrap/internal/cluster/addons.go` | Create | add-on stage | 7 |
| `tools/bootstrap/cmd/cluster.go` | Create | `cluster addons` | 7 |

## Testing Plan

- [ ] Load tests per new validation rule, one failing input each, each
      with a positive control (Phases 4–7).
- [ ] **Byte-identity:** the existing goldens stay unchanged by every
      Stage 2 phase unless the fixture opts in. This is the
      back-compat contract as a test.
- [ ] Machinery `Validate` (metal mode) passes on every emitted class
      template and every apply-rendered config.
- [ ] Mock-client tests for the apply stage, and fake-client tests
      for the add-on stage.
- [ ] Live proofs (Phase 0 T1–T8, Phase 3 zero-drift, the Phase 5 CI
      week, the Phase 6 and 7 verifications) stay out of CI and are
      recorded here. IMPL-0001 decided the e2e drill is not a merge
      gate.

## Open Questions

Each question offers lettered options: **a** is the recommendation and
the others are alternatives. Answer with a letter or write your own.

**Before Phase 0:** OQ-1 to OQ-6, all resolved 2026-10-11. OQ-7 to
OQ-12 are Stage 2 and are re-asked at the Phase 2 gate.

1. **Branch and merge order.** *Resolved 2026-10-11 (a)*: merge
   `inv/0004` first, then cut the Phase 0 chore branch from `main`.
   IMPL checkboxes are updated on the branch doing the work.
2. **Phase 0 PR and release.** *Resolved 2026-10-11 (a)*: one chore PR,
   record-fix commit first and then the machinery bump, labelled
   `dont-release`. No tools release; the live upgrade doesn't need a
   new binary.
3. **Upgrade order in Phase 0.4.** *Resolved 2026-10-11 (a)*: workers
   first, then control planes one at a time.
4. **Which two workers are the CI pool.** *Resolved 2026-10-11 (a)*:
   `work04` and `work05`. The test kit uses `work04`.
5. **etcd snapshot before the re-image.** *Resolved 2026-10-11 (a)*:
   take it. It is one command (`talosctl etcd snapshot` into
   `~/drill/backups/`) before the first control plane.
6. **The Phase 0 test RuntimeClass after the test kit.** *Resolved
   2026-10-11 (a)*: keep it until Phase 5 replaces it. Nothing in GitOps
   references it.
7. **How the apply stage finds node addresses** (Stage 2). The config
   has no per-node IPs.
   - a. Talos discovery: list cluster members through the endpoint and
     match by hostname. A missing node is an error.
   - b. Kubernetes Node `InternalIP` via `out/kubeconfig`.
   - c. A new optional per-node `address` attribute: explicit, but it
     duplicates the DHCP reservation and can drift.
8. **Taint-drift detection** (Stage 2).
   - a. Read-only Node lookup through the Kubernetes client, with the
     exact `kubectl taint` printed on drift.
   - b. No live read: always print the declared taints as commands for
     the operator to compare.
   - c. Shell out to `kubectl`.
9. **Tools release cadence in Stage 2.**
   - a. A release after each code phase with a live step (3, 4, 5, and
     each 6.x and 7), so the operator always runs a tagged binary.
   - b. One release at the end of Stage 2, with the operator on locally
     built binaries until then.
10. **Order of the Phase 6 options.**
    - a. etcd metrics → logging → registry mirror → image verification:
      ascending blast radius, and image verification can block pulls.
    - b. The DESIGN-0005 order: image verification, logging, etcd
      metrics, registry mirror.
11. **Kubernetes client for Phases 4 and 7** (Stage 2).
    - a. Add `k8s.io/client-go` (typed and dynamic clients, server-side
      apply) as a direct dependency of `tools/bootstrap`.
    - b. Shell out to `kubectl`.
    - c. No Kubernetes access: print commands only, and drop the
      add-on stage.
12. **Where Stage 2 findings land** (Stage 2).
    - a. In DESIGN-0005 as amendments (Phase 2), with this IMPL
      recording only results.
    - b. In a short INV-0005 for the Kata test, with DESIGN-0005
      citing it.

## Dependencies

- INV-0004 (Concluded) and DESIGN-0005 (Draft; Stage 1 decisions
  recorded). DESIGN-0006 for everything 1.14.
- The live cluster: 3 control planes and 5 workers, `cpu = host` VMs
  with `/dev/kvm` present, Cilium 1.20.1 with Gateway API on (which
  forces `hostNamespaceOnly` today).
- The booty host serving the emitted tree (the sync-and-restart update
  contract from IMPL-0003 Phase 1).
- The private GitOps repo for Cilium values, RuntimeClasses, CI pod
  specs, receivers, scrapes, the mirror, and add-on ownership.
- `tools-release.yml` (workflow_dispatch) for any binary the operator
  needs.
- Upstream: Image Factory serving `siderolabs/kata-containers` for
  v1.13.11; cilium#48420 (watched in every reboot check); talos#13342
  (image verification and etcd); talos#13745 (Kata and bandwidth
  manager).

## References

- [DESIGN-0005: Talos 1.13.11 upgrade with Kata CI isolation and cluster options](../design/0005-talos-11311-upgrade-with-kata-ci-isolation-and-cluster-options.md)
- [DESIGN-0006: Talos 1.14 multi-document migration requirements](../design/0006-talos-114-multi-document-migration-requirements.md)
- [INV-0004: Talos 1.14 and Kubernetes 1.37 upgrade with workload isolation](../investigation/0004-talos-114-and-kubernetes-137-upgrade-with-workload-isolation.md)
- [IMPL-0003: Talos storage plane](0003-talos-storage-plane.md) — Phase 1 is the re-image pattern this repeats
- [DESIGN-0002](../design/0002-talos-cluster-completion-scope-for-bootstrap.md) and [DESIGN-0004](../design/0004-network-planes-and-interfaces-for-the-bootstrap-cli.md)
- `docs/runbook/bootstrap-cluster.md`
- Kata extension —
  <https://github.com/siderolabs/extensions/tree/v1.13.11/container-runtime/kata-containers>
- Talos v1.13.11 — <https://github.com/siderolabs/talos/releases/tag/v1.13.11>
- Upgrading Kubernetes on Talos —
  <https://docs.siderolabs.com/kubernetes-guides/advanced-guides/upgrading-kubernetes>
