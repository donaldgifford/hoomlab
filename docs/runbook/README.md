# Runbooks

Operator procedures: the steps to actually run something, in order,
with the output you should see at each one.

Runbooks are **not** docz documents and carry no ID or status. An RFC
argues, an ADR decides, a DESIGN specifies, an IMPL tracks — a runbook
is what you have open in a terminal while doing the work.

## What belongs here

- Ordered steps with the exact command and its expected output.
- The prerequisites that must be true before step 1.
- The failure modes you will actually hit, and what they mean.
- Where the boundary is between what the tooling does and what the
  operator does by hand.

Not here: rationale (that is the design doc's job), or task tracking
(that is the IMPL's).

## The rule

**A runbook describes the code as it is, not as it should be.** Its
value is that it can be followed literally, so every command and
output string is taken from the current implementation. When a
procedure turns out to be wrong, fix the code and the runbook in the
same change. Steps that have not been executed against the real thing
say so inline rather than implying a confidence nobody has earned.

## Index

| Runbook | What it covers |
| --- | --- |
| [Bare Proxmox nodes → healthy Talos cluster](bootstrap-cluster.md) | The `bootstrap` CLI end to end: config, PVE formation, certificates, Talos secrets, booty artifacts, VM creation, etcd bootstrap, health |
| [Network requirements — cluster planes](network-requirements.md) | The assertable network contract (Servers + Storage planes): UniFi settings, port profiles, jumbo chain, validation sweeps, fault signatures — feed for the homelab Terraform modules |
| [Fleet NIC map](fleet-nic-map.md) | Per-host NICs, MACs, switch ports, bridges, and addresses as they are live |
