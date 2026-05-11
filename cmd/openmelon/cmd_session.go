package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/eight-acres-lab/openmelon/internal/session"
)

func runSession(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: openmelon session <events> ...")
		os.Exit(2)
	}
	switch args[0] {
	case "events":
		return runSessionEvents(args[1:])
	default:
		return fmt.Errorf("unknown session subcommand: %q", args[0])
	}
}

func runSessionEvents(args []string) error {
	fs := flag.NewFlagSet("session events", flag.ContinueOnError)
	limit := fs.Int("n", 50, "Number of recent events")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: openmelon session events <session-id> [-n 50]")
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	events, err := session.LoadEvents(wd, fs.Arg(0), *limit)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		fmt.Println("No events recorded for this session.")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tTYPE\tSTEP\tTOOL\tSPACE\tSTATUS")
	for _, e := range events {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\n",
			e.At.Local().Format("01-02 15:04:05"),
			e.Type,
			e.Step,
			e.Tool,
			e.SpaceID,
			e.Status,
		)
	}
	return tw.Flush()
}
