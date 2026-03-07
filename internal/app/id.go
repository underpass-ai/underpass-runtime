package app

import (
	"crypto/rand"
	"encoding/hex"
)

func newID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return prefix + "-fallback"
	}
	return prefix + "-" + hex.EncodeToString(buf)
}
