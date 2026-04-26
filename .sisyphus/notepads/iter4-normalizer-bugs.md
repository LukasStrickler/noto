# Normalizer Bugs Report - Iteration 4

## Files Analyzed
- `internal/providers/normalization.go` (183 lines)
- `internal/providers/speech/normalizers.go` (516 lines)

## Bugs Found

---

### Bug 1: Division by Zero in DiarizationNormalizer (CRITICAL - Crash)
**Location:** `normalizers.go:79`  
**Severity:** Crash / Data Corruption  
**Type:** Division by zero when merging segments with zero duration

```go
currentDuration := current.EndSeconds - current.StartSeconds
segDuration := seg.EndSeconds - seg.StartSeconds
totalDuration := currentDuration + segDuration
weightedAvg := (*current.Confidence*currentDuration + *seg.Confidence*segDuration) / totalDuration
```

**Problem:** When `currentDuration + segDuration == 0` (both segments have same start and end time), division by zero occurs. This causes a runtime panic.

**Trigger Condition:** Two adjacent segments from the same speaker with identical timestamps.

---

### Bug 2: Nil Pointer Dereference in DiarizationNormalizer (CRITICAL - Crash)
**Location:** `normalizers.go:89`  
**Severity:** Crash  
**Type:** Nil pointer dereference when `current` is nil

```go
merged = append(merged, *current)
```

**Problem:** If the segment loop never initializes `current` (e.g., if `transcript.Segments` is empty after the nil check passes), then `current` remains nil. The final append of `*current` causes a nil pointer dereference.

**Note:** While there's a nil check for the whole transcript (line 44-46), the segments slice could be empty but not nil, causing `current` to never be set.

---

### Bug 3: Missing Deep Copy of Speaker in NormalizeSpeakerLabels (MEDIUM - Data Corruption)
**Location:** `normalization.go:94-101`  
**Severity:** Data potential corruption / aliasing  
**Type:** Shallow copy issue with nested slices

```go
normalized := artifacts.Speaker{
    ID:            speakerMap[canonicalLabel],
    Label:         canonicalLabel,
    Origin:        speaker.Origin,
    DefaultSourceID: speaker.DefaultSourceID,
    DisplayName:   speaker.DisplayName,
    ProviderLabel: speaker.ProviderLabel,
}
result.Speakers = append(result.Speakers, normalized)
```

**Problem:** The `artifacts.Speaker` struct contains no pointer fields, so a shallow copy is safe here. However, this should be verified against the Speaker struct definition. If the struct ever changes to include slices or pointers, this becomes a bug.

**Current Status:** Struct contains only value types (strings), so this is not currently exploitable. Marking as low risk but flagged for future awareness.

---

### Bug 4: Integer Conversion Bug in itoa (MEDIUM - Logic Error)
**Location:** `normalization.go:156-167`  
**Severity:** Logic error  
**Type:** Incorrect integer to string conversion for value 0

```go
func itoa(n int) string {
    if n == 0 {
        return "0"
    }
    digits := "0123456789"
    var result []byte
    for n > 0 {
        result = append([]byte{digits[n%10]}, result...)
        n /= 10
    }
    return string(result)
}
```

**Problem:** The function correctly handles `n == 0` by returning "0". However, the loop logic `for n > 0` would skip zero, but the explicit check handles it. This is actually correct. **BUG NOT CONFIRMED** - the explicit check for `n == 0` makes this work correctly.

**Re-evaluation:** This function is actually correct. The early return for zero handles the edge case properly.

---

### Bug 5: ContainsHelper Has Off-by-One Error (LOW - Logic Error)
**Location:** `normalization.go:173-183`  
**Severity:** Potential incorrect behavior  
**Type:** Off-by-one error in string search

```go
func containsHelper(s, substr string) bool {
    if len(s) < len(substr) {
        return false
    }
    for i := 0; i <= len(s)-len(substr); i++ {
        if s[i:i+len(substr)] == substr {
            return true
        }
    }
    return false
}
```

**Problem:** When `len(s) == len(substr)`, the loop runs once with `i=0`, checking `s[0:len(substr)] == substr` which is `s == substr`. This is correct behavior. However, the redundant check `s == substr` before calling `containsHelper` in the parent function suggests there may be confusion about correctness.

**Current Analysis:** The contains function has redundant logic - it first checks `s == substr` (exact equality) before calling containsHelper. This is likely an optimization to avoid the loop for exact matches. The containsHelper itself appears correct.

---

### Bug 6: Segment Reference Not Copied in NormalizeSpeakerLabels (HIGH - Data Loss)
**Location:** `normalization.go:125`  
**Severity:** Data loss potential  
**Type:** Incomplete deep copy

```go
result.Segments[i] = seg
result.Segments[i].SpeakerID = newSpeakerID
```

**Problem:** `result.Segments` is pre-allocated with `make([]artifacts.Segment, len(transcript.Segments))` at line 74. This creates elements with zero values. The code then assigns `seg` to `result.Segments[i]`, but the `seg.WordIDs` slice is a reference to the original slice, not a copy. When the subsequent line modifies `result.Segments[i].SpeakerID`, it modifies the copied struct but the `WordIDs` slice still shares the underlying array with the original.

