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

---

## Step 2 - Register lab machines

On each lab machine, run `telad`.

Example (Windows lab machine exposing RDP):

```powershell
.\telad.exe -hub wss://LAB-HUB -machine lab-pc-017 -ports "3389"
```

Example (Linux lab machine exposing SSH):

```bash
./telad -hub wss://LAB-HUB -machine lab-linux-03 -ports "22"
```

---

## Step 3 - Student workflow

On the student’s machine:

1. Download `tela`.
2. List machines:

```bash
./tela machines -hub wss://LAB-HUB
```

3. Connect to the assigned machine:

```bash
./tela connect -hub wss://LAB-HUB -machine lab-pc-017
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
