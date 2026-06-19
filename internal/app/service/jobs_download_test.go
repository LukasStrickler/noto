package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/compute"
	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/db"
	"github.com/lukasstrickler/noto/internal/platform/models"
	"github.com/lukasstrickler/noto/internal/platform/providers"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

func newDownloadTestSvc(t *testing.T, dir string, mk func(string) *models.Manager) *Service {
	t.Helper()
	jobsDB, err := db.Open(filepath.Join(dir, "jobs.sqlite"))
	if err != nil {
		t.Fatalf("jobs db: %v", err)
	}
	if err := jobsDB.Migrate(db.JobsSchema); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = jobsDB.Close() })
	return New(Deps{
		Config:       config.Config{ConfigDir: dir, ArtifactRoot: dir},
		Registry:     providers.DefaultRegistry(),
		JobsDB:       jobsDB,
		Compute:      compute.Plan{Backend: compute.BackendCPU, Tier: compute.TierFast},
		ModelManager: mk,
		Version:      "test",
	})
}

func TestRunDownloadModelStoresAndReportsProgress(t *testing.T) {
	body := make([]byte, 2<<20) // 2 MiB so progress emits more than once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(200)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	man := models.Manifest{
		SchemaVersion: "test.v1",
		Models: []models.Model{{
			ID: "demo", Role: models.RoleSTT, License: "MIT",
			Variants: []models.Variant{{
				Backend: models.BackendCPU, Tier: models.TierFast, Primary: "model.onnx",
				Assets: []models.Asset{{Filename: "model.onnx", URL: srv.URL + "/model.onnx", Size: int64(len(body))}},
			}},
		}},
	}
	dir := t.TempDir()
	svc := newDownloadTestSvc(t, dir, func(d string) *models.Manager {
		return models.NewWithManifest(d, man)
	})

	// Capture streamed job events.
	sub := svc.events.subscribe()
	defer svc.events.unsubscribe(sub)
	progressSeen := make(chan struct{}, 1)
	go func() {
		for ev := range sub {
			if ev.Kind == notoapi.EventJob && ev.Job != nil && ev.Job.Phase == "downloading demo" {
				select {
				case progressSeen <- struct{}{}:
				default:
				}
			}
		}
	}()

	job := notoapi.Job{ID: "job-dl-1", Kind: notoapi.JobDownloadModel, Options: map[string]any{
		"model_id": "demo", "backend": "cpu", "tier": "fast",
	}}
	if err := svc.runDownloadModel(context.Background(), &job); err != nil {
		t.Fatalf("runDownloadModel: %v", err)
	}

	// File landed.
	if !models.NewWithManifest(dir, man).Available("demo", compute.BackendCPU, compute.TierFast) {
		t.Error("model not available after download job")
	}
	// At least one progress event with the right phase streamed.
	select {
	case <-progressSeen:
	default:
		t.Error("no download progress event observed")
	}
}

func TestRunDownloadModelUnknownFails(t *testing.T) {
	dir := t.TempDir()
	svc := newDownloadTestSvc(t, dir, func(d string) *models.Manager {
		return models.NewWithManifest(d, models.Manifest{SchemaVersion: "t"})
	})
	job := notoapi.Job{ID: "job-dl-2", Options: map[string]any{"model_id": "nope"}}
	if err := svc.runDownloadModel(context.Background(), &job); err == nil {
		t.Fatal("expected error for unknown model")
	}
}

func TestDownloadModelEnqueues(t *testing.T) {
	dir := t.TempDir()
	svc := newDownloadTestSvc(t, dir, func(d string) *models.Manager {
		return models.NewWithManifest(d, models.Manifest{SchemaVersion: "t"})
	})
	job, err := svc.DownloadModel(context.Background(), "parakeet-tdt-0.6b-v3", "", "")
	if err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}
	if job.Kind != notoapi.JobDownloadModel {
		t.Errorf("kind = %s", job.Kind)
	}
	// Backend/tier default to the host plan (cpu/fast in this test svc).
	if job.Options["backend"] != "cpu" || job.Options["tier"] != "fast" {
		t.Errorf("defaulted options = %v", job.Options)
	}
}
