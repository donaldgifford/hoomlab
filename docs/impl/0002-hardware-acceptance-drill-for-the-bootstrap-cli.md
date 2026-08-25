---
id: IMPL-0002
title: "Hardware acceptance drill for the bootstrap CLI"
status: In Progress
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

Execute INV-0001 the pragmatic way, in two goals:

1. **Evaluate whether bootstrap as it stands is acceptable to run
   against the production bare-metal nodes** — then run it: form the
   cluster from the hosts' current state and certify it with
   Cloudflare DNS-01 + ACME so every node UI carries a valid
   certificate.
2. **Use that cluster to test Talos cluster creation** with the CLI,
   against the existing booty service already running in the
   environment — the fastest feedback loop available, in the actual
   environment the tool is for.

Fold every deviation back into code and docs, prove the second full
pass applies nothing, and cut `tools/bootstrap/v0.1.0`. The three PVE
hosts are already installed and individually configured, not
re-imaged; what clustering does to that state is itself under test.

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
  deliberately deferred, not rejected: this run is its requirements
  gathering, and once the production cluster exists, the nested
  environment gets built *on it*, working backwards from a known-good
  state — with the production cluster and production Talos cluster as
  the reference it is validated against (OQ-4). Note it needs
  capabilities nothing ships today — PVE-installer PXE profiles for
  booty and nested-VM provisioning tooling in hoomlab — which is
  exactly why it is designed after the manual run, not before.
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

- [x] Resolve OQ-1 through OQ-4 below and record the decisions in this
      doc (strikethrough the losing options, IMPL-0001 style) —
      decided 2026-08-22
- [x] Choose the primary node deliberately: the cluster is created on
      it and its `/etc/pve` *becomes* the cluster configuration —
      joiners get theirs replaced. The node whose local config
      (storage definitions especially) should become cluster truth is
      the primary — **decided 2026-08-23: `r740a`** (largest storage
      node; the handoff snapshot verified all three carry byte-identical
      installer-default `/etc/pve`, so nothing on the joiners is
      contested, and the one delta worth keeping — the `root@pam!sdk`
      token — lives on r740a and survives as primary)
- [x] Back up `/etc/pve` (tar) and note `/etc/network/interfaces` on
      all three nodes — the two-minute insurance against a botched
      join costing real reconfiguration — **done 2026-08-25**: pulled
      off-node to the workstation (`~/drill/backups/`, per-node
      `<node>-etc-pve-2026-08-25.tgz` + `<node>-interfaces-2026-08-25`)
- [x] Confirm both joiner nodes are guest-free (PVE refuses to join a
      node with guests) and inventory what node-local config exists on
      them (storage entries, users, tokens) so post-join losses are
      expected, not discovered — **verified 2026-08-22/23** (handoff
      snapshot): zero guests on all three; joiner `/etc/pve` is
      installer-default and byte-identical to the primary's
      (`storage.cfg` = `local` + `local-zfs`, `user.cfg` = root only,
      `datacenter.cfg` = one line). **Predicted join loss: nothing** —
      the replace is a same-content overwrite
- [x] Confirm the `root@pam` password is identical on all three nodes
      (OQ-2: the nodes were built from per-node answer files with
      node-specific ansible on top, but access credentials are already
      set everywhere — align via ansible if they differ; `pve form`
      dials every joiner with the single configured password) —
      **confirmed 2026-08-23**: the answer files set one root password
      fleet-wide; seeded in 1Password (`pve-root`, homelab vault)
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
      `acme.directory` per OQ-1); inject the four `HOOMLAB_*`
      variables at runtime via `op run --env-file` (no 1Password
      integration in the CLI — the wrapper is the integration);
      `bootstrap validate` exits 0. Settled inputs (2026-08-23):
      cluster name `shart`; cert domain `shart.sh`; endpoints by mgmt
      IP (r740a `10.10.11.20`, r640a `10.10.11.21`, srv01
      `10.10.11.40`); each node's `address` = its sync0 IP
      (`10.10.15.20/.21/.40` — the decided corosync link0; the CLI
      carries a single link, so mgmt-as-link1 redundancy is a
      post-formation manual step if wanted — procedure noted in the
      runbook, SDK follow-up filed in Phase 6); join order = config
      order (r640a, then srv01); VMIDs per the 100s infra / 200s CP /
      300s workers convention
- [ ] Record the environment in INV-0001's table: PVE version, node
      names/endpoints, CLI commit SHA, lab topology
- [ ] The goal-1 gate: with the predictions, backups, and dry-runs in
      hand, make the explicit go/no-go call that bootstrap as it
      stands is acceptable to run against the production nodes, and
      record it in INV-0001

#### Success Criteria

- Every open question below shows **Decided** with a date.
- `bootstrap validate` passes against the real config; no secret value
  appears in it.
