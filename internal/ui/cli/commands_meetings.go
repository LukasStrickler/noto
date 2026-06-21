package cli

import (
	"fmt"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// This file holds the read-only meeting-data verbs: list, search, show,
// transcript, summary, files, agent. Each follows the same shape —
// connect, call one notoapi.Client method, render JSON or human text.

func (a *app) runList(args []string) int {
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	res, err := client.ListMeetings(ctx, notoapi.ListMeetingsOpts{})
	if err != nil {
		return a.errExit(err)
	}
	return a.emitOrText(args, res, func() {
		if len(res.Meetings) == 0 {
			fmt.Fprintln(a.out, "No meetings yet. Try `noto tui` and press r to record.")
			return
		}
		fmt.Fprintf(a.out, "%-36s  %-30s  %s\n", "ID", "TITLE", "STATUS")
		for _, m := range res.Meetings {
			title := m.Title
			if len(title) > 30 {
				title = title[:27] + "…"
			}
			fmt.Fprintf(a.out, "%-36s  %-30s  %s\n", m.ID, title, m.Status)
		}
	})
}

func (a *app) runSearch(args []string) int {
	q := stripFlagsJoin(args) // a query is multi-word; don't drop everything after word 1
	if q == "" {
		fmt.Fprintln(a.errOut, "noto search: query required")
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	res, err := client.Search(ctx, notoapi.SearchOpts{Query: q})
	if err != nil {
		return a.errExit(err)
	}
	return a.emitOrText(args, res, func() {
		if len(res.Hits) == 0 {
			fmt.Fprintf(a.out, "no matches for %q\n", q)
			return
		}
		for _, h := range res.Hits {
			fmt.Fprintf(a.out, "%s  [%.0fs] %s — %s\n", h.MeetingID, h.Timestamp, h.Speaker, trim(h.Snippet, 80))
		}
	})
}

func (a *app) runShow(args []string) int {
	id := stripFlags(args)
	if id == "" {
		fmt.Fprintln(a.errOut, "noto show: meeting id required")
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	m, err := client.GetMeeting(ctx, id)
	if err != nil {
		return a.errExit(err)
	}
	return a.emitOrText(args, m, func() {
		fmt.Fprintf(a.out, "%s\n", m.Title)
		fmt.Fprintf(a.out, "  id            %s\n", m.ID)
		fmt.Fprintf(a.out, "  status        %s\n", m.Status)
		fmt.Fprintf(a.out, "  duration_sec  %d\n", m.DurationSeconds)
		fmt.Fprintf(a.out, "  decisions     %d\n", m.DecisionCount)
		fmt.Fprintf(a.out, "  action items  %d\n", m.ActionCount)
		fmt.Fprintf(a.out, "  risks         %d\n", m.RiskCount)
		if m.ShortSummary != "" {
			fmt.Fprintf(a.out, "\n%s\n", m.ShortSummary)
		}
	})
}

func (a *app) runTranscript(args []string) int {
	id := stripFlags(args)
	if id == "" {
		fmt.Fprintln(a.errOut, "noto transcript: meeting id required")
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	t, err := client.GetTranscript(ctx, id)
	if err != nil {
		return a.errExit(err)
	}
	return a.emitOrText(args, t, func() {
		for _, seg := range t.Segments {
			fmt.Fprintf(a.out, "[%6.1fs] %s [%s]: %s\n", seg.StartSec, seg.Speaker, seg.Role, seg.Text)
		}
	})
}

func (a *app) runSummary(args []string) int {
	id := stripFlags(args)
	if id == "" {
		fmt.Fprintln(a.errOut, "noto summary: meeting id required")
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	s, err := client.GetSummary(ctx, id)
	if err != nil {
		return a.errExit(err)
	}
	return a.emitOrText(args, s, func() {
		if s.Markdown != "" {
			fmt.Fprintln(a.out, s.Markdown)
			return
		}
		fmt.Fprintf(a.out, "%s\n", s.ShortSummary)
	})
}

func (a *app) runFiles(args []string) int {
	id := stripFlags(args)
	if id == "" {
		fmt.Fprintln(a.errOut, "noto files: meeting id required")
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	f, err := client.GetMeetingFiles(ctx, id)
	if err != nil {
		return a.errExit(err)
	}
	return a.emitJSON(f)
}

func (a *app) runAgent(args []string) int {
	id := stripFlags(args)
	if id == "" {
		fmt.Fprintln(a.errOut, "noto agent: meeting id required")
		return 64
	}
	ctx, cancel := defaultCtx()
	defer cancel()
	client, closer, code := a.connect(ctx)
	if code != 0 {
		return code
	}
	defer closer()
	h, err := client.GetAgentHandoff(ctx, id)
	if err != nil {
		return a.errExit(err)
	}
	return a.emitJSON(h)
}
