package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/speakers"
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

// countPeopleToReview counts profiles that carry at least one unconfirmed
// mapping (auto-created/linked but not yet reviewed) — the People nav pill's
// attention signal. Person-centric, so it stays distinct from the meeting-side
// CountUnresolved that drives SpeakersToID.
func (s *Service) countPeopleToReview() int {
	if s.speakerProfiles == nil {
		return 0
	}
	profs, err := s.ListSpeakerProfiles(context.Background())
	if err != nil {
		return 0
	}
	n := 0
	for _, p := range profs {
		if p.UnconfirmedMeetings > 0 {
			n++
		}
	}
	return n
}

func (s *Service) GetSpeakerProfile(_ context.Context, id string) (notoapi.SpeakerProfile, error) {
	if _, err := uuid.Parse(id); err != nil {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeInvalidRequest, "profile id is not a valid UUID", map[string]any{"id": id})
	}
	p, err := s.speakerProfiles.Get(context.Background(), id)
	if err != nil {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeNotFound, "speaker profile not found", map[string]any{"id": id})
	}
	api := s.profileToAPI(p)
	api.Meetings = s.profileMeetings(id)
	return api, nil
}

// profileMeetings lists the meetings a profile appears in (newest first), with
// titles resolved from the meeting repo — for the People screen detail.
func (s *Service) profileMeetings(profileID string) []notoapi.ProfileMeetingRef {
	maps, err := s.meetingMappings.ListByProfile(context.Background(), profileID)
	if err != nil || len(maps) == 0 {
		return nil
	}
	out := make([]notoapi.ProfileMeetingRef, 0, len(maps))
	for _, m := range maps {
		ref := notoapi.ProfileMeetingRef{MeetingID: m.MeetingID, MatchStatus: m.MatchStatus}
		if mid, perr := uuid.Parse(m.MeetingID); perr == nil {
			if sm, gerr := s.repo.GetMeeting(context.Background(), mid); gerr == nil {
				ref.Title = sm.Title
				ref.CreatedAt = sm.CreatedAt
			}
		}
		out = append(out, ref)
	}
	return out
}

func (s *Service) CreateSpeakerProfile(_ context.Context, req notoapi.CreateSpeakerProfileRequest) (notoapi.SpeakerProfile, error) {
	if req.DisplayName == "" {
		return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeInvalidRequest, "display_name is required", nil)
	}
	now := time.Now()
	p := speakerstore.SpeakerProfile{
		ID:           uuid.New().String(),
		DisplayName:  req.DisplayName,
		Email:        req.Email,
		Pronouns:     req.Pronouns,
		Notes:        req.Notes,
		Affiliations: affiliationsFromAPI(req.Affiliations),
		CreatedAt:    now,
		UpdatedAt:    now,
		LastSeenAt:   nil,
	}
	// Seed the voiceprint from a meeting-speaker when assigning "+ new person",
	// and link that mapping to the freshly-created profile in one step.
	if req.FromMeetingID != "" && req.FromSpeakerID != "" {
		if maps, err := s.meetingMappings.ListByMeeting(context.Background(), req.FromMeetingID); err == nil {
			for _, m := range maps {
				if m.MeetingSpeakerID == req.FromSpeakerID && len(m.EmbeddingVector) > 0 {
					p.EmbeddingVector = m.EmbeddingVector
					p.EmbeddingDim = m.EmbeddingDim
					p.EmbeddingModel = s.embedderModelID()
					p.LastSeenAt = &now
					break
				}
			}
		}
	}
	if err := s.speakerProfiles.Create(context.Background(), p); err != nil {
		return notoapi.SpeakerProfile{}, err
	}
	if req.FromMeetingID != "" && req.FromSpeakerID != "" {
		_ = s.assignMeetingSpeaker(req.FromMeetingID, req.FromSpeakerID, p.ID)
	}
	s.emitStatusBar() // a new/linked person changes the "to identify" count
	return s.profileToAPI(p), nil
}

