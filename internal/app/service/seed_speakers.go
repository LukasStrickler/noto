package service

import (
	"context"
	"fmt"
	"hash/crc32"
	"time"

	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
)

// Speaker-identity seeding. This layers a realistic People directory on top of
// the fixture meetings (seed.go) so the People screen, the Speakers-tab
// assignment flow, the dashboard identity clusters, and the top-bar
// "⚑ N to identify" badge all have honest, end-to-end data to render:
//
//   - every named fixture speaker is a real cross-meeting person, with the core
//     trio (Alice/Bob/Carol) recurring across two meetings so the co-attendance
//     / recurring-person prior has something to work with;
//   - profiles carry pronouns, notes and multi-context affiliations so the
//     People detail pane is well populated;
//   - a handful of speakers are deliberately left in each NON-resolved state
//     (see seedAttention) so the dashboard shows the full triage spread
//     (◐ likely · ✦ new · ○ not set · ✓ all set), not an all-green run, and the
//     top bar reads "⚑ 4 to identify". Because every embedding is real, the
//     candidate ranking on the Speakers tab is produced by the live matcher:
//       · Carol in "Design review: Search v2" → pending (likely), ranked
//         [Carol (a frequent co-attendee), Riya (a plausible voice match)];
//       · Bob in the same meeting → not set (matched no one here), so the
//         Identify dialog falls back to search / create;
//       · Marc in the mobile-beta kickoff → pending (likely);
//       · Sam in the security incident → a fresh voice → an auto-minted
//         provisional profile (named only by its diarization label, no other
//         config), i.e. "there but not configured" — exercises naming/merging.
//
// All IDs/embeddings are deterministic, so re-running `noto seed` overwrites in
// place rather than duplicating.

const seedEmbedDim = 192

// seedPerson is a fixture member of the People directory. speakerID matches the
// transcript speaker id used across the fixture meetings (so a person is the
// SAME identity everywhere they appear). slot selects a near-orthogonal voice
// axis; nearSlot/nearWeight optionally blend toward another person's axis to
// model two voices that genuinely sound alike.
type seedPerson struct {
	speakerID  string
	name       string
	pronouns   string
	notes      string
	affils     []speakerstore.Affiliation
	slot       int
	nearSlot   int
	nearWeight float64
}

func aff(context, org, email string) speakerstore.Affiliation {
	return speakerstore.Affiliation{Context: context, Organization: org, Email: email}
}

