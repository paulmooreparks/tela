# Education labs

A university or training provider that needs to give students remote access to lab machines (Remote Desktop Protocol (RDP), Virtual Network Computing (VNC), SSH) without requiring a campus VPN. The recommended topology is one hub per lab or course, with the access model configured separately for students and instructors.

## Recommended topology

- **One hub per lab or course** (simple isolation)
- `telad` on each lab machine (endpoint agent pattern)

---

## Step 1 - Deploy a hub for the lab

1. Deploy the hub and publish it as `wss://lab-hub.example.com`.
2. Verify hub console and `/api/status` are reachable.

See [Run a hub on the public internet](../howto/hub.md) for the full hub deployment guide.

---

## Step 2 - Enable authentication

The hub prints an owner token on first start. Save it, then create identities for lab machines and students. These commands run on the hub machine:

```bash
# Create a shared agent token for lab machines
telahubd user add lab-agent
telahubd user grant lab-agent lab-pc-017
telahubd user grant lab-agent lab-linux-03

# Create per-student tokens
telahubd user add student-alice
telahubd user grant student-alice lab-pc-017
```

See [Run a hub on the public internet](../howto/hub.md) for remote token management with `tela admin`.

---

## Step 3 - Register lab machines

On each lab machine, run `telad`.

Example (Windows lab machine exposing RDP):

```powershell
telad.exe -hub wss://lab-hub.example.com -machine lab-pc-017 -ports "3389" -token <lab-agent-token>
```

Example (Linux lab machine exposing SSH):

```bash
telad -hub wss://lab-hub.example.com -machine lab-linux-03 -ports "22" -token <lab-agent-token>
```

For persistent deployment, install `telad` as an OS service (see [Run Tela as an OS service](../howto/services.md)).

---

## Step 4 - Student workflow

On the student's machine:

1. Download `tela`.
2. List machines:

```bash
tela machines -hub wss://lab-hub.example.com -token <student-token>
```

3. Connect to the assigned machine:

```bash
tela connect -hub wss://lab-hub.example.com -machine lab-pc-017 -token <student-token>
```

4. Use the local address shown in the output. For RDP:

```powershell
mstsc /v:127.88.x.x
```

---

## Operational guidance

- Pre-assign machine names to students.
- Rotate credentials and policies each term.
- Expose only RDP/VNC/SSH. Avoid granting broad internal network access.

---

## Troubleshooting

### Students can list machines but connect fails

- Confirm the lab machine is online.
- Confirm RDP/SSH is enabled and listening on the machine.
- Ensure the lab hub URL supports WebSockets.
