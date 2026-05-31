# internal/platform — infrastructure adapters

Everything that touches the outside world: disk, SQLite, the network, and AI
providers. Each package adapts one external concern to a `core` type.

**Dependency rule:** may import `core`, the standard library, and third-party
deps. **MUST NOT** import `app`, `transport`, or `ui`. Business orchestration
(job lifecycle, recording state) does **not** live here — it lives in
`app/service`.

**Packages**

| Package | Responsibility |
|---------|----------------|
| `config` | viper-based config (dirs, providers, models); defaults in `defaults.go` |
| `secrets` | Keychain (darwin) / file (linux) credential store |
| `db` | The single SQLite connection opener (DSN pragmas, `Migrate`) |
| `storage` | File-based artifact persistence (used **only** by `repo`) |
| `repo` | `ArtifactRepository` interface + `LocalArtifactRepository` — the storage seam |
| `search` | SQLite FTS5 index + query parser |
| `speakerstore` | Speaker profile + meeting-mapping repos (SQLite) |
| `providers` | STT + LLM provider registry + routers (`stt/`, `speech/`, `llm/` + `llm/prompts/`) |

**Anti-pattern:** don't duplicate the SQLite open/pragma logic — go through
`db.Open`. `storage` is an implementation detail of `repo`; nothing outside
`repo` should import it. See the repo-root `AGENTS.md` for the big picture.
