# hoomlab bootstrap handoff

> **How to read this document (2026-08-23):** the **Current state** section
> is this file's value — the verified snapshot of the three nodes after the
> last Ansible run, to mirror in the bootstrap config. The **Target state**
> sections were written without full knowledge of hoomlab's design and are
> **superseded by DESIGN-0001 / IMPL-0002 wherever they disagree** (boot
> order, Talos placement, principal/1Password flow, formation sequencing,
> phase structure). DESIGN-0007/0009 are not requirements docs for this CLI.
> Facts that don't contradict hoomlab's design (decided cluster name and
> corosync links, VMID convention, dataset layout, network state) remain
> useful input.

Input for hoomlab's bootstrap implementation: the live state of the three PVE
nodes as of **2026-08-23**, the exact target each layer must be converged to,
and the human prerequisites that are neither Ansible's nor hoomlab's to do.
[DESIGN-0007](docs/design/0007-pve-cluster-target-state-for-hoomlab-bootstrap.md)
is the authoritative requirements doc; this file is the verified snapshot to
build and test against. The ownership rule carried over from it: hoomlab owns
everything behind the PVE API, asserts the host layer below it (datasets,
bridges), and **fails pointing at this repo** rather than fixing the host
itself.

## Current state — verified live 2026-08-22/23

Identical on all three nodes (`r740a` 10.10.11.20, `r640a` 10.10.11.21, `srv01`
10.10.11.40) unless noted:

- **No cluster.** `/etc/pve/corosync.conf` absent everywhere.
- **Zero guests** on all three (`qm`/`pct` empty — the talos-pxe-test VM is
  gone). Join preconditions hold today.
- **`datacenter.cfg`** is one line: `keyboard: en-us`.
- **`storage.cfg`** is untouched installer output, byte-identical on all three:

  ```text
  dir: local
          path /var/lib/vz
          content iso,vztmpl,backup,import

  zfspool: local-zfs
          pool rpool/data
          sparse
          content images,rootdir
  ```

- **Principals:** `root@pam` only. One delta: `r740a` carries an API token
  `root@pam!sdk` (privsep=0, no expiry) from proxmox-go-sdk development.
- **Host layer (Ansible-owned) is READY:**
  - `fast/vm` and `tank/vm` datasets exist on both dells (created 2026-08-22).
    On `r740a` they sit beside `fast/garage-meta` and `tank/garage` — the live
    Garage instance PVE must never touch.
  - `srv01` has no data pools; its `rpool/data` is a 930G NVMe mirror.
  - `vmbr1` (VLAN-aware, on the 10 GbE `vm0`, no address) is up on all three.
    `vmbr0`/mgmt is the API path and carries no guests.
  - `stor0` (10.10.13.20/.21/.40) and `sync0` (10.10.15.20/.21/.40) are up on
    all three — srv01's sync0 is a VLAN riding mgmt0, DESIGN-0009's combined
    control-plane copper for 3-NIC hosts.
  - **East-west fabric: COMPLETE, verified 2026-08-23** (it had never worked
    before this week — ARP flux masked it). Storage and Sync pass all-pairs
    across all three nodes; all three vm0 trunks pass tagged 11+14 probes;
    srv01's stor0 runs at its full 10G on Aggregation Port 5. Port model,
    profiles, and the LLDP-verified map:
    docs/design/0009-unifi-port-profiles-and-the-fleet-port-map.md.

## Target state — what the bootstrap converges

### Phase 1 — formation (runs as `root@pam`, create-once behind asserts)

