package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/data"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/providers"
	"github.com/lukasstrickler/noto/internal/providers/llm"
	"github.com/lukasstrickler/noto/internal/providers/stt"
	"github.com/lukasstrickler/noto/internal/repo"
	"github.com/lukasstrickler/noto/internal/search"
	"github.com/lukasstrickler/noto/internal/speakers"
)

// --- job implementations ---
// These are the seam between the queue and the provider/storage code.
// V1 implementations are deliberately simple — make the pipeline visible
// in the TUI; provider-specific calls slot in as they get wired.

func (s *Service) runIngest(ctx context.Context, job *notoapi.Job) error {
	mid, err := parseMeetingID("ingest", job.MeetingID)
	if err != nil {
		return err
	}
	s.publishProgress(job, "preparing layout", 0.2, "")
	title := jobOptString(job.Options, "title", "Untitled meeting")
	// CreateMeeting is idempotent: if ImportAudio already wrote the
	// manifest, this is a no-op (GetMeeting will succeed for this id).
	if existing, err := s.repo.GetMeeting(ctx, mid); err != nil || existing == nil {
		if err := s.repo.CreateMeeting(ctx, mid, repo.CreateMeetingOpts{
			Title:  title,
			Reason: string(artifacts.ReasonAudioImported),
		}); err != nil {
			return err
		}
	}
	s.publishProgress(job, "ingest complete", 1.0, "")
	return sleep(ctx, 100*time.Millisecond)
}

func (s *Service) runTranscribe(ctx context.Context, job *notoapi.Job) error {
	mid, err := parseMeetingID("transcribe", job.MeetingID)
	if err != nil {
		return err
	}

	title := jobOptString(job.Options, "title", "Untitled meeting")
	// Job options carry the explicit path (from record/import); fall back
	// to whatever the repo knows about for this meeting.
	audioPath := jobOptString(job.Options, "output_path", "")
	if audioPath == "" {
		audioPath, _ = s.repo.AudioPath(mid)
	}
	if _, err := os.Stat(audioPath); err != nil {
		audioPath = ""
	}

	var transcript *artifacts.Transcript
	var audio []byte
	if audioPath != "" {
		s.publishProgress(job, "transcribing", 0.3, "")
		key, _ := s.secrets.Get(ctx, "provider:assemblyai")
		if strings.TrimSpace(key) != "" {
			var rerr error
			audio, rerr = os.ReadFile(audioPath)
			if rerr != nil {
				return fmt.Errorf("read audio: %w", rerr)
			}
			adapter, err := s.newSTTAdapter("assemblyai")
			if err != nil {
				return fmt.Errorf("get assemblyai adapter: %w", err)
			}
			transcript, err = adapter.Transcribe(ctx, audio, stt.TranscribeOptions{
				Language:  jobOptString(job.Options, "language", ""),
				MeetingID: mid.String(),
			})
			if err != nil {
				return fmt.Errorf("transcribe: %w", err)
			}
			// Clean the raw provider output (merge diarization gaps, fix
			// overlapping timestamps, canonicalize speaker labels, …) before
			// anything downstream consumes it. MeetingID is set first so the
			// normalized result can be validated. If normalization produces
			// something the storage layer can't read back, keep the raw
			// transcript and surface a note rather than failing the job.
			transcript.MeetingID = mid.String()
			if normalized, nerr := providers.NormalizeTranscript(transcript); nerr != nil {
				s.publishProgress(job, "normalization skipped", 0.5, nerr.Error())
			} else if normalized != nil {
				if verr := artifacts.ValidateTranscript(*normalized); verr != nil {
					s.publishProgress(job, "normalization skipped", 0.5, verr.Error())
				} else {
					transcript = normalized
				}
			}
		}
	}
	if transcript == nil {
		s.publishProgress(job, "synthesizing transcript", 0.6, "no audio or no provider key")
		transcript = &artifacts.Transcript{
			SchemaVersion: "transcript.v1",
			MeetingID:     mid.String(),
			Provider: artifacts.TranscriptProvider{
				ID:    s.currentCfg().Routing.SpeechProvider,
				JobID: job.ID,
			},
			Speakers: []artifacts.Speaker{
				{ID: "spk_0", DisplayName: "You", Origin: "local_speaker", Label: "me"},
				{ID: "spk_1", DisplayName: "Participants", Origin: "participants", Label: "participants"},
			},
			Segments: synthesizeSegments(title),
		}
	} else {
		transcript.MeetingID = mid.String()
	}
	embeddings := map[string][]float64(nil)
	if s.speakerEmbedder != nil && len(audio) > 0 {
		if generated, err := s.speakerEmbedder.EmbedSpeakers(ctx, audio, transcript); err == nil {
			embeddings = generated
		}
	}
	// Speaker matching is best-effort: a failure here must not fail the
	// transcribe job, but surface it as a progress note rather than drop it.
	if err := s.matchSpeakers(ctx, mid.String(), transcript, embeddings); err != nil {
		s.publishProgress(job, "speaker matching skipped", 0.9, err.Error())
	}
	if err := s.repo.SaveTranscript(ctx, mid, transcript); err != nil {
		return err
	}
	s.publishProgress(job, "transcript written", 1.0, fmt.Sprintf("%d segments", len(transcript.Segments)))
	return nil
}