// foldEmbeddingIntoProfile teaches a profile from one more enrollment observation,
// updating its centroid as a count-weighted running mean (speakers.RunningMean) so
// each new voiceprint — an auto-match OR a human confirmation — moves an established
// profile only ~1/(count+1). Best-effort: a missing profile or empty embedding is a
// no-op, since identity learning must never fail the operation that triggered it.
func (s *Service) foldEmbeddingIntoProfile(ctx context.Context, profileID string, emb []float64) {
	if profileID == "" || len(emb) == 0 {
		return
	}
	p, err := s.speakerProfiles.Get(ctx, profileID)
	if err != nil {
		return
	}
	now := time.Now()
	if len(p.EmbeddingVector) == 0 {
		// First voiceprint for this profile (e.g. one created without audio).
		p.EmbeddingVector = emb
		p.EmbeddingDim = len(emb)
		p.EmbeddingCount = 1
	} else {
		centroid, cerr := speakers.RunningMean(p.EmbeddingVector, p.EmbeddingCount, emb)
		if cerr != nil || len(centroid) == 0 {
			return
		}
		p.EmbeddingVector = centroid
		p.EmbeddingDim = len(centroid)
		p.EmbeddingCount = max(p.EmbeddingCount, 1) + 1
	}
	p.LastSeenAt = &now
	p.UpdatedAt = now
	_ = s.speakerProfiles.Update(ctx, p)
}

// assignMeetingSpeaker links a meeting speaker to a profile and marks it manual.
func (s *Service) assignMeetingSpeaker(meetingID, speakerID, profileID string) error {
	maps, err := s.meetingMappings.ListByMeeting(context.Background(), meetingID)
	if err != nil {
		return err
	}
	for _, m := range maps {
		if m.MeetingSpeakerID == speakerID {
			pid := profileID
			m.ProfileID = &pid
			m.MatchStatus = "manual"
			m.UpdatedAt = time.Now()
			return s.meetingMappings.Upsert(context.Background(), m)
		}
	}
	return nil
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
	if patch.Notes != nil {
		p.Notes = *patch.Notes
	}
	if patch.Affiliations != nil {
		p.Affiliations = affiliationsFromAPI(*patch.Affiliations)
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
	s.emitStatusBar() // unlinking a person can re-open speakers as unresolved
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
	if target.Notes == "" && source.Notes != "" {
		target.Notes = source.Notes
	}
	target.Affiliations = mergeAffiliations(target.Affiliations, source.Affiliations)
	// Combine voiceprints so the surviving profile represents both enrollments,
	// weighted by how many each side accumulated — merging a 1-enrollment profile
	// into a 20-enrollment one must barely move the latter, not average it 50/50.
	if len(source.EmbeddingVector) > 0 {
		if len(target.EmbeddingVector) == 0 {
			target.EmbeddingVector = source.EmbeddingVector
			target.EmbeddingDim = source.EmbeddingDim
			target.EmbeddingCount = max(source.EmbeddingCount, 1)
		} else {
			tc, sc := max(target.EmbeddingCount, 1), max(source.EmbeddingCount, 1)
			if c, cerr := speakers.WeightedMean(target.EmbeddingVector, float64(tc), source.EmbeddingVector, float64(sc)); cerr == nil && len(c) > 0 {
				target.EmbeddingVector = c
				target.EmbeddingDim = len(c)
				target.EmbeddingCount = tc + sc
			}
		}
	}
	target.UpdatedAt = time.Now()
	if err := s.speakerProfiles.Update(context.Background(), target); err != nil {
		return notoapi.SpeakerProfile{}, err
	}
	// Re-point every mapping that referenced source so nothing dangles at the
	// soon-to-be-deleted profile, then drop source.
	if err := s.meetingMappings.ReassignProfile(context.Background(), sourceID, targetID); err != nil {
		return notoapi.SpeakerProfile{}, err
	}
	_ = s.speakerProfiles.Delete(context.Background(), sourceID)
	s.emitStatusBar() // merging folds two people into one — recount unresolved
	return s.profileToAPI(target), nil
}

// mergeAffiliations concatenates two affiliation lists, dropping exact dupes.
func mergeAffiliations(a, b []speakerstore.Affiliation) []speakerstore.Affiliation {
	seen := map[speakerstore.Affiliation]bool{}
	var out []speakerstore.Affiliation
	for _, list := range [][]speakerstore.Affiliation{a, b} {
		for _, af := range list {
			if !seen[af] {
				seen[af] = true
				out = append(out, af)
			}
		}
	}
	return out
}

func (s *Service) GetMeetingSpeakerMappings(_ context.Context, meetingID string) (notoapi.MeetingSpeakerMappings, error) {
	mappings, err := s.meetingMappings.ListByMeeting(context.Background(), meetingID)
	if err != nil {
		return notoapi.MeetingSpeakerMappings{}, err
	}
	return notoapi.MeetingSpeakerMappings{
		MeetingID: meetingID,
		Mappings:  s.enrichMappings(meetingID, mappings),
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
				oldProfile := ""
				if m.ProfileID != nil {
					oldProfile = *m.ProfileID
				}
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
				// A human (re)assigning this speaker to a DIFFERENT profile is the
				// strongest enrollment signal there is — teach that profile from the
				// speaker's stored voiceprint. Only on an actual profile change: a
				// confirm-in-place (same profile) was already folded by the auto path,
				// so re-folding would double-count the same embedding.
				if m.ProfileID != nil && *m.ProfileID != "" && *m.ProfileID != oldProfile {
					s.foldEmbeddingIntoProfile(context.Background(), *m.ProfileID, m.EmbeddingVector)
				}
				break
			}
		}
		if !found {
			return notoapi.MeetingSpeakerMappings{}, notoapi.NewError(notoapi.CodeNotFound, "speaker mapping not found", map[string]any{"meeting_speaker_id": entry.MeetingSpeakerID})
		}
	}
	s.emitStatusBar() // assigning/reassigning a speaker changes the "to identify" count
	return s.GetMeetingSpeakerMappings(context.Background(), meetingID)
}

