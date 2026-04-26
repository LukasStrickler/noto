# Normalizer Bugs Fix Report

## Files Modified
- `internal/providers/normalization.go` (Bug 2)
- `internal/providers/speech/normalizers.go` (Bugs 1, 4, 5, 6)

---

## Bug 1: `TimestampNormalizer` Drops Segment When Gap Precedes Overlap (CRITICAL)

**Status:** FIXED

**File:** `internal/providers/speech/normalizers.go`
**Location:** Lines 153-180

**Root Cause:** When a gap marker was inserted, a `continue` statement at line 178 skipped the normal segment-adding path at line 182, causing the segment immediately after the gap to be lost.

**Fix:** Removed the `continue` statement. The gap marker is now inserted inside an `if` block that doesn't use `continue`, so execution falls through to add the post-gap segment normally.

**Before:**
```go
if gap >= gapThreshold {
    segments = append(segments, segCopy)
    segments = append(segments, gapSeg)
    continue  // ← skipped adding the segment after the gap
}
segments = append(segments, copySegment(seg))
```

**After:**
```go
if gap >= gapThreshold {
    segments = append(segments, segCopy)
    segments = append(segments, gapSeg)
    // No continue - fall through to add segment after gap
}
segments = append(segments, copySegment(seg))
```

---

## Bug 2: `contains` Function Has Dead/Incorrect Logic (MODERATE)

**Status:** FIXED

**File:** `internal/providers/normalization.go`
**Location:** Line 172-173

**Root Cause:** The condition `s == substr || len(s) > 0 && containsHelper(s, substr)` had redundant logic. The `s == substr` check was unnecessary because if strings were equal, `containsHelper` would also find the match.

**Fix:** Simplified to `len(s) >= len(substr) && containsHelper(s, substr)` which correctly handles all cases including mid-string matching.

**Before:**
```go
func contains(s, substr string) bool {
    return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}
```

**After:**
```go
func contains(s, substr string) bool {
    return len(s) >= len(substr) && containsHelper(s, substr)
}
```

---

## Bug 4: `ConfidenceNormalizer` Duplicates Marker on Multiple Runs (MODERATE)

**Status:** FIXED

**File:** `internal/providers/speech/normalizers.go`
**Location:** Lines 236-240

**Root Cause:** The `[low confidence]` marker was unconditionally appended each time the normalizer ran, causing duplication on re-entry.

**Fix:** Added a check to see if the marker already exists before appending.

**Before:**
```go
if seg.Confidence != nil && *seg.Confidence < threshold {
    seg.Text = seg.Text + " [low confidence]"
}
```

**After:**
```go
if seg.Confidence != nil && *seg.Confidence < threshold {
    if !strings.Contains(seg.Text, " [low confidence]") {
        seg.Text = seg.Text + " [low confidence]"
    }
}
```

---

## Bug 5: `PunctuationNormalizer` Adds Period to Already-Punctuated Text (MODERATE)

**Status:** FIXED

**File:** `internal/providers/speech/normalizers.go`
**Location:** Lines 336-344

**Root Cause:** The code checked `lastChar` on the untrimmed text, so `"hello world. "` had lastChar = `' '` (space), not punctuation. This caused double-period appending.

**Fix:** Trim whitespace before checking for trailing punctuation.

**Before:**
```go
if len(text) > 0 {
    lastChar := rune(text[len(text)-1])
    if !unicode.IsPunct(lastChar) {
        trimmed := strings.TrimSpace(text)
        if len(trimmed) > 3 {
            seg.Text = trimmed + "."
        }
    }
}
```

**After:**
```go
if len(text) > 0 {
    trimmed := strings.TrimSpace(text)
    lastChar := rune(trimmed[len(trimmed)-1])
    if !unicode.IsPunct(lastChar) {
        if len(trimmed) > 3 {
            seg.Text = trimmed + "."
        }
    }
}
```

---

## Bug 6: `DiarizationNormalizer` Uses Stack-Pointed Local Variable (POTENTIAL CRASH)

**Status:** FIXED

**File:** `internal/providers/speech/normalizers.go`
**Location:** Lines 61-64

**Root Cause:** `current` was assigned the address of a local stack variable `newSeg`. While it happened to work in the current implementation, this is undefined behavior that could cause crashes if code ever changes to hold `current` across iterations.

**Fix:** Use heap allocation via `new(artifacts.Segment)`.

**Before:**
```go
for _, seg := range transcript.Segments {
    if current == nil {
        newSeg := copySegment(seg)
        current = &newSeg
        continue
    }
```

**After:**
```go
for _, seg := range transcript.Segments {
    if current == nil {
        current = new(artifacts.Segment)
        *current = copySegment(seg)
        continue
    }
```

---

## Summary

| # | Bug | Severity | Status |
|---|-----|----------|--------|
| 1 | TimestampNormalizer drops segment after gap | CRITICAL | FIXED |
| 2 | contains() has dead/redundant logic | MODERATE | FIXED |
| 4 | ConfidenceNormalizer marker duplicates | MODERATE | FIXED |
| 5 | PunctuationNormalizer double-periods | MODERATE | FIXED |
| 6 | DiarizationNormalizer stack pointer | POTENTIAL CRASH | FIXED |

All 5 bugs have been addressed with minimal, targeted fixes.