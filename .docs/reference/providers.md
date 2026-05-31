# Provider Reference

## Strategy

AssemblyAI is the production STT provider. OpenRouter provides LLM summarization.
Provider adapters can change — artifact formats cannot.

Both providers fall back to deterministic synthetic output when no API key is
configured, so the full pipeline (ingest → transcribe → summarize → index) works
in development and on Linux without any keys.

## STT Provider — AssemblyAI

| Capability | Supported |
| --- | --- |
| Transcription | Yes |
| Word timestamps | Yes |
| Speaker diarization | Yes |
| Context biasing | Yes |
| Multi-channel | Yes |

**Configure:** TUI Config screen → API Keys → `assemblyai`, or:

```bash
noto providers key-set assemblyai <your-key>
# or set env var (read-only fallback, not stored):
export NOTO_ASSEMBLYAI_KEY=<your-key>
```

When no key is set, the pipeline synthesizes a short placeholder transcript.

## LLM Provider — OpenRouter

The summarizer is an OpenRouter-compatible adapter. It accepts any model accessible
via OpenRouter and returns a structured summary with evidence segment IDs.

**Configure:** TUI Config screen → API Keys → `openrouter`, or:

```bash
noto providers key-set openrouter <your-key>
noto providers active-llm google/gemma-3-27b-it   # set active model
# or env var:
export NOTO_OPENROUTER_KEY=<your-key>
```

When no key is set, the pipeline synthesizes a deterministic placeholder summary.

## Speaker Embedding Provider (optional)

AssemblyAI diarization returns per-meeting labels (`speaker_0`, `speaker_1`, ...) —
not stable voice identities. For persistent cross-meeting speaker profiles, point
the backend at an embedding service:

```bash
export NOTO_SPEAKER_EMBEDDING_URL=http://embedding-host:8080
```

### Embedding service contract

```
POST {NOTO_SPEAKER_EMBEDDING_URL}/v1/speaker-embeddings
```

Request:
```json
{
  "meeting_id": "uuid",
  "audio_base64": "...",
  "speakers": [
    { "id": "spk_0", "provider_label": "A", "display_name": "Speaker A" }
  ],
  "segments": [
    { "id": "seg_000001", "speaker_id": "spk_0", "start_seconds": 0.0, "end_seconds": 4.2 }
  ]
}
```

Response:
```json
{
  "model": "titanet-large",
  "embeddings": { "A": [0.01, 0.02, 0.03] }
}
```

If unconfigured or unreachable, transcription still completes — speaker mappings
remain `"unmatched"` in the database until an embedding service is available.

## Artifact Formats

All providers normalize output to standard Noto schemas.

### Transcript artifact (`transcript.json`)

```json
{
  "schema_version": "transcript.v1",
  "meeting_id": "uuid",
  "provider": { "id": "assemblyai", "job_id": "aai_xyz" },
  "speakers": [
    { "id": "spk_0", "display_name": "Alice", "origin": "local_speaker", "label": "me" },
    { "id": "spk_1", "display_name": "Bob",   "origin": "participants",  "label": "participants" }
  ],
  "segments": [
    {
      "id": "seg_000000",
      "speaker_id": "spk_0",
      "source_role": "local_speaker",
      "start_seconds": 0.0,
      "end_seconds": 5.2,
      "text": "Let's start with the roadmap review.",
      "confidence": 0.97
    }
  ]
}
```

### Summary artifact (`summary.json`)

```json
{
  "schema_version": "summary.v1",
  "meeting_id": "uuid",
  "short_summary": "Three decisions on the roadmap. Timeline risk flagged.",
  "decisions": [
    {
      "text": "Ship v1 by end of Q2",
      "speaker_ids": ["spk_0"],
      "evidence": [{ "segment_id": "seg_000140", "quote": "ship v1 by end of quarter" }]
    }
  ],
  "action_items": [
    {
      "text": "Circulate updated timeline by Friday",
      "owner": "spk_1",
      "evidence": [{ "segment_id": "seg_000210", "quote": "timeline by Friday" }]
    }
  ],
  "risks": [
    { "text": "Q2 timeline may be aggressive", "evidence": [...] }
  ],
  "open_questions": [
    { "text": "Which open questions are highest priority?", "evidence": [...] }
  ],
  "model": { "provider": "openrouter", "model_id": "google/gemma-3-27b-it", "prompt_version": "summary.v1" }
}
```

## Provider Configuration Summary

| Provider | CLI command | Env var (fallback) |
| --- | --- | --- |
| AssemblyAI STT | `noto providers key-set assemblyai <key>` | `NOTO_ASSEMBLYAI_KEY` |
| OpenRouter LLM | `noto providers key-set openrouter <key>` | `NOTO_OPENROUTER_KEY` |
| Active LLM model | `noto providers active-llm <model>` | `NOTO_LLM_MODEL` |
| Speaker embeddings | — | `NOTO_SPEAKER_EMBEDDING_URL` |

## Adding a Provider

**STT:**
1. Implement `stt.STTProvider` in `internal/platform/providers/stt/`
2. Register in `internal/platform/providers/registry.go`
3. Add schema tests for the transcript output
4. Confirm downstream processors (search, summary) work unchanged

**LLM:**
1. Implement `llm.SummaryProvider` in `internal/platform/providers/llm/`
2. Register in the registry
3. Add schema tests for the summary output
