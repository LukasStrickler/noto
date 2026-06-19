# `benchmark/e2e/` — chained pipeline benchmark

The CHAINED layer: the full local pipeline run with **real handoffs**, scored
end-to-end. Where the atomic benches feed each stage golden inputs, here each
stage gets the previous stage's actual output, so the numbers show **compounding
error** and total who-said-what quality.

```
LocalSTT (words) ─┐
                  ├─► merge.Attribute ─► attributed transcript ─► cpWER / SA-WER
LocalDiarizer ────┘     (word → turn)
   (turns)
```

- **`chain.go`** — `RunChain` wires `LocalSTT → LocalDiarizer → merge`, timing
  each stage (`StageTiming.RTF` gives per-stage + total real-time factors).
- **`oracle.go`** — `OracleSTT` / `OracleDiarizer` replay ground truth through the
  real engine seams. They are the *second working runtime* behind the engine
  abstraction (proving a runtime is genuinely swappable) and give the chain a
  deterministic perfect provider, so the wiring and scorers run with **no models**.
  Feed an oracle perturbed input to simulate a stage's error.
- **`baseline.json` + `baseline.go`** — the pinned per-stage metrics and the
  regression gate.

## What the scorers distinguish (see `e2e_test.go`)

| Injected error | cpWER | SA-WER | Why |
|----------------|-------|--------|-----|
| none (perfect) | 0 | 0 | — |
| diarizer label swap | 0 | 1.0 | cpWER permutes labels; SA-WER's fixed mapping catches mis-attribution |
| diarizer boundary early | 0.5 | — | a correctly-recognized word lands on the wrong speaker — **compounding** |
| one substituted word | 0.25 | — | the STT stage's own error, seen through the chain |

## Regression gate

`TestBaselineRegression` (gated `BENCH_DEEP=1`) loads `baseline.json`, runs the
deterministic pipeline, logs an accuracy delta table, and **fails on any
regression** beyond each metric's tolerance.

```bash
BENCH_DEEP=1 go test ./benchmark/e2e/ -run TestBaselineRegression -v              # check
BENCH_DEEP=1 BENCH_REPIN=1 go test ./benchmark/e2e/ -run TestBaselineRegression   # re-pin
```

Each `BENCH_DEEP=1` run appends to `runs.jsonl` (gitignored) and prints a trend
table vs the previous run — "are we improving over time", distinct from the fixed
`baseline.json` gate.

`TestE2EAMI` honors `-hours=n` (and `-seed=n` for a reproducible draw) to run only
~`n` hours of meetings for a quick check — pass them **after** the package path,
e.g. `go test ./benchmark/e2e/ -run TestE2EAMI -v -hours=1 -seed=3`, or use
`make bench HOURS=1 SEED=3`. See the top-level
[`benchmark/README.md`](../README.md#quick-runs-by-audio-budget---hours--seed).

The committed baseline is currently a **placeholder** pinned from the oracle
(perfect-replay) pipeline — all zeros — so the gate guards the harness itself
(metrics/merge/loaders must keep scoring a perfect chain at 0). Re-pin with real
numbers (and update `runtime`/`compute`) once a local engine is wired; that is
the loop the harness exists for — swap a model, run the gate, read the
accuracy+speed delta vs the pinned CPU baseline.
