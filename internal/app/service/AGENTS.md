# internal/app/service — the orchestrator hub

`Service` is the one type every HTTP handler and CLI command funnels through.
It owns recording state (mutex-protected), the job lifecycle (`jobCancels`
map), and composes storage/search/registry/secrets/IPC/event-hub. This is the
densest package in the repo — the map below is how it's split.

## Where things live

- `service.go` — the `Service` struct, its dependencies, and the constructor
  that wires them. **Start here.**
- `meetings.go` — meeting list / get / delete read+write
- `recording.go` — recording state machine (start/stop/pause/resume/marker/preflight)
- `import.go` — audio import → meeting + pipeline job

Job system (split by runtime stage):
- `jobs.go` — queue CRUD + event streaming (`CreateJob`…`StreamEvents`, `scanJob`)
- `jobs_worker.go` — worker runtime (`startWorkers`, `workerLoop`, `claimNextJob`, `runJob`)
- `jobs_pipeline.go` — pipeline stages (`runIngest`/`Transcribe`/`Summarize`/`Index`/`Verify`, `matchSpeakers`)

Supporting concerns:
- `search.go` — search delegation to the FTS index
- `storage.go` — artifact R/W through `repo`, plus verify/reindex
- `providers.go` — provider list / key-set / test / active-selection
- `config.go` — config get/patch + paths
- `agent.go` — agent-handoff endpoint logic
- `events.go` — in-process `eventHub` that broadcasts job progress to SSE + TUI
- `speaker_profiles.go` — speaker profile CRUD + merge
- `speaker_embedder.go` — speaker embedding pipeline
- `speaker_suggest.go` — match-suggestion ranking for the speakers UI
- `seed.go` / `seed_speakers.go` — dev fixture seeding (used by `noto seed`)
- `util.go` — small shared helpers

## Local rules

- Reach storage **only** through the `repo.ArtifactRepository` interface — never
  `platform/storage` directly.
- **Never block the Service goroutine** — all long work (transcribe, summarize,
  index) runs in background job goroutines, cancellable via the `jobCancels` map.
- Return `notoerr.*` errors; don't log to stdout/stderr here.

See `internal/app/AGENTS.md` for the layer rule and the repo-root `AGENTS.md`
for the big picture.