// seedPeople is the directory. The first three are richly detailed (they're the
// recurring team the demo revolves around); the rest are concise but real.
// Sam is intentionally absent — he's the "unknown voice" in the security
// incident, so naming him exercises profile creation.
var seedPeople = []seedPerson{
	{
		speakerID: "spk_alice", name: "Alice Nguyen", pronouns: "she/her",
		notes:  "Eng lead. Owns roadmap + the search v2 ship.",
		affils: []speakerstore.Affiliation{aff("Work", "noto", "alice@noto.app")},
		slot:   1,
	},
	{
		speakerID: "spk_bob", name: "Bob Martins", pronouns: "he/him",
		notes: "Search v2 DRI. FTS5 + ranking.",
		affils: []speakerstore.Affiliation{
			aff("Work", "noto", "bob@noto.app"),
			aff("Open source", "FTS5 working group", "bob@fts.dev"),
		},
		slot: 2,
	},
	{
		speakerID: "spk_carol", name: "Carol Reyes", pronouns: "she/her",
		notes: "Platform + infra. Runs the search index migrations.",
		affils: []speakerstore.Affiliation{
			aff("Work", "noto", "carol@noto.app"),
			aff("University", "TU Berlin", "c.reyes@tu-berlin.de"),
		},
		slot: 3,
	},
	{speakerID: "spk_dana", name: "Dana Okafor", pronouns: "she/her", notes: "Eng manager at Acme; evaluating noto.", affils: []speakerstore.Affiliation{aff("Customer", "Acme Corp", "dana@acme.example")}, slot: 4},
	{speakerID: "spk_evan", name: "Evan Brooks", pronouns: "he/him", notes: "Solutions / customer discovery.", affils: []speakerstore.Affiliation{aff("Work", "noto", "evan@noto.app")}, slot: 5},
	{speakerID: "spk_fiona", name: "Fiona Walsh", pronouns: "she/her", notes: "Billing service owner.", affils: []speakerstore.Affiliation{aff("Work", "noto", "fiona@noto.app")}, slot: 6},
	{speakerID: "spk_greg", name: "Greg Park", pronouns: "he/him", notes: "Payments + webhooks.", affils: []speakerstore.Affiliation{aff("Work", "noto", "greg@noto.app")}, slot: 7},
	{speakerID: "spk_priya", name: "Priya Anand", pronouns: "she/her", notes: "Mobile PM. Runs the beta program.", affils: []speakerstore.Affiliation{aff("Work", "noto", "priya@noto.app")}, slot: 8},
	{speakerID: "spk_marc", name: "Marc Dubois", pronouns: "he/him", notes: "iOS lead.", affils: []speakerstore.Affiliation{aff("Work", "noto", "marc@noto.app")}, slot: 9},
	{speakerID: "spk_lin", name: "Lin Zhao", pronouns: "they/them", notes: "Mobile infra + observability.", affils: []speakerstore.Affiliation{aff("Work", "noto", "lin@noto.app")}, slot: 10},
	{speakerID: "spk_omar", name: "Omar Haddad", pronouns: "he/him", notes: "Security engineering.", affils: []speakerstore.Affiliation{aff("Work", "noto", "omar@noto.app")}, slot: 11},
	{speakerID: "spk_rita", name: "Rita Gomes", pronouns: "she/her", notes: "Incident response.", affils: []speakerstore.Affiliation{aff("Work", "noto", "rita@noto.app")}, slot: 12},
	{speakerID: "spk_nick", name: "Nick Aldridge", pronouns: "he/him", notes: "Hiring manager, backend.", affils: []speakerstore.Affiliation{aff("Work", "noto", "nick@noto.app")}, slot: 13},
	{speakerID: "spk_yuri", name: "Yuri Sokolov", pronouns: "he/him", notes: "Staff engineer, routing.", affils: []speakerstore.Affiliation{aff("Work", "noto", "yuri@noto.app")}, slot: 14},
	{speakerID: "spk_paula", name: "Paula Mendes", pronouns: "she/her", notes: "Backend interviewer.", affils: []speakerstore.Affiliation{aff("Work", "noto", "paula@noto.app")}, slot: 15},
	{speakerID: "spk_eve", name: "Eve Carter", pronouns: "she/her", notes: "Finance / vendor contracts.", affils: []speakerstore.Affiliation{aff("Work", "noto", "eve@noto.app")}, slot: 16},
	{speakerID: "spk_dan", name: "Dan Reilly", pronouns: "he/him", notes: "Infra lead. Owns observability spend.", affils: []speakerstore.Affiliation{aff("Work", "noto", "dan@noto.app")}, slot: 17},
	// Riya's voice sits near Carol's (nearSlot=3) so she surfaces as a plausible
	// runner-up when Carol is unresolved — the context prior is what keeps Carol
	// on top, not the voice score alone.
	{speakerID: "spk_riya", name: "Riya Kapoor", pronouns: "she/her", notes: "Infra. Grafana migration POC.", affils: []speakerstore.Affiliation{aff("Work", "noto", "riya@noto.app")}, slot: 18, nearSlot: 3, nearWeight: 0.66},
}

// seedAttentionKind marks how a deliberately-unresolved fixture speaker reads on
// the dashboard + Speakers tab, so the demo shows the full triage spread instead
// of an all-resolved run.
type seedAttentionKind int

const (
	attnLikely seedAttentionKind = iota // strong unconfirmed candidate → status "pending"
	attnNew                             // auto-minted provisional profile, unreviewed → "new"
	attnNotSet                          // voice matched no one this meeting → "unmatched"
)