- `/etc/pve` backups exist for all three nodes, the joiners are
  guest-free, and the expected join-time config losses are written
  down *before* the join.
- The go/no-go call is recorded — running against production is a
  decision made on evidence, not momentum.

---

### Phase 2: Form and certify the real cluster

First contact between the CLI and real Proxmox, taken incrementally
(OQ-1): a cluster of one first, certified end-to-end on the primary,
then grown to three by re-running the same commands against an
expanded config. The growth path is not a workaround — it exercises
the convergence model doing exactly what it claims (already-done steps
skip, new steps apply), and it means the certs stage is proven on one
node before it ever touches the other two.

#### Tasks

- [ ] Single node first: with a primary-only `bootstrap.hcl`,
      `pve form --dry-run` then `pve form` — the cluster of one
      created on the primary, quorate, UI reachable
- [ ] `bootstrap pve certs` on the single node against LE staging
      until it converges cleanly (account, Cloudflare plugin, domain,
      order)
- [ ] Flip `acme.directory` to production and re-run — the primary's
      UI presents a valid browser-trusted certificate (OQ-1: once
      it's correct here, it's production-correct)
- [ ] Grow to three: expand the config to all nodes;
      `pve form --dry-run` shows exactly the two joins pending;
      `pve form` joins them serially, full quorum verified, every UI
      reachable
- [ ] Verify the join-wipe reality against the prediction from
      Phase 1: what survived on the joiners, what was replaced;
      re-declare any joiner-local storage cluster-wide; amend the
      runbook's "fresh installs" prerequisite to the precise truth
- [ ] `bootstrap pve certs` (production) extends to the joiners:
      domains wired, orders complete, all three node UIs presenting
      valid certificates
- [ ] Convergence: re-run both stages; each reports every step done,
      nothing applied
- [ ] Record every real-Proxmox-vs-mockpve discrepancy in INV-0001;
      fix code + mockpve seeding + tests for each

#### Success Criteria

- The cluster grew from one node to three by re-running the same
  commands against a changed config — the convergence model's
  incremental-growth claim, demonstrated on real hardware.
- All three node UIs serve valid production certificates.
- The second pass of both stages applies nothing.
- The runbook's prerequisites section now tells the truth about
  non-fresh nodes.

---

### Phase 3: Talos artifacts and the booty environment

The handoff point between the CLI's output and the **existing booty
service already running in the environment** — operator-managed this
run, by design. What deploying to it turns out to require is input for
the future test environment's design.

#### Tasks

