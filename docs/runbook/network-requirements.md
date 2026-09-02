# Network requirements — cluster planes

The network contract the fartlab Talos cluster depends on, in
assertable form. Everything here was live-verified 2026-09-02
(IMPL-0003 Phases 1–2); the per-host cabling and current state live
in the [fleet NIC map](fleet-nic-map.md). Rationale lives in
DESIGN-0004 and IMPL-0003 — this doc is what must be true and how
to prove it, written so the homelab Terraform modules can enforce
the UniFi side as code instead of memory.

## The two guest planes

| Plane | VLAN | Subnet | L3 | DHCP | MTU | Guest bridge | Tagging |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Servers | 11 | 10.10.11.0/24 | routed (UCG `10.10.11.1`) | yes | 1500 | `vmbr1` | guest NICs tag 11 (trunk native None) |
| Storage | 14 | 10.10.13.0/24 | **none — L2-only** | **none** | 9000 | `storbr0` | untagged (ports native Storage, block tagged) |

## R1 — Servers network (VLAN 11)

- Routed network on the UCG, gateway `10.10.11.1`, DHCP server ON —
  PXE depends on it (booty's proxyDHCP rides this broadcast domain;
  the DHCP offer is what starts every node's boot).
- booty serves at `http://10.10.11.190:8080` on this VLAN.
- Fixed-IP entries for the node MACs (`.51–.53`, `.61–.63`) are
  documentation plus collision insurance — the nodes take real
  leases here (net0 is DHCP by design).

## R2 — Storage network (VLAN 14): L2-only, by construction

- **Third-party gateway** (Terraform: `unifi_network` with
  `purpose = "vlan-only"`): no UCG L3 interface exists on this
  network. `10.10.13.1` must not answer — the plane's
  unroutability is the storage access boundary, and it must be
  structural, not a firewall rule.
- **DHCP Mode: None.** No server, no relay. All addressing is
  static, held by configs (Talos machineconfigs for guests, the
  hosts' `/etc/network/interfaces` for `storbr0`) — never by UniFi.
- **No internet access.** Nothing on this plane ever egresses.
- The subnet `10.10.13.0/24` is a convention of those configs;
  UniFi carries only the VLAN.
- Jumbo (9000) end-to-end — the chain is R3 + R4 + R5, and it is
  all-or-nothing: a single 1500 link blackholes large frames
  silently (see Validation for the proof command).

### Changed 2026-09-02 — as-found vs. as-required

As found, the network violated all of the above: routed L3 on the
UCG (`10.10.13.1`, internet access allowed), DHCP server with pool
`.6–.254` (containing every planned static), and Auto Default
Gateway offering `10.10.13.1` as a default route. It surfaced as an
implicit-DHCP lease (`10.10.13.222`) on a hot-plugged NIC — Talos
runs DHCP on any unconfigured link, so the whole fleet had silently
joined. Fixed at the UCG: DHCP off, internet access off, network
converted to third-party gateway. The deviation record is in
IMPL-0003 Phase 2.

## R3 — Switch (UniFi)

| Port profile | Semantics | Ports |
| --- | --- | --- |
| `pve-storage` | native Storage (14), **block all tagged** | Agg 4 (r740a stor0), Agg 1 (r640a stor0), Agg 5 (srv01 stor0) |
| `pve-guest-trunk` | tagged Servers (11) **only**, native None | Agg 6 (r740a vm0), Agg 3 (r640a vm0), Agg 2 (srv01 vm0) |

- Jumbo frames enabled on the aggregator (a switch-wide setting;
  each port/server end then opts in explicitly).
- Both profiles fail closed, and that is the point: an untagged
  guest frame on the trunk dies (native None), a tagged frame on a
  storage port dies (block all tagged). `storbr0` is the **only**
  guest path to the storage plane.

## R4 — PVE host fabric

- `storbr0` bridge enslaving `stor0` on every host, `mtu 9000` on
  **both** stanzas in `/etc/network/interfaces` (Ansible-managed,
  roles/network). Host addresses: r740a `10.10.13.20`, r640a
  `10.10.13.21`, srv01 `10.10.13.40`.
- `vmbr1` VLAN-aware on `vm0`, MTU 1500, never addressed —
  guest-only.
- r640a caution: its `qede` SFP+ NICs can wedge into a carrier
  flap loop on **live** MTU changes (hit 2026-09-01; recovery
  documented in roles/network). Change MTU via config + reload in
  a window, one host at a time.

## R5 — Guest NICs (every node, both layers)

PVE side:

```text
net0: virtio=<net0-mac>,bridge=vmbr1,tag=11,firewall=0          # 1500; the ONLY NIC in boot order
net1: virtio=<net1-mac>,bridge=storbr0,firewall=0,mtu=9000      # untagged; never in boot order
```

Talos side — both NICs declared, selection by
`deviceSelector.hardwareAddr` only (interface names are not a
contract), net1 with **no routes, no gateway, no DNS**:

```yaml
machine:
  network:
    interfaces:
      - deviceSelector:
          hardwareAddr: "<net0-mac>"
        dhcp: true
      - deviceSelector:
          hardwareAddr: "<net1-mac>"
        dhcp: false
        mtu: 9000
        addresses:
          - "<storage-address>/24"
```

Hand-applied today (IMPL-0003 Phase 2); `bootstrap` ≥ v0.3.0
renders both layers from config (DESIGN-0004).

## R6 — Address plan

| Host / Node | Storage address | net1 MAC |
| --- | --- | --- |
| r740a (portal — ZFS) | 10.10.13.20 | — (storbr0) |
| r640a | 10.10.13.21 | — (storbr0) |
| srv01 | 10.10.13.40 | — (storbr0) |
| ctrl01 | 10.10.13.51/24 | 02:50:99:a2:14:c9 |
| ctrl02 | 10.10.13.52/24 | 02:50:99:a2:14:ca |
| ctrl03 | 10.10.13.53/24 | 02:50:99:a2:14:cb |
| work01 | 10.10.13.61/24 | 02:50:99:a2:14:2d |
| work02 | 10.10.13.62/24 | 02:50:99:a2:14:2e |
| work03 | 10.10.13.63/24 | 02:50:99:a2:14:2f |

No DHCP pool exists anywhere in `10.10.13.0/24` — an address on
this plane that is not in this table is a fault (see Validation).

## Validation

Run after any change to the networks, port profiles, host stanzas,
or node configs. Every command below was executed against the real
cluster 2026-09-02 with the outputs described.

### Fabric (from a PVE host)

```sh
ip link show storbr0            # mtu 9000
# 9000-byte do-not-fragment ping, host ↔ host and host ↔ every node.
# 8972 = 9000 - 20 (IP) - 8 (ICMP). Silence = a 1500 link is lurking.
for i in 51 52 53 61 62 63; do ping -M do -s 8972 -c 2 10.10.13.$i; done
```

Expected: `8980 bytes from 10.10.13.<n>` for all six, sub-ms RTT,
0% loss. From r740a this doubles as the portal-path proof — the
pings traverse the literal iSCSI route.

### Talos (from a machine with the talosconfig)

```sh
# 1. Static addresses — exactly the R6 table on ens19 (plus fe80)
for ip in 51 52 53 61 62 63; do talosctl -n 10.10.11.$ip get addresses | rg ens19; done

# 2. Default routes — one per node, via 10.10.11.1 on ens18, and
#    NONE via the storage plane. talosctl renders the default route
#    with an EMPTY destination: `inet4/10.10.11.1//1024` IS 0.0.0.0/0
#    (don't grep for "/0/" — it will never match).
for ip in 51 52 53 61 62 63; do talosctl -n 10.10.11.$ip get routes | rg '10.10.11.1'; done

# 3. Guest jumbo — six lines of "mtu: 9000"
for ip in 51 52 53 61 62 63; do talosctl -n 10.10.11.$ip get links ens19 -o yaml | rg '^\s*mtu'; done

# 4. iscsid — ext-iscsid Running on all six
for ip in 51 52 53 61 62 63; do talosctl -n 10.10.11.$ip services | rg iscsid; done

# 5. Config carries exactly the node's own two selectors
for ip in 51 52 53 61 62 63; do talosctl -n 10.10.11.$ip get machineconfig -o yaml | rg hardwareAddr | sort -u; done
```

### Fault signatures (all hit for real on 2026-09-02)

- **Rogue DHCP on the storage plane**: an ens19 address outside the
  R6 table (e.g. `10.10.13.222`) — Talos leases on any unconfigured
  link. The fingerprint: the DHCP-installed connected route carries
  metric 1024 (`inet4//10.10.13.0/24/1024`); the static one carries
  metric 0. Fix is at the UCG (R2), not on the node.
- **Inert machineconfig patch**: a `deviceSelector` that matches no
  NIC on the node is silently ignored — `talosctl patch` reports
  success either way. Check 5 above is the detector; the node/file
  pairing must move together. Talos strategic-merges the interfaces
  list keyed by selector, so wrong-node patches *accumulate* (clean
  up with `talosctl edit machineconfig` — JSON6902 is refused on
  multi-document configs).
- **Transient default-route gap**: applying an interfaces block
  drops the node's default route for ~1–2 minutes while ens18's
  DHCP renegotiates. Same-subnet traffic (etcd, apid, kubelet↔API)
  is unaffected; it self-heals. Recheck before declaring a fault.

## Terraform assertion notes

What the homelab modules should pin, per requirement:

- R2: `unifi_network` for Storage — `purpose = "vlan-only"`,
  `vlan_id = 14`, no subnet/DHCP/gateway attributes at all.
- R1: `unifi_network` for Servers — routed with DHCP enabled, plus
  `unifi_user` fixed-IP entries for the node MACs.
- R3: `unifi_port_profile` ×2 (`pve-storage`: native Storage,
  tagged-blocked; `pve-guest-trunk`: native None, tagged = Servers
  only) and `unifi_port_override`/port assignments for the six Agg
  ports per the table.
- Aggregator jumbo is a device/site-level setting — check provider
  support; if absent, it stays a documented manual step verified by
  the DF-ping above.

## References

- DESIGN-0004 — network planes and interfaces (the design this
  contract feeds)
- IMPL-0003 — Phases 1–2 execution and deviation records
- [fleet NIC map](fleet-nic-map.md) — per-host NICs, ports, MACs
- [bootstrap-cluster.md](bootstrap-cluster.md) — the CLI runbook
- DESIGN-0006 / DESIGN-0008 (homelab docs) — address table, booty
  deployment