// seedAttention overrides specific (meeting, speaker) pairs into a non-resolved
// state. Keyed by fixture meeting ID → speaker ID → kind. The matching tests in
// seed_speakers_test.go lock these in.
var seedAttention = map[string]map[string]seedAttentionKind{
	// "Mobile beta kickoff" — Marc is a strong but unconfirmed match.
	"44444444-4444-4444-8444-444444444444": {"spk_marc": attnLikely},
	// "Security incident" — Sam is a fresh voice → a provisional profile.
	"55555555-5555-4555-8555-555555555555": {"spk_sam": attnNew},
	// "Design review: Search v2" — Carol is unresolved but the prior still
	// surfaces her #1 (she co-attends Q2); Bob's voice matched no one here, so
	// he reads as not-set. Gives this row a mixed ◐/○ cluster.
	"88888888-8888-4888-8888-888888888888": {
		"spk_carol": attnLikely,
		"spk_bob":   attnNotSet,
	},
}

// seedProfileID derives a stable UUID for a directory person from their voice slot.
func seedProfileID(slot int) string {
	return fmt.Sprintf("5eed0000-0000-4000-8000-%012d", slot)
}

// seedProvisionalID derives a stable UUID for an auto-minted provisional ("new")
// profile from its meeting-speaker id — a separate UUID space from seedProfileID
// so the two never collide.
func seedProvisionalID(speakerID string) string {
	return fmt.Sprintf("5eed1111-1111-4111-8111-%012d", crc32.ChecksumIEEE([]byte(speakerID)))
}

// seedBase returns a unit vector along one near-orthogonal voice axis.
func seedBase(slot int) []float64 {
	v := make([]float64, seedEmbedDim)
	v[slot%seedEmbedDim] = 1
	return v
}

// seedVoiceprint is a person's enrolled profile vector. With nearWeight>0 it
// blends toward another person's axis to model two similar-sounding voices.
func (p seedPerson) seedVoiceprint() []float64 {
	v := seedBase(p.slot)
	if p.nearWeight > 0 {
		v[p.nearSlot%seedEmbedDim] = p.nearWeight
	}
	return v
}

// seedSample is a meeting-speaker's voiceprint: the person's enrolled vector
// plus a touch of drift, so cosine to the profile is high but not a perfect 1.
func (p seedPerson) seedSample() []float64 {
	v := p.seedVoiceprint()
	v[(p.slot+5)%seedEmbedDim] += 0.15
	return v
}

// seedUnknownSample is a voiceprint along an axis no profile occupies — it
// matches nobody, so the speaker reads as "unknown".
func seedUnknownSample() []float64 {
	v := seedBase(88)
	v[89] = 0.15
	return v
}

