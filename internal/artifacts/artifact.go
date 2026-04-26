package artifacts

import "github.com/lukasstrickler/noto/internal/notoerr"

type ArtifactKind string

const (
	KindMeeting     ArtifactKind = "meeting"
	KindAudio       ArtifactKind = "audio"
	KindTranscript  ArtifactKind = "transcript"
	KindSummary     ArtifactKind = "summary"
	KindChecksums   ArtifactKind = "checksums"
	KindPrompt      ArtifactKind = "prompt"
)

type Artifact interface {
	Kind() ArtifactKind
	Version() string
	Validate() *notoerr.Error
}

type BaseArtifact struct {
	SchemaVersion string `json:"schema_version"`
}

func (b *BaseArtifact) Version() string {
	return b.SchemaVersion
}
