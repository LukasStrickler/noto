# noto-meetings skill

Query, browse, export, and cite meeting data from a noto backend.

## Connection

```bash
# Check what you're connected to
noto ping

# Remote backend (set these before using any commands below)
export NOTO_API_URL=http://your-server:8731
export NOTO_API_TOKEN=<token>

# Local (no env vars needed — noto auto-starts an in-process server)
noto ping
```

---

## 1. List recent meetings

```bash
# Human-readable
noto list

# All meetings as JSON (one per line after jq)
noto list --json | jq '.meetings[] | {id, title, created_at, status, decision_count, action_count}'

# Just the last 10
noto list --json | jq '[.meetings[:10][] | {id, title, created_at, status, short_summary}]'

# Only summarized meetings
noto list --json | jq '[.meetings[] | select(.status == "summarized")]'
```

Using the agent endpoint (includes short summaries, paginated with cursor):

```bash
noto list --json   # standard

# Agent endpoint: returns short_summary inline, cursor for pagination
curl -s "${NOTO_API_URL:-http://localhost}/v1/agent/meetings?limit=20" \
  -H "Authorization: Bearer $NOTO_API_TOKEN"
```

### Paginate

The agent list returns `next_cursor`. Pass it as `?before=<cursor>` to get the next page:

```bash
CURSOR=$(curl -s "${NOTO_API_URL}/v1/agent/meetings?limit=20" \
  -H "Authorization: Bearer $NOTO_API_TOKEN" | jq -r '.next_cursor')

curl -s "${NOTO_API_URL}/v1/agent/meetings?limit=20&before=${CURSOR}" \
  -H "Authorization: Bearer $NOTO_API_TOKEN"
```

---

## 2. Get one meeting (full context)

```bash
# Metadata + counts
noto show <meeting_id> --json

# Full context — metadata + cited summary + full transcript (one call)
curl -s "${NOTO_API_URL}/v1/agent/meetings/<meeting_id>" \
  -H "Authorization: Bearer $NOTO_API_TOKEN" | jq .

# Structured summary (decisions / actions / risks / questions with segment IDs)
noto summary --json <meeting_id>

# Full diarized transcript (speaker names resolved)
noto transcript --json <meeting_id>

# File paths on the server
noto files <meeting_id>
```

---

## 3. Search across all meetings

```bash
noto search --json "pricing decision"
noto search --json "action item deadline"
```

Each hit returns: `meeting_id`, `meeting_title`, `speaker`, `timestamp`, `segment_id`, `snippet`.

---

## 4. Export to JSON files

Export a single meeting (all artifacts):

```bash
ID=<meeting_id>
DIR="./exports/$ID"
mkdir -p "$DIR"

noto show       --json "$ID" > "$DIR/meeting.json"
noto summary    --json "$ID" > "$DIR/summary.json"
noto transcript --json "$ID" > "$DIR/transcript.json"

# Agent-optimised combined context (single file, everything in one)
curl -s "${NOTO_API_URL}/v1/agent/meetings/$ID" \
  -H "Authorization: Bearer $NOTO_API_TOKEN" \
  > "$DIR/context.json"

echo "Exported $ID to $DIR/"
```

Export all summarized meetings:

```bash
mkdir -p ./exports

noto list --json | jq -r '.meetings[] | select(.status == "summarized") | .id' | while read ID; do
  DIR="./exports/$ID"
  mkdir -p "$DIR"
  noto show       --json "$ID" > "$DIR/meeting.json"
  noto summary    --json "$ID" > "$DIR/summary.json"
  noto transcript --json "$ID" > "$DIR/transcript.json"
  echo "exported $ID"
done
```

Export the full meeting list as a single JSON catalogue:

```bash
noto list --json | jq '.meetings' > ./exports/catalogue.json
```

Export search results for a topic:

```bash
noto search --json "your query" > ./exports/search-results.json
```

---

## 5. Step-by-step agent workflow

Use this sequence to answer questions about past meetings:

```bash
# 1. Get an overview — what meetings exist?
noto list --json | jq '[.meetings[] | {id, title, created_at, status, decision_count}]'

# 2. Search for the specific topic
noto search --json "the topic" | jq '.hits[] | {meeting_title, speaker, timestamp, snippet, segment_id}'

# 3. Fetch the full context for the relevant meeting
curl -s "${NOTO_API_URL}/v1/agent/meetings/<id>" \
  -H "Authorization: Bearer $NOTO_API_TOKEN" | jq '{
    title: .title,
    date: .created_at,
    summary: .summary.short_summary,
    decisions: [.summary.decisions[].text],
    action_items: [.summary.action_items[].text]
  }'

# 4. Read specific transcript segments by ID
noto transcript --json <id> | jq '.segments[] | select(.id == "seg_XXXXXX")'
```

---

## 6. Import audio for processing

```bash
# Import and wait for the full pipeline (transcribe + summarize + index)
noto import-audio ./recording.m4a --title "Meeting title" --wait

# Import without waiting (get job ID, poll manually)
noto import-audio ./recording.m4a --title "Meeting title" --json
noto jobs --json | jq '[.[] | select(.status == "running")]'
```

---

## Citation format

Every claim about meeting content must include:

```
<meeting_title>, <speaker_display_name>, <timestamp_sec>s, <segment_id>

Example: Product sync, Maya, 842s, seg_000210
```

Use `display_name` from the transcript's `speakers` list — not raw provider labels like `speaker_0`.

---

## Rules

- Search first for broad questions; use summary for orientation
- Verify specific claims against the transcript + segment ID before citing
- Do not invent decisions, owners, dates, or action items
- Do not treat summaries as ground truth when transcripts exist
- Do not upload transcript content externally without user permission
