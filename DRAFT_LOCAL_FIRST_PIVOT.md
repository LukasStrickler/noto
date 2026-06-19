# Local-First Pivot — Draft Design Note

> **Status: DRAFT / proposal.** Captures the reasoning from a design discussion.
> Not a committed plan. Numbers are 2026 estimates; verify before building.
> Scope: how noto should produce attributed meeting transcripts, where the
> compute runs, and what the hosted offering looks like.

---

## TL;DR

Pivot noto from a **cloud-dependent** transcriber (all STT + diarization via
AssemblyAI) to a **local-first** one: capture, transcription, diarization,
speaker identity, and summarization can all run **on the user's machine**, with
the cloud demoted to an **optional offload** (sync, heavy compute, an agent
query API). Same binary, swappable provider implementations.

The unlock is timing: in 2026, open ASR/diarization models run **fast enough on
consumer hardware / Apple Silicon** to make local both viable and *better* for
our case — it gives a genuinely free, private, offline tier and removes ~80% of
our per-hour cost. The hosted tier stays for people who'd rather not run it
themselves or want an API for agents.

---

## Why pivot

**Today** every transcript requires the cloud: AssemblyAI does STT + speaker
diarization, OpenRouter does the LLM summary. That means:

- **Cost.** Marginal COGS is ~**$0.23 per hour of audio**, ~**80% of it STT**
  (AssemblyAI ~$0.17–0.19/hr + LLM ~$0.04/hr). Every minute recorded costs us
  money.
- **Privacy.** Voice — the most sensitive data class — leaves the device and
  passes through third-party subprocessors.
- **No free/offline tier.** "Local" today still calls AssemblyAI, so there's no
  truly self-contained mode.
- **Vendor dependency** on a single STT provider.

**What changed (2026):** open models crossed the line for on-device use —
- **Parakeet-TDT-0.6b-v3**: 25 European languages + elite English, **native word
  timestamps**, RTFx ~3,300, **2 GB RAM**, CC-BY-4.0; transcribes 1h of audio in
  ~20–50s on an M1.
- **FluidAudio**: native Swift/CoreML packaging of Parakeet + pyannote
  diarization on the Apple Neural Engine.
- **Streaming models** (Nemotron 3.5 ASR) and **streaming diarization**
  (Sortformer) make live, on-device transcripts realistic.

So local-first is now the **cheaper, more private, and more differentiated**
path — and it makes the free tier cost us **$0**.

---

## The two shapes (one codebase)

**A. Fully local** — `noto` runs capture, models, store, and TUI on-device. No
network, no per-use cost, fully private. The free / DIY tier.

**B. Local TUI + remote data + compute + API** — the TUI (or a thin client) runs
locally; storage, heavy transcription/diarization compute, and a query API live
on a server. For users who want to offload compute, sync across devices, or
expose an **agent-queryable API**. The paid / hosted tier.

The difference is **configuration + which provider implementation is active**,
not a second application.

---

## Platform & runtime portability

**TUI = Mac client. Backend (data + storage + compute) = the same code on Mac
*or* Linux. Only audio capture is genuinely Mac-locked.**

- **TUI** — Go / Bubble Tea; shipped for Mac (it's portable, but Mac is the
  target client).
- **Capture** (`cmd/capture`, Swift / ScreenCaptureKit) — the one Mac-only piece.
  Servers don't capture audio anyway (it's uploaded).
- **Backend = `noto serve`** — data, storage, and **all compute** (STT,
  diarization, identity, summary, search) run **in-process on the Mac** (local /
  free tier) **or on a Linux server** (remote / hosted tier). Same Go service; the
  *model runtime* swaps per OS behind the provider seams.

The models are portable; only the runtime is OS-specific:

| Stage | Mac runtime (local backend) | Linux runtime (remote backend) |
|---|---|---|
| STT | CoreML/ANE (FluidAudio), MLX (parakeet-mlx) | ONNX Runtime, NeMo+CUDA, sherpa-onnx, parakeet.cpp |
| Diarization | CoreML (FluidAudio) | PyTorch/NeMo+CUDA (pyannote/Sortformer), sherpa-onnx |
| Identity (ECAPA) | onnxruntime (CPU) | onnxruntime (CPU/CUDA) — **installer already fetches `linux-x64`** |
| Storage / search | SQLite + FTS5 | SQLite or Postgres + S3 |

