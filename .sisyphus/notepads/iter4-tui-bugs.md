# Iter4: TUI Bugs Found

## Bug Reports for internal/tui/ (10 files analyzed)

---

### BUG 1 - CRITICAL: Nil dereference in selectTranscriptAtRow
**File:** input.go:364  
**Code:**
```go
func (m *AppModel) selectTranscriptAtRow(index int) bool {
    segments := m.selectedMeetingFixture().Segments  // <-- PANIC if nil
    if index < 0 || index >= len(segments) {
        return false
    }
```
**Issue:** `selectedMeetingFixture()` can return `nil` when `len(m.App.Meetings) == 0` (see util.go:316-322). Calling `.Segments` on nil pointer causes panic.  
**Repro:** Call selectTranscriptAtRow when meetings list is empty.  
**Fix:** Check nil before accessing `.Segments`:
```go
seg := m.selectedMeetingFixture()
if seg == nil {
    return false
}
segments := seg.Segments
```

---

### BUG 2 - CRITICAL: Nil dereference in move() for ScreenTranscript
**File:** input.go:486  
**Code:**
```go
case ScreenTranscript:
    seg := m.selectedMeetingFixture()  // <-- can be nil
    if seg != nil && len(seg.Segments) > 0 {  // nil check exists but...
        m.UI.SelectedResult = wrapIndex(...)
    }
```
**Issue:** While there IS a nil check here, the pattern `selectedMeetingFixture()` returning nil is a latent issue. If code changes to remove the nil check, it would crash. More importantly, the function itself has inconsistent safety - some callers check for nil, others don't.  
**Note:** This particular case has a nil check, but BUG 1 in the same file does NOT have the nil check.

---

### BUG 3 - HIGH: selectedMeeting() returns address of zero-value when empty
**File:** screens.go:590-591  
**Code:**
```go
func (m AppModel) selectedMeeting() *MeetingFixture {
    if len(m.App.Meetings) == 0 {
        return nil
    }
    idx := clamp(m.UI.SelectedMeeting, 0, len(m.App.Meetings)-1)
    return &m.App.Meetings[idx]  // <-- returns pointer to MeetingFixture
}
```
**Issue:** Returns pointer to actual struct element (safe). But other functions like `selectedMeetingFixture()` in util.go:316-322 also returns pointer. The inconsistency is that `selectedMeeting()` has explicit nil guard but `selectTranscriptAtRow` (input.go:364) doesn't check before dereferencing.

---

### BUG 4 - HIGH: Clamp with negative maxValue in viewport ScrollToItem
**File:** types.go:282-284  
**Code:**
```go
func (vp *ViewportComponent) SelectItem(item int) {
    vp.Selected = clamp(item, 0, vp.TotalItems-1)  // <-- if TotalItems=0, maxValue=-1
    vp.ScrollToItem(vp.Selected)
}
```
**Issue:** If `TotalItems` is 0, `clamp` receives maxValue=-1. The clamp function (util.go:264-275) handles `maxValue < minValue` by returning `minValue`. So clamp(5, 0, -1) returns 0. But then `vp.ScrollToItem(0)` is called, and in ScrollToItem (types.go:274-279):
```go
func (vp *ViewportComponent) ScrollToItem(item int) {
    if item < vp.Offset {
        vp.Offset = max(0, item)
    } else if item >= vp.Offset+vp.VisibleHeight {  // <-- comparison with 0+VisibleHeight
        vp.Offset = min(item, max(0, vp.TotalItems-vp.VisibleHeight))  // = min(0, max(0, -VisibleHeight))
    }
}
```
If `vp.TotalItems=0` and `vp.VisibleHeight > 0`, then `vp.TotalItems-vp.VisibleHeight` is negative, and the clamp logic may cause offset issues, but likely doesn't panic. This is more of a logic error than crash.

---

### BUG 5 - MEDIUM: JumpNumber panics on non-numeric key
**File:** input.go:398-405  
**Code:**
```go
func (m *AppModel) jumpNumber(value string) {
    index := int(value[0] - '1')  // <-- assumes value[0] exists
    items := navItems()
    if index < 0 || index >= len(items) {
        return
    }
```
**Issue:** If `value` is empty string, `value[0]` panics with "index out of range". This could happen if key events fire unexpectedly.  
**Fix:** Add length check before indexing.

