# CLI Reference

## JSON Contract

Commands with `--json` write machine-readable JSON to stdout and human-readable errors to stderr. Error responses should use this shape:

```json
{
  "error": {
    "code": "not_found",
    "message": "Meeting not found.",
    "details": {}
  }
}
```

All JSON commands used by agents must be covered by golden tests or schema tests. See [testing.md](./testing.md) for the validation gates.

## TUI

```text
noto
noto status --json
```

## Recording

```text
noto record --title "Roadmap sync"
noto stop
noto status --json
```

## Artifacts

```text
noto import-audio ./sample.m4a --title "Sample"
noto import-transcript ./sample.transcript.json --title "Sample"
noto transcribe <meeting_id> --provider <provider_id>
noto summarize <meeting_id>
noto index rebuild --json
noto list --json --limit 20
noto show <meeting_id>
noto transcript --json <meeting_id>
noto summary --json <meeting_id>
noto actions --json <meeting_id>
noto files --json <meeting_id>
```

Commands that return meeting JSON should include local artifact paths when
available so scripts and agents can inspect files directly.

## Validation

```text
noto verify --json
noto verify --json <meeting_id>
noto status --json
noto files --json <meeting_id>
noto index rebuild --json
```

`noto verify --json` is the primary agentic health check. It validates schemas,
checksums, artifact paths, raw-audio retention state, source-role presence when
expected, and index freshness. It must return stable machine-readable failure
codes for missing files, schema failures, checksum mismatches, invalid source
roles, and stale indexes.

## Search

```text
noto search --json "pricing decision"
```

Search results include meeting ID, segment ID, speaker, timestamp, text, source
role when known, and score.

## Post-V1 Storage And Workspace

Storage sync and remote workspaces are reserved for later phases. Their command
shape should be defined when [storage-sync.md](./storage-sync.md) moves from
architecture reference to implementation work.

## Providers

```text
noto providers list --json
noto providers set-default transcription <provider>
noto providers set-default summary <provider>
```

## Benchmarks

```text
noto benchmark run --dataset ami --sample ES2004a \
  --provider assemblyai:universal-3-pro --json
noto benchmark compare --run-a <run_id> --run-b <run_id> --json
noto benchmark report <run_id>
```

Benchmark JSON writes `benchmark-result.v1` and should include metrics,
processor metadata, schema validation state, and cost when known.

## Related

- [Agent interface](./agent-interface.md)
- [Artifact reference](./artifacts.md)
- [Testing and validation](./testing.md)
