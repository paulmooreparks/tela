# Preface

This book exists because the system it documents got too big for a README.

Tela started as a small experiment: could I build a remote-access tool that
needed no admin rights, no inbound ports, and no kernel modules, using
WireGuard's cryptography but in pure userspace? The answer turned out to be
yes, and the experiment kept growing. By the time it could reach a workstation
behind a firewall it had also grown an agent, a hub, a desktop GUI, a
multi-organization web portal, a release channel system, and a fleet
management plane. What started as "let me forward an SSH session" ended up
shaped like a small cloud without the cloud part: you bring your own
machines, your own network, your own storage, and Tela handles the encrypted
fabric and the operations layer that ties them together.

The book is organized in three loose movements. The first is **getting up and
running** -- the architecture in one paragraph, installing the binaries,
making your first connection, and understanding what just happened. If you
only read this part, you should be able to use Tela to do real work.

The second is **the operator's guide** -- everything about running Tela in
production. The hub on the public internet, the agent on managed servers,
the access control model, file sharing, gateways, the desktop client, the
multi-org portal, release channels, and the day-to-day operations of a
fleet. If you read this part, you should be able to deploy Tela to a team
or an organization.

The third is the **architecture and deep dives** -- the protocol
specification, the security model, the deployment recipes, the
troubleshooting playbook, and the roadmap. If you read this part, you should
be able to debug, extend, or fork Tela.

The book and the [documentation site](https://paulmooreparks.github.io/tela/)
share source. When the docs change, the book updates automatically on the
next Leanpub build. The book has more long-form material -- design rationale,
worked examples, opinionated advice -- that does not belong in reference
docs.

The early chapters are stable. The later chapters are still being written
and will arrive in updates. If you bought this book on Leanpub, you get
every update for free.

A note on the title: I called this "A FOSS Cloud Without the IaaS" because
it captures what Tela is shaped like, even if it does not match the way
"cloud" is usually defined. Tela does not provide compute, storage, or
networking infrastructure -- you bring those. What it provides is the
*management plane* on top of infrastructure you already own: the encrypted
transport, the access control, the agent fleet, the operations UI, the
release pipeline. The pieces a managed cloud service usually charges for,
minus the compute rental and the lock-in.
