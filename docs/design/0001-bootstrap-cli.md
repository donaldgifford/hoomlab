---
id: DESIGN-0001
title: "Bootstrap CLI"
status: In Review
author: Donald Gifford
created: 2026-08-17
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0001: Bootstrap CLI

**Status:** In Review
**Author:** Donald Gifford
**Date:** 2026-08-17

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
  - [What the primitives provide today](#what-the-primitives-provide-today)
  - [Where their coverage ends](#where-their-coverage-ends)
- [Detailed Design](#detailed-design)
  - [Command tree](#command-tree)
  - [Package layout](#package-layout)
  - [Convergence model](#convergence-model)
  - [Stage 1: Proxmox cluster formation](#stage-1-proxmox-cluster-formation)
  - [Stage 2: Certificates](#stage-2-certificates)
  - [Stage 3: Artifact emission for booty](#stage-3-artifact-emission-for-booty)
  - [Stage 4: VM creation](#stage-4-vm-creation)
  - [Stage 5: Talos cluster bring-up](#stage-5-talos-cluster-bring-up)
  - [Secrets handling](#secrets-handling)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
  - [Configuration schema](#configuration-schema)
  - [Files the CLI writes](#files-the-cli-writes)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

The bootstrap CLI (ADR-0001) takes bare Proxmox nodes to a formed Proxmox
cluster with a Talos Kubernetes cluster running on it, driven entirely by HCL
configuration files. This document designs the tool that lives in
`tools/bootstrap`: its command tree, configuration schema, convergence model,
and the exact seams against `proxmox-go-sdk`, `booty`, `hclkit`, and the Talos
Go libraries — grounded in what those libraries actually export today.

## Goals and Non-Goals

### Goals

- One config file in, a Talos cluster ready for Hoomlab out — through
  operator-run, individually re-runnable stages. The same file is what
  the Hoomlab service eventually consumes to take ownership of the
  cluster (OQ-8).
- Every stage converges: it reads actual state from the APIs, computes the
  delta against the config, applies only the delta, and is safe to re-run
  after any interruption (ADR-0001 "Re-runs must converge").
- Artifact emission as the booty contract: our HCL in, booty's catalog +
  boot assets + machineconfigs out. booty itself is unmodified and runs as
  the operator's container.
- Cover booty's deliberate Talos gap (no cluster secrets in its
  machineconfig templates) with the Talos `machinery` libraries.
- Testable without a lab: `mockpve` for every Proxmox interaction, golden
  files for every emitted artifact.

### Non-Goals

- **No PVE OS install.** "Bare Proxmox nodes" means PVE 9.x installed and
  reachable, not bare metal. (booty's `proxmox-answer` path could automate
  PVE installs later — that would be its own design.)
- **No service component, no database, no state file** (ADR-0001). The
  config directory and the world are the only truths.
- **No unattended end-to-end run.** Stages are separate commands by design;
  the operator drives, inspects, and re-runs.
- **No release-train integration yet** — built and run locally
  (`just bootstrap-build`); tools release path is a planned ADR.
- **No day-2 management.** Scaling, upgrades, and node lifecycle beyond
  bring-up belong to the Hoomlab service (RFC-0001 Phase 2).

## Background

### What the primitives provide today

**proxmox-go-sdk** (`proxmox` package, PVE 9.0+ floor):

- `proxmox.NewClient(ctx, endpoint, creds, opts...)` — creds via
  `api.TokenCredentials` / `api.UserCredentials`; `client.API()` is the
  escape hatch for unmodeled endpoints; `client.Tasks()` awaits UPIDs.
- Cluster formation: `cluster.CreateCluster` (fire-and-poll on the first
  node), `JoinInfo` (member fingerprints), `JoinCluster` — issued **on the
  joining node** with an existing member's hostname, **root@pam password**,
  and fingerprint. `GetStatus` / `ListConfigNodes` observe convergence.
- ACME: `RegisterACMEAccount`, `OrderNodeCertificate` /
  `RenewNodeCertificate` per node, `GetNodeCertificates` for inspection.
- VMs: per-node `qemu.NewService(client, node, caps)`;
  `Create(&CreateSpec{VMID, Name, Memory, Cores, SCSI0, Net0, Boot, Extra})`,
  `Start`, `SetConfig`. PXE boot is `Boot: "order=net0"` plus a pinned
  `macaddr=` in `Net0`.
- `mockpve` — importable in-memory PVE responder (`mockpve.New()`, seed,
  `NewClient()`); `pvelab`'s `lab.FormCluster` demonstrates the full
  formation sequence (create → join-info → serialized joins → member wait →
  quorum wait) and is the functional documentation to copy from.

**booty** (library + container, Talos-first):

- Serves the whole PXE chain: proxyDHCP `:67`/`:4011`, TFTP `:69`
  (`ipxe.efi`), HTTP `:8080` — `/boot.ipxe`, `/ipxe?mac=…`,
  `/boot/{path}` (kernels/initrds), `/machine-config?mac=…` (Talos),
  `/cloud-init/*`, `/proxmox/answer`.
- Input contract: a **catalog directory** of HCL (`variable`/`locals`,
  `profile` blocks with `boot {kernel initrd cmdline}` + `render {kind
  template}` + `vars`, `group` blocks binding machines to profiles via
  `selector = { mac = … }`), a **boot-dir** of staged kernel/initrd assets,
  and optional `--templates-dir` operator template overrides.
- Runs as the operator's container:
  `booty serve --catalog … --boot-dir … --url http://<ip>:8080
  [--proxydhcp --server-ip <ip>] [--templates-dir …]`. Operational facts
  that shape emission: real PXE requires `--net=host` (broadcast DHCP +
  per-transfer TFTP ports don't survive bridge NAT), the nonroot image
  cannot bind 69/67/4011 under host networking (run `--user 0:0` or
  grant unprivileged-port sysctl), the catalog and templates are loaded
  **once at startup** (re-emit ⇒ container restart), and the serve flag
  is `--catalog` (booty's own walkthrough says `--catalog-dir`, which
  does not parse).
- booty imports **zero Talos Go code** — machineconfig serving is pure
  `text/template`. Its documented, worked-end-to-end path to real
  clusters is the **overlay-template pattern**: `talosctl gen secrets` →
  `gen config --with-secrets` → copy the generated configs into
  `--templates-dir` as templates with exactly two edits (hostname var,
  install image var). The bootstrap CLI automates precisely this
  procedure (OQ-1).

**hclkit** (`pkg/hclkit`):

- `hclkit.New(opts...)` → `Loader`; `LoadDir(dir, &target)` decodes into
  gohcl-tagged structs; `WithVarsFile`, `WithVariables`, `WithFunctions`,
  `WithValidators`, `WithMergeMode`; GCC-style `Diagnostics`. The `hclkit`
  CLI (`fmt`/`validate`/`lint`) works on our config files as-is.

### Where their coverage ends

These are the seams this CLI owns (and the source of several open
questions):

1. **Talos cluster secrets.** booty's `talos/*.yaml.tmpl` deliberately omit
   `machine.token`, PKI, and `cluster.id/secret` — its templates note the
   machinery secrets bundle is "the deferred upgrade". Real machineconfigs
   must come from the Talos `machinery` config-generation packages, seeded
   by a secrets bundle this CLI generates and the operator keeps.
2. **ACME DNS plugin registration.** The SDK models ACME accounts and
   per-node certificate ordering, but not `/cluster/acme/plugins` — the
   endpoint where the Cloudflare DNS-01 plugin and its API token are
   configured (OQ-3).
3. **etcd bootstrap and cluster health.** booty ends at "node booted with a
   machineconfig". `talosctl bootstrap`-equivalent (one-time etcd bootstrap
   against the first control plane), kubeconfig/talosconfig retrieval, and
   health waits come from the Talos client libraries.
4. **The iPXE entry binary.** booty's proxyDHCP answers *every* PXE
   DISCOVER with the boot binary — a stock `ipxe.efi` re-DHCPs and is
   handed itself forever. Breaking the loop requires an `ipxe.efi` built
   with an embedded chain script pointing at booty's `/boot.ipxe`
   (booty's `just ipxe-embed` recipe does this in a container); booty
   deliberately ships no binaries. Decided (OQ-9): the CLI's
   `talos ipxe` command runs that containerized build itself.

## Detailed Design

### Command tree

Stages are cobra subcommands, grouped by target. Every stage is
independently re-runnable and idempotent; `--dry-run` on each prints the
computed delta without applying it. There is deliberately no all-in-one
`bootstrap run` (ADR-0001: operator-driven; see OQ-7).

```text
bootstrap
├── validate                  # parse + validate the config file (hclkit diagnostics)
├── pve
│   ├── form                  # create cluster on first node, join the rest, wait for quorum
│   └── certs                 # ACME account + Cloudflare plugin + per-node certificates
├── talos
│   ├── secrets               # generate the Talos secrets bundle (once; no-op if present)
│   ├── emit                  # write booty catalog + machineconfigs + fetch boot assets
│   ├── ipxe                  # build the embedded-chain ipxe.efi via booty's containerized build (OQ-9)
│   ├── vms                   # create + start PXE-boot VMs on PVE nodes
│   ├── bootstrap             # one-time etcd bootstrap; write talosconfig/kubeconfig
│   └── health                # wait for / report cluster health
└── version
```

Global flags: `--config <file>` (default `bootstrap.hcl`), `--output
<dir>` (emission/output root, default `./bootstrap-out`), `--secrets
<path>` (Talos secrets bundle, default `secrets.yaml` next to the config
file), `--dry-run`, `--log-level`. Per OQ-8, the flag/config split is a
rule, not a habit: anything the Hoomlab service would also need lives in
the HCL file; anything CLI-only (output paths, verbosity) is a flag.

The expected operator flow, start to finish:

```text
validate → pve form → pve certs → talos secrets → talos emit → talos ipxe
        → [operator starts booty container] → talos vms
        → talos bootstrap → talos health
```

### Package layout

Self-contained in the `tools/bootstrap` module (ADR-0001; extraction for
the Hoomlab service happens later, when the service exists):

```text
tools/bootstrap/
├── main.go                   # slog default, version vars, cmd.Execute
├── cmd/                      # cobra tree only — flag parsing, wiring, output
├── internal/config/          # HCL schema structs + hclkit loader + validators
├── internal/pve/             # stage 1–2: formation, certs (proxmox-go-sdk)
├── internal/talos/           # secrets bundle, machineconfig gen, bootstrap, health
├── internal/emit/            # booty catalog/templates/assets writers (golden-testable)
└── internal/steps/           # the shared step/convergence engine + dry-run printer
```

`internal/steps` is the one piece of shared machinery: a stage is a list of
`Step{Name, Check(ctx) (done bool), Apply(ctx) error}`. The runner skips
done steps, applies pending ones in order, and `--dry-run` prints the
pending list instead of applying. That is the whole convergence engine —
no plan files, no state.

### Convergence model

Each step's `Check` reads the world through the APIs; nothing is cached
between runs. Concretely:

| Step | "Done" check against the world |
| --- | --- |
| create cluster | `cluster.GetStatus` / `ListConfigNodes` on node 1 shows a cluster with the configured name |
| join node N | node N appears in `ListConfigNodes` (per-member wait, then quorum wait — pvelab's `waitForMember`/`waitForQuorum` pattern) |
| ACME account | `ListACMEAccounts` contains the configured account |
| node certificate | `GetNodeCertificates` shows an ACME cert covering the node FQDN, not near expiry |
| secrets bundle | the file at `--secrets` exists (the one filesystem check — see OQ-2) |
| emit artifacts | emitted tree matches a fresh render (byte-compare; always safe to re-emit — but booty loads the catalog/templates once at startup, so a changed emit ends with "restart the booty container" in the step output) |
| VM for node X | a VM with the configured VMID exists on the target PVE node (`qemu.Get`); started if config says so |
| etcd bootstrap | Talos API reports etcd healthy / bootstrap already done (treat "already bootstrapped" errors as done) |

Interruption anywhere → re-run the stage; completed steps check as done
and are skipped. PVE writes go through `tasks.Ref` waits so a step is not
"applied" until PVE says the task finished.

### Stage 1: Proxmox cluster formation

Direct translation of pvelab's `FormCluster` (copied and adapted, not
imported — pvelab is functional documentation):

1. Client to node 1 (`api.TokenCredentials` resolved through the
   config's `env()` references — OQ-4).
   `CreateCluster(&ClusterCreateSpec{Name})`; poll
   `ListConfigNodes` until the node appears (fire-and-poll per the SDK
   docs; formation restarts pmxcfs under the call).
2. `JoinInfo` from node 1 → fingerprint.
3. For each remaining node, **serially**: client to that node,
   `JoinCluster(&JoinSpec{Hostname: node1, Password: <root@pam>,
   Fingerprint})`, wait for membership, then wait for quorum before the
   next join. Corosync `link0`/ring addresses go through `JoinSpec.Extra`
   from the node's `corosync_link` config when set.

Joins require the root@pam password of an existing member — the one
credential the token cannot substitute for. Like every secret, the
config carries only an `env()` reference to it, never the value
(see [Secrets handling](#secrets-handling), OQ-4).

### Stage 2: Certificates

1. Register the ACME account (`RegisterACMEAccount`, directory + contact
   from config) if absent.
2. Register/verify the Cloudflare DNS-01 plugin:
   `CreateACMEPlugin(&ACMEPluginSpec{...})` with the typed
   `ACMECloudflare` provider (scoped `CF_Token` preferred; credentials
   redacted from `%v` by design) — shipped and live-verified in
   proxmox-go-sdk v0.11.0 (OQ-3).
3. Per node: set the ACME domain config via
   `SetNodeConfig(node, &NodeConfigUpdate{...})` (`ACMEDomain` entries),
   then `OrderNodeCertificate`, await the task. Renewals are the same
   command re-run (`Check` looks at expiry via `GetNodeCertificates`).

### Stage 3: Artifact emission for booty

`talos secrets` (once) and `talos emit` (repeatable) produce everything
booty's container consumes, under the CLI output root (`--output` — a
CLI concern, not config, per OQ-8):

```text
<output>/booty/
├── catalog/
│   ├── 00-variables.hcl      # cluster name, talos version, endpoint, boot_base
│   ├── 10-profiles.hcl       # talos-control / talos-worker profiles
│   └── 20-groups.hcl         # one group per node, pinned by MAC
├── templates/                # --templates-dir overlay (see OQ-1)
│   └── talos/{controlplane,worker}.yaml.tmpl   # family subdir is mandatory
├── boot/
│   ├── ipxe.efi              # embedded-chain iPXE build (OQ-9)
│   └── talos/<version>/{vmlinuz,initramfs.xz}  # fetch: see OQ-6
└── booty-run.sh              # ready-to-run `docker run … booty serve …`
```

- The CLI only writes this tree locally — **the operator moves it** to
  the booty host and points `--catalog`/`--templates-dir`/`--boot-dir`
  at it (OQ-1/OQ-2). No remote copying, no pushing; placement is an
  ADR-0001 "documented step".
- The catalog mirrors booty's documented shape (profiles with
  `boot`/`render`/`vars`, groups with `selector = { mac }`), generated
  from our node config — each VM's MAC is declared in the config
  (OQ-5), so groups and VM NICs agree by construction. Emission details that are contract, not style:
  selector keys are a closed set (`mac` et al. — unknown keys silently
  never match), profile cmdlines carry the mandatory Talos metal args
  (`talos.platform=metal init_on_alloc=1 slab_nomerge pti=on`), and the
  iPXE `${mac}` substitution must be written `$${mac}` to survive HCL.
- Machineconfig completeness (booty's templates omit secrets) is OQ-1;
  the recommended path bakes machinery-generated, secret-bearing
  per-role templates into the `templates/` overlay so booty serves full
  configs with zero booty changes.
- Emission is pure rendering: no API calls, deterministic output,
  golden-file tested. Re-emit is always safe; a diff against the existing
  tree is the `Check`.
- Starting the booty container remains an operator step by design
  (ADR-0001 prerequisite); `booty-run.sh` makes it copy-paste — and
  encodes the sharp edges so the operator doesn't rediscover them:
  `--net=host`, a port-capable user, volume mounts for
  catalog/templates/boot, the correct `--catalog` flag, and
  `--proxydhcp --server-ip` from config. Re-emits end with "restart
  booty" because it has no hot reload.

### Stage 4: VM creation

Per Talos node in config, on its target PVE node:

```go
qemu.NewService(client, pveNode, caps).Create(ctx, &qemu.CreateSpec{
    VMID:   node.VMID,
    Name:   node.Hostname,
    Memory: node.Memory, Cores: node.Cores,
    SCSI0:  fmt.Sprintf("%s:%d", node.Storage, node.DiskGB),
    Net0:   fmt.Sprintf("virtio,bridge=%s,macaddr=%s,firewall=0", node.Bridge, node.MAC),
    Boot:   "order=scsi0;net0",   // disk first, PXE fallback — see below
    Extra:  map[string]string{ /* bios/efidisk0/machine/cpu/rng0/serial0 */ },
})
```

Then `Start` and await the task. `Boot: "order=scsi0;net0"` — disk
first, net fallback: the empty disk falls through to PXE on first boot,
and after Talos installs itself the node boots from disk. Re-imaging is
"wipe disk, reboot" and the firmware falls back to PXE again (RFC-0001's
re-imageable-by-reboot property). The MAC in `Net0` is the same MAC
pinned in the catalog group — identity flows from config to both sides
(OQ-5).

booty's Proxmox+Talos walkthrough proved several VM settings are
load-bearing, so the CLI encodes them via `Extra` rather than leaving
them to defaults: UEFI (`bios=ovmf` + `efidisk0`) **without
pre-enrolled Secure Boot keys** (they reject unsigned iPXE/Talos), a
**VirtIO RNG device** (post-PixieFail EDK2 silently drops the PXE boot
option without one), `cpu=host` (Talos requires x86-64-v2; `kvm64`
panics), NIC firewall off, and a serial console for `qm terminal`
debugging.

### Stage 5: Talos cluster bring-up

The post-PXE remainder, via the Talos Go libraries (`machinery` config +
client):

1. Nodes PXE-boot, fetch machineconfig from booty, install, reboot into
   Talos.
2. `talos bootstrap`: one-time etcd bootstrap against the first
   control-plane node (idempotent: "already bootstrapped" is success).
3. Write `talosconfig` and `kubeconfig` under the output root
   (`<output>/out/`), which is how the operator — and later the Hoomlab
   service Helm install — talks to the cluster.
4. `talos health`: block until the cluster reports healthy; also the
   standalone verification command.

### Secrets handling

Per OQ-4, secret *values* never appear in the config — the config
carries `env()` references, resolved at load time by an `env(name)`
function the loader registers in hclkit's eval context
(`WithFunctions`). The same file therefore works in both lives of the
config: the CLI run exports the variables at invocation, and the
Hoomlab service deployment gets them from a Kubernetes Secret via the
chart's `secrets.stringData`/`envFrom` — the config never changes.

| Secret | Used by | Config reference |
| --- | --- | --- |
| PVE API token | all PVE reads and non-formation writes | `env("HOOMLAB_PVE_TOKEN_ID")` / `env("HOOMLAB_PVE_TOKEN_SECRET")` |
| root@pam password | the root@pam-reserved endpoints (amended by INV-0001, 2026-08-25): `pve form`'s create and joins, and `pve certs`' one-time ACME account registration — PVE reserves these for the literal root@pam user; no API token passes the `user != root@pam` check | `env("HOOMLAB_PVE_ROOT_PASSWORD")` |
| Cloudflare API token | `pve certs` plugin registration | `env("HOOMLAB_CLOUDFLARE_API_TOKEN")` |
| Talos secrets bundle | `talos emit`, `talos bootstrap` | not in config — the file at `--secrets`, generated once by `talos secrets`, operator-owned (OQ-2) |

`validate` reports unresolvable `env()` references as diagnostics with
the variable name, so a missing export fails loudly before any stage
touches an API.

One exposure to state plainly: booty's `/machine-config` endpoint is
unauthenticated plaintext HTTP, and with OQ-1a the served configs carry
the cluster PKI and join tokens. That is the standard `talos.config`
metal trade-off, and the mitigation is environmental — the boot network
is a trusted, isolated segment (an ADR-0001 "documented step, not a
hidden one" prerequisite). booty has an auth token for this endpoint
designed but not yet implemented; when it lands, `booty-run.sh` and the
emitted cmdline grow the token together.

## API / Interface Changes

- New commands as in [Command tree](#command-tree); no changes to the root
  `hoomlab` scaffold, chart, or release train.
- `/cluster/acme/plugins` support shipped in proxmox-go-sdk **v0.11.0**
  (verified 2026-08-20); `pve certs` imports that version or later
  (OQ-3).
- No booty changes (OQ-1): booty consumes the emitted files exactly as
  it consumes hand-written ones.

## Data Model

### Configuration schema

One HCL file (OQ-8), `internal/config` structs gohcl-tagged, loaded
with `hclkit.New(WithFunctions(env), WithValidators(...)).LoadFile(path,
&cfg)`. Every VM is declared explicitly, MAC included (OQ-5) — no
counts, no derivation; the config is the single source of identity for
both the PVE API calls and the emitted booty artifacts, the way pvelab's
config drives its nested lab. Secrets are `env()` references (OQ-4).
The shape (illustrative, field names final at implementation):

```hcl
cluster "homelab" {
  # Stage 1
  pve {
    token_id      = env("HOOMLAB_PVE_TOKEN_ID")
    token_secret  = env("HOOMLAB_PVE_TOKEN_SECRET")
    root_password = env("HOOMLAB_PVE_ROOT_PASSWORD")   # formation writes (create + joins)

    node "pve-01" {
      endpoint = "https://10.0.10.11:8006"
      address  = "10.0.10.11"          # corosync link0
      primary  = true                   # cluster is created here
    }
    node "pve-02" { endpoint = "https://10.0.10.12:8006" address = "10.0.10.12" }
    node "pve-03" { endpoint = "https://10.0.10.13:8006" address = "10.0.10.13" }
  }

  # Stage 2
  acme {
    email  = "dgifford06@gmail.com"
    domain = "pve.example.internal"     # node FQDNs: <node>.<domain>
    dns    = "cloudflare"               # the blessed provider (ADR-0001)
    token  = env("HOOMLAB_CLOUDFLARE_API_TOKEN")
  }

  # Stages 3–5
  talos {
    version  = "v1.13.8"
    endpoint = "https://10.0.20.10:6443"   # cluster VIP / endpoint

    booty {
      url = "http://10.0.10.5:8080"        # where the operator runs the container
    }

    node "cp-01" {
      role     = "controlplane"
      pve_node = "pve-01"
      vmid     = 200
      mac      = "02:50:99:a2:00:01"
      cores    = 4
      memory   = 8192
      disk_gb  = 64
      storage  = "local-zfs"
      bridge   = "vmbr0"
    }
    node "cp-02" { role = "controlplane" pve_node = "pve-02" vmid = 201 mac = "02:50:99:a2:00:02" /* … */ }
    node "worker-01" { role = "worker" pve_node = "pve-03" vmid = 300 mac = "02:50:99:a2:01:01" /* … */ }
  }
}
```

Config-continuity constraint (RFC-0001, OQ-8): this file is what the
Hoomlab service later consumes to take ownership — deployed on the
cluster it describes, it sees everything already running and converges
on no-op. Additions are fine; renames need a migration note. CLI-only
concerns (output paths) never enter the schema.

### Files the CLI writes

| Path | Writer | Contents |
| --- | --- | --- |
| `--secrets` path (default `secrets.yaml`) | `talos secrets` | Talos machinery secrets bundle (OQ-2) |
| `<output>/booty/**` | `talos emit` / `talos ipxe` | catalog, templates overlay, boot assets, run script |
| `<output>/out/{talosconfig,kubeconfig}` | `talos bootstrap` | cluster access credentials |

The config file is read-only input. Deleting `<output>/` and re-running
regenerates it; the secrets bundle is the one file that must not be
regenerated (OQ-2).

## Testing Strategy

- **`internal/config`**: table tests over HCL fixtures — valid configs,
  each validator failure, `env()` resolution (present, missing, empty).
  Diagnostics rendered and asserted.
- **`internal/pve`**: `mockpve` end to end — seed nodes, run `pve form`,
  assert cluster state; interruption tests (kill between join N and N+1,
  re-run, assert convergence). The task-waiter path uses
  `AddTask`/`FinishTask`. mockpve (v0.11.0) serves the ACME plugin
  routes natively (`AddACMEPlugin` seeding) — no custom handlers needed.
- **`internal/emit`**: golden-file tests for the full emitted tree
  (catalog HCL, templates, run script); byte-stable output is a test
  invariant, not an aspiration. Beyond byte-comparison, tests import
  booty itself to prove the contract: `catalog.DirSource.Load` over the
  emitted catalog (booty's own `validate` in-process), and
  `render.New(render.WithTemplates(...))` + `Renderer.Config` against a
  synthetic identity to dry-render exactly what a booting node would
  receive — validated with the machinery config loader
  (`Validate(ModeMetal)`).
- **`internal/talos`**: interface-wrap the Talos client; mockery v3 mocks
  for bootstrap/health sequencing (first-call bootstrap, "already
  bootstrapped" tolerance).
- **e2e (deferred)**: real PVE lab via pvelab-style nested VMs once the
  stages exist; not a merge gate.

## Migration / Rollout Plan

Build in stage order, each landing usable on its own:

1. `internal/config` + `validate` (schema, loader, validators).
2. `internal/steps` + `pve form` against mockpve, then a real 3-node lab.
3. `pve certs` (proxmox-go-sdk ≥ v0.11.0, which ships the ACME-plugins
   API — OQ-3, resolved).
4. `talos secrets` + `talos emit` (golden files; catalog verified against
   a hand-run booty container with `booty validate --catalog`).
5. `talos vms` (mockpve).
6. `talos bootstrap` + `health` against the real lab — the first full
   drill of RFC-0001's success criterion.

Roll-forward only; no compatibility surface to migrate. The first full
bare-nodes-to-healthy-cluster run is the acceptance test for the whole
document.

## Open Questions

Format: **a** is my recommendation; **b**+ are alternatives; fill in
**other** to override with something not listed.

**OQ-1 — How do Talos machineconfigs get their secrets to booted nodes?**
booty's templates deliberately omit cluster secrets, and nodes fetch
config from booty's `/machine-config` endpoint at boot.

**Decided: a** (2026-08-19). `talos emit` generates complete,
secret-bearing per-role machineconfigs with the Talos machinery
packages, written as booty's `--templates-dir` overlay (hostname stays a
template var) — the mechanized version of booty's own overlay-template
walkthrough. Refinement from review: the CLI only writes the files
locally; **the operator moves them** to wherever booty runs and points
`--templates-dir` at them. No pushing, no remote placement.

- ~~b: upstream machinery-secrets support into booty first~~
- ~~c: secret-less configs + `talosctl apply-config` per node~~

**OQ-2 — Where does the Talos secrets bundle live?**
It must survive between runs (rejoining nodes, regenerating configs,
rebuild drills) — it is input, not state, but it cannot be regenerated
without making a new cluster.

**Decided: a** (2026-08-19). `talos secrets` writes the bundle once (at
`--secrets`, default `secrets.yaml` next to the config file); every
later stage requires it; gitignored by default, operator-owned — the
operator moves/keeps it the same way they place the emitted booty files
(same handling principle as OQ-1). Encryption at rest is the operator's
choice (sops/git-crypt).

- ~~b: regenerate per run (breaks convergence)~~
- ~~c: external secrets manager now~~

**OQ-3 — The SDK has no `/cluster/acme/plugins` API. Where does the
Cloudflare DNS-01 plugin registration live?**

**Decided: a — already in flight** (2026-08-19). The
`/cluster/acme/plugins` work is being verified in proxmox-go-sdk right
now; when it lands and a version is cut, this CLI imports it. `pve
certs` is sequenced behind that import — no `client.API()` interim
implementation.

*Resolved (2026-08-20): shipped in v0.11.0 and verified against this
design — plugin CRUD (`ListACMEPlugins`/`CreateACMEPlugin`/…), typed
`ACMECloudflare` provider (live-verified upstream, token-first, redacting
`String()`), `GetNodeConfig`/`SetNodeConfig` with `ACMEDomain` for the
per-node domain config, `GetACMEChallengeSchema`/`ListACMEDirectories`
discovery, and native mockpve routes with `AddACMEPlugin` seeding. Tag
resolves through the module proxy
(`go get github.com/donaldgifford/proxmox-go-sdk@v0.11.0`). Nothing
missing for Stage 2.*

- ~~b: `client.API()` raw calls now, swap later~~

**OQ-4 — How is the root@pam password (required by cluster joins)
provided?**

**Decided: other — `env()` references in the config** (2026-08-19),
superseding both drafted options and generalizing to *all* secrets. The
config file must serve two lives — the bootstrap CLI now, the Hoomlab
service on the cluster later ("take ownership": deployed with the same
file, it finds everything running and converges on no-op). So the config
carries `env("HOOMLAB_…")` references, never values: the CLI run exports
the variables at invocation; the k8s deployment feeds them from a
cluster Secret via the chart's `envFrom`. Same file, both runtimes.

- ~~a: env var + interactive prompt, nothing in config~~
- ~~b: vars-file entries~~

**OQ-5 — MAC address strategy (PXE identity binding)?** booty matches
booting machines to groups by MAC; VM NICs and catalog groups must agree.

**Decided: c** (2026-08-19). Every VM's settings — MAC included — are
declared explicitly per `node` block in the config, the way pvelab's
config drives its nested lab. The CLI sets PVE node and VM settings from
those declarations, and the emitted booty files derive from the same
declarations, so the PXE identity binding agrees by construction.
Maximum explicitness is the point: config is the single source of
identity, with a uniqueness validator catching MAC/VMID collisions at
`validate` time.

- ~~a: CLI-derived deterministic MACs~~
- ~~b: create VMs first, read PVE-assigned MACs back~~

**OQ-6 — Who stages the Talos kernel/initrd into booty's boot dir?**

*Clarification (2026-08-19), answering the review question "does the CLI
build kernels?": no building, by anyone.* Talos publishes prebuilt
`vmlinuz`/`initramfs.xz` per release through its Image Factory: you
describe a schematic (extensions, extra kernel args — driven by our
`talos` config block), the factory returns a schematic ID, and the boot
assets are plain HTTPS downloads keyed by schematic ID + version.
Option a means `talos emit` performs those downloads; nothing is
compiled. (The only compiled artifact anywhere is OQ-9's `ipxe.efi`,
which booty's containerized build produces.)

**Decided: a** (2026-08-19). `talos emit` downloads the prebuilt assets
from the Talos Image Factory for the configured version/schematic into
`booty/boot/`, checksum-verified, skipped when already present — a
quality-of-life feature that guarantees the staged kernel always matches
the config's `talos` settings instead of drifting from a manual step.

- ~~b: operator stages them manually~~

**OQ-7 — Is there a meta-command that chains stages?**

**Decided: a** (2026-08-19). No meta-command. The documented flow is the
runbook; each stage prints what to run next on success.

- ~~b: `bootstrap up` with confirmation gates~~

**OQ-8 — Config layout: directory or single file?**

**Decided: b — a single HCL config file** (2026-08-19), because the file
is the reuse unit: the same file passed to the bootstrap CLI is
eventually handed to the Hoomlab service. Corollary rule for every
future flag-or-config call: **anything the service would also consume
belongs in the HCL file; anything CLI-only (output dir for emitted
files, verbosity, dry-run) is a CLI flag or command.** The emission
output root is therefore `--output`, not config.

- ~~a: config directory via `LoadDir` + vars files~~

**OQ-9 — Who produces the embedded-chain `ipxe.efi`?** A stock iPXE
binary loops forever against booty's proxyDHCP; the working pattern is
an iPXE build with an embedded three-line chain script pointing at
booty's `/boot.ipxe`. booty deliberately ships no binaries ("not a
build service"), and downloading `boot.ipxe.org/ipxe.efi` is both
unverified and loop-prone.

**Decided: a** (2026-08-17). `talos emit` writes the `embed.ipxe` chain
script (URL derived from `booty.url`) and `bootstrap talos ipxe` runs
booty's containerized iPXE build (docker is already a prerequisite for
the booty container itself), dropping the binary into
`booty/boot/ipxe.efi`. Convergence stays stateless: the step compares
the on-disk `embed.ipxe` against a fresh render — a changed `booty.url`
diffs the script and triggers the rebuild.

- ~~b: operator builds manually via booty's `just ipxe-embed`~~
- ~~c: download a stock `ipxe.efi`~~

## References

- ADR-0001: Bootstrap CLI (decision this designs)
- RFC-0001: Hoomlab (Phase 1)
- `github.com/donaldgifford/proxmox-go-sdk` — `proxmox` package;
  `cmd/pvelab/lab` (`FormCluster`, functional documentation);
  `proxmox/mockpve`
- `github.com/donaldgifford/booty` — catalog HCL contract
  (`examples/catalog/`), `render/templates/talos/` (secrets-omitted
  templates), serve endpoints, and
  `docs/go-ipxe/10-talos-overlay-walkthrough.md` (the manual procedure
  OQ-1a mechanizes, including the load-bearing Proxmox VM settings)
- `github.com/donaldgifford/hclkit` — `pkg/hclkit` Loader/vars/validators
- Talos machinery: `github.com/siderolabs/talos/pkg/machinery` (secrets
  bundle, config generation), Talos client (bootstrap, health)
