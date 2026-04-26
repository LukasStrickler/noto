# Processor And Provider Reference

## Strategy

Use swappable processors behind stable Noto schemas. Benchmark public datasets
and real consented meeting audio before choosing the default STT provider. V1
should preserve mic/system source roles so transcription can distinguish the
local speaker from meeting participants.

Adapters can change. Artifact formats cannot.

## Processor Shape

| Processor | Swappable examples | Required output |
| --- | --- | --- |
| Audio preparation | native encoder, ffmpeg-style converter, silence trimming | provider-ready audio asset metadata |
| Transcription | AssemblyAI, ElevenLabs, Soniox, OpenAI, local model | `transcript.diarized.json` with source-role hints when available |
| Summary | OpenAI-compatible model, hosted gateway, local model | `summary.json` |
| Rendering | built-in Markdown renderer, export renderer | `.md` artifacts |
| Indexing | SQLite FTS5, future search backend | standard search rows/results |

Processor metadata must declare ID, inputs/outputs, capabilities, config keys,
cost/latency hints when known, and whether audio or transcript text leaves the
device.

## Source-Aware Transcription

The audio processor should prefer a capture layout that keeps microphone and
system/app audio distinguishable. Provider adapters should use that source
information when possible:

- Preserve `src_mic` as `local_speaker` and `src_system` as `participants`.
- Prefer provider modes that accept stereo/channel-separated audio or
  speaker/channel metadata.
- If a provider collapses channels, keep source roles in `audio.json` and mark
  transcript speaker origins as `unknown` or `mixed`.
- Never overwrite source-derived local/participant hints with weak provider
  labels unless confidence is high.
- Speaker rename changes display names only; it must preserve source origin metadata.

## STT Candidates

- AssemblyAI Universal-2: baseline cloud candidate. Test names, domain terms,
  diarization, and source preservation.
- AssemblyAI Universal-3 Pro: quality candidate. Test whether cost, latency, and
  source-aware attribution justify using it.
- ElevenLabs Scribe v2: word timestamps, diarization, keyterm prompting, and
  high speaker-count claims make it worth benchmarking.
- Soniox async: async diarization and token speaker labels need workflow and
  source-role validation.
- OpenAI diarized STT: `diarized_json` and known-speaker references need
  chunking and source-role validation.
- Local model: privacy/offline path; post-V1 unless benchmarks justify earlier
  work.

## Summary Providers

Use a separate summary provider interface. Prefer OpenAI-compatible summary
adapters so hosted models, local endpoints, and LLM gateways can share one
request shape. Do not force STT through an LLM gateway; STT APIs differ in
upload, polling, diarization, timestamps, and normalization.

## Provider Modes

| Mode | Key owner | Raw audio path |
| --- | --- | --- |
| Local keys | User/customer | Client calls provider. |
| Hosted managed | Noto | Worker calls provider from Noto storage. |
| Customer-managed hosted | Customer | Worker uses customer key from vault. |
| Local model | User/customer | Audio stays local. |

## Normalization Rules

- Provider output is normalized immediately after the provider job completes.
- Downstream processors consume only Noto artifacts, not provider payloads.
- Unknown provider fields stay in debug output or metadata, not in core schemas.
- Source-derived `local_speaker` and `participants` hints are normalized into Noto artifacts before downstream use.
- Adding a provider requires a normalizer and schema validation tests.
- Adding a processor requires parity tests proving the same standard output contract.

## Add A Provider Or Processor

1. Implement the relevant processor interface.
2. Declare ID, capabilities, input type, output type, and configuration keys.
3. Transform provider/native output into the required Noto artifact.
4. Add schema validation tests for the output artifact.
5. Add a parity fixture proving downstream processors work without changes.
6. Document whether data leaves the device and which credentials are required.

Do not add provider-specific fields to core artifacts for one adapter. Use debug metadata instead.

Provider work is not done until the contract tests and agentic validation gates in [testing.md](./testing.md) pass for that provider.

## Benchmark Criteria

Provider selection is benchmark-gated. See [benchmarks.md](./benchmarks.md) for datasets, scoring tools, metrics, and acceptance gates.

## References

- [AssemblyAI diarization](https://www.assemblyai.com/docs/speech-to-text/speaker-diarization)
- [ElevenLabs speech-to-text](https://elevenlabs.io/docs/capabilities/speech-to-text/)
- [Soniox diarization](https://soniox.com/docs/speech-to-text/core-concepts/speaker-diarization)
- [OpenAI speech-to-text](https://platform.openai.com/docs/guides/speech-to-text)
- [Ollama OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility)
- [LiteLLM](https://github.com/BerriAI/litellm)
