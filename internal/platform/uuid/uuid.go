// Package uuid generates RFC 4122 version-4 UUIDs using only the standard library.
package uuid

import (
	"crypto/rand"
	"fmt"
)

// New returns a random UUIDv4 string.
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand.Read never returns an error on supported platforms (Go 1.24+ panics internally).
		panic(fmt.Errorf("uuid.New: %w", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
