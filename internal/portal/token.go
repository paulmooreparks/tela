package portal

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// GenerateSyncToken returns a fresh sync token suitable for issuing
// to a hub on POST /api/hubs (DESIGN-portal.md section 3.2). The
// returned token starts with HubSyncTokenPrefix and contains 32
// bytes of cryptographic randomness encoded as hex (64 chars), for
// a total length of 72 characters.
//
// Stores MUST persist only the SHA-256 hash of the returned token,
// never the cleartext. The cleartext is returned to the hub exactly
// once in the registration response.
func GenerateSyncToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate sync token: %w", err)
	}
	return HubSyncTokenPrefix + hex.EncodeToString(b), nil
}

// HashSyncToken returns the SHA-256 hash of a sync token, encoded
// as a lowercase hex string. Stores call this when persisting a
// freshly-generated token and when comparing a presented token
// against the stored hash.
//
// The hash is taken over the full token string, including the
// HubSyncTokenPrefix, to keep the input space large.
func HashSyncToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// IsSyncTokenFormat reports whether s has the structural shape of a
// sync token: HubSyncTokenPrefix followed by lowercase hex of at
// least 32 hex characters (16 bytes). It does NOT check whether the
// token corresponds to any actual stored value -- that is the
// store's job via VerifyHubSyncToken.
//
// This function exists so handlers can reject obviously malformed
// tokens before doing any work, and so callers can validate input
// without going to the store.
func IsSyncTokenFormat(s string) bool {
	if !strings.HasPrefix(s, HubSyncTokenPrefix) {
		return false
	}
	rest := s[len(HubSyncTokenPrefix):]
	if len(rest) < 32 {
		return false
	}
	for _, c := range rest {
		if !isLowerHex(c) {
			return false
		}
	}
	return true
}

func isLowerHex(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

// CompareSyncTokenHash returns true if presented (a cleartext sync
// token) hashes to the same value as stored (the hex-encoded SHA-256
// hash held by the store). The comparison is timing-safe.
//
// This is the function stores use inside VerifyHubSyncToken. The
// portal package exposes it so stores do not have to reimplement
// the format check + hash + constant-time compare sequence.
//
// Returns false (not an error) for a malformed presented token, a
// length-mismatched stored hash, or any comparison miss. Stores
// translate "false" into ErrUnauthorized for the wire response.
func CompareSyncTokenHash(presented, stored string) bool {
	if !IsSyncTokenFormat(presented) {
		return false
	}
	presentedHash := HashSyncToken(presented)
	if len(presentedHash) != len(stored) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presentedHash), []byte(stored)) == 1
}