- [ ] `bootstrap talos secrets`; back up `secrets.yaml` immediately
      (destination decided in Phase 1's config task)
- [ ] `bootstrap talos emit` and `bootstrap talos ipxe` against the
      real config, with `talos.booty.url` pointing at the existing
      booty service
- [ ] Deploy the emitted tree (catalog, templates, boot assets,
      `ipxe.efi`) to the running booty service and restart it —
      operator task; booty loads the catalog once at startup
- [ ] Compare the existing service's live configuration against the
      emitted `booty-run.sh` — every deliberate difference gets noted
      in INV-0001 (the launcher encodes the sharp edges; where the
      real environment diverges, either the launcher or the runbook is
      wrong for this environment, and that is a finding)
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
- [ ] Write the future test environment's DESIGN doc (OQ-4), carrying
      the requirements this run observed: what booty's environment
      needed, what nested provisioning must provide, what a
      PVE-installer PXE profile would take — the environment to be
      built on the production cluster this run created, validated
      against the production cluster and Talos cluster as the
      reference
- [ ] File the multi-link corosync follow-up against proxmox-go-sdk
      (noted 2026-08-23): the PVE API accepts `link0`–`link7` on both
      cluster create and join, and `GET /cluster/config/nodes` exposes
      each node's ring addresses — so links are both settable and
      listable. Today the SDK only carries them via the untyped
      `Extra` map (which is how bootstrap sets `link0`); the
      enhancement is first-class typed link fields on the create/join
      specs plus link addresses on the config-node read. Once the SDK
      has that, bootstrap can grow a per-node second address and form
      with `link1` redundancy directly instead of the manual
      post-formation `corosync.conf` edit the runbook documents
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
- The existing operator-managed booty service on the boot network
  (deployed to manually this run, by design).
- DHCP control on the boot network (reservations for the Talos MACs).
- `tools-release.yml` (already merged) for Phase 6.

## Open Questions

OQ-1 through OQ-4 decided **a** on 2026-08-22 and folded into the
phase tasks above; the reasoning stays for the record. OQ-5 (added
2026-08-23) is **open** — it gates Phase 4's Talos config, not the
formation and certs work in Phases 1–3.

**OQ-1 — Certificate CA sequencing: staging first, or straight to
production?** The end state is valid production certificates on every
node UI. The question is only the path while the certs stage is being
exercised for the first time against real Proxmox and real Cloudflare.

**Decided: a** (2026-08-22), with the caveat that reshaped Phase 2:
the stage gets its first exercise against a *single node* (the
primary-only config), so even a production misstep would burn a
handful of attempts, not fifty. Staging proves the stage converges;
the production run is the authoritative verification — once it is
correct there, it is production-correct, which is the feedback this
pragmatic pass exists to get.

- **a:** Staging until `pve certs` converges cleanly, then flip
  `acme.directory` to production and re-run. Iterating a
  first-contact stage directly against production LE risks
  rate-limiting the domain (production limits are strict and last up
  to a week); the flip costs one extra command and also proves the
  staging→production transition works.
- ~~b: Straight to production. One less re-run if everything works
  first try; an expensive week if the stage has a retry bug.~~

**OQ-2 — `pve form` assumes one shared root password. Your hosts, your
call.** `applyJoin` dials each joining node as `root@pam` with the
single `root_password` config value, and sends that same value as the
cluster password in the join spec — the design assumes all nodes share
it.

**Decided: a** (2026-08-22). The three nodes were built from per-node
answer files with node-specific ansible on top — they are deliberately
different — but access credentials (passwords, SSH keys) are already
set on all of them. Phase 1 confirms the `root@pam` password is
identical across the three (ansible makes aligning it cheap if it
isn't); the single-password config model stands unless the run itself
proves it wrong, which would be a fold-back, not a precondition.

- **a:** One shared root password across the nodes. No code change,
  matches the design's assumption, and the password is only
  load-bearing during joins.
- ~~b: Extend the config model to per-node root passwords before
  first contact. More faithful to hosts as they are, but it changes
  config schema + validation + form + runbook ahead of any evidence
  the simpler model fails.~~

**OQ-3 — IMPL-0001's "nested spot-check" checkbox: what satisfies
it?** The pragmatic path replaces the nested `pve form` rehearsal with
first contact on the real cluster.

**Decided: a** (2026-08-22) — *deferred* is the operative word: the
spot-check is not performed nested, because the production nodes
already provide exactly what it wanted (unclustered PVE nodes to form)
and Phase 2's real formation is a strictly stronger validation of the
same stage. IMPL-0001's task gets annotated as
deferred-and-superseded with a pointer to INV-0001 and checked off
when Phase 2 passes, so IMPL-0001's completion does not block on
tooling that is deliberately deferred.

- **a:** Annotate as superseded by the real-cluster formation; check
  off when Phase 2 passes.
- ~~b: Leave it unchecked until the future nested test environment
  exists and runs it nested. Keeps the letter of the task; blocks
  IMPL-0001's completion on tooling that is deliberately deferred.~~

**OQ-4 — Where does the future isolated test environment get
captured?** The VLAN'd, booty-driven nested environment (PXE-installed
nested PVE cluster + Talos on top) is wanted, deliberately deferred,
and this run generates its requirements.

**Decided: a** (2026-08-22), with two refinements: the nested
environment gets built **on the production cluster this run creates**
— working backwards from a known-good state once the tool demonstrably
works in the environment it is for — and the production cluster +
production Talos cluster become the **reference the test environment
is validated against**: the nested env must reproduce what prod
demonstrably does.

- **a:** Collect requirements in INV-0001 as they are observed during
  the run, then write the DESIGN doc after Phase 6 with the real
  inputs in hand. The manual run is the requirements gathering; the
  design comes after the evidence.
- ~~b: Open a placeholder INV/DESIGN doc now and append to it during
  the run. Captures intent earlier, at the cost of a doc that is
  mostly empty until the run finishes anyway.~~

**OQ-5 — Do the Talos VM NICs on `vmbr1` need a VLAN tag?** **Open**
(2026-08-23). The guest bridge `vmbr1` is VLAN-aware and its trunk
ports pass tagged traffic, but whether the Talos boot/machine network
reaches guests untagged (native VLAN on the trunk) or requires
`tag=<vlan>` on `net0` is undecided. The CLI currently renders `net0`
without VLAN-tag support — if a tag is required, that is a small
config-schema + VM-spec change (with tests) that must land before
Phase 4's `talos vms`.

- **a (recommended):** Read the answer off the network config (UniFi
  port profiles for the guest trunks): if the Talos network arrives
  tagged, add an optional VLAN field to the Talos config rendered as
  `tag=` on `net0`; if it is the native VLAN, no change needed.
  Resolve before writing the full (three-node + Talos) config, so
  Phase 4 starts unblocked.
- **b:** Re-profile the trunk ports so the Talos VLAN is native,
  avoiding the code change. Bends the network to the tool's current
  limitation and special-cases the Talos VLAN on ports that
  deliberately trunk everything.

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
