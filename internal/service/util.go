package service

import (
	"crypto/rand"
	"encoding/hex"
	"os"
)

// pid returns the current process id.
func pid() int { return os.Getpid() }

// newID returns a 16-byte hex id (32 chars) suitable for job rows and
// recorder marker ids.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