func synthesizeSegments(title string) []artifacts.Segment {
	now := 0.0
	confidence := 0.92
	mk := func(speaker, role, text string, dur float64) artifacts.Segment {
		seg := artifacts.Segment{
			ID:           fmt.Sprintf("seg_%06d", int(now*10)),
			SpeakerID:    speaker,
			SourceRole:   role,
			StartSeconds: now,
			EndSeconds:   now + dur,
			Text:         text,
			Confidence:   &confidence,
		}
		now += dur
		return seg
	}
	return []artifacts.Segment{
		mk("spk_0", "local_speaker", "Let me start with the agenda for "+title+".", 3),
		mk("spk_1", "participants", "Sounds good. I'd love to discuss the timeline first.", 4),
		mk("spk_0", "local_speaker", "Right — we need to ship the v1 by end of quarter.", 3),
		mk("spk_1", "participants", "What about the open-questions list? Should we close them today?", 5),
		mk("spk_0", "local_speaker", "Let's decide on the top three and defer the rest.", 4),
	}
}

func (s *Service) runSummarize(ctx context.Context, job *notoapi.Job) error {
	mid, err := parseMeetingID("summarize", job.MeetingID)
	if err != nil {
		return err
	}
	s.publishProgress(job, "loading transcript", 0.2, "")
	transcript, err := s.repo.LoadTranscript(ctx, mid)
	if err != nil {
		return err
	}
	title := jobOptString(job.Options, "title", "Untitled meeting")

	if real := s.summarizeWithProvider(ctx, job, *transcript, mid); real != nil {
		md := renderSummaryMD(title, real, transcript)
		if err := s.repo.SaveSummary(ctx, mid, md, real); err != nil {
			return err
		}
		s.publishProgress(job, "summary written", 1.0, "real LLM")
		return nil
	}

	s.publishProgress(job, "synthesizing summary", 0.6, "no LLM key configured")
	cfg := s.currentCfg()
	summary := synthesizeSummary(title, mid.String(), cfg.Routing.LLMProvider, cfg.Routing.LLMModel)
	md := renderSummaryMD(title, summary, transcript)
	if err := s.repo.SaveSummary(ctx, mid, md, summary); err != nil {
		return err
	}
	s.publishProgress(job, "summary written", 1.0, "")
	return nil
}

// synthesizeSummary returns a deterministic placeholder summary used when no
// LLM key is configured.
func synthesizeSummary(title, meetingID, llmProvider, llmModel string) *artifacts.Summary {
	return &artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:     meetingID,
		ShortSummary:  fmt.Sprintf("Meeting %q reached three decisions and one action item.", title),
		Decisions: []artifacts.SummaryItem{
			{Text: "Ship v1 by end of quarter", SpeakerIDs: []string{"spk_0"}, Evidence: []artifacts.Evidence{{SegmentID: "seg_000070", Quote: "ship the v1 by end of quarter"}}},
			{Text: "Pick top three open questions; defer the rest", SpeakerIDs: []string{"spk_0", "spk_1"}, Evidence: []artifacts.Evidence{{SegmentID: "seg_000150", Quote: "decide on the top three and defer the rest"}}},
		},
		ActionItems: []artifacts.ActionItem{
			{Text: "Circulate the timeline next week", Owner: "spk_0", Evidence: []artifacts.Evidence{{SegmentID: "seg_000030", Quote: "timeline first"}}},
		},
		Risks: []artifacts.SummaryItem{
			{Text: "Timeline may be aggressive for v1 scope.", Evidence: []artifacts.Evidence{{SegmentID: "seg_000070", Quote: "ship the v1 by end of quarter"}}},
		},
		OpenQuestions: []artifacts.SummaryItem{
			{Text: "Which open questions are highest priority?", Evidence: []artifacts.Evidence{{SegmentID: "seg_000100", Quote: "open-questions list"}}},
		},
		Model: artifacts.SummaryModel{Provider: llmProvider, ModelID: llmModel, PromptVersion: "summary.v1"},
	}
}

