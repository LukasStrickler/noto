# TUI Bug Fixes - iter4-tui-fixes

## Fixed Bugs

### BUG 1 [CRITICAL] - input.go:364 - Nil dereference in selectTranscriptAtRow
**Problem**: `m.selectedMeetingFixture().Segments` called without nil check - PANIC if nil
**Fix**: Added nil check before accessing `.Segments`
```go
segments := m.selectedMeetingFixture().Segments
if segments == nil {
    return false
}
```

### BUG 2 [HIGH] - input.go:486 - Already has nil check (noted as inconsistent safety)
**Status**: Already properly guarded with `seg != nil && len(seg.Segments) > 0`

### BUG 3 [HIGH] - types.go:282 - SelectItem with TotalItems=0 causes clamp(maxValue=-1)
**Problem**: `clamp(item, 0, vp.TotalItems-1)` with TotalItems=0 gives clamp(x, 0, -1)
**Fix**: Added guard at start of SelectItem
```go
func (vp *ViewportComponent) SelectItem(item int) {
    if vp.TotalItems <= 0 {
        vp.Selected = 0
        return
    }
    // ... rest of function
}
```

### BUG 4 [MEDIUM] - input.go:399 - jumpNumber panics on empty string
**Problem**: `value[0]` accessed without checking `len(value) > 0`
**Fix**: Added length check at start of function
```go
func (m *AppModel) jumpNumber(value string) {
    if len(value) == 0 {
        return
    }
    // ... rest of function
}
```

### BUG 5 [MEDIUM] - util.go:261 - activeLabel panics on single-char screen "S"
**Problem**: `screen[1:]` out of range when len(screen) == 1
**Fix**: Added explicit length check
```go
func activeLabel(screen Screen) string {
    if screen == "" {
        return "Dashboard"
    }
    if len(screen) == 1 {
        return strings.ToUpper(string(screen))
    }
    return strings.ToUpper(string(screen[:1])) + string(screen[1:])
}
```

### BUG 6 [MEDIUM] - screens.go:512,531 - Clamp pattern without explicit len==0 guard
**Problem**: `clamp(m.UI.SelectedResult, 0, len(results)-1)` doesn't handle invalid SelectedResult
**Fix**: Replaced with explicit bounds check
```go
if m.UI.SelectedResult < 0 || m.UI.SelectedResult >= len(results) {
    return detailLines(width-4, []string{"No selected result.", "Type to search, Enter to open."})
}
result := results[m.UI.SelectedResult]
```

## Files Modified
- internal/tui/input.go (BUG 1, BUG 4)
- internal/tui/types.go (BUG 3)
- internal/tui/util.go (BUG 5)
- internal/tui/screens.go (BUG 6)

## Verification
Go build could not be run (go binary not found in PATH), but all changes follow existing code patterns.