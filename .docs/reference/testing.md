# Testing Reference

## Test Layers

| Layer | What it proves | Where |
| --- | --- | --- |
| **Unit** | Single function/method, no I/O | `*_test.go` alongside the package; use `testutil.FakeRepo` |
| **Repo** | `LocalArtifactRepository` reads/writes correctly | `internal/repo/local_test.go` (temp dir, real filesystem) |
| **Integration** | Full pipeline via in-process host | `internal/notohost/host_test.go`, `internal/service/import_test.go` |
| **Server** | HTTP handler routing and error shapes | `internal/server/routes_speaker_test.go` |
| **TUI** | Screen model updates and rendering | `internal/tui/*_test.go` (characterization + search) |

## Writing Unit Tests

Use `testutil.FakeRepo` for service-layer tests that should not touch the filesystem:

```go
import (
    "github.com/lukasstrickler/noto/internal/testutil"
    "github.com/lukasstrickler/noto/internal/repo"
)

func TestService_DeleteMeeting(t *testing.T) {
    fr := testutil.NewFakeRepo()
    ctx := context.Background()
    id := uuid.New()
    _ = fr.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: "to delete"})

    svc := newTestSvc(t, fr)   // helper that builds a Service with FakeRepo
    if err := svc.DeleteMeeting(ctx, id.String()); err != nil {
        t.Fatal(err)
    }
    if fr.MeetingCount() != 0 {
        t.Error("expected 0 meetings")
    }
}
```

See `internal/service/meetings_unit_test.go` for the `newTestSvc` helper pattern.

## Writing Integration Tests

Use `notohost.Start` for end-to-end flows. The host starts in-process — no
separate server needed:

```go
func TestE2E(t *testing.T) {
    t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
    t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    host, err := notohost.Start(ctx, notohost.Options{
        Address: filepath.Join(t.TempDir(), "noto.sock"),
    })
    // ... use host.Client()
}
```

## Test File Location

Go requires `*_test.go` files to be in the same directory as the package they test.
Tests in a subdirectory become a separate package — fine for external/black-box tests
but cannot access unexported symbols.

```
internal/
  service/
    meetings.go
    meetings_unit_test.go   ← same package, accesses internals
    import_test.go          ← package service_test, external style
  repo/
    local.go
    local_test.go           ← package repo_test, black-box
  testutil/
    fake_repo.go            ← shared test helper (not test files)
```

## Provider / Network Tests

Tag slow provider tests with `//go:build integration` and skip by default:

```go
//go:build integration
// Run with: go test -tags integration ./...
```

Keep `go test ./...` under 30 seconds on a developer machine.

## Acceptance Criteria

A feature is done when:

- `go test ./...` passes.
- Service-layer logic has unit tests using `testutil.FakeRepo`.
- `LocalArtifactRepository` changes have tests in `internal/repo/local_test.go`.
- CLI JSON output has at minimum a smoke test (call the command, parse JSON, check key fields).
- Agent-facing endpoints are covered by the server routes test or an integration test.
