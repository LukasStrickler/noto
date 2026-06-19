package dataset

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/benchmark/metrics"

	// Register the suite's -hours/-seed flags so the shared `go test ./benchmark/...`
	// invocation parses here too (these loader tests use no -hours cap themselves).
	_ "github.com/lukasstrickler/noto/benchmark/internal/sample"
)

func TestParseRTTM(t *testing.T) {
	// Two speakers, out of order on disk, plus a comment and a short line that
	// must be ignored.
	in := `SPEAKER mtg 1 5.00 2.50 <NA> <NA> B <NA> <NA>
; a comment line
SPEAKER mtg 1 0.00 3.00 <NA> <NA> A <NA> <NA>
SPKR-INFO mtg 1 <NA> <NA> <NA> unknown A <NA> <NA>
`
	turns, err := ParseRTTM(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseRTTM: %v", err)
	}
	want := []Turn{
		{Speaker: "A", StartSeconds: 0, EndSeconds: 3},
		{Speaker: "B", StartSeconds: 5, EndSeconds: 7.5},
	}
	if !reflect.DeepEqual(turns, want) {
		t.Errorf("turns = %+v, want %+v", turns, want)
	}
}

func TestParseCTM(t *testing.T) {
	in := `;; per-speaker word file
mtg 1 0.00 0.30 The
mtg 1 0.30 0.40 cat
mtg 1 0.70 0.50 sat
`
	words, err := ParseCTM(strings.NewReader(in), "A")
	if err != nil {
		t.Fatalf("ParseCTM: %v", err)
	}
	if len(words) != 3 {
		t.Fatalf("got %d words, want 3", len(words))
	}
	if words[1] != (Word{Speaker: "A", StartSeconds: 0.30, EndSeconds: 0.70, Text: "cat"}) {
		t.Errorf("words[1] = %+v", words[1])
	}
}

func TestParseWords(t *testing.T) {
	in := `[
	  {"speaker":"A","start":0.0,"end":0.4,"text":"hello"},
	  {"speaker":"B","start":0.5,"end":0.9,"text":"world"}
	]`
	words, err := ParseWords(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseWords: %v", err)
	}
	want := []Word{
		{Speaker: "A", StartSeconds: 0.0, EndSeconds: 0.4, Text: "hello"},
		{Speaker: "B", StartSeconds: 0.5, EndSeconds: 0.9, Text: "world"},
	}
	if !reflect.DeepEqual(words, want) {
		t.Errorf("words = %+v, want %+v", words, want)
	}
}

func TestLoadSyntheticMeeting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meeting_2spk.json")
	js := `{"speakers":["X","Y"],"duration":12.5,
	  "turns":[{"speaker_id":"Y","start":5.0,"end":12.5},
	           {"speaker_id":"X","start":0.0,"end":5.0}]}`
	if err := os.WriteFile(path, []byte(js), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadSyntheticMeeting(path)
	if err != nil {
		t.Fatalf("LoadSyntheticMeeting: %v", err)
	}
	if m.ID != "meeting_2spk" {
		t.Errorf("ID = %q, want meeting_2spk", m.ID)
	}
	if m.AudioPath != filepath.Join(dir, "meeting_2spk.wav") {
		t.Errorf("AudioPath = %q", m.AudioPath)
	}
	if m.Duration != 12.5 {
		t.Errorf("Duration = %v, want 12.5", m.Duration)
	}
	// turns sorted by start
	want := []Turn{
		{Speaker: "X", StartSeconds: 0, EndSeconds: 5},
		{Speaker: "Y", StartSeconds: 5, EndSeconds: 12.5},
	}
	if !reflect.DeepEqual(m.Turns, want) {
		t.Errorf("Turns = %+v, want %+v", m.Turns, want)
	}
}

