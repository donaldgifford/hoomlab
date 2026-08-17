// docker-bake.hcl — multi-arch build pipeline for hoomlab.
//
// Targets:
//   - default: local single-arch build (used by `docker buildx bake`)
//   - ci:      linux/amd64 build + push of `:dev-ci` for PR validation
//   - release: multi-arch build + push to GHCR (CI only, gated on tag)
//
// CI workflow consumes this via docker/bake-action@v6 with the `targets`
// input. The release workflow merges in tag-derived image refs from
// docker/metadata-action's bake-file outputs.

variable "REGISTRY" {
  default = "ghcr.io/donaldgifford/hoomlab"
}

variable "TAG" {
  default = "dev"
}

variable "VERSION" {
  default = "0.0.0-dev"
}

variable "COMMIT" {
  default = "none"
}

variable "DATE" {
  default = "unknown"
}

group "default" {
  targets = ["hoomlab"]
}

group "ci" {
  targets = ["hoomlab-ci"]
}

group "release" {
  targets = ["hoomlab-release"]
}

target "_common" {
  context    = "."
  dockerfile = "Dockerfile"
  args = {
    VERSION = "${VERSION}"
    COMMIT  = "${COMMIT}"
    DATE    = "${DATE}"
  }
  labels = {
    "org.opencontainers.image.source"      = "https://github.com/donaldgifford/hoomlab"
    "org.opencontainers.image.licenses"    = "Apache-2.0"
    "org.opencontainers.image.description" = "Homelab Service"
  }
}

// Stub providing default `tags` for local `docker buildx bake`. CI
// runs override this target via docker/metadata-action's
// bake-file-tags output so the bake pushes the same semver-derived
// image refs the metadata-action emits — which is what cosign then
// signs in the next step. The release target inherits from this and
// does NOT declare tags itself, so the override actually takes
// effect (with HCL inheritance, a child's tags list replaces the
// parent's, not extends it).
target "docker-metadata-action" {
  tags = [
    "${REGISTRY}:${TAG}",
    "${REGISTRY}:latest",
  ]
}

// No platforms pin — local builds target the host platform, so the
// image runs natively in a local k3d cluster on both amd64 and arm64
// hosts (`just k3d-install`).
target "hoomlab" {
  inherits = ["_common"]
  tags     = ["${REGISTRY}:${TAG}"]
}

// CI builds are linux/amd64 only — emulated arm64 builds via QEMU on
// GitHub's ubuntu-latest runners take ~25 min and dominate PR feedback
// time. Multi-arch coverage is restored in the release target, which
// runs only on tag pushes.
target "hoomlab-ci" {
  inherits  = ["_common"]
  tags      = ["${REGISTRY}:${TAG}-ci"]
  platforms = ["linux/amd64"]
}

target "hoomlab-release" {
  inherits = ["_common", "docker-metadata-action"]
  // tags intentionally omitted — they come from docker-metadata-action
  // (defaults for local bake; CI overrides via metadata-action).
  platforms = [
    "linux/amd64",
    "linux/arm64",
  ]
  output = ["type=registry"]
}