func (s *Service) profileToAPI(p speakerstore.SpeakerProfile) notoapi.SpeakerProfile {
	count, unconfirmed := 0, 0
	if maps, err := s.meetingMappings.ListByProfile(context.Background(), p.ID); err == nil {
		count = len(maps)
		for _, m := range maps {
			if m.MatchStatus != "auto" && m.MatchStatus != "manual" {
				unconfirmed++ // auto-linked but not yet reviewed (e.g. a "new" mint)
			}
		}
	}
	return notoapi.SpeakerProfile{
		ID:                  p.ID,
		DisplayName:         p.DisplayName,
		Email:               p.Email,
		Pronouns:            p.Pronouns,
		Notes:               p.Notes,
		Affiliations:        affiliationsToAPI(p.Affiliations),
		EmbeddingDim:        p.EmbeddingDim,
		EmbeddingModel:      p.EmbeddingModel,
		MeetingCount:        count,
		UnconfirmedMeetings: unconfirmed,
		CreatedAt:           p.CreatedAt,
		UpdatedAt:           p.UpdatedAt,
		LastSeenAt:          p.LastSeenAt,
	}
}

func affiliationsToAPI(in []speakerstore.Affiliation) []notoapi.Affiliation {
	if len(in) == 0 {
		return nil
	}
	out := make([]notoapi.Affiliation, len(in))
	for i, a := range in {
		out[i] = notoapi.Affiliation{Context: a.Context, Organization: a.Organization, Email: a.Email}
	}
	return out
}

func affiliationsFromAPI(in []notoapi.Affiliation) []speakerstore.Affiliation {
	if len(in) == 0 {
		return nil
	}
	out := make([]speakerstore.Affiliation, len(in))
	for i, a := range in {
		out[i] = speakerstore.Affiliation{Context: a.Context, Organization: a.Organization, Email: a.Email}
	}
	return out
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
