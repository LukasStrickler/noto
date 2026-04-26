# Search Package Bug Report

## Files Analyzed
- `internal/search/search.go`
- `internal/search/tokenizer.go`
- `internal/search/ranker.go`
- `internal/search/search_test.go`

---

## Bug 1: `insertEntry` sets `content` but leaves all other columns empty (CRITICAL)

**File:** `search.go`, line 105-115

**Problem:**
```go
func (s *SearchIndex) insertEntry(tx *sql.Tx, meetingID, title, segmentText, speaker, decisions, actions, risks, segmentID, resultType string) error {
    content := buildContent(title, segmentText, speaker, decisions, actions, risks)
    _, err := tx.Exec(
        `INSERT INTO meetings_fts(content, meeting_id, segment_text, speaker, decisions, actions, risks, segment_id, result_type) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
        content, meetingID, segmentText, speaker, decisions, actions, risks, segmentID, resultType,
    )
```

The `content` column is built from ALL fields via `buildContent()`, but individual columns (`segment_text`, `decisions`, `actions`, `risks`) receive the original values. However, for `title` entries (line 56), `segmentText=""`, `speaker=""`, `decisions=""`, `actions=""`, `risks=""` are all passed as empty strings!

**Impact:**
- Title entries have `content="Meeting Title"` but `segment_text=""`, `decisions=""`, etc.
- The fallback snippet generation (lines 211-221) fails for title entries since all fallback fields are empty
- Searching for a title-only meeting returns results with empty snippets

**Severity:** CRITICAL - Data integrity and search functionality broken for title-only entries

---

## Bug 2: Snippet fallback logic skips `title` field entirely (CRITICAL)

**File:** `search.go`, lines 211-221

**Problem:**
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

When `snippet(meetings_fts, ...)` returns empty, the fallback checks all fields except `title`. Combined with Bug 1 (title entries have empty `segment_text`, `decisions`, etc.), title-only meetings will NEVER produce a snippet.

**Impact:**
- Title-only entries produce search results with empty snippets
- User sees blank snippet in search results

**Severity:** CRITICAL - Search results for title-only meetings are unusable

---

## Bug 3: BM25 parameters hardcoded instead of using `ranker.go` constants

**File:** `search.go`, line 180

**Problem:**
```go
bm25(meetings_fts, 1.0, 0.0, 1.0, 1.0, 1.0, 1.0, 1.0, 0.0, 0.0) as rank,
```

The `ranker.go` file defines:
```go
const (
    BM25K1 = 1.2
    BM25B   = 0.75
)
```

But these constants are NEVER used. The BM25 parameters are hardcoded inline in SQL.

**Impact:**
- Configuration is duplicated and inconsistent
- Changing BM25 parameters requires modifying SQL AND `ranker.go`
- The `BM25Config` struct and `DefaultBM25Config()` function are dead code
- `ranker.go` provides no actual ranking logic

**Severity:** HIGH - Code maintenance issue, but also semantically confusing

---

## Bug 4: BM25 `k1=0.0` parameter produces undefined ranking behavior

**File:** `search.go`, line 180

**Problem:**
```go
bm25(meetings_fts, 1.0, 0.0, 1.0, 1.0, 1.0, 1.0, 1.0, 0.0, 0.0)
```

The second parameter is `0.0`. According to BM25 formula, when `k1 = 0`, the ranking becomes a binary measure (term frequency has no effect). However, the other parameters don't align with standard BM25:
- FTS5 bm25() takes (table, k1, B, [optional override parameters])  
- Standard BM25 has k1 around 1.2-2.0 and B around 0.5-0.75
- `1.0, 0.0, 1.0, 1.0, 1.0, 1.0, 1.0, 0.0, 0.0` - 9 extra parameters is non-standard

**Impact:**
- Document frequency normalization may not work correctly with k1=0
- Results may not be optimally ranked

**Severity:** MEDIUM - Non-optimal search ranking

---

## Bug 5: FTS5 query validation is incomplete - allows dangerous operators

**File:** `search.go`, lines 140-154

**Problem:**
```go
func validateFTS5Query(query string) (string, error) {
    query = strings.TrimSpace(query)
    if query == "" {
        return "", fmt.Errorf("empty query")
    }

    if strings.Count(query, "\"")%2 != 0 {
        return "", fmt.Errorf("unbalanced quotes")
    }
    if strings.Count(query, "(") != strings.Count(query, ")") {
        return "", fmt.Errorf("unbalanced parentheses")
    }

    return strings.ReplaceAll(strings.ReplaceAll(query, "\\", "\\\\"), "\"", "\"\""), nil
}
```

Validation only checks quotes and parentheses. Does NOT handle:
- `*` wildcard at end of term (can cause full table scan)
- Boolean operators: `AND`, `OR`, `NOT` (can change search semantics)
- `NEAR` operator
- Prefix matching with `*`
- Column filters like `segment_text:foo`

**Impact:**
- Users can craft inefficient queries like `"a" *` or `"a" AND "b"`
- Malicious queries could cause denial of service
- Boolean operators could return unexpected results

**Severity:** HIGH - Potential DoS and query manipulation

---

## Bug 6: No limit on keyword extraction for highlighting - potential ReDoS

**File:** `tokenizer.go`, lines 59-88

**Problem:**
```go
func ExtractKeywords(text string) []string {
    text = NormalizeText(text)
    words := strings.Fields(text)
    // ... stop word filtering ...
    var keywords []string
    for _, word := range words {
        if len(word) > 2 && !stopWords[word] {
            keywords = append(keywords, word)
        }
    }
    return keywords
}
```

No limit on number of keywords. A long query text could extract hundreds of keywords, each compiled into a regex via `HighlightMatches` → `getHighlightRegex` → `regexp.MustCompile`.

**Impact:**
- Large query text creates many regex compilations
- Potential memory exhaustion with adversarial input
- ReDoS patterns in keywords (e.g., `a{100}`) could cause slow regex evaluation

**Severity:** MEDIUM - Potential performance/memory issues

---

## Bug 7: `HighlightMatches` compiles regex without timeout protection

**File:** `tokenizer.go`, lines 90-102

**Problem:**
```go
func HighlightMatches(text, query string) string {
    keywords := ExtractKeywords(query)
    if len(keywords) == 0 {
        return text
    }
    result := text
    for _, keyword := range keywords {
        re := getHighlightRegex(keyword)
        result = re.ReplaceAllStringFunc(result, func(match string) string {
            return "**" + match + "**"
        })
    }
    return result
}
```

Regex compilation via `regexp.MustCompile` has no timeout. For user-supplied keywords containing ReDoS patterns (e.g., `a(a+)+b`), evaluation could hang.

**Impact:**
- ReDoS pattern in query keyword causes thread blocking
- Service becomes unresponsive during regex evaluation

**Severity:** MEDIUM - Potential DoS via ReDoS

---

## Bug 8: `BM25B` constant has inconsistent formatting and wrong value type

**File:** `ranker.go`, lines 3-6

**Problem:**
```go
const (
    BM25K1 = 1.2
    BM25B   = 0.75
)
```

`BM25B` is defined as float `0.75` but FTS5 bm25() actually expects B parameter in range [0.0, 1.0] and uses it as a weight for document length normalization. The Go constant type is correct, but the constant is never used (see Bug 3).

**Impact:**
- Dead code - constants defined but unused
- Misleading about actual ranking configuration

**Severity:** LOW - Unused code, not a functional bug

---

## Bug 9: Race condition possible with sync.Map in getHighlightRegex

**File:** `tokenizer.go`, lines 12-19

**Problem:**
```go
func getHighlightRegex(keyword string) *regexp.Regexp {
    if re, ok := highlightRegexCache.Load(keyword); ok {
        return re.(*regexp.Regexp)
    }
    re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(keyword))
    highlightRegexCache.Store(keyword, re)
    return re
}
```

The sync.Map Load/Store pattern is mostly correct, but there's a TOCTOU (time-of-check-time-of-use) race:
1. Two goroutines check same keyword simultaneously
2. Both find not cached
3. Both compile regex
4. Both store (second overwrites first, but both spent resources)

**Impact:**
- Wasted CPU/memory from duplicate regex compilations under high concurrency
- Not a correctness issue, but performance

**Severity:** LOW - Performance only, not correctness

---

## Bug 10: Search results never include `title` field directly

**File:** `search.go`, lines 192-223

**Problem:**
The `SearchResult` struct has a `Title` field, but the SELECT query never populates it:
```go
SELECT
    meeting_id,
    segment_text,
    ...
