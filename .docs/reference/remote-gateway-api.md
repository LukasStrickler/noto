# Remote Gateway API Reference

This is a post-V1 reference. V1 is terminal TUI/CLI plus local recording,
artifacts, transcription, summaries, and search. It does not implement a remote
gateway.

## Purpose

The Noto API is the remote `SyncGateway` implementation. It adds identity,
device trust, policy, signed object access, hosted jobs, audit, and billing
metadata.

Application code must call `SyncGateway` methods. Only `RemoteSyncGateway` knows these HTTP endpoints.

## Non-Goals

- Do not capture audio.
- Do not replace local artifacts as the durable source format.
- Do not proxy large artifacts when signed object access is available.
- Do not run STT or LLM work inside request handlers.
- Do not become required for local recording, search, or agent access.
- Do not require an idle desktop HTTP client, polling loop, or worker.

## Contract Sketch

When remote gateway work starts, define HTTP endpoints from the
`SyncGateway` methods in [storage-sync.md](./storage-sync.md). Do not add
API-only behavior unless it fits that port.

The remote API should own only:

- device registration and revocation
- workspace policy
- exact-key signed object access
- manifest commit metadata and conflicts
- hosted job state
- audit and billing metadata

Request handlers stay thin. Large artifacts move through signed object access,
and provider calls run in async workers.

## Future API Reference Requirements

When endpoints are implemented, document auth, request and response bodies,
error codes, idempotency, and rate limits in an API-specific reference.

## Test Rules

Remote gateway work is post-V1 and must not weaken local validation:

- Remote and local gateway clients share contract tests for supported methods.
- API handlers return stable JSON error codes for auth, policy, conflict,
  validation, and retryable remote errors.
- Hosted jobs produce the same normalized artifacts as local provider jobs.
- Raw audio upload tests must prove explicit policy exists before upload.

See [testing.md](./testing.md) for the V1 agentic validation model.

## References

- [Good Docs API reference template](https://www.thegooddocsproject.dev/template/api-reference)
- [Cloudflare Workers limits](https://developers.cloudflare.com/workers/platform/limits/)
