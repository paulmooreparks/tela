# Why a connectivity fabric

Tela ships as three small binaries. It uses WireGuard but not the kernel driver. Its hub relays traffic without reading it. These are not defaults that fell out of convenience: each is a deliberate choice with a specific alternative that was considered and rejected. This chapter explains the three decisions that shaped the architecture.

## Three binaries, not one

Tela could have been a single binary run in different modes: `tela --mode agent`, `tela --mode hub`, `tela --mode client`. The code would be simpler and distribution easier. The problem is that a single binary conflates trust domains.

The hub is designed to run on infrastructure the user does not own: a cloud VM, a VPS, shared hosting, a machine run by a different organization. If the hub and the agent shared a binary and a codebase, the hub would contain agent code that could, in principle, be activated. More importantly, the protocol separation between hub and agent would be a matter of convention rather than structure.

Separate binaries make the separation structural. `telahubd` has no code path that reads WireGuard payloads, because it has no WireGuard code. It cannot be configured to proxy traffic to a local service, because it has no local service integration. It does only what a relay needs to do: accept registrations, manage sessions, and forward opaque bytes. The constraint is enforced by what the binary contains, not by what flags are set.

The same argument applies to the split between client and agent. `tela` connects outbound and creates a local port binding. `telad` registers with a hub and exposes local services. They share a Go module but are distinct processes with distinct privilege requirements and distinct deployment contexts. A machine can run an agent without having the client binary, and vice versa.

## The hub is a blind relay

The hub could inspect WireGuard payloads. It could decrypt them, log the content, or apply policy based on what traffic flows through. This is how most commercial VPN concentrators work.

Tela takes the opposite approach: the hub forwards opaque bytes and has no key material to decrypt them. WireGuard encryption is end-to-end between agent and client. The hub sees only ciphertext it cannot read.

The reason is that a relay that *can* inspect traffic *will* be pressured to do so. An operator running a hub for a team does not need to read what flows through it. A portal aggregating many hubs does not need traffic content to provide management and directory services. If the architecture required inspecting traffic to function, then every hub operator would become a party to every user's communications.

By making the hub blind structurally (no keys, no decryption code path, no policy hook), the security property is not a promise the hub operator makes. It is a consequence of what the software does.

## No TUN, no root

Standard WireGuard works through a kernel TUN device. On Linux you create a `wg0` interface. On Windows you use the WireGuard kernel driver. On macOS you use the utun driver. All of these require elevated privileges: root on Unix, Administrator on Windows.

Tela uses userspace WireGuard via gVisor's netstack. The WireGuard cryptographic protocol runs entirely in user space. No kernel interface is created, no driver is loaded, and no elevated privilege is required.

The tradeoff is real: a userspace network stack has lower throughput than a kernel stack, and the current implementation handles TCP only. For the use cases Tela targets (remote desktop, SSH, file transfer, web access), TCP throughput through a userspace stack is adequate.

The reason the tradeoff is worth making is deployability. An agent that requires root cannot run in a container without elevated container privileges. It cannot run as a restricted service account. It cannot be deployed on a corporate laptop without IT involvement. It cannot run on a NAS or an edge device that locks down privilege escalation.

If the agent requires root, it will not get deployed on many of the machines it needs to reach. Userspace WireGuard removes that barrier.
