---
id: INV-0007
title: "Archiving the hoomlab repository and migrating it into homelab as a private project"
status: Concluded
author: Donald Gifford
created: 2026-11-02
---

<!-- markdownlint-disable-file MD025 MD041 -->

# INV-0007: Archiving the hoomlab repository and migrating it into homelab as a private project

<!--toc:start-->
<!-- markdownlint-capture -->
<!-- markdownlint-disable MD013 -->
- [Question](#question)
- [Hypothesis](#hypothesis)
- [Context](#context)
- [Approach](#approach)
- [Environment](#environment)
- [Findings](#findings)
  - [Observation 1: the private variant removes INV-0006's first ground](#observation-1-the-private-variant-removes-inv-0006s-first-ground)
  - [Observation 2: the public audience is zero](#observation-2-the-public-audience-is-zero)
  - [Observation 3: what the repository ships is a CLI, not the lockstep train](#observation-3-what-the-repository-ships-is-a-cli-not-the-lockstep-train)
  - [Observation 4: the public-hygiene rule is already breached, and private dissolves it](#observation-4-the-public-hygiene-rule-is-already-breached-and-private-dissolves-it)
  - [Observation 5: the migration, step by step](#observation-5-the-migration-step-by-step)
  - [Observation 6: one new coupling, the cluster that builds its own rebuild tool](#observation-6-one-new-coupling-the-cluster-that-builds-its-own-rebuild-tool)
  - [Observation 7: what is lost, weighed](#observation-7-what-is-lost-weighed)
  - [Observation 8: what is gained, weighed](#observation-8-what-is-gained-weighed)
  - [Observation 9: reversibility and alternatives](#observation-9-reversibility-and-alternatives)
- [Conclusion](#conclusion)
- [Recommendation](#recommendation)
  - [Open questions for the homelab IMPL](#open-questions-for-the-homelab-impl)
  - [Regardless of the decision](#regardless-of-the-decision)
- [References](#references)
<!-- markdownlint-restore -->
<!--toc:end-->

<!--docz:question:start-->
## Question

If the public `donaldgifford/hoomlab` repository were archived and its
code, docs and history moved into the private homelab monorepo as a
`go/hoomlab` project, what would be lost, what would be gained, and
what would the migration take? Does the *private* variant change the
answer [INV-0006](0006-platform-repo-reuse-and-moving-hoomlab-into-the-homelab-monorepo.md)
gave to "should hoomlab move into homelab"?

<!--docz:question:end-->

<!--docz:hypothesis:start-->
## Hypothesis

INV-0006 answered "no, not now" on three grounds: homelab's own
documents exclude public repositories; neither monorepo can build an
image or publish a chart; and the one precedent for wanting homelab's
build (platform-repo) copied the shape rather than merging. It
weighed the move as "hoomlab becomes private" being a *cost*.

The expectation here is that treating "private" as the premise rather
than a cost removes the first ground outright (homelab has absorbed a
public repository before) and forces the other two to be re-weighed
against what hoomlab actually ships today. If the public audience is
hypothetical and the image-and-chart train carries nothing real yet,
the answer flips to "viable, once hoomlab decides it is not a public
product for now", with the migration being homelab's existing import
playbook plus a handful of hoomlab-specific steps.

<!--docz:hypothesis:end-->

<!--docz:context:start-->
## Context

**Triggered by:** INV-0006 Observations 7–9 and Recommendation a, and
the operator's follow-up question: what if the repository is simply
archived and the project goes private inside homelab?

INV-0006 was asked two questions and answered the second ("move into
homelab?") under the assumption that hoomlab stays public, which made
every monorepo argument fight the public-repository policy in homelab's
ADR-0062, DESIGN-0013 and DESIGN-0016, and IMPL-0013's hold on
"the public ones (hoomlab, boop, repo-guardian and others)". This
investigation drops that assumption and asks the narrower question.

Two standing facts shape it:

- hoomlab is public and the standing rule is that no site detail
  (internal addresses, PVE host names, cluster names, MACs, email
  addresses) appears in it; every PR diff is scanned before pushing.
- homelab is private, models hoomlab as the external owner of the PVE
  layer and the Talos machine config (its DESIGN-0028 and DESIGN-0029),
  supplies hoomlab's inputs (host state from Ansible, booty, vault
  items) and consumes its outputs (the `runtime.hoomlab.dev/kata` node
  label in its RuntimeClass manifest).

<!--docz:context:end-->

<!--docz:approach:start-->
## Approach

1. Measure the public audience with the GitHub API: stars, forks,
   watchers, traffic, releases, packages, and a code search for
   external importers of the module paths. Check what the Go module
   mirror has cached.
2. Measure what the repository ships: line counts for the service and
   the bootstrap CLI, the release history of each, and what homelab
   actually pulls from hoomlab today (image, chart, release archive,
   or nothing).
3. Audit the public-hygiene rule: count site names in tracked files
   and across all history (counts and paths only; no values copied
   here), and find where the gitignored site runbooks would live in
   homelab.
4. Walk homelab's import playbook (DESIGN-0016 D3–D14) against hoomlab
   step by step: Go version and module-graph overlap, history size,
   tags, import-path references, nested module, workflows to delete,
   docz ID collisions, release path, and the retirement procedure.
5. Enumerate what is lost (CodeQL, free runners, the lockstep train,
   SLSA/cosign on GitHub OIDC, public `go install`) and what is gained
   (hygiene, atomic cross-repo changes, Bazel CI, tracked drill state,
   direct doc links), and the one new coupling: the tool that rebuilds
   the cluster would be built by CI that runs on the cluster.
6. Assess reversibility and list the alternatives, then answer.

<!--docz:approach:end-->

<!--docz:environment:start-->
## Environment

| Component | Version / Value |
| --------- | --------------- |
| hoomlab | branch `docs/inv-0006-platform-repo-reuse` at `4f5c198` (main at `5884c38`), 2026-11-02 |
| homelab | `main` at `040c6214`, 2026-11-02; read-only; cited by document ID, title and tool name only |
| GitHub data | REST API and `gh` on 2026-11-02; traffic figures are the 14-day window |
| Go | 1.26.10 in hoomlab's root module, `tools/bootstrap`, and homelab's `go/go.mod` |
| Module graphs | `go list -m -json all` in `tools/bootstrap` and in homelab's `go/` |
| docz | 2.6.1 |

<!--docz:environment:end-->

<!--docz:findings:start-->
## Findings

### Observation 1: the private variant removes INV-0006's first ground

INV-0006 Observation 7 quoted homelab's Non-Goals: public
repositories keep GitHub-hosted runners (ADR-0062, DESIGN-0013) and
"stay where they are; they are external dependencies" (DESIGN-0016).
Those sentences are about repositories that *remain* public. The one
public repository homelab has absorbed, keycloak-cli, stopped being
public on import, and homelab's INV-0014 F2 recorded why that was fine:
"It is public, and ADR-0063 is about private repositories. Nothing in
homelab depends on that difference." ADR-0063's "private by
construction" is then a description of the result, not a bar.

What remains of the policy is IMPL-0013 Phase 7's hold, decided
2026-10-25: "Not migrating now. These repositories, and the public ones
in the wider scope (hoomlab, boop, repo-guardian and others), wait for
a decision on how public repositories are handled, if they move at
all." That decision is hoomlab's to make about itself: whether it is a
public product today. Once it decides it is not, homelab's playbook
applies to it as it applied to keycloak-cli. The hold also parks the
private wave-2 repositories, so a hoomlab import would be a new
homelab IMPL rather than a resumed phase.

### Observation 2: the public audience is zero

| Measure (2026-11-02) | Value |
| --- | --- |
| Created / licence | 2026-08-15 / Apache-2.0 |
| Stars / forks / watchers | 0 / 0 / 0 |
| Views, 14 days | 7 (2 unique) |
| Clones, 14 days | 812 (145 unique); CI, Renovate and the docz server clone it, so this is not readers |
| External importers (GitHub code search for the module path) | none; only hoomlab itself |
| Releases | bootstrap v0.1.0–v0.6.0 (9 tags, binary archives only); root v0.0.1, v0.0.2 (image, chart, SBOM, cosign, SLSA) |
| ghcr package | `hoomlab`, public, two stub versions |
| Go module mirror | all 9 bootstrap versions and both root versions cached; the mirror retains what it has fetched |
| Issues / discussions / wiki | enabled; none used |
| GitHub Pages | none; `docs/hoomlab-guide.html` is committed, not published |

RFC-0001 frames hoomlab as a product ("a self-hosted, cloud-like SaaS
for homelab environments … the opinions are the product") and accepts
that "opinionation narrows the audience". Today the audience is the
operator. Being public is a bet on a future audience, not a present
constraint, and nothing measurable is lost by taking the bet later.

### Observation 3: what the repository ships is a CLI, not the lockstep train

| Part | Size | Releases | Shape |
| --- | --- | --- | --- |
| `tools/bootstrap` (nested module) | 14,882 lines of Go, 169 modules in its graph | 9 tags, `tools-release.yml` on demand | plain `go build` linux/darwin archives and a sha256 checksums file; no SBOM, no signature, no image, no chart |
| `cmd/hoomlab` + `internal/` (root module) | 27 lines; `internal/` is empty | v0.0.1, v0.0.2 on merge to `main` | binary + multi-arch image + chart at one tag, cosign, SLSA |

The one capability INV-0006 said neither monorepo could replicate, the
lockstep binary + image + chart release, currently ships a stub
service. The product is the bootstrap CLI, and its shape is exactly
homelab's DESIGN-0017 D6 path: a `<p>/vX.Y.Z` tag from a PR label,
goreleaser archives, GPG-signed checksums, SBOM, and a GitHub release
on homelab (working today for fwsync and champs).

homelab consumes none of hoomlab's artefacts. A search of homelab
outside its docs finds no pull of the ghcr image, no chart install,
and no release download; the references are Ansible comments that
describe hoomlab's layer, a champs e2e fixture that names a test
organisation `hoomlab`, and the RuntimeClass manifest that selects on
the node label. The operator runs bootstrap from a local build via
`op run`. INV-0006's "homelab consumes its releases exactly as it
consumes docz, boop and repo-guardian" is prospective.

When the service does exist, its deployment target is the cluster
homelab's GitOps manages. DESIGN-0017 D5's first path deploys a chart
**by Git path** with no publishing at all; D2 (rules_oci to the
in-cluster registry) is the one unbuilt piece, and IMPL-0013 Phase 5
marked it "won't do" for the current waves. That is a real
dependency for the service phase, but it is the same dependency the
service would have *anywhere*: an image has to reach the cluster's
registry, and homelab is the project that owns that path.

### Observation 4: the public-hygiene rule is already breached, and private dissolves it

Counting the restricted names (two PVE host names, the bootstrap host
name, the two cluster names) without copying them:

| Where | Count |
| --- | --- |
| Tracked files containing at least one | 16: 9 docs (IMPL-0002, IMPL-0003, INV-0001–0003, DESIGN-0002/0004, two runbooks) and 7 under `tools/bootstrap` (two `examples/`, five `_test.go` files in `config`, `emit`, `pve`, `talos`) |
| Occurrences across all history (`git log -p --all`) | 199 |
| `.gitignore` | the comment on the site-runbook ignore rule names the fleet |

None of these are secrets; they are the site's names, and the rule
exists because the owner wants them out of a public repository. Three
consequences:

- **Staying public means a cleanup, not a scan.** Renaming the seven
  fixtures and examples to neutral names is small. Removing the names
  from history needs a history rewrite and a force-push, which is
  feasible (no forks, no external clones of note) but cannot reach
  the Go module mirror: every tagged bootstrap version's zip includes
  `examples/` and the test files, and the mirror keeps them.
- **Archiving the public repository as-is freezes the 199 hits in
  public view permanently.** If the project goes private, the order
  is make-private first, then archive (GitHub allows archiving a
  private repository), so the history leaves public view. The mirror
  copies persist either way.
- **In homelab the rule disappears.** The two gitignored site runbooks
  (`fleet-nic-map`, `network-requirements`) already have tracked
  siblings in homelab's `ansible/docs/fleet/` that cite hoomlab's
  runbooks by name; the drill state under homelab's gitignored
  `hoomlab/drill` directory becomes tracked; and the docs that today
  cite homelab "by ID and title only" can link and name things.

### Observation 5: the migration, step by step

DESIGN-0016 D8's fifteen-step playbook, with what is specific to
hoomlab:

| # | Step | hoomlab specifics |
| --- | --- | --- |
| 1 | Freeze; last standalone release (D14) | IMPL-0004 is Paused; two local branches await their PRs. Cut bootstrap v0.7.0 (or v0.6.x) after they land so the old module path has a final version |
| 2 | Catch-up to homelab's versions (D3) | Go matches (1.26.10). Of bootstrap's 169 modules, 43 overlap homelab's graph; 12 differ and every one is a raise: gRPC 1.67.3 → 1.83.2, protobuf 1.36.3 → 1.36.12, testify 1.11.1 → 1.12.1 and nine transitive. No homelab Go project imports gRPC directly, so the raise is transitive. 126 modules are new to `go/go.mod` (Talos machinery, cosi, proxmox-go-sdk, booty, hclkit) |
| 3 | Secret scan (D7) | hoomlab already runs trufflehog over full history on every PR and push to `main`; no known findings. The site names in Observation 4 are not detector hits |
| 4 | Filter and merge (D6) | 271 commits, 919 KiB pack, 11 tags. `--tag-rename '':'hoomlab/'` yields `hoomlab/tools/bootstrap/v0.6.0` and `hoomlab/v0.0.2`; neither continues the bootstrap line, so the next release's name is a decision (Open Question 3) |
| 5 | Import-path rewrite (D4); delete `go.mod`s | 74 references to `github.com/donaldgifford/hoomlab/...`; `tools/bootstrap/go.mod` folds into `go/go.mod` (D5); `tools/bootstrap/main.go` becomes a `cmd/` under the project (Open Question 1) |
| 6 | Plain `go test` | unit tier; the bootstrap suite uses an in-process mock PVE and Talos machinery, no network |
| 7–8 | Gazelle and sandbox fixes | unknown until tried; keycloak-cli brought 61 shared-lint findings. hoomlab's `.golangci.yml` and homelab's are of similar size (118 and 126 configured lines) but not the same set |
| 9 | Generated-code checks (D10) | `helm-docs` for the chart README is the only generate step today |
| 10 | Integration tiers (DESIGN-0015 D4) | the chart's kind/k3d install test from `ci.yml`, and the nested-PVE drill of DESIGN-0003 stays `manual` |
| 11 | Tooling cleanup (D11) | twelve workflows deleted (`ci`, `tools-ci`, `release`, `tools-release`, `codeql`, `security`, `trufflehog`, `license-check`, `changelog`, `changelog-regen`, `pr-labels`, `dependabot-severity-label`), goreleaser config, `docker-bake.hcl`, Dockerfile, `mise.toml`, root justfile recipes |
| 12 | Release path (DESIGN-0017) | D6 covers the CLI as-is. The chart moves to `go/hoomlab/charts/hoomlab`; D11 says "helm-unittest runs as a step" but no helm step exists in the pipeline tooling today, so that step is new work |
| 13 | Path check | as the playbook |
| 14 | Merge import PR, push tags | as the playbook |
| 15 | Retire (D14) | README pointer, then archive. Observation 4 adds: make private before archiving |

**Docs.** Every hoomlab ID collides with homelab's root series:

| Type | hoomlab | homelab |
| --- | --- | --- |
| RFC | 1 | 9 |
| ADR | 1 | 65 |
| DESIGN | 6 | 30 |
| IMPL | 4 | 17 |
| INV | 7 | 23 |
| Runbook | 2 tracked | 19 |

Two precedents in homelab: renumber into the root series (pve-ansible)
or keep a per-project `.docz.yaml` under `go/hoomlab/docs`
(sdk-booty-sh, which INV-0014 F4 disliked). Either way the
cross-references are rewritten on both sides: hoomlab's docs cite each
other densely, and homelab's DESIGN-0028/0029 and runbooks cite
hoomlab's IDs. The docz server's ingest table (homelab IMPL-0010)
lists `donaldgifford/hoomlab` as a separate source with 11 documents;
that entry goes away.

### Observation 6: one new coupling, the cluster that builds its own rebuild tool

Today bootstrap's CI and releases run on GitHub-hosted runners,
independent of the cluster. After the move, homelab's Buildkite agents
run on the cluster bootstrap builds (DESIGN-0013), so **CI cannot cut
a bootstrap release while the cluster is down.** This is the
chicken-and-egg problem of INV-0006 Observation 4 in a new place.

It is bounded. The tool is a static Go binary: `go build` from any
clone produces it, the operator already runs it that way, and the
GitHub release on homelab (D6) is downloadable from anywhere with a
token. The rule to write down is the one ADR-0001 implies already: a
tagged, downloaded bootstrap release exists before any
cluster-breaking change, and the recovery runbook names the local
build path first. Nothing in this coupling needs the cluster to be up.

*(2026-11-02: accepted by the operator. Managing the cluster it runs on
and builds on is part of the design; the starting point is always a
local build, as it has to be.)*

### Observation 7: what is lost, weighed

| Lost | Weight today | Why |
| --- | --- | --- |
| Public source, issues, `go install` for others | none | Observation 2: no audience, no importers |
| CodeQL | small | private repositories need Advanced Security; homelab runs govulncheck, trivy, nogo and golangci-lint instead |
| Unlimited free GitHub-hosted minutes | none | CI moves to Buildkite; only matters for the private-standalone alternative |
| Lockstep binary + image + chart train | none now, real later | ships a stub (Observation 3); the service phase needs DESIGN-0017 D2 built |
| SLSA provenance and keyless cosign bound to GitHub OIDC | small | DESIGN-0017 D8 moves signing to GPG plus cosign on Buildkite's OIDC, with two things still to verify |
| On-demand `tools-release.yml` dispatch | none | label-driven releases replace it |
| Independent review cadence and labels (`dont-release`, semver labels) | small | homelab's PR gates and release-label skill apply; INV-0006 noted `--latest=false` |
| Dependabot | none | Renovate; Dependabot alerts also work on private repositories |
| hoomlab's own docz numbering and index | small | Observation 5 |

### Observation 8: what is gained, weighed

| Gained | Weight | Evidence |
| --- | --- | --- |
| The public-hygiene rule and its per-PR scan go away; site runbooks and drill state are tracked | large | Observation 4; the rule is already breached in 16 files |
| Atomic changes with homelab's manifests, Ansible inputs and booty config | large | IMPL-0004 Phase 5: the RuntimeClass went through a second agent, a separate PR and a hand-apply before Argo adopted it |
| Bazel CI with remote cache and no per-project step code; one Renovate stream; one lint config | medium | INV-0006 Observation 8 |
| A working release path for the CLI | medium | DESIGN-0017 D6, in use |
| Docs that can link homelab's docs and name the site | medium | INV-0006 had to cite "by ID and title only" |
| Drops the fiction that homelab consumes hoomlab's releases | small | Observation 3: it consumes nothing today |

### Observation 9: reversibility and alternatives

- **Going private** is reversible in content: a later public release
  is a fresh repository or the subtree-split/Copybara mirror IMPL-0013
  noted and never built. The renumbered docs and rewritten import path
  make a later split a second rewrite. The mirror's copies of the
  public versions persist regardless.
- **Archiving** is reversible; **making the repository private** is
  reversible.
- **The decision that is hard to reverse** is the statement "hoomlab
  is not a public product for now", because RFC-0001 is written as
  one. That is why it needs an ADR, not only an IMPL.

Alternatives considered:

| | Alternative | What it fixes | What it leaves |
| --- | --- | --- | --- |
| A | Status quo (INV-0006 a) plus an in-place cleanup: rename fixtures, rewrite history, keep the scan | the breach | the per-PR rule, cross-repo friction, two CI systems, a train for a stub |
| B | Make the repository private but keep it standalone on GitHub Actions | the breach and the rule | cross-repo friction; the free plan's 2,000 minutes a month against a kind + multi-arch-bake + CodeQL CI; CodeQL needs Advanced Security; a stepping stone if homelab does not want imports now |
| C | Archive and migrate (this investigation) | the breach, the rule, the friction, CI | the service phase depends on DESIGN-0017 D2; Observation 6's coupling; the docs rewrite |
| D | Copy homelab's Bazel shape into hoomlab and stay public (INV-0006 b) | CI | everything else; INV-0006 found no benefit at hoomlab's size |

<!--docz:findings:end-->

<!--docz:conclusion:start-->
## Conclusion

**Answer:** Yes. Archiving the public repository and migrating hoomlab
into homelab as a private project is viable and, on today's evidence,
net positive, and it changes INV-0006's second answer from "no, not
now" to "yes, once hoomlab records that it is not a public product for
now".

INV-0006's two load-bearing objections do not apply to the private
variant. The public-repository policy in homelab's documents is about
repositories that stay public; homelab has already absorbed one that
did not (Observation 1). The missing image-and-chart path protects a
27-line stub; the product that actually ships is a CLI whose release
shape homelab already runs (Observation 3). What INV-0006 did not
weigh is that the public audience is zero (Observation 2) and that the
public-hygiene rule is a standing cost that the repository already
fails to meet (Observation 4).

The migration is homelab's existing fifteen-step playbook with modest
hoomlab-specific work: a version catch-up that is all raises, a 74-site
import-path rewrite, a nested-module fold, twelve workflows deleted, a
new helm-unittest pipeline step, and a docs renumbering on both sides
(Observation 5). The one new risk, CI for the rebuild tool living on
the cluster it rebuilds, is bounded by the tool being a static binary
with a release kept outside the cluster (Observation 6).

The decision this turns on is not technical: RFC-0001 describes a
product, and going private says "not yet". That is reversible in
content but should be recorded deliberately (Observation 9).

<!--docz:conclusion:end-->

<!--docz:recommendation:start-->
## Recommendation

- **a. (recommended) Decide, then migrate at a phase boundary.**
  1. Write an ADR in hoomlab: "hoomlab is developed as a private
     project inside homelab; a public release is a separate, later
     decision." It supersedes the "stays public, homelab is a
     consumer" clause of INV-0006 Recommendation a; INV-0006's
     ADR-0001 amendment about the service's write scope still stands.
  2. Land the two open branches, then cut the last standalone
     bootstrap release (D14 step 1).
  3. Open a homelab IMPL that copies DESIGN-0016 D8 with the hoomlab
     specifics of Observation 5, and resolves the open questions
     below. Do it at an IMPL-0004 phase boundary; after Phase 5 closes
     is natural, since Phases 6–8 touch homelab's manifests anyway.
  4. Retire in this order: README pointer, make private, archive. Keep
     the ghcr package and the old releases; they are harmless and
     D14 says archiving keeps them downloadable.
  5. Add the recovery rule of Observation 6 to the bootstrap runbook.
- b. Make the repository private and keep it standalone (Alternative
  B). Cheapest, removes the hygiene rule today, changes nothing else;
  the right interim step if homelab does not want a new import now.
- c. Stay public and clean up in place (Alternative A): rename the
  seven fixtures, rewrite history, keep the scan. The choice if the
  public product is still the intent in the near term.

### Open questions for the homelab IMPL

1. **Project layout.** a. (recommended) one project `go/hoomlab` with
   `cmd/bootstrap` now and `cmd/hoomlab` when the service exists; one
   release per tag with two archives, if boilermate's release config
   allows more than one binary per project. b. Two projects,
   `go/hoomlab` and `go/hoomlab-bootstrap`, each released on its own.
2. **Docs.** a. (recommended) renumber into homelab's root series and
   rewrite the cross-references on both sides in the import PR.
   b. A per-project `.docz.yaml` under `go/hoomlab/docs`.
3. **Tag line.** a. (recommended) continue bootstrap's line as
   `hoomlab/v0.7.0`. b. Restart at `hoomlab/v0.1.0`.

### Regardless of the decision

- Fix the breach now: rename the seven fixtures and examples under
  `tools/bootstrap` to neutral names (RFC 5737 addresses, generic host
  and cluster names) and reword the `.gitignore` comment. The nine
  docs can wait for the move or the history rewrite, whichever comes.

<!--docz:recommendation:end-->

<!--docz:references:start-->
## References

- hoomlab: [RFC-0001](../rfc/0001-hoomlab-a-self-hosted-cloud-for-homelab-environments.md),
  [ADR-0001](../adr/0001-bootstrap-cli-and-service.md),
  [DESIGN-0003](../design/0003-nested-test-environment-for-the-bootstrap-cli.md),
  [IMPL-0004](../impl/0004-talos-11311-upgrade-with-kata-ci-isolation-and-cluster-options.md)
  (Phase 5), [INV-0006](0006-platform-repo-reuse-and-moving-hoomlab-into-the-homelab-monorepo.md)
  (Observations 4, 7–9; Recommendation a)
- [homelab](https://github.com/donaldgifford/homelab) (private; cited
  by ID and title only): ADR-0062 (Buildkite with self-hosted agents),
  ADR-0063 (Go projects as a single module under `go/`), DESIGN-0013
  (Buildkite agent stack), DESIGN-0015 (generated Buildkite pipeline),
  DESIGN-0016 (Go monorepo layout and repository import playbook:
  D3, D5–D8, D11, D13, D14), DESIGN-0017 (release and deploy path: D2,
  D5, D6, D8), DESIGN-0028 (PVE cluster target state for the hoomlab
  bootstrap), DESIGN-0029 (bootstrap plane), IMPL-0009 (keycloak-cli
  import), IMPL-0010 (docz v2 ingest table), IMPL-0013 (migration
  waves; Phase 5 and Phase 7 "won't do"), INV-0014 (F2, F4)
- GitHub: [archiving repositories](https://docs.github.com/en/repositories/archiving-a-github-repository/archiving-repositories),
  [code scanning availability on private repositories](https://docs.github.com/en/code-security/code-scanning/introduction-to-code-scanning/about-code-scanning),
  [GitHub Actions billing](https://docs.github.com/en/billing/managing-billing-for-your-products/about-billing-for-github-actions)
- Go: [proxy.golang.org retention](https://proxy.golang.org/) (the
  mirror may retain content after it is removed from the origin)

<!--docz:references:end-->
