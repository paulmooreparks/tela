# Private Web Application

You are running a web application that should not be reachable from the
open internet: an internal admin panel, a staging environment, a team
dashboard, or a self-hosted tool like Grafana, Gitea, or Outline. Today it
either lives behind a VPN (complex to onboard users), has IP allowlisting
(fragile when team members move around), or is simply exposed with a long
URL and a hope that nobody finds it.

With Tela, the application server runs `telad` with a path gateway
configured. The server has no inbound firewall rule. Users who have been
explicitly granted access connect through the hub and open a local address
in their browser:

```
Services available:
  localhost:8080   → HTTP
```

The connection travels through an end-to-end encrypted WireGuard tunnel to
the application server. The hub relays ciphertext and cannot see request or
response content. Users without a valid token cannot reach the machine at
all; there is nothing to find, because the server never accepts an inbound
connection from them.

## Topology

One hub, one agent on the application server, one gateway exposing the app.
The gateway is the load-bearing piece: it routes URL path prefixes to local
ports, so a frontend, an API, and an admin panel all appear as one origin
in the browser and Cross-Origin Resource Sharing (CORS) never comes up.

```yaml
# telad.yaml on the application server
hub: wss://hub.example.com
token: "<app-agent-token>"

machines:
  - name: myapp
    gateway:
      port: 8080
      routes:
        - path: /admin/
          target: 5000    # admin panel
        - path: /api/
          target: 4000    # REST API
        - path: /
          target: 3000    # frontend
```

The local ports 3000, 4000, and 5000 are invisible outside the server.
Routes match by longest prefix regardless of their order in the file. For
the mechanics (hub deployment, agent service install, gateway
verification), see [Run a Hub on the Public Internet](../howto/hub.md),
[Run an Agent](../howto/telad.md), and
[Set Up a Path-Based Gateway](../howto/gateway.md).

## The Access Model

This scenario is where per-user identities pay off. One agent identity for
the server, one identity per person, each with a connect grant on the one
machine:

```bash
tela admin tokens add app-agent -hub wss://hub.example.com
tela admin access grant app-agent myapp register -hub wss://hub.example.com

tela admin tokens add alice -hub wss://hub.example.com
tela admin access grant alice myapp connect -hub wss://hub.example.com
```

A user holding a valid hub token but no connect grant on `myapp` still
cannot reach it. To onboard someone without shuttling a raw token around,
generate a pairing code instead
(`tela admin pair-code myapp`; see
[Credentials and Pairing](../guide/credentials.md)).

Revocation is one command, effective immediately, and terminates any active
session from that token:

```bash
tela admin access revoke alice myapp -hub wss://hub.example.com   # this machine only
tela admin access remove alice -hub wss://hub.example.com          # identity gone entirely
```

## The User's Side

Each user stores their token once, then connects with a profile:

```bash
tela login wss://hub.example.com
tela connect -hub wss://hub.example.com -machine myapp
```

They open `http://localhost:8080/` in a browser. A profile with
`services: [{name: gateway}]` makes it a one-command habit; export the
profile file and hand it to new users so nobody has to build it by hand.

Keep the gateway on a port like 8080 rather than 80. The client binds the
gateway's port on the user's own machine, and binding port 80 on Linux and
macOS requires elevated privileges, which defeats one of Tela's design
points; a client that cannot bind it will silently land on the 10080
fallback instead.

## Pitfalls Specific to This Scenario

- **404 on every path** usually means the catch-all `path: /` route is
  missing.
- **The page loads but API calls fail**: the route prefix must match what
  the frontend actually requests. If the frontend calls `/api/v1/users`,
  either `path: /api/` or `path: /api/v1/` works; `path: /api` without the
  trailing slash is the classic typo.
- **The app uses WebSockets** (live dashboards often do): if it misbehaves
  behind the gateway, expose that service directly as a named TCP service
  alongside the gateway and point the app at its own port.
- Application-level authentication is still the application's job. Tela
  controls who can reach the app, not who can log into it.
