---
id: INV-0005
title: "Rolling node upgrades managed by hoomlab"
status: Open
author: Donald Gifford
created: 2026-10-11
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0005: Rolling node upgrades managed by hoomlab

<!--toc:start-->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1: Talos 1.13 installs server-side and reboots client-side](#observation-1-talos-113-installs-server-side-and-reboots-client-side)
  - [Observation 2: single-instance workloads make every drain fail](#observation-2-single-instance-workloads-make-every-drain-fail)
  - [Observation 3: emptying a node is slow by design, not hung](#observation-3-emptying-a-node-is-slow-by-design-not-hung)
  - [Observation 4: "upgraded" is a per-role checklist, not a version string](#observation-4-upgraded-is-a-per-role-checklist-not-a-version-string)
  - [Observation 5: the bootstrap step model already fits a node upgrade](#observation-5-the-bootstrap-step-model-already-fits-a-node-upgrade)
  - [Observation 6: the upgrade exposed data-safety gaps the tool should gate on](#observation-6-the-upgrade-exposed-data-safety-gaps-the-tool-should-gate-on)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
- [References](#references)
<!--toc:end-->

## Question

What should own rolling Talos node upgrades in hoomlab, and what does
it have to do that `talosctl upgrade` does not? The candidates are:

- a `bootstrap talos upgrade` stage;
- the hoomlab service's reconciler;
- an existing in-cluster upgrade controller;
- staying with an operator script.

The answer should name the owner. It should also name the sequence
that owner runs per node: preconditions, eviction policy, upgrade, and
verification.

## Hypothesis

A convergent stage in the bootstrap CLI is the right first owner. It
would sit on the existing `steps.Runner`, with one step per node:

- **Check:** the node reports the target version and the schematic its
  config resolves to.
- **Apply:** the full sequence the IMPL-0004 0.4 script runs today.

The hoomlab service lifts the same steps into its reconcile loop later.
An in-cluster controller is the wrong owner: it runs on the nodes it
upgrades and depends on the API server it is disrupting. That clashes
with "recovery can't depend on itself" (INV-0003 Observation 6).
`talosctl` alone is not enough, because the hard parts of today's
upgrade were all outside it.

## Context

The IMPL-0004 0.4 rolling re-image (Talos v1.13.8 → v1.13.11 with Kata
in the worker image) is the first fleet-wide upgrade since bring-up. On
the first worker the plain `talosctl upgrade` drain looped
indefinitely. The node was finished by hand: cordon, delete the
blocking pods, wait, upgrade again. That procedure was then written up
as an operator script (`~/drill/upgrade-node.sh`) to finish the
remaining nodes. A script works for one operator on one day. It does
not:

- know the config's node classes or schematics;
- show up in a dry-run;
- survive a re-run halfway through;
- fit the converge-from-config model of ADR-0001.

The bootstrap CLI already renders the schematics the upgrade installs
(`talos emit`), and IMPL-0004 Phase 3 already extends the Talos client
for a day-2 `talos apply`. Upgrades are the other day-2 operation that
needs the same machinery.

**Triggered by:** IMPL-0004 Phase 0.4; DESIGN-0005 (Stage 1);
DESIGN-0006 (the Talos 1.14 move will need the same procedure,
at a larger scale).

## Approach

1. Record what the IMPL-0004 0.4 upgrade actually did, per node, from
   the Talos and kubelet logs. Use that as the requirements baseline
   (Observations 1–4).
2. Read the Talos 1.13 machinery API for the upgrade path. Find which
   calls are server-side (`LifecycleService.Upgrade`) and which the
   client owns (cordon, drain, reboot, wait). Find what a Go caller
   must reimplement from `talosctl`.
3. Decide the eviction policy for workloads whose PDBs never allow a
   disruption:
   - delete gracefully (today's script);
   - CNPG `nodeMaintenanceWindow`;
   - fix the workloads (more instances);
   - refuse and report.

   Consider whether the policy belongs in config.
4. Evaluate the existing tools against the hypothesis: `talosctl`
   alone, Sidero Omni, the community Talos upgrade controller (tuppr),
   and Rancher's system-upgrade-controller with a Talos plan. Record
   for each where it runs, what it needs, and how it handles the
   Observation 2 blockers.
5. Sketch the step for one node in the `steps.Step` shape. Check and
   Apply must be safe to re-run from any point of interruption,
   including the Observation 1 "installed, not rebooted" state.
6. Decide where the Kubernetes calls live. The bootstrap module has no
   Kubernetes client today. Options: add client-go with the
   `kubectl/pkg/drain` helper, or shell out like `talos ipxe` does.

## Environment

| Component | Version / Value |
| --------- | --------------- |
| Talos (from → to) | v1.13.8 → v1.13.11 |
| `talosctl` client | v1.13.8 (mise) |
| Kubernetes | v1.36.3 |
| Talos machinery (bootstrap module) | v1.13.11 |
| Cilium | v1.20.1, kube-proxy replacement, socket LB |
| CloudNativePG operator | 1.30.0; 9 clusters, all `instances: 1` |
| CNPG shutdown | `stopDelay` 1800s, `smartShutdownTimeout` 180s |
| OpenBao | 1 replica, PDB `maxUnavailable: 0`, static-key auto-unseal |
| Storage | democratic-csi iSCSI, RWO, reclaim `Delete` |
| Fleet | 3 control planes, 5 workers; two schematics (control plane, worker) |

## Findings

### Observation 1: Talos 1.13 installs server-side and reboots client-side

On work01 the machined log shows `LifecycleService/Upgrade` completing
in 1.6s. The installer probed sd-boot, removed the older UKI, wrote
`Talos-v1.13.11.efi`, updated the EFI boot entry and saved META, then
logged `installation of v1.13.11 complete`. The cordon, drain and
reboot happen afterwards, driven by the `talosctl` client.

When that client-side drain looped and was abandoned, the node was
left running v1.13.8 with v1.13.11 staged as the default boot entry.
Any later reboot — a kernel panic, a PVE host restart, or an unrelated
`talosctl reboot` — would upgrade the node unattended, with no drain.

An owner therefore has to treat "installed, not rebooted" as a real,
detectable state. Its Check cannot read only the running version. The
staged UKI is not visible through `/boot` over the API (`ls` fails),
so how to detect it is an open part of approach step 2.

### Observation 2: single-instance workloads make every drain fail

Ten PDBs never allow a disruption:

- nine CNPG `*-primary` PDBs (`minAvailable: 1` on single-instance
  clusters);
- OpenBao's PDB (`maxUnavailable: 0` on one replica).

Each uses the eviction API, which honours PDBs, so the upgrade's drain
retries forever. It is not a transient. Whichever node hosts one of
these pods cannot be drained, and the pods move between nodes as the
upgrade rolls: after work01 they landed on work03, work04 and work05.

A graceful delete bypasses the PDB without losing data. The data lives
on network PVCs. CNPG recreates the instance pod on its existing PVC,
and OpenBao's StatefulSet does the same and auto-unseals. All five
moved pods were Running and ready elsewhere within minutes. The
outage is the same as any restart of a single instance. Deleting
loses nothing a working drain would have kept.

So eviction policy is a decision the tool must make explicitly, not
something it can infer. "Delete when the PDB can never allow a
disruption and the pod has a controller" is a rule a tool can encode.
Bare pods are the exception: deleting one loses it, so they must stop
the run.

### Observation 3: emptying a node is slow by design, not hung

CNPG sets the instance pods' grace period to `stopDelay` (1800s).
Postgres first does a smart shutdown that waits up to 180s for clients
to disconnect. Temporal and repo-guardian hold connections, so their
databases took minutes to terminate. To the operator this looked like
a hang.

A tool needs:

- per-phase timeouts sized to this (the script uses 2400s to empty
  the node);
- progress that names what it is waiting on;
- no temptation to `--force`.

A forced delete would skip Postgres's clean shutdown and could
attach the iSCSI volume on the new node before it detaches from the
old one.

### Observation 4: "upgraded" is a per-role checklist, not a version string

These checks verified work01, and the script now automates them:

| Check | Workers | Control planes |
| ----- | ------- | -------------- |
| Node Ready, OS image at the target version | yes | yes |
| Extensions match the class schematic (kata-containers present) | yes | base set |
| `/dev/kvm`, `vhost-net`, `vhost-vsock` present | yes | — |
| etcd 3/3 members, snapshot taken before | — | yes |
| cilium#48420: 11 `cil_sock*` hooks at the root cgroup | yes | yes |
| cilium#48420: host-network ClusterIP answers | yes | yes |
| Cluster settled: nothing Pending, Terminating or unready | yes | yes |

Most of these rows come from the config: the target version, the
class schematic, the extension set (which implies the devices), and
the role. Two come from the cluster's add-on set: the Cilium checks,
and the CNPG and OpenBao facts. A tool that renders the config can
derive the first group itself, but needs a home for the second.

### Observation 5: the bootstrap step model already fits a node upgrade

`steps.Step` is `{Name, Check, Apply}`. `Runner` runs Check, skips the
step when done, and otherwise applies. A re-run resumes because
finished steps check as done, and dry-run calls only Check. A node
upgrade fits that shape:

- one step per node, in a deliberate order (workers, then control
  planes one at a time);
- Check = running version and schematic match, and the node is
  Ready;
- Apply = the sequence above.

The Talos `Client` interface is narrow today: Bootstrap, Kubeconfig,
EtcdMemberList, Health. IMPL-0004 Phase 3 already plans to add reading
a node's live config, listing its extensions, and per-node
addressing. An upgrade would also need Upgrade and Reboot, plus a way
to wait for a node. The main hoomlab module (`internal/`) has no code
yet. INV-0003 records that the service's reconcile loop is meant to
take over these same steps.

### Observation 6: the upgrade exposed data-safety gaps the tool should gate on

None of the nine CNPG clusters has a backup configured: no barman
object store and no plugin, although the barman-cloud plugin is
installed in `cnpg-system`. Separately, work01's kernel log shows
`Aborting journal on device sd*-8` — ext4 on iSCSI LUNs — at the
moment pods were evicted. Earlier occurrences date from 2026-09-27 and
2026-10-10, so this predates the upgrade.

Each upgrade moves every stateful pod at least once. An unattended
owner should refuse to move a database that has no recent backup, or
at least say so loudly. These gaps belong to their own work, but the
upgrade path is where they bite.

## Conclusion

**Answer:** Inconclusive (Open). Observations 1–6 establish the
requirements. Approach steps 2–6 are still to do: the machinery API
split, the eviction-policy decision, existing tools, the step sketch,
and the Kubernetes client.

## Recommendation

Pending the remaining approach steps. Expected direction, to be
confirmed:

- A DESIGN for a `bootstrap talos upgrade` stage: one step per node,
  with the eviction policy and the Observation 4 checks built in, a
  dry-run that lists each node's blockers, and the "installed, not
  rebooted" state handled. It ships after IMPL-0004 Phase 3's client
  work, which it reuses.
- Kubernetes-version upgrades (`upgrade-k8s`, IMPL-0004 Phase 1) are in
  scope for the same stage or a sibling. The DESIGN decides which.
- Separate follow-ups, outside this investigation:
  - CNPG backups through the installed barman-cloud plugin;
  - a second instance for the clusters that matter;
  - the iSCSI journal aborts.
- The `~/drill/upgrade-node.sh` script stays the operator path until
  the stage exists, and serves as its executable specification.

## References

- IMPL-0004 Phase 0.4: the rolling re-image and its drain-blocker
  finding.
- DESIGN-0005: Talos 1.13.11 upgrade with Kata.
- DESIGN-0006: Talos 1.14 multi-document migration requirements.
- INV-0003: Platform reuse for hoomlab (Observation 6: recovery can't
  depend on itself).
- ADR-0001: the bootstrap CLI and its converge-from-config model.
- `tools/bootstrap/internal/steps/steps.go`: Step and Runner.
- `tools/bootstrap/internal/talos/client.go`: the Talos Client
  interface.
- cilium/cilium#48420: socket-LB regression, checked after every
  reboot.
