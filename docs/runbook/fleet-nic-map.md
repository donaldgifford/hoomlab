# Fleet nic map

## r740a — Dell R740, 2× copper + 2× SFP+ (Intel, `ixgbe`)

| NIC       | MAC                 | Speed     | Switch port (profile)       | VLAN                               | Bridge on top | Address (owner)                 | MTU  |
| --------- | ------------------- | --------- | --------------------------- | ---------------------------------- | ------------- | ------------------------------- | ---- |
| `mgmt0`   | `24:6e:96:72:8a:fc` | 1G copper | Pro-24 / 17 (`pve-mgmt`)    | Servers 11, untagged               | `vmbr0`       | — (bridge holds it)             | 1500 |
| `vmbr0`   | inherits mgmt0      | —         | —                           | —                                  | —             | **10.10.11.20** (answer file)   | 1500 |
| `sync0`   | `24:6e:96:72:8a:fd` | 1G copper | Pro-24 / 19 (`sync`)        | Sync 15, native                    | none          | **10.10.15.20** (roles/network) | 1500 |
| `stor0`   | `24:6e:96:72:8a:fa` | 10G SFP+  | Agg / 4 (`pve-storage`)     | Storage 14, native                 | `storbr0`     | — (bridge holds it)             | 9000 |
| `storbr0` | inherits stor0      | —         | —                           | —                                  | —             | **10.10.13.20** (roles/network) | 9000 |
| `vm0`     | `24:6e:96:72:8a:f8` | 10G SFP+  | Agg / 6 (`pve-guest-trunk`) | trunk: tagged 11 only; native None | `vmbr1`       | — (never addressed)             | 1500 |
| `vmbr1`   | inherits vm0        | —         | —                           | VLAN-aware; guest NICs tag per-NIC | —             | none, ever (guest-only bridge)  | 1500 |

r740a-only oddity: `vmbr0v11`/`mgmt0.11` — PVE's standard proxy-bridge plumbing
for a tagged guest on a NON-VLAN-aware bridge (a VLAN subinterface of the port,
enslaved into a per-VLAN bridge). The mechanism is normal; its presence here is
the residue: some guest once attached to `vmbr0` with `tag=11` (talos-pxe-test
or pvelab-era testing), which is off-design — guests belong on `vmbr1`, whose
VLAN-aware bridge tags without proxy bridges, and `vmbr0` stays mgmt-only
(DESIGN-0007 R4). Runtime-only state: addressless, harmless, gone at next reboot
with nothing to recreate it.

## r640a — Dell R640, 2× copper + 2× SFP+ (QLogic QL41000, `qede`)

The SFP+ pair being `qede` rather than `ixgbe` is not trivia: live MTU changes
can wedge its firmware into a carrier flap loop (2026-09-01; recovery and
detection are documented where the MTU is declared, in
`roles/network/tasks/main.yml`).

| NIC       | MAC                 | Speed     | Switch port (profile)       | VLAN                               | Bridge on top | Address (owner)                 | MTU  |
| --------- | ------------------- | --------- | --------------------------- | ---------------------------------- | ------------- | ------------------------------- | ---- |
| `mgmt0`   | `34:80:0d:48:18:6a` | 1G copper | Pro-24 / 16 (`pve-mgmt`)    | Servers 11, untagged               | `vmbr0`       | — (bridge holds it)             | 1500 |
| `vmbr0`   | inherits mgmt0      | —         | —                           | —                                  | —             | **10.10.11.21** (answer file)   | 1500 |
| `sync0`   | `34:80:0d:48:18:6b` | 1G copper | Pro-24 / 10 (`sync`)        | Sync 15, native                    | none          | **10.10.15.21** (roles/network) | 1500 |
| `stor0`   | `34:80:0d:48:18:68` | 10G SFP+  | Agg / 1 (`pve-storage`)     | Storage 14, native                 | `storbr0`     | — (bridge holds it)             | 9000 |
| `storbr0` | inherits stor0      | —         | —                           | —                                  | —             | **10.10.13.21** (roles/network) | 9000 |
| `vm0`     | `34:80:0d:48:18:69` | 10G SFP+  | Agg / 3 (`pve-guest-trunk`) | trunk: tagged 11 only; native None | `vmbr1`       | — (never addressed)             | 1500 |
| `vmbr1`   | inherits vm0        | —         | —                           | VLAN-aware; guest NICs tag per-NIC | —             | none, ever (guest-only bridge)  | 1500 |

