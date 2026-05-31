# internal/transport — delivery boundaries

How the outside reaches the application: the wire contract, the HTTP
server/client, and the capture-helper IPC socket.

**Dependency rule:** may import `app`, `platform`, and `core`. `notoapi` is the
shared wire contract — keep it dependency-light (it's imported by `app`, `ui`,
and the rest of `transport`).

## Packages

- `notoapi` — API types (requests, responses) + the `notoapi.Client` interface,
  the one contract TUI/CLI speak
- `server` — HTTP handlers, SSE, routing, middleware/auth (thin wrappers over
  `Service`); core lifecycle in `routes.go`, admin/agent surface in `routes_admin.go`
- `apiclient` — `notoapi.Client` impls: direct (in-process) and HTTP (remote/daemon)
- `appsocket` — `IPCClient` ↔ the macOS capture helper over a Unix domain socket

**Anti-pattern:** keep handlers thin — parse the request, call one `Service`
method, render the result. No business logic in `server`. See the repo-root
`AGENTS.md` for the big picture.
