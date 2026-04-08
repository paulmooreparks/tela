# File sharing

This chapter is the design rationale for the file sharing protocol: a sandboxed file transfer channel layered onto the existing WireGuard tunnel between client and agent. It explains why file sharing is its own subsystem rather than a layer over Server Message Block or Secure File Transfer Protocol, why the hub remains blind to file contents, and what the security boundary looks like in practice.

{{#include ../../../DESIGN-file-sharing.md:3:}}
