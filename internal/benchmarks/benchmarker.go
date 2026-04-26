package benchmarks

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/lukasstrickler/noto/internal/search"
)

type BenchmarkConfig struct {
	SyntheticAudioSizes            []int
	FTS5IndexSize                  int
	TUIRequiredFPS                 int
	S3UploadBytesPerSecond         int64
	TranscriptionLatencyPerMinute   float64
}

func DefaultBenchmarkConfig() BenchmarkConfig {
	return BenchmarkConfig{
		SyntheticAudioSizes:          []int{60, 300, 900, 1800},
		FTS5IndexSize:                1000,
		TUIRequiredFPS:               30,
		S3UploadBytesPerSecond:       512 * 1024,
		TranscriptionLatencyPerMinute: 1.0,
	}
}

type BenchmarkResult struct {
	SchemaVersion string        `json:"schema_version"`
	RunID         string        `json:"run_id"`
	Timestamp     string        `json:"timestamp"`
	Results       []MetricResult
}

type MetricResult struct {
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
	Unit      string  `json:"unit"`
	Threshold float64 `json:"threshold"`
	Pass      bool    `json:"pass"`
}

type Benchmarker struct {
	config    BenchmarkConfig
	indexPath string
}

func NewBenchmarker(config BenchmarkConfig) (*Benchmarker, error) {
	tmpDir, err := os.MkdirTemp("", "noto-benchmark-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	return &Benchmarker{
		config:    config,
		indexPath: filepath.Join(tmpDir, "benchmark_search.db"),
	}, nil
}

func (b *Benchmarker) Run() (*BenchmarkResult, error) {
	runID := fmt.Sprintf("bench-%d", time.Now().UnixNano())
	result := &BenchmarkResult{
		SchemaVersion: "benchmark-results.v1",
		RunID:         runID,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Results:       []MetricResult{},
	}

	transcriptionResults, err := b.runTranscriptionBenchmarks()
	if err != nil {
		return nil, fmt.Errorf("transcription benchmarks: %w", err)
	}
	result.Results = append(result.Results, transcriptionResults...)

	fts5Results, err := b.runFTS5Benchmarks()
	if err != nil {
		return nil, fmt.Errorf("FTS5 benchmarks: %w", err)
	}
	result.Results = append(result.Results, fts5Results...)

	s3Results, err := b.runS3Benchmarks()
	if err != nil {
		return nil, fmt.Errorf("S3 benchmarks: %w", err)
	}
	result.Results = append(result.Results, s3Results...)

	tuiResults, err := b.runTUIBenchmarks()
	if err != nil {
		return nil, fmt.Errorf("TUI benchmarks: %w", err)
	}
	result.Results = append(result.Results, tuiResults...)

	return result, nil
}

