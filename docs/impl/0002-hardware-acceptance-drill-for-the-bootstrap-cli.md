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
  - [Phase 1: Groundwork and open-question resolution](#phase-1-groundwork-and-open-question-resolution)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Nested rehearsal — pve form and pve certs](#phase-2-nested-rehearsal--pve-form-and-pve-certs)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Nested PXE rehearsal](#phase-3-nested-pxe-rehearsal)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Hardware preparation](#phase-4-hardware-preparation)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: The drill](#phase-5-the-drill)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Convergence pass and fold-back](#phase-6-convergence-pass-and-fold-back)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Release and closure](#phase-7-release-and-closure)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Execute INV-0001: prove on real hardware that the `bootstrap` CLI takes
bare Proxmox nodes to a healthy Talos Kubernetes cluster by following
[the runbook](../runbook/bootstrap-cluster.md) literally, prove the
second full pass applies nothing, fold every deviation back into code
and docs, and cut `tools/bootstrap/v0.1.0`.

This closes out IMPL-0001's six remaining hardware-gated tasks and
discharges RFC-0001's Phase 1 success criterion: `kubectl get nodes`
via the emitted kubeconfig shows every configured node `Ready`.

The ordering principle, carried over from INV-0001's findings: every
defect found so far came from *running* something rather than reading
it, and hardware time is the most expensive place to find the next one.
So the phases run each risk in the cheapest environment that can
genuinely exercise it — workstation, then nested VMs, then hardware —
and nothing is attempted on hardware that a nested pass could have
caught first.

**Implements:** INV-0001 (the investigation is the record; this doc is
the work plan). Parent chain: IMPL-0001 → DESIGN-0001 → ADR-0001 →
RFC-0001 Phase 1.

## Scope

### In Scope

- The nested rehearsal of `pve form` and `pve certs` against a
  pvelab-provisioned 3-node lab (IMPL-0001 Phase 2's unchecked
  spot-check).
- A nested PXE rehearsal — booty + proxyDHCP + TFTP + `ipxe.efi`
  chainload against a VM — subject to OQ-4.
- Hardware preparation, the drill itself, and the convergence pass,
  all recorded in INV-0001's tables.
- Fold-back: every deviation becomes a code fix (with a regression
  test), a runbook fix, or a documented decision.
- Any upstream change pvelab needs to support the rehearsal (OQ-1),
  consumed as a released version per IMPL-0001's scope rule.
- Cutting `tools/bootstrap/v0.1.0` via `tools-release.yml`, and the
  status flips that follow (IMPL-0001 → Completed, DESIGN-0001 →
  Implemented, INV-0001 → Concluded).

### Out of Scope

- New bootstrap CLI features. Code changes here are drill-driven fixes
  only; anything bigger gets its own doc.
- The Hoomlab service taking ownership of the cluster (RFC-0001
  Phase 2+).
- Day-2 cluster operations, workload deployment, production cert
  automation beyond the one `pve certs` re-run (OQ-5).
- Forking booty, hclkit, or proxmox-go-sdk — gaps become upstream
  issues/PRs consumed at released versions, exactly as IMPL-0001 did.

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Phases 2 and 3
are rehearsals: their whole purpose is to fail cheaply, so a failure
there is a finding to fold back (fix code → re-run the rehearsal), not
a blocker to route around.

---

### Phase 1: Groundwork and open-question resolution

Decide the open questions, close the pvelab gap they surface, and get
every prerequisite in place so later phases spend their time running
stages, not discovering missing pieces.

#### Tasks

- [ ] Resolve OQ-1 through OQ-5 below and record the decisions in this
      doc (strikethrough the losing options, IMPL-0001 style)
- [ ] If OQ-1 = a: open the upstream proxmox-go-sdk issue/PR adding a
      provision-only mode to `pvelab up` (the lab package already
      separates `provision.go` from `cluster.go`; the flag skips the
      `FormCluster` call and records the unformed state), get it
      released, and pin the released version
- [ ] Write the nested-lab pvelab config (`pvelab.yaml` from
      `pvelab.example.yaml`): outer host per OQ-2, ≥3 nested nodes in
      the reserved 9200–9399 VMID block, and — if OQ-3 = a — the ACME
      variant with real A records for the nested node FQDNs
- [ ] Write the nested-lab `bootstrap.hcl`: the nested nodes' endpoints
      as the PVE nodes, LE staging `acme.directory`, and a scratch
      Talos topology
- [ ] Run `pvelab iso` (and `pvelab template build` for fast
      re-provisioning — teardown/re-up cycles are the rehearsal's inner
      loop)
- [ ] Stage the credentials: nested-lab PVE token +
      `root@pam` password, Cloudflare token scoped to the lab zone
      (if OQ-3 = a), all as environment variables per the runbook —
      never values in config
- [ ] Confirm `just bootstrap-build` from the branch that will be
      drilled and record the commit SHA in INV-0001's Environment table

#### Success Criteria

- Every open question below shows **Decided** with a date.
- `pvelab status` shows the prepared ISO/template on the outer host;
  a provision-only `up` path exists (or the OQ-1 alternative is
  documented as the accepted risk).
- Both config files exist, `bootstrap validate` passes against the
  nested-lab config, and no secret value appears in either file.

---

### Phase 2: Nested rehearsal — `pve form` and `pve certs`

First contact between the bootstrap CLI and a real PVE API. Everything
mockpve believes about Proxmox gets tested here, where a failed run
costs a `pvelab down && pvelab up`, not a rack visit. This checks off
IMPL-0001 Phase 2's nested spot-check.

#### Tasks

- [ ] `pvelab up` (provision-only): ≥3 fresh, unformed nested PVE
      nodes, API reachable on all of them
- [ ] `bootstrap validate` then `bootstrap pve form --dry-run` against
      the nested lab; confirm the survey lists create-cluster, one
      join per non-primary node, and cluster-quorate as pending
- [ ] `bootstrap pve form`: cluster created on the primary, nodes
      joined serially, quorum verified
- [ ] Interruption test: tear down, re-provision, run `pve form` again
      and kill it after the first join completes; re-run and confirm it
      picks up at the first unjoined node and converges
- [ ] `bootstrap pve certs` against LE staging (OQ-3): account
      registered, Cloudflare plugin created, per-node domains wired,
      orders complete
- [ ] Certificate drift test: rotate the Cloudflare token value and
      re-run `pve certs`; confirm the plugin step goes pending and the
      new credentials are pushed
- [ ] Convergence: re-run both stages and confirm each reports every
      step done, `0 steps applied`
- [ ] Record every mockpve-vs-Proxmox discrepancy found; fix code +
      mockpve seeding + tests for each, and re-run the rehearsal until
      it passes clean from a fresh `pvelab up`

#### Success Criteria

- A fresh provision-only lab goes from unformed nodes to a quorate,
  staging-certified cluster using only `bootstrap` commands, with the
  interruption re-run converging.
- The second pass of both stages applies nothing.
- Every discrepancy found is folded back (code, mockpve, runbook) and
  IMPL-0001's Phase 2 spot-check task is checked off.

---

### Phase 3: Nested PXE rehearsal

The riskiest untested seam is the PXE handshake — proxyDHCP answering
alongside a real DHCP server, TFTP serving `ipxe.efi`, the embedded
chain script reaching booty. All of it can be exercised against a
nested VM before any hardware is touched. Subject to OQ-4; if decided
against, strike this phase and the risk lands in Phase 5.

#### Tasks

- [ ] Set up the boot segment per OQ-4's decision (isolated bridge on
      the outer host with a scoped DHCP, or the drill's real boot
      network) and start booty there via the emitted `booty-run.sh`
