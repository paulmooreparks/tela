package certpin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

// makeTestCert builds a minimal self-signed ECDSA leaf cert plus its
// parsed *x509.Certificate so tests can exercise capture and verify
// paths without spinning up a TLS handshake.
func makeTestCert(t *testing.T) (*x509.Certificate, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return cert, der
}

func TestCapture_FormatAndDeterminism(t *testing.T) {
	cert, _ := makeTestCert(t)

	pin := Capture(cert)
	if !strings.HasPrefix(pin, "sha256:") {
		t.Errorf("Capture returned %q, missing sha256: prefix", pin)
	}
	hexPart := strings.TrimPrefix(pin, "sha256:")
	if len(hexPart) != HexLength {
		t.Errorf("Capture hex part = %d chars, want %d", len(hexPart), HexLength)
	}
	if hexPart != strings.ToLower(hexPart) {
		t.Errorf("Capture hex part should be lowercase: %q", hexPart)
	}

	// Determinism: same cert, same pin.
	if again := Capture(cert); again != pin {
		t.Errorf("Capture not deterministic: first %q, second %q", pin, again)
	}
}

func TestCapture_NilCert(t *testing.T) {
	if got := Capture(nil); got != "" {
		t.Errorf("Capture(nil) = %q, want empty", got)
	}
}

func TestCapture_DifferentCertsDifferentPins(t *testing.T) {
	a, _ := makeTestCert(t)
	b, _ := makeTestCert(t)
	if Capture(a) == Capture(b) {
		t.Errorf("two independently-generated certs produced identical pins; key generation entropy bug?")
	}
}

func TestParse_AcceptsCanonicalForm(t *testing.T) {
	cert, _ := makeTestCert(t)
	pin := Capture(cert)
	bytes, err := Parse(pin)
	if err != nil {
		t.Fatalf("Parse(%q) returned error: %v", pin, err)
	}
	if len(bytes) != sha256.Size {
		t.Errorf("Parse returned %d bytes, want %d", len(bytes), sha256.Size)
	}
}

func TestParse_AcceptsUppercaseHex(t *testing.T) {
	cert, _ := makeTestCert(t)
	pin := Capture(cert)
	upper := "sha256:" + strings.ToUpper(strings.TrimPrefix(pin, "sha256:"))
	if _, err := Parse(upper); err != nil {
		t.Errorf("Parse should be case-insensitive on hex: got %v on %q", err, upper)
	}
}

func TestParse_AcceptsUppercaseAlgorithm(t *testing.T) {
	cert, _ := makeTestCert(t)
	pin := Capture(cert)
	upper := "SHA256:" + strings.TrimPrefix(pin, "sha256:")
	if _, err := Parse(upper); err != nil {
		t.Errorf("Parse should accept uppercase algorithm: got %v on %q", err, upper)
	}
}

