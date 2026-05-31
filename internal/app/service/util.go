package service

import (
	"crypto/rand"
	"encoding/hex"
	"os"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
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

// refsFromEvidence extracts non-empty segment IDs from evidence items.
func refsFromEvidence(ev []artifacts.Evidence) []string {
	if len(ev) == 0 {
		return nil
	}
	out := make([]string, 0, len(ev))
	for _, e := range ev {
		if e.SegmentID != "" {
			out = append(out, e.SegmentID)
		}
	}
	return out
}
