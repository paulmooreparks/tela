// Package file implements a single-user, file-backed portal Store.
//
// This is the storage backend designed for personal-use portals: the
// store holds hubs in a YAML file on disk, recognizes exactly one
// principal (the operator running the portal), and grants that
// principal full permissions on every hub it holds. There is no
// account model, no organization model, no team model, no per-hub
// ACLs. If you can authenticate to the portal, you can do anything.
//
// Two deployment shapes:
//
//   - Localhost only, no auth. The portal binds to 127.0.0.1 and
//     trusts every request. This is the default for TelaVisor in
//     Portal mode (the desktop user is authenticated by virtue of
//     their OS session).
//
//   - Network exposed, bearer-token auth. The store is configured
//     with an admin token; every request must present it in the
//     Authorization header. Suitable for a small standalone
//     telaportal binary on a private network.
//
// The store reads its file at Open time, holds the parsed contents
// in memory under a sync.RWMutex, and writes back atomically on
// every mutation. The on-disk format is documented in file.go on
// the Store struct.
//
// For multi-user, multi-organization, account-based deployments, use
// the postgres-backed store instead (Awan Saya). The interfaces are
// identical so portals can swap storage backends without touching the
// HTTP handler layer.
package file
