# `benchmark/dataset/` — ground truth

Loaders (`dataset.go`) and fetchers for the data the scorers measure against.
Media is **gitignored**; only specs/loaders are committed. Tests `t.Skip` when
assets are absent.

## What's here

```
dataset/
  dataset.go            loaders → Turn / Word / Meeting → metrics shapes
  fetch.py              builds the synthetic 2-speaker smoke from LibriSpeech clips
  librispeech/          per-speaker LibriSpeech clips + manifest.json (gitignored media)
  synthetic/            meeting_2spk.{wav,json} — generated smoke fixture
  words/                <meeting>.words.json — reference transcripts (gitignored)
```

AMI audio + diarization RTTM are fetched by the sibling
[`../identity/fetch.py`](../identity/fetch.py) into `../identity/ami/`; the STT
and diarize benches read from there (or a symlink).

## Formats

The Go side reads three small, well-specified shapes — see `dataset.go` for the
parsers:

**RTTM** (diarization ground truth, AMI ships it):
```
SPEAKER <file> <chan> <start> <dur> <NA> <NA> <speaker> <NA> <NA>
```
`ParseRTTM` reads `start`, `dur`, `speaker` → time-sorted `[]Turn`.

**synthetic meeting JSON** (`synthetic/meeting_2spk.json`):
```json
{ "speakers": ["2277","2035"], "duration": 49.0,
  "turns": [ {"speaker_id":"2277","start":0.0,"end":6.59}, ... ] }
```
`LoadSyntheticMeeting` → a `Meeting` with `Turns` and an `AudioPath` to the
sibling `.wav`. Turns only — no reference words.

**words JSON** — our **canonical reference-transcript format**
(`words/<meeting>.words.json`):
```json
[ {"speaker":"FEE005","start":1.20,"end":1.55,"text":"okay"}, ... ]
```
`LoadWords` → `[]Word`. Diarization sets carry no text, so a fetcher converts the
corpus's word annotations into this shape **once**; the Go loaders only ever read
this clean form, decoupled from AMI's NXT/CTM layout. A NIST `ParseCTM` helper is
also provided for callers holding per-speaker CTM files directly.

## Research & analysis scripts

Beyond the core loaders, this dir holds the accuracy-research experiments (findings
recorded in [`GPU_REDESIGN_PROGRESS.md`](../../GPU_REDESIGN_PROGRESS.md)). Offline
ones reuse an existing run's hyps (zero GPU); the `scripts/modal_*` ones run a
bounded Modal pass and write a JSON the local scorer reads.

**Offline (no GPU — reuse a stored run's hyps):**

| Script | Question → finding |
|--------|--------------------|
| `single_speaker_wer.py` | split WER by single-speaker vs overlap regions → single 15.8% (near ceiling) vs overlap 60.1%; the 20.7% headline is a Mix-Headset (overlap) artifact |
| `attribution_experiment.py` | can re-attributing words to diar turns cut cpWER? → no, the production merge is already optimal |
| `overlap_regions.py` | extract overlap regions + per-speaker ref words → the ground truth for evaluating separation |

**Modal STT/separation experiments (`scripts/modal_*.py` + local scorer here):**

| Experiment | Scripts | Finding |
|-----------|---------|---------|
| Overlap **separation** ("transcribe both") | `scripts/modal_overlap_sep.py` + `score_overlap_sep.py` | SepFormer → 2 streams → STT each recovers the 2nd speaker: **+15pt** overlap cpWER on substantive overlaps ([ADR 0007](../../.docs/decisions/0007-overlap-separation-refinement-pass.md)) |
| Accented WER (parakeet-weak set) | `scripts/modal_edacc_wer.py` + `fetch_edacc.py` + `score_edacc.py` | parakeet weak on hard accents (Spanish 29%, Kenyan 19%); but canary loses everywhere → model-swap repair is a dead-end |
| Per-channel WER (AMI-IHM) | `scripts/modal_ihm_wer.py` + `fetch_ami_ihm.py` + `score_ihm.py` | individual headsets carry cross-talk bleed → naive per-channel scoring is misleading; superseded by `single_speaker_wer.py` |

(`.modal-*.json` result dumps are gitignored.)

## A Meeting feeds every scorer

`Meeting` converts itself into exactly what `metrics` consumes:

| Method | Shape | Scorer |
|--------|-------|--------|
| `Segments()` | `[]metrics.Segment` | `metrics.DER`, attribution |
| `Reference()` | `[]string` (time-ordered, normalized) | `metrics.WER` |
| `ReferenceBySpeaker()` | `map[string][]string` | `metrics.CpWER`, `metrics.SAWER` |

Tokenization goes through `metrics.Normalize`, so reference and hypothesis are
tokenized identically.

## Fetching

```bash
python3 benchmark/dataset/fetch.py        # build the synthetic smoke (LibriSpeech)
python3 benchmark/identity/fetch.py        # AMI Mix-Headset wav + RTTM (~1.7 GB)
```

**AMI word references** (`words/<meeting>.words.json`) are not yet auto-fetched —
the STT/cpWER benches that need them convert AMI's word annotations into the
canonical format when they're wired (the source/format is verified against real
data there, per the harness plan's open risk on CTM-vs-NXT).

**Model weights** are deliberately *not* fetched by a single `models.py`: the
local STT/diarizer runtime is pluggable (an `STTEngine`/`SegmentEngine` behind
`LocalSTT`/`LocalDiarizer`), so weights are runtime-specific. Each engine's setup
fetches its own weights when that runtime is wired (behind its build tag); the
benchmarks run against the ground-truth **oracle** engine until then.
