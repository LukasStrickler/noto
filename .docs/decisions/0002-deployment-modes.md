# ADR 0002: Sync Gateway Modes

Date: 2026-04-24

## Status

Accepted for planning.

Post-V1. V1 is terminal TUI/CLI plus local recording, artifacts, transcription, summaries, and search. It does not implement deployment modes beyond local filesystem storage.

## Context

Noto should work locally, with owner-provided object storage, as hosted SaaS, and as self-hosted infrastructure without separate sync/control implementations.

## Decision

Use one `SyncGateway` port with local and remote adapters. A config switch selects the adapter; app code calls the same methods in every mode.

| Mode | Gateway implementation | Storage | Provider keys |
| --- | --- | --- | --- |
| Local | In-process pass-through | `~/Noto` | User/local |
| Local + object store | In-process pass-through | Owner-provided R2/S3 credentials | User/local |
| Hosted | Remote Noto API | API-issued signed object access | Noto-managed or customer-managed |
| Self-hosted | Customer-run Noto API | Customer object store | Customer-managed |

Lock rules:

- The remote API is optional and never required for local recording, local search, or local agent access.
- Capture always stays local.
- Local artifacts remain readable and exportable in every mode.
- The local gateway must not require a daemon or HTTP server while idle.
- Remote API details are hidden inside `RemoteSyncGateway`.
- Local and remote adapters must provide 1:1 semantics for supported gateway methods.
- Hosted provider routing requires explicit workspace policy before raw audio leaves the client.

## Consequences

- The client needs clear gateway configuration.
- Local and remote gateway parity must be tested.
- Remote gateway mode can add billing, device trust, audit, and policy without changing local artifact schemas.

## References

- [Read the Docs on documentation structure](https://docs.readthedocs.com/platform/stable/explanation/documentation-structure.html)
- [Cloudflare R2 presigned URLs](https://developers.cloudflare.com/r2/api/s3/presigned-urls/)
- [Cloudflare Queues](https://developers.cloudflare.com/queues/)
