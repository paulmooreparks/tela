package portal

import (
	"strings"
	"testing"
)

func TestGenerateSyncToken_FormatAndUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tok, err := GenerateSyncToken()
		if err != nil {
			t.Fatalf("GenerateSyncToken: %v", err)
		}
		if !strings.HasPrefix(tok, HubSyncTokenPrefix) {
			t.Errorf("token %q missing required prefix %q", tok, HubSyncTokenPrefix)
		}
		// 32 random bytes encoded as hex = 64 chars; plus the prefix.
		want := len(HubSyncTokenPrefix) + 64
		if len(tok) != want {
			t.Errorf("token %q length = %d, want %d", tok, len(tok), want)
		}
		if !IsSyncTokenFormat(tok) {
			t.Errorf("freshly generated token %q failed IsSyncTokenFormat", tok)
		}
		if seen[tok] {
			t.Fatalf("duplicate token at iteration %d: %q", i, tok)
		}
		seen[tok] = true
	}
}

func TestIsSyncTokenFormat(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"prefix only", HubSyncTokenPrefix, false},
		{"missing prefix", "abcd1234567890abcd1234567890abcd1234", false},
		{"prefix with too-short body", HubSyncTokenPrefix + "abcd", false},
		{"prefix with valid hex body", HubSyncTokenPrefix + strings.Repeat("a", 64), true},
		{"prefix with uppercase hex body", HubSyncTokenPrefix + strings.Repeat("A", 64), false},
		{"prefix with non-hex body", HubSyncTokenPrefix + strings.Repeat("g", 64), false},
		{"prefix with mixed valid + invalid", HubSyncTokenPrefix + strings.Repeat("a", 32) + "GGGG" + strings.Repeat("a", 28), false},
		{"prefix with exactly 32 hex chars", HubSyncTokenPrefix + strings.Repeat("0", 32), true},
		{"prefix with 31 hex chars (one too few)", HubSyncTokenPrefix + strings.Repeat("0", 31), false},
		{"plausible-looking but wrong prefix", "hubadmin_" + strings.Repeat("0", 64), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsSyncTokenFormat(c.in); got != c.want {
				t.Errorf("IsSyncTokenFormat(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestHashSyncToken_DeterministicAndOpaque(t *testing.T) {
	tok, err := GenerateSyncToken()
	if err != nil {
		t.Fatal(err)
	}

	h1 := HashSyncToken(tok)
	h2 := HashSyncToken(tok)
	if h1 != h2 {
		t.Errorf("HashSyncToken not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("hash length = %d, want 64 (hex sha256)", len(h1))
	}
	if h1 == tok {
		t.Error("hash equals plaintext; HashSyncToken is doing nothing")
	}
}

func TestCompareSyncTokenHash_HappyPath(t *testing.T) {
	tok, err := GenerateSyncToken()
	if err != nil {
		t.Fatal(err)
	}
	stored := HashSyncToken(tok)

	if !CompareSyncTokenHash(tok, stored) {
		t.Error("matching token + hash should compare equal")
	}
}

func TestCompareSyncTokenHash_Mismatch(t *testing.T) {
	tokA, _ := GenerateSyncToken()
	tokB, _ := GenerateSyncToken()
	storedA := HashSyncToken(tokA)

	if CompareSyncTokenHash(tokB, storedA) {
		t.Error("mismatched token + hash should NOT compare equal")
	}
}

func TestCompareSyncTokenHash_MalformedPresented(t *testing.T) {
	tok, _ := GenerateSyncToken()
	stored := HashSyncToken(tok)

	cases := []string{
		"",
		"not-a-token",
		"hubsync_short",
		strings.Repeat("a", 64), // hex but no prefix
	}
	for _, bad := range cases {
		if CompareSyncTokenHash(bad, stored) {
			t.Errorf("malformed presented token %q compared equal", bad)
		}
	}
}

func TestCompareSyncTokenHash_LengthMismatchOnStored(t *testing.T) {
	tok, _ := GenerateSyncToken()
	// Truncated stored hash should not compare equal even if the
	// presented token is well-formed.
	if CompareSyncTokenHash(tok, "abcd") {
		t.Error("length-mismatched stored hash should not compare equal")
	}
}
