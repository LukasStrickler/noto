# internal/core — domain layer (leaves)

Pure domain types and logic. These are the leaves of the dependency graph:
the universal exchange types every other layer speaks in.

**Dependency rule:** may import only the standard library and other `core`
packages. **MUST NOT** import `platform`, `app`, `transport`, or `ui` — if a
type in here needs disk, SQL, HTTP, or a provider, the dependency is pointing
the wrong way and the logic belongs in `platform`/`app` instead.

**Packages**

| Package | Responsibility |
|---------|----------------|
| `artifacts` | Typed artifacts (meeting, audio, transcript, summary); each implements `Artifact` with `Kind()`/`Version()`/`Validate()` |
| `speakers` | Speaker-matching domain logic |
| `notoerr` | Structured, typed errors (`notoerr.Error`) — used everywhere instead of bare `errors.New` |

**Anti-pattern:** no I/O, no goroutines, no logging. Keep these deterministic
and trivially unit-testable. See the repo-root `AGENTS.md` for the big picture.
