package bench_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
	"github.com/lukasstrickler/noto/internal/platform/bench"
)

// isolateModalCreds points Modal credential discovery at a guaranteed-absent
// path and clears the token env so the probe is deterministic regardless of the
// developer's real ~/.modal.toml.
func isolateModalCreds(t *testing.T) {
	t.Helper()
	t.Setenv("MODAL_TOKEN_ID", "")
	t.Setenv("MODAL_TOKEN_SECRET", "")
	t.Setenv("MODAL_CONFIG_PATH", filepath.Join(t.TempDir(), "absent.modal.toml"))
}

func TestCheckModalPrereqs_MissingEverything(t *testing.T) {
	isolateModalCreds(t)
	pre := bench.CheckModalPrereqs(t.TempDir()) // empty repo: no venv, no script
	if pre.Ready() {
		t.Fatal("empty repo without credentials should not be Ready")
	}
	blockers := strings.Join(pre.Blockers(), "\n")
	for _, want := range []string{".venv-modal", "Modal credentials", "modal_benchmark.py"} {
		if !strings.Contains(blockers, want) {
			t.Errorf("blockers missing %q:\n%s", want, blockers)
		}
	}
}

func TestCheckModalPrereqs_ReadyWhenConfigured(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".venv-modal", "bin", "python"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(root, "scripts", "modal_benchmark.py"), "# launcher\n")
	t.Setenv("MODAL_TOKEN_ID", "id")
	t.Setenv("MODAL_TOKEN_SECRET", "secret")

	pre := bench.CheckModalPrereqs(root)
	if !pre.Ready() {
		t.Fatalf("configured repo should be Ready: %+v", pre)
	}
	if len(pre.Blockers()) != 0 {
		t.Errorf("Ready prereqs should have no blockers: %v", pre.Blockers())
	}
}

func TestPreflight_GatesLiveRunOnModalPrereqs(t *testing.T) {
	isolateModalCreds(t)
	runner := bench.NewRunner(bench.NewStore(t.TempDir()))
	runner.Modal = bench.NewModalRunner(runner.Store, t.TempDir()) // empty repo: prereqs absent

	live := corebench.RunManifest{SuiteID: "anchor_ami@2026-06-18", Tier: "baseline_variance"}
	resp, err := runner.Preflight(context.Background(), bench.PreflightRequest{Manifest: live})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(resp.Blockers, "\n"), "modal_prereq") {
		t.Errorf("live run without credentials should be blocked, got %v", resp.Blockers)
	}
	if resp.ComparableToWinner {
		t.Error("blocked run must not be comparable")
	}

	// Integration-only runs use a local fixture and must NOT be gated.
	integ := corebench.RunManifest{SuiteID: "smoke@2026-06-18", Tier: "smoke", IntegrationOnly: true}
	resp, err = runner.Preflight(context.Background(), bench.PreflightRequest{Manifest: integ})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(resp.Blockers, "\n"), "modal_prereq") {
		t.Errorf("integration-only run should not need Modal credentials, got %v", resp.Blockers)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
