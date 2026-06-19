# ADR 0001: Backend owns all state; the TUI is a thin client

- **Status:** Accepted
- **Date:** 2026-05-20

## Context

noto has two consumers of meeting data — a human at a terminal TUI and an LLM agent driving the
CLI/HTTP — and a pipeline (transcribe, summarize, index, speaker-match) that calls paid external
providers and writes durable artifacts. If the TUI held its own copy of meeting state, the two
consumers could diverge, processing would be coupled to a running UI, and a future remote
deployment would mean re-implementing the data layer on the client.

## Decision

A single backend (`noto serve`, implemented by `internal/app/service.Service`) owns **all**
storage, job orchestration, provider calls, search, and speaker identity. The TUI and CLI hold
no durable state; they read and mutate exclusively through the `notoapi.Client` interface and
re-render from what the backend returns, with live updates pushed over SSE.

## Alternatives considered

- **Fat client with a sync layer** — TUI keeps a local DB and syncs to a server. Rejected: two
  sources of truth, conflict resolution, and a much larger client. The whole point is that the
  terminal is a *view*, not a database.
- **No backend process at all** (library linked into the TUI) — rejected because it rules out
  the remote/daemon deployments in [ADR-0002](0002-three-deployment-modes-one-client.md) and
  couples long-running jobs to the UI's lifetime.

## Consequences

- The TUI stays small and stateless; the same code path serves a local or remote backend.
- Processing survives the UI closing — jobs run in the backend's queue.
- Everything an agent can do, a human can do, because both go through the same service surface.
- Cost: every read is an API call. The HTTP client mitigates this with a stale-while-revalidate
  cache refreshed by SSE, but the backend remains the only authority.

## Verification

`internal/app/service` is the orchestrator; `internal/app/host` wires it and hands callers a
`notoapi.Client`. The TUI (`internal/ui/tui`) and CLI (`internal/ui/cli`) depend only on that
interface. `internal/app/host/conformance_test.go` exercises a client against the service.