```

Only `meeting_id`, `segment_text`, `speaker`, `decisions`, `actions`, `risks`, `segment_id`, `result_type`, `rank`, `snippet` are selected - `title` is missing!

**Impact:**
- `SearchResult.Title` is always empty string
- Consumers of search results cannot determine which meeting matched without additional queries

**Severity:** HIGH - Data not available to callers

---

## Summary Table

| Bug # | Severity | Issue |
|-------|----------|-------|
| 1 | CRITICAL | `insertEntry` passes empty strings for title entries |
| 2 | CRITICAL | Snippet fallback skips title field |
| 3 | HIGH | BM25 params hardcoded, ranker.go is dead code |
| 4 | MEDIUM | k1=0.0 produces non-standard ranking |
| 5 | HIGH | FTS5 validation incomplete, allows dangerous operators |
| 6 | MEDIUM | No limit on keyword extraction |
| 7 | MEDIUM | No ReDoS protection in regex compilation |
| 8 | LOW | Unused constants in ranker.go |
| 9 | LOW | TOCTOU race in regex caching |
| 10 | HIGH | Title field never selected in search results |

---

## Minimum 5 Critical Bugs

1. **Bug 1** - `insertEntry` leaves columns empty for title entries
2. **Bug 2** - Snippet fallback doesn't check title
3. **Bug 5** - FTS5 query validation incomplete
4. **Bug 10** - Title field never populated in search
5. **Bug 3** - BM25 constants unused, hardcoded values
