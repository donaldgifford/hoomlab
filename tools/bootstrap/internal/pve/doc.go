// Package pve implements the Proxmox stages of the bootstrap CLI:
// cluster formation (Stage 1, form.go) and — in a later phase — ACME
// certificates (Stage 2). It builds steps.Step lists from the config
// and talks to Proxmox exclusively through proxmox-go-sdk service
// interfaces, dialed fresh per call because formation restarts the
// node daemons underneath long-lived sessions (the pvelab pattern,
// copied as functional documentation per ADR-0001).
package pve
