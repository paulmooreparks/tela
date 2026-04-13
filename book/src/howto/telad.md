# Run an agent

The agent (`telad`) is the daemon that runs on the machine you want to reach. This chapter covers installing it, configuring machines and services, authenticating with a hub, and the two deployment patterns: endpoint mode (the agent runs on the target machine itself) and gateway/bridge mode (the agent runs elsewhere and forwards to LAN-reachable targets). It also covers file sharing, the built-in HTTP gateway, and running `telad` as an operating system service.

{{#include ../../../howto/telad.md:3:}}
