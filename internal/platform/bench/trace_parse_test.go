package bench_test

import (
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/bench"
)

func TestParsePyannoteStderr_StagesAndVADKept(t *testing.T) {
	// kept= precedes the stage list so it is not swept into StagesMS; this
	// mirrors the real pyannote server line order (`vad=Xms kept=Y [stages]`).
	line := "timing worker=0 kept=0.83 stages_ms segmentation=120.5 embeddings=890.2 clustering=45.0"
	got := bench.ParsePyannoteStderr(line)

	want := map[string]float64{"segmentation": 120.5, "embeddings": 890.2, "clustering": 45.0}
	if len(got.StagesMS) != len(want) {
		t.Fatalf("StagesMS = %v, want %v", got.StagesMS, want)
	}
	for k, v := range want {
		if got.StagesMS[k] != v {
			t.Errorf("StagesMS[%q] = %v, want %v", k, got.StagesMS[k], v)
		}
	}
	if got.VADKept != 0.83 {
		t.Errorf("VADKept = %v, want 0.83", got.VADKept)
	}
}

func TestParsePyannoteStderr_NoMatch(t *testing.T) {
	got := bench.ParsePyannoteStderr("nothing useful here")
	if len(got.StagesMS) != 0 {
		t.Errorf("StagesMS = %v, want empty", got.StagesMS)
	}
	if got.VADKept != 0 {
		t.Errorf("VADKept = %v, want 0", got.VADKept)
	}
}

// The LIVE pyannote server logs the stages bracketed and prefixed with
// [pyannote-server], with NO `stages_ms` token. The parser must read this real
// format (the [pyannote-server] prefix has no '=', so it isn't mistaken for the
// stage group) — previously StagesMS came back empty on every real run.
func TestParsePyannoteStderr_BracketedLiveFormat(t *testing.T) {
	line := "[pyannote-server] timing worker=1 wav=ES2002a.wav load=120.0ms vad=80.0ms kept=0.85 [segmentation=800 embeddings=4800 clustering=200]"
	got := bench.ParsePyannoteStderr(line)

	want := map[string]float64{"segmentation": 800, "embeddings": 4800, "clustering": 200}
	if len(got.StagesMS) != len(want) {
		t.Fatalf("StagesMS = %v, want %v", got.StagesMS, want)
	}
	for k, v := range want {
		if got.StagesMS[k] != v {
			t.Errorf("StagesMS[%q] = %v, want %v", k, got.StagesMS[k], v)
		}
	}
	if got.VADKept != 0.85 {
		t.Errorf("VADKept = %v, want 0.85", got.VADKept)
	}
}

func TestParsePyannoteStderr_IgnoresMalformedNumbers(t *testing.T) {
	// segmentation=abc has no numeric value, so the (\w+)=([\d.]+) pair regex
	// skips it; the well-formed embeddings pair still parses.
	got := bench.ParsePyannoteStderr("stages_ms segmentation=abc embeddings=12.0")
	if _, ok := got.StagesMS["segmentation"]; ok {
		t.Errorf("segmentation should be dropped, got %v", got.StagesMS["segmentation"])
	}
	if got.StagesMS["embeddings"] != 12.0 {
		t.Errorf("embeddings = %v, want 12.0", got.StagesMS["embeddings"])
	}
}

func TestLoadPyannoteStageMap_DefaultsOnEmpty(t *testing.T) {
	m, err := bench.LoadPyannoteStageMap(nil)
	if err != nil {
		t.Fatal(err)
	}
	if m["embeddings"] != "diar_emb" || m["segmentation"] != "diar_seg" {
		t.Errorf("default map missing canonical stages: %v", m)
	}
}

func TestLoadPyannoteStageMap_OverrideAndError(t *testing.T) {
	m, err := bench.LoadPyannoteStageMap([]byte(`{"foo":"bar"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m["foo"] != "bar" {
		t.Errorf("override not applied: %v", m)
	}
	if _, err := bench.LoadPyannoteStageMap([]byte(`{not json`)); err == nil {
		t.Error("expected error on malformed JSON")
	}
}

func TestPyannoteStageMap_MapStages(t *testing.T) {
	// Known hooks map to canonical IDs; an unknown hook lands in "unattributed".
	out := bench.DefaultPyannoteStageMap.MapStages(map[string]float64{
		"segmentation": 100, "embeddings": 200, "mystery_step": 50,
	})
	if out["diar_seg"] != 100 || out["diar_emb"] != 200 {
		t.Errorf("canonical mapping wrong: %v", out)
	}
	if out["unattributed"] != 50 {
		t.Errorf("unknown hook should be unattributed, got %v", out)
	}
}

func TestPyannoteStageMap_MapStages_SumsCollidingHooks(t *testing.T) {
	// Two hooks that map to the same canonical ID accumulate.
	m := bench.PyannoteStageMap{"a": "diar_emb", "b": "diar_emb"}
	out := m.MapStages(map[string]float64{"a": 1.5, "b": 2.5})
	if out["diar_emb"] != 4.0 {
		t.Errorf("diar_emb = %v, want 4.0 (summed)", out["diar_emb"])
	}
}

func TestMeetingHyp_Key(t *testing.T) {
	if got := (bench.MeetingHyp{MeetingID: "ES2002a", ID: "x"}).Key(); got != "ES2002a" {
		t.Errorf("Key = %q, want ES2002a (MeetingID wins)", got)
	}
	if got := (bench.MeetingHyp{ID: "fallback"}).Key(); got != "fallback" {
		t.Errorf("Key = %q, want fallback (ID when MeetingID empty)", got)
	}
}