- [ ] `bootstrap talos emit` + `talos ipxe` with `booty.url` pointing
      at the rehearsal booty host; copy the tree over and restart booty
- [ ] Create one UEFI test VM on the boot bridge matching the
      `talos vms` spec (ovmf + q35, no pre-enrolled keys, virtio NIC
      with a configured MAC, empty disk, order=scsi0;net0)
- [ ] Watch the full chain on the VM console and booty logs: proxyDHCP
      offer → TFTP `ipxe.efi` fetch → embedded chain script →
      `/ipxe?mac=…` → kernel/initramfs download → Talos boots with
      `talos.config` pointing at booty
- [ ] Verify the DHCP coexistence claim: the VM gets its IP from the
      real DHCP server and its boot path from booty's proxyDHCP, and
      no non-PXE client on the segment is affected
- [ ] Record findings in INV-0001; fold back any fix (booty flags in
      the emitted launcher, VM spec, runbook wording) and re-run

#### Success Criteria

- A VM with an empty disk chainloads the built `ipxe.efi`, fetches its
  machineconfig from booty by MAC, and begins a Talos install — the
  INV-0001 pre-drill task "verify the built ipxe.efi actually
  chainloads" gets checked without hardware.
- The existing DHCP server on the segment keeps working for everything
  else.

