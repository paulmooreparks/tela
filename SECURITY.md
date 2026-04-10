# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Tela, please report it
responsibly. Do not open a public issue.

**Email:** security@parkscomputing.com

Include:
- A description of the vulnerability
- Steps to reproduce it
- The version of Tela affected
- Any potential impact you have identified

We will acknowledge your report within 48 hours and provide an
estimated timeline for a fix. We will credit reporters in the
release notes unless you request otherwise.

## Supported Versions

Tela is pre-1.0. Security fixes are applied to the latest dev
channel build only. There are no backports to older versions.

| Channel | Supported |
|---------|-----------|
| dev     | Yes       |
| beta    | Yes (promoted from dev) |
| stable  | Yes (promoted from beta) |
| Older tags | No |

## Security Model

Tela is a remote-access tool that creates encrypted WireGuard tunnels.
The security model is documented in detail in DESIGN.md. Key properties:

- **End-to-end encryption.** WireGuard tunnels are encrypted between
  client and agent. The hub relays opaque ciphertext and cannot
  inspect or modify tunnel payloads.
- **No TUN, no root.** Agents use userspace WireGuard via gVisor
  netstack. No administrator or root privileges are required.
- **Token-based authentication.** Hub access is controlled by bearer
  tokens with role-based permissions (owner, admin, user, viewer).
  Tokens are compared using constant-time comparison.
- **Secure defaults.** Hubs auto-generate an owner token on first
  start. Config files are written with restrictive permissions.
  The admin API requires authentication even on open hubs.

## Scope

The following are in scope for security reports:

- Authentication or authorization bypass
- Token leakage (in logs, responses, error messages)
- Command injection or path traversal
- Cryptographic weaknesses in the tunnel or key exchange
- Privilege escalation in the agent or hub
- Denial of service that affects availability

The following are out of scope:

- Vulnerabilities in dependencies (report upstream; we update promptly)
- Social engineering attacks
- Physical access attacks
- Issues that require the attacker to already hold a valid admin token
