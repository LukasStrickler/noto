# ADR 0003: Storage lives behind the ArtifactRepository seam

- **Status:** Accepted
- **Date:** 2026-05-20

## Context

For local-first use, SQLite + the filesystem are the right store: zero-config, durable, no
server. But "meeting memory you control" should eventually scale to a shared backend (Postgres +
object storage) without rewriting business logic. If service code reached into SQLite and file
paths directly, that migration would touch every method, and unit tests would need real disk I/O.

## Decision

All persistence goes through one interface, `repo.ArtifactRepository` (`internal/platform/repo`).
Service and transport code import **only** that interface, never the concrete
`internal/platform/storage` package. The shipped implementation is `LocalArtifactRepository`
(SQLite + filesystem); a Postgres/S3 backend is a second implementation injected via
`service.Deps.Repo`, with no service or handler changes.

## Alternatives considered

- **Direct SQLite/filesystem calls in services** — simplest today, but welds the app to one
  backend and makes fast unit tests impossible. Rejected.
- **A full ORM / repository-per-entity layer** — more machinery than a single-binary app needs;
  one artifact-shaped interface keeps the surface small.

## Consequences

- Swapping the backend is one package, behind a stable contract.
- Service-layer logic is unit-testable against `testutil.FakeRepo` — in-memory, no disk, no
  goroutines.
- The seam is enforceable: a test asserts `internal/app/service` does not import
  `internal/platform/storage`.
- Cost: the interface is the lowest common denominator. Backend-specific features (e.g. SQL
  joins) must be expressed in its terms or the seam leaks.

## Verification

`internal/platform/repo` (interface + `LocalArtifactRepository`), `internal/platform/storage`
(the detail it hides), `internal/testutil` (`FakeRepo`). Import-boundary and repo tests live in
those packages.
