package benchmarks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewBenchmarker(t *testing.T) {
	config := DefaultBenchmarkConfig()
	b, err := NewBenchmarker(config)
	if err != nil {
		t.Fatalf("NewBenchmarker failed: %v", err)
	}
	if b == nil {
		t.Fatal("Benchmarker is nil")
	}
	defer b.Cleanup()
}

func TestRunTranscriptionBenchmarks(t *testing.T) {
	config := BenchmarkConfig{
		SyntheticAudioSizes:          []int{60, 300},
		FTS5IndexSize:                10,
		TUIRequiredFPS:                30,
		S3UploadBytesPerSecond:       512 * 1024,
		TranscriptionLatencyPerMinute: 1.0,
	}
	b, err := NewBenchmarker(config)
	if err != nil {
		t.Fatalf("NewBenchmarker failed: %v", err)
	}
	defer b.Cleanup()

	results, err := b.runTranscriptionBenchmarks()
	if err != nil {
		t.Fatalf("runTranscriptionBenchmarks failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 transcription results, got %d", len(results))
	}

	for _, r := range results {
		if r.Unit != "seconds" {
			t.Errorf("expected unit 'seconds', got %q", r.Unit)
		}
		if r.Threshold <= 0 {
			t.Errorf("threshold should be positive, got %f", r.Threshold)
		}
	}
}

func TestRunFTS5Benchmarks(t *testing.T) {
	config := BenchmarkConfig{
		SyntheticAudioSizes:          []int{60},
		FTS5IndexSize:                100,
		TUIRequiredFPS:                30,
		S3UploadBytesPerSecond:       512 * 1024,
		TranscriptionLatencyPerMinute: 1.0,
	}
	b, err := NewBenchmarker(config)
	if err != nil {
		t.Fatalf("NewBenchmarker failed: %v", err)
	}
	defer b.Cleanup()

	results, err := b.runFTS5Benchmarks()
	if err != nil {
		t.Fatalf("runFTS5Benchmarks failed: %v", err)
	}

	var hasLatency bool
	var hasIndexSize bool
	for _, r := range results {
		if r.Metric == "fts5_search_latency_p50" {
			hasLatency = true
		}
		if r.Metric == "fts5_index_size_bytes" {
			hasIndexSize = true
		}
	}

	if !hasLatency {
		t.Error("missing fts5_search_latency_p50 metric")
	}
	if !hasIndexSize {
		t.Error("missing fts5_index_size_bytes metric")
	}
}

func TestRunS3Benchmarks(t *testing.T) {
	config := BenchmarkConfig{
		SyntheticAudioSizes:          []int{60},
		FTS5IndexSize:                10,
		TUIRequiredFPS:                30,
		S3UploadBytesPerSecond:       512 * 1024,
		TranscriptionLatencyPerMinute: 1.0,
	}
	b, err := NewBenchmarker(config)
	if err != nil {
		t.Fatalf("NewBenchmarker failed: %v", err)
	}
	defer b.Cleanup()

	results, err := b.runS3Benchmarks()
	if err != nil {
		t.Fatalf("runS3Benchmarks failed: %v", err)
	}

	if len(results) < 2 {
		t.Errorf("expected at least 2 S3 results, got %d", len(results))
	}

	for _, r := range results {
		if r.Metric == "" {
			t.Error("metric name is empty")
		}
	}
}

func TestRunTUIBenchmarks(t *testing.T) {
	config := BenchmarkConfig{
		SyntheticAudioSizes:          []int{60},
		FTS5IndexSize:                10,
		TUIRequiredFPS:                30,
		S3UploadBytesPerSecond:       512 * 1024,
		TranscriptionLatencyPerMinute: 1.0,
	}
	b, err := NewBenchmarker(config)
	if err != nil {
		t.Fatalf("NewBenchmarker failed: %v", err)
	}
	defer b.Cleanup()

	results, err := b.runTUIBenchmarks()
	if err != nil {
		t.Fatalf("runTUIBenchmarks failed: %v", err)
	}

	var hasAvgFPS bool
	var hasMinFPS bool
	for _, r := range results {
		if r.Metric == "tui_render_avg_fps" {
			hasAvgFPS = true
		}
		if r.Metric == "tui_render_min_fps" {
			hasMinFPS = true
		}
	}

	if !hasAvgFPS {
		t.Error("missing tui_render_avg_fps metric")
	}
	if !hasMinFPS {
		t.Error("missing tui_render_min_fps metric")
	}
}

