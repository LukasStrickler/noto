package speakerstore

import "context"

type SpeakerProfileRepository interface {
	Create(ctx context.Context, p SpeakerProfile) error
	Get(ctx context.Context, id string) (SpeakerProfile, error)
	List(ctx context.Context) ([]SpeakerProfile, error)
	Update(ctx context.Context, p SpeakerProfile) error
	Delete(ctx context.Context, id string) error
}

type MeetingSpeakerMappingRepository interface {
	Upsert(ctx context.Context, m MeetingSpeakerMapping) error
	ListByMeeting(ctx context.Context, meetingID string) ([]MeetingSpeakerMapping, error)
	// ListByProfile returns every mapping currently linked to a profile,
	// across all meetings — used for a person's meeting list, the
	// co-occurrence prior, and merge re-pointing.
	ListByProfile(ctx context.Context, profileID string) ([]MeetingSpeakerMapping, error)
	// ReassignProfile re-points every mapping from oldID to newID (used when
	// merging two profiles so no mapping is left dangling at the deleted one).
	ReassignProfile(ctx context.Context, oldID, newID string) error
	DeleteByMeeting(ctx context.Context, meetingID string) error
	// StatusCountsByMeeting returns, for every meeting that has mappings, a
	// count of mappings per match_status (meetingID → status → count). One
	// grouped query powers the dashboard's per-meeting identity rollup.
	StatusCountsByMeeting(ctx context.Context) (map[string]map[string]int, error)
	// CountUnresolved returns how many mappings are still unresolved (status
	// not auto/manual) across all meetings — the top bar's "to identify" count.
	CountUnresolved(ctx context.Context) (int, error)
}
