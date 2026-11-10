package shadowport

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// opaqueToken returns n random bytes rendered as lowercase hex. It backs the
// runtime-generated names (for example firewall rule names) that must not
// identify the tool, its vendor, or this fork.
func opaqueToken(n int) (string, error) {
	buffer := make([]byte, n)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate opaque token: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}
