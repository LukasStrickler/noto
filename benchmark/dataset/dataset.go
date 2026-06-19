// Package dataset loads the ground truth the benchmark scorers measure against:
// who spoke when (diarization turns, from RTTM) and what was said (reference
// words). It turns those into the shapes metrics.DER / metrics.WER / metrics.CpWER
// consume, so an atomic or e2e bench is "load a Meeting → score the provider's
// output against it".
//
// Three on-disk formats are understood, each with a tiny, well-specified parser:
//
//   - RTTM — NIST Rich Transcription. `SPEAKER <file> <chan> <start> <dur> <ortho>
//     <stype> <name> …`; we read start, dur, and the speaker name. This is the
//     diarization ground truth (the AMI sets ship it).
//   - synthetic meeting JSON — `{speakers, duration, turns:[{speaker_id,start,end}]}`,
//     the generated 2-speaker smoke fixture under synthetic/. Turns only, no words.
//   - words JSON — our canonical reference-transcript format:
//     `[{speaker,start,end,text}]`. Diarization sets (RTTM) carry no text, so the
//     fetchers convert AMI's word annotations into this once; the Go side only ever
//     reads this clean shape (decoupled from AMI's NXT/CTM idiosyncrasies).
//
// A NIST CTM parser is also provided for callers that have per-speaker CTM word
// files directly.
package dataset

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lukasstrickler/noto/benchmark/metrics"
)

// Turn is a single-speaker stretch of speech — the unit of diarization ground
// truth.
type Turn struct {
	Speaker      string
	StartSeconds float64
	EndSeconds   float64
}

// Word is one reference token with its speaker and timing — the unit of
// transcription ground truth.
type Word struct {
	Speaker      string
	StartSeconds float64
	EndSeconds   float64
	Text         string
}

// Meeting bundles a meeting's ground truth: where the audio is, who spoke when
// (Turns) and the reference words (Words, possibly empty for diarization-only
// sets). It converts itself into the shapes the scorers want.
type Meeting struct {
	ID        string
	AudioPath string
	Duration  float64
	Turns     []Turn
	Words     []Word
}

// AudioSeconds is the meeting's span: its declared Duration if set, else the
// latest turn/word end time. Used to budget quick benchmark runs by audio length.
func (m Meeting) AudioSeconds() float64 {
	if m.Duration > 0 {
		return m.Duration
	}
	var end float64
	for _, t := range m.Turns {
		if t.EndSeconds > end {
			end = t.EndSeconds
		}
	}
	for _, w := range m.Words {
		if w.EndSeconds > end {
			end = w.EndSeconds
		}
	}
	return end
}

// SelectHours returns a reproducible subset of meetings whose cumulative audio
// fits within `hours` — the quick-run budget so a dev iterates on a few minutes
// instead of the whole corpus.
//
// Selection is deterministic for a given `seed` (a seeded shuffle, then greedy
// fill), so two runs with the same -hours/-seed score the *same* meetings and
// are directly comparable — but the seed draws a representative spread rather
// than always the first few alphabetical sessions. It selects WHOLE meetings
// (the unit a provider transcribes and is scored on, so audio never desyncs from
// its reference) and always returns at least one — a partial 10-minute clip has
// nothing complete to score against. hours <= 0 returns every meeting unchanged.
//
// The returned subset is ordered by ID for tidy, stable reporting; the seed
// governs *which* meetings, not their order.
func SelectHours(meetings []Meeting, hours float64, seed int64) []Meeting {
	if hours <= 0 || len(meetings) == 0 {
		return meetings
	}
	shuffled := append([]Meeting(nil), meetings...)
	r := rand.New(rand.NewSource(seed))
	r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	budget := hours * 3600
	var (
		out   []Meeting
		total float64
	)
	for _, m := range shuffled {
		d := m.AudioSeconds()
		if len(out) > 0 && total+d > budget {
			break
		}
		out = append(out, m)
		total += d
		if total >= budget {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// TotalHours sums the audio length of a set of meetings, in hours.
func TotalHours(meetings []Meeting) float64 {
	var s float64
	for _, m := range meetings {
		s += m.AudioSeconds()
	}
	return s / 3600
}

// Segments converts the diarization turns to metrics.Segment for DER /
// attribution scoring.
func (m Meeting) Segments() []metrics.Segment {
	segs := make([]metrics.Segment, len(m.Turns))
	for i, t := range m.Turns {
		segs[i] = metrics.Segment{Speaker: t.Speaker, Start: t.StartSeconds, End: t.EndSeconds}
	}
	return segs
}

// Reference returns the whole reference transcript as one token stream in time
// order — the shape metrics.WER wants. Tokenization goes through metrics.Normalize
// so it matches exactly how a hypothesis is tokenized.
func (m Meeting) Reference() []string {
	ws := append([]Word(nil), m.Words...)
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].StartSeconds < ws[j].StartSeconds })
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = w.Text
	}
	return metrics.Normalize(strings.Join(parts, " "))
}

