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

The hub prints an owner token on first start. Save it, then create identities for lab machines and students:

```bash
# Create a shared agent token for lab machines
tela admin tokens add lab-agent -hub wss://lab-hub.example.com -token <owner-token>
# Save the printed token -- this is <lab-agent-token> used in telad on each lab machine (Step 3)

# Grant the agent permission to register each machine
tela admin access grant lab-agent lab-pc-017 register -hub wss://lab-hub.example.com -token <owner-token>
tela admin access grant lab-agent lab-linux-03 register -hub wss://lab-hub.example.com -token <owner-token>

# Create per-student tokens
tela admin tokens add student-alice -hub wss://lab-hub.example.com -token <owner-token>
# Save the printed token -- give it to Alice for use with tela connect (Step 4)
tela admin access grant student-alice lab-pc-017 connect -hub wss://lab-hub.example.com -token <owner-token>
```

See [Run a hub on the public internet](../howto/hub.md) for the full list of `tela admin` commands.

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
