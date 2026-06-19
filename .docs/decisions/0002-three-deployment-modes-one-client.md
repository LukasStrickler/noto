# ADR 0002: Three deployment modes behind one client interface

- **Status:** Accepted
- **Date:** 2026-05-20

## Context

[ADR-0001](0001-backend-is-source-of-truth.md) puts a backend between the UI and the data. That
backend needs to run in three very different places without forcing the user to think about it:
zero-config on a laptop, as a local daemon for faster repeated CLI calls, and on a remote box
the user controls. If each mode had its own client code, every command and screen would carry
transport-specific branches.

## Decision

All callers depend on a single `notoapi.Client` interface with two implementations behind it:

- **`direct`** (`apiclient.NewDirect`) — wraps `service.Service` with in-process function calls.
- **`httpClient`** (`apiclient.NewHTTP`) — JSON over HTTP + SSE, over a Unix domain socket
  (local daemon) or TCP with a bearer token (remote).

`host.Connect` does discovery in a fixed order — `NOTO_API_URL` (+ `NOTO_API_TOKEN`) → a healthy
local UDS daemon → otherwise spawn an in-process server — so the default is zero-config and the
caller never picks a transport.

## Alternatives considered

- **HTTP always, even locally** — uniform but pays serialization + a loopback hop for the common
  in-process case, and complicates the no-daemon default. Rejected; `direct` makes the local case
  a function call.
- **Separate `notod` binary for the daemon** — rejected for now; one binary with a `serve` mode
  matches the `host` design and keeps packaging simple. Revisit only if packaging demands it.

## Consequences

- One command/UI code path works identically in all three modes; tests run against `direct`.
- Going remote is a config change (`NOTO_API_URL` + token), not a code change.
- The two implementations must stay behavior-compatible; a conformance test guards this.
- SSE semantics (reconnect, replay) have to hold over both UDS and TCP — see the reconnect tests.

## Verification

`internal/transport/apiclient/{direct,http,sse,reconnect}.go`; discovery in `internal/app/host`.
`conformance_test.go` runs the same assertions against a client; `reconnect_test.go` and
`sse_characterization_test.go` cover the streaming path.
