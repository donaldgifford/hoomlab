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

### Bug Fixes

- *(bootstrap)* Emit a booty image tag that exists
- *(bootstrap)* Make the containerized ipxe build actually run
- *(bootstrap)* Install libc6-dev in the ipxe builder

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

### Testing

- *(bootstrap)* Cover pre-existing acme plugins via mockpve seeding
- *(bootstrap)* Cover the slot-allocation and contact-address branches
- *(bootstrap)* Pin the PXE identity binding across the booty boundary

### Miscellaneous Tasks

- *(tools)* Scaffold bootstrap CLI module with gated CI and release
- *(bootstrap)* Add yaml document start to .mockery.yml

