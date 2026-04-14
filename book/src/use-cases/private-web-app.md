# Private web application

## The scenario

You are running a web application that should not be reachable from the open internet. It might be an internal admin panel, a staging environment, a team dashboard, or a self-hosted tool like Grafana, Gitea, or Outline. Right now it either lives behind a VPN (complex to onboard users), has IP allowlisting (fragile when team members work from different locations), or is simply exposed to the public internet with a long URL and a hope that nobody finds it.

With Tela, the application server runs `telad` with a path gateway configured. The gateway exposes a single tunnel port that routes HTTP requests by URL prefix to the right local service. The server has no inbound firewall rule. Users who have been explicitly granted access connect through the hub and get a local address in their browser:

```
Services available:
  127.88.x.x:8080  → HTTP
```

They open `http://127.88.x.x:8080/` in a browser. The connection travels through an end-to-end encrypted WireGuard tunnel to the application server. The hub relays ciphertext and cannot see request or response content. Users without a valid token cannot reach the machine at all -- there is nothing to find, because the server never accepted an inbound connection from them.

If the application has multiple services (a frontend, an API, a metrics endpoint), the gateway routes each URL prefix to the right local port, so the browser sees everything as the same origin and Cross-Origin Resource Sharing (CORS) issues do not arise.

## How it works

`telad` runs on the application server and registers the machine with the hub.
It exposes the web application through its built-in path gateway -- a single
tunnel port that routes HTTP requests to local services by URL prefix. Only
users whose tokens have been granted `connect` permission on that machine can
reach anything at all. The hub relays ciphertext; it cannot see request or
response content.

When a user connects, `tela` binds a local address (for example,
`127.88.x.x:8080`). The user opens that address in a browser. The connection
travels through the encrypted WireGuard tunnel to the application server, where
`telad` forwards it to the local service. No inbound firewall rule is needed on
the application server.

---

## Step 1 - Stand up a hub

See [Run a hub on the public internet](../howto/hub.md) for the full
deployment guide. For a quick start:

```bash
telahubd
```

The hub prints an owner token on first start. Save it. Publish the hub as
`wss://hub.example.com`.

---

## Step 2 - Set up authentication

Create a token for the agent and one token per user:

```bash
# Create an agent token for the application server
tela admin tokens add app-agent -hub wss://hub.example.com -token <owner-token>
# Save the printed token -- this is <app-agent-token> used in telad.yaml (Step 3)

# Grant the agent permission to register the machine
tela admin access grant app-agent myapp register -hub wss://hub.example.com -token <owner-token>

# Create user tokens (one per person)
tela admin tokens add alice -hub wss://hub.example.com -token <owner-token>
# Save Alice's printed token -- give it to Alice to use with tela connect or tela login
tela admin tokens add bob -hub wss://hub.example.com -token <owner-token>
# Save Bob's printed token -- give it to Bob

# Grant each user connect access to the machine
tela admin access grant alice myapp connect -hub wss://hub.example.com -token <owner-token>
tela admin access grant bob myapp connect -hub wss://hub.example.com -token <owner-token>
```

Users without an explicit `connect` grant cannot reach the machine even if they
hold a valid hub token.

---

## Step 3 - Configure and run `telad` on the application server

Because `telad` uses userspace networking, the gateway can listen on port 80
inside the tunnel without elevated privileges on either the server or the
user's machine. Users browse to `http://127.88.x.x/` with no port number.

### Single-service application

If the application runs on one local port (for example, port 3000), route
it through the gateway on port 80:

```yaml
# telad.yaml
hub: wss://hub.example.com
token: "<app-agent-token>"

machines:
  - name: myapp
    gateway:
      port: 80
      routes:
        - path: /
          target: 3000    # application's local port
```

```bash
telad -config telad.yaml
```

Users connect and open `http://127.88.x.x/` in a browser.

### Multi-service application

If the application has separate frontend and backend processes -- a common
arrangement for single-page applications -- route them by path:

```yaml
# telad.yaml
hub: wss://hub.example.com
token: "<app-agent-token>"

machines:
  - name: myapp
    gateway:
      port: 80
      routes:
        - path: /api/
          target: 4000    # REST API
        - path: /
          target: 3000    # frontend (SPA or server-rendered)
```

Requests to `/api/...` are forwarded to the local API process on port 4000.
Everything else goes to the frontend on port 3000. Both local ports are
invisible outside the server. The browser sees a single origin, so no
Cross-Origin Resource Sharing (CORS) configuration is needed.

To add an admin panel at a separate path:

```yaml
gateway:
  port: 80
  routes:
    - path: /admin/
      target: 5000    # admin panel
    - path: /api/
      target: 4000    # REST API
    - path: /
      target: 3000    # frontend
```

Routes are matched by longest prefix first, regardless of their order in the
file.

For persistent operation, install `telad` as a service:

```bash
telad service install -config telad.yaml
telad service start
```

See [Run Tela as an OS service](../howto/services.md) for platform-specific
details.

---

## Step 4 - User workflow

On each user's machine:

1. Download `tela`.
2. Store the hub token so it does not need to be passed on every command:

```bash
tela login wss://hub.example.com
# Prompts for token
```

3. Connect:

```bash
tela connect -hub wss://hub.example.com -machine myapp
```

4. Open the address shown in the output in a browser:

```
http://127.88.x.x/
```

### Connection profile (optional)

If users connect to this application regularly, a profile avoids repeating
flags:

```yaml
# ~/.tela/profiles/myapp.yaml
connections:
  - hub: wss://hub.example.com
    token: ${MYAPP_TOKEN}
    machine: myapp
    services:
      - name: gateway
```

```bash
tela connect -profile myapp
```

Set `MYAPP_TOKEN` in the environment, or omit the `token` field if the token
is already in the credential store.

---

## Revoking access

To revoke a specific user's access:

```bash
# Remove connect permission for this machine only
tela admin access revoke alice myapp -hub wss://hub.example.com -token <owner-token>

# Or remove the identity entirely (disconnects immediately, deletes all permissions)
tela admin access remove alice -hub wss://hub.example.com -token <owner-token>
```

Revocation takes effect immediately. Any active session from that token is
terminated.

---

## Troubleshooting

### Browser shows "connection refused"

- Confirm the application is running on the server and listening on the
  expected local port.
- Confirm `telad` is running and the machine is online (`tela machines -hub
  wss://hub.example.com`).
- For gateway setups, confirm the `target` port in `telad.yaml` matches the
  port the application actually listens on.

### User can connect but gets a 404 on all paths

- The gateway route for `/` may be missing. Add a catch-all route with
  `path: /` pointing at the frontend service.
- Confirm the frontend process is running and reachable from the server
  itself (for example, `curl http://localhost:3000/`).

### Browser loads the page but API calls fail

- In a gateway setup, the API route path must match the path prefix the
  frontend uses for its requests. If the frontend calls `/api/v1/users`, the
  route must be `path: /api/` or `path: /api/v1/`.
- The gateway does not proxy WebSocket connections. If the application uses
  WebSockets for the API, expose the WebSocket service as a separate named
  service alongside the gateway.

### `tela connect` is refused ("auth_required" or 403)

- Confirm the user's token has been granted `connect` access:
  `tela admin access -hub wss://hub.example.com -token <owner-token>`
- Confirm the token is stored correctly: `tela login wss://hub.example.com`.
