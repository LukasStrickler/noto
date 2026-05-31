package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

func (s *Service) ListSpeakerProfiles(_ context.Context) ([]notoapi.SpeakerProfile, error) {
	profiles, err := s.speakerProfiles.List(context.Background())
	if err != nil {
		return nil, err
	}
	return s.profilesToAPI(profiles), nil
}

func (s *Service) GetSpeakerProfile(_ context.Context, id string) (notoapi.SpeakerProfile, error) {
	if _, err := uuid.Parse(id); err != nil {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeInvalidRequest, "profile id is not a valid UUID", map[string]any{"id": id})
	}
	p, err := s.speakerProfiles.Get(context.Background(), id)
	if err != nil {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeNotFound, "speaker profile not found", map[string]any{"id": id})
	}
	return s.profileToAPI(p), nil
}

func (s *Service) CreateSpeakerProfile(_ context.Context, req notoapi.CreateSpeakerProfileRequest) (notoapi.SpeakerProfile, error) {
	if req.DisplayName == "" {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeInvalidRequest, "display_name is required", nil)
	}
	now := time.Now()
	p := speakerstore.SpeakerProfile{
		ID:          uuid.New().String(),
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Pronouns:    req.Pronouns,
		CreatedAt:   now,
		UpdatedAt:   now,
		LastSeenAt:  nil,
	}
	if err := s.speakerProfiles.Create(context.Background(), p); err != nil {
		return notoapi.SpeakerProfile{}, err
	}
	return s.profileToAPI(p), nil
}

func (s *Service) PatchSpeakerProfile(_ context.Context, id string, patch notoapi.SpeakerProfilePatch) (notoapi.SpeakerProfile, error) {
	if _, err := uuid.Parse(id); err != nil {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeInvalidRequest, "profile id is not a valid UUID", map[string]any{"id": id})
	}
	p, err := s.speakerProfiles.Get(context.Background(), id)
	if err != nil {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeNotFound, "speaker profile not found", map[string]any{"id": id})
	}
	if patch.DisplayName != nil {
		p.DisplayName = *patch.DisplayName
	}
	if patch.Email != nil {
		p.Email = *patch.Email
	}
	if patch.Pronouns != nil {
		p.Pronouns = *patch.Pronouns
	}
	p.UpdatedAt = time.Now()
	if err := s.speakerProfiles.Update(context.Background(), p); err != nil {
		return notoapi.SpeakerProfile{}, err
	}
	return s.profileToAPI(p), nil
}

func (s *Service) DeleteSpeakerProfile(_ context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "profile id is not a valid UUID", map[string]any{"id": id})
	}
	if err := s.speakerProfiles.Delete(context.Background(), id); err != nil {
		return notoapi.NewError(notoapi.CodeNotFound, "speaker profile not found", map[string]any{"id": id})
	}
	return nil
}

func (s *Service) MergeSpeakerProfiles(_ context.Context, targetID, sourceID string) (notoapi.SpeakerProfile, error) {
	if _, err := uuid.Parse(targetID); err != nil {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeInvalidRequest, "target_profile_id is not a valid UUID", map[string]any{"id": targetID})
	}
	if _, err := uuid.Parse(sourceID); err != nil {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeInvalidRequest, "source_profile_id is not a valid UUID", map[string]any{"id": sourceID})
	}
	if targetID == sourceID {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeInvalidRequest, "target and source must be different", nil)
	}
	target, err := s.speakerProfiles.Get(context.Background(), targetID)
	if err != nil {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeNotFound, "target speaker profile not found", map[string]any{"id": targetID})
	}
	source, err := s.speakerProfiles.Get(context.Background(), sourceID)
	if err != nil {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeNotFound, "source speaker profile not found", map[string]any{"id": sourceID})
	}
	if target.DisplayName == "" && source.DisplayName != "" {
		target.DisplayName = source.DisplayName
	}
	if target.Email == "" && source.Email != "" {
		target.Email = source.Email
	}
	if target.Pronouns == "" && source.Pronouns != "" {
		target.Pronouns = source.Pronouns
	}
	target.UpdatedAt = time.Now()
	if err := s.speakerProfiles.Update(context.Background(), target); err != nil {
		return notoapi.SpeakerProfile{}, err
	}
	_ = s.speakerProfiles.Delete(context.Background(), sourceID)
	return s.profileToAPI(target), nil
}