Two cross-platform server idioms you already have in the codebase: **in-process
ONNX** (like ECAPA today; sherpa-onnx adds STT + diarization in Go, no Python) and
a **remote HTTP model service** (like `NOTO_SPEAKER_EMBEDDING_URL`, for NeMo /
pyannote on CUDA). The deployment modes also already exist (`notoapi.Client`:
in-process / UDS / remote TCP+bearer), so **"same backend, local Mac or remote
Linux"** needs no new architecture — just cross-platform model runtimes behind the
STT / diarizer seams.

> Net: **TUI Mac-only; data + storage + compute run identically on Mac (local)
> and Linux (remote); capture stays Mac.** Because the backend runs on both, the
> eval harness can benchmark the Mac-native (CoreML) and the Linux (ONNX/NeMo)
> runtime of the *same* model and confirm parity — record the runtime in the
> benchmark matrix.

---

## Compute backend selection & model provisioning

**Never force a GPU or CPU path — detect the hardware and pick the fastest
available backend, with fallback.** The model weights and the `transcript.v1`
output are fixed (**same accuracy everywhere**); only the *execution backend*
varies. CPU vs GPU is a ~20–40× **speed** difference and a **~0% accuracy**
difference at the same model/precision.

### Backend selection (automatic)

Two independent knobs — conflating them is the mistake:

- **Accelerator** — *free, always automatic.* Same model, same accuracy, fastest
  engine present.
- **Model / precision tier** — an *accuracy decision.* Default to the best the
  hardware can run; step down only when forced (slow CPU, or live latency). Keep
  it **visible and overridable — never silently downgrade accuracy.** (INT8 ≈ free;
  a lighter diarizer costs ~a few DER points.)

Detection ladder (first that works wins; fall back on **failure**, not just
absence):

```
1. Apple Silicon   → CoreML / ANE (FluidAudio, MLX)              [Mac client]
2. NVIDIA CUDA GPU → ONNX Runtime CUDA EP / NeMo                 [GPU server]
3. (optional) ROCm / DirectML                                   [AMD / Windows]
4. CPU             → ONNX Runtime CPU EP (INT8, threads=nproc) / sherpa-onnx
```

ONNX Runtime *is* this abstraction: load one model, hand it an execution-provider
priority list (`[CUDA, CoreML, CPU]`), and it auto-selects + falls back. So most of
this is **configuring the EP list, not writing three code paths** — and ECAPA
already runs on `onnxruntime` in-tree, so the path is proven.

Detect at `noto serve` startup, inject into the `STTProvider` / `Diarizer` /
`SpeakerEmbedder` constructors, cache the decision, and:
- **verify, don't just detect** — try the accelerator, catch failure (missing
  libs, GPU OOM), fall back to CPU;
- **override + log** — `NOTO_COMPUTE=auto|cuda|coreml|cpu`,
  `NOTO_MODEL_TIER=accurate|fast`; log the chosen path and record it in the
  benchmark matrix (CoreML vs CPU must *match* on accuracy, *differ* on speed).

Effect (1 h of audio): Mac ANE ~1 min · GPU box ~2 min · bare CPU via the ONNX
path ~20–25 min (still faster than real-time) — **same code, same accuracy.**

### Modal / GPU benchmark rule

Modal is an accelerator provider, not just a remote Linux host. A Modal GPU
benchmark is valid only when the selected STT and diarization providers are
CUDA-capable and verified to use the GPU. Running the CPU `sherpa-onnx` CLI in
an L4 container is a **CPU baseline on an expensive machine**, not a GPU
benchmark.

Required behavior:

- `BENCH_ACCELERATOR=cuda` / Modal default must select registered CUDA engines
  (`cuda-parakeet`, `cuda-pyannote` or their final names), backed by CUDA/NeMo or
  ONNX Runtime CUDA.
- If only CPU engines are configured (`sherpa-parakeet`, `sherpa-pyannote`), the
  Modal GPU benchmark must fail before launching or billing GPU time.
- The benchmark result must record `accelerator`, `engine`, `execution_provider`,
  `gpu_name`, and a sampled GPU-utilization/memory summary. A result with
  `gpu_memory_mb=0` or no CUDA EP is not accepted as a GPU run.
- CPU baselines are still useful, but they must be explicit
  (`BENCH_ACCELERATOR=cpu`) and reported separately from GPU results.
- Model cache is per accelerator: CUDA weights/runtime go into the Modal model
  Volume; CPU assets are a separate baseline cache. Do not silently reuse a CPU
  runtime because it happens to be installed.

