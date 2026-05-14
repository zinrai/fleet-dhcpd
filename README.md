# fleet-dhcpd

An HA DHCP server for iPXE-based physical server provisioning. Multiple
instances share lease state through Consul KV, so any single instance
can be restarted without service interruption.

The motivation was to have a production DHCP server that can be
restarted at any time. The architecture of sabakan fit this requirement,
so fleet-dhcpd is built on it.

The architecture of fleet-dhcpd is derived from [sabakan][sabakan]:
multiple DHCP instances share lease state through a consensus-backed
key-value store, with no instance-level state. The DHCP handler in
`internal/dhcp/` is adapted from sabakan's `dhcpd/` package. Lease state
is shared through Consul KV (sabakan uses etcd).

[sabakan]: https://github.com/cybozu-go/sabakan

## Scope

- DHCPv4 message handling: DISCOVER, REQUEST, DECLINE, RELEASE, INFORM
- iPXE client detection (Option 77 User Class)
- BootFilename in the DHCP response pointing to a configured URL
- Shared lease state in Consul KV with CAS-based concurrency control
- Configuration in Consul KV, read on startup and watched for changes
- Optional webhook notification on lease state changes

## Bootstrap

Write the configuration into Consul KV. See `config.json.example` for the
schema.

    consul kv put fleet-dhcpd/clusters/provisioning/config @config.json

## Run

    sudo fleet-dhcpd -kv-prefix fleet-dhcpd/clusters/provisioning -interface eno1

Required capabilities: `CAP_NET_BIND_SERVICE` (UDP port 67) and
`CAP_NET_RAW` (raw socket for DHCP packet I/O).

Consul connection follows standard Consul environment variables:
`CONSUL_HTTP_ADDR`, `CONSUL_HTTP_TOKEN`, etc. Defaults to
`localhost:8500`.

## Flags

| Flag | Required | Description |
|---|---|---|
| `-kv-prefix` | yes | Consul KV prefix containing `config` and `leases` keys |
| `-interface` | yes | Network interface to listen on |
| `-listen` | no | Address to bind for DHCP, default `0.0.0.0:67` |

## Configuration schema

JSON written to `<kv-prefix>/config`:

| Field | Type | Required | Description |
|---|---|---|---|
| `subnet` | string | yes | Subnet in CIDR notation |
| `range_start` | string | yes | First IP in the offer range |
| `range_end` | string | yes | Last IP in the offer range |
| `router` | string | yes | Default gateway offered to clients |
| `dns` | []string | no | DNS servers offered to clients |
| `domain_name` | string | no | Domain name offered to clients |
| `lease_time` | string | no | Duration string, default `1h` |
| `boot_url` | string | no | iPXE boot URL returned in BootFilename |
| `webhook_url` | string | no | HTTP(S) URL for lease event notifications |

Configuration is reloaded automatically when the Consul KV value changes.
Invalid updates are logged and ignored; the previous valid configuration
continues in effect.

## Webhook payload

If `webhook_url` is configured, the bundled webhook observer POSTs the
following JSON on every lease change:

    {
      "type": "offer" | "ack" | "release" | "decline",
      "ip": "192.168.10.123",
      "mac": "52:54:00:aa:bb:cc",
      "expires_at": "2026-05-11T13:00:00Z",
      "timestamp": "2026-05-11T12:00:00Z"
    }

Delivery is best-effort and asynchronous; failures are logged and
discarded. In an HA deployment, each lease change is notified exactly
once across the cluster (the instance whose CAS succeeded sends the
event).

## Operations

Rolling restart for maintenance:

    # On host A
    systemctl stop fleet-dhcpd
    # ... apply update ...
    systemctl start fleet-dhcpd
    # On host B (after host A is back)
    systemctl stop fleet-dhcpd
    systemctl start fleet-dhcpd

Update configuration cluster-wide:

    consul kv put fleet-dhcpd/clusters/provisioning/config @new-config.json

All instances detect the change via blocking query and reload.

Decommission:

    systemctl stop fleet-dhcpd
    consul kv delete -recurse fleet-dhcpd/clusters/provisioning

## License

Apache License 2.0. See [LICENSE](./LICENSE) and [NOTICE](./NOTICE).

DHCP handler code is adapted from [sabakan][sabakan] (Apache 2.0).
