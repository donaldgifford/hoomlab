// Package emit renders the booty artifact tree the operator serves the
// Talos PXE chain from: the HCL catalog, the secret-bearing
// machineconfig template overlay, the embedded iPXE chain script, a
// ready-to-run launcher, and the Image Factory boot assets
// (DESIGN-0001 Stage 3).
//
// Emission is pure rendering — no API calls, deterministic output — so
// the convergence Check is a byte-diff of a fresh render against the
// tree already on disk. Everything but the boot assets is rendered into
// memory first (see Tree) and only then written, which keeps Check
// honest: it touches neither the network nor the output.
//
// The CLI only writes the tree locally. Moving it to the booty host and
// starting the container are operator steps by design (ADR-0001); the
// emitted booty-run.sh makes the second one copy-paste.
package emit