This keeps the abstraction honest: automatic detection may fall back for normal
user transcription, but benchmarks must say exactly what path was measured.

### Model downloader (fetch-if-missing)

The binary ships **without** multi-GB weights; models are fetched on demand into
the data dir. This **extends the existing `noto speaker-model download`** pattern
(`internal/platform/providers/speaker/install.go` already downloads the ECAPA
model + the onnxruntime shared lib + ffmpeg, and skips what's present) into a
general **model manager**:

- **Lazy fetch-if-missing** on first transcribe (progress shown in the TUI), plus
  explicit `noto models download` / `noto models status`.
- **Backend-aware** — fetch the variant matching the detected accelerator + tier
  (CoreML package for ANE, ONNX FP16/INT8 for CPU/CUDA, the right quantization).
- **Verified + pinned** — a manifest of `model id → URL → checksum → license`, so
  fetches are integrity-checked (these are weights that touch audio), reproducible,
  and the **license is tracked** (ties to the licensing risk below).
- **Cached + reused** — resolved lazily into the data dir, shared across runs;
  fully offline after first fetch.
- **Graceful** — a clear prompt/error when a required model is missing and can't be
  fetched (unlike ECAPA, STT can't silently degrade to a placeholder mid-pipeline).

---

## Pipeline

```
            ┌─ mic           → "me/us"  (local_speaker) ─┐
 capture ───┤                                            ├─ keep SEPARATE
            └─ system audio  → "them"   (participants)  ─┘
                    │                         │
            (reference-based AEC + per-channel denoise)
                    │                         │
       me: VAD only (usually 1 speaker)  them: STT + diarization
                    │                         │     (anon Voice 1/2/3)
                    └────────── merge ────────┘
                                  │
              ECAPA embed each voice → match to profile store
               (cosine + robust aggregation; mint new on miss)
                                  │
                   LLM summarize (evidence-grounded, segment IDs)
                                  │
                   store (SQLite | Postgres) + FTS index
```

---

## Components — and why each

### 1. Dual-channel capture: mic = "me", system audio = "them"
**What:** record the microphone and system audio as **two separate channels**,
not a mixdown. Mic → `local_speaker`, system → `participants`.

**Why:** diarization is fundamentally "un-mix a smoothie back into fruit." The
cheapest way to win is to **not mix in the first place**. On a call, "me vs.
them" is *physically* separable — so we get a **0%-error us/them split for free**,
the me↔them overlap problem disappears, and the genuinely hard multi-speaker
diarization shrinks to **one stream** (the participants channel). Most of the
*damaging* errors (wrong owner on an action item) are me-vs-them confusions —
this kills them. (Falls back gracefully to single-channel for in-person
meetings.)

### 2. Echo cancellation + per-channel denoise
**What:** reference-based AEC (subtract the known system-audio signal out of the
mic) + denoise each channel; lean on Apple's `VoiceProcessingIO`.

**Why:** without headphones the mic picks up the speakers' output, re-polluting
the clean us/them split. We hold the reference signal (captured separately), so
AEC is high-quality. Cleaner audio → cleaner embeddings → better diarization
*and* ASR.

### 3. Local STT — Parakeet-TDT-0.6b-v3 (default)
**What:** Parakeet-TDT-0.6b-v3 as the workhorse (batch), Nemotron-3.5 ASR for
live captions, Whisper Turbo for languages outside the 25.

**Why:**
- **Native word + segment timestamps** (TDT architecture predicts token
  durations) — no WhisperX/forced-alignment step. Our `transcript.v1` schema
  needs segment timing for evidence-grounded quotes, so this matters.
- **Covers English *and* multilingual in one model** — 25 European languages
  (incl. Russian/Ukrainian) with auto language detect; English WER 1.93%
  (LibriSpeech clean). EU-friendly.
- **Tiny + fast**: RTFx ~3,300, 600M params, 2 GB RAM, runs on an 8 GB M1.
- **Commercial license** (CC-BY-4.0 — verify exact checkpoint).
- **Cost**: self-hosted batch ≈ **$0.006/hr** vs AssemblyAI's ~$0.19; on the
  user's machine, **$0**.
- *Caveat:* the offline checkpoint handles ~24 min full-attention / ~3 h
  local-attention per inference → use local-attention or chunk at silences.

### 4. Local diarization — its own stage
**What:** a diarization stage **separate from STT**, run only on the
*participants* channel. FluidAudio (pyannote, offline, ANE) for the authoritative
pass; Streaming Sortformer (≤4 speakers) for optional live labels.

**Why:** today diarization is a free side-effect of AssemblyAI; going local means
promoting it to a standalone seam. End-to-end neural diarizers (pyannote 3.x,
Sortformer) beat embedding-**clustering** specifically on **overlapping speech** —
the weak spot that matters in meetings. Expect **~10–15% DER** on real meeting
audio (low-teens is near state-of-the-art; >10% is the normal operating range,
not a failure). Most error is overlap-miss (backchannels), not confusion. We
finalize diarization in a fast **offline batch pass** at `stop` for best quality;
live labels are intentionally rougher.

### 5. Speaker identity — existing ECAPA-TDNN (no change)
**What:** keep the in-process **ECAPA-TDNN-512** (WeSpeaker ONNX, 192-d, CPU) +
the robust cross-meeting matching in `internal/core/speakers`.

**Why:** it's already local, CPU-only, and *mature*. Identity (matching a voice
to a stored profile) is a different job from diarization (splitting a meeting
into anonymous voices) — both use embeddings, but identity is the **matching**
half. The matching code already rejects diarizer-misassigned turns. Critically,
it supports the **hit-record-and-figure-it-out-on-the-fly** UX: speakers start
anonymous, voiceprints accumulate, and identity improves over time **without any
enrollment or pre-known participant list**.

### 6. Summarization — OpenRouter LLM (keep; local later)
**What:** keep the OpenRouter evidence-grounded summary pipeline; consider a
local LLM only later.

**Why:** LLM is the *minor* cost line (~$0.04/hr, or ~$0.02 with a Flash-Lite
model), and the multi-pass grounded pipeline benefits from a strong model. Not
worth the complexity of self-hosting first.

### 7. Storage / search — SQLite+FTS5 local, Postgres+S3 remote
**What:** local SQLite + FTS5; swap to Postgres + S3/R2 for the hosted tier.

**Why:** zero-config locally; the `ArtifactRepository` seam already exists for the
swap. R2's zero-egress pricing makes hosted audio/transcript storage a rounding
error.

---

## On-the-fly identity (no enrollment)

The flow that makes "just hit record" work with no setup:

1. Diarization splits the participants stream into anonymous **Voice 1/2/3**.
2. ECAPA embeds each voice's turns (robustly, rejecting outliers).
3. Each embedding is **matched** against the accumulated profile store (cosine):
   - high confidence → auto-link to a known person,
   - borderline → one-click confirm,
   - no match → **mint a new profile**.
4. Over time the store grows, so frequent collaborators are recognized
   automatically — but the very first meeting works knowing nobody.

We never need participants in advance. Known speaker counts / profiles, *when
available*, simply make it more accurate.

---

## Live vs. finalize

- **Live (optional, on-device):** streaming STT (Nemotron 3.5) + rough streaming
  diarization (Sortformer) for live captions during the meeting. Runs in a
  single-digit-% slice of an M-series chip (~2–3 GB RAM total).
- **Finalize (authoritative):** at `stop`, a batch pass re-transcribes /
  re-diarizes globally for best quality (~1s for a 1-h meeting at batch speeds),
  then summarizes. This is the transcript of record — especially for **action-item
  ownership**, the one place diarization accuracy really matters.

---

## Remote tier (offload + agent API)

Same `noto serve`, deployed on our infra:

- **Storage**: Postgres + S3/R2 (multi-tenant).
- **Auth**: SSH-key challenge or device-OAuth (Google link); token-gated.
- **Compute**: STT + diarization on **batch GPU** (serverless, scales to zero) —
  cheap because throughput is 25–3,000× real time; only pay per job.
- **Agent API**: agents query meetings/transcripts/summaries/search over HTTP.
- Local capture stays on the client; only finished audio uploads.

### Unit economics (why local matters)
- Cloud marginal COGS ≈ **$0.23/hr** (≈80% STT).
- Self-hosted batch STT ≈ **$0.006/hr**; on the user's device, **$0**.
- Tiers: **free/local** (BYO compute, $0 to us), **$5 BYO-key sync** (~90%
  margin), **$15/mo hosted** w/ a cap + overage (~60–85% margin depending on
  local vs self-hosted STT), **business license** for self-hosted commercial use
  (~95% margin, no COGS).

---

## Gap vs. current architecture

Grounded in a code check. **~70% of this already exists in the architecture's
bones; the real new work is the local compute path.**

| Area | Current (in code) | Gap |
|---|---|---|
| Channel roles (mic→local_speaker, system→participants) | already in `cmd/capture` + schema | ✅ designed in |
| System-audio capture | `main.swift` uses AVAudioEngine; notes ScreenCaptureKit "ideal" — appears stubbed; falls back to mic-only | ⚠️ **finish it** |
| On-the-fly identity / accumulating profiles | ECAPA-512 local + `core/speakers/robust.go` + mint/confirm | ✅ mature, matches |
| STT | **AssemblyAI only**; explicit "no local/offline STT" constraint | ❌ **biggest delta** |
| Diarization | bundled in AssemblyAI (`speaker_labels=true`); a `diarize` capability, no standalone seam | ❌ **new stage + local impl** |
| AEC / denoise | none | ❌ new |
| Per-channel pipeline | channels modeled but one audio path to AssemblyAI | ⚠️ pipeline change |
| Live transcription | batch only (record→stop→job) | ⚠️ additive |
| Remote/offload mode | `notoapi.Client`: in-process / UDS / remote TCP+bearer; `host.Connect()` | ✅ already there |
| Repo swap (Postgres+S3) | `ArtifactRepository` seam, "swap without service changes" | ✅ seam ready |
| Agent API | `/v1/agent/meetings`, `/v1/agent/meetings/{id}`, `/v1/search` | ✅ already there |
| Provider seams | `STTProvider` / `SummaryProvider` / `SpeakerEmbedder` + registry | ✅ ready for impls |
| Multi-tenancy / auth / billing | single-tenant; bearer plumbing exists | ❌ additive |

**Already matches:** remote/offload deployment, agent query API, on-the-fly
identity, the channel-role model, and the provider seams. **The distance is one
cluster:** local STT, local diarization as a standalone per-channel stage,
finishing system-audio capture, and AEC. The hosted multi-tenancy is additive
plumbing.

---

## Suggested build order

1. **Finish ScreenCaptureKit system-audio capture; keep channels separate.**
   Unlocks the us-vs-them win on what's already modeled.
2. **Backend autodetect + model downloader** — runtime EP selection
   (CoreML / CUDA / CPU, with fallback) and a fetch-if-missing model manager
   (extends `speaker-model download`). Underpins steps 3–4 on every platform.
3. **Add a local diarization stage** (new seam + FluidAudio on Mac / sherpa-onnx
   or pyannote on Linux), run on the participants channel → feeds existing ECAPA
   matching.
4. **Add a local `STTProvider`** (Parakeet-TDT-0.6b-v3, via CoreML on Mac /
   ONNX on Linux-CPU/CUDA) → flips "AssemblyAI-only" to "local default, cloud
   optional."
5. **AEC + per-channel denoise** pre-processing.
6. *(Later)* live on-device streaming; hosted multi-tenancy / auth / billing.

---

## Open questions / risks

- **Model licenses** — confirm the exact Parakeet/diarization checkpoints are
  commercially usable (CC-BY-4.0 / MIT / Apache); some NVIDIA checkpoints have
  shipped CC-BY-NC.
- **System-audio capture completeness** — verify how much of the ScreenCaptureKit
  path actually works today vs. the AVAudioEngine stub.
- **Meeting DER** — overlap-heavy meetings sit ~15–25% DER; lean on the
  channel split + finalize pass + participant constraints to keep
  *owner-attribution* tight.
- **AEC quality** without headphones; reference-based should help.
- **Real-time diarization maturity** — Sortformer is ≤4 speakers and "open
  research"; keep live labels rough, finalize offline.
- **Per-language word-timestamp coverage** for the Whisper long-tail fallback
  (forced-alignment models exist mainly for major languages).

---

## References (key sources)

- Parakeet-TDT-0.6b-v3 model card (KPIs, 25 languages, native timestamps) —
  huggingface.co/nvidia/parakeet-tdt-0.6b-v3
- FluidAudio (Swift/CoreML ASR + diarization on ANE) —
  github.com/FluidInference/FluidAudio
- Modal batch transcription cost (~$0.006/audio-hr) —
  modal.com/blog/fast-cheap-batch-transcription
- Streaming Sortformer (online diarization) — arxiv.org/pdf/2507.18446
- Nemotron 3.5 ASR streaming (40 locales, cache-aware) —
  huggingface.co/blog/nvidia/nemotron-speech-asr-scaling-voice-agents
- ScreenCaptureKit system + mic audio (macOS 15+, no built-in AEC) —
  recall.ai/blog/how-to-access-to-system-audio

---

## Benchmarks & evaluation

> These are **reported / leaderboard figures**, each measured under its own
> conditions — treat cross-row comparisons as indicative, **not** apples-to-apples
> (scoring collars, overlap handling, and test splits differ, especially for DER).
> Lower is better for WER / DER / EER; higher is better for RTFx.

### Part 1 — Official benchmarks for our model choices

**Speech-to-text** (WER %, RTFx) — HF Open ASR Leaderboard / model cards

| Model | Role | WER | RTFx | Note |
|---|---|---|---|---|
| **Parakeet-TDT-0.6b-v3** | local default (EN + 25 EU) | 6.34 avg · EN 1.93 (LibriSpeech-clean) / 4.85 (FLEURS) · ES 3.45 · IT 3.00 · DE 5.04 · FR 5.15 | **3,332** | native word timestamps |
| Parakeet-TDT-1.1B | EN max-accuracy | ~4.4 (podcast set) | >2,000 | English only |
| Parakeet-CTC-1.1B | fastest EN | 6.68 | 2,794 | English only |
| Canary-Qwen-2.5B | best open EN | **5.63** | 418 | English only |
| Canary-1b-v2 | EU accuracy | tops multilingual leaderboard | high | 25 EU + translation |
| Whisper large-v3-turbo | long-tail langs | 7.75 | 216 | 99 languages |
| Whisper large-v3 | long-tail accuracy | 7.4 (2.1 LibriSpeech-clean) | 68 | 99 languages |
| Nemotron-3.5-ASR (streaming) | live captions | streaming | 240–2,400 streams/GPU | 40 locales; 80 ms–1.12 s chunk |

**Diarization** (DER %, lower better) — *with/without-overlap scoring varies; read the conditions*

| Model | DER (dataset) | Speed | Note |
|---|---|---|---|
| pyannoteAI (commercial) | 11.2 (study avg) | — | reference ceiling |
| pyannote 3.1 (open) | ~10–15 (dataset-dep.) | ~40× RT (GPU) | the open default baseline |
| DiariZen | 13.3 avg · 5.2 VoxConverse | — | strong open |
| Sortformer v2 (streaming) | 7.0 ALI · 13.3 CALLHOME · 19.0 DIHARD | RTF 214× | ≤4 speakers; real-time capable |
| FluidAudio (pyannote community-1, on-device) | ~15 | 122× RT (M-series) | Mac CoreML — our local pick |
| EEND-TA | 15.3 CALLHOME · 14.5 DIHARD III | — | end-to-end baseline |

**Identity / speaker verification** (VoxCeleb1-O: EER %, minDCF; lower better)

| Model | EER | minDCF | Note |
|---|---|---|---|
| ECAPA-TDNN (original) | 0.87 | 0.107 | reference |
| WeSpeaker ECAPA c1024 | 0.728 | 0.099 | + LMFT/AS-Norm; **noto runs the c512 VoxCeleb variant (≈1% range)** |
| TitaNet-Large | 0.66–0.68 | 0.087 | higher-accuracy alt (the remote-embedder option) |

### Part 2 — Datasets to evaluate the pipeline (end-to-end + per-stage)

**Metrics:** **WER** (transcription), **DER** (diarization), **EER** (identity
matching), and for the full "who-said-what" pipeline **cpWER** — concatenated
minimum-permutation WER, the joint transcription + speaker-attribution metric used
in CHiME (via the `meeteval` toolkit) — plus **SA-WER**, a stricter variant that
enforces the correct speaker label.

**The standout dataset: AMI.** It ships per-speaker **headset mics (IHM)** *and* a
**single distant mic (SDM)** of the same meetings — so it directly tests our
**channel-separation thesis**: compare cpWER on IHM (≈ our separated mic/system
channels) vs SDM (≈ a mixdown) and quantify what the split buys. It also has the
**same speakers recurring across sessions** — the only realistic public proxy for
our **cross-meeting identity** task.

| Dataset | Stage(s) | Relevance | Why |
|---|---|---|---|
| **AMI Meeting Corpus** | ASR + diar + identity + **e2e (cpWER)** | ⭐⭐⭐ | meetings, 4 spk, overlap; IHM vs SDM = channel-split test; recurring speakers = identity test |
| **CHiME-8 DASR** | **e2e (cpWER)** | ⭐⭐ | far-field, overlap, official multi-talker challenge — hardest realistic |
| **ICSI** | ASR + diar + e2e | ⭐⭐ | real meetings, more speakers |
| **VoxConverse** | diarization | ⭐⭐ | in-the-wild, overlap, rapid turn-taking |
| **CALLHOME** | diarization | ⭐⭐ | standard 2–7 spk, multilingual |
| **DIHARD III** | diarization | ⭐⭐ | hard/diverse domains — stress test |
| **VoxPopuli** | ASR (multilingual) | ⭐⭐⭐ | European Parliament, 23 EU langs — matches our 25-lang target |
| **FLEURS** | ASR (multilingual) | ⭐⭐ | per-language WER across our language set |
| **Earnings-22** | ASR (long-form) | ⭐⭐ | long, real-world, accented business audio |
| **VoxCeleb1 (O/E/H)** | identity (EER) | ⭐⭐⭐ | the speaker-verification benchmark for the embedder |
| **CN-Celeb / SITW** | identity robustness | ⭐ | cross-genre / in-the-wild stress |
| **LibriSpeech / TED-LIUM 3** | ASR baseline | ⭐ | clean read/talk — sanity baseline, not meeting-like |

**What each dataset actually ships (where it's only *partial* coverage).** Most
public sets cover **one** stage; only the meeting corpora carry transcript +
speaker + timing together, which is what a true end-to-end (cpWER) test needs.

| Dataset | Transcript (WER) | Speaker turns (DER) | Timestamps | Identity / recurring | Separate channels | Langs | Covers |
|---|---|---|---|---|---|---|---|
| **AMI** | ✓ | ✓ | ✓ word | ✓ recurring across sessions | ✓ **IHM / SDM / MDM** | EN | **full e2e** + channel-split + identity proxy |
| **ICSI** | ✓ | ✓ | ✓ | ✓ recurring | ✓ close + distant | EN | full e2e |
| **CHiME-8 DASR** | ✓ | ✓ | ✓ | – | ✓ far-field arrays (no clean per-spk) | EN(+) | e2e (cpWER); no clean channels |
| VoxConverse | – | ✓ | ✓ | – | – | multi | **diar only** |
| CALLHOME | (exists, rarely used) | ✓ | ✓ | – | 2-ch phone | 6 | **diar only** (multiling) |
| DIHARD III | – | ✓ | ✓ | – | – | multi | **diar only** (hard) |
| VoxPopuli | ✓ | segmented single-spk | ✓ | partial (speaker meta) | – | 23 EU | **ASR only** (multiling) |
| FLEURS | ✓ | – | – | – | – | 102 | **ASR only** (read) |
| Earnings-21 / 22 | ✓ | 21: ✓ / 22: – | ✓ | – | – | EN / accents | ASR (21 = partial e2e) |
| VoxCeleb1 (O/E/H) | – | – | – | ✓ verification pairs | – | multi | **identity only** (EER) |
| CN-Celeb / SITW | – | – | – | ✓ pairs | – | ZH / multi | **identity only** |
| LibriSpeech / TED-LIUM 3 | ✓ | single-spk | ✓ | spk IDs avail | – | EN | ASR baseline |

**Coverage gaps to plan around:**
- **Cross-meeting identity** (our actual job — matching diarized speakers to
  stored profiles across sessions) has **no perfect public benchmark.** Best
  options: AMI / ICSI **recurring speakers**, or **construct** an enroll/test
  split from VoxCeleb (enroll N utterances per speaker, score the rest).
- **Channel separation** is only directly testable where **separate per-speaker
  channels exist** → **AMI IHM vs SDM** (and ICSI close vs distant). Nowhere else.
- **Multilingual ASR** is covered by FLEURS / VoxPopuli, but those are
  **single-speaker** — they can't test diarization or e2e, only WER per language.
- So **assemble coverage:** meeting corpora (AMI/ICSI/CHiME) for e2e + channels,
  the specialized sets (VoxConverse/DIHARD for diar, FLEURS/VoxPopuli for langs,
  VoxCeleb for identity) to stress each stage in isolation.

**Size & languages** (how much there is to run, and what tongues it covers):

| Dataset | Total hrs | Recordings | Avg / recording | Languages |
|---|---|---|---|---|
| **AMI** | ~100 | ~171 meetings | ~35 min | English |
| **ICSI** | ~72 | 75 meetings | ~58 min | English (many non-native) |
| **CHiME-6 / DASR** | ~50 | 20 dinner parties | ~2–2.5 h | English |
| VoxConverse | 63.8 | 526 videos | ~7 min | multilingual (in-the-wild) |
| CALLHOME | ~99 | 500 calls | ~12 min | 5 (EN, ZH, JA, DE, ES) |
| DIHARD III | ~50+ | 100s of clips, 11 domains | ~5–10 min | English-heavy, mixed |
| VoxPopuli | ~1,800 (transcribed) | EU-Parliament sessions (segmented) | short segments | **23 EU** |
| FLEURS | ~1,400 (~12/lang) | read sentences | ~seconds | **102** |
| Earnings-22 | 119 | 125 calls | ~57 min | English + global accents |
| Earnings-21 | 39 | 44 calls | ~53 min | English |
| VoxCeleb1 | 352 | 153,516 utts / 1,251 spk | ~8 s | multilingual (celebrity) |
| VoxCeleb2 | 2,442 | 1,128,246 utts / 6,112 spk | ~8 s | multilingual |
| CN-Celeb1 | ~274 | ~130k utts / 1,000 spk | short | Chinese (multi-genre) |
| LibriSpeech | 960 (test-clean 5.4) | utterances | ~12 s | English |
| TED-LIUM 3 | 452 | 2,351 talks | ~11 min | English |

Two practical takeaways:
- **Running these is cheap.** The meeting corpora are *small* (AMI ~100 h / 171
  meetings; a common test split is ~10 meetings / ~9 h). At Parakeet batch speeds
  (~3,000× RT) all of AMI transcribes in ~2 min on a GPU, ~40 min on an M1 — so we
  can re-run the e2e gate on every change. The identity/ASR sets are large
  (hundreds–thousands of hours); **sample a subset** rather than running them whole.
- **Language coverage is lopsided — flag it.** Our **multilingual ASR** is
  well-covered (FLEURS 102, VoxPopuli 23 EU, CALLHOME's DE/ES), but the **meeting
  corpora (AMI/ICSI/CHiME) are English-only** — so multilingual *diarization* and
  *e2e (cpWER)* are **not** publicly testable for our 25-EU target. To validate the
  non-English pipeline end-to-end we'd need to **build a small in-house EU-language
  meeting set** (or accept English-only e2e + per-language WER as the proxy).

**Test matrix — so each swap's effect is attributable:**

| Change | Measure | On |
|---|---|---|
| Swap STT model (Parakeet ↔ Whisper ↔ Canary) | WER | AMI-IHM, VoxPopuli, FLEURS, Earnings-22 |
| Swap diarizer (pyannote ↔ Sortformer ↔ FluidAudio) | DER | AMI, CALLHOME, DIHARD, VoxConverse |
| Swap/upgrade embedder (ECAPA ↔ TitaNet) | EER + cross-session match accuracy | VoxCeleb1; recurring speakers in AMI/ICSI |
| **Channel separation on/off** | **cpWER** | **AMI IHM vs SDM** |
| AEC / denoise on/off | WER + DER | AMI-SDM, CHiME-8 (far-field) |
| Live (streaming) vs finalize (batch) | DER + cpWER | AMI, CALLHOME |
| Full-pipeline regression gate | **cpWER + SA-WER** | AMI, CHiME-8 |

Pin a baseline on AMI (cpWER) + VoxCeleb1 (EER) and re-run on every model or
processing-step change; the delta tells us whether a swap helped or hurt, and
the per-stage rows isolate *where*. This is also how we validate the headline
bets — channel separation, the finalize pass, accumulating profiles — against
published baselines rather than by feel.

### Benchmark sources

- Open ASR Leaderboard — huggingface.co/spaces/hf-audio/open_asr_leaderboard
- Benchmarking Diarization Models (2026) — arxiv.org/abs/2509.26177
- pyannoteAI benchmark — pyannote.ai/benchmark
- ECAPA-TDNN paper / WeSpeaker — arxiv.org/abs/2005.07143 · github.com/wenet-e2e/wespeaker
- TitaNet — arxiv.org/abs/2110.04410
- cpWER / meeteval (CHiME) — multi-talker ASR survey arxiv.org/abs/2505.10975
