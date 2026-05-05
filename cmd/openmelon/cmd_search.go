package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/eight-acres-lab/openmelon/internal/search"
)

// runSearch is `openmelon search <query>...`.
//
// All non-flag args are joined with spaces and parsed as a search query.
// See package search for the query language.
func runSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "Cap result count (default 50)")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: openmelon search <query>... — supports tag:foo, kind:character, -negative, \"quoted phrase\"")
	}
	q, err := search.Parse(strings.Join(fs.Args(), " "))
	if err != nil {
		return err
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	hits, err := search.Run(wd, q)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		fmt.Println("No matches.")
		return nil
	}
	if *limit > 0 && len(hits) > *limit {
		hits = hits[:*limit]
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SCORE\tKIND\tSLUG\tNAME\tDESCRIPTION")
	for _, h := range hits {
		desc := h.Item.Description
		if len(desc) > 72 {
			desc = desc[:72] + "…"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n",
			h.Score, h.Item.Kind, h.Item.Slug, h.Item.Name, desc)
	}
	return tw.Flush()
}
