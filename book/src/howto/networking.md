# Networking caveats

Tela is designed to work through firewalls and NATs without special configuration, but there are assumptions about what each component can reach. This chapter makes those assumptions explicit: what ports the hub needs, what outbound access the agent and client need, how the transport fallback cascade works, and the most common questions about topology, addressing, and session limits.

{{#include ../../../howto/networking.md:3:}}
