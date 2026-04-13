# Set up a path-based gateway

The gateway is a built-in HTTP reverse proxy inside `telad`. It lets you expose several local HTTP services through a single tunnel port, routed by URL path. This chapter is the operational how-to: when to use a gateway, how to configure it, how to connect through it, and how to combine it with direct service access. For the design rationale and the broader gateway primitive family, see the [Gateways](../architecture/gateway.md) chapter in the Design Rationale section.

{{#include ../../../howto/gateway.md:5:}}