// seedSpeakerIdentity populates the People directory and the per-meeting
// speaker→person mappings for the fixture meetings. Idempotent: it deletes any
// previously-seeded profiles + fixture mappings first. Safely no-ops when the
// speaker stores aren't wired.
func (s *Service) seedSpeakerIdentity(ctx context.Context) (int, error) {
	if s.speakerProfiles == nil || s.meetingMappings == nil {
		return 0, nil
	}
	bySpeaker := make(map[string]seedPerson, len(seedPeople))
	for _, p := range seedPeople {
		bySpeaker[p.speakerID] = p
		_ = s.speakerProfiles.Delete(ctx, seedProfileID(p.slot)) // idempotent re-seed
	}
	for _, perMeeting := range seedAttention {
		for sid, kind := range perMeeting {
			if kind == attnNew {
				_ = s.speakerProfiles.Delete(ctx, seedProvisionalID(sid)) // idempotent re-seed
			}
		}
	}
	for _, fx := range seedFixtures {
		_ = s.meetingMappings.DeleteByMeeting(ctx, fx.ID)
	}

	now := time.Now()
	profileCreatedAt := seedAnchor.Add(-30 * 24 * time.Hour)
	const seedConfidence = 0.91

	// First pass: write mappings, tracking when each person was last seen and
	// which provisional ("new") profiles to mint.
	lastSeen := map[string]time.Time{}
	type provisional struct {
		label  string
		seenAt time.Time
	}
	provisionals := map[string]provisional{} // profile id → stub details

	for _, fx := range seedFixtures {
		overrides := seedAttention[fx.ID]
		for i, sp := range fx.Speakers {
			person, hasProfile := bySpeaker[sp.ID]

			// Default: a known person auto-links; an unknown voice is not set.
			status := "unmatched"
			var profileID *string
			emb := seedUnknownSample()
			if hasProfile {
				status = "auto"
				pid := seedProfileID(person.slot)
				profileID = &pid
				emb = person.seedSample()
			}

			if kind, ok := overrides[sp.ID]; ok {
				switch kind {
				case attnLikely:
					// Likely: keep the real voiceprint so the matcher ranks
					// candidates, but leave it unconfirmed (no link).
					status, profileID = "pending", nil
					if hasProfile {
						emb = person.seedSample()
					}
				case attnNotSet:
					// Not set: the voice matched no one in this meeting.
					status, profileID = "unmatched", nil
					emb = seedUnknownSample()
				case attnNew:
					// New: an auto-minted provisional profile, named only by its
					// diarization label. The stub carries no voiceprint, so it
					// never suggests itself back as a candidate.
					pid := seedProvisionalID(sp.ID)
					status, profileID = "new", &pid
					emb = seedUnknownSample()
					// A "new" mint is named only by its diarization label (here the
					// anonymous "Speaker C"), exactly as the live pipeline names it
					// from sp.DisplayName — so it reads as "there but not named yet".
					provisionals[pid] = provisional{label: seedSpeakerLabel(i), seenAt: fx.CreatedAt}
				}
			}

			m := speakerstore.MeetingSpeakerMapping{
				MeetingID:        fx.ID,
				MeetingSpeakerID: sp.ID,
				ProviderLabel:    sp.ID,
				MatchStatus:      status,
				ProfileID:        profileID,
				EmbeddingVector:  emb,
				EmbeddingDim:     len(emb),
				CreatedAt:        fx.CreatedAt,
				UpdatedAt:        now,
			}
			if status == "auto" {
				conf := seedConfidence
				m.MatchConfidence = &conf
				if fx.CreatedAt.After(lastSeen[sp.ID]) {
					lastSeen[sp.ID] = fx.CreatedAt
				}
			}
			if err := s.meetingMappings.Upsert(ctx, m); err != nil {
				return 0, err
			}
		}
	}

	// Second pass: create the profiles, stamping last-seen from the linked rows.
	for _, p := range seedPeople {
		voiceprint := p.seedVoiceprint()
		var seen *time.Time
		if ls, ok := lastSeen[p.speakerID]; ok {
			seen = &ls
		}
		if err := s.speakerProfiles.Create(ctx, speakerstore.SpeakerProfile{
			ID:              seedProfileID(p.slot),
			DisplayName:     p.name,
			Pronouns:        p.pronouns,
			Notes:           p.notes,
			Affiliations:    p.affils,
			EmbeddingVector: voiceprint,
			EmbeddingDim:    len(voiceprint),
			EmbeddingModel:  "seed",
			CreatedAt:       profileCreatedAt,
			UpdatedAt:       now,
			LastSeenAt:      seen,
		}); err != nil {
			return 0, err
		}
	}

	// Third pass: mint the provisional ("new") profiles. They're in the
	// directory but barely configured — a name from diarization, no voiceprint,
	// pronouns, or notes — so they exercise the "name / merge this auto-created
	// person" flow and read as "there but not configured yet".
	for pid, pv := range provisionals {
		seen := pv.seenAt
		if err := s.speakerProfiles.Create(ctx, speakerstore.SpeakerProfile{
			ID:             pid,
			DisplayName:    pv.label,
			EmbeddingModel: "seed",
			CreatedAt:      pv.seenAt,
			UpdatedAt:      now,
			LastSeenAt:     &seen,
		}); err != nil {
			return 0, err
		}
	}

	return len(seedPeople) + len(provisionals), nil
}
