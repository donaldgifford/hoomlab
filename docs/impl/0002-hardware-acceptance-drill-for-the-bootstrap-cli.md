---
id: IMPL-0002
title: "Hardware acceptance drill for the bootstrap CLI"
status: Draft
author: Donald Gifford
created: 2026-08-22
---

<!-- markdownlint-disable-file MD024 MD025 MD041 -->

# IMPL-0002: Hardware acceptance drill for the bootstrap CLI

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Preparation on the existing hosts](#phase-1-preparation-on-the-existing-hosts)
  - [Phase 2: Form and certify the real cluster](#phase-2-form-and-certify-the-real-cluster)
  - [Phase 3: Talos artifacts and the booty environment](#phase-3-talos-artifacts-and-the-booty-environment)
  - [Phase 4: Talos cluster creation](#phase-4-talos-cluster-creation)
  - [Phase 5: Convergence pass and fold-back](#phase-5-convergence-pass-and-fold-back)
  - [Phase 6: Release and closure](#phase-6-release-and-closure)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Execute INV-0001 the pragmatic way: use the three existing bare-metal
PVE hosts — already installed and individually configured, not
re-imaged — to take the `bootstrap` CLI through its entire flow for
real: form the cluster, certify it with Cloudflare DNS-01 + ACME so the
node UIs carry valid certificates, then create and verify a Talos
cluster on top of it, with booty running in an operator-configured
environment. Fold every deviation back into code and docs, prove the
second full pass applies nothing, and cut `tools/bootstrap/v0.1.0`.

This deliberately front-loads *manual* iteration: build → run → adjust
→ re-run, recorded in INV-0001 as it happens. The isolated, VLAN'd
nested test environment (a hoomlab-owned booty VM PXE-provisioning both
a nested PVE cluster and the Talos cluster on top of it) is wanted —
but designing it before ever creating a Talos cluster with this CLI
would be speculating about requirements this run exists to discover.
The manual run **is** that environment's requirements gathering.

Why this beats a nested rehearsal first: the nested lab's value was
avoiding expensive bare-metal round trips, and that cost does not
exist here — PVE is already installed on all three hosts, and every
remaining step in the flow is API calls and VMs. The area most likely
to churn (Talos cluster creation) iterates at the same speed on the
real cluster as it would nested.

This closes out IMPL-0001's remaining hardware-gated tasks and
discharges RFC-0001's Phase 1 success criterion: `kubectl get nodes`
via the emitted kubeconfig shows every configured node `Ready`.

**Implements:** INV-0001 (the investigation is the record; this doc is
the work plan). Parent chain: IMPL-0001 → DESIGN-0001 → ADR-0001 →
RFC-0001 Phase 1.

## Scope

### In Scope

- The full bootstrap flow on the three real PVE hosts, from their
  *current* state: unclustered but individually configured (storage,
  SSH keys, etc.) — not fresh installs. The gap between that reality
  and the runbook's "must be fresh installs" prerequisite is itself in
  scope: what joining actually preserves and destroys gets verified
  and documented.
- Certificates on the real cluster: Cloudflare DNS-01, Let's Encrypt
  staging first, production once the stage converges (OQ-1), ending
  with valid certificates on every node UI.
- Talos cluster creation on the real cluster, with booty running in an
  environment the operator configures manually.
- Fold-back: every deviation becomes a code fix (with a regression
  test), a runbook fix, or a documented decision. A bunch of changes
  are *expected* in the Talos-creation area — that is the point of the
  run, not a failure of it.
- The convergence pass, `tools/bootstrap/v0.1.0`, and the doc-chain
  closure (IMPL-0001 → Completed, DESIGN-0001 → Implemented,
  INV-0001 → Concluded).

### Out of Scope

- **The isolated nested test environment** (dedicated test VLAN, a
  hoomlab-controlled booty VM scoped to it, a nested 3-VM PVE cluster
  PXE-installed from that booty, Talos on top — all automated). It is
  deliberately deferred, not rejected: requirements observed during
  this run get captured for it (Phase 6), and it becomes its own
  design doc afterwards (OQ-4). Note it needs capabilities nothing
  ships today — PVE-installer PXE profiles for booty and nested-VM
  provisioning tooling in hoomlab — which is exactly why it should be
  designed after the manual run, not before.
- pvelab. It stays what it is — proxmox-go-sdk's own validation
  harness and example CLI. The bootstrap CLI has no dependency on it
  (its formation *logic* was adapted from pvelab's, but the code lives
  in `internal/pve`), and hoomlab's future test tooling will be its
  own, not an extension of the SDK's.
- New bootstrap CLI features. Code changes here are run-driven fixes
  only; anything bigger gets its own doc.
- The Hoomlab service taking ownership of the cluster (RFC-0001
  Phase 2+), and day-2 operations beyond the re-image spot-check.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Failures during
Phases 2–4 are findings: record in INV-0001's deviations table first,
then fix-and-continue (config/environment mistakes) or fold back
through code with a regression test (defects), re-running the stage
either way.

---

### Phase 1: Preparation on the existing hosts

Everything decided and staged so the first `pve form` run starts from a
known, recoverable state. The hosts are configured, not disposable —
so the primary choice and the backups here are what bound the blast
radius of first contact.

#### Tasks

- [ ] Resolve OQ-1 through OQ-4 below and record the decisions in this
      doc (strikethrough the losing options, IMPL-0001 style)
- [ ] Choose the primary node deliberately: the cluster is created on
      it and its `/etc/pve` *becomes* the cluster configuration —
      joiners get theirs replaced. The node whose local config
      (storage definitions especially) should become cluster truth is
      the primary
- [ ] Back up `/etc/pve` (tar) and note `/etc/network/interfaces` on
      all three nodes — the two-minute insurance against a botched
      join costing real reconfiguration
- [ ] Confirm both joiner nodes are guest-free (PVE refuses to join a
      node with guests) and inventory what node-local config exists on
      them (storage entries, users, tokens) so post-join losses are
      expected, not discovered
- [ ] Root credentials per OQ-2's decision: one shared `root@pam`
      password across all nodes (align them), or the config model
      grows per-node passwords first
- [ ] Create the API token on the **primary** (`pve form` dials
      joiners as root@pam, and every other stage dials the primary,
      which proxies node-scoped calls cluster-wide — a token on the
      joiners would not survive the join anyway)
- [ ] DNS A records for every `<node>.<domain>` certificate FQDN, and
      a Cloudflare token scoped to that zone
- [ ] Talos boot-network prerequisites: DHCP reservations for every
      configured Talos MAC, and `talos.endpoint`'s host resolving to
      the first control-plane node's reserved address
- [ ] Write the real `bootstrap.hcl` in a scratch drill directory
      (real endpoints, MACs, VMIDs, storage, bridges; staging
      `acme.directory` per OQ-1); export the four `HOOMLAB_*`
      variables; `bootstrap validate` exits 0
- [ ] Record the environment in INV-0001's table: PVE version, node
      names/endpoints, CLI commit SHA, lab topology

#### Success Criteria

- Every open question below shows **Decided** with a date.
- `bootstrap validate` passes against the real config; no secret value
  appears in it.
- `/etc/pve` backups exist for all three nodes, the joiners are
  guest-free, and the expected join-time config losses are written
  down *before* the join.

---

### Phase 2: Form and certify the real cluster

First contact between the CLI and real Proxmox. The end state you
actually want: the three hosts clustered, quorate, and serving their
UIs with valid certificates.

#### Tasks

- [ ] `bootstrap pve form --dry-run`: the survey lists create-cluster,
      one join per joiner, and cluster-quorate as pending, and touches
      nothing
- [ ] `bootstrap pve form`: cluster created on the primary, joiners
      joined serially, quorum verified; every node's UI reachable
      afterwards
- [ ] Verify the join-wipe reality against the prediction from
      Phase 1: what survived on the joiners, what was replaced;
      re-declare any joiner-local storage cluster-wide; amend the
      runbook's "fresh installs" prerequisite to the precise truth
- [ ] `bootstrap pve certs` against LE staging until it converges
      cleanly (account, Cloudflare plugin, per-node domains, orders)
- [ ] Per OQ-1: flip `acme.directory` to production and re-run — every
      node UI presents a valid browser-trusted certificate
- [ ] Convergence: re-run both stages; each reports every step done,
      nothing applied
- [ ] Record every real-Proxmox-vs-mockpve discrepancy in INV-0001;
      fix code + mockpve seeding + tests for each

#### Success Criteria

- One command formed the cluster from the hosts' real starting state;
  interruptions (if any occurred) converged on re-run.
- All three node UIs serve valid production certificates.
- The second pass of both stages applies nothing.
- The runbook's prerequisites section now tells the truth about
  non-fresh nodes.

---

### Phase 3: Talos artifacts and the booty environment

The handoff point between the CLI's output and the operator-managed
booty instance. Booty's environment is configured manually this run —
by design; what that configuration turns out to require is input for
the future test environment's design.

#### Tasks

- [ ] `bootstrap talos secrets`; back up `secrets.yaml` immediately
      (destination decided in Phase 1's config task)
- [ ] `bootstrap talos emit` and `bootstrap talos ipxe` against the
      real config
- [ ] Stand up booty where it will serve the boot network (operator
      task, manual configuration); copy the emitted tree over
- [ ] Compare the manual booty invocation against the emitted
      `booty-run.sh` — every deliberate difference gets noted in
      INV-0001 (the launcher encodes the sharp edges; where the manual
      setup diverges, either the launcher or the runbook is wrong for
      this environment, and that is a finding)
- [ ] Runbook step 7 verification before any VM exists: `/boot.ipxe`,
      `/ipxe?mac=…`, `/machine-config?mac=…` for a configured MAC
      (right role + hostname), 404 for an unconfigured MAC, boot
      assets served at full length

#### Success Criteria

- booty serves the emitted catalog, templates, and boot assets on the
  real boot network, verified by the runbook's curl checks.
- `secrets.yaml` is backed up and 0600; a `talos secrets` re-run
  leaves it alone.
- Every manual-vs-emitted-launcher difference is recorded, not
  shrugged off.

---

### Phase 4: Talos cluster creation

The part that has never run anywhere, and the reason this whole run
exists. Expect changes — the loop is run → record → fix → rebuild →
re-run, with INV-0001 as the log.

#### Tasks

- [ ] `bootstrap talos vms`: every configured VM created on its node
      and started
- [ ] Watch the full first-boot cycle per VM (booty logs +
      `qm terminal`): proxyDHCP/PXE → `ipxe.efi` chainload → per-MAC
      boot script → kernel/initramfs → Talos install → reboot from
      disk into Talos; record per-VM notes in INV-0001
- [ ] `bootstrap talos bootstrap`: etcd bootstrapped against the
      endpoint (first control-plane node), `talosconfig` +
      `kubeconfig` written 0600 under `<output>/out/`
- [ ] `bootstrap talos health` passes within the wait
- [ ] `kubectl --kubeconfig <output>/out/kubeconfig get nodes`: every
      configured node `Ready` — fill INV-0001's 12-step drill table as
      each step lands
- [ ] For every failure along the way: deviations table first, then
      the fix — CLI defects get a regression test and a rebuilt
      binary; environment/config mistakes get a runbook amendment if
      the runbook could have prevented them

#### Success Criteria

- All 12 rows of INV-0001's drill-results table filled in; row 12 is
  every node `Ready` — RFC-0001's Phase 1 criterion, demonstrated.
- Credential files were never overwritten by any re-run along the way.
- Every deviation encountered is recorded with its resolution.

---

### Phase 5: Convergence pass and fold-back

The property the Hoomlab service will later rely on: taking ownership
converges on no-op. Then the findings become durable.

#### Tasks

- [ ] Re-run every stage in order against the live cluster
      (`pve form`, `pve certs`, `talos secrets`, `talos emit`,
      `talos ipxe`, `talos vms`, `talos bootstrap`, `talos health`);
      fill INV-0001's convergence table with the applied count per
      stage
- [ ] For any stage that applied something: diagnose the re-fired
      step, fix its Check with a regression test reproducing the
      false-pending, re-run to no-op
- [ ] Re-image spot-check: wipe one worker's disk and reboot; confirm
      it PXE-boots, reinstalls, and rejoins — the runbook's re-imaging
      claim, verified
- [ ] Close out the deviations table: every row has a resolution
      (code fix with test, runbook fix, or documented
      accepted-behavior note)
- [ ] All gates green on the fold-back branch: `just bootstrap-lint` /
      `bootstrap-test` / `bootstrap-build`, `govulncheck`,
      `markdownlint`, `yamllint`

#### Success Criteria

- Every convergence-table row reads `0` (or the documented
  equivalent), with any exception fixed and re-verified — not
  explained away.
- The re-image cycle works as the runbook describes.
- No unresolved deviation remains.

---

### Phase 6: Release and closure

The run passed; make it official, and turn what it taught into the
next doc.

#### Tasks

- [ ] Merge the fold-back branch(es) to `main` with `dont-release`
      (tools stay off the service release train)
- [ ] Dispatch `tools-release.yml` with tool=`bootstrap`,
      version=`v0.1.0`; verify the `tools/bootstrap/v0.1.0` tag, the
      GitHub release with binary archives, and that
      `go install github.com/donaldgifford/hoomlab/tools/bootstrap@v0.1.0`
      resolves
- [ ] Complete INV-0001: Conclusion answered, Environment table
      filled, status → **Concluded**
- [ ] IMPL-0001: annotate the nested spot-check task per OQ-3's
      decision, check off the remaining tasks, status → **Completed**
- [ ] DESIGN-0001 status → **Implemented**
- [ ] Update the runbook's `[unverified]` markers to match what this
      run verified; `docz update` for the index tables
- [ ] Seed the future test environment doc per OQ-4's decision,
      carrying the requirements this run observed (what booty's
      environment needed, what nested provisioning must provide, what
      a PVE-installer PXE profile would take)
- [ ] This doc: all boxes checked, status → **Completed**

#### Success Criteria

- `tools/bootstrap/v0.1.0` exists and installs.
- The doc chain is consistent: RFC-0001 Phase 1 demonstrated,
  IMPL-0001 Completed, DESIGN-0001 Implemented, INV-0001 Concluded,
  runbook current, and the test-environment follow-up captured rather
  than lost.

## Testing Plan

This IMPL *is* a testing plan — the phases are the tests. What keeps
CI honest alongside them:

- Every run-driven code fix lands with a regression test that fails
  without the fix, keeping `tools-ci.yml` the gate for the code half
  of every finding.
- Real-Proxmox discrepancies found in Phase 2 are fixed in mockpve's
  *seeding*, so the unit suite's model of Proxmox tightens with each
  finding.
- The run itself stays deliberately out of CI (IMPL-0001 already
  decided the e2e drill is not a merge gate); its record is INV-0001.

## Dependencies

- The three bare-metal PVE hosts in their current state, and the
  authority to cluster them (this run changes them permanently —
  joining replaces the joiners' `/etc/pve`).
- A Cloudflare-managed DNS zone for the certificate FQDNs; Let's
  Encrypt staging and production.
- An operator-configured booty environment on the boot network
  (manual this run, by design).
- DHCP control on the boot network (reservations for the Talos MACs).
- `tools-release.yml` (already merged) for Phase 6.

## Open Questions

Answer each with the letter of the chosen option (**a** is my
recommendation), or **other** with your own. Decisions get recorded
inline, IMPL-0001 style.

**OQ-1 — Certificate CA sequencing: staging first, or straight to
production?** The end state is valid production certificates on every
node UI. The question is only the path while the certs stage is being
exercised for the first time against real Proxmox and real Cloudflare.

- **a (recommended):** Staging until `pve certs` converges cleanly,
  then flip `acme.directory` to production and re-run once. Iterating
  a first-contact stage directly against production LE risks
  rate-limiting the domain (production limits are strict and last up
  to a week); the flip costs one extra command and also proves the
  staging→production transition works.
- b: Straight to production. One less re-run if everything works
  first try; an expensive week if the stage has a retry bug.

**OQ-2 — `pve form` assumes one shared root password. Your hosts, your
call.** `applyJoin` dials each joining node as `root@pam` with the
single `root_password` config value, and sends that same value as the
cluster password in the join spec — the design assumes all nodes share
it.

- **a (recommended):** Align all three hosts to one root password for
  the run (rotate afterwards if wanted). No code change, matches the
  design's assumption, and the password is only load-bearing during
  joins.
- b: Extend the config model to per-node root passwords before first
  contact. More faithful to hosts as they are, but it changes config
  schema + validation + form + runbook ahead of any evidence the
  simpler model fails.

**OQ-3 — IMPL-0001's "nested spot-check" checkbox: what satisfies
it?** The pragmatic path replaces the nested `pve form` rehearsal with
first contact on the real cluster.

- **a (recommended):** Annotate the task as superseded — the real
  three-node formation in Phase 2 is a strictly stronger validation
  than a nested rehearsal of the same stage — and check it off when
  Phase 2 passes, with a pointer to INV-0001.
- b: Leave it unchecked until the future nested test environment
  exists and runs it nested. Keeps the letter of the task; blocks
  IMPL-0001's completion on tooling that is deliberately deferred.

**OQ-4 — Where does the future isolated test environment get
captured?** The VLAN'd, booty-driven nested environment (PXE-installed
nested PVE cluster + Talos on top) is wanted, deliberately deferred,
and this run generates its requirements.

- **a (recommended):** Collect requirements in INV-0001 as they're
  observed during the run, then write a DESIGN doc after Phase 6 with
  the real inputs in hand. The manual run is the requirements
  gathering; the design comes after the evidence.
- b: Open a placeholder INV/DESIGN doc now and append to it during
  the run. Captures intent earlier, at the cost of a doc that's
  mostly empty until the run finishes anyway.

## References

- [INV-0001](../investigation/0001-bootstrap-cli-hardware-acceptance-drill.md)
  — the investigation this implements; holds the result tables
- [Runbook: bare Proxmox nodes → healthy Talos
  cluster](../runbook/bootstrap-cluster.md) — the procedure Phases 2–5
  follow (amended along the way where reality disagrees)
- [IMPL-0001](0001-bootstrap-cli.md) — the code-complete predecessor
  this closes out
- [DESIGN-0001](../design/0001-bootstrap-cli.md) — convergence model
  and stage design
- [ADR-0001](../adr/0001-bootstrap-cli-and-service.md) — the layering
  decision (the SDK owns pvelab; hoomlab owns its own flow)
- [RFC-0001](../rfc/0001-hoomlab-a-self-hosted-cloud-for-homelab-environments.md)
  — Phase 1 success criterion
- [`tools-release.yml`](../../.github/workflows/tools-release.yml) —
  the Phase 6 release mechanism (workflow_dispatch, tag
  `tools/bootstrap/vX.Y.Z`)
