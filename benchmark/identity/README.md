# Cross-meeting persona accuracy (real meetings)

Does noto link the **same person** to the **same profile across different
meetings**, and not confuse different people? This evaluates exactly that, on
real multi-party meeting audio, through noto's **production** embedding +
matching code (`internal/platform/providers/speaker` → `internal/core/speakers`).
LibriSpeech (read speech, one speaker per clip) can't answer this — we need
real meetings with the same people recorded on separate occasions.

## Dataset — curated AMI Meeting Corpus subset (`dataset.json`)

The [AMI corpus](https://groups.inf.ed.ac.uk/ami/corpus/) records the **same
participants across a series of sessions** (e.g. `ES2005a/b/c/d`). The
ground-truth diarization (RTTM, via
[pyannote/AMI-diarization-setup](https://github.com/pyannote/AMI-diarization-setup))
labels turns with **global participant IDs** that are consistent across
sessions — so "is this the same person in another meeting?" has an unambiguous
answer. We use the single-channel `Mix-Headset` mix (one mic, overlap +
crosstalk — a realistic single-stream meeting recording) and ground-truth turns,
so this isolates the part noto owns: voice **embedding + cross-meeting
matching**, not the upstream diarizer.

`dataset.json` (committed) is the dev-research corpus spec: `build_dataset.py`
indexes every AMI meeting's RTTM and greedily picks 3 sessions from each
scenario series until ~50 distinct speakers are covered, so **every speaker
appears in 3 meetings** (1 enroll + 2 test). Current spec: **52 speakers, 39
meetings, ~1.7 GB**. Media is re-downloadable, never committed.

```bash
python3 benchmark/identity/build_dataset.py     # regenerate dataset.json (optional)
python3 benchmark/identity/fetch.py             # download the dataset media (~1.7 GB) into ami/
go test ./benchmark/identity/ -run Persona -v               # quick experiments (ES2002)
PERSONA_DEEP=1 go test ./benchmark/identity/ -run Benchmark50 -v -timeout 900s   # full 52-speaker bench
```

(Needs the model installed — `noto speaker-model download`, or the dev copies in
`tools/voiceprint/`; override with `ECAPA_MODEL`/`ORT_LIB`/`FFMPEG`.)

## Experiments

- **Benchmark50 (deep):** enroll each of the 52 speakers from their first
  meeting, then test every other meeting they appear in (104 test personas)
  against the full 52-speaker gallery. Reports closed-set identification
  (rank-1/5), verification EER over all genuine/impostor pairs, and what the
  **shipped** (`auto≥0.70 / pending≥0.55`) vs **calibrated** (`0.58 / 0.50`)
  thresholds do. Gated behind `PERSONA_DEEP=1` (~200 s: 39 meeting decodes).
- **CrossSession / SplitMeeting (quick):** the ES2002 series — enroll session
  `a`, link `b/c/d`; and split one 40-min meeting into 3 chunks. Fast sanity
  checks that run in the normal suite when assets are present.
- **Stress (failure review):** open-set rejection, short-speech buckets, the
  hardest confusable pairs, and a **decision-layer comparison** of the shipped
  `Match` vs the new `MatchConfident` (margin gate). Gated `PERSONA_DEEP=1`.
- **Conditions (degradation sweep):** re-embeds the corpus under short speech,
  simulated diarization speaker-confusion, and telephone-band audio to find
  where accuracy actually breaks. Gated `PERSONA_DEEP=1` (~14 min).

## Results (ECAPA-512, this machine)

### Deep — 52-speaker gallery, 104 test personas, 5304 impostor pairs

Numbers below are with the **robust (medoid-anchored) centroid** that is now the
default aggregation (see "Robust aggregation" further down). The pre-change
`mean` figures are in parentheses for contrast.

| metric | value |
|---|---|
| **identification rank-1** | **104/104 = 100.0%** (rank-5 100%) |
| **verification EER** | **0.00%** @ threshold 0.623  *(mean: 0.09%)* |
| score separation | genuine min **0.623** > impostor max **0.596** — **clean gap** *(mean: 0.568 < 0.671, overlap)* |
| shipped `auto 0.70 / pend 0.55` | true-link auto **99.0%** *(mean 96.2%)* / surfaced 100% · impostor **≥auto 0.00%** |
| calibrated `0.58 / 0.50` | true-link auto 100% / surfaced 100% · impostor≥pend 0.85%, ≥auto 0.06% |

Reading the numbers:

- **Every** test persona's nearest profile in a 52-way gallery is the right
  person (100% rank-1).
- The big change from robust aggregation: the genuine and impostor distributions
  **no longer overlap** (weakest genuine 0.623 now sits *above* the strongest
  impostor 0.596), so a clean global threshold exists and **EER is 0.00%** over
  5k+ pairs. With the plain mean the weakest link (0.568) fell below the
  strongest impostor (0.671).
- The strongest impostor stays **below the shipped auto threshold (0.70)** ⇒
  **zero false auto-merges**, while pending (0.55) surfaces 100% of true links.
  Shipped thresholds remain **precision-safe**; only 1% of true links land in
  "pending" now (was 3.8%).
- Keep `auto` high (0.70) for a meeting roster — the LibriSpeech-calibrated 0.58
  raises recall but starts false auto-merging, so it is not adopted.

### Quick — ES2002 series (4 speakers)

| experiment | rank-1 | mean cos same / impostor | weakest true-link | strongest impostor |
|---|---|---|---|---|
| cross-session (a → b+c+d) | 12/12 = 100% | 0.874 / 0.242 | 0.801 | 0.311 |
| split one meeting (3 chunks) | 8/8 = 100% | 0.866 / 0.348 | 0.740 | 0.472 |

### Condition sweep — where it breaks (`PERSONA_DEEP=1 -run Persona_Conditions`)

The clean numbers above use ground-truth turns and full-bandwidth audio. This
sweep re-embeds the **same 52 speakers** under realistic degradations:

| condition | rank-1 | EER | stranger-FAR@0.55 | verdict |
|---|---|---|---|---|
| **baseline** (clean GT turns) | 100.0% | 0.09% | 5.13% | reference |
| short speech, 6 s/persona | 94.2% | 1.84% | 3.85% | mild |
| short speech, 3 s/persona | 93.3% | 1.92% | 3.85% | mild — ID survives |
| **diarization noise, 15%** | 93.3% | 2.88% | **39.74%** | open-set collapses |
| **diarization noise, 30%** | **58.7%** | 9.62% | **62.82%** | catastrophic |
| **telephone band, 8 kHz** | 100.0% | 0.05% | **52.56%** | ID fine, rejection collapses |

**The embedder is not the bottleneck — the pipeline around it is.** Identification
shrugs off short speech and even telephone-band narrowing. The two things that
actually break it: **(a) diarization speaker-confusion** — mislabeling 15% of
turns poisons the enrollment centroid and the stranger false-accept rate jumps
5%→40%, at 30% identification itself craters to 59%; and **(b) channel mismatch /
open-set** — 8 kHz telephone keeps rank-1 at 100% but lets strangers in 52% of
the time. Neither is visible in the happy-path KPIs.

(The `stranger-FAR@0.55` column above uses the plain-mean baseline so it lines
up with the original numbers; the SOTA table below re-runs the same conditions
with the robust centroid and an operating-point-fair FAR metric.)

### Robust aggregation × AS-Norm — `PERSONA_DEEP=1 -run Persona_SOTA`

This isolates two upgrades: a **robust (medoid-anchored, bimodality-guarded)
centroid** that drops windows a diarizer misassigned to a speaker before
averaging, and **AS-Norm** score normalization. `FAR@1%FRR` is the stranger
false-accept rate at a 99%-genuine-recall operating point (comparable across
scorers).

| condition | method | rank-1 | EER | FAR@1%FRR |
|---|---|---|---|---|
| **baseline** | mean+cosine *(original)* | 100% | 0.09% | 0.11% |
| | **robust+cosine** | 100% | **0.00%** | **0.00%** |
| | robust+AS-Norm | 100% | 0.00% | 0.00% |
| **diar-noise 15%** | mean+cosine | **93.3%** | **2.88%** | **20.97%** |
| | robust+cosine | 86.5% | 8.65% | 75.23% |
| **diar-noise 30%** | mean+cosine | **58.7%** | **9.62%** | **76.02%** |
| | robust+cosine | 55.8% | 14.42% | 95.91% |
| **telephone 8kHz** | mean+cosine | 100% | 0.05% | 0.06% |
| | **robust+cosine** | 100% | **0.03%** | 0.06% |

- **Robust aggregation is a qualitative win in normal + channel conditions:** it
  **closes the genuine/impostor overlap** (EER 0.09%→0.00%), lifts auto-recall
  96%→99% and cuts stranger false-accepts 5.13%→3.85%. It is now the **default**.
- **It does *not* fix diarization errors — it slightly regresses them.** A
  bimodality guard (fall back to mean when the dropped windows form a coherent
  rival cluster) roughly halved the damage vs the naive medoid (15%: 82.7%→86.5%,
  30%: 44.2%→55.8%) but robust still trails the mean there. This is under an
  **adversarial** corruption model (turns sprayed to uniformly-random speakers)
  that overstates real diarizer behavior; real operation sits near baseline. The
  proper diarization fix is **per-word confidence weighting**, not aggregation.
- **AS-Norm is marginal on this corpus:** no gain where scores already separate,
  only a modest dent in the worst open-set FAR. Implemented and available
  (`speakers.ScoreASNorm`) but not enabled in the live matcher.

### Decision layer — margin gate (`MatchConfident`) cost/benefit

Run through the **production matcher**, not raw cosines (104 genuine tests, full
52-profile gallery; strangers vs a 26-profile half-gallery):

| | plain `Match` | `MatchConfident` (margin 0.05) |
|---|---|---|
| genuine auto-correct | 103/104 | 103/104 |
| genuine auto-**wrong** | 0 | 0 |
| correct auto-links downgraded | — | **0 (0.00%)** |
| wrong auto-merges prevented | — | 0 |
| strangers reaching **auto** | 0 | 0 |
| strangers reaching **pending** | 3 | 3 |

The margin gate is a **free safety net** here: it never downgrades a correct
confident link (0% cost) because AMI links have clean top-1 leads, and there were
no wrong auto-merges to catch on this roster — it earns its keep on denser /
same-gender rosters where two profiles genuinely collide. Crucially, it **does
not** reduce the 4 pending-band stranger suggestions (5.13%): the gate only
withholds *auto* merges, so the residual open-set problem is a **`pending`-floor**
issue, not a margin one.

### What fails (stress review — `PERSONA_DEEP=1 go test -run Persona_Stress`)

The happy-path numbers above use ground-truth turns, a closed set, and everyone
enrolled. `TestPersona_Stress` removes those crutches. Where it cracks:

- **Open-set false suggestions (~5%).** Enroll half the roster, treat the other
  half as strangers: **4/78 strangers (5.13%) score ≥ pending (0.55)** against
  some enrolled profile — i.e. the UI would suggest "is this <wrong person>?".
  But **0/78 reach auto (0.70)**: the system never *silently* merges a stranger.
  All 52 known links were surfaced (0 missed). (Gallery here is 26; with all 52
  enrolled the stranger false-suggestion rate is only higher — 5% is a floor.)
- **Same-gender voices are the failure mechanism.** Every hard impostor pair is
  same-sex: `FEE042/FEE043 = 0.671`, `FIE088/FIO084 = 0.665`, `MIO005/MIO035`,
  `MIO072/MIO078` — all in the 0.59–0.67 pending band. Cross-gender pairs never
  come close.
- **No clean global threshold.** The weakest true-link (`FIO084 = 0.568`) sits
  *below* the strongest impostor (`0.671`): between 0.57 and 0.67 you trade
  missed links for false accepts. That overlap is why EER is 0.09%, not 0, and
  why the `pending` band exists for human review.
- **Short-speech failure is unproven here.** AMI scenario participants all speak
  minutes (only 1 test persona < 45 s, and it scored lower: genuine 0.747 vs
  0.872). A real meeting where someone says one line is expected to be much
  weaker — this corpus can't measure it.

### Not tested at all (known blind spots)

- **Diarization errors.** We feed ground-truth turns, so the embedder gets clean
  per-speaker audio. Production uses AssemblyAI diarization, whose
  speaker-confusion/overlap errors are the dominant real-world risk and are
  **not** measured here.
- **Cross-device / cross-room acoustics.** AMI records the same people on the
  same headsets in the same room across sessions. Linking a laptop mic to a
  phone call on another day would lower genuine scores well below these numbers.

### Verdict

Identification and `auto`-precision are strong (100% rank-1, 0 false
auto-merges) and robust to short speech and narrowband audio. The system's real
failure modes are, in order:

1. **Upstream diarization errors** — the dominant production risk. Mislabeled
   turns poison enrollment; stranger false-accepts hit 40–63%. The matcher gates
   don't touch this; it needs per-turn confidence / overlap rejection *before*
   building a centroid.
2. **Open-set / channel** — the residual ~5% pending-band stranger suggestions
   (baseline) blow up to 52% on telephone-band audio. The lever is the
   **`pending` floor** (→~0.60) and channel normalization, *not* the margin gate.
3. **Same-gender confusion** — every hard pair is same-sex; the **margin gate**
   (now implemented) is the safety net for when two such profiles collide at auto.

**Implemented this round:**

- **Robust centroid aggregation** (`speakers.RobustCentroid`, now the default in
  `LocalEmbedder.EmbedSpeakers`) — medoid-anchored, bimodality-guarded outlier
  rejection. Closes the genuine/impostor overlap on clean audio (EER 0.09%→0%,
  auto-recall 96%→99%, stranger FAR 5.13%→3.85%). Trade-off: a documented few-point
  regression under heavy/adversarial diarization corruption (guard halves it).
- **AS-Norm** score normalization (`speakers.ScoreASNorm`) — implemented and
  unit-tested; marginal on this corpus, so not enabled in the live matcher.
- **Top-1-vs-top-2 margin gate** (`MatchConfident`, on the live path — free on
  this corpus, 0% recall cost) and a **min-enrollment-speech gate**
  (`MinEnrollSpeech=3s`, in `matchSpeakers`). Thresholds are now a `MatchConfig`
  so they can be made config-driven without touching the matcher.

Still open: per-word **confidence weighting** (the real diarization-error fix),
nudging the `pending` floor, and cross-device acoustics.

Caveat: one corpus, ~50 speakers, one machine, ground-truth diarization — a
strong signal, not a published benchmark. Extend the roster by editing the
`--target`/`--sessions` of `build_dataset.py` and re-fetching.
