package main

// cmd_resume.go — `openmelon resume [<id>]`. Without an id, lists the
// most recent sessions for the current project so the user can copy-
// paste an id back. With an id, loads that session's messages.jsonl
// into the new TUI as starting history (a fresh session dir is opened
// to record the continuation, with `resumed_from` set in its meta).

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/session"
)

func runResume(args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	wd, err := projectx.Discover(cwd)
	if err != nil {
		return err
	}
	if wd == "" {
		return fmt.Errorf("resume: not inside an openmelon project (cd into one or run `openmelon init`)")
	}

	if len(args) == 0 {
		// List + hint.
		summaries, err := session.Recent(wd, 10)
		if err != nil {
			return err
		}
		if len(summaries) == 0 {
			fmt.Println("No prior sessions in this project.")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tWHEN\tTURNS\tFIRST")
		for _, s := range summaries {
			when := s.StartedAt.Local().Format("01-02 15:04")
			first := strings.Join(strings.Fields(s.FirstUserMessage), " ")
			if len(first) > 60 {
				first = first[:60] + "…"
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", s.ID, when, s.TurnCount, first)
		}
		_ = tw.Flush()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Resume one with: openmelon resume <id>")
		return nil
	}

	// Verify the id exists before launching the TUI.
	id := args[0]
	if err := session.ValidateWorkspace(wd, id); err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	if _, err := session.LoadHistory(wd, id); err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	// Defer to runRepl with the resume id stashed in package-level
	// state. cmd_repl reads it on entry.
	resumeID = id
	return runRepl(nil)
}

// resumeID is set by runResume before runRepl is called. cmd_repl
// reads it on startup to load the prior history. Empty means "fresh
// session". Lives at package scope rather than threaded through every
// call site since `openmelon resume` is the only entry point that
// sets it.
var resumeID string

// formatRelativeTime is a small helper for the picker output (unused
// today, kept for the future bubbletea picker).
func formatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return "(unknown)"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
