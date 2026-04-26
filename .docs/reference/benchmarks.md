# Benchmark Reference

## Purpose

Use repeatable benchmarks before choosing default providers or local models. The
benchmark suite must test Noto outputs, not provider demos.

Benchmark runs are part of the TDD plan: they write `benchmark-result.v1`,
validate normalized artifacts, and feed the phase gates in
[testing.md](./testing.md).

## Baseline Datasets

- AMI Meeting Corpus: meeting ASR, diarization, and summaries. Caveat:
  scenario meetings may not match modern remote calls.
- VoxConverse: diarization robustness on varied real-world audio. Caveat: less
  meeting-specific than AMI.
- DIHARD III: hard diarization cases across diverse domains. Caveat: data
  access and licensing must be checked before automation.
- QMSum: meeting summary quality. Caveat: text-first, so it does not validate
  audio capture or STT.
- MeetingBank: long public meeting summarization. Caveat: public meetings differ
  from private business calls.
- ELITR Minuting Corpus: minutes and action-item comparison. Caveat: recordings
  are excluded for privacy, so use it for summary only.
- Private Noto fixture: end-to-end product fit with real split-source capture.
  Caveat: it must be consented and redacted before sharing.

## Metrics

| Area | Metric | Target use |
| --- | --- | --- |
| Transcription | WER, named-entity error rate, number/date error rate | Compare STT text quality. |
| Diarization | DER, JER, speaker count error, speaker turn fragmentation | Compare speaker attribution. |
| Source attribution | local-speaker precision/recall, participant-source precision/recall, mixed-source count | Validate mic/system separation as a speaker hint. |
| Timestamps | median word/segment timestamp drift | Validate search citations and playback anchors. |
| Summary | citation precision, unsupported-claim count, action-item recall | Prevent plausible but uncited summaries. |
| Performance | real-time factor, peak RSS, idle CPU, upload bytes | Keep local and hosted modes efficient. |
| Cost | provider cost per audio hour and per summarized hour | Pick sustainable defaults. |

## Scoring Tools

| Metric | Tool | Rule |
| --- | --- | --- |
| WER | `jiwer` or SCTK `sclite` | Normalize punctuation/case consistently before scoring. |
| DER/JER | `pyannote.metrics` or NIST-compatible `md-eval` flow | Record collar, overlap handling, and speaker mapping settings with every run. |
| Summary evidence | Noto review checklist plus sampled human review | Fail unsupported decisions, action items, or risks. |
| Runtime/RSS | Noto benchmark harness | Measure provider wall time, local CPU time, peak RSS, idle CPU after completion, and bytes uploaded. |

## Provider Benchmark Matrix

| Provider | Required tests |
| --- | --- |
| AssemblyAI Universal-2/Universal-3 Pro | Async diarization, utterance normalization, channel/source preservation, domain prompt behavior, cost. |
| ElevenLabs Scribe v2 | Diarization at high speaker counts, word timestamps, keyterm prompting, source-role behavior. |
| Soniox async | Async diarization, token-to-segment grouping, mixed-language behavior, source-role behavior. |
| OpenAI diarized STT | `diarized_json` normalization, chunking strategy, known-speaker references, source-role behavior. |
| Local model path | Runtime, RAM/model size, offline diarization quality, install size. |

## First Benchmark Suite

1. Select 3 AMI meetings: short, medium, and overlap-heavy.
2. Add one 20-30 minute consented private meeting with split mic/system capture and domain terms.
3. Run two cloud STT processors and one local candidate if feasible.
4. Normalize every output to `transcript.v1`.
5. Score DER/JER where reference diarization exists.
6. Score local-speaker vs participant attribution on the private split-source fixture.
7. Score WER and domain-term errors where reference text exists.
8. Generate `summary.v1` and manually check citation precision.
9. Record latency, real-time factor, peak RSS, idle CPU after job completion, and cost.
10. Save scoring settings and provider versions in `benchmark-result.v1`.

## Acceptance Gates

- A provider cannot become the default unless its normalizer passes schema validation and downstream summary/search tests.
- A provider cannot become the default for recorded meetings unless it preserves or reliably reconstructs mic/system source-role hints.
- A local model cannot become a default unless 30-minute processing fits expected Mac memory and time budgets.
- Summary output cannot pass if a decision, action item, or risk lacks valid segment evidence.
- Benchmark results must be stored as artifacts, not prose-only notes.
- Benchmark failures must be machine-readable so agents can compare providers without reading prose reports.

## References

- [AMI Corpus](https://groups.inf.ed.ac.uk/ami/corpus/)
- [DIHARD III](https://dihardchallenge.github.io/dihard3/)
- [QMSum](https://github.com/Yale-LILY/QMSum)
- [MeetingBank](https://meetingbank.github.io/dataset/)
- [ELITR Minuting Corpus](https://impact.ornl.gov/en/publications/elitr-minuting-corpus-a-novel-dataset-for-automatic-minuting-from/)
- [pyannote benchmark notes on DER](https://www.pyannote.ai/benchmark)
- [pyannote.metrics](https://pyannote.github.io/pyannote-metrics/)
- [jiwer](https://github.com/jitsi/jiwer)
- [SDBench paper](https://arxiv.org/abs/2507.16136)
- [AssemblyAI speech-to-text docs](https://www.assemblyai.com/docs/speech-to-text)
- [ElevenLabs speech-to-text docs](https://elevenlabs.io/docs/capabilities/speech-to-text/)
- [Soniox speaker diarization docs](https://soniox.com/docs/speech-to-text/core-concepts/speaker-diarization)
- [OpenAI speech-to-text docs](https://platform.openai.com/docs/guides/speech-to-text)
