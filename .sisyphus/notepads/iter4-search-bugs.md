# iter4-search-bugs: Search Directory Bug Reports

## Bug Summary (5 bugs found)

---

### Bug 1: snippet() uses wrong column index (content vs segment_text)
**File:** internal/search/search.go:159
**Severity:** HIGH

**Description:**
The `snippet()` function uses column index 0 (`content`) instead of the specific text column being searched:

```go
snippet(meetings_fts, 0, '**', '**', '...', 32) as snippet
```

Column 0 is the `content` column which contains concatenated text from ALL fields via `buildContent()`. For transcript searches, users expect to see the actual segment text highlighted, not a mix of title+segment+speaker+decisions+actions+risks.

**Impact:** Snippets may show unrelated content. For example, searching for a unique title term could return a snippet showing decision/action text instead of the matching transcript segment.

---

### Bug 2: FTS5 query not validated before MATCH
**File:** internal/search/search.go:161
**Severity:** MEDIUM

**Description:**
User query is passed directly to FTS5 MATCH without any validation or escaping:

```go
WHERE meetings_fts MATCH ?
`, query)
```

FTS5 has its own query syntax with special characters. Unbalanced quotes, invalid boolean operators (e.g., `query AND AND`), backslashes, or other malformed FTS5 syntax will cause errors or unexpected behavior.

**Example that breaks:**
- `"pricing decision` (unbalanced quote) → FTS5 syntax error
- `AND AND` → FTS5 syntax error

**Impact:** Users searching with certain query patterns get errors instead of results.

---

### Bug 3: buildContent concatenates unrelated fields for snippet source
**File:** internal/search/search.go:117-138
**Severity:** MEDIUM

**Description:**
`buildContent()` concatenates ALL fields into one string:
```go
func buildContent(title, segmentText, speaker, decisions, actions, risks string) string {
    var parts []string
    if title != "" { parts = append(parts, title) }
    if segmentText != "" { parts = append(parts, segmentText) }
    if speaker != "" { parts = append(parts, speaker) }
    if decisions != "" { parts = append(parts, decisions) }
    if actions != "" { parts = append(parts, actions) }
    if risks != "" { parts = append(parts, risks) }
    return strings.Join(parts, " ")
}
```

The `content` column stores this combined text. When `snippet(meetings_fts, 0, ...)` extracts snippets from `content`, it can include text from unrelated fields.

**Impact:** For title-only entries (no transcript), the `content` contains only title. But for entries with multiple field values, snippet may include irrelevant context from other fields.

---

### Bug 4: BM25 computed over all columns including identifiers
**File:** internal/search/search.go:158
**Severity:** MEDIUM

**Description:**
```go
bm25(meetings_fts) as rank,
```

When `bm25()` is called without column specification, SQLite FTS5 computes BM25 over ALL columns. This includes `meeting_id` and `segment_id` which contain identifiers (not natural language text).

**Impact:** Ranking scores are influenced by identifier strings (e.g., "meeting-001", "seg_abc123") which shouldn't affect text relevance.

---

### Bug 5: Duplicate PRAGMA journal_mode WAL
**File:** internal/search/search.go:229, 234
**Severity:** LOW

**Description:**
WAL mode is set twice:
1. Line 229: In DSN string `path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"`
2. Line 234: Explicit `db.Exec(\`PRAGMA journal_mode=WAL\`)`

**Impact:** Minor redundancy. The second PRAGMA is redundant since WAL is already set via DSN.

---

## Analysis Notes

### What looks correct (from previous iterations):
- BM25 sort order: `ORDER BY rank ASC` is correct (FTS5 BM25: lower = better)
- regex cache: Uses sync.Map with regexp.QuoteMeta for safe pattern building
- PRAGMA optimize: Called after IndexMeeting for FTS5 maintenance
- SQL injection: Query uses parameterized `?` placeholder (safe from SQL injection)
- Tokenizer: Unicode61 is properly configured for FTS5
- Close(): Proper mutex handling and defer for cleanup

### Potential edge cases not bugs:
- Stop word filtering in ExtractKeywords (>2 char filter)
- Empty query check at line 141-143
- Segment timestamp not stored in FTS5 (not searchable, just in struct)