// A Meeting's conversions must feed the scorers cleanly: a perfect hypothesis
// (same turns, same words) scores zero everywhere. This pins the loader↔metrics
// contract.
func TestMeetingConversionsScoreZeroOnPerfectHyp(t *testing.T) {
	m := Meeting{
		Turns: []Turn{
			{Speaker: "A", StartSeconds: 0, EndSeconds: 5},
			{Speaker: "B", StartSeconds: 5, EndSeconds: 10},
		},
		Words: []Word{
			{Speaker: "A", StartSeconds: 0, EndSeconds: 1, Text: "Hello,"},
			{Speaker: "A", StartSeconds: 1, EndSeconds: 2, Text: "there"},
			{Speaker: "B", StartSeconds: 5, EndSeconds: 6, Text: "general"},
			{Speaker: "B", StartSeconds: 6, EndSeconds: 7, Text: "Kenobi"},
		},
	}

	// DER against itself is zero.
	der := metrics.DER(m.Segments(), m.Segments(), metrics.DefaultDEROptions())
	if der.Rate != 0 {
		t.Errorf("self DER = %v, want 0", der.Rate)
	}

	// Reference is normalized, time-ordered, punctuation-stripped.
	ref := m.Reference()
	wantRef := []string{"hello", "there", "general", "kenobi"}
	if !reflect.DeepEqual(ref, wantRef) {
		t.Errorf("Reference() = %v, want %v", ref, wantRef)
	}
	if w := metrics.WER(ref, ref); w.Rate != 0 {
		t.Errorf("self WER = %v, want 0", w.Rate)
	}

	// cpWER of the per-speaker reference against itself is zero.
	bySpk := m.ReferenceBySpeaker()
	if cp := metrics.CpWER(bySpk, bySpk); cp.Rate != 0 {
		t.Errorf("self cpWER = %v, want 0", cp.Rate)
	}
	if !reflect.DeepEqual(bySpk["A"], []string{"hello", "there"}) {
		t.Errorf("bySpk[A] = %v", bySpk["A"])
	}
}

func TestAudioSeconds(t *testing.T) {
	// declared Duration wins
	if got := (Meeting{Duration: 49.0, Turns: []Turn{{EndSeconds: 10}}}).AudioSeconds(); got != 49.0 {
		t.Errorf("AudioSeconds = %v, want 49.0", got)
	}
	// else latest turn/word end
	m := Meeting{
		Turns: []Turn{{EndSeconds: 30}},
		Words: []Word{{EndSeconds: 42}},
	}
	if got := m.AudioSeconds(); got != 42 {
		t.Errorf("AudioSeconds = %v, want 42", got)
	}
}

func TestSelectHours(t *testing.T) {
	// four half-hour meetings = 2h total
	meetings := []Meeting{
		{ID: "a", Duration: 1800}, {ID: "b", Duration: 1800},
		{ID: "c", Duration: 1800}, {ID: "d", Duration: 1800},
	}
	ids := func(ms []Meeting) []string {
		out := make([]string, len(ms))
		for i, m := range ms {
			out[i] = m.ID
		}
		return out
	}

	// hours <= 0 → everything, untouched.
	if got := ids(SelectHours(meetings, 0, 1)); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("hours=0 (all) = %v", got)
	}

	// 1h budget → exactly two whole meetings, ~1h, ID-sorted output.
	got1 := SelectHours(meetings, 1, 1)
	if len(got1) != 2 {
		t.Errorf("hours=1 selected %d meetings, want 2", len(got1))
	}
	if h := TotalHours(got1); h != 1.0 {
		t.Errorf("hours=1 total = %v h, want 1.0", h)
	}
	if g := ids(got1); !sort.StringsAreSorted(g) {
		t.Errorf("output not ID-sorted: %v", g)
	}

	// budget smaller than any single meeting still returns one WHOLE meeting.
	if got := SelectHours(meetings, 0.1, 1); len(got) != 1 {
		t.Errorf("hours=0.1 selected %d, want 1 (always a full meeting)", len(got))
	}

	// Deterministic: same seed → identical subset.
	if !reflect.DeepEqual(SelectHours(meetings, 1, 7), SelectHours(meetings, 1, 7)) {
		t.Error("same seed produced different subsets")
	}

	// The seed actually influences which meetings are drawn (not always the
	// alphabetical prefix): across seeds, the selection is not constant.
	distinct := map[string]bool{}
	for seed := int64(0); seed < 12; seed++ {
		distinct[strings.Join(ids(SelectHours(meetings, 1, seed)), ",")] = true
	}
	if len(distinct) < 2 {
		t.Errorf("seed did not vary the sample across 12 seeds (got %v)", distinct)
	}
}

// If the real generated fixture is present locally, it must parse and look sane.
// Skipped in CI where the (gitignored) media isn't fetched.
func TestRealSyntheticFixtureParses(t *testing.T) {
	path := filepath.Join("synthetic", "meeting_2spk.json")
	if _, err := os.Stat(path); err != nil {
		t.Skip("synthetic fixture not present")
	}
	m, err := LoadSyntheticMeeting(path)
	if err != nil {
		t.Fatalf("LoadSyntheticMeeting: %v", err)
	}
	if len(m.Turns) < 2 {
		t.Errorf("expected >=2 turns, got %d", len(m.Turns))
	}
	if m.Duration <= 0 {
		t.Errorf("Duration = %v, want > 0", m.Duration)
	}
	// turns must be ordered and non-overlapping for this alternating fixture
	for i := 1; i < len(m.Turns); i++ {
		if m.Turns[i].StartSeconds < m.Turns[i-1].StartSeconds {
			t.Errorf("turns not time-ordered at %d", i)
		}
	}
}