func (s *Service) GetMeetingSpeakerMappings(_ context.Context, meetingID string) (notoapi.MeetingSpeakerMappings, error) {
	mappings, err := s.meetingMappings.ListByMeeting(context.Background(), meetingID)
	if err != nil {
		return notoapi.MeetingSpeakerMappings{}, err
	}
	return notoapi.MeetingSpeakerMappings{
		MeetingID: meetingID,
		Mappings:  s.mappingsToAPI(mappings),
	}, nil
}

func (s *Service) PatchMeetingSpeakerMappings(_ context.Context, meetingID string, patch notoapi.MeetingSpeakerMappingsPatch) (notoapi.MeetingSpeakerMappings, error) {
	for _, entry := range patch.Mappings {
		existing, err := s.meetingMappings.ListByMeeting(context.Background(), meetingID)
		if err != nil {
			return notoapi.MeetingSpeakerMappings{}, err
		}
		var found bool
		for _, m := range existing {
			if m.MeetingSpeakerID == entry.MeetingSpeakerID {
				found = true
				if entry.ProfileID != nil {
					m.ProfileID = entry.ProfileID
				}
				if entry.MatchConfidence != nil {
					m.MatchConfidence = entry.MatchConfidence
				}
				if entry.MatchStatus != nil {
					m.MatchStatus = *entry.MatchStatus
				}
				m.UpdatedAt = time.Now()
				if err := s.meetingMappings.Upsert(context.Background(), m); err != nil {
					return notoapi.MeetingSpeakerMappings{}, err
				}
				break
			}
		}
		if !found {
			return notoapi.MeetingSpeakerMappings{}, notoapi.NewError(notoapi.CodeNotFound, "speaker mapping not found", map[string]any{"meeting_speaker_id": entry.MeetingSpeakerID})
		}
	}
	return s.GetMeetingSpeakerMappings(context.Background(), meetingID)
}

func (s *Service) profileToAPI(p speakerstore.SpeakerProfile) notoapi.SpeakerProfile {
	return notoapi.SpeakerProfile{
		ID:             p.ID,
		DisplayName:    p.DisplayName,
		Email:          p.Email,
		Pronouns:       p.Pronouns,
		EmbeddingDim:   p.EmbeddingDim,
		EmbeddingModel: p.EmbeddingModel,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
		LastSeenAt:     p.LastSeenAt,
	}
}

func (s *Service) profilesToAPI(profiles []speakerstore.SpeakerProfile) []notoapi.SpeakerProfile {
	out := make([]notoapi.SpeakerProfile, len(profiles))
	for i, p := range profiles {
		out[i] = s.profileToAPI(p)
	}
	return out
}

func (s *Service) mappingToAPI(m speakerstore.MeetingSpeakerMapping) notoapi.MeetingSpeakerMapping {
	return notoapi.MeetingSpeakerMapping{
		MeetingID:        m.MeetingID,
		MeetingSpeakerID: m.MeetingSpeakerID,
		ProviderLabel:    m.ProviderLabel,
		ProfileID:        m.ProfileID,
		MatchConfidence:  m.MatchConfidence,
		MatchStatus:      m.MatchStatus,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}

func (s *Service) mappingsToAPI(mappings []speakerstore.MeetingSpeakerMapping) []notoapi.MeetingSpeakerMapping {
	out := make([]notoapi.MeetingSpeakerMapping, len(mappings))
	for i, m := range mappings {
		out[i] = s.mappingToAPI(m)
	}
	return out
}
