// Package config holds the HCL configuration schema for the bootstrap
// CLI: the gohcl-tagged structs (config.go), the hclkit loader with
// the env() function (load.go), and the semantic validation pass
// (validate.go). The schema stays private to the tool until the
// Hoomlab service consumes the same files (ADR-0001, DESIGN-0001).
package config
