# bootstrap

Operator runbook for the Hoomlab bootstrap CLI: bare Proxmox nodes in,
a healthy Talos Kubernetes cluster out. Implements ADR-0001 /
DESIGN-0001; phase tracking in IMPL-0001.

The CLI is deliberately operator-driven. Stages are separate commands
you run, inspect, and re-run. Every stage **converges**: it checks the
world, applies only what is missing, and is always safe to re-run —
there is no state file; the config and the world are the only truths.
`--dry-run` on any stage surveys what would happen without writing.

## Prerequisites

Have these before starting; each is a documented step, not a hidden
assumption:

- **PVE nodes installed and reachable.** Proxmox VE on every node,
  API reachable at the endpoints in the config. Nodes other than the
  primary must be fresh installs — joining wipes a node's local
  configuration.
- **An API token and the root password.** The token
  (`HOOMLAB_PVE_TOKEN_ID` / `HOOMLAB_PVE_TOKEN_SECRET`) drives every
  PVE call except joins; joins are issued on the joining node and need
  an existing member's `root@pam` password
  (`HOOMLAB_PVE_ROOT_PASSWORD`).
- **A Cloudflare API token** (`HOOMLAB_CLOUDFLARE_API_TOKEN`) with
  DNS-edit on the certificate domain's zone, for DNS-01 challenges.
- **A trusted, isolated boot network.** booty's `/machine-config`
  endpoint is unauthenticated plaintext HTTP and the served configs
  carry the cluster PKI and join tokens — the standard `talos.config`
  metal trade-off. The boot segment must be yours alone.
- **A booty host with docker** on that network, reachable at the
  `talos.booty.url` from the config. Real PXE needs `--net=host`, so
  the host's IP is the `--server-ip` the emitted launcher uses.
- **DHCP reservations for the Talos VMs.** The config pins each VM's
  MAC; reserve addresses for those MACs, and make sure
  `talos.endpoint`'s host resolves to the **first control-plane
  node's** reserved address (bootstrap and health dial it; a VIP that
  only exists after bootstrap will not work).
- **docker on the workstation** running the CLI — `talos ipxe` builds
  the boot binary in a container.

## Configuration

One HCL file describes one cluster — see the annotated
[`examples/bootstrap.hcl`](examples/bootstrap.hcl). Secrets never
appear as values: secret-bearing attributes carry `env("HOOMLAB_…")`
references resolved at load time, so export these before any stage:

```sh
export HOOMLAB_PVE_TOKEN_ID='root@pam!bootstrap'
export HOOMLAB_PVE_TOKEN_SECRET='…'
export HOOMLAB_PVE_ROOT_PASSWORD='…'
export HOOMLAB_CLOUDFLARE_API_TOKEN='…'
```

Global flags (CLI concerns, deliberately not config): `--config`
(default `bootstrap.hcl`), `--output` (default `./bootstrap-out`),
`--secrets` (default `secrets.yaml` next to the config), `--dry-run`,
`--log-level`.

## The stage flow

Run the stages in order. Each prints the next one when it finishes.

```text
validate → pve form → pve certs → talos secrets → talos emit
        → talos ipxe → [start booty] → talos vms
        → talos bootstrap → talos health
```

### 1. `bootstrap validate`

Loads and validates the config, resolving every `env()` reference.
Fix everything it reports before touching an API.

### 2. `bootstrap pve form`

Creates the cluster on the primary node, joins the remaining nodes one
at a time (each join waits for corosync membership and quorum), and
verifies the formed cluster is quorate. Interruption-safe: re-run and
it picks up at the first unjoined node.

### 3. `bootstrap pve certs`

ACME account, Cloudflare DNS-01 plugin, per-node certificate domains
(`<node>.<domain>`), and certificate orders. Renewal is the same
command re-run — a certificate under 30 days of validity goes pending
again. While drilling, set the commented `acme.directory` to Let's
Encrypt staging so failed orders don't burn production rate limits.

### 4. `bootstrap talos secrets`

Generates the Talos secrets bundle — **the cluster identity** — at
`--secrets`. Runs once; an existing file is never overwritten, because
regenerating it orphans every node holding the old identity. Back this
file up and treat it like a private key.

### 5. `bootstrap talos emit`

Renders everything booty serves, under `<output>/booty/`:

```text
booty/
├── catalog/            # 00-variables / 10-profiles / 20-groups
├── templates/talos/    # complete, secret-bearing machineconfig templates
├── boot/talos/<ver>/   # Image Factory vmlinuz + initramfs.xz
├── embed.ipxe          # the chain script ipxe.efi embeds
└── booty-run.sh        # ready-to-run launcher (the sharp edges encoded)
```

Re-run any time; the check is a byte-diff. **When anything changed,
restart the booty container** — booty loads the catalog and templates
once at startup, and a re-emit nobody restarts serves stale configs.

### 6. `bootstrap talos ipxe`

Builds `<output>/booty/boot/ipxe.efi` in a container (pinned iPXE
source, the emitted `embed.ipxe` baked in). Takes a few minutes the
first time; afterwards it only rebuilds when `talos.booty.url`
changes.

### 7. Start booty (operator step)

Copy `<output>/booty/` to the booty host and run `./booty-run.sh`
there. The script encodes the operational sharp edges (`--net=host`,
a port-capable user, the correct `--catalog` flag, `--proxydhcp
--server-ip`). Verify it serves before creating VMs:

```sh
curl http://<booty-host>:8080/boot.ipxe
curl "http://<booty-host>:8080/machine-config?mac=<a-configured-mac>"
```

### 8. `bootstrap talos vms`

Creates and starts every configured VM on its Proxmox node. The
settings are deliberate — UEFI without pre-enrolled Secure Boot keys,
VirtIO RNG, `cpu=host`, boot order disk-then-net — each one required
for the PXE → install → boot-from-disk cycle to work. The VMs PXE
boot from booty, install Talos, and reboot into it; watch progress on
the booty logs or `qm terminal <vmid>`.

Re-imaging a node later is: wipe its disk, reboot — the firmware falls
back to PXE and the cycle repeats.

### 9. `bootstrap talos bootstrap`

One-time etcd bootstrap against the endpoint host, then credentials
under `<output>/out/`: `talosconfig` (from the secrets bundle) and
`kubeconfig` (fetched from the live cluster). "Already bootstrapped"
is success; existing credential files are never overwritten.

### 10. `bootstrap talos health`

Blocks until the cluster's own health check passes (`--wait`, default
10m). This is also the standalone verification command. When it
passes:

```sh
kubectl --kubeconfig <output>/out/kubeconfig get nodes
```

Every configured node `Ready` is RFC-0001's Phase 1 success criterion.

## Convergence check

After a full pass, run every stage again. Each should report all steps
done and apply nothing — that no-op property is what the Hoomlab
service later relies on when it takes ownership of the cluster.

## Files the operator owns

| Path | Written by | Rule |
| --- | --- | --- |
| `secrets.yaml` | `talos secrets` | never overwritten; back it up; gitignored |
| `<output>/booty/**` | `talos emit` / `talos ipxe` | regenerate freely; copy to the booty host |
| `<output>/out/{talosconfig,kubeconfig}` | `talos bootstrap` | never overwritten; gitignored via `bootstrap-out/` |

## Development

This is a nested Go module with its own CI (`tools-ci.yml`) and
on-demand releases (`tools-release.yml` → `tools/bootstrap/vX.Y.Z`).

```sh
just bootstrap-build   # build into build/bin/bootstrap
just bootstrap-test    # go test ./...
just bootstrap-lint    # golangci-lint
mise x -- mockery      # regenerate internal/talos/mocks
```
