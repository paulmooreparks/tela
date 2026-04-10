# Gateways

This chapter is the design rationale for the gateway primitive that recurs throughout Tela. The body of the book teaches each instance individually: the path gateway in `telad`, the bridge agent deployment pattern, upstream rerouting, and the single-hop relay gateway that the hub itself implements. This chapter explains what those instances have in common, what design rule unites them (a node in the middle forwards without inspecting beyond what its layer requires), and what the planned multi-hop relay gateway adds when it lands in 1.0.

{{#include ../../../DESIGN-gateway.md:3:}}
