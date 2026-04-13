# IoT and edge devices

Devices deployed behind NATs and firewalls you do not control: Raspberry Pis, kiosks, industrial controllers, point-of-sale terminals. The primary goal is reliable outbound-only SSH (and optionally web admin ports) without requiring port forwards on the site's network. This chapter covers the endpoint and site-gateway deployment patterns, the access model for fleet operators, and the operational considerations for devices that may go offline for extended periods.

{{#include ../../../howto/iot-edge.md:3:}}