---

### Phase 4: Hardware preparation

Everything the runbook's "Before you start" section requires, made
true on the real lab — so drill day starts at `bootstrap validate`,
not at cabling.

#### Tasks

- [ ] Fresh Proxmox VE installs on every physical node (nodes other
      than the primary MUST be fresh — joining wipes them); record PVE
      version, node names, and endpoints in INV-0001's Environment
      table
- [ ] Create the drill API token and confirm the `root@pam` password
      works on every node
- [ ] Boot network ready: DHCP reservations for every configured Talos
      MAC, and `talos.endpoint`'s host resolving to the first
      control-plane node's reserved address
- [ ] DNS A records for every `<node>.<domain>` cert FQDN, and the
      Cloudflare token scoped to that zone
- [ ] Booty host ready on the boot segment (per OQ-2/OQ-4 decisions):
      docker installed, reachable at the `talos.booty.url` the config
      names
- [ ] Write the real `bootstrap.hcl` in a scratch drill directory:
      real endpoints, MACs, VMIDs, storage, bridges; LE staging
      directory for the drill
- [ ] Export the four `HOOMLAB_*` variables; `bootstrap validate`
      exits 0
- [ ] Decide and note the `secrets.yaml` backup destination before it
      exists (the runbook says back it up; pick where, now)

#### Success Criteria

- `bootstrap validate` passes against the real config.
- Every prerequisite in the runbook's "Before you start" is literally
  true and checked here — nothing left to discover on drill day.

---

### Phase 5: The drill

The event itself. Run **from the runbook, not from memory** — a wrong
or missing runbook step is a finding, not something to work around
silently. Results land in INV-0001's 12-step table as they happen.

#### Tasks

- [ ] Steps 1–7: `validate` → `pve form` → `pve certs` →
      `talos secrets` (backup taken immediately) → `talos emit` →
      `talos ipxe` → copy tree + start booty; verify booty serves
      before creating any VM
- [ ] Step 8: `talos vms` — every configured VM created and running on
      its node
- [ ] Step 9: watch the PXE → install → reboot-into-Talos cycle on
      every VM (booty logs + `qm terminal`); record per-VM notes
- [ ] Steps 10–12: `talos bootstrap` → `talos health` →
      `kubectl get nodes` shows every configured node `Ready`
- [ ] Fill INV-0001's drill-results table row by row as each step
      completes — including the rows that just say `pass`
- [ ] For any failure: record it in the deviations table first, then
      decide — fix-and-continue (config/env mistakes) or
      stop-and-fold-back (code/runbook defects, which return via
      Phase 6)

#### Success Criteria

- All 12 rows of INV-0001's drill-results table filled in, and row 12
  is every node `Ready` — RFC-0001's Phase 1 success criterion,
  demonstrated.
- `secrets.yaml` backed up; `talosconfig`/`kubeconfig` written 0600
  and never overwritten by any re-run.

---

### Phase 6: Convergence pass and fold-back

The property the Hoomlab service will later rely on: taking ownership
converges on no-op. Then every deviation becomes a durable fix.

#### Tasks

- [ ] Re-run every stage in order against the live cluster
      (`pve form`, `pve certs`, `talos secrets`, `talos emit`,
      `talos ipxe`, `talos vms`, `talos bootstrap`, `talos health`)
      and fill INV-0001's convergence table with the applied count per
      stage
- [ ] For any stage that applied something: diagnose the re-fired
      step, fix the Check (with a regression test reproducing the
      false-pending), and re-run that stage to no-op
- [ ] Fold back every deviation recorded in Phase 5: code fix with
      regression test, runbook fix, or a documented
      accepted-behavior note — no deviation left unresolved
