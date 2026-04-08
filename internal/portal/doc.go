// Package portal implements the Tela portal protocol described in
// DESIGN-portal.md. A portal is the directory and credential broker
// that aggregates Tela hubs into a list users can browse, and proxies
// authenticated administrative requests through to the hubs it lists.
//
// The package separates the protocol contract from the identity
// implementation. The contract is the HTTP wire format -- small,
// version-stable, documented in DESIGN-portal.md. The identity
// implementation is the Store interface, which the package leaves
// pluggable: a single-user portal stores hubs in a YAML file on disk,
// while a multi-organization deployment uses a database-backed Store
// implementation.
//
// Three use cases the package is designed for:
//
//   - Standalone portal binary (telaportal). Run as a network service
//     for a deployment that wants a portal but not the full Awan Saya
//     SaaS frontend.
//   - Embedded in TelaVisor (Portal mode). The desktop client runs a
//     portal goroutine listening on 127.0.0.1 with a file-backed
//     store. One-stop shopping for personal use, no separate process.
//   - Replacement backend for Awan Saya. The Awan Saya web frontend
//     stops implementing the portal protocol itself and starts calling
//     this package via the same HTTP API every other client uses.
//
// All three speak the same wire protocol on the same set of endpoints.
// The only thing that varies is the Store implementation behind the
// HTTP handlers.
//
// See DESIGN-portal.md in the repository root for the wire-level
// specification this package implements.
package portal