## srv01 — 1× copper + 2× SFP+ (Intel, `ixgbe`); `wlo1` WiFi is down/unused

Three wired ports means the combined control-plane copper: mgmt untagged and
sync TAGGED share `mgmt0`, so `sync0` here is a VLAN interface riding it — same
MAC, same cable, same switch port.

| NIC       | MAC                      | Speed     | Switch port (profile)         | VLAN                               | Bridge on top | Address (owner)                 | MTU  |
| --------- | ------------------------ | --------- | ----------------------------- | ---------------------------------- | ------------- | ------------------------------- | ---- |
| `mgmt0`   | `74:56:3c:2b:d1:91`      | 1G copper | Pro-24 / 23 (`pve-mgmt-sync`) | Servers 11 untagged + Sync 15 tag  | `vmbr0`       | — (bridge holds it)             | 1500 |
| `vmbr0`   | inherits mgmt0           | —         | —                             | —                                  | —             | **10.10.11.40** (answer file)   | 1500 |
| `sync0`   | VLAN on mgmt0 (same MAC) | —         | (same port 23)                | Sync 15, tagged                    | none          | **10.10.15.40** (roles/network) | 1500 |
| `stor0`   | `1a:4b:24:a9:9a:8a`      | 10G SFP+  | Agg / 5 (`pve-storage`)       | Storage 14, native                 | `storbr0`     | — (bridge holds it)             | 9000 |
| `storbr0` | inherits stor0           | —         | —                             | —                                  | —             | **10.10.13.40** (roles/network) | 9000 |
| `vm0`     | `1a:4b:24:a9:9a:8b`      | 10G SFP+  | Agg / 2 (`pve-guest-trunk`)   | trunk: tagged 11 only; native None | `vmbr1`       | — (never addressed)             | 1500 |
| `vmbr1`   | inherits vm0             | —         | —                             | VLAN-aware; guest NICs tag per-NIC | —             | none, ever (guest-only bridge)  | 1500 |

## UniFi-facing summary — MAC ↔ IP ↔ network

The flat list for client/fixed-IP mapping. The MAC UniFi sees for each IP is the
physical NIC's (bridges inherit):

| Host  | UniFi network | MAC                            | IP          |
| ----- | ------------- | ------------------------------ | ----------- |
| r740a | Servers       | `24:6e:96:72:8a:fc`            | 10.10.11.20 |
| r740a | Sync          | `24:6e:96:72:8a:fd`            | 10.10.15.20 |
| r740a | Storage       | `24:6e:96:72:8a:fa`            | 10.10.13.20 |
| r640a | Servers       | `34:80:0d:48:18:6a`            | 10.10.11.21 |
| r640a | Sync          | `34:80:0d:48:18:6b`            | 10.10.15.21 |
| r640a | Storage       | `34:80:0d:48:18:68`            | 10.10.13.21 |
| srv01 | Servers       | `74:56:3c:2b:d1:91`            | 10.10.11.40 |
| srv01 | Sync          | `74:56:3c:2b:d1:91` (same MAC) | 10.10.15.40 |
| srv01 | Storage       | `1a:4b:24:a9:9a:8a`            | 10.10.13.40 |

Mapping caveats, so UniFi's client table does not send anyone chasing ghosts:

1. **Fixed-IP entries only do real work on the Servers network** — it is the
   only fleet VLAN with DHCP, and even there the hosts are static, so the
   entries are documentation plus collision-insurance (the pool can never hand
   those addresses to something else). Sync and Storage have no DHCP and no
   gateway; their rows only ever appear as discovered clients.
2. **srv01's repeated MAC is expected, not flux**: `74:56:3c:2b:d1:91`
   legitimately lives on both Servers and Sync because sync rides the mgmt
   copper tagged.
3. **The `vm0` NICs never carry an IP** — pure trunk plumbing under `vmbr1`.
   UniFi's network guesses for them are cosmetic noise. The reservations that
   matter for real assignment are the future Talos guests on Servers
   (`.51`–`.69`, deterministic VM MACs) — a VM-creation-time concern, per
   `HOOMLAB_BOOTSTRAP_HANDOFF.md`.