- [ ] Re-image test (the runbook's claim): wipe one worker's disk,
      reboot, confirm it PXE-boots and rejoins; note the observed
      behavior in INV-0001
- [ ] If any fold-back changed stage behavior: re-run the affected
      stages on the lab and confirm they still pass and still converge

#### Success Criteria

- Every row of the convergence table reads `0` (or the documented
  equivalent), with any exception fixed and re-verified — not
  explained away.
- The deviations table has a resolution in every row; each code fix
  carries a test that fails without it.
- All bootstrap gates stay green (`just bootstrap-lint` /
  `bootstrap-test` / `bootstrap-build`, `govulncheck`).

---

### Phase 7: Release and closure

The drill passed; make it official and close the paper trail.

#### Tasks

- [ ] Merge the fold-back branch(es) to `main` with `dont-release`
      (tools stay off the service release train)
- [ ] Dispatch `tools-release.yml` with tool=`bootstrap`,
      version=`v0.1.0` — cuts tag `tools/bootstrap/v0.1.0` and
      publishes the binary archives; verify
      `go install github.com/donaldgifford/hoomlab/tools/bootstrap@v0.1.0`
      resolves
- [ ] If OQ-5 = a: flip `acme.directory` to production (remove the
      staging line) and re-run `bootstrap pve certs` on the live
      cluster; every node gets a production certificate
- [ ] Complete INV-0001: Conclusion answered, Environment table filled,
      status → **Concluded**
- [ ] Check off IMPL-0001's six remaining tasks, status → **Completed**
- [ ] DESIGN-0001 status → **Implemented**
- [ ] Update the runbook's `[unverified]` markers to reflect what the
      drill verified; `docz update` for the index tables
- [ ] This doc: all boxes checked, status → **Completed**

#### Success Criteria

- `tools/bootstrap/v0.1.0` exists as a tag and a GitHub release with
  binary archives, and `go install …@v0.1.0` works.
- The doc chain is consistent: RFC-0001 Phase 1 criterion demonstrated,
  IMPL-0001 Completed, DESIGN-0001 Implemented, INV-0001 Concluded,
  runbook markers updated, no stale `[unverified]` on anything the
  drill covered.

## Testing Plan

This IMPL *is* a testing plan — the phases are the tests. What keeps
CI honest alongside them:

- Every drill-driven code fix lands with a regression test that fails
  without the fix (the Phase 6 fold-back rule), keeping `tools-ci.yml`
  the gate for the code half of every finding.
- mockpve discrepancies found in Phase 2 are fixed in the *seeding*,
  so the unit suite's model of Proxmox tightens with each finding.
- The rehearsal and drill themselves stay deliberately out of CI
  (IMPL-0001's testing plan already decided the e2e drill is not a
  merge gate); their record is INV-0001.

## Dependencies

- `proxmox-go-sdk` — pvelab for the nested lab; possibly a new release
  carrying the provision-only mode (OQ-1).
- A physical PVE-capable outer host for Phases 2–3 (OQ-2), and the
  full homelab hardware for Phases 4–6.
- Cloudflare-managed DNS zone for cert FQDNs; Let's Encrypt staging
  and production.
- The booty host: a Linux machine with docker on the boot segment.
- `tools-release.yml` (already merged) for Phase 7.

## Open Questions

Answer each with the letter of the chosen option (**a** is my
recommendation), or **other** with your own. Decisions get recorded
inline, IMPL-0001 style.

**OQ-1 — pvelab always forms the cluster; the rehearsal needs unformed
nodes. How do we get them?** `pvelab up` unconditionally calls
`lab.FormCluster` after provisioning, but Phase 2's whole point is
letting `bootstrap pve form` do the forming against fresh nodes.

- **a (recommended):** Upstream a provision-only mode to pvelab
  (`up --no-form` or equivalent) in proxmox-go-sdk. The lab package
  already separates provisioning (`provision.go`) from formation
  (`cluster.go`), so the change is small; hoomlab consumes it at a
  released version, per the existing scope rule. Bonus: the flag is
  exactly what any future consumer rehearsing cluster formation needs.
- b: Provision with pvelab as-is (formed), then rehearse only the
  *convergence* half of `pve form` — re-run against the already-formed
  cluster and confirm no-op. The create/join path stays untested until
  the drill.
- c: pvelab up, then manually unform the nested cluster
  (`pvecm`-level surgery on each node) before running `pve form`.
  Fragile, and tests a state no real operator starts from.
- d: Skip pvelab; hand-provision three nested PVE VMs for the
  rehearsal. Loses the teardown/re-up automation, which is the
  rehearsal's inner loop.

**OQ-2 — Where does the nested lab run?** pvelab needs an outer
physical PVE host, but the drill needs every homelab node freshly
installed.

- **a (recommended):** Install PVE on one homelab node early and use
  it as the interim outer host for Phases 2–3, then re-install it
  fresh in Phase 4 before the drill. No extra hardware, and the
  re-install cost is one node.
- b: Use a separate always-on machine (existing server, spare box)
  as a permanent lab host, keeping the drill nodes untouched until
  Phase 4. Cleaner separation if such a box exists.
- c: Skip the nested rehearsal entirely; accept that first contact
  with a real PVE API happens during the drill. Cheapest now, most
  expensive per defect found later.

**OQ-3 — Rehearse `pve certs` nested, or defer certificates to the
drill?** DNS-01 doesn't care that the nodes are nested — it needs A
records for the nested FQDNs and a Cloudflare token; pvelab even ships
an ACME variant config (`pvelab-acme.example.yaml`) for exactly this.

- **a (recommended):** Rehearse nested against LE staging. The certs
  stage is mockpve-only today (ACME account/plugin/domain-slot/order
  handling), and DNS-01 timing was called out in INV-0001 as a likely
  failure mode — cheap to test nested, annoying to debug on drill day.
- b: Defer to the drill. Saves setting up lab DNS records, spends
  drill time on first-contact ACME debugging instead.

**OQ-4 — Rehearse the PXE handshake nested (Phase 3), or accept it as
drill risk?** The chain — proxyDHCP beside the real DHCP, TFTP,
`ipxe.efi`, chain script, machineconfig fetch — is the least-tested
seam and one of INV-0001's top expected failure modes.

- **a (recommended):** Rehearse it nested on the outer host with a
  UEFI test VM (Phase 3 as written). The whole handshake is
  virtualizable, and it's the one place a defect would otherwise cost
  repeated physical reboot cycles on drill day.
- b: Rehearse only booty's HTTP surface (already done in INV-0001
  pre-drill) and take the DHCP/TFTP/chainload path cold in the drill.
  Drops Phase 3 entirely.
