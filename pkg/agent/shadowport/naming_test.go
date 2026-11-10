package shadowport

import (
	"regexp"
	"testing"
)

func TestOpaqueTokenUsesLowercaseHex(t *testing.T) {
	token, err := opaqueToken(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 16 {
		t.Fatalf("token length = %d, want 16", len(token))
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(token) {
		t.Fatalf("token = %q, want 16 lowercase hex characters", token)
	}
}

func TestOpaqueTokenIsUnique(t *testing.T) {
	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		token, err := opaqueToken(8)
		if err != nil {
			t.Fatal(err)
		}
		if _, duplicate := seen[token]; duplicate {
			t.Fatalf("duplicate token %q after %d draws", token, i+1)
		}
		seen[token] = struct{}{}
	}
}
