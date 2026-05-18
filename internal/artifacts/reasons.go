package artifacts

// VersionReason names the reason a new meeting version was committed. It
// is recorded in `ManifestVersion.Reason` so the version history is human-
// readable and machine-filterable.
type VersionReason string

const (
	ReasonSummaryCreated    VersionReason = "summary_created"
	ReasonTranscriptCreated VersionReason = "transcript_created"
	ReasonAudioImported     VersionReason = "audio_imported"
	ReasonSpeakerRenamed    VersionReason = "speaker_renamed"
	ReasonEdited            VersionReason = "edited"
)
