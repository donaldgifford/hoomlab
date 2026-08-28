# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
this project adheres to [Semantic Versioning](https://semver.org/).
## [unreleased]

### Features

- *(bootstrap)* Add HCL config schema structs (IMPL-0001 P1.1)
- *(bootstrap)* Add env(), config loader, and validators (IMPL-0001 P1.2-P1.4)
- *(bootstrap)* Add validate command, example config, and config tests (IMPL-0001 P1.5-P1.8)
- *(bootstrap)* Add step engine and pve form (IMPL-0001 P2.1-P2.7)
- *(bootstrap)* Add pve certs stage (IMPL-0001 P3.1-P3.7)
- *(bootstrap)* Add talos secrets stage (IMPL-0001 P4.1-P4.2)
- *(bootstrap)* Add talos machineconfig template generation (IMPL-0001 P4.4-P4.5)
- *(bootstrap)* Add talos emit stage (IMPL-0001 P4.3-P4.11)
- *(bootstrap)* Add talos ipxe and talos vms stages (IMPL-0001 P5.1-P5.6)
- *(bootstrap)* Add talos bootstrap and health stages (IMPL-0001 P6.1-P6.5)
- *(bootstrap)* Tag net0 with an optional per-node vlan (OQ-5)
- *(bootstrap)* Pve storage stage — declared cluster storage entries
- *(bootstrap)* Add the cluster completion config surface
- *(bootstrap)* Emit the cluster completion machine-config surface
- *(bootstrap)* Derive boot images from extension profiles
- *(bootstrap)* Pin the current Cilium pairing — v1.20.1 on Gateway API v1.6.1
- *(bootstrap)* Reject profiles no node references

### Bug Fixes

- *(bootstrap)* Emit a booty image tag that exists
- *(bootstrap)* Make the containerized ipxe build actually run
- *(bootstrap)* Install libc6-dev in the ipxe builder
- *(bootstrap)* Create the cluster as root@pam, not the API token
- *(bootstrap)* Register the ACME account as root@pam
- *(bootstrap)* Check ACME plugin existence via the index, not by ID
- *(bootstrap)* Make the certs stage converge CA changes and tolerate PVE's plugin encoding
- *(bootstrap)* Converge existing certificates via renew or replace, not blind reorder
- *(bootstrap)* Decode PVE's unpadded plugin payload; surface drift reasons
- *(bootstrap)* Parse PVE's decoded plugin payload as plaintext
- *(bootstrap)* Resolve storage existence via the index, not by-ID GET
- *(bootstrap)* Resolve vm existence through the node index
- *(bootstrap)* Embed booty's documented dhcp+chain script in ipxe.efi
- *(bootstrap)* Pin the vm scsi controller to virtio-scsi
- *(bootstrap)* Probe etcd membership, not kubeconfig, for bootstrap
- *(bootstrap)* Bound the etcd membership probe with a deadline
- *(bootstrap)* Provision the qemu guest agent channel on talos vms

### Refactor

- *(bootstrap)* Return steps.Result from Runner.Run
- *(bootstrap)* Simplify two-item loops from the phase 4 review
- *(bootstrap)* Address the phase 5-6 review

### Documentation

- Add RFC-0001, ADR-0001, and DESIGN-0001 for the bootstrap CLI
- *(bootstrap)* Update package docs for the vms and ipxe stages
- *(bootstrap)* Add the acceptance drill appendix to the runbook
- *(impl)* Record IMPL-0001 status — code complete, drill outstanding
- Add the operator runbook and INV-0001 for the hardware drill
- *(impl)* Add IMPL-0002 — the hardware acceptance drill plan
- *(impl)* Rewrite IMPL-0002 around the pragmatic real-hosts path
- *(impl)* Record the IMPL-0002 decisions — all four OQs decided a
- *(impl)* Fold the verified node snapshot into IMPL-0002 phase 1
- *(runbook)* Note the manual link1 procedure and file the SDK follow-up
- *(impl)* Check off the /etc/pve backups in IMPL-0002 phase 1
- *(impl)* Check off the primary API token in IMPL-0002 phase 1
- *(impl)* Check off DNS records and the Cloudflare token in IMPL-0002
- *(impl)* Check off the drill config in IMPL-0002 phase 1
- *(impl)* Defer the Talos boot-network prep from phase 1 to phase 3
- *(impl)* Close IMPL-0002 phase 1 — gate is GO
- *(inv)* Record the first two drill deviations
- *(impl)* Record the single-node formation pass
- *(inv)* Record deviation 3 — ACME account is root@pam-reserved
- *(inv)* Record deviation 4 — plugin GET-on-missing is a 500, not 404
- *(impl)* Check off the single-node staging certs pass
- *(inv)* Record deviations 5-6 — the CA-blind flip and the plugin re-rotation
- *(inv)* Record deviation 7 and the plugin rotation's open diagnosis
- *(inv)* Close deviation 6's diagnosis — PVE stores unpadded base64
- *(inv)* Correct deviation 6's root cause — PVE returns plaintext
- *(drill)* Record the production flip — single-node certs converged
- *(drill)* Record grow-to-three and the join-wipe truth
- *(drill)* Close Phase 2 — three nodes formed, certified, idempotent
- *(drill)* Decide the storage task — a pve storage stage, SDK-gated
- *(design)* Draft DESIGN-0002 — talos completion scope for bootstrap
- *(drill)* Resolve OQ-5 — vm-trunk has no native VLAN, tag required
- *(drill)* Record Phase 3 — booty serving the emitted tree, verified
- *(inv)* Record deviation 8 — storage GET 500s on missing entries
- *(drill)* Record the storage stage's lab verification
- *(drill)* Close the secrets record; env table onto released SDK
- *(drill)* Storage residual closed — parity re-run applied 0
- *(bootstrap)* Ship the completion example surface and sync the drill record
- *(drill)* Step-7 re-verification with the cilium tree — all pass
- *(bootstrap)* Document the booty-host broadcast route
- *(drill)* Record Phase 4 completion in INV-0001 and IMPL-0002
- *(drill)* Row 12 — every node Ready, Phase 4 complete
- *(drill)* Phase 5 complete — convergence, re-image, close-out, gates
- *(drill)* Conclude INV-0001 and close out the drill's doc chain

### Testing

- *(bootstrap)* Cover pre-existing acme plugins via mockpve seeding
- *(bootstrap)* Cover the slot-allocation and contact-address branches
- *(bootstrap)* Pin the PXE identity binding across the booty boundary

### Miscellaneous Tasks

- *(tools)* Scaffold bootstrap CLI module with gated CI and release
- *(bootstrap)* Add yaml document start to .mockery.yml
- Gitignore docs/examples — prior-cluster reference configs
- *(bootstrap)* Bump proxmox-go-sdk to v0.12.0 — the lab-proven release