**Impact:** If the original transcript is modified later (which shouldn't happen but is possible in concurrent scenarios), the normalized transcript's WordIDs could be affected.

**Severity:** Low in practice since transcripts are typically processed in isolation, but violates the principle of defensive copying.

---

### Bug 7: copySegment Doesn't Copy WordIDs Slice Properly (HIGH - Data Corruption)
**Location:** `normalizers.go:508` and `normalizers.go:514`  
**Severity:** Data corruption  
**Type:** Slice aliasing in deep copy

```go
func copySegment(s artifacts.Segment) artifacts.Segment {
    result := artifacts.Segment{
        ...
        WordIDs:      make([]string, len(s.WordIDs)),
    }
    ...
    copy(result.WordIDs, s.WordIDs)
    return result
}
```

**Problem:** The `WordIDs` field is correctly deep-copied using `make([]string, len(s.WordIDs))` followed by `copy()`. This is correct! The string slice elements themselves are values, so copying the slice header and then copying the elements is the right approach.

**Re-evaluation:** This is actually CORRECT. The `make()` creates a new slice header, and `copy()` copies all elements. No aliasing occurs.

---

### Bug 8: contains() Function Has Logic Issue (LOW - Incorrect Behavior)
**Location:** `normalization.go:169-171`  
**Severity:** Incorrect behavior edge case  
**Type:** String contains logic error

```go
func contains(s, substr string) bool {
    return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}
```

**Problem:** When `s` is empty (`len(s) == 0`) and `substr` is also empty (`len(substr) == 0`):
- First condition: `len(s) >= len(substr)` → `0 >= 0` → true
- Second condition: `s == substr` → `"" == ""` → true
- Returns true

This is actually correct behavior - an empty string contains an empty substring.

When `s` is non-empty but `substr` is empty:
- First condition: `len(s) >= 0` → true (always)
- Second condition: `s == substr` → `"" == "something"` → false
- Then: `len(s) > 0 && containsHelper(s, substr)` - `containsHelper` with empty substr...

When `substr` is empty and we call `containsHelper`:
- `len(s) < len(substr)` → `len(s) < 0` → false
- Loop runs with `i <= len(s)`, checking `s[i:i+0]` which is `s[i:i]` → always "" → matches empty substr

So `contains("", "")` returns true, `contains("abc", "")` returns true.

This is actually standard string library behavior - an empty substring is contained in any string.

**Re-evaluation:** This is CORRECT behavior.

---

### Bug 9: Missing Error Check in TranscriptNormalizers.Normalize (MEDIUM - Error Swallowing)
**Location:** `normalizers.go:463-467`  
**Severity:** Error handling  
**Type:** Partial error handling

```go
func (c TranscriptNormalizers) Normalize(transcript *artifacts.Transcript) (*artifacts.Transcript, error) {
    result := transcript
    for _, normalizer := range c {
        var err error
        result, err = normalizer.Normalize(result)
        if err != nil {
            return nil, err
        }
    }
    return result, nil
}
```

**Problem:** The error handling looks correct - it returns the error if any normalizer fails. However, the initial `result := transcript` is a reference, not a copy. If the first normalizer returns `nil, nil` (like NopNormalizer does for nil input), then subsequent normalizers might receive nil and crash.

**Not currently exploitable** since all normalizers handle nil inputs properly by returning nil, nil.

---

### Bug 10: canonicalizeSpeakerLabel Has Potential Panic (MEDIUM - Crash Risk)
**Location:** `normalization.go:138-141`  
**Severity:** Crash potential  
**Type:** Index out of bounds if label is too short

```go
func canonicalizeSpeakerLabel(label string) string {
    if label == "" {
        return "speaker_1"
    }

    switch {
    case len(label) > 8 && label[:8] == "speaker_":
        return label
    case len(label) > 4 && label[:4] == "spk_":
        return "speaker_" + label[4:]
    default:
        return label
    }
}
```

**Problem:** The checks `len(label) > 8` and `len(label) > 4` ensure safe slicing. This is CORRECT - no panic possible here.

---

## Summary

| Bug # | Severity | Type | Location | Status |
|-------|----------|------|----------|--------|
| 1 | CRITICAL | Division by zero | normalizers.go:79 | CONFIRMED |
| 2 | CRITICAL | Nil pointer dereference | normalizers.go:89 | CONFIRMED |
| 3 | MEDIUM | Shallow copy awareness | normalization.go:94-101 | NOT EXPLOITABLE |
| 4 | - | Logic error | normalization.go:156-167 | FALSE POSITIVE |
| 5 | LOW | Logic concern | normalization.go:173-183 | NOT EXPLOITABLE |
| 6 | HIGH | Slice aliasing | normalization.go:125 | CONFIRMED |
| 7 | - | Slice copy | normalizers.go:508-514 | FALSE POSITIVE (CORRECT) |
| 8 | - | Contains behavior | normalization.go:169-171 | FALSE POSITIVE (CORRECT) |
| 9 | MEDIUM | Error handling concern | normalizers.go:463-467 | NOT EXPLOITABLE |
| 10 | - | Panic risk | normalization.go:138-141 | FALSE POSITIVE (CORRECT) |

## Confirmed Bugs Requiring Fixes:

1. **Bug 1**: Division by zero when both segment durations sum to zero
2. **Bug 2**: Nil pointer dereference when segment list is empty but not nil
3. **Bug 6**: Segment.WordIDs slice not properly deep copied

## Files to Modify:
- `internal/providers/speech/normalizers.go` - Fix bugs 1 and 2
- `internal/providers/normalization.go` - Fix bug 6