| Step | Detail                                                                                                                                                                                                    |
| ---- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1    | Create cluster on `r740a`, name **`shart`** (Q2 DECIDED 2026-08-22: shart.sh is the homelab's domain; fartlab.dev is reserved for the Talos cluster).                                                     |
| 2    | Join `r640a`, then `srv01`, strictly serial, **by IP** (DNS cutover has not happened). Corosync links per Q1, DECIDED 2026-08-23: link0 = Sync (10.10.15.20/.21/.40), link1 = mgmt (10.10.11.20/.21/.40). |
| 3    | Per-node preconditions asserted immediately before each join: zero guests, no corosync.conf, time in sync, target fingerprint fetched and pinned.                                                         |
| 4    | Quorum after: 3 nodes, 1 vote each. Never set `expected_votes`. No hardcoded node counts — `m01`/`m02` make it five later by re-running the same code.                                                    |

A join replaces the joining node's `/etc/pve`; all storage/datacenter work is
strictly **after** all three are members (the installer-default files are
identical, so the merge is benign).

### Phase 2 — converge (runs as the `hoomlab` token, idempotent, re-runnable)

**`datacenter.cfg`:**

```text
keyboard: en-us                          # converge, don't re-set
migration: secure,network=10.10.13.0/24  # storage VLAN — else migrations ride 1 GbE mgmt
```

**`storage.cfg`** (cluster-wide file, every entry carries explicit `nodes`):

```text
dir: local
        path /var/lib/vz
        content iso,vztmpl,import        # backup REMOVED (targets are R7, deferred);
                                         # snippets deliberately absent (booty serves configs)

zfspool: local-zfs
        pool rpool/data
        sparse
        content images,rootdir
        nodes srv01                      # BOSS guard: on the dells rpool/data is the
                                         # 223.5G BOSS VD and must never take a VM disk

zfspool: fast
        pool fast/vm                     # child dataset, NEVER the pool root —
        sparse                           # Garage lives beside it on r740a
        content images,rootdir
        nodes r740a,r640a

zfspool: tank
        pool tank/vm                     # bulk is an explicit choice, never a default;
        content images,rootdir           # not sparse, per DESIGN-0007 R3
        nodes r740a,r640a
```

Before creating `fast`/`tank`: assert the `fast/vm`/`tank/vm` datasets exist on
both dells and hard-fail naming `host_vars/<node>.yml` if not. They exist today.
Storage IDs are stable API names — `fast`/`tank` on BOTH dells, no
primary/secondary at this layer (that vocabulary belongs to the Talos
StorageClasses one layer up).

**Principal (created in formation, used ever after):** user `hoomlab@pve`,
minimal role (cluster/storage/pool admin + full VM lifecycle — enumerate in
hoomlab's own doc), ACL at `/`, API token. Every post-formation and re-run
operation uses the token, never root. Token stored as 1Password item
`pve-hoomlab-api` in the `homelab` vault — username = full token id (e.g.
`hoomlab@pve!bootstrap`), password = secret. Resolution failure is a hard fail,
never a skip.

**Scaffolding:** resource pool `talos` (first tenant; further pools arrive with
their consumers). VMID convention per Q4, DECIDED 2026-08-23: 100–199 infra,
200–299 Talos control plane, 300–399 Talos workers, 900–999 templates — owned
and enforced by hoomlab. No VM templates and no image imports for Talos:
empty-disk VMs, NIC-first boot, booty serves kernel/initramfs/machine-config,
Talos installs itself. Placement intent: Talos on `r640a` puts disks on that
node's `fast`; `srv01` guests use `local-zfs`; `tank` is opt-in bulk.

## Human todos (not Ansible's, not hoomlab's)

1. ~~Seed the root credential in 1P~~ **DONE 2026-08-23**: `pve-root` in the
   `homelab` vault (Login, username=`root@pam`, password verified present). One
   item covers all three nodes — the answer files set the same root password
   fleet-wide. `pve-hoomlab-api` still does not exist and is NOT hand-created:
   the bootstrap mints `hoomlab@pve` + token at formation and writes it then. No
   root tokens beyond pvelab's `root@pam!sdk` — R5's dedicated-user flow,
   deliberately.
2. ~~Decide Q4~~ **DONE 2026-08-23**: (a), ranges by role — stamped in
   DESIGN-0007 alongside Q1 and Q2. Every open question this bootstrap depends
   on is now decided.
3. ~~Finish srv01's east-west~~ **DONE 2026-08-23**: stor0 recabled to
   Aggregation Port 5 (`pve-storage`, 10G restored), storage passes all-pairs;
   sync0 rides mgmt0 tagged (`10.10.15.40`, Ansible-converged, idempotent) and
   sync passes all-pairs. Port 21 is Disabled (the unused-port convention,
   DESIGN-0009). Q1 is stamped (2026-08-23): link0 Sync, link1 mgmt, with
   srv01's shared-copper caveat recorded.
4. ~~Guest trunks~~ **DONE, verified 2026-08-23**: USW-Aggregation Ports 2, 3, 6
   all pass tagged 11 + tagged 14 probes. The port-profile model and full
   LLDP-verified map moved to DESIGN-0009 (with the Terraform path for managing
   it as code). Per-VM DHCP reservations (the Talos endpoint `.44` and friends)
   remain a VM-creation-time concern, not port config.

5. ~~Decide the `root@pam!sdk` token's fate~~ **KEPT** (2026-08-22): it is
   pvelab's credential for nested-cluster SDK testing on r740a, out of hoomlab's
   scope — the bootstrap must neither create, rotate, nor remove it.
   Consequences (pvelab tracking hoomlab's storage restrictions; a future
   ZFS-backed `lab` dir storage for ISOs/templates/scripts) are recorded in
   DESIGN-0007 R3/R5 amendments.

## Acceptance

DESIGN-0007's finished-state checklist is the source of truth; the short version
the bootstrap must be able to assert on every re-run: quorate 3-node cluster,
migration on 10.10.13.0/24, the four storage entries above with their `nodes`
restrictions, `hoomlab@pve` token valid, pool `talos` present, and a re-run
reports zero changes.
