# Run a hub on the public internet

The hub is the one component that needs a public address. This chapter walks through deploying `telahubd` for production use: choosing a host, configuring TLS through a reverse proxy, enabling the UDP relay for faster tunnels, bootstrapping authentication, and registering with a portal. If you ran through the [First connection](../getting-started/first-connection.md) walkthrough on localhost, this chapter takes you from that to a real deployment.

{{#include ../../../howto/hub.md:11:}}
