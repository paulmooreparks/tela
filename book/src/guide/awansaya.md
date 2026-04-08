# Awan Saya portal

Awan Saya is the multi-organization web portal that sits in front of one or
more Tela hubs. It is a separate project at
[paulmooreparks/awansaya](https://github.com/paulmooreparks/awansaya) and not
required for Tela to function -- a hub can run standalone, and the `tela` CLI
can talk to it directly without any portal in between.

What Awan Saya adds:

- A directory of hubs that users can browse, with hub metadata, status, and
  the agents registered on each
- Multi-organization access control: users belong to organizations,
  organizations have teams, teams own hubs and agents
- A web-based admin UI for hub and agent management (parallel to TelaVisor's
  Infrastructure mode but accessed from any browser)
- Channel selectors for hubs and agents (the same UX as TelaVisor)
- Activity logging and audit trails

If you are running a single hub for personal use, you do not need Awan Saya.
If you are managing many hubs across an organization or providing remote
access as a service, the portal model becomes useful quickly.

See the [Awan Saya repository](https://github.com/paulmooreparks/awansaya)
for installation, schema, and operational documentation.