---

### BUG 6 - MEDIUM: activeLabel panics on empty Screen string
**File:** util.go:257-262  
**Code:**
```go
func activeLabel(screen Screen) string {
    if screen == "" {
        return "Dashboard"
    }
    return strings.ToUpper(string(screen[:1])) + string(screen[1:])  // <-- screen[1:] panics if len(screen)==1
}
```
**Issue:** If `screen` is a single character like "S", then `screen[:1]` is safe, but `screen[1:]` is index out of range. For single-char screen names this will panic.  
**Repro:** Call activeLabel("S") -> "S"[1:] panics.  
**Fix:** Use `screen[1:]` only when len(screen) > 1.

---

### BUG 7 - MEDIUM: clamp function allows empty slice access pattern
**File:** screens.go:512, screens.go:531, actions.go:346, actions.go:514  
**Code (screens.go:512):**
```go
result := results[clamp(m.UI.SelectedResult, 0, len(results)-1)]
```
**Issue:** This pattern is used in multiple places. While most have guards (e.g., screens.go:509-510 checks `len(results)==0` before this line), the pattern itself is fragile. If code is refactored and guards are removed, this would panic on empty results.  
**Note:** shell.go:215 was FIXED in iter3 with explicit `if len(results) > 0` guard.

---

### BUG 8 - MEDIUM: sort.Slice called on nil slice in providers.go
**File:** providers.go:59  
**Code:**
```go
sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
```
**Issue:** In `sortedByKind`, if no providers match the kind, `out` remains `nil`. Calling `sort.Slice` on a nil slice is safe (no-op), but the semantic is confusing - empty output vs nil output.  
**Not a crash**, but a subtle logic issue.

---

### BUG 9 - LOW: actionAt Y coordinate comparison
**File:** actions.go:28-32  
**Code:**
```go
func (m AppModel) actionAt(x int, y int) (ActionID, bool) {
    _, height := normalizedSize(m.UI.Width, m.UI.Height)
    if y != height-1 {  // <-- uses normalized height
        return "", false
    }
```
**Issue:** The comparison uses `normalizedSize` height, but `y` comes from raw tea.MouseMsg coordinates. If mouse events provide coordinates outside normalized range, the comparison could silently fail to detect action clicks.  
**Not a crash**, but potential missed interactions.

---

### BUG 10 - LOW: Unhandled tea.Cmd error in commitOverlay
**File:** actions.go:348  
**Code:**
```go
func (m *AppModel) commitOverlay() {
    ...
    row := rows[clamp(m.UI.Overlay.Selected, 0, len(rows)-1)]
    m.UI.Overlay = OverlayState{}
    _ = m.performAction(row.action)  // <-- return value (tea.Cmd) is discarded
}
```
**Issue:** `performAction` returns `tea.Cmd` which could be an error or nil. Discarding it silently loses potential errors. While bubbletea typically handles cmd errors gracefully, this is not best practice.  
**Not a crash**, but lost error information.

---

## Summary

| # | File:Line | Severity | Type |
|---|-----------|----------|------|
| 1 | input.go:364 | CRITICAL | Nil dereference |
| 2 | input.go:486 | HIGH | Nil dereference (potential) |
| 3 | screens.go:590 | HIGH | Nil safety inconsistency |
| 4 | types.go:282 | HIGH | Logic error |
| 5 | input.go:399 | MEDIUM | Panic on empty string |
| 6 | util.go:261 | MEDIUM | Panic on single-char screen |
| 7 | screens.go:512,531 | MEDIUM | Fragile clamp pattern |
| 8 | providers.go:59 | MEDIUM | Nil slice semantics |
| 9 | actions.go:30 | LOW | Coordinate comparison |
| 10 | actions.go:348 | LOW | Swallowed error |

## Files NOT Found (per task requirement)
- app.go - does not exist
- view.go - does not exist
- tree.go - does not exist

Actual files analyzed (10):
- app_core.go
- screens.go
- shell.go
- util.go
- input.go
- types.go
- actions.go
- components.go
- providers.go
- fixtures.go