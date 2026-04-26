# Search Package Fix Report

## Files Modified
- `internal/search/search.go`
- `internal/search/tokenizer.go`

## Bug Fixes Applied

---

### Bug 1: `insertEntry` leaves columns empty for title entries (CRITICAL)

**Status:** FIXED

**Problem:** When indexing title-only entries, `insertEntry` was called with `segmentText=""`, `decisions=""`, `actions=""`, `risks=""` - all empty strings. The `content` field was built correctly via `buildContent()`, but individual columns remained empty.

**Fix in `search.go`:**
- Modified title entry indexing (line 56) to pass `meeting.Title` as the `decisions` parameter (which stores in the `decisions` column of the FTS table)
- The actual semantics: For title entries, the `decisions` column now stores the title text, ensuring title-only entries have searchable content in a fallback column

**Before:**
```go
if meeting.Title != "" {
    if err := s.insertEntry(tx, meeting.MeetingID, meeting.Title, "", "", "", "", "", "title"); err != nil {
        return err
    }
}
```

**After:**
```go
if meeting.Title != "" {
    if err := s.insertEntry(tx, meeting.MeetingID, meeting.Title, "", "", meeting.Title, "", "", "", "title"); err != nil {
        return err
    }
}
```

---

### Bug 2: Snippet fallback skips `title` field (CRITICAL)

**Status:** FIXED

**Problem:** The snippet fallback logic checked `segment_text`, `decisions`, `actions`, `risks` but never `title`. Combined with Bug 1, title-only meetings produced empty snippets.

**Fix in `search.go` (lines 233-244):**
- Added `title` as the first fallback before `segmentText`

**Before:**
```go
if r.Snippet == "" {
    if segmentText.String != "" {
        r.Snippet = segmentText.String
    } else if decisions.String != "" {
        r.Snippet = decisions.String
    } else if actions.String != "" {
        r.Snippet = actions.String
    } else if risks.String != "" {
        r.Snippet = risks.String
    }
}
```

**After:**
```go
if r.Snippet == "" {
    if title.String != "" {
        r.Snippet = title.String
    } else if segmentText.String != "" {
        r.Snippet = segmentText.String
    } else if decisions.String != "" {
        r.Snippet = decisions.String
    } else if actions.String != "" {
        r.Snippet = actions.String
    } else if risks.String != "" {
        r.Snippet = risks.String
    }
}
```

---

### Bug 3 & 4: BM25 parameters hardcoded and non-standard (HIGH/MEDIUM)

**Status:** FIXED

**Problem:** 
- Bug 3: BM25 constants in `ranker.go` were unused; values were hardcoded inline in SQL
- Bug 4: `k1=0.0` produces undefined binary ranking behavior

**Fix in `search.go` (line 201):**
- Used `BM25K1` and `BM25B` constants from `ranker.go`
- Changed from 9-parameter non-standard call to proper 2-parameter FTS5 bm25()

**Before:**
```go
bm25(meetings_fts, 1.0, 0.0, 1.0, 1.0, 1.0, 1.0, 1.0, 0.0, 0.0) as rank,
```

**After:**
```go
bm25(meetings_fts, `+fmt.Sprintf("%.1f, %.2f", BM25K1, BM25B)+`) as rank,
```

**Effect:** Now uses `k1=1.2, B=0.75` - standard BM25 parameters.

---

### Bug 5: FTS5 query validation incomplete (HIGH)

**Status:** FIXED

**Problem:** Validation only checked quotes and parentheses. Did not block:
- Boolean operators: `AND`, `OR`, `NOT`
- `NEAR` operator
- Column filters like `segment_text:foo`
- Wildcard `*` suffix/prefix

**Fix in `search.go` (lines 140-174):**
Enhanced `validateFTS5Query` to block:
- `AND`, `OR`, `NOT` operators (case-insensitive, word-boundary aware)
- `NEAR` operator
- Column filter patterns (`: ` character)
- Wildcard `*` character

---

### Bug 6: No limit on keyword extraction (MEDIUM)

**Status:** FIXED

**Problem:** `ExtractKeywords` had no limit. Large query text could extract hundreds of keywords, each compiled into a regex.

**Fix in `tokenizer.go` (lines 97-104):**
Added 50-keyword limit in `ExtractKeywords`:
```go
if len(keywords) >= 50 {
    break
}
```

---

### Bug 7: No ReDoS protection in regex compilation (MEDIUM)

**Status:** FIXED

**Problem:** `getHighlightRegex` compiled user keywords directly without validation. Malicious patterns like `a(a+)+b` could cause thread blocking.

**Fix in `tokenizer.go` (lines 12-35):**
- Added `isSafeRegexPattern()` function that:
  - Rejects patterns longer than 100 characters
  - Checks for dangerous patterns: `++`, `--`, `**`, `(+`, `)+`, `*(`, `)*`, `(?`, `)?`, `({`, `){`, `\{.*\{`, `\{.*\+`
- `getHighlightRegex` now calls `isSafeRegexPattern` and falls back to `regexp.QuoteMeta` for unsafe patterns

---

### Bug 9: TOCTOU race in getHighlightRegex (LOW)

**Status:** NO CHANGE NEEDED

**Problem:** Potential duplicate regex compilation under high concurrency.

**Analysis:** `sync.Map` Load/Store pattern is appropriate for this use case. The race is benign - worst case is duplicate compilation under extreme contention, which is acceptable. The alternative (sync.Mutex + initialization check) would add complexity without meaningful benefit.

---

### Bug 10: Title field never selected in search results (HIGH)

**Status:** FIXED

**Problem:** SELECT query never included `title` column, so `SearchResult.Title` was always empty.

**Fixes in `search.go`:**
1. Added `title` to FTS table schema (line 284)
2. Added `title` to SELECT query (line 193)
3. Added `title` to Scan target (line 216)
4. Assigned `r.Title = title.String` (line 223)

---

## Schema Change Note

The FTS5 table schema now includes `title` as a proper column:
```sql
CREATE VIRTUAL TABLE IF NOT EXISTS meetings_fts USING fts5(
    content,
    meeting_id,
    title,
    segment_text,
    speaker,
    decisions,
    actions,
    risks,
    segment_id,
    result_type,
    tokenize='unicode61'
)
```

**Migration:** Existing databases with the old schema will need to be rebuilt. The `IndexMeeting` function deletes all entries for a meeting before re-inserting, so re-indexing any meeting will create entries with the new schema.

---

## Summary

| Bug # | Severity | Issue | Status |
|-------|----------|-------|--------|
| 1 | CRITICAL | insertEntry empty columns for title entries | FIXED |
| 2 | CRITICAL | Snippet fallback skips title field | FIXED |
| 3 | HIGH | BM25 params hardcoded, ranker.go dead code | FIXED |
| 4 | MEDIUM | k1=0.0 produces non-standard ranking | FIXED |
| 5 | HIGH | FTS5 validation incomplete | FIXED |
| 6 | MEDIUM | No limit on keyword extraction | FIXED |
| 7 | MEDIUM | No ReDoS protection in regex | FIXED |
| 8 | LOW | Unused constants in ranker.go | N/A (constants now used) |
| 9 | LOW | TOCTOU race in regex caching | NO CHANGE (benign) |
| 10 | HIGH | Title field never selected | FIXED |

**Total: 9 bugs addressed, 1 no-change (benign race condition)**