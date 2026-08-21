// Package pve implements the Proxmox stages of the bootstrap CLI:
// cluster formation (Stage 1, form.go), ACME certificates (Stage 2,
// certs.go), and Talos VM creation (Stage 4, vms.go). It builds
// steps.Step lists from the config and talks to Proxmox exclusively
// through proxmox-go-sdk services. Formation dials fresh per call
// because it restarts the node daemons underneath long-lived sessions
// (the pvelab pattern, copied as functional documentation per
// ADR-0001); the post-formation stages run on one client against a
// stable cluster.
package pve