// summarizeWithProvider returns nil when no provider key is configured;
// otherwise it returns the LLM-generated summary or nil on adapter
// error (the job then falls through to the deterministic synthesis so
// users always see *some* output).
func (s *Service) summarizeWithProvider(ctx context.Context, job *notoapi.Job, transcript artifacts.Transcript, mid uuid.UUID) *artifacts.Summary {
	cfg := s.currentCfg()
	providerID := cfg.Routing.LLMProvider
	if providerID == "" {
		providerID = "openrouter"
	}
	suite, ok := s.registry.Get(providerID)
	if !ok || suite.CredentialRef == "" {
		return nil
	}
	key, err := s.secrets.Get(ctx, suite.CredentialRef)
	if err != nil || strings.TrimSpace(key) == "" {
		return nil
	}
	s.publishProgress(job, "calling LLM", 0.6, providerID)
	adapter := &llm.OpenRouterAdapter{APIKey: key, ModelID: cfg.Routing.LLMModel}
	summary, err := adapter.Summarize(ctx, transcript, llm.SummarizeOptions{MeetingID: mid.String()})
	if err != nil {
		s.publishProgress(job, "llm error, falling back", 0.7, err.Error())
		return nil
	}
	if summary == nil {
		return nil
	}
	summary.MeetingID = mid.String()
	return summary
}

func renderSummaryMD(title string, s *artifacts.Summary, _ *artifacts.Transcript) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintln(&b, s.ShortSummary)
	if len(s.Decisions) > 0 {
		b.WriteString("\n## Decisions\n\n")
		for i, d := range s.Decisions {
			fmt.Fprintf(&b, "%d. %s\n", i+1, d.Text)
		}
	}
	if len(s.ActionItems) > 0 {
		b.WriteString("\n## Action items\n\n")
		for _, a := range s.ActionItems {
			fmt.Fprintf(&b, "- %s", a.Text)
			if a.Owner != "" {
				fmt.Fprintf(&b, " _(owner: %s)_", a.Owner)
			}
			b.WriteString("\n")
		}
	}
	if len(s.Risks) > 0 {
		b.WriteString("\n## Risks\n\n")
		for _, r := range s.Risks {
			fmt.Fprintf(&b, "- %s\n", r.Text)
		}
	}
	if len(s.OpenQuestions) > 0 {
		b.WriteString("\n## Open questions\n\n")
		for _, q := range s.OpenQuestions {
			fmt.Fprintf(&b, "- %s\n", q.Text)
		}
	}
	return b.String()
}

func (s *Service) runIndex(ctx context.Context, job *notoapi.Job) error {
	s.publishProgress(job, "rebuilding index", 0.1, "")
	if job.MeetingID == "" {
		// Reindex all meetings.
		meetings, err := s.repo.ListMeetings(ctx)
		if err != nil {
			return err
		}
		for i, m := range meetings {
			if err := ctx.Err(); err != nil {
				return err
			}
			progress := 0.1 + float64(i)/float64(len(meetings)+1)*0.9
			s.publishProgress(job, "indexing", progress, m.Title)
			if err := s.indexOneMeeting(ctx, m.ID, jobOptString(job.Options, "title", m.Title)); err != nil {
				s.publishProgress(job, "index error (skipped)", progress, err.Error())
			}
		}
		// Optimize once after the whole batch rather than per meeting.
		if err := s.search.Optimize(); err != nil {
			s.publishProgress(job, "optimize skipped", 1.0, err.Error())
		}
		s.publishProgress(job, "reindex complete", 1.0, fmt.Sprintf("%d meetings", len(meetings)))
		return nil
	}
	mid, err := parseMeetingID("reindex", job.MeetingID)
	if err != nil {
		return err
	}
	if err := s.indexOneMeeting(ctx, mid, jobOptString(job.Options, "title", "Untitled meeting")); err != nil {
		return err
	}
	s.publishProgress(job, "indexed", 1.0, "")
	return nil
}