// ReferenceBySpeaker groups the reference tokens per speaker — the shape
// metrics.CpWER / metrics.SAWER want. Each speaker's words are concatenated in
// time order, then normalized as one block.
func (m Meeting) ReferenceBySpeaker() map[string][]string {
	bySpk := map[string][]Word{}
	for _, w := range m.Words {
		bySpk[w.Speaker] = append(bySpk[w.Speaker], w)
	}
	out := make(map[string][]string, len(bySpk))
	for spk, ws := range bySpk {
		sort.SliceStable(ws, func(i, j int) bool { return ws[i].StartSeconds < ws[j].StartSeconds })
		parts := make([]string, len(ws))
		for i, w := range ws {
			parts[i] = w.Text
		}
		toks := metrics.Normalize(strings.Join(parts, " "))
		if len(toks) > 0 {
			out[spk] = toks
		}
	}
	return out
}

// LoadRTTM reads an RTTM file into time-sorted turns.
func LoadRTTM(path string) ([]Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseRTTM(f)
}

// ParseRTTM parses RTTM SPEAKER lines into time-sorted turns. Non-SPEAKER and
// malformed lines are skipped, matching how the production persona eval reads
// these files.
func ParseRTTM(r io.Reader) ([]Turn, error) {
	var turns []Turn
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 8 || fields[0] != "SPEAKER" {
			continue
		}
		start, err1 := strconv.ParseFloat(fields[3], 64)
		dur, err2 := strconv.ParseFloat(fields[4], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		turns = append(turns, Turn{Speaker: fields[7], StartSeconds: start, EndSeconds: start + dur})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sortTurns(turns)
	return turns, nil
}

// ParseCTM parses NIST CTM lines (`<file> <chan> <start> <dur> <word> [conf]`)
// into words. CTM carries no speaker column, so the speaker for every word is the
// supplied label (AMI ships one CTM per participant).
func ParseCTM(r io.Reader, speaker string) ([]Word, error) {
	var words []Word
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ";;") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		start, err1 := strconv.ParseFloat(fields[2], 64)
		dur, err2 := strconv.ParseFloat(fields[3], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		words = append(words, Word{
			Speaker:      speaker,
			StartSeconds: start,
			EndSeconds:   start + dur,
			Text:         fields[4],
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return words, nil
}

// jsonWord is the on-disk form of a reference word.
type jsonWord struct {
	Speaker string  `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
}

// LoadWords reads our canonical reference-words JSON (`[{speaker,start,end,text}]`).
func LoadWords(path string) ([]Word, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseWords(f)
}

// ParseWords decodes the canonical reference-words JSON.
func ParseWords(r io.Reader) ([]Word, error) {
	var raw []jsonWord
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return nil, err
	}
	words := make([]Word, len(raw))
	for i, w := range raw {
		words[i] = Word{Speaker: w.Speaker, StartSeconds: w.Start, EndSeconds: w.End, Text: w.Text}
	}
	return words, nil
}

// syntheticMeeting is the on-disk form of the generated smoke fixture.
type syntheticMeeting struct {
	Speakers []string `json:"speakers"`
	Duration float64  `json:"duration"`
	Turns    []struct {
		SpeakerID string  `json:"speaker_id"`
		Start     float64 `json:"start"`
		End       float64 `json:"end"`
	} `json:"turns"`
}

// LoadSyntheticMeeting reads the generated 2-speaker fixture JSON
// (synthetic/meeting_2spk.json) into a Meeting. AudioPath points at the sibling
// .wav; the fixture carries diarization turns but no reference words.
func LoadSyntheticMeeting(jsonPath string) (*Meeting, error) {
	f, err := os.Open(jsonPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var sm syntheticMeeting
	if err := json.NewDecoder(f).Decode(&sm); err != nil {
		return nil, fmt.Errorf("decode synthetic meeting: %w", err)
	}
	id := strings.TrimSuffix(filepath.Base(jsonPath), filepath.Ext(jsonPath))
	m := &Meeting{
		ID:        id,
		AudioPath: filepath.Join(filepath.Dir(jsonPath), id+".wav"),
		Duration:  sm.Duration,
	}
	for _, t := range sm.Turns {
		m.Turns = append(m.Turns, Turn{Speaker: t.SpeakerID, StartSeconds: t.Start, EndSeconds: t.End})
	}
	sortTurns(m.Turns)
	return m, nil
}

func sortTurns(turns []Turn) {
	sort.SliceStable(turns, func(i, j int) bool {
		if turns[i].StartSeconds != turns[j].StartSeconds {
			return turns[i].StartSeconds < turns[j].StartSeconds
		}
		return turns[i].EndSeconds < turns[j].EndSeconds
	})
}
