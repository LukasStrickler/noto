# Normalizer Fixes Report - Iteration 4

## Files Modified
- `internal/providers/normalization.go`
- `internal/providers/speech/normalizers.go`

## Bugs Fixed

---

### Bug 1: Division by Zero in DiarizationNormalizer (CRITICAL - Crash)
**Location:** `normalizers.go:79`  
**Status:** FIXED

**Original Code:**
```go
totalDuration := currentDuration + segDuration
weightedAvg := (*current.Confidence*currentDuration + *seg.Confidence*segDuration) / totalDuration
```

**Fixed Code:**
```go
totalDuration := currentDuration + segDuration
if totalDuration > 0 {
    weightedAvg := (*current.Confidence*currentDuration + *seg.Confidence*segDuration) / totalDuration
    current.Confidence = &weightedAvg
}
```

**Explanation:** Added a guard to check `totalDuration > 0` before performing division. When both segments have zero duration (identical start and end times), the division would cause a runtime panic. Now we simply skip the confidence merge when duration is zero, preserving whatever confidence value was already set.

---

### Bug 2: Nil Pointer Dereference in DiarizationNormalizer (CRITICAL - Crash)
**Location:** `normalizers.go:89` (line 96-98 after fix)  
**Status:** NO CHANGE NEEDED (Already protected)

**Analysis:** The code already has proper nil protection:
```go
if current != nil {
    merged = append(merged, *current)
}
```

This check at line 96-98 prevents nil pointer dereference. However, I verified this by re-reading the code and confirming the loop initializes `current` before any potential append, and the nil check guards the final append.

**Conclusion:** No code change required. The existing nil check is correct and sufficient.

---

### Bug 3: Segment WordIDs Slice Aliasing in NormalizeSpeakerLabels (HIGH - Data Loss)
**Location:** `normalization.go:125-129`  
**Status:** FIXED

**Original Code:**
```go
result.Segments[i] = seg
result.Segments[i].SpeakerID = newSpeakerID
```

**Fixed Code:**
```go
copiedSeg := seg
copiedSeg.WordIDs = make([]string, len(seg.WordIDs))
copy(copiedSeg.WordIDs, seg.WordIDs)
copiedSeg.SpeakerID = newSpeakerID
result.Segments[i] = copiedSeg
```

**Explanation:** The original code performed a shallow copy of `seg` and then modified the `SpeakerID` field. However, `seg.WordIDs` is a slice (a reference type in Go), so the "copy" still pointed to the same underlying array as the original segment's WordIDs. This caused potential data aliasing where modifications to one segment's WordIDs could affect another.

The fix creates a proper deep copy by:
1. Copying the struct value (which copies value fields like strings, floats, bools)
2. Explicitly allocating a new slice for WordIDs with `make([]string, len(seg.WordIDs))`
3. Copying the string elements with `copy()` which duplicates the actual data

This ensures the normalized transcript's segments have independent WordIDs arrays.

---

## Summary of Changes

| Bug | Severity | File | Change Made |
|-----|----------|------|-------------|
| 1 | CRITICAL | normalizers.go:79 | Added `totalDuration > 0` guard before division |
| 2 | CRITICAL | normalizers.go | No change needed - already protected |
| 3 | HIGH | normalization.go:125-129 | Added proper deep copy for WordIDs slice |

## Verification

### Syntax Check
The changes are syntactically valid Go:
- The `if totalDuration > 0` check is a standard conditional
- The `copiedSeg := seg` creates a value copy of the struct
- `make([]string, len(seg.WordIDs))` properly allocates a new slice
- `copy()` is the standard way to copy slice elements in Go

### Logic Verification
- **Bug 1 fix:** When `totalDuration == 0`, we skip the weighted average calculation but still merge the segment text and WordIDs. This is correct behavior - we preserve the merged content but don't calculate a new confidence (the old confidence value remains on `current`).
- **Bug 3 fix:** The new code correctly creates independent copies of the WordIDs slice, preventing any aliasing issues between the original and normalized transcripts.

## Notes

- Bug 2 (nil pointer) was already properly handled in the original code. The existing `if current != nil` check at line 96 is correct and sufficient.
- Additional bugs were investigated but found to be false positives or not exploitable:
  - Bug 4 (itoa function): Actually correct - handles zero case properly
  - Bug 5 (containsHelper): Actually correct - edge cases handled properly
  - Bug 7 (copySegment WordIDs): Actually correct - properly deep copies
  - Bug 8 (contains function): Actually correct - matches standard library behavior
  - Bug 9 (error handling): Actually correct - properly returns errors
  - Bug 10 (canonicalizeSpeakerLabel): Actually correct - bounds checks prevent panic

## Files Summary

### `internal/providers/speech/normalizers.go`
- Line 79: Added division-by-zero guard

### `internal/providers/normalization.go`
- Lines 125-129: Replaced shallow copy with proper deep copy for WordIDs slice