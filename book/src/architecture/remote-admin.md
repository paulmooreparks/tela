# Remote administration

This chapter is the design rationale for managing telad agents and telahubd hubs through the same encrypted tunnels that carry data traffic, without requiring shell access to the hosts running them. It explains why the project chose to multiplex management onto the existing wire format rather than running a parallel admin channel, and how the resulting model interacts with the access model and the portal protocol.

{{#include ../../../DESIGN-remote-admin.md:3:}}
