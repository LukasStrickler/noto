package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/core/entityrepair"
	"github.com/lukasstrickler/noto/internal/core/speakers"
	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/providers"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
	"github.com/lukasstrickler/noto/internal/platform/providers/llm"
	"github.com/lukasstrickler/noto/internal/platform/providers/merge"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
	"github.com/lukasstrickler/noto/internal/platform/repo"
	"github.com/lukasstrickler/noto/internal/platform/search"
	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
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
	// Diarization consumes only the raw audio and is the LONGER local stage, so
	// it runs concurrently with STT; the merge below is the first point that
	// needs both. The buffered channel doubles as the join: nil = no diarizer.
	type diarResult struct {
		turns []diarize.Turn
		err   error
	}
	var diarCh chan diarResult
	if audioPath != "" {
		s.publishProgress(job, "transcribing", 0.3, "")
		// Load the audio bytes whenever the file exists — the local voice
		// profiler needs them even on the no-STT-key path (it embeds speakers
		// independently of whoever produced the diarization).
		if data, rerr := os.ReadFile(audioPath); rerr == nil {
			audio = data
		}
		if len(audio) > 0 {
			if d := s.resolveDiarizer(ctx); d != nil {
				diarCh = make(chan diarResult, 1)
				s.publishProgress(job, "diarizing", 0.35, d.ProviderID())
				go func() {
					turns, derr := d.Diarize(ctx, audio, diarize.DiarizeOptions{
						MeetingID:   mid.String(),
						NumSpeakers: 0,
					})
					diarCh <- diarResult{turns: turns, err: derr}
				}()
			}
		}
		// Route to the configured speech provider — local-first by default
		// (parakeet-local), with cloud (AssemblyAI) as an optional offload.
		// Any failure here (model not installed, engine not built, missing
		// cloud key, network error) is NON-fatal: we note it and fall through
		// to the synthesized transcript so the job still completes and the
		// reason is visible, rather than producing a silent wrong transcript.
		if len(audio) > 0 {
			provider := strings.TrimSpace(s.currentCfg().Routing.SpeechProvider)
			if provider == "" {
				provider = config.DefaultSTTProvider
			}
			// The context-bias glossary (participant names + title terms) is
			// identical for both consumers in this job; compute it once in the
			// adapter-resolved branch and feed both the recognizer and the
			// entity-repair pass below — one DB read, and the two stages can't drift
			// onto different glossaries. Declared out here so it's in scope for the
			// repair pass, which lives in the outer transcript block.
			var biasTerms []string
			adapter, aerr := s.resolveSTTAdapter(ctx, provider)
			if aerr != nil {
				s.publishProgress(job, "stt provider unavailable", 0.5, aerr.Error())
			} else {
				biasTerms = s.contextBiasTerms(ctx, title)
				t, terr := adapter.Transcribe(ctx, audio, stt.TranscribeOptions{
					Language:    jobOptString(job.Options, "language", ""),
					MeetingID:   mid.String(),
					ContextBias: biasTerms,
				})
				if terr != nil {
					s.publishProgress(job, "transcription unavailable", 0.5, terr.Error())
				} else {
					transcript = t
				}
			}
			// Clean the raw provider output (merge diarization gaps, fix
			// overlapping timestamps, canonicalize speaker labels, …) before
			// anything downstream consumes it. MeetingID is set first so the
			// normalized result can be validated. If normalization produces
			// something the storage layer can't read back, keep the raw
			// transcript and surface a note rather than failing the job.
			if transcript != nil {
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
				// Entity repair: snap near-miss words to the meeting's known
				// vocabulary (participant names, product/jargon terms) — the same
				// glossary the recognizer never sees. Conservative (only
				// close-but-not-exact matches change) and a no-op without a glossary,
				// so it only ever sharpens the product-critical "who/what" accuracy.
				if reps := providers.RepairTranscriptEntities(transcript, biasTerms, entityrepair.DefaultOptions()); len(reps) > 0 {
					s.publishProgress(job, "entity repair", 0.55, fmt.Sprintf("%d term(s) corrected", len(reps)))
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
				{ID: "spk_0", DisplayName: "You", Origin: "local_speaker", Label: "me", ProviderLabel: "A"},
				{ID: "spk_1", DisplayName: "Participants", Origin: "participants", Label: "participants", ProviderLabel: "B"},
			},
			Segments: synthesizeSegments(title),
		}
	} else {
		transcript.MeetingID = mid.String()
	}
	// Join the concurrent diarization (started alongside STT above). Always
	// wait so the engine subprocess isn't still running when the job ends.
	// Attribution stays best-effort and still requires word-level timing: a
	// diarizer failure leaves the STT speakers in place and surfaces a note.
	if diarCh != nil {
		res := <-diarCh
		if transcript != nil && len(transcript.Words) > 0 {
			if res.err != nil {
				s.publishProgress(job, "diarization skipped", 0.75, res.err.Error())
			} else if attributed := merge.Attribute(transcript, res.turns); attributed != nil {
				attributed.MeetingID = mid.String()
				transcript = attributed
			}
		}
	}
	embeddings := map[string][]float64(nil)
	if emb := s.activeSpeakerEmbedder(); emb != nil && len(audio) > 0 {
		if generated, err := emb.EmbedSpeakers(ctx, audio, transcript); err == nil {
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

// contextBiasTerms collects domain terms that improve transcription accuracy on
// proper nouns: the known real speaker names from the profile library plus the
// meeting title. Generic placeholder names ("spk_…", "Speaker …", "Participants")
// are skipped — they bias nothing useful. Best-effort: any error yields no bias.
func (s *Service) contextBiasTerms(ctx context.Context, title string) []string {
	var names []string
	if s.speakerProfiles != nil {
		if profiles, err := s.speakerProfiles.List(ctx); err == nil {
			for _, p := range profiles {
				name := strings.TrimSpace(p.DisplayName)
				if name == "" || name == "Participants" ||
					strings.HasPrefix(name, "spk_") || strings.HasPrefix(name, "Speaker ") {
					continue
				}
				names = append(names, name)
			}
		}
	}
	return biasTermsFromNames(title, names)
}

// biasTermsFromNames builds the dedup'd context-bias term list from the meeting title and
// the known speaker display names. Each multi-word name contributes BOTH its full form
// ("Lukas Strickler") AND its individual parts ("Lukas", "Strickler"): participant first
// and last names are spoken standalone far more often than in full, and those standalone
// mentions are the product-critical "who". Short parts (<4 chars) are dropped — they'd
// match too loosely. The title is added whole, never split (its words aren't entities).
func biasTermsFromNames(title string, displayNames []string) []string {
	seen := map[string]bool{}
	var terms []string
	add := func(t string) {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		terms = append(terms, t)
	}
	if title != "" && title != "Untitled meeting" {
		add(title)
	}
	for _, name := range displayNames {
		add(name)
		if parts := strings.Fields(name); len(parts) > 1 {
			for _, p := range parts {
				if len([]rune(p)) >= 4 {
					add(p)
				}
			}
		}
	}
	return terms
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
	summary := synthesizeSummary(title, mid.String())
	md := renderSummaryMD(title, summary, transcript)
	if err := s.repo.SaveSummary(ctx, mid, md, summary); err != nil {
		return err
	}
	s.publishProgress(job, "summary written", 1.0, "placeholder (no LLM key)")
	return nil
}

// synthesizeSummary returns a clearly-labelled placeholder used when no LLM key
// is configured. It deliberately fabricates NO decisions/actions/risks: passing
// invented insights off as real would be worse than an empty summary, so the
// placeholder states plainly that it is one and that a key is needed.
func synthesizeSummary(title, meetingID string) *artifacts.Summary {
	return &artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:     meetingID,
		ShortSummary:  fmt.Sprintf("No LLM provider key is configured, so no summary was generated for %q. Add an OpenRouter API key in settings to produce a real, evidence-grounded summary.", title),
		Model:         artifacts.SummaryModel{Provider: "synthetic", ModelID: "placeholder", PromptVersion: "none"},
		Coverage:      &artifacts.SummaryCoverage{},
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
	s.publishProgress(job, "calling LLM", 0.6, providerID+" · "+cfg.Routing.LLMModel)
	adapter := &llm.OpenRouterAdapter{
		APIKey:             key,
		ModelID:            cfg.Routing.LLMModel,
		ZDR:                cfg.Routing.LLMPrivacy.ZDR,
		DenyDataCollection: cfg.Routing.LLMPrivacy.DenyDataCollection,
		RequireParameters:  cfg.Routing.LLMPrivacy.RequireParameters,
	}
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

// speakerLabelsByID maps each speaker ID to the most human-readable label
// available: the resolved display name, else the normalized "Speaker N" label,
// else the raw ID. Used to index searchable, readable speaker names instead of
// internal IDs.
func speakerLabelsByID(speakers []artifacts.Speaker) map[string]string {
	out := make(map[string]string, len(speakers))
	for _, sp := range speakers {
		label := sp.DisplayName
		if label == "" {
			label = sp.Label
		}
		if label == "" {
			label = sp.ID
		}
		out[sp.ID] = label
	}
	return out
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

	// Index each segment's speaker by its HUMAN label (resolved name, else the
	// normalized "Speaker N" label, else the raw id) instead of the opaque
	// "spk_0" — so search results read "Maya" and a query for a person's name
	// matches their segments.
	speakerLabel := speakerLabelsByID(t.Speakers)
	segs := make([]search.TranscriptSegment, 0, len(t.Segments))
	for _, seg := range t.Segments {
		label := speakerLabel[seg.SpeakerID]
		if label == "" {
			label = seg.SpeakerID
		}
		segs = append(segs, search.TranscriptSegment{
			SegmentID: seg.ID,
			Text:      seg.Text,
			Speaker:   label,
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

// embedderModelID reports the active embedding model so profiles are tagged with the
// space they live in. Defaults to "ecapa" (the shipped local provider).
func (s *Service) embedderModelID() string {
	if m, ok := s.activeSpeakerEmbedder().(interface{ ModelID() string }); ok {
		if id := m.ModelID(); id != "" {
			return id
		}
	}
	return "ecapa"
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
	// All speaker embeddings in a meeting come from the same embedder, so they
	// share one dimension; only profiles embedded at that SAME dimension are
	// comparable. A profile from a different/older embedder model (different dim) is
	// skipped here rather than fed to the matcher — which fails the WHOLE step on
	// the first dimension mismatch (its strict contract), so a single stale-dim
	// profile would otherwise poison speaker matching for every meeting the moment
	// the embedder model ever changes.
	queryDim := 0
	for _, emb := range embeddings {
		if len(emb) > 0 {
			queryDim = len(emb)
			break
		}
	}
	var candidates []speakers.Candidate
	for _, p := range existing {
		if queryDim > 0 && len(p.EmbeddingVector) == queryDim {
			candidates = append(candidates, speakers.Candidate{
				ProfileID: p.ID,
				Name:      p.DisplayName,
				Centroid:  p.EmbeddingVector,
			})
		}
	}
	// Total speech per transcript speaker, so we can withhold auto-confirmation
	// when a match is built from too little voice (see speakers.MinEnrollSpeech).
	speechBySpeaker := make(map[string]float64, len(tr.Speakers))
	for _, seg := range tr.Segments {
		if d := seg.EndSeconds - seg.StartSeconds; d > 0 {
			speechBySpeaker[seg.SpeakerID] += d
		}
	}
	// Preserve human work: a speaker the user MANUALLY assigned must survive a
	// re-transcribe / re-process (reachable via the CreateJob API). Re-matching
	// would otherwise upsert a fresh auto-guess over the same (meeting, speaker)
	// key and silently destroy the strongest identity signal there is. Auto /
	// pending / new mappings are safe to recompute; only "manual" is sacred.
	manualSpeaker := map[string]bool{}
	if maps, merr := s.meetingMappings.ListByMeeting(ctx, meetingID); merr == nil {
		for _, m := range maps {
			if m.MatchStatus == "manual" {
				manualSpeaker[m.MeetingSpeakerID] = true
			}
		}
	}
	now := time.Now()
	for _, sp := range tr.Speakers {
		if manualSpeaker[sp.ID] {
			continue // keep the user's manual assignment as-is
		}
		emb, ok := embeddings[sp.ProviderLabel]
		if !ok || len(emb) == 0 {
			_ = s.meetingMappings.Upsert(ctx, speakerstore.MeetingSpeakerMapping{
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
			// MatchConfident adds a top-1-vs-top-2 margin gate so a close call
			// between two stored profiles (the same-gender confusion seen in the
			// AMI bench) lands in pending instead of auto-merging.
			decision, err = speakers.MatchConfident(emb, candidates)
			if err != nil {
				return err
			}
			// Min-enrollment-speech gate: an auto-confirm built from very little
			// voice is untrustworthy, so downgrade it to pending for review.
			if decision.Status == speakers.StatusAuto && speechBySpeaker[sp.ID] < speakers.MinEnrollSpeech {
				decision.Status = speakers.StatusPending
				decision.Reason = "insufficient enrollment speech"
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
		if err := s.meetingMappings.Upsert(ctx, speakerstore.MeetingSpeakerMapping{
			MeetingID:        meetingID,
			MeetingSpeakerID: sp.ID,
			ProviderLabel:    sp.ProviderLabel,
			ProfileID:        profileID,
			MatchConfidence:  confidence,
			MatchStatus:      status,
			// Persist this meeting-speaker's voiceprint so the UI can (re-)rank
			// suggestions against the profile library without re-decoding audio.
			EmbeddingVector: emb,
			EmbeddingDim:    len(emb),
			CreatedAt:       now,
			UpdatedAt:       now,
		}); err != nil {
			return err
		}
		if decision.Status == speakers.StatusNew && profileID != nil {
			p := speakerstore.SpeakerProfile{
				ID:              *profileID,
				DisplayName:     sp.DisplayName,
				EmbeddingVector: emb,
				EmbeddingDim:    len(emb),
				EmbeddingCount:  1, // first enrollment
				EmbeddingModel:  s.embedderModelID(),
				CreatedAt:       now,
				UpdatedAt:       now,
				LastSeenAt:      &now,
			}
			if err := s.speakerProfiles.Create(ctx, p); err != nil {
				return err
			}
		} else if decision.Status == speakers.StatusAuto && profileID != nil {
			// Fold this meeting's voiceprint into the matched profile as a count-
			// weighted running mean (shared with the manual-confirmation path), so a
			// single noisy enrollment can't swing an established profile halfway.
			s.foldEmbeddingIntoProfile(ctx, *profileID, emb)
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
