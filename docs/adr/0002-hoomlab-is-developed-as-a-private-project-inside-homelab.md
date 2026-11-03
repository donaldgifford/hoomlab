---
id: ADR-0002
title: "hoomlab is developed as a private project inside homelab"
status: Accepted
author: Donald Gifford
created: 2026-11-02
---

<!-- markdownlint-disable-file MD025 MD041 -->

# ADR-0002: hoomlab is developed as a private project inside homelab

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Summary](#summary)
- [Context](#context)
- [Decision](#decision)
  - [Supporting Data](#supporting-data)
- [Consequences](#consequences)
  - [Positive](#positive)
  - [Negative](#negative)
  - [Neutral](#neutral)
- [Alternatives Considered](#alternatives-considered)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:summary:start-->
## Summary

hoomlab stops being a public, standalone repository. Its code, docs and
history move into the private homelab monorepo as a `go/hoomlab`
project, built and released by homelab's Bazel and Buildkite path, and
the public repository is made private and archived. A public release of
hoomlab is a separate, later decision; nothing here forecloses it.

<!--docz:summary:end-->

<!--docz:context:start-->
## Context

[RFC-0001](../rfc/0001-hoomlab-a-self-hosted-cloud-for-homelab-environments.md)
describes hoomlab as a product for homelab operators, and the
repository has been public since it was created on 2026-08-15, under
a standing rule that no site detail appears in it. Two investigations
examined where the project should live:

- [INV-0006](../investigation/0006-platform-repo-reuse-and-moving-hoomlab-into-the-homelab-monorepo.md)
  asked whether hoomlab should move into homelab *while staying
  public* and answered no: homelab's own documents keep public
  repositories out, neither monorepo can publish an image or a chart,
  and the precedent for wanting homelab's build (platform-repo) copied
  the shape rather than merging.
- [INV-0007](../investigation/0007-archiving-the-hoomlab-repository-and-migrating-it-into-homelab.md)
  dropped the "stays public" assumption and found that it carried the
  argument. The public audience is zero (no stars, forks, watchers or
  external importers). The lockstep image-and-chart train that
  homelab cannot replicate ships a 27-line stub service; the product
  that ships is the bootstrap CLI, whose release shape homelab already
  runs. The site-detail rule is a per-PR cost that the repository
  already fails to meet in 16 tracked files and about 200 places in
  history. homelab's policy is about repositories that remain public;
  it has absorbed a public one before (keycloak-cli).

INV-0007 concluded that archiving and migrating is viable and net
positive once hoomlab records that it is not a public product for now.
That record is this ADR. The one new coupling it found, that CI for the
tool that rebuilds the cluster would run on that cluster, was accepted
by the operator on 2026-11-02: managing the cluster it runs on and
builds on is part of the design, and the starting point is always a
local build.

<!--docz:context:end-->

<!--docz:decision:start-->
## Decision

**hoomlab is developed as a private project inside homelab.** The
repository `donaldgifford/hoomlab` is retired and its contents become
`go/hoomlab` in the homelab monorepo, following homelab's repository
import playbook (its DESIGN-0016 D8) with the hoomlab-specific steps
INV-0007 Observation 5 lists. After the move, the site-detail rule of
the public repository no longer applies; the gitignored site runbooks
and the drill state become tracked files beside their homelab
siblings.

**Order of work.**

1. Land the open hoomlab branches
   ([IMPL-0004](../impl/0004-talos-11311-upgrade-with-kata-ci-isolation-and-cluster-options.md)
   Phase 5 record; this ADR).
2. Cut the last standalone bootstrap release from this repository, so
   the old module path `github.com/donaldgifford/hoomlab/tools/bootstrap`
   has a final version.
3. Open a homelab IMPL for the import, at an IMPL-0004 phase boundary;
   after Phase 5 closes is the natural point, since Phases 6–8 touch
   homelab's manifests anyway. IMPL-0004's remaining phases complete
   in homelab.
4. Retire the public repository in this order: README pointer to
   `go/hoomlab`, make private, archive. The ghcr package and the old
   releases stay; the Go module mirror keeps the public versions
   regardless.
5. Add the recovery rule to the bootstrap runbook: a tagged,
   downloaded bootstrap release exists before any cluster-breaking
   change, and the runbook names the local build path first.

**A public release of hoomlab is a separate decision.** When there is
a product to show, it is a fresh public repository or a mirror split
from homelab; neither is designed here.

**Three questions the homelab IMPL settles**, with the recommended
option first:

1. **Project layout.**
   - a. (recommended) One project `go/hoomlab` with `cmd/bootstrap`
     now and `cmd/hoomlab` when the service exists; one release per
     tag with two archives, if boilermate's release config allows more
     than one binary per project.
   - b. Two projects, `go/hoomlab` and `go/hoomlab-bootstrap`, each
     released on its own.
2. **Docs numbering.**
   - a. (recommended) Renumber hoomlab's documents into homelab's root
     series and rewrite the cross-references on both sides in the
     import PR.
   - b. A per-project `.docz.yaml` under `go/hoomlab/docs`.
3. **Tag line.**
   - a. (recommended) Continue bootstrap's line as `hoomlab/v0.7.0`.
   - b. Restart at `hoomlab/v0.1.0`.

### Supporting Data

From INV-0007, measured 2026-11-02:

| Measure | Value |
| --- | --- |
| Stars / forks / watchers / external importers | 0 / 0 / 0 / 0 |
| Service (`cmd/hoomlab` + `internal/`) | 27 lines |
| Bootstrap CLI (`tools/bootstrap`) | 14,882 lines, 9 releases, binary archives only |
| Artefacts of hoomlab that homelab pulls today | none; the operator runs a local build |
| Tracked files with site names | 16 (7 fixtures and examples, since renamed; 9 docs) |
| Module catch-up to homelab's versions | 12 differences, all raises; 126 modules new to homelab |

<!--docz:decision:end-->

<!--docz:consequences:start-->
## Consequences

<!--docz:positive:start-->
### Positive

- The site-detail rule and its per-PR diff scan end. Site runbooks,
  fixtures and drill state can say what they mean.
- Changes to hoomlab, homelab's manifests, Ansible host inputs and
  booty config land atomically in one PR. IMPL-0004 Phase 5's
  RuntimeClass, which went through a second agent, a separate PR and a
  hand-apply, is the case this removes.
- One CI (Bazel with remote cache, Renovate, shared lint, govulncheck,
  trivy) with no per-project workflow code; a working release path for
  the CLI (homelab's DESIGN-0017 D6).
- hoomlab's docs can link homelab's docs directly instead of citing
  them by ID and title.

<!--docz:positive:end-->

<!--docz:negative:start-->
### Negative

- No public source, issues or `go install` for others until a public
  release is decided. RFC-0001's audience is deferred, not served.
- The lockstep binary + image + chart train is dropped. The service
  phase depends on homelab building its image path (DESIGN-0017 D2),
  which is unimplemented; charts deploy by Git path (D5) without
  publishing.
- CodeQL is lost (private repositories need Advanced Security); SLSA
  provenance and keyless cosign on GitHub OIDC are replaced by
  homelab's DESIGN-0017 D8 signing, which still has two things to
  verify.
- CI for the cluster-rebuild tool runs on the cluster it rebuilds.
  Accepted; bounded by the tool being a static binary with a release
  kept outside the cluster (order of work, step 5).
- Every hoomlab document ID collides with homelab's series; the import
  renumbers and rewrites cross-references on both sides.
- `tools/bootstrap` folds into homelab's single Go module; its
  dependency versions become everyone's by minimum version selection.

<!--docz:negative:end-->

<!--docz:neutral:start-->
### Neutral

- Going private and archiving are both reversible. The Go module
  mirror keeps every public version of the old module path either
  way.
- The ADR-0001 amendment INV-0006 recommended (the service reads the
  cluster it runs on and writes GitOps files; node and VM mutation
  stays in bootstrap) is unaffected and still worth writing.
- INV-0006's reuse list for platform-repo code is unaffected; the
  copies land in `go/hoomlab` instead of `tools/bootstrap`.
- homelab's model of hoomlab as the owner of the PVE layer and the
  Talos machine config does not change; only the repository does.

<!--docz:neutral:end-->
<!--docz:consequences:end-->

<!--docz:alternatives:start-->
## Alternatives Considered

- **Stay public and clean up in place** (INV-0006 Recommendation a;
  INV-0007 Alternative A). Rename the fixtures, rewrite history, keep
  the scan. Rejected: it keeps the per-PR rule, the cross-repo
  friction and two CI systems for an audience that does not exist yet;
  the history rewrite cannot reach the module mirror.
- **Make the repository private but keep it standalone on GitHub
  Actions** (INV-0007 Alternative B). Rejected as the end state: it
  removes the rule and nothing else, and the free plan's minutes are
  thin for a kind, multi-arch bake and CodeQL pipeline. It remains the
  interim step if homelab does not take a new import now.
- **Copy homelab's Bazel and Buildkite shape into hoomlab and stay
  public** (INV-0006 Recommendation b). Rejected: no benefit at
  hoomlab's size, against homelab's stated Non-Goal, and the image and
  chart would still need GitHub Actions.

<!--docz:alternatives:end-->

<!--docz:references:start-->
## References

- [RFC-0001](../rfc/0001-hoomlab-a-self-hosted-cloud-for-homelab-environments.md)
- [ADR-0001](0001-bootstrap-cli-and-service.md)
- [IMPL-0004](../impl/0004-talos-11311-upgrade-with-kata-ci-isolation-and-cluster-options.md)
- [INV-0006](../investigation/0006-platform-repo-reuse-and-moving-hoomlab-into-the-homelab-monorepo.md)
- [INV-0007](../investigation/0007-archiving-the-hoomlab-repository-and-migrating-it-into-homelab.md)
- [homelab](https://github.com/donaldgifford/homelab) (private; cited
  by ID and title only): DESIGN-0016 (Go monorepo layout and
  repository import playbook), DESIGN-0017 (release and deploy path),
  IMPL-0013 (migration waves)

<!--docz:references:end-->
