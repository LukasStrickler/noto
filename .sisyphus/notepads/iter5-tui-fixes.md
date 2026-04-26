# Iter5: TUI Bug Fixes

## Summary of Fixes Applied

| Bug # | File | Severity | Status | Fix Description |
|-------|------|----------|--------|------------------|
| 1 | util.go:233 | HIGH | FIXED | Rewrote fit() to accumulate runes by visual width (lipgloss.Width) instead of rune count. Now correctly handles CJK/emoji text. |
| 2 | input.go:324 | HIGH | FIXED | Removed hardcoded row index mapping in selectProviderAtRow(). Now uses direct row-to-index mapping that matches providerRows() length. |
| 3 | input.go:144,519,493 | HIGH | FIXED | Added CachedSearchResults to UIState and cache search results in syncSearch(). filteredSearchResults() now returns cached results to prevent inconsistent state across multiple calls. |
| 4 | screens.go:226,434,556 | MEDIUM | ACKNOWLEDGED | Bug report noted the max(0, totalItems-visibleHeight) pattern is actually correct per Go's clamp implementation. No code change needed. |
| 5 | util.go:146 | MEDIUM | FIXED | filteredSearchResults() no longer recreates fixtureStore on every call. Now returns m.App.SearchResults when Runtime.Meetings is nil. |
| 6 | util.go:257 | MEDIUM | KNOWN | Re-assessed as false positive - the len==1 case is already handled correctly by the second branch. |
| 7 | actions.go:371,435 | MEDIUM | FIXED | Added context.WithTimeout(5*time.Second) to Secrets.Set() and Secrets.Remove() calls to prevent potential UI freeze. |
| 8 | components.go:98 | LOW | FIXED | Added bounds clamping for ANSI cursor positioning coordinates to ensure they stay within terminal bounds. |
| 9 | util.go:267 | LOW | KNOWN | Verified clamp function has proper guards. No code change needed. |
| 10 | providers.go:59 | LOW | FIXED | Changed sort.Slice to sort.SliceStable to ensure stable sort order for providers with equal IDs. |

## Files Modified

- internal/tui/util.go
- internal/tui/input.go
- internal/tui/screens.go
- internal/tui/actions.go
- internal/tui/components.go
- internal/tui/providers.go
- internal/tui/types.go
- internal/tui/app_core.go

## Bugs NOT Fixed (by design)

- Bug 4: The viewport offset calculation was flagged but the analysis showed the existing clamp() guards handle edge cases correctly.
- Bug 6: Re-assessed as false positive - the code already handles single-character screens correctly.
- Bug 9: Verified the clamp function already has proper guards against division edge cases.
