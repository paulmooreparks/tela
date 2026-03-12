# HOWTO - Education / Lab Environments (Tela)

This guide shows how to use Tela to provide remote access to lab machines for students (RDP/VNC/SSH) without requiring a campus VPN.

---

## Recommended topology

- **One hub per lab or course** (simple isolation)
- `telad` on each lab machine (endpoint agent pattern)

---

## Step 1 - Deploy a hub for the lab

1. Deploy the hub and publish it as `wss://LAB-HUB`.
2. Verify hub console and `/api/status` are reachable.

See [hub.md](hub.md) for the full hub deployment guide.

---

## Step 1.5 - Enable authentication (recommended)

Enable token-based auth to prevent unauthorized access to lab machines:

```bash
# On the hub machine
telahubd user bootstrap
# → Save the owner token

# Create tokens for each lab machine agent
telahubd user add lab-agent
telahubd user grant lab-agent lab-pc-017
telahubd user grant lab-agent lab-linux-03

# Create student tokens
telahubd user add student-alice
telahubd user grant student-alice lab-pc-017
```

See [hub.md](hub.md) for remote management with `tela admin`.

---

## Step 2 - Register lab machines

On each lab machine, run `telad`.

Example (Windows lab machine exposing RDP):

```powershell
.\telad.exe -hub wss://LAB-HUB -machine lab-pc-017 -ports "3389" -token <lab-agent-token>
```

Example (Linux lab machine exposing SSH):

```bash
./telad -hub wss://LAB-HUB -machine lab-linux-03 -ports "22" -token <lab-agent-token>
```

For persistent deployment, install `telad` as an OS service (see [services.md](services.md)).

---

## Step 3 - Student workflow

On the student's machine:

1. Download `tela`.
2. List machines:

```bash
./tela machines -hub wss://LAB-HUB -token <student-token>
```

3. Connect to the assigned machine:

```bash
./tela connect -hub wss://LAB-HUB -machine lab-pc-017 -token <student-token>
```

4. Use RDP:

```powershell
mstsc /v:localhost
```

---

## Operational guidance

- Pre-assign machine names to students.
- Rotate credentials/policies each term.
- Consider exposing only RDP/VNC/SSH. Avoid granting broad internal network access.

---

## Troubleshooting

### Students can list machines but connect fails

- Confirm the lab machine is online.
- Confirm RDP/SSH is enabled and listening.
- Ensure the lab hub URL supports WebSockets.