// indexOneMeeting reads the meeting's transcript + summary and upserts
// the FTS entry. Returns nil silently when no transcript exists yet.
func (s *Service) indexOneMeeting(ctx context.Context, mid uuid.UUID, titleHint string) error {
	if s.search == nil {
		return nil
	}
	t, err := s.repo.LoadTranscript(ctx, mid)
	if err != nil {
		return nil // no transcript yet — skip silently
	}

	var shortSummary string
	var decisions, actions, risks, questions []string
	createdAt := time.Now().UTC()

	if _, summary, err := s.repo.LoadSummary(ctx, mid); err == nil && summary != nil {
		shortSummary = summary.ShortSummary
		for _, d := range summary.Decisions {
			decisions = append(decisions, d.Text)
		}
		for _, a := range summary.ActionItems {
			actions = append(actions, a.Text)
		}
		for _, r := range summary.Risks {
			risks = append(risks, r.Text)
		}
		for _, q := range summary.OpenQuestions {
			questions = append(questions, q.Text)
		}
	}

	// Use the repo's meeting record for the canonical creation time.
	if sm, err := s.repo.GetMeeting(ctx, mid); err == nil {
		createdAt = sm.CreatedAt
		if titleHint == "" || titleHint == "Untitled meeting" {
			titleHint = sm.Title
		}
	}

	segs := make([]search.TranscriptSegment, 0, len(t.Segments))
	for _, seg := range t.Segments {
		segs = append(segs, search.TranscriptSegment{
			SegmentID: seg.ID,
			Text:      seg.Text,
			Speaker:   seg.SpeakerID,
			Timestamp: seg.StartSeconds,
		})
	}
	toItems := func(texts []string) []search.SummaryItem {
		items := make([]search.SummaryItem, len(texts))
		for i, t := range texts {
			items[i] = search.SummaryItem{Text: t}
		}
		return items
	}
	toActionItems := func(texts []string) []search.ActionItem {
		items := make([]search.ActionItem, len(texts))
		for i, t := range texts {
			items[i] = search.ActionItem{Text: t}
		}
		return items
	}
	return s.search.IndexMeetingFromInput(&search.IndexMeetingInput{
		MeetingID:          mid.String(),
		Title:              titleHint,
		ShortSummary:       shortSummary,
		CreatedAt:          createdAt,
		TranscriptSegments: segs,
		Decisions:          toItems(decisions),
		ActionItems:        toActionItems(actions),
		Risks:              toItems(risks),
		OpenQuestions:      toItems(questions),
	})
}

func jobOptString(opts map[string]any, key, def string) string {
	if opts == nil {
		return def
	}
	if v, ok := opts[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

// parseMeetingID validates and parses a job's meeting_id. The error is
// prefixed with the job kind so a failure points at the originating job.
func parseMeetingID(kind, raw string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, fmt.Errorf("%s: meeting_id is required", kind)
	}
	mid, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s: invalid meeting id: %w", kind, err)
	}
	return mid, nil
}

