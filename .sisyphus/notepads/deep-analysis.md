# SQLite FTS5 Search Implementation Bugs

## Bug 1: BM25 Sort Order is DESC (Wrong) — Rank Should be ASC
**File:** `internal/search/search.go:162`
**Severity:** HIGH

**Problem:** The FTS5 `bm25()` function returns LOWER scores for BETTER matches (more relevant documents get LOWER BM25 scores). However, the query orders by `rank DESC`:

```go
ORDER BY rank DESC
```

This is backwards. Higher BM25 = lower relevance, but results are sorted as if higher is better.

**Fix:**
```go
ORDER BY rank ASC
```

**Note:** The test at `search_test.go:157` compares `results[0].BM25Score > results[1].BM25Score` which is also wrong if expecting higher score = better. Test would pass incorrectly with descending order.

---

## Bug 2: Snippet Uses `content` Column But `content` is Built, Not Original
**File:** `internal/search/search.go:159`
**Severity:** HIGH

**Problem:** The FTS5 table schema has separate columns:
```go
content, meeting_id, segment_text, speaker, decisions, actions, risks, segment_id, result_type
```

The `snippet()` function call uses column 0 which is `content`:
```go
snippet(meetings_fts, 0, '**', '**', '...', 32)
```

But `content` is a **computed field** created by `buildContent()` that joins all text fields together with spaces:
```go
func buildContent(title, segmentText, speaker, decisions, actions, risks string) string {
    // joins all parts with " "
}
```

The original individual fields (`segment_text`, `decisions`, `actions`, `risks`) are NOT what was indexed — only the concatenated `content` was indexed. So snippets will show **mangled text** from the concatenation.

**Fix:** Either:
1. Index individual columns and use `snippet(meetings_fts, 2, ...)` for `segment_text`, etc.
2. Or use FTS5 `content=` option to reference an external content table for original values

---

## Bug 3: SQL Injection via Unescaped User Query
**File:** `internal/search/search.go:161`
**Severity:** CRITICAL

**Problem:** The FTS5 MATCH query passes user input directly:
```go
WHERE meetings_fts MATCH ?
```

FTS5 query syntax allows special operators (`,`, `*`, `"`, `()`, `AND`, `OR`, `NOT`, `*` for prefix matching). A malicious query like `"font" OR 1=1--` or `*` could break the query or expose data.

While `sql.DB.Query` uses prepared statements which handle value escaping, **FTS5 MATCH is NOT a parameterized value — it's part of the query syntax itself**. The `?` placeholder becomes the query string, and SQLite will parse it as FTS5 syntax, not SQL.

**Fix:** Escape FTS5 special characters in user input before passing to MATCH, or validate/sanitize the query string.

```go
// Sanitize FTS5 query
query = strings.ReplaceAll(query, "\"", "\"\"")
query = strings.ReplaceAll(query, "(", "")
query = strings.ReplaceAll(query, ")", "")
query = strings.ReplaceAll(query, "*", " ")
// Or use fts5 phrase escaping
```

---

## Bug 4: Redundant Index on FTS5 Table is Useless
**File:** `internal/search/search.go:257`
**Severity:** MEDIUM