- c: Partial: test only TFTP + chainload on an isolated bridge where
  booty is the sole DHCP answer. Covers the binary but not the
  proxyDHCP-coexistence claim.

**OQ-5 — Are production certificates in scope for Phase 7?** The
runbook says: staging for the drill, re-run against production once
proven.

- **a (recommended):** Yes — the production `pve certs` re-run is a
  Phase 7 task. It's one command, it's the actual end state the
  homelab wants, and it proves the staging→production flip works
  while the paper trail is still open.
- b: No — close this IMPL at the staging-proven point and treat
  production certs as routine operations outside the drill's record.

## References

- [INV-0001](../investigation/0001-bootstrap-cli-hardware-acceptance-drill.md)
  — the investigation this implements; holds the result tables
- [Runbook: bare Proxmox nodes → healthy Talos
  cluster](../runbook/bootstrap-cluster.md) — the procedure Phases 5–6
  follow
- [IMPL-0001](0001-bootstrap-cli.md) — the code-complete predecessor
  whose six remaining tasks this closes
- [DESIGN-0001](../design/0001-bootstrap-cli.md) — convergence model
  and stage design
- [ADR-0001](../adr/0001-bootstrap-cli-and-service.md) — the layering
  decision (pvelab as the SDK's reference CLI)
- [RFC-0001](../rfc/0001-hoomlab-a-self-hosted-cloud-for-homelab-environments.md)
  — Phase 1 success criterion
- [proxmox-go-sdk `cmd/pvelab`](https://github.com/donaldgifford/proxmox-go-sdk)
  — the nested-lab harness (`iso` / `up` / `down` / `status` /
  `template`), plus `pvelab-acme.example.yaml` for the OQ-3 variant
- [`tools-release.yml`](../../.github/workflows/tools-release.yml) —
  the Phase 7 release mechanism (workflow_dispatch, tag
  `tools/bootstrap/vX.Y.Z`)
