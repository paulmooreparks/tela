# Cert pinning on the portal-mediated path: scope decision for 1.0

**Status:** decision recorded for 1.0. Pinning ships as documented for `tela CLI -> hub` (PRs #72 and #73). `TelaVisor -> portal` and `portal -> hub` pinning are deliberately out of scope for 1.0; this document is the reasoning. Issue #23 closes against this scope. Reopen post-1.0 if the threat model changes.

## 1. The problem

The pin model already shipped (see PRs #72 and #73) assumes a single TLS handshake between the operator's machine and the hub. That assumption holds for the `tela` CLI: one process opens one TLS connection per hub, captures or verifies the leaf certificate's SPKI fingerprint, refuses on mismatch.

That assumption does **not** hold for TelaVisor, because TelaVisor talks to hubs through a portal proxy:

```
TelaVisor --[TLS A]--> Portal --[TLS B]--> Hub
                          |                  |
                          ^                  ^
                  TV sees this cert   TV never sees this cert
```

Two TLS handshakes per admin call, each with its own cert chain. Three deployment topologies for the portal:

- **Embedded portal:** the portal runs as a goroutine inside TelaVisor. Handshake A is `127.0.0.1` (loopback); handshake B is the real one to the hub.
- **Self-hosted portal (telaport, or self-hosted Awan Saya):** the operator runs the portal somewhere they control. Handshake A is to that operator-owned host.
- **Hosted portal (the public Awan Saya at `awansaya.net`):** a SaaS the operator does not control.

Any pin design has to make a choice about which hops to pin and where the pin lives in each topology.

## 2. What "pinning" would mean at each hop

Three hops can in principle be pinned:

1. **TV -> portal (handshake A):** TV's actual TLS partner. Pinning here is straightforward — same `internal/certpin` package, pin stored in TV's portal-source config. Has the same operational risks any client-side pin has.
2. **Portal -> hub (handshake B), enforced by the portal:** the portal validates the hub's TLS leaf SPKI when it dials. Pin lives in the portal's config. No protocol change between TV and portal.
3. **Portal -> hub (handshake B), enforced by TV through the portal:** the portal reports the hub's currently-presented cert SPKI to TV through a new field; TV verifies against a stored expected pin. Requires a new protocol field, new TV state, and an inherent weakness: the portal can lie about what it observed.

Hop 3 is the only one that gives TV a direct cryptographic claim about the hub. It's also the only one whose security depends on the portal being honest about a value the operator can't independently verify. If the portal is compromised, hop-3 pinning is worthless. If the portal is honest, hop-3 pinning is redundant with hop-2 pinning. Hop 3 is rejected on principle.

The remaining design space is whether to do hop 1, hop 2, or neither.

## 3. The Awan Saya context

The author runs both Tela and Awan Saya. Awan Saya is a commercial product. This biases the decision toward "default behavior must work for paying customers on hostile networks" and away from "default behavior locks customers out."

Two threats are worth weighing:

| Threat | Mitigation if Awan Saya pin is default ON | Mitigation if pin is opt-in or off |
|---|---|---|
| Awan Saya's CA-issued cert gets MITM-replaced by a rogue CA | Pin blocks the MITM | Standard CA trust + Certificate Transparency monitoring catches the unauthorized issuance |
| Operator on a corporate network with MITM proxy (cert installed by IT) | **Operator is locked out** of Awan Saya entirely | Operator can use Awan Saya through their corporate proxy |
| Awan Saya cert rotation goes wrong | **Every TV user is bricked** until manual pin update | Standard renewal works transparently |
| Awan Saya ops mistake (fat-finger a cert, expired cert renewed late) | **Everyone affected** until pin expires | Self-heals on next renewal |
| Hub TLS compromise (hub's cert stolen, attacker MITMs portal-to-hub) | Pin at hop 1 does nothing for this hop | Hop-2 pinning would help; hop-3 pinning is structurally weak |
| Portal compromise (the portal itself goes rogue) | Nothing — pin authenticates *which* portal you talk to, not whether it's honest | Same |

The asymmetric risk of "default pinning bricks legitimate users vs. default no-pinning loses one CA-compromise attack vector" comes out the same way the web platform decided it: pinning by default is operationally worse than the threat it mitigates, **for clients of a public CA-issued service**.

## 4. Prior art

Architectures structurally similar to TelaVisor -> Awan Saya -> hub:

- **SaaS-managed overlay networks (Tailscale, Twingate, Cloudflare Access, NetBird, headscale-style self-hosted variants):** none pin the operator-to-control-plane TLS connection by default. The control plane is the trust anchor for the operator-facing surface. The data plane runs its own cryptographic identity (WireGuard keys for Tailscale, mTLS with rotating short-lived certs for Cloudflare Access). Some self-hosted variants document optional CA-pinning for users who run their own control plane, but it's opt-in.

- **Browser -> CDN -> origin:** structurally identical to TelaVisor -> Awan Saya -> hub. The browser only ever sees the CDN's cert; the origin's cert is invisible. Industry has converged on: the CDN is the trust anchor for the client. Origin authentication is the CDN's problem (mTLS, signed origin requests). Browsers do not attempt to verify origins through the CDN.

- **Service meshes (Istio, Linkerd, Consul Connect):** replace TLS pinning with cryptographic workload identity (SPIFFE/SPIRE). Each service has a verifiable identity attested by the mesh control plane. Trust flows from "I trust the mesh's root" not from "I pinned this specific cert."

- **OAuth2 / OIDC:** standard TLS to the IdP, no pinning. Trust is rooted in the IdP's signing key, surfaced via JWT verification at the application layer. Pin the cryptographic claim, not the TLS connection.

- **SSH with jumphost (`ssh -J bastion target`):** the closest analog where a client *does* verify each hop independently. Works because the client controls each connection and the keys are not behind a CA so there's nothing else to trust. Doesn't generalize: the second hop must be visible to the client.

- **Mobile banking / Signal / WhatsApp:** these DO pin, but they're single-hop. Doesn't port cleanly to a portal-mediated system.

### The HPKP cautionary tale

The web platform tried HTTP Public Key Pinning around 2015. Browsers (Chrome, Firefox) deprecated it within ~5 years. The reasons map directly onto Awan Saya's risk profile:

- **Operator footguns:** a misconfigured pin could lock the entire user base out. Recovery required waiting for the pin TTL to expire, which clients cached aggressively.
- **Hostage scenarios:** an attacker who briefly stole a cert could pin a malicious key for the maximum allowed TTL, locking out the legitimate operator for that long.
- **Cert lifecycle pain:** routine cert rotation became a coordinated rollout instead of a transparent renewal.
- **No graceful failure:** when a pin mismatched, the user got a hard error with no override.

The web's replacement was Certificate Transparency: instead of clients pinning specific keys, CT creates an auditable public log of every issued cert, which the operator monitors for unauthorized issuances. This shifts the cost from every client to the one party who has full context (the operator).

The HPKP retrospective is the strongest single argument against making portal pinning the default for a commercial SaaS product.

## 5. Decision for 1.0

### What ships at 1.0 (already merged or in flight)

- **`tela` CLI -> hub:** TLS SPKI pinning shipped in PRs #72 and #73. Pin stored per-hub in `~/.tela/credentials.yaml`, `hubs.yaml`, and `telahubd.yaml` (for the bridge gateway's outbound dial). TOFU on first connect with a clear log message; refusal on mismatch with `certpin.ErrMismatch`. CLI surface: `tela pin <hub-url> [fingerprint]` and `tela login -pin <fingerprint>`.

- **`telahubd` -> destination hub (bridge gateway):** same pinning, configured per `bridges[]` entry in `telahubd.yaml`. Documented in DESIGN-relay-gateway.md §5.4.

This is the path where the operator owns both ends, knows what cert they expect, and benefits from defense-in-depth against CA compromise.

### What is deliberately out of scope for 1.0

- **`TelaVisor -> portal` (handshake A):** no pinning. TelaVisor uses standard CA chain validation for its connection to the portal, regardless of whether the portal is hosted Awan Saya, a self-hosted telaport, or someone else's instance. The reasoning: defaulting to pinned would brick users on corporate networks with MITM proxies, every Awan Saya cert rotation would be a coordinated rollout across every installed TelaVisor, and the threat (rogue CA mints a cert for `awansaya.net`) is mitigated more effectively by Certificate Transparency monitoring on the Awan Saya side than by client-side pinning.

- **Portal -> hub (handshake B), enforced by the portal:** no pinning. The portal authenticates hubs by their admin token, which is rotatable, scoped, and revocable — strictly better than a pinned TLS leaf cert for application-layer authorization. TLS chain validation provides transport authentication. Adding pinning here would duplicate work the token model already does and introduce the same operational risks pinning has elsewhere.

- **TV-enforced hub pinning through the portal (the hop-3 design above):** rejected on principle. Trusts the portal to honestly report a value the operator can't independently verify; provides no protection if the portal is compromised; redundant if the portal is honest. Not a sound design.

### Hosted-portal-side trust hardening (not pinning, but related)

Awan Saya as a commercial product carries operational responsibility for its TLS hygiene. The recommended posture, separate from anything Tela ships:

- **Certificate Transparency monitoring** for every domain Awan Saya serves on (`awansaya.net` and any vanity domains). Tools: `certstream`, `crt.sh` watch lists, or commercial CT monitors. An unauthorized cert issuance for the domain shows up in CT logs within minutes; the operator gets paged before any client connects to the rogue cert.

- **CAA records** on the DNS zone, restricting which CAs are allowed to issue for the domain. Reduces the rogue-issuance attack surface to the explicitly-authorized CA(s).

- **HSTS** with a long max-age and `includeSubDomains` once the domain is stable. Prevents downgrade attacks.

- **Short-lived certs** via Let's Encrypt or equivalent. Reduces the window during which a stolen cert is useful.

These are operational hygiene items for the Awan Saya operator, not features Tela ships. They achieve the same goal as default-on pinning (defense against CA compromise) without imposing the cost of pinning on every client.

## 6. Future work (post-1.0, if the threat model changes)

If at some point the user-facing trust model needs strengthening beyond CA + CT, the right directions in priority order:

1. **Optional opt-in pinning for `TelaVisor -> portal`** when the operator runs a self-hosted portal (telaport, self-hosted Awan Saya). The portal-source config in TelaVisor gains an optional `pin` field; TV uses the same `internal/certpin` package; the UX is "advanced operators only, with clear footgun warnings." Rough effort: ~1 PR similar to #72.

2. **Optional opt-in pinning for `portal -> hub`** in the embedded-portal case (the portal goroutine inside TelaVisor reads `~/.tela/credentials.yaml` for pins, the same file the `tela` CLI uses). Rough effort: ~half a PR; mostly wiring the existing `pinAwareHTTPClient` into `internal/portal/admin_proxy.go`'s admin dial path with the credstore lookup.

3. **Cryptographic agent identity (issue #33).** Replaces or supplements TLS-layer pinning with application-layer identity (Ed25519 keypair per agent, TOFU registration, signed messages). This is a different model than pinning and a better long-term answer for hub authentication; pinning becomes a transport-layer hardening rather than the primary trust mechanism.

None of this is necessary for 1.0. All of it is reachable post-1.0 without breaking the v1 wire format or the v1 compat contract: the storage shapes already accommodate the field, the helper packages are reusable, and the decisions documented here can be revisited.

## 7. Summary

| Hop | 1.0 behavior | Post-1.0 reachable |
|---|---|---|
| `tela` CLI -> hub | Pinning shipped (PRs #72 / #73) | Already done |
| Bridge gateway: hub -> hub | Pinning shipped (DESIGN-relay-gateway §5.4) | Already done |
| TelaVisor -> portal | Standard CA validation | Optional opt-in pinning per portal source |
| Portal -> hub (in embedded portal) | Standard CA validation | Optional opt-in pinning via credstore (same store the CLI uses) |
| Portal -> hub (in hosted Awan Saya) | Standard CA validation | Out of scope (operational concern of the Awan Saya operator, not a Tela feature) |
| TV-verifies-hub-cert-through-portal (hop 3) | Rejected on principle | Rejected on principle |

Issue #23 closes against this scope. The HPKP retrospective is the load-bearing argument; revisit only if a concrete threat in the wild makes default-pinning cost-effective.
