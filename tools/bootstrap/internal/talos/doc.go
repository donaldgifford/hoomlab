// Package talos implements the Talos side of the bootstrap CLI: the
// machinery secrets bundle (secrets.go), machineconfig template
// generation for booty's overlay (machineconfig.go), and cluster
// bring-up — the narrow Client interface over the Talos API
// (client.go, mocks in mocks/), the etcd bootstrap and credential
// stage (bootstrap.go), and the health wait. booty deliberately ships
// secret-less templates; this package covers that gap with the Talos
// machinery libraries (DESIGN-0001 OQ-1).
package talos
