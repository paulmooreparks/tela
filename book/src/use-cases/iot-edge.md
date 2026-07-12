# IoT and Edge Devices

You have devices deployed in the field: Raspberry Pis running sensor
software, kiosks at retail locations, industrial controllers at
manufacturing sites, point-of-sale terminals at customer premises. These
devices sit behind NATs and firewalls that you do not control and cannot
configure. Getting SSH access to any of them today means coordinating with
the site's IT team to open a port, shipping the device back, or driving
out.

With Tela, each device (or a small gateway at each site) runs `telad` and
makes an outbound connection to a central hub. From then on you can reach
any registered device from your workstation, with no firewall changes at
the site. The hub never has access to the device's filesystem or
credentials; it only relays the encrypted tunnel.

```
Services available:
  localhost:22     → SSH          (kiosk-store-042)
  localhost:10022  → SSH          (kiosk-store-107)
  localhost:8080   → HTTP         (controller-plant-a)
```

Devices that lose power or network reconnect automatically when they come
back.

## Topology

The endpoint-versus-gateway choice matters more here than in any other
scenario:

- **An agent on each device** is simplest per device and survives site
  network changes. It costs a `telad` process on hardware that may be
  small; the agent is a single static binary with ARM builds, so a
  Raspberry Pi class device handles it comfortably. Bake the binary and a
  systemd unit into the device image
  ([Run Tela as an OS Service](../howto/services.md)).
- **A site gateway** (one `telad` fronting many devices at a location)
  minimizes the footprint on devices that cannot run extra software, at
  the price of making the gateway a critical asset. Put it in a dedicated
  subnet, allowlist its egress to the hub URL only, and allowlist its
  internal reach to exactly the target devices and ports:

  ```yaml
  hub: wss://hub.example.com
  token: "<site-agent-token>"
  machines:
    - name: kiosk-001
      target: 192.168.10.21
      services:
        - port: 22
          name: SSH
    - name: kiosk-002
      target: 192.168.10.22
      services:
        - port: 22
          name: SSH
  ```

Fleet-wide software management is where release channels earn their place:
agents update themselves from a channel manifest, and you can point the
fleet at a channel you control. See
[Self-Update and Release Channels](../howto/channels.md).

## The Access Model

The tradeoff to decide deliberately: per-device identities or a shared
fleet identity.

- **Per-device identities** give you per-device revocation: a device that
  is stolen or tampered with can be cut off without touching the rest of
  the fleet. The cost is provisioning: each device needs its own token or
  register pairing code at imaging time.
- **A shared fleet identity** is simpler to provision but means a
  credential extracted from one compromised device is valid for the whole
  fleet, and revoking it disconnects everything until re-provisioned.

For devices in physically uncontrolled locations (retail kiosks, customer
premises), per-device identities are worth the provisioning cost.
Register-type pairing codes make it scriptable: mint one code per device
(`tela admin pair-code kiosk-042 -type register -expires 7d`) and have the
provisioning script run `telad pair`, so no long-lived credential ever
sits in an image.

Operators get their own identities with connect grants, wildcard or scoped
by region or customer, following the same rules as
[Production Access](production-access.md).

## Pitfalls Specific to This Scenario

- **Flapping devices** (online, offline, online) are almost always power or
  local network stability, not Tela; the agent's reconnect loop is doing
  its job. Persistent absence usually means the site started blocking
  outbound HTTPS, which is the one thing the device needs.
- **SSH connects but authentication fails**: Tela is only the transport;
  the device's SSH server still enforces its own keys and accounts.
- **Cellular and metered links**: the idle control connection is
  lightweight, but remember every remote session rides the site's uplink;
  the UDP relay transport (hub port 41820) noticeably improves interactive
  latency when the site allows outbound UDP.
- Name devices by site and role (`kiosk-store-042`), and use the agent's
  `tags` and `location` fields so a 500-device fleet stays filterable in
  TelaVisor and portals.
