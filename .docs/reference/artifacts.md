# Artifact Reference

## Local Layout

```text
~/Noto/
  config.json
  SKILL.md
  prompts/summary.v1.md
  meetings/YYYY/MM/{meeting_id}/
    manifest.json
    versions/{version_id}/
      meeting.json
      audio.json
      transcript.diarized.json
      transcript.md
      summary.json
      summary.md
      checksums.json
      audio/
        recording.m4a
    .tmp/recording.m4a
  benchmarks/runs/{run_id}/
    benchmark-result.json
  indexes/noto.sqlite
```

## Version Rules

- `manifest.json` points at the current version.
- Version folders are immutable; edits create new versions.
- Transcripts and summaries are never silently merged.
- `indexes/noto.sqlite` is rebuildable cache; `.tmp/recording.m4a` is staging only.
- Retained audio is promoted into `versions/{version_id}/audio/`.
- Raw audio is deleted only after transcript artifacts validate and retention policy allows deletion.
- V1 retention is local-only. No hosted upload or remote retention path exists in V1.

## Artifact Contract

Artifacts are the stable boundary between processors. Any provider or processor may be replaced if it produces the same required artifact schema.

| Artifact | Producer | Consumers |
| --- | --- | --- |
| `meeting.json` | import, recorder, metadata editor | TUI, search, sync, agents |
| `audio.json` | recorder, import, audio processor | transcription, retention, sync |
| `transcript.diarized.json` | transcription processor | summary, render, search, agents |
| `summary.json` | summary processor | render, TUI, agents |
| `transcript.md` | render processor | users, exports |
| `summary.md` | render processor | users, exports |
| `checksums.json` | artifact writer | sync, verify, repair |

Processor raw payloads are optional debug artifacts, never downstream inputs.

## Minimal `manifest.json`

```json
{
  "schema_version": "manifest.v1",
  "meeting_id": "mtg_20260424_153012_ab12",
  "current_version_id": "ver_20260424_142001_c932",
  "versions": [
    {
      "version_id": "ver_20260424_142001_c932",
      "created_at": "2026-04-24T14:20:01Z",
      "reason": "summary_created",
      "checksum": "sha256:manifest-content-hash"
    }
  ]
}
```

## Minimal `meeting.json`

```json
{
  "schema_version": "meeting.v1",
  "id": "mtg_20260424_153012_ab12",
  "title": "Roadmap sync",
  "started_at": "2026-04-24T13:30:12Z",
  "ended_at": "2026-04-24T14:12:48Z",
  "timezone": "Europe/Berlin",
  "status": "summarized",
  "raw_audio_retained": true,
  "source": {
    "kind": "recording",
    "capture_device": "macos-screencapturekit",
    "source_policy": "prefer_split_mic_system",
    "local_speaker_source": "mic",
    "participant_source": "system"
  },
  "providers": {
    "transcription": "example-stt:baseline",
    "summary": "openai-compatible:default"
  }
}
```

Provider IDs in examples are placeholders. The default STT provider is not chosen
until the benchmark gates pass.

## Minimal `audio.json`

```json
{
  "schema_version": "audio-asset.v1",
  "meeting_id": "mtg_20260424_153012_ab12",
  "asset_id": "aud_20260424_153012_ab12",
  "path": "audio/recording.m4a",
  "format": "m4a",
  "codec": "aac",
  "duration_seconds": 2560.4,
  "channels": 2,
  "sample_rate_hz": 48000,
  "sources": [
    {
      "id": "src_mic",
      "role": "local_speaker",
      "label": "Microphone",
      "channel": 0,
      "device_name": "MacBook Pro Microphone"
    },
    {
      "id": "src_system",
      "role": "participants",
      "label": "System/App Audio",
      "channel": 1,
      "device_name": "ScreenCaptureKit System Audio"
    }
  ],
  "size_bytes": 48219342,
  "sha256": "a3e1...",
  "retention": {
    "policy": "delete_after_valid_transcript",
    "deleted_at": null,
    "retained": true
  }
}
```

## Minimal `transcript.diarized.json`

```json
{
  "schema_version": "transcript.v1",
  "meeting_id": "mtg_20260424_153012_ab12",
  "language": "en",
  "duration_seconds": 2560.4,
  "provider": {
    "id": "example-stt:baseline",
    "job_id": "provider_job_123",
    "raw_response_ref": null
  },
  "speakers": [
    {
      "id": "spk_0",
      "label": "Speaker 0",
      "origin": "local_speaker",
      "default_source_id": "src_mic",
      "display_name": null,
      "provider_label": "A"
    },
    {
      "id": "spk_1",
      "label": "Speaker 1",
      "origin": "participant",
      "default_source_id": "src_system",
      "display_name": null,
      "provider_label": "B"
    }
  ],
  "segments": [
    {
      "id": "seg_000001",
      "speaker_id": "spk_0",
      "start_seconds": 0.42,
      "end_seconds": 5.18,
      "source_id": "src_mic",
      "source_role": "local_speaker",
      "channel": 0,
      "overlap": false,
      "text": "Let's start with the roadmap.",
      "confidence": null,
      "word_ids": ["w_000001", "w_000002"]
    }
  ],
  "words": [
    {
      "id": "w_000001",
      "segment_id": "seg_000001",
      "speaker_id": "spk_0",
      "start_seconds": 0.42,
      "end_seconds": 0.71,
      "source_id": "src_mic",
      "source_role": "local_speaker",
      "text": "Let's",
      "confidence": null,
      "speaker_confidence": null
    }
  ],
  "capabilities": {
    "word_timestamps": true,
    "speaker_diarization": true,
    "overlap_detection": false,
    "channels": true,
    "source_roles": true
  }
}
```