func (s *Service) matchSpeakers(ctx context.Context, meetingID string, tr *artifacts.Transcript, embeddings map[string][]float64) error {
	if tr == nil || len(tr.Speakers) == 0 {
		return nil
	}
	if len(embeddings) == 0 {
		return nil
	}
	existing, err := s.speakerProfiles.List(ctx)
	if err != nil {
		return err
	}
	var candidates []speakers.Candidate
	for _, p := range existing {
		if len(p.EmbeddingVector) > 0 {
			candidates = append(candidates, speakers.Candidate{
				ProfileID: p.ID,
				Name:      p.DisplayName,
				Centroid:  p.EmbeddingVector,
			})
		}
	}
	now := time.Now()
	for _, sp := range tr.Speakers {
		emb, ok := embeddings[sp.ProviderLabel]
		if !ok || len(emb) == 0 {
			_ = s.meetingMappings.Upsert(ctx, data.MeetingSpeakerMapping{
				MeetingID:        meetingID,
				MeetingSpeakerID: sp.ID,
				ProviderLabel:    sp.ProviderLabel,
				ProfileID:        nil,
				MatchConfidence:  nil,
				MatchStatus:      "unmatched",
				CreatedAt:        now,
				UpdatedAt:        now,
			})
			continue
		}
		var decision speakers.MatchDecision
		if len(candidates) > 0 {
			decision, err = speakers.Match(emb, candidates)
			if err != nil {
				return err
			}
		} else {
			decision = speakers.MatchDecision{Status: speakers.StatusNew, Reason: "no existing profiles"}
		}
		var profileID *string
		var confidence *float64
		status := string(decision.Status)
		switch decision.Status {
		case speakers.StatusAuto, speakers.StatusPending:
			// Copy out of the loop-scoped decision so the stored pointer
			// can't be mutated by the next iteration.
			pid := decision.ProfileID
			score := decision.Score
			profileID = &pid
			confidence = &score
		case speakers.StatusNew:
			// Mint a globally-unique profile id. The transcript-local
			// sp.ID (e.g. "spk_0") is not unique across meetings and would
			// collide on the speaker_profiles primary key, so reuse here
			// silently dropped every profile after the first.
			newID := "spk_" + uuid.NewString()
			profileID = &newID
		}
		if err := s.meetingMappings.Upsert(ctx, data.MeetingSpeakerMapping{
			MeetingID:        meetingID,
			MeetingSpeakerID: sp.ID,
			ProviderLabel:    sp.ProviderLabel,
			ProfileID:        profileID,
			MatchConfidence:  confidence,
			MatchStatus:      status,
			CreatedAt:        now,
			UpdatedAt:        now,
		}); err != nil {
			return err
		}
		if decision.Status == speakers.StatusNew && profileID != nil {
			p := data.SpeakerProfile{
				ID:              *profileID,
				DisplayName:     sp.DisplayName,
				EmbeddingVector: emb,
				EmbeddingDim:    len(emb),
				EmbeddingModel:  "titanet-large",
				CreatedAt:       now,
				UpdatedAt:       now,
				LastSeenAt:      &now,
			}
			if err := s.speakerProfiles.Create(ctx, p); err != nil {
				return err
			}
		} else if decision.Status == speakers.StatusAuto && profileID != nil {
			p, err := s.speakerProfiles.Get(ctx, *profileID)
			if err == nil {
				allEmbs := []speakers.Embedding{p.EmbeddingVector, emb}
				centroid, cerr := speakers.Centroid(allEmbs)
				if cerr == nil && len(centroid) > 0 {
					p.EmbeddingVector = centroid
					p.EmbeddingDim = len(centroid)
					p.LastSeenAt = &now
					p.UpdatedAt = now
					if err := s.speakerProfiles.Update(ctx, p); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (s *Service) runVerify(ctx context.Context, job *notoapi.Job) error {
	if job.MeetingID == "" {
		// Verify all meetings.
		meetings, err := s.repo.ListMeetings(ctx)
		if err != nil {
			return err
		}
		var firstErr error
		for i, m := range meetings {
			s.publishProgress(job, "verifying", float64(i)/float64(len(meetings)+1), m.Title)
			if err := s.repo.VerifyIntegrity(ctx, m.ID); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("meeting %s: %w", m.ID, err)
			}
		}
		s.publishProgress(job, "verify complete", 1.0, fmt.Sprintf("%d meetings", len(meetings)))
		return firstErr
	}
	mid, err := parseMeetingID("verify", job.MeetingID)
	if err != nil {
		return err
	}
	s.publishProgress(job, "verifying checksums", 0.5, job.MeetingID)
	return s.repo.VerifyIntegrity(ctx, mid)
}

// runPipeline chains ingest → transcribe → summarize → index for one
// meeting. Each phase reports a portion of total progress.
func (s *Service) runPipeline(ctx context.Context, job *notoapi.Job) error {
	steps := []struct {
		name string
		fn   func(context.Context, *notoapi.Job) error
		span float64
	}{
		{"ingest", s.runIngest, 0.10},
		{"transcribe", s.runTranscribe, 0.55},
		{"summarize", s.runSummarize, 0.85},
		{"index", s.runIndex, 1.0},
	}
	for _, step := range steps {
		s.publishProgress(job, step.name, step.span-0.001, "")
		if err := step.fn(ctx, job); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
		s.publishProgress(job, step.name+" done", step.span, "")
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
