// Package metrics provides pure, deterministic accuracy and speed metrics for
// the noto meeting → transcript benchmark harness. Nothing here loads a model,
// reads audio, or touches the network, so every function is unit-testable with
// tiny hand-built fixtures and runs in the normal `go test ./...` suite.
//
// Accuracy metrics:
//
//   - WER  — token-level word error rate (Levenshtein, split into sub/del/ins).
//   - DER  — diarization error rate (missed + false-alarm + confusion over the
//     reference speech time), with an optimal speaker-label mapping and an
//     optional scoring collar around reference boundaries.
//   - cpWER — concatenated minimum-permutation WER: the joint "who said what"
//     metric. Words are concatenated per speaker and the hypothesis speakers are
//     assigned to reference speakers to MINIMIZE total word errors, so cpWER is
//     diarization-permutation-agnostic (it forgives swapped anonymous labels but
//     still penalizes split/merged speakers and transcription errors).
//   - SA-WER — speaker-attributed WER: the same concatenation but under a FIXED
//     hypothesis→reference speaker mapping (e.g. once identity has assigned real
//     names), so attributing a turn to the wrong person is penalized.
//   - AttributionAccuracy — fraction of reference speech time given the right
//     speaker under the optimal mapping.
//
// Speed metric:
//
//   - Timing / RTF — wall time ÷ audio seconds for a pipeline stage. RTF < 1
//     means faster than real time. This is hardware-dependent; record the
//     compute backend alongside it.
//
// The DER collar default is 0.25 s and overlapping reference speech is scored by
// default (the honest worst case for meeting audio). cpWER and DER both use the
// O(n³) Hungarian assignment in hungarian.go so they scale past a handful of
// speakers without a factorial permutation search.
package metrics
