# Audio → Transcript → Insights Pipeline

How a recording becomes a searchable, evidence-grounded summary. This is the
detail behind the four chained jobs in `internal/app/service/jobs_pipeline.go`
(`runPipeline`): **ingest → transcribe → summarize → index**. Each phase reports
a slice of progress (0.10 / 0.55 / 0.85 / 1.0) over the IPC/SSE channel.

```
audio ─▶ ingest ─▶ transcribe ─▶ summarize ─▶ index
                     │              │            │
                 AssemblyAI     OpenRouter    SQLite FTS5
                 + normalize    2-pass LLM    (search)
                 + speaker ID   + verify
```

Provider adapters are swappable; the **artifact schemas are the contract**
(`internal/core/artifacts`, see [artifacts.md](./artifacts.md)). Without API keys
the pipeline still completes end-to-end using clearly-labelled placeholders.

---

## 1. Ingest — `runIngest`

Idempotently creates/repairs the meeting manifest (`repo.CreateMeeting`). A no-op
if `ImportAudio` already wrote it. Cheap; exists so the meeting is visible in the
TUI before the slow stages run.

## 2. Transcribe — `runTranscribe`

The seam between raw audio and the normalized `transcript.v1` artifact.

1. **Resolve audio** — explicit `output_path` from the job, else `repo.AudioPath`.
   The bytes are loaded whenever the file exists (the local voice profiler needs
   them even on the no-STT-key path).
2. **STT (if `provider:assemblyai` key set)** — `AssemblyAIAdapter.Transcribe`
   (`providers/stt/assemblyai.go`): upload → submit → poll → parse.
   - submit options: `speech_models: [universal-3-pro, universal-2]`,
     `speaker_labels: true`, `language_detection` when no language is given, and
     **`keyterms_prompt`** built from `contextBiasTerms` (meeting title + known
     real speaker names from the profile library) to improve proper-noun accuracy.
   - poll: 3 s interval, 120 attempts (~6 min ceiling); cancellable.
3. **Normalize** — `providers.NormalizeTranscript`. The default chain
   (`speech.NewTranscriptNormalizers`) is deliberately **minimal**: merge
   adjacent same-speaker turns + canonicalize the human-readable speaker label.
   It does **not** rewrite segment text — AssemblyAI already returns punctuated,
   formatted text, and the marker-injecting normalizers (gap flags, `[low
   confidence]` tags, filler/partial-word edits) used to corrupt content and leak
   into the LLM prompt and search index. They remain available as standalone
   components but are off the default path. On normalization/validation failure
   the **raw** transcript is kept and a progress note is surfaced.
4. **Speaker identity (best-effort)** — `activeSpeakerEmbedder.EmbedSpeakers`
   then `matchSpeakers` against the cross-meeting profile library. Failures never
   fail the job (see [speaker-identity.md](./speaker-identity.md)).
5. **Persist** — `repo.SaveTranscript`.

**No key / no audio:** a deterministic placeholder transcript is synthesized so
the rest of the pipeline is exercisable in dev.

## 3. Summarize — `runSummarize` → `summarizeWithProvider`

Loads the transcript and runs the **two-pass, evidence-grounded** pipeline in
`OpenRouterAdapter.Summarize` (`providers/llm/openrouter.go`). This is the core
quality work.

```
transcript
  │  (system: @S1 + grounding rules + few-shot;  user: token-attributed transcript)
  ▼
① EXTRACT ──▶ ② VERIFY ──▶ ③ REFINE + GAP ──▶ ④ VERIFY ──▶ summary.v1
  structured    quote        gap analysis,       re-score,
  JSON call     grounding    repair unverified,  attach
  (no LLM)      prune dupes  coverage
```

### ① Extract
One structured-output call over the **whole** transcript (no segment cap).
- **System prompt** (`prompts.fullSummaryPromptTemplate`): defines the strict
  JSON shape, the `@S1` speaker-token rule, the verbatim-quote/anti-hallucination
  rules, and few-shot examples.
- **User message** (`PromptBuilder.Build`): the transcript with every speaker
  attributed by a fixed token `@S1`, `@S2`, … — **real names never leave the
  device as labels**; the TUI swaps tokens back to people + identity colours at
  render time (`detail_pane_people.renderPeople`).
- **Request** (`chat`): `temperature 0.1`, `max_tokens 8000`,
  `response_format` (json_schema when privacy guards are on, else json_object),
  the privacy `provider` routing block, retries on 429/502/503/504 with backoff.
