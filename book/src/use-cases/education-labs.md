# Education Labs

A university computer lab has 30 workstations. Students need to reach their
assigned machine from home for coursework: remote desktop, SSH, or a
web-based IDE. The campus VPN is complex to set up, requires IT support for
every student, and grants access to far more of the campus network than
students should have.

With Tela, each lab machine runs `telad` and registers with a lab-specific
hub. Each student gets access to their assigned machine only. Setup for a
new student is a pairing code: one command to redeem it and they are ready
to connect.

```
Services available:
  localhost:3389   → RDP          (lab-pc-07)
```

They open Remote Desktop to that address and are on their lab machine. No
VPN client, no campus IT ticket, no exposure to the rest of the campus
network. At the end of the semester, the instructor removes the student
identities in one pass; the lab machines stay registered for the next
cohort.

## Topology

One hub per lab or course keeps the blast radius and the roster equally
small. The lab machines run endpoint agents as OS services, each exposing
exactly one service (RDP for Windows labs, SSH for Linux labs, or the port
of a web IDE). A shared agent identity across the lab machines is
acceptable here; the machines are institutionally owned and identically
managed, and it keeps imaging simple. Bake `telad` plus a `telad pair`
step into the machine image and registration becomes part of provisioning.

Deployment mechanics: [Run a Hub on the Public Internet](../howto/hub.md),
[Run an Agent](../howto/telad.md),
[Run Tela as an OS Service](../howto/services.md).

## The Access Model

The defining constraint is scoping: each student connects to their assigned
machine and nothing else.

```bash
tela admin tokens add student-alice -hub wss://lab-hub.example.com
tela admin access grant student-alice lab-pc-07 connect -hub wss://lab-hub.example.com
```

Onboard with pairing codes rather than raw tokens. A connect code scoped to
one machine, generated per student, turns the first lab session into "run
this one command":

```bash
tela admin pair-code lab-pc-07 -expires 7d
# hand the printed 'tela pair' command to the student
```

The 7-day expiry covers add/drop week; unredeemed codes die on their own.
An instructor identity gets a wildcard connect grant
(`tela admin access grant instructor '*' connect`) for monitoring and
support across all lab machines.

At semester end, remove the cohort:

```bash
tela admin access remove student-alice -hub wss://lab-hub.example.com
# ... one per student; the machine registrations are untouched
```

The identity list on the hub is the roster; scripting the add and remove
passes from the enrollment system is a natural next step.

## Pitfalls Specific to This Scenario

- Student home networks are the wild west, but they only need outbound
  HTTPS to the hub, which is precisely what home networks allow. Publish
  the hub on port 443 behind TLS per the hub how-to.
- RDP login is still campus-account authentication on the lab machine. A
  student who cannot log in after connecting has an account problem, not a
  Tela problem; the distinction saves support time.
- Pre-assign machine names to students and use predictable names
  (`lab-pc-07`). Grant mistakes with 30 near-identical machines are
  otherwise inevitable.
