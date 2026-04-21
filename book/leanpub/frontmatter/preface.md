# Preface

This book exists because the system it documents got too big for a README.

Tela started as a small experiment: could I build a remote-access tool that
needed no admin rights, no inbound ports, and no kernel modules, using
WireGuard's cryptography but in pure userspace? The answer turned out to be
yes, and the experiment kept growing. By the time it could reach a
workstation behind a firewall it had also grown an agent, a hub, a desktop
graphical interface, a multi-organization web portal, a release channel
system, and a fleet management plane. What started as "let me forward a
Secure Shell session" ended up shaped like a connectivity fabric: a small,
boring, long-lived substrate that does one thing well, and a layer of
features built on top of it that turn the substrate into something a real
team can use.

The book is organized in three loose movements. The first is **getting up
and running**: the architecture in one paragraph, installing the binaries,
making your first connection, and understanding what just happened. If you
only read this part, you should be able to use Tela to do real work.

The second is **the operator's guide**: everything about running Tela in
production. The hub on the public internet, the agent on managed servers,
the access control model, file sharing, gateways, the desktop client, the
multi-organization portal, release channels, and the day-to-day operations
of a fleet. If you read this part, you should be able to deploy Tela for a
team or an organization.

The third is the **architecture and deep dives**: the protocol
specification, the security model, the deployment recipes, the
troubleshooting playbook, and the roadmap. If you read this part, you
should be able to debug, extend, or fork Tela.

The book and the [documentation site](https://telaproject.org/book/)
share source. When the docs change, the book updates automatically on the
next Leanpub build. The book has more long-form material (design rationale,
worked examples, opinionated advice) that does not belong in reference docs.

The early chapters are stable. The later chapters are still being written
and will arrive in updates. If you bought this book on Leanpub, you get
every update for free.

A note on the title: I chose "A Connectivity Fabric" because it is the
phrase the design document has used since the beginning, and because the
word *fabric* is the most honest one-word description of the shape of the
system. Tela does not provide compute, storage, or networking
infrastructure. You bring those. What it provides is the woven layer
between them: the encrypted transport, the access control, the agent
fleet, the operations interface, the release pipeline. The pieces a
managed cloud service usually charges for, minus the compute rental and
the lock-in.
