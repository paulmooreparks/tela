// Package certpin implements TLS certificate pinning for Tela hub
// connections. A pin is a SHA-256 hash of the leaf certificate's
// Subject Public Key Info (SPKI), formatted as "sha256:<hex>". The
// SPKI hash is the right thing to pin (rather than the certificate
// itself) because it survives certificate renewal with the same key
// and survives intermediate-CA reorganization at the upstream CA;
// it does not survive a key rotation, which is the desired
// behavior since a key rotation is exactly when the operator
// should be re-prompted to confirm the change.
//
// Two operations are supported:
//
//   - Capture: extract the pin string from a leaf certificate during
//     a successful TLS handshake. Used by TOFU first-connect flows
//     to record the pin for later verification.
//   - Verify: compare a presented certificate against an expected
//     pin string. Used by every subsequent connect to refuse
//     connections whose SPKI does not match.
//
// Pinning is not a substitute for CA validation; the dialer should
// continue to do normal certificate-chain validation (the standard
// tls.Config defaults). The pin is an additional layer that ties
// the connection to a specific public key the operator has
// previously approved, so a compromised CA cannot silently MITM
// the link.
//
// See ROADMAP-1.0.md "Cert pinning" and DESIGN-relay-gateway.md
// section 5.4 for the design discussion. The pin format and TOFU
// flow are documented in the security model section of the book
// (TODO: add when issue #42's security model doc lands).
package certpin

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Algorithm is the prefix that distinguishes the hash algorithm in
// the pin string. v1 supports only SHA-256.
const Algorithm = "sha256"

// HexLength is the expected number of hex characters following the
// algorithm prefix and colon separator. SHA-256 produces 32 bytes
// = 64 hex characters.
const HexLength = 64

// ErrMismatch is returned by Verify when the presented certificate's
// pin does not match the expected pin. Callers compare by errors.Is
// to distinguish a pin-mismatch refusal from a generic TLS error.
var ErrMismatch = errors.New("certpin: presented certificate does not match pinned fingerprint")

// ErrMalformed is returned by Parse and Verify when a pin string
// does not match the expected format.
var ErrMalformed = errors.New("certpin: malformed pin string")

// Capture returns the SHA-256 SPKI pin for a leaf certificate,
// formatted as "sha256:<lowercase hex>". The cert is taken as-is;
// callers are responsible for chain validation (the standard
// tls.Config does this before the pin verification callback runs).
func Capture(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return Algorithm + ":" + hex.EncodeToString(sum[:])
}

// CaptureChain returns the SHA-256 SPKI pin for the leaf certificate
// of a verified TLS connection state. The first cert in the
// VerifiedChains is the leaf; if no chain is available (mTLS
// callbacks before VerifyConnection has populated chains, or
// InsecureSkipVerify=true), falls back to the first PeerCertificate.
// Returns "" if neither is populated.
func CaptureChain(state tls.ConnectionState) string {
	if len(state.VerifiedChains) > 0 && len(state.VerifiedChains[0]) > 0 {
		return Capture(state.VerifiedChains[0][0])
	}
	if len(state.PeerCertificates) > 0 {
		return Capture(state.PeerCertificates[0])
	}
	return ""
}

// Parse validates a pin string and returns its hash bytes. Returns
// ErrMalformed if the string is not "sha256:<64-hex-chars>".
func Parse(pin string) ([]byte, error) {
	if pin == "" {
		return nil, fmt.Errorf("%w: empty pin", ErrMalformed)
	}
	parts := strings.SplitN(pin, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("%w: missing algorithm prefix in %q", ErrMalformed, pin)
	}
	if !strings.EqualFold(parts[0], Algorithm) {
		return nil, fmt.Errorf("%w: unsupported algorithm %q (want %q)", ErrMalformed, parts[0], Algorithm)
	}
	hexPart := strings.ToLower(parts[1])
	if len(hexPart) != HexLength {
		return nil, fmt.Errorf("%w: hash %q is %d hex chars, want %d", ErrMalformed, hexPart, len(hexPart), HexLength)
	}
	out, err := hex.DecodeString(hexPart)
	if err != nil {
		return nil, fmt.Errorf("%w: hex decode failed: %v", ErrMalformed, err)
	}
	return out, nil
}

// Normalize returns the canonical lowercase form of a pin string
// so two pins that differ only in case compare equal. Returns the
// input unchanged if it does not parse; callers that need
// validation should use Parse.
func Normalize(pin string) string {
	if _, err := Parse(pin); err != nil {
		return pin
	}
	return strings.ToLower(pin)
}

// Verify checks that the leaf cert in cs matches the expected pin.
// Returns nil on match, ErrMalformed if expected is not a valid
// pin string, and ErrMismatch if the connection's pin does not
// match expected.
//
// Use this from a tls.Config.VerifyConnection callback after the
// standard chain validation has populated VerifiedChains.
func Verify(cs tls.ConnectionState, expected string) error {
	if expected == "" {
		return nil // no pin configured: accept whatever the chain validator accepted
	}
	if _, err := Parse(expected); err != nil {
		return err
	}
	got := CaptureChain(cs)
	if got == "" {
		return fmt.Errorf("%w: no peer certificate available to pin", ErrMismatch)
	}
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("%w: got %s, want %s", ErrMismatch, got, expected)
	}
	return nil
}