**Problem:**
```go
_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_meeting_id ON meetings_fts(meeting_id)`)
```

FTS5 virtual tables manage their own internal structure. Creating a regular B-tree index on a column within an FTS5 table is **not valid** — FTS5 has its own indexing mechanism and does not use external indexes. This CREATE INDEX statement will either fail silently or be ignored by SQLite for FTS5 tables.

**Fix:** Remove this line. FTS5 automatically indexes by all columns. If fast lookup by `meeting_id` is needed, use FTS5's built-in `content=` option with a separate content table.

---

## Bug 5: No Visibility into FTS5 Tokenizer Configuration
**File:** `internal/search/search.go:249`
**Severity:** LOW

**Problem:** The tokenizer is set to `unicode61`:
```go
tokenize='unicode61'
```

But there's no configuration for:
- `remove_diacritics` (1 or 2)
- `token_chars` / `separator_chars` for special characters
- No custom prefix indexing (prefix indexes are NOT enabled by default in unicode61)

If searching for hyphenated words, special characters, or accented characters produces unexpected results, this could be the cause.

**Fix:** Add explicit configuration if non-default behavior is needed, e.g., to enable prefix matching:
```go
tokenize='unicode61 remove_diacritics 1'
```

---

## Bug 6: Race Condition — WAL PRAGMA Double-Set
**File:** `internal/search/search.go:227-236`
**Severity:** LOW

**Problem:** The connection string already includes `_pragma=journal_mode(WAL)`:
```go
db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
```

Then immediately after, the code executes:
```go
_, err = db.Exec(`PRAGMA journal_mode=WAL`)
```

This is redundant and could cause issues on some SQLite versions. The first PRAGMA in the connection string should be sufficient.

**Fix:** Remove the redundant `PRAGMA journal_mode=WAL` exec, or remove the `_pragma=journal_mode(WAL)` from the connection string.

---

## Bug 7: `HighlightMatches` in tokenizer.go Uses Different Matching Than FTS5
**File:** `internal/search/tokenizer.go:90-103`
**Severity:** MEDIUM

**Problem:** `HighlightMatches()` uses Go's `regexp` with `(?i)` (case-insensitive) and `regexp.QuoteMeta`:
```go
re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(keyword))
```

But FTS5 `unicode61` tokenizer normalizes differently (Unicode case folding, diacritics handling). The Go regex and SQLite FTS5 may produce **different tokenization**. A match found by FTS5 may not be highlighted by Go regex, or vice versa.

Additionally, the snippet from FTS5 (`snippet()`) uses FTS5's own highlighting which conflicts with `HighlightMatches()`.

**Fix:** If client-side highlighting is needed, either:
1. Use FTS5's `highlight()` function instead of Go regex
2. Ensure Go regex and FTS5 tokenizer produce identical results (unlikely to be fully achievable)

---

## Bug 8: Transaction Rollback After Commit
**File:** `internal/search/search.go:94-98`
**Severity:** MEDIUM

**Problem:** `defer tx.Rollback()` is called at line 49, then if commit succeeds we immediately call `tx.Commit()`. However, if `PRAGMA optimize` fails (line 98-100), the transaction has already committed. The `Rollback()` will be a no-op but could cause confusion.

```go
tx, err := s.db.Begin()
defer tx.Rollback()  // This will run on function exit AFTER commit
// ... insert operations ...
if err := tx.Commit(); err != nil {  // Commit succeeds
    return fmt.Errorf("commit transaction: %w", err)
}
// Later: PRAGMA optimize fails, but rollback already set to no-op
if _, err := s.db.Exec(`PRAGMA optimize`); err != nil {
    return fmt.Errorf("optimize index: %w", err)
}
```

**Fix:** Move `PRAGMA optimize` inside the transaction, or remove the defer and manage rollback explicitly around the commit.

---

## Summary Table

| Bug | Severity | File:Line | Issue |
|-----|----------|-----------|-------|
| 1 | HIGH | search.go:162 | BM25 ORDER BY DESC is wrong (should be ASC) |
| 2 | HIGH | search.go:159 | snippet() operates on computed `content`, not original fields |
| 3 | CRITICAL | search.go:161 | FTS5 MATCH vulnerable to query injection |
| 4 | MEDIUM | search.go:257 | Invalid index on FTS5 virtual table |
| 5 | LOW | search.go:249 | Unicode tokenizer config may cause tokenization mismatch |
| 6 | LOW | search.go:232-236 | Redundant WAL PRAGMA |
| 7 | MEDIUM | tokenizer.go:90 | Go regex vs FTS5 tokenizer mismatch for highlighting |
| 8 | MEDIUM | search.go:49,94 | Rollback deferred past commit point |