## Source And Speaker Terms

| Field | Values |
| --- | --- |
| `sources[].role`, `source_role` | `local_speaker`, `participants`, `mixed`, `unknown` |
| `speakers[].origin` | `local_speaker`, `participant`, `mixed`, `unknown` |
| Display labels | `me/mic`, `participants/system`, custom names |

Meanings:

- `sources[].role` and `source_role` are audio-source roles used by artifacts,
  search, and agent JSON.
- `speakers[].origin` is speaker-level attribution inferred from source
  evidence.
- Display labels are human-facing only; they do not change stored source roles.

Use `participants` for source roles because a system/app audio source can contain
more than one remote speaker. Use `participant` for a single inferred speaker
origin. Speaker rename changes display names only.

## Minimal `summary.json`

```json
{
  "schema_version": "summary.v1",
  "meeting_id": "mtg_20260424_153012_ab12",
  "short_summary": "The meeting settled on a local-first MVP.",
  "decisions": [
    {
      "text": "Use post-meeting diarization for V1.",
      "speaker_ids": ["spk_0"],
      "evidence": [
        {
          "segment_id": "seg_000210",
          "quote": "post-meeting diarization is enough for V1"
        }
      ]
    }
  ],
  "action_items": [
    {
      "text": "Run the first provider benchmark suite.",
      "owner": null,
      "due_at": null,
      "evidence": [{ "segment_id": "seg_000245", "quote": "benchmark both providers" }]
    }
  ],
  "open_questions": [],
  "risks": [],
  "model": {
    "provider": "openai-compatible:default",
    "prompt_version": "summary.v1"
  }
}
```

If audio is deleted, keep `audio.json`, set `retention.retained=false`, set `deleted_at`, and remove the audio file from the next version.

## Hosted Audio Retention

Hosted processing is post-V1. When added:

- Raw audio may leave the device only under explicit workspace policy.
- The client uploads raw audio through exact-key signed object access, not through API request bodies.
- Hosted workers delete temporary raw audio after normalized transcript artifacts validate unless the workspace retention policy explicitly keeps it.
- If local and hosted raw audio are both deleted, reprocessing requires re-importing or re-recording the audio.
- `audio.json` remains the audit record after deletion and must show local and hosted retention state separately.

## Minimal `checksums.json`

```json
{
  "schema_version": "checksums.v1",
  "algorithm": "sha256",
  "files": {
    "meeting.json": "sha256:...",
    "audio.json": "sha256:...",
    "transcript.diarized.json": "sha256:...",
    "transcript.md": "sha256:...",
    "summary.json": "sha256:...",
    "summary.md": "sha256:..."
  }
}
```

## Minimal `benchmark-result.json`

```json
{
  "schema_version": "benchmark-result.v1",
  "run_id": "bench_20260424_180000_a91f",
  "created_at": "2026-04-24T16:00:00Z",
  "dataset": {
    "name": "AMI",
    "sample_id": "ES2004a",
    "license_checked": true
  },
  "processor": {
    "id": "assemblyai:universal-3-pro",
    "version": "2026-04-24"
  },
  "metrics": {
    "wer": null,
    "der": null,
    "jer": null,
    "real_time_factor": 0.42,
    "peak_rss_mb": 180,
    "idle_cpu_percent": 0.0,
    "cost_usd": 0.31
  },
  "scoring": {
    "wer_normalization": "lowercase_strip_punctuation",
    "diarization_collar_seconds": 0.25,
    "diarization_skip_overlap": false
  },
  "outputs": {
    "transcript_schema_valid": true,
    "summary_schema_valid": true,
    "citation_precision_checked": false
  }
}
```

## Schema Rules

- `schema_version` is required in every JSON artifact.
- IDs are stable within a version; speaker rename creates a new version.
- Segment IDs must be monotonic by start time.
- Word timestamps are optional only when the processor declares no word timestamp capability.
- Segment and word timestamps may overlap when speakers overlap; monotonic ordering uses start time, then end time, then ID.
- `channel` is nullable for mixed/system audio and required only when the capture path preserves separate channels.
- Recorded V1 audio should preserve mic and system/app audio as separate sources whenever the OS capture path supports it.
- `sources[].role` uses `local_speaker` for microphone input and `participants` for system/app audio by default.
- Transcript speakers should include `origin` when source evidence supports it: `local_speaker`, `participant`, `mixed`, or `unknown`.
- Transcript segments and words should include `source_id` and `source_role` when the source can be mapped.
- `overlap` marks known overlapping speech; it can be `false` when the provider does not expose overlap detection.
- Summary evidence must reference existing segment IDs and include a short supporting quote when text is retained.
- Provider job IDs may be stored, but provider-native response bodies stay outside the core artifacts.
- Benchmark result artifacts must never include private transcript text unless explicitly created in a private fixture directory.

## Validation Requirements

Artifact tests must be fixture-first:

- Valid fixtures pass JSON schema validation and checksum validation.
- Invalid fixtures fail with stable CLI error codes.
- Mutating commands write partial output under `.tmp/` and promote only after schemas and checksums pass.
- `noto verify --json` reports schema, checksum, source-role, retention, path, and index freshness state.
- Agent-facing commands return artifact paths that exist and match the current manifest.

See [testing.md](./testing.md) for the fixture set and Definition of Done.

## Related

- [Architecture reference](./architecture.md)
- [Processor and provider reference](./providers.md)
- [CLI reference](./cli.md)
- [Testing and validation](./testing.md)