func TestRunAllBenchmarks(t *testing.T) {
	config := DefaultBenchmarkConfig()
	config.FTS5IndexSize = 50
	b, err := NewBenchmarker(config)
	if err != nil {
		t.Fatalf("NewBenchmarker failed: %v", err)
	}
	defer b.Cleanup()

	result, err := b.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.SchemaVersion != "benchmark-results.v1" {
		t.Errorf("expected schema version 'benchmark-results.v1', got %q", result.SchemaVersion)
	}

	if result.RunID == "" {
		t.Error("run ID is empty")
	}

	if len(result.Results) == 0 {
		t.Error("no results returned")
	}

	if !result.AllPasses() {
		failed := 0
		for _, r := range result.Results {
			if !r.Pass {
				failed++
			}
		}
		t.Errorf("%d benchmark(s) failed", failed)
	}
}

func TestSaveResults(t *testing.T) {
	config := DefaultBenchmarkConfig()
	config.FTS5IndexSize = 20
	b, err := NewBenchmarker(config)
	if err != nil {
		t.Fatalf("NewBenchmarker failed: %v", err)
	}
	defer b.Cleanup()

	result, err := b.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "benchmark-results.json")

	err = b.SaveResults(result, path)
	if err != nil {
		t.Fatalf("SaveResults failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read results file: %v", err)
	}

	if len(data) == 0 {
		t.Error("results file is empty")
	}

	if result.AllPasses() {
		t.Log("All benchmarks passed")
	}
}

func TestBenchmarkResultAllPasses(t *testing.T) {
	result := &BenchmarkResult{
		SchemaVersion: "benchmark-results.v1",
		RunID:         "test",
		Timestamp:     "2026-04-26T00:00:00Z",
		Results: []MetricResult{
			{Metric: "test1", Pass: true},
			{Metric: "test2", Pass: true},
		},
	}

	if !result.AllPasses() {
		t.Error("expected AllPasses to return true")
	}

	result.Results[1].Pass = false

	if result.AllPasses() {
		t.Error("expected AllPasses to return false when one metric fails")
	}
}

func TestMockSTTProvider(t *testing.T) {
	provider := &mockSTTProvider{baseLatency: 100 * 1e6}

	latency, err := provider.Transcribe(60)
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}

	if latency <= 0 {
		t.Error("latency should be positive")
	}
}

func TestMockS3Adapter(t *testing.T) {
	adapter := &mockS3Adapter{
		baseLatency:   10 * 1e6,
		bytesPerSecond: 1024 * 1024,
	}

	latency, err := adapter.Upload("test/key", nil, 1024*1024)
	if err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	if latency <= 0 {
		t.Error("latency should be positive")
	}
}

func TestGenerateSegments(t *testing.T) {
	segments := generateSegments(10)
	if len(segments) != 10 {
		t.Errorf("expected 10 segments, got %d", len(segments))
	}

	for i, seg := range segments {
		if seg.SegmentID == "" {
			t.Errorf("segment %d has empty SegmentID", i)
		}
		if seg.Speaker == "" {
			t.Errorf("segment %d has empty Speaker", i)
		}
		if seg.Text == "" {
			t.Errorf("segment %d has empty Text", i)
		}
	}
}

func TestGenerateSummaryItems(t *testing.T) {
	items := generateSummaryItems(3)
	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}
}

func TestGenerateActionItems(t *testing.T) {
	items := generateActionItems(2)
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}

	for i, item := range items {
		if item.Text == "" {
			t.Errorf("item %d has empty Text", i)
		}
		if item.Owner == "" {
			t.Errorf("item %d has empty Owner", i)
		}
	}
}

func TestFTS5SearchLatency(t *testing.T) {
	config := BenchmarkConfig{
		SyntheticAudioSizes:          []int{60},
		FTS5IndexSize:                100,
		TUIRequiredFPS:                30,
		S3UploadBytesPerSecond:       512 * 1024,
		TranscriptionLatencyPerMinute: 1.0,
	}
	b, err := NewBenchmarker(config)
	if err != nil {
		t.Fatalf("NewBenchmarker failed: %v", err)
	}
	defer b.Cleanup()

	results, err := b.runFTS5Benchmarks()
	if err != nil {
		t.Fatalf("runFTS5Benchmarks failed: %v", err)
	}

	for _, r := range results {
		if r.Metric == "fts5_search_latency_p50" {
			if r.Value >= 0.100 {
				t.Errorf("FTS5 p50 latency %f >= 100ms threshold", r.Value)
			}
			if !r.Pass {
				t.Errorf("FTS5 p50 latency should pass at 100ms threshold", r.Value)
			}
		}
	}
}
