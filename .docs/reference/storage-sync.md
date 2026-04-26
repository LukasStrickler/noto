# Sync Gateway Reference

This is a post-V1 reference. V1 is terminal TUI/CLI plus local recording, artifacts, transcription, summaries, and search. It does not implement storage sync.

## Gateway Modes

| Mode | Implementation | Credential on client | Artifact path |
| --- | --- | --- |
| Local | In-process pass-through | None | `~/Noto` |
| Local + object store | In-process pass-through | Scoped R2/S3 key | `noto/v1/workspaces/{workspace_id}/...` |
| Remote | Noto API | Revocable device token | API-issued signed URLs |

The client uses one workflow and one `SyncGateway` port. A config switch selects the implementation.

## Gateway Contract

| Method | Purpose |
| --- | --- |
| `GetPolicy` | Return retention, provider, upload, and sync policy. |
| `ListManifests` | Return manifests changed since a cursor. |
| `CreateObjectAccess` | Return write/read access for exact artifact keys. |
| `CommitManifest` | Atomically accept, reject, or mark a manifest conflict. |
| `SubmitJob` | Submit a transcription, summary, sync, or verification job. |
| `GetJob` | Return job state. |
| `ReportEvent` | Record audit or diagnostic events. |

All implementations use the same method semantics. Unsupported features return typed `unsupported_capability` errors.

## Manifest Commit Semantics

| Step | Requirement |
| --- | --- |
| Read current | Gateway reads the current manifest and version checksum. |
| Compare | Candidate must name the expected current version or declare first commit. |
| Write immutable files | Version files are written before manifest promotion. |
| Promote | Manifest pointer changes only after checksums validate. |
| Conflict | Competing current version keeps both versions and returns `artifact_conflict`. |

## Remote Layout

```text
noto/v1/workspaces/{workspace_id}/
  meetings/YYYY/MM/{meeting_id}/
    manifest.json
    versions/{version_id}/
      meeting.json
      audio.json
      transcript.diarized.json
      transcript.md
      summary.json
      summary.md
      checksums.json
  indexes/by-start-date/YYYY-MM-DD/{meeting_id}.json
```

## Flows

| Flow | Steps |
| --- | --- |
| Push | Validate schemas/checksums, request object access, write immutable files, commit manifest, repair index metadata. |
| Pull | List changed manifests, request object access, read missing versions, validate schemas/checksums, rebuild SQLite FTS5. |

## Local Pass-Through Rules

- Does not require a daemon, HTTP server, or polling loop while idle.
- Uses local filesystem writes or owner-provided R2/S3 credentials.
- Applies the same manifest and conflict rules as remote mode.
- Keeps API-shaped behavior behind function calls, not HTTP calls.

## Remote API Rules

- Uses revocable device tokens.
- Returns signed object access for large artifact transfer.
- Stores policy, metadata, audit, and billing state.
- Runs hosted provider jobs asynchronously.
- Avoids buffering audio or artifacts in request handlers.

## Conflict Rules

- Keep both versions.
- Show conflict in TUI and `noto storage status --json`.
- User chooses `use-local`, `use-remote`, or `keep-both`.
- Rebuild indexes after resolution.

## Test Rules

Storage sync is post-V1, but it must preserve the V1 validation model:

- Local and remote adapters run the same gateway contract tests.
- Pull validates schemas and checksums before updating the current manifest.
- Conflict fixtures cover local-wins, remote-wins, and keep-both outcomes.
- `noto storage status --json` reports machine-readable conflict and validation state.

See [testing.md](./testing.md) for the V1 validation model that post-V1 sync must preserve.

## Credential Rules

- Local object-store mode stores scoped storage credentials in Keychain or environment variables.
- Remote mode stores a revocable device token in Keychain.
- Remote mode must not store R2/S3 write credentials on the client.
- Signed URLs are bearer tokens and should be short-lived.

## References

- [Cloudflare R2 consistency](https://developers.cloudflare.com/r2/reference/consistency/)
- [Cloudflare R2 S3 compatibility](https://developers.cloudflare.com/r2/api/s3/api/)
- [Cloudflare R2 presigned URLs](https://developers.cloudflare.com/r2/api/s3/presigned-urls/)