- **Parse** (`parseSummaryContent`): strips ``` code fences, salvages the outer
  `{…}`, and on non-JSON degrades to using the raw text as `short_summary` (never
  loses the whole job).

### ② Verify (deterministic, free)
`artifacts.VerifyAndScore` checks each evidence `quote` against the **cited
segment's text** (case/punctuation-tolerant match) and `sanitizeEvidence` drops
citations to non-existent segments. This catches hallucinated quotes that
`ValidateSummary` (which only checks the segment_id exists) cannot.

### ③ Refine + gap analysis
A second call (`prompts.refineSummaryPromptTemplate`) gets the transcript, the
draft as JSON, and an explicit **list of unverified items**. It (a) adds
supported items the first pass missed, (b) replaces or removes ungrounded quotes,
(c) prunes duplicates. This is the single biggest quality lever beyond extraction
and is cheap. Best-effort: a failure here keeps the draft.

### ④ Verify + select
Re-run `VerifyAndScore`. The refined pass is adopted **unless** it regressed the
count of grounded items. Final `ValidateSummary`, then `SaveSummary` (markdown +
`summary.json`).

**Correctness insights** land on the artifact:
- per-item `confidence` (share of evidence quotes verified, weighted by segment
  confidence), and
- `coverage` (`items_total`, `items_grounded`, `grounding_score`).

**No key / LLM error:** `synthesizeSummary` writes an honest, clearly-labelled
placeholder (`model.provider = "synthetic"`) with **no fabricated** decisions or
actions — passing invented insights off as real is worse than an empty summary.

## 4. Index — `runIndex`

Reads transcript + summary and upserts a SQLite FTS5 row (`indexOneMeeting`).
Whole-library reindex optimizes once after the batch.

---

## Privacy posture

Transcripts are sent to OpenRouter under an explicit `provider` routing block
built from `routing.llm_privacy` (all **ON by default**):

| Guard | OpenRouter field | Effect |
| --- | --- | --- |
| `zdr` | `provider.zdr: true` | Route only to Zero-Data-Retention endpoints |
| `deny_data_collection` | `provider.data_collection: "deny"` | No providers that train on data |
| `require_parameters` | `provider.require_parameters: true` | Only providers that honor `response_format` |

Users can relax these (wider model pool, lower cost/latency) via
`ConfigPatch.Privacy` / config. **Note:** ZDR governs *retention*, not
*transmission* — the transcript text (including any names spoken aloud) is still
sent to the provider. The `@S1` tokenization only anonymizes speaker *labels*.

## Model selection

Default `routing.llm_model` is **`google/gemini-3.1-flash-preview`** — cheap
input, ~1M context (whole transcript in one call), strong structured output.
Users may enter **any OpenRouter slug**; it is validated at call time. For
maximum faithfulness on evidence grounding, a low-hallucination model (e.g.
Claude Sonnet) is the premium choice. See [benchmarks.md](./benchmarks.md) for
the selection rationale.

## Configuration reference

| Key | Default | Meaning |
| --- | --- | --- |
| `routing.llm_model` | `google/gemini-3.1-flash-preview` | OpenRouter model slug (free text) |
| `routing.llm_privacy.zdr` | `true` | Zero-data-retention routing |
| `routing.llm_privacy.deny_data_collection` | `true` | No-training routing |
| `routing.llm_privacy.require_parameters` | `true` | Structured-output-capable routing |
| `routing.speech_provider` | `assemblyai` | STT provider |

Prompt revision is tracked on each summary as `model.prompt_version`
(`summary.v2` for the @S1 + two-pass pipeline); the artifact `schema_version`
remains `summary.v1` (the additions — `confidence`, `coverage` — are backward
compatible).

---

## Gaps & risks

Honest assessment of where this pipeline is thin. Roughly ordered by impact.

### Accuracy / quality
1. **No summary-quality eval.** `benchmark/identity` measures voiceprint matching
   only. There is no gold set scoring decision/action recall or quote
   faithfulness, so model, prompt, and refine-heuristic choices are
   **unmeasured** — regressions are invisible. *This is the highest-leverage
   gap to close.*
2. **Diarization is the upstream bottleneck.** Speaker-confusion from AssemblyAI
   poisons both cross-meeting identity (documented in
   [benchmarks.md](./benchmarks.md)) **and** `@S` attribution in summaries
   (wrong owner on an action item). The summary layer can't detect this.
3. **Quote verification is fuzzy substring matching.** `normalizeForMatch`
   collapses punctuation/case, so a very short quote can false-*positive*
   (inflated confidence) and a lightly-paraphrased quote can false-*negative*. It
   grounds; it does not guarantee.
4. **Output language is unconstrained.** The prompt never tells the model to
   summarize in the transcript's language — a non-English meeting may be
   summarized in English.
5. **Refine-adoption is a heuristic** ("grounded count didn't regress"), not a
   measured win. It can keep a worse-written draft or adopt a refined pass that
   padded low-value items.

### Robustness
6. **No chunking; single 1 MB request cap.** A multi-hour transcript can exceed
   the request-body guard in `chat` → hard error. The 1M-context default covers
   realistic meetings, but there is no graceful map-reduce fallback.
7. **Fixed `max_tokens: 8000`.** A meeting with very many items can truncate the
   extract JSON mid-object → prose fallback (the refine pass may partially
   recover). No adaptive sizing.
8. **Double prose → silent quality cliff.** If *both* passes return non-JSON,
   the result is `short_summary`-only with no items and **no error** — quietly
   degraded rather than failed.
9. **No prompt caching.** The transcript is sent on both passes; OpenRouter
   prompt caching isn't used. Cost is still tiny, but it doubles tokens/latency
   unnecessarily.

### Privacy / operations
10. **ZDR routing can empty the provider pool.** With all guards on and a model
    that has no ZDR endpoint, OpenRouter may return no route → the job falls back
    to the synthetic placeholder with only a progress note. The *why* (privacy
    routing left nothing) is not clearly surfaced to the user.
11. **Transmission ≠ retention.** Full transcript content reaches the provider
    regardless of ZDR. Inherent to any cloud LLM; users with strict requirements
    have no local-LLM path today.
12. **Context biasing uses the whole profile library.** `contextBiasTerms` feeds
    *all* known speaker names as keyterms, not just this meeting's likely
    participants — on a large library this can over-bias and has practical
    keyterm limits.

### Coupling
13. **Normalizer minimalism assumes a clean STT provider.** The trimmed default
    chain relies on AssemblyAI returning punctuated, formatted text. A future STT
    provider returning raw text would need the formatting normalizers
    re-enabled — the assumption is provider-coupled and implicit.
14. **Insights aren't surfaced in the UI yet.** `confidence` / `coverage` are
    computed and stored but the TUI does not yet render them, so the
    "correctness insight" isn't visible to users.
