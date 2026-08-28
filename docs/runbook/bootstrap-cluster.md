# Runbook: bare Proxmox nodes → healthy Talos cluster

Step-by-step operator procedure for the `bootstrap` CLI
(`tools/bootstrap`). Implements ADR-0001 / DESIGN-0001; phase tracking
in IMPL-0001, remaining pre-drill work in INV-0001.

**Scope of verification.** Every command, flag, and output string below
was taken from the code and, where the stage does not need Proxmox
hardware, executed on a workstation. Stages that need the lab are
marked **`[unverified]`** — they are written from the code and are
exactly what the acceptance drill is meant to confirm. When a step
turns out to be wrong, fix the code and this document together; that is
the whole point of running the drill from here rather than from memory.

## Table of contents

- [Before you start](#before-you-start)
- [0. Get the CLI](#0-get-the-cli)
- [1. Write the config](#1-write-the-config)
- [2. `validate`](#2-validate)
- [3. `pve form`](#3-pve-form)
- [4. `pve storage`](#4-pve-storage)
- [5. `pve certs`](#5-pve-certs)
- [6. `talos secrets`](#6-talos-secrets)
- [7. `talos emit`](#7-talos-emit)
- [8. `talos ipxe`](#8-talos-ipxe)
- [9. Start booty](#9-start-booty-operator-step)
- [10. `talos vms`](#10-talos-vms)
- [11. `talos bootstrap`](#11-talos-bootstrap)
- [12. `talos health`](#12-talos-health)
- [13. Convergence pass](#13-convergence-pass)
- [Troubleshooting](#troubleshooting)

## Before you start

**On the workstation running the CLI:**

- Go 1.26+ and `just`, or a release binary from `tools/bootstrap/vX.Y.Z`.
- `docker` — `talos ipxe` compiles the boot binary in a container.
- Network egress to `factory.talos.dev` (boot assets), `ghcr.io`
  (the booty image), and `github.com` (the iPXE source).
- Reachability to every Proxmox API endpoint and, later, to the Talos
  endpoint.

**In the lab:**

- **Proxmox VE installed on every node**, API reachable at the
  endpoints you will put in the config. Nodes other than the primary
  need not be fresh installs, but joining replaces a joiner's
  `/etc/pve` **wholesale** with the cluster's — storage definitions,
  users, tokens, firewall rules, jobs, all of it — and PVE refuses to
  join a node that has guests. Node-local state outside `/etc/pve`
  (ZFS pools, network config, SSH keys) survives. So the real
  prerequisite is: joiners must be **guest-free** and hold no
  cluster-level config you are not prepared to lose; anything worth
  keeping must be re-declared cluster-wide after the join. Watch the
  reverse hazard too — after growth, every unrestricted storage entry
  in the cluster config is live on *every* node (the stock
  `local-zfs` will bind each node's `rpool/data`); pin entries with
  `nodes` restrictions where a pool must not take VM disks.
- **A PVE API token and the root password.** PVE reserves cluster
  creation, node joins, and ACME account registration for the literal
  `root@pam` user — no token or root-equivalent user passes the
  identity check — so those calls authenticate with the root
  password; the token drives everything else, renewals included.
  Converged re-runs touch the password only for the account-directory
  read fallback.
- **A Cloudflare API token** with DNS-edit permission on the
  certificate domain's zone. Cloudflare is the only DNS-01 provider
  the certs stage supports (ADR-0001), and config validation enforces
  that.
- **A boot network you trust end-to-end.** booty's `/machine-config`
  endpoint is unauthenticated plaintext HTTP and the configs it serves
  carry the cluster PKI and join tokens — the standard `talos.config`
  metal trade-off. A dedicated segment is ideal but not required; a
  shared VLAN works if you accept both consequences with eyes open:
  every host on the segment can read the machine configs (PKI
  included), and booty's proxyDHCP answers **every** PXE attempt
  there — unconfigured machines chainload `ipxe.efi`, get a 404, and
  drop to an iPXE shell, so nothing else on the segment can netboot
  for its own purposes while booty runs.
- **A booty host with docker** on that network, reachable at the
  `talos.booty.url` you configure. Real PXE needs `--net=host`, so
  that host's own IP is what the emitted launcher advertises.
- **DHCP reservations for the Talos VMs.** The config pins each VM's
  MAC; reserve an address for each. Critically, `talos.endpoint`'s
  host must resolve to the **first control-plane node's** reserved
  address — `talos bootstrap` and `talos health` dial it directly, and
  a VIP that only exists *after* bootstrap cannot serve the bootstrap
  call that creates it.

## 0. Get the CLI

From a checkout:

```sh
just bootstrap-build     # → build/bin/bootstrap
```

There is no `bootstrap` container image — tools under `tools/` release
binary archives only. Docker is a *dependency* of two stages (the iPXE
build, and booty itself), not the delivery mechanism.

Work from a scratch directory so nothing pre-exists:

```sh
mkdir -p ~/drill && cd ~/drill
alias bootstrap=/path/to/repo/build/bin/bootstrap
bootstrap version
```

## 1. Write the config

One HCL file describes one cluster. Start from the annotated example:

```sh
cp /path/to/repo/tools/bootstrap/examples/bootstrap.hcl .
$EDITOR bootstrap.hcl
```

Fill in real endpoints, MACs, VMIDs, storage, and bridges. Secret
*values* never appear in the file — secret-bearing attributes carry
`env("HOOMLAB_…")` references resolved at load time. Export them
first; the names are yours to choose, these are what the example uses:

```sh
export HOOMLAB_PVE_TOKEN_ID='root@pam!bootstrap'
export HOOMLAB_PVE_TOKEN_SECRET='…'
export HOOMLAB_PVE_ROOT_PASSWORD='…'
export HOOMLAB_CLOUDFLARE_API_TOKEN='…'
```

While drilling, uncomment `acme.directory` and point it at Let's
Encrypt **staging** so failed orders don't burn production rate limits.
Re-run `pve certs` against production once the flow is proven.

### Global flags

These are CLI concerns, deliberately kept out of the config file
(DESIGN-0001 OQ-8):

| Flag | Default | Meaning |
| --- | --- | --- |
| `--config` | `bootstrap.hcl` | config file |
| `--output` | `./bootstrap-out` | root for emitted files |
| `--secrets` | `secrets.yaml` next to the config | Talos secrets bundle |
| `--dry-run` | off | check everything, apply nothing |
| `--log-level` | `info` | `debug`, `info`, `warn`, `error` |

`--dry-run` works on every stage and never writes. Use it ahead of any
stage you are unsure about:

```sh
bootstrap talos emit --dry-run
pending    emit-artifacts
pending    boot-assets

dry-run: 2 of 2 steps pending, nothing applied
```

## 2. `validate`

```sh
bootstrap validate
```

Expected:

```text
✓ bootstrap.hcl: cluster "homelab" is valid (3 pve nodes, 4 talos nodes)
```

Every stage runs this same load-and-validate first, so fix everything
it reports before touching an API. A missing export is reported with
the offending line:

```text
bootstrap.hcl:48,14-52: Error in function call; Call to function "env"
failed: environment variable HOOMLAB_CLOUDFLARE_API_TOKEN is not set.
```

## 3. `pve form`

**`[unverified — needs the lab]`**

```sh
bootstrap pve form
```

Creates the cluster on the primary node, joins the remaining nodes one
at a time (each join waits for corosync membership and quorum before
the next starts), then verifies the cluster is quorate. Steps:
`create-cluster`, `join-<node>` per remaining node, `cluster-quorate`.

Expected:

```text
✓ cluster "homelab" formed and quorate (3 of 3 steps applied)
next: bootstrap pve storage
```

Interruption-safe: re-run and it picks up at the first unjoined node.

> TLS verification is deliberately skipped for every PVE call.
> Pre-formation nodes serve self-signed certificates and those churn
> again during joins — installing real ones is what the *next* stage
> is for.

### Post-formation (manual, optional): redundant corosync links

The CLI declares a single corosync link per node — each node's
`address` becomes `link0` on create/join, and that is the only link
the formed cluster has. If the link0 network goes down, the cluster
loses quorum and pmxcfs goes read-only until it returns.

Adding a redundant `link1` afterwards is supported by PVE but is a
**manual `corosync.conf` edit**, not an API call: follow the pvecm
docs' procedure (copy `/etc/pve/corosync.conf`, add a `ring1_addr`
per node in the nodelist and a second `interface` entry under
`totem`, bump `config_version`, move the copy into place — corosync
reloads live). Do it any time after formation; no re-form needed.
Note the redundancy is only as real as the physical paths: a node
whose two link networks share one cable (e.g. a tagged VLAN riding
the same copper) keeps a single physical failure domain.

The PVE cluster API itself accepts `link0`–`link7` on both create and
join, so first-class multi-link support is an SDK/CLI enhancement,
not a protocol gap — tracked in IMPL-0002 Phase 6.

## 4. `pve storage`

```sh
bootstrap pve storage
```

Converges the config's `storage` blocks into cluster storage entries.
Steps: one `storage-<name>` per declared block, in config order.
Expected (here: one entry created over a pre-existing dataset, the
stock `local-zfs` restricted to one node):

```text
✓ 2 storage declarations converged (2 steps applied)
next: bootstrap pve certs
```

What the stage will and will not do:

- A missing entry is **created**; an existing one is **updated in
  place**, touching only the fields the block declares. Settings the
  config expresses no opinion about (an empty list, an unset bool)
  are never sent — restricting the stock `local-zfs` to one node
  leaves its content types exactly as the installer wrote them.
- Identity is fixed: an existing entry whose `type` (or `path`)
  disagrees with the config is an **error**, never a
  delete-and-recreate. Deletion could orphan VM disks; that call
  stays with you.
- With no `storage` blocks the stage does nothing, and Talos nodes
  may reference pre-existing storage. Declaring *any* block turns on
  validation: every Talos node's `storage` must then reference a
  declared block.
- The stage declares entries; it does not create datasets. A
  zfspool block's `pool` (e.g. `fast/vm`) must already exist on the
  restricted nodes — that is node provisioning, not cluster config.

Two read-back behaviors that are normal, not drift: a
node-restricted entry shows as `disabled` in `pvesm status` on the
nodes outside its list (the restriction working, not a fault), and
PVE materializes server-generated properties into the entry —
creating a zfspool adds a `mountpoint` line you never declared. The
stage compares structurally (list options are unordered sets, only
declared fields count) for exactly these reasons.

The API token covers this whole stage: storage writes are gated by
`Datastore.Allocate`, a regular privilege check, not one of the
root@pam-reserved endpoints.

## 5. `pve certs`

**`[unverified — needs the lab]`**

```sh
bootstrap pve certs
```

Registers the ACME account, registers the Cloudflare DNS-01 plugin with
the token from the config, wires each node's certificate domain
(`<node>.<domain>`), and orders each certificate. Steps:
`acme-account`, `acme-plugin-cloudflare`, then `acme-config-<node>` and
`acme-cert-<node>` per node.

Expected:

```text
✓ acme certificates converged on 3 nodes (8 steps applied)
next: bootstrap talos secrets
```

Orders are serial and a DNS-01 order legitimately runs for minutes —
it waits on the CA resolving the challenge record. Renewal is this same
command re-run: a certificate with under 30 days of validity goes
pending again. A rotated Cloudflare token is detected and pushed the
same way.

## 6. `talos secrets`

```sh
bootstrap talos secrets
```

Expected:

```text
✓ secrets bundle written to secrets.yaml — back this file up, it is the cluster identity
next: bootstrap talos emit
```

The bundle **is** the cluster identity — the CA, tokens, and encryption
keys every machineconfig is seeded from. An existing file is never
overwritten, because regenerating it orphans every node holding the old
one. Re-running says so and does nothing:

```text
✓ secrets bundle already exists at secrets.yaml — leaving it alone
```

The file is written `0600`. Back it up now; treat it like a private key.

## 7. `talos emit`

```sh
bootstrap talos emit
```

Renders everything booty serves and downloads the boot assets. Steps:
`emit-artifacts`, `boot-assets`. Expected:

```text
✓ booty tree written to bootstrap-out/booty (2 steps applied)
next: bootstrap talos ipxe

artifacts changed — restart the booty container (it loads the catalog
and templates once at startup): bootstrap-out/booty/booty-run.sh
```

The tree:

```text
bootstrap-out/booty/
├── catalog/
│   ├── 00-variables.hcl        # talos version, booty url
│   ├── 10-profiles.hcl         # one boot recipe per node class
│   └── 20-groups.hcl           # one group per VM, pinned by MAC
├── templates/talos/
│   ├── controlplane.yaml.tmpl  # complete, secret-bearing machineconfigs
│   └── worker.yaml.tmpl
├── boot/talos/<version>/<schematic>/
│   ├── vmlinuz + .sha256       # Talos Image Factory kernel
│   └── initramfs.xz + .sha256
├── embed.ipxe                  # the chain script ipxe.efi embeds
└── booty-run.sh                # ready-to-run launcher
```

With `profile` blocks in the config, emit first resolves each node's
flattened extension set to an Image Factory schematic (a POST to the
factory; IDs are content-addressed, so re-runs get the same answer)
and stages one kernel/initramfs pair per unique set under its
schematic directory — the extensions are baked into those images and
the matching installer image, which is the only place they can be.
With a `talos cluster` block, the machineconfig templates additionally
carry the completion surface: topology labels, kubelet
serving-certificate rotation plus its approver manifest, and — for
`cni = "cilium"` — CNI none, kube-proxy disabled, the pinned Gateway
API CRDs, and the cilium-install Job with your validated values file
sealed in as a ConfigMap. Cilium then installs itself during `talos
bootstrap` with no operator action.

Emission is deterministic rendering, so re-running is always safe —
the check is a byte-diff against what is on disk, and staged boot
assets are left alone. A no-op run says:

```text
✓ booty tree at bootstrap-out/booty is up to date (nothing to do)
```

> **The one rule for this stage:** booty loads the catalog and
> templates *once at startup*. If anything changed here and booty is
> already running, restart it. A re-emit nobody restarts serves stale
> configs, and the failure surfaces much later as a node booting the
> wrong machineconfig.

The `.sha256` sidecars are trust-on-first-use: the Image Factory
publishes no authoritative checksum, so the first download records what
arrived and later runs verify against that record.

## 8. `talos ipxe`

```sh
bootstrap talos ipxe
```

Builds `bootstrap-out/booty/boot/ipxe.efi` — a pinned iPXE source tree
compiled with the emitted `embed.ipxe` baked in. That embedded script is
what makes network boot work at all: iPXE sends no machine identity on
its own, so the chain script is what turns a PXE request into booty's
`/ipxe?mac=…` lookup.

Expected:

```text
✓ ipxe.efi built at bootstrap-out/booty/boot/ipxe.efi
next: bootstrap talos vms
```

Requires docker, and takes several minutes — longer on an Apple Silicon
Mac, where the builder runs emulated because the artifact must be
x86-64 regardless of your workstation. Run `talos emit` first: that
stage owns `embed.ipxe`, and this one refuses to build around a stale
copy.

The build is skipped unless it is needed. The binary is stamped with
the SHA-256 of the chain script it embeds (`ipxe.efi.embed.sha256`) —
the embedded script can't be read back out of a compiled binary, so the
stamp is the only way to tell "built for this booty URL" from "built
for the previous one". In practice a changed `talos.booty.url` triggers
a rebuild and nothing else does:

```text
✓ ipxe.efi is already built for this booty url (nothing to do)
```

## 9. Start booty (operator step)

**`[unverified on real PXE — HTTP endpoints verified]`**

The CLI never copies anything to the booty host; moving the tree is
yours:

```sh
rsync -a bootstrap-out/booty/ booty-host:~/booty/
ssh booty-host
cd ~/booty && ./booty-run.sh
```

The launcher encodes the operational sharp edges, each load-bearing:

| Flag | Why |
| --- | --- |
| `--net=host` | DHCP is broadcast and TFTP moves to a fresh port per transfer; neither survives a bridge NAT |
| `--user 0:0` | under host networking the nonroot image cannot bind 69/67/4011 |
| `--catalog` | the flag is `--catalog`; booty's own walkthrough says `--catalog-dir`, which does not parse |
| `--boot-dir /boot` | without it booty registers no `/boot/…` route at all and every kernel fetch 404s |
| `--proxydhcp --server-ip` | answers PXE alongside your existing DHCP server, advertising `ipxe.efi` over TFTP |

Override the image with `BOOTY_IMAGE=…` if you need a different build.

The script is a reference implementation, and the flag table above is
the actual contract: any delivery mechanism that preserves those
flags is equivalent. A config-managed compose service (host network
mode, `user: "0:0"`, the same `:ro` mounts and `serve` arguments,
plus a restart policy) is a proven substitution — just keep the
"restart after every re-emit" rule, which no delivery mechanism can
repeal.

**Multi-homed booty host: pin the broadcast route.** proxyDHCP
replies go to `255.255.255.255`, and the kernel sends those out the
default-route interface — which on a multi-homed host is usually NOT
the boot VLAN. The offers leave the wrong NIC, the firmware never
sees them, and the symptom is `PXE-E16: No valid offer received`
while booty's log shows `proxyDHCP offer` ×4 (backoff 0/4/8/16 s).
The fix is a host route on the booty host:

```sh
ip route add 255.255.255.255/32 dev <boot-vlan-iface>
```

`ip route add` does not survive a reboot — persist it in whatever
owns the host's network config (systemd-networkd `[Route]`, netplan
`routes:`, or the config management that builds the host), or the
next reboot of the booty host silently breaks all PXE.

Verify it serves before creating any VMs — substitute a MAC from your
config:

```sh
curl -s http://<booty-host>:8080/boot.ipxe
curl -s "http://<booty-host>:8080/ipxe?mac=02:50:99:a2:00:01"
curl -s "http://<booty-host>:8080/machine-config?mac=02:50:99:a2:00:01" | head
curl -sI "http://<booty-host>:8080/boot/talos/v1.13.8/vmlinuz"
```

What to check in the responses:

- `/boot.ipxe` chains to `/ipxe?mac=${mac}&…` at your booty URL.
- `/ipxe?mac=…` names the right host and profile
  (`booty: booting cp-01 (profile talos-control)`) and its `kernel`
  and `initrd` lines point at `/boot/talos/<version>/…`.
- `/machine-config?mac=…` returns a full machineconfig whose
  `type:` matches the node's role and whose `hostname:` is the node
  name — no `{{ … }}` template expressions should remain.
- An **unconfigured** MAC returns `404`. If it returns a config, the
  catalog is matching more broadly than it should.
- The kernel and initramfs return `200` with a nonzero length.

booty logs `catalog loaded … profiles=2 groups=<N>` at startup; if `N`
does not equal your node count, it is serving a stale or partial tree.

## 10. `talos vms`

**`[unverified — needs the lab]`**

```sh
bootstrap talos vms
```

Creates every configured VM on its Proxmox node and starts it. Steps:
`vm-create-<node>` and `vm-start-<node>` per node. Expected:

```text
✓ 4 talos vms created and running (8 steps applied)
the VMs are now PXE booting from booty; watch progress there
next: bootstrap talos bootstrap
```

The VM settings are deliberate, not Proxmox defaults — UEFI (`ovmf` +
`q35`) *without* pre-enrolled Secure Boot keys, a VirtIO RNG,
`cpu=host`, and boot order `scsi0` then `net0`. Each is required for the
PXE → install → boot-from-disk cycle: disk-first with an empty disk
falls through to PXE on the first boot and boots the installed system
on every boot after.

The NIC MAC is the one from the config — the same MAC the emitted
catalog selects on, so a VM gets its own machineconfig by construction.

Watch progress in the booty logs, or `qm terminal <vmid>` on the
Proxmox node. Re-imaging a node later is: wipe its disk and reboot.

Re-running converges: an existing VM is left alone, a stopped one is
started.

## 11. `talos bootstrap`

**`[unverified — needs the lab]`**

Run once the VMs have PXE-booted, installed, and rebooted into Talos.

```sh
bootstrap talos bootstrap
```

Steps: `write-talosconfig`, `etcd-bootstrap`, `write-kubeconfig`.
Expected:

```text
✓ cluster bootstrapped, credentials in bootstrap-out/out (4 steps applied)
next: bootstrap talos health
```

Issues the one-time etcd bootstrap against the cluster endpoint — which
must resolve to the first control-plane node — and writes
`bootstrap-out/out/talosconfig` (generated from the secrets bundle) and
`bootstrap-out/out/kubeconfig` (fetched from the live cluster), both
`0600`.

Re-running converges: "already bootstrapped" is treated as success, and
existing credential files are **never** overwritten. They are working
operator credentials, not render output — and every generated
talosconfig carries a freshly minted client certificate, so rewriting
would hand you new credentials on every run.

## 12. `talos health`

**`[unverified — needs the lab]`**

```sh
bootstrap talos health            # --wait defaults to 10m
```

Blocks until the cluster's own health check passes — etcd quorum,
control-plane components, every member `Ready` — or the wait expires.

```text
✓ cluster "homelab" is healthy
```

Then the acceptance criterion itself (RFC-0001 Phase 1):

```sh
kubectl --kubeconfig bootstrap-out/out/kubeconfig get nodes
```

Every configured node must show `Ready`.

This is also the standalone verification command — run it after any
maintenance, any time you want the cluster to prove itself.

## 13. Convergence pass

**`[unverified — needs the lab]`**

Re-run **every** stage. This no-op property is what the Hoomlab service
later relies on when it takes ownership of the cluster.

```sh
for stage in "pve form" "pve storage" "pve certs" "talos secrets" \
             "talos emit" "talos ipxe" "talos vms" "talos bootstrap" \
             "talos health"; do
  echo "== $stage"; bootstrap $stage || break
done
```

Every stage should report its steps already done and apply nothing
(`0 steps applied`, or the equivalent "nothing to do" summary). A stage
that applies something on the second pass is a convergence bug — record
which step re-fired and why.

## Files you own

| Path | Written by | Rule |
| --- | --- | --- |
| `bootstrap.hcl` | you | the source of truth; no secret values |
| `secrets.yaml` | `talos secrets` | never overwritten; back it up; gitignored |
| `bootstrap-out/booty/**` | `talos emit` / `talos ipxe` | regenerate freely; copy to the booty host |
| `bootstrap-out/out/{talosconfig,kubeconfig}` | `talos bootstrap` | never overwritten; gitignored |

## Troubleshooting

**A stage errors partway through.** Re-run it. Every stage converges:
completed steps check as done and are skipped, so a re-run picks up
where the failure was. There is no state file to clean up.

**`docker: ... not found` when running `booty-run.sh`.** The image
reference does not resolve. booty's GHCR tags carry no leading `v`
(`booty:0.2.1`, never `booty:v0.2.1`); the CLI strips it, but a
hand-edited launcher or a `BOOTY_IMAGE` override might not.

**`includes invalid characters for a local volume name` during
`talos ipxe`.** docker reads a relative `--volume` source as a *named
volume* rather than a host path. The CLI resolves its mounts to
absolute paths; if you see this, you are running a build from before
that fix.

**`server certificate verification failed. CAfile: none` during
`talos ipxe`.** The builder image ships no trust store, so the iPXE
clone over HTTPS cannot verify GitHub. The build script installs
`ca-certificates`; seeing this means an older build.

**`gcc: error: unrecognized command-line option '-m64'` during
`talos ipxe`.** The builder ran on arm64 and its gcc cannot target
x86-64. The build pins `--platform linux/amd64`; on Apple Silicon that
runs emulated and is slow but correct.

**A node PXE boots but is handed nothing.** Its MAC does not match any
catalog group. Compare the MAC the machine reports (booty logs it) with
the one in your config — booty normalizes both sides, so a spelling
difference is not the cause, but a *wrong* address is. Confirm with
`curl "http://<booty-host>:8080/machine-config?mac=<the-logged-mac>"`.

**A node boots the wrong or an outdated config.** booty was not
restarted after a re-emit. It reads the catalog and templates once at
startup.

**`talos bootstrap` cannot reach the endpoint.** `talos.endpoint`'s
host must resolve to the first control-plane node's address. A VIP that
only exists after bootstrap cannot serve the call that creates it.

**Certificate orders fail repeatedly.** Check the Cloudflare token's
zone permissions, and confirm you are on the staging directory while
drilling. Production rate limits are unforgiving.
