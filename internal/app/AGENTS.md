# internal/app — application core + composition root

The hub. `service` orchestrates everything; `host` wires the concrete
dependencies together and owns the process lifecycle.

**Dependency rule:** `service` may import `core`, `platform`, and the
`transport/notoapi` + `transport/appsocket` clients — but **NOT**
`transport/server` or `transport/apiclient` (delivery depends on `service`, not
the reverse), and never `ui`. `host` is the composition root and the deliberate
exception: it wires `service` together with `transport/server`,
`transport/apiclient`, and `transport/appsocket` to stand up the process.

## Packages

- `service` — CORE ORCHESTRATOR. Every HTTP handler and CLI command funnels
  through `Service`; it owns recording state + job lifecycle and composes
  storage/search/registry/secrets/IPC/event-hub. See its own `AGENTS.md` for
  the per-file map.
- `host` — composition root: decides remote vs. local-daemon vs. in-process,
  opens the shared `noto.sqlite` once, and starts `service` + `server`
  (`Start`/`Connect`)

**Anti-pattern:** `service` reaches storage **only** through the
`repo.ArtifactRepository` interface — never `platform/storage` directly — and
never blocks its goroutine (long work runs in background job goroutines). See
the repo-root `AGENTS.md` for the big picture.
