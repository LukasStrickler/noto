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
	DeleteByMeeting(ctx context.Context, meetingID string) error
}
