# Benchmark Reference

## Purpose

Benchmarks validate the remote backend output that users and agents consume:
normalized transcripts, speaker mappings, summaries, search results, and API
responses. They are not provider shopping documents.

## Baseline Datasets

- AMI Meeting Corpus: meeting transcription, diarization, and summary behavior.
- VoxConverse: varied speaker turn-taking and attribution stress cases.
- QMSum: summary quality and evidence citation checks.
- Private Noto fixture: consented end-to-end recording with realistic domain terms.

## Metrics

| Area | Metric | Target use |
| --- | --- | --- |
| Transcription | WER, named-entity error rate, number/date error rate | Validate transcript quality. |
| Diarization | DER, JER, speaker count error, turn fragmentation | Validate AssemblyAI speaker labels. |
| Speaker profiles | match precision/recall, pending-match rate, false merge rate | Validate cross-meeting identity mapping. |
| Timestamps | median word/segment timestamp drift | Validate search citations and playback anchors. |
| Summary | citation precision, unsupported-claim count, action-item recall | Prevent plausible but uncited summaries. |
| Cost | provider cost per audio hour and per summarized hour | Keep the remote default sustainable. |

## First Benchmark Suite

1. Run AssemblyAI on short, medium, and overlap-heavy meeting samples.
2. Normalize every response to Noto transcript artifacts.
3. Run speaker profile matching across at least two meetings with repeated speakers.
4. Verify summaries cite real transcript segments.
5. Store benchmark settings and outputs as machine-readable artifacts.

## Acceptance Gates

- AssemblyAI output must normalize into the standard transcript schema.
- Speaker profile tests must prove stable identity across repeated fixtures.
- Summary output cannot pass if a decision, action item, or risk lacks valid segment evidence.
- Benchmark failures must be machine-readable so agents can compare runs without reading prose reports.
