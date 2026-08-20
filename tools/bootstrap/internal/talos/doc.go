// Package talos implements the Talos side of the bootstrap CLI: the
// machinery secrets bundle (secrets.go), machineconfig template
// generation for booty's overlay (machineconfig.go), and — in a later
// phase — etcd bootstrap and cluster health. booty deliberately ships
// secret-less templates; this package covers that gap with the Talos
// machinery libraries (DESIGN-0001 OQ-1).
package talos
