# Example bootstrap config (DESIGN-0001 Data Model).
#
# One file describes one cluster: the Proxmox nodes it is formed from,
# the ACME certificate setup, and the Talos cluster that runs on top.
# This same file is what the Hoomlab service eventually consumes to
# take ownership of the cluster — so it carries only configuration,
# never CLI concerns (output paths, verbosity are flags) and never
# secret values (secrets are env() references, resolved at load time;
# export the HOOMLAB_* variables before running any stage).
#
# Every VM is declared explicitly, MAC included: the config is the
# single source of identity for both the PVE API calls and the emitted
# booty artifacts, so the PXE identity binding agrees by construction.

cluster "homelab" {
  # ── Stage 1: pve form ─────────────────────────────────────────────
  pve {
    # API token for every PVE read and most writes. PVE reserves a
    # few endpoints for the literal root@pam user — cluster create,
    # cluster join, and ACME account registration — and those dial
    # with root_password instead (joins additionally because a token
    # would not survive the join wiping the joiner's local config).
    token_id      = env("HOOMLAB_PVE_TOKEN_ID")
    token_secret  = env("HOOMLAB_PVE_TOKEN_SECRET")
    root_password = env("HOOMLAB_PVE_ROOT_PASSWORD")

    # Exactly one node carries primary = true: the cluster is created
    # there and the remaining nodes join it, serially. address, when
    # set, becomes the corosync link0 for the join.
    node "pve-01" {
      endpoint = "https://10.0.10.11:8006"
      address  = "10.0.10.11"
      primary  = true
    }
    node "pve-02" {
      endpoint = "https://10.0.10.12:8006"
      address  = "10.0.10.12"
    }
    node "pve-03" {
      endpoint = "https://10.0.10.13:8006"
      address  = "10.0.10.13"
    }

    # Cluster storage the talos VMs reference, converged by
    # `bootstrap pve storage` (create-if-missing, update-if-drifted).
    # Declared fields are opinions; fields a block omits are never
    # touched on an existing entry. nodes = [...] restricts an entry
    # to specific hosts; sparse = true is zfspool thin provisioning.
    # Declaring ANY storage block requires every talos node's storage
    # to reference a declared block; with no blocks the stage is a
    # no-op and talos nodes may reference pre-existing storage.
    storage "local-zfs" {
      type    = "zfspool"
      pool    = "rpool/data"
      content = ["images", "rootdir"]
    }
  }

  # ── Stage 2: pve certs ────────────────────────────────────────────
  acme {
    email  = "dgifford06@gmail.com"
    domain = "pve.example.internal" # node FQDNs become <node>.<domain>
    dns    = "cloudflare"           # the blessed DNS-01 provider (ADR-0001)
    token  = env("HOOMLAB_CLOUDFLARE_API_TOKEN")

    # Optional: the CA directory URL. Omitted means Let's Encrypt
    # production; point it at the staging directory while drilling so
    # failed orders don't burn production rate limits.
    # directory = "https://acme-staging-v02.api.letsencrypt.org/directory"
  }

  # ── Stages 3–5: talos secrets/emit/ipxe/vms/bootstrap/health ──────
  talos {
    version  = "v1.13.8"
    endpoint = "https://10.0.20.10:6443" # cluster VIP / endpoint

    # Optional: names the Talos cluster independently of the PVE
    # cluster (the label above, which pve form pins against the live
    # cluster). Omitted means the Talos cluster inherits the label.
    # Set it when the layers carry different names — or ever might:
    # a second Talos cluster on the same PVE cluster needs its own.
    # name = "fartlab"

    # Optional: pins the Kubernetes version the machineconfigs
    # install. Omitted means the default of the Talos machinery this
    # CLI was built against.
    # kubernetes_version = "1.36.3"

    # Optional: pin one Image Factory schematic ID for every node's
    # boot assets and installer image. Omitted means the vanilla
    # no-extensions schematic for the version above. Mutually
    # exclusive with the profile blocks below, which derive schematics
    # from extension sets instead.
    # schematic_id = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"

    # Optional: the cluster-completion surface (DESIGN-0002). The
    # block's presence turns on the completion knobs in the emitted
    # machineconfigs — topology labels (region = the PVE cluster
    # label, deliberately shared by anything on that iron; zone = node)
    # and kubelet serving-certificate rotation with its pinned
    # approver manifest — and cni selects the cluster network. With
    # "cilium" the machineconfigs disable the built-in CNI and
    # kube-proxy and install Cilium at first bootstrap: Gateway API
    # CRDs at the pinned version, then a one-shot install Job running
    # cilium-cli with the values file (path relative to this config,
    # validated at load — KubePrism endpoint and kube-proxy
    # replacement are enforced there).
    cluster {
      cni = "cilium" # "cilium" | "flannel" (default) | "none"
      cilium {
        version             = "v1.20.1"
        values              = "cilium-values.yaml"
        gateway_api_version = "v1.6.1"
        # Optional: the cilium-cli image the install Job runs.
        # Omitted means the release this CLI was tested against.
        # cli_version = "v0.19.7"
      }
    }

    # Network planes (DESIGN-0004): a network block states the shared
    # facts once — vlan (omit for untagged: the bridge and switch
    # port profile own membership), dhcp (required; every plane
    # states its mode), primary (at most one plane; its member
    # interface is each node's PXE path and booty identity), cidr
    # (required exactly when dhcp = false), mtu (optional; rendered
    # into the VM NIC and machineconfig when set). Node interfaces
    # reference a plane by name and inherit its facts whole — never
    # overridden, per the XOR rule below.
    network "servers" {
      vlan    = 11 # tagged into the guest trunk (native VLAN: none)
      dhcp    = true
      primary = true
    }

    # A static plane, for a second NIC per node — e.g. a dedicated
    # storage network with jumbo frames and no DHCP anywhere:
    # network "storage" {
    #   dhcp = false             # no vlan: untagged — the switch port
    #   cidr = "10.0.30.0/24"    # profile owns membership
    #   mtu  = 9000              # jumbo; verify the fabric end to end
    # }

    # Optional: named, composable extension profiles. Nodes reference
    # them below; emit flattens each node's set, resolves it to an
    # Image Factory schematic, and bakes the extensions into that
    # node class's boot and installer images — the only point where
    # that decision can be made. base: the guest agent because every
    # node is a PVE VM, iscsi-tools on every node because the CSI
    # node plugin mounts volumes wherever pods land.
    profile "base" {
      extensions = [
        "siderolabs/qemu-guest-agent",
        "siderolabs/iscsi-tools",
      ]
    }

    booty {
      # Where the operator runs the booty container; the emitted
      # embed.ipxe chain script and catalog point here.
      url = "http://10.0.10.5:8080"

      # Optional: pins the booty container image in the emitted
      # booty-run.sh. Omitted means the release this CLI was tested
      # against.
      # version = "v0.3.0"
    }

    # One node block per VM, everything explicit. role is
    # "controlplane" or "worker" (at least one controlplane). vmid is
    # unique, >= 100. Each NIC is a network_interface block labeled
    # with its PVE slot (net0, net1, …); mac is globally unique in
    # any standard notation (the CLI normalizes to lowercase-colon
    # form everywhere it is used), bridge is per-NIC, and the mode
    # facts arrive exactly one of two ways (the XOR rule — setting
    # both is an error, never an override):
    #
    #   referenced:  network = "servers"     # inherits the plane whole
    #   inline:      dhcp = true             # states every fact itself
    #                primary = true          # (vlan/cidr/mtu optional)
    #
    # Static planes additionally require address (CIDR form, inside
    # the plane's cidr); dhcp planes forbid it. Exactly one interface
    # per node resolves primary — that NIC is the PXE boot path.
    node "cp-01" {
      role     = "controlplane"
      pve_node = "pve-01"
      vmid     = 200
      cores    = 4
      memory   = 8192 # MiB
      disk_gb  = 64
      storage  = "local-zfs"
      # profiles names the extension profiles baked into this node's
      # boot image; omit for the vanilla (or schematic_id) image.
      profiles = ["base"]

      network_interface "net0" {
        network = "servers"
        mac     = "02:50:99:a2:00:01"
        bridge  = "vmbr0"
      }

      # The storage-plane second NIC, when the plane above is real:
      # network_interface "net1" {
      #   network = "storage"
      #   mac     = "02:50:99:a2:14:01"
      #   bridge  = "storbr0"           # dedicated bridge — untagged
      #   address = "10.0.30.51/24"     # static: the plane has no DHCP
      # }
    }
    node "cp-02" {
      role     = "controlplane"
      pve_node = "pve-02"
      vmid     = 201
      cores    = 4
      memory   = 8192
      disk_gb  = 64
      storage  = "local-zfs"
      profiles = ["base"]

      network_interface "net0" {
        network = "servers"
        mac     = "02:50:99:a2:00:02"
        bridge  = "vmbr0"
      }
    }
    node "cp-03" {
      role     = "controlplane"
      pve_node = "pve-03"
      vmid     = 202
      cores    = 4
      memory   = 8192
      disk_gb  = 64
      storage  = "local-zfs"
      profiles = ["base"]

      network_interface "net0" {
        network = "servers"
        mac     = "02:50:99:a2:00:03"
        bridge  = "vmbr0"
      }
    }
    node "worker-01" {
      role     = "worker"
      pve_node = "pve-01"
      vmid     = 300
      cores    = 8
      memory   = 16384
      disk_gb  = 128
      storage  = "local-zfs"
      profiles = ["base"]

      # The inline form, equally valid — the primitives are always
      # reachable without declaring a plane:
      network_interface "net0" {
        mac     = "02:50:99:a2:01:01"
        bridge  = "vmbr0"
        vlan    = 11
        dhcp    = true
        primary = true
      }
    }
  }
}