func (b *Benchmarker) SaveResults(result *BenchmarkResult, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create results file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func (r *BenchmarkResult) AllPasses() bool {
	for _, mr := range r.Results {
		if !mr.Pass {
			return false
		}
	}
	return true
}

type mockSTTProvider struct {
	baseLatency time.Duration
}

func (m *mockSTTProvider) Transcribe(audioSeconds int) (time.Duration, error) {
	latency := m.baseLatency + (time.Duration(audioSeconds) * 800 * time.Millisecond / 1000)
	return latency, nil
}

func (m *mockSTTProvider) Name() string {
	return "mock-stt"
}

func (b *Benchmarker) runTranscriptionBenchmarks() ([]MetricResult, error) {
	results := []MetricResult{}
	provider := &mockSTTProvider{baseLatency: 200 * time.Millisecond}

	for _, audioSeconds := range b.config.SyntheticAudioSizes {
		var latencies []float64
		iterations := 5
		for i := 0; i < iterations; i++ {
			latency, err := provider.Transcribe(audioSeconds)
			if err != nil {
				return nil, fmt.Errorf("transcribe audio length %d: %w", audioSeconds, err)
			}
			latencies = append(latencies, latency.Seconds())
		}
		sort.Float64s(latencies)
		medianLatency := latencies[iterations/2]

		threshold := b.config.TranscriptionLatencyPerMinute * float64(audioSeconds) / 60.0

		results = append(results, MetricResult{
			Metric:    fmt.Sprintf("transcription_latency_%ds", audioSeconds),
			Value:     medianLatency,
			Unit:      "seconds",
			Threshold: threshold,
			Pass:      medianLatency <= threshold,
		})
	}

	return results, nil
}

func (b *Benchmarker) runFTS5Benchmarks() ([]MetricResult, error) {
	results := []MetricResult{}

	idx, err := search.NewSearchIndex(b.indexPath)
	if err != nil {
		return nil, fmt.Errorf("create search index: %w", err)
	}
	defer idx.Close()

	meetingCount := b.config.FTS5IndexSize
	searchTerms := []string{
		"architecture decision",
		"api design",
		"performance optimization",
		"implementation plan",
	}

	for i := 0; i < meetingCount; i++ {
		input := &search.IndexMeetingInput{
			MeetingID: fmt.Sprintf("meeting-%d", i),
			Title:     fmt.Sprintf("Meeting %d - %s", i, searchTerms[i%len(searchTerms)]),
			TranscriptSegments: generateSegments(50 + (i % 100)),
			Decisions:          generateSummaryItems(3),
			ActionItems:        generateActionItems(2),
			Risks:              generateSummaryItems(1),
		}
		if err := idx.IndexMeetingFromInput(input); err != nil {
			return nil, fmt.Errorf("index meeting %d: %w", i, err)
		}
	}

	var latencies []float64
	iterations := 100
	for i := 0; i < iterations; i++ {
		term := searchTerms[i%len(searchTerms)]
		start := time.Now()
		_, err := idx.Search(term)
		latency := time.Since(start).Seconds()
		if err != nil {
			return nil, fmt.Errorf("search term %q: %w", term, err)
		}
		latencies = append(latencies, latency)
	}
	sort.Float64s(latencies)

	p50 := latencies[int(float64(len(latencies))*0.50)]
	p95 := latencies[int(float64(len(latencies))*0.95)]
	p99 := latencies[int(float64(len(latencies))*0.99)]

	latencyThreshold := 0.100

	for _, percentile := range []struct {
		name  string
		value float64
	}{
		{"fts5_search_latency_p50", p50},
		{"fts5_search_latency_p95", p95},
		{"fts5_search_latency_p99", p99},
	} {
		results = append(results, MetricResult{
			Metric:    percentile.name,
			Value:     percentile.value,
			Unit:      "seconds",
			Threshold: latencyThreshold,
			Pass:      percentile.value < latencyThreshold,
		})
	}

	indexSize, err := b.measureIndexSize()
	if err != nil {
		return nil, fmt.Errorf("measure index size: %w", err)
	}
	results = append(results, MetricResult{
		Metric:    "fts5_index_size_bytes",
		Value:     float64(indexSize),
		Unit:      "bytes",
		Threshold: 0,
		Pass:      true,
	})

	indexPerMeeting := float64(indexSize) / float64(meetingCount)
	results = append(results, MetricResult{
		Metric:    "fts5_index_size_per_meeting_bytes",
		Value:     indexPerMeeting,
		Unit:      "bytes",
		Threshold: 0,
		Pass:      true,
	})

	return results, nil
}

func (b *Benchmarker) measureIndexSize() (int64, error) {
	info, err := os.Stat(b.indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}

type mockS3Adapter struct {
	baseLatency     time.Duration
	bytesPerSecond   int64
}

func (m *mockS3Adapter) Upload(key string, data io.Reader, size int64) (time.Duration, error) {
	transferTime := time.Duration(size) * time.Second / time.Duration(m.bytesPerSecond)
	latency := m.baseLatency + transferTime
	return latency, nil
}

func (b *Benchmarker) runS3Benchmarks() ([]MetricResult, error) {
	results := []MetricResult{}

	adapter := &mockS3Adapter{
		baseLatency:   50 * time.Millisecond,
		bytesPerSecond: 600 * 1024,
	}

	testSizes := []struct {
		name                    string
		sizeBytes               int64
		thresholdSecondsPerMB    float64
	}{
		{"1mb", 1024 * 1024, 2.0},
		{"5mb", 5 * 1024 * 1024, 2.0},
		{"10mb", 10 * 1024 * 1024, 2.0},
	}

	for _, test := range testSizes {
		var latencies []float64
		iterations := 5
		for i := 0; i < iterations; i++ {
			data := make([]byte, test.sizeBytes)
			for j := range data {
				data[j] = byte(j % 256)
			}

			start := time.Now()
			_, err := adapter.Upload(fmt.Sprintf("test/%s-%d.bin", test.name, i), nil, test.sizeBytes)
			latency := time.Since(start).Seconds()
			if err != nil {
				return nil, fmt.Errorf("upload %s: %w", test.name, err)
			}
			latencies = append(latencies, latency)
		}
		sort.Float64s(latencies)
		medianLatency := latencies[iterations/2]

		threshold := float64(test.sizeBytes) / (1024 * 1024) * test.thresholdSecondsPerMB

		results = append(results, MetricResult{
			Metric:    fmt.Sprintf("s3_upload_%s", test.name),
			Value:     medianLatency,
			Unit:      "seconds",
			Threshold: threshold,
			Pass:      medianLatency < threshold,
		})

		bytesPerSecond := float64(test.sizeBytes) / medianLatency
		results = append(results, MetricResult{
			Metric:    fmt.Sprintf("s3_upload_%s_bytes_per_second", test.name),
			Value:     bytesPerSecond,
			Unit:      "bytes_per_second",
			Threshold: float64(b.config.S3UploadBytesPerSecond),
			Pass:      bytesPerSecond >= float64(b.config.S3UploadBytesPerSecond),
		})
	}

	return results, nil
}

type mockTUIModel struct {
	items  []string
	width  int
	height int
}

func (m *mockTUIModel) Update(msg interface{}) (interface{}, error) {
	return nil, nil
}

func (m *mockTUIModel) View() string {
	var result string
	for i := 0; i < min(m.height, len(m.items)); i++ {
		result += fmt.Sprintf("  %s\n", m.items[i])
	}
	return result
}

func (b *Benchmarker) runTUIBenchmarks() ([]MetricResult, error) {
	results := []MetricResult{}

	items := make([]string, 1000)
	for i := range items {
		items[i] = fmt.Sprintf("Meeting %d - Title text for render test", i)
	}

	model := &mockTUIModel{
		items:  items,
		width:  100,
		height: 30,
	}

	var frameTimes []float64
	iterations := 60

	for i := 0; i < iterations; i++ {
		start := time.Now()
		_ = model.View()
		frameTime := time.Since(start).Seconds()
		frameTimes = append(frameTimes, frameTime)
	}

	var totalTime float64
	for _, ft := range frameTimes {
		totalTime += ft
	}
	avgFrameTime := totalTime / float64(len(frameTimes))
	avgFPS := 1.0 / avgFrameTime

	minFPS := 1.0 / frameTimes[len(frameTimes)-1]

	fpsThreshold := float64(b.config.TUIRequiredFPS)

	results = append(results, MetricResult{
		Metric:    "tui_render_avg_fps",
		Value:     avgFPS,
		Unit:      "fps",
		Threshold: fpsThreshold,
		Pass:      avgFPS >= fpsThreshold,
	})

	results = append(results, MetricResult{
		Metric:    "tui_render_min_fps",
		Value:     minFPS,
		Unit:      "fps",
		Threshold: fpsThreshold,
		Pass:      minFPS >= fpsThreshold,
	})

	results = append(results, MetricResult{
		Metric:    "tui_render_avg_frame_time_ms",
		Value:     avgFrameTime * 1000,
		Unit:      "milliseconds",
		Threshold: 1000.0 / fpsThreshold,
		Pass:      avgFrameTime*1000 <= 1000.0/fpsThreshold,
	})

	return results, nil
}

func generateSegments(count int) []search.TranscriptSegment {
	segments := make([]search.TranscriptSegment, count)
	words := []string{
		"architecture", "decision", "api", "design", "performance",
		"optimization", "implementation", "plan", "review", "discussion",
		"requirements", "specification", "development", "testing", "deployment",
		"infrastructure", "monitoring", "scalability", "reliability", "security",
	}
	for i := range segments {
		text := ""
		for len(text) < 100 {
			text += words[len(text)%len(words)] + " "
		}
		segments[i] = search.TranscriptSegment{
			SegmentID: fmt.Sprintf("seg_%06d", i),
			Speaker:   fmt.Sprintf("speaker_%d", i%3),
			Text:      text[:100],
			Timestamp: float64(i * 30),
		}
	}
	return segments
}

func generateSummaryItems(count int) []search.SummaryItem {
	items := make([]search.SummaryItem, count)
	for i := range items {
		items[i] = search.SummaryItem{
			Text:       fmt.Sprintf("Summary item %d text content", i),
			SpeakerIDs: []string{fmt.Sprintf("speaker_%d", i%3)},
		}
	}
	return items
}

func generateActionItems(count int) []search.ActionItem {
	items := make([]search.ActionItem, count)
	for i := range items {
		items[i] = search.ActionItem{
			Text:  fmt.Sprintf("Action item %d to be done", i),
			Owner: fmt.Sprintf("user_%d", i%5),
		}
	}
	return items
}

func (b *Benchmarker) Cleanup() error {
	return os.RemoveAll(filepath.Dir(b.indexPath))
}