func TestParse_RejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		pin  string
	}{
		{"empty", ""},
		{"no-colon", "sha2566f96e..."},
		{"wrong-algorithm", "md5:abcdef0123456789abcdef0123456789"},
		{"too-short-hex", "sha256:beef"},
		{"too-long-hex", "sha256:" + strings.Repeat("a", HexLength+1)},
		{"non-hex", "sha256:" + strings.Repeat("z", HexLength)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.pin); err == nil {
				t.Errorf("Parse(%q) accepted malformed input", tc.pin)
			} else if !errors.Is(err, ErrMalformed) {
				t.Errorf("Parse(%q) returned %v, want ErrMalformed", tc.pin, err)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	cert, _ := makeTestCert(t)
	pin := Capture(cert)
	upper := "SHA256:" + strings.ToUpper(strings.TrimPrefix(pin, "sha256:"))
	if got := Normalize(upper); got != strings.ToLower(upper) {
		t.Errorf("Normalize(%q) = %q, want lowercase form", upper, got)
	}
	// Malformed input passes through unchanged.
	mal := "not a pin"
	if got := Normalize(mal); got != mal {
		t.Errorf("Normalize on malformed input changed value: %q -> %q", mal, got)
	}
}

func TestVerify_EmptyExpectedAcceptsAnything(t *testing.T) {
	cert, _ := makeTestCert(t)
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	if err := Verify(cs, ""); err != nil {
		t.Errorf("Verify with empty expected pin should accept; got %v", err)
	}
}

func TestVerify_MatchingPin(t *testing.T) {
	cert, _ := makeTestCert(t)
	expected := Capture(cert)
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	if err := Verify(cs, expected); err != nil {
		t.Errorf("Verify with matching pin returned %v", err)
	}
}

func TestVerify_MismatchPin(t *testing.T) {
	cert, _ := makeTestCert(t)
	otherCert, _ := makeTestCert(t)
	expected := Capture(otherCert) // pin for a different cert
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	err := Verify(cs, expected)
	if err == nil {
		t.Fatal("Verify with mismatched pin should fail")
	}
	if !errors.Is(err, ErrMismatch) {
		t.Errorf("Verify mismatch returned %v, want ErrMismatch", err)
	}
	// The error message should name both got and want for diagnosis.
	msg := err.Error()
	if !strings.Contains(msg, "got ") || !strings.Contains(msg, "want ") {
		t.Errorf("mismatch error should describe got/want; got %q", msg)
	}
}

func TestVerify_MalformedExpectedPin(t *testing.T) {
	cert, _ := makeTestCert(t)
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	err := Verify(cs, "garbage")
	if err == nil {
		t.Fatal("Verify with malformed expected pin should fail")
	}
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("Verify with malformed expected returned %v, want ErrMalformed", err)
	}
}

func TestVerify_NoPeerCertificates(t *testing.T) {
	cert, _ := makeTestCert(t)
	expected := Capture(cert)
	cs := tls.ConnectionState{} // no PeerCertificates, no VerifiedChains
	err := Verify(cs, expected)
	if err == nil {
		t.Fatal("Verify against empty connection state should fail")
	}
	if !errors.Is(err, ErrMismatch) {
		t.Errorf("Verify with no peer cert returned %v, want ErrMismatch (no cert == cannot match)", err)
	}
}

func TestCaptureChain_PrefersVerifiedChain(t *testing.T) {
	cert, _ := makeTestCert(t)
	otherCert, _ := makeTestCert(t)
	// VerifiedChains[0][0] is the leaf the chain validator accepted;
	// PeerCertificates[0] could be a different leaf the peer sent
	// (this should never happen in practice, but the function should
	// prefer VerifiedChains as the authoritative source).
	cs := tls.ConnectionState{
		VerifiedChains:   [][]*x509.Certificate{{cert}},
		PeerCertificates: []*x509.Certificate{otherCert},
	}
	got := CaptureChain(cs)
	if got != Capture(cert) {
		t.Errorf("CaptureChain returned %q, want pin from VerifiedChains[0][0]", got)
	}
}

func TestCaptureChain_FallsBackToPeerCert(t *testing.T) {
	cert, _ := makeTestCert(t)
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	if got := CaptureChain(cs); got != Capture(cert) {
		t.Errorf("CaptureChain fallback to PeerCertificates returned %q, want %q", got, Capture(cert))
	}
}

func TestCaptureChain_Empty(t *testing.T) {
	if got := CaptureChain(tls.ConnectionState{}); got != "" {
		t.Errorf("CaptureChain on empty state = %q, want empty string", got)
	}
}

func TestCapture_ComputesSPKINotCertHash(t *testing.T) {
	// Sanity check that Capture is hashing RawSubjectPublicKeyInfo,
	// not Raw (the whole cert). Pin a known cert, manually compute
	// SHA-256 of its SPKI, and compare.
	cert, _ := makeTestCert(t)
	pin := Capture(cert)
	expectedHex := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	want := "sha256:" + hex.EncodeToString(expectedHex[:])
	if pin != want {
		t.Errorf("Capture pinned the wrong bytes: got %q, want %q", pin, want)
	}
}
