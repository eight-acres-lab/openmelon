package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/eight-acres-lab/openmelon/internal/continuity"
)

// runSpace dispatches `openmelon space <subcommand>`.
func runSpace(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: openmelon space <create|activate|list|show|context|search|decision|feedback|memory|promote|episode|asset|asset-weight|compact> ...")
		os.Exit(2)
	}
	switch args[0] {
	case "create":
		return runSpaceCreate(args[1:])
	case "activate":
		return runSpaceActivate(args[1:])
	case "list":
		return runSpaceList(args[1:])
	case "show":
		return runSpaceShow(args[1:])
	case "context":
		return runSpaceContext(args[1:])
	case "search":
		return runSpaceSearch(args[1:])
	case "decision":
		return runSpaceDecision(args[1:])
	case "feedback":
		return runSpaceFeedback(args[1:])
	case "memory":
		return runSpaceMemory(args[1:])
	case "promote":
		return runSpacePromote(args[1:])
	case "episode":
		return runSpaceEpisode(args[1:])
	case "asset":
		return runSpaceAsset(args[1:])
	case "asset-weight":
		return runSpaceAssetWeight(args[1:])
	case "compact":
		return runSpaceCompact(args[1:])
	default:
		return fmt.Errorf("unknown space subcommand: %q", args[0])
	}
}

func runSpaceCreate(args []string) error {
	fs := flag.NewFlagSet("space create", flag.ContinueOnError)
	name := fs.String("name", "", "Human-readable name (default: id)")
	platform := fs.String("platform", "", "Target platform, e.g. short-video")
	audience := fs.String("audience", "", "Target audience")
	description := fs.String("description", "", "One-line space description")
	assumptions := fs.String("assumptions", "", "Provisional assumptions markdown")
	var tags stringSlice
	fs.Var(&tags, "tag", "Add a tag (repeatable)")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: openmelon space create <id> [--name ...] [--description ...] [--tag t]...")
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	sp, err := continuity.CreateSpace(wd, continuity.CreateSpaceOptions{
		ID:          fs.Arg(0),
		Name:        *name,
		Platform:    *platform,
		Audience:    *audience,
		Description: *description,
		Tags:        tags,
		Assumptions: *assumptions,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Created space %s\n", sp.ID)
	fmt.Printf("  dir: %s\n", continuity.SpaceDir(wd, sp.ID))
	return nil
}

func runSpaceActivate(args []string) error {
	fs := flag.NewFlagSet("space activate", flag.ContinueOnError)
	reason := fs.String("reason", "", "Why this direction was confirmed")
	weight := fs.Float64("weight", 1.0, "Decision weight")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: openmelon space activate <space-id> <confirmed decision...>")
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	sp, d, err := continuity.ActivateSpace(wd, fs.Arg(0), continuity.Decision{
		Decision: strings.Join(fs.Args()[1:], " "),
		Reason:   *reason,
		Weight:   *weight,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Activated space %s with decision %s\n", sp.ID, d.ID)
	return nil
}

func runSpaceList(args []string) error {
	fs := flag.NewFlagSet("space list", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	spaces, err := continuity.ListSpaces(wd)
	if err != nil {
		return err
	}
	if len(spaces) == 0 {
		fmt.Println("No creative spaces in this project.")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tTAGS\tDESCRIPTION")
	for _, sp := range spaces {
		desc := sp.Description
		if len(desc) > 72 {
			desc = desc[:72] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", sp.ID, sp.Name, sp.Status, strings.Join(sp.Tags, ","), desc)
	}
	return tw.Flush()
}

func runSpaceShow(args []string) error {
	fs := flag.NewFlagSet("space show", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Print full context packet as JSON")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: openmelon space show <id> [--json]")
	}
	wd, proj, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	p, err := continuity.BuildContextPacket(wd, proj.ID, fs.Arg(0))
	if err != nil {
		return err
	}
	if *jsonOut {
		b, _ := json.MarshalIndent(p, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("ID:          %s\n", p.Space.ID)
	fmt.Printf("Name:        %s\n", p.Space.Name)
	if p.Space.Platform != "" {
		fmt.Printf("Platform:    %s\n", p.Space.Platform)
	}
	if p.Space.Audience != "" {
		fmt.Printf("Audience:    %s\n", p.Space.Audience)
	}
	if p.Space.Description != "" {
		fmt.Printf("Description: %s\n", p.Space.Description)
	}
	if len(p.Space.Tags) > 0 {
		fmt.Printf("Tags:        %s\n", strings.Join(p.Space.Tags, ", "))
	}
	if strings.TrimSpace(p.Assumptions) != "" {
		fmt.Println("\nAssumptions:")
		fmt.Print(p.Assumptions)
	}
	if strings.TrimSpace(p.Canon) != "" {
		fmt.Println("\nCanon:")
		fmt.Print(p.Canon)
	}
	fmt.Printf("\nRecent: %d decisions, %d feedback items, %d episodes, %d assets\n",
		len(p.RecentDecisions), len(p.RecentFeedback), len(p.RecentEpisodes), len(p.Assets))
	return nil
}

func runSpaceContext(args []string) error {
	fs := flag.NewFlagSet("space context", flag.ContinueOnError)
	query := fs.String("query", "", "Current creative intent for ranking")
	maxDecisions := fs.Int("max-decisions", 8, "Decision budget")
	maxFeedback := fs.Int("max-feedback", 8, "Feedback budget")
	maxEpisodes := fs.Int("max-episodes", 8, "Episode budget")
	maxAssets := fs.Int("max-assets", 20, "Asset budget")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: openmelon space context <id> [--query ...] [--max-assets n] [--max-decisions n]")
	}
	wd, proj, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	p, err := continuity.BuildSelectedContextPacket(wd, proj.ID, fs.Arg(0), continuity.SelectionOptions{
		Query:        *query,
		MaxDecisions: *maxDecisions,
		MaxFeedback:  *maxFeedback,
		MaxEpisodes:  *maxEpisodes,
		MaxAssets:    *maxAssets,
	})
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	fmt.Println(string(b))
	return nil
}

func runSpaceSearch(args []string) error {
	fs := flag.NewFlagSet("space search", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: openmelon space search <query>...")
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	hits, err := continuity.SearchSpaces(wd, strings.Join(fs.Args(), " "))
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		fmt.Println("No matches.")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SCORE\tID\tNAME\tDESCRIPTION")
	for _, h := range hits {
		desc := h.Space.Description
		if len(desc) > 72 {
			desc = desc[:72] + "..."
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", h.Score, h.Space.ID, h.Space.Name, desc)
	}
	return tw.Flush()
}

func runSpaceDecision(args []string) error {
	fs := flag.NewFlagSet("space decision", flag.ContinueOnError)
	scope := fs.String("scope", "space", "Decision scope")
	target := fs.String("target", "", "Decision target")
	reason := fs.String("reason", "", "Why this decision was made")
	weight := fs.Float64("weight", 1.0, "Decision weight")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: openmelon space decision <space-id> <decision text...>")
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	d, err := continuity.RecordDecision(wd, fs.Arg(0), continuity.Decision{
		Scope:    *scope,
		Target:   *target,
		Decision: strings.Join(fs.Args()[1:], " "),
		Reason:   *reason,
		Weight:   *weight,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Recorded decision %s\n", d.ID)
	return nil
}

func runSpaceFeedback(args []string) error {
	fs := flag.NewFlagSet("space feedback", flag.ContinueOnError)
	episode := fs.String("episode", "", "Related episode id")
	source := fs.String("source", "user", "Feedback source")
	evidence := fs.String("evidence", "", "Evidence")
	recommendation := fs.String("recommendation", "", "Recommended strategy change")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: openmelon space feedback <space-id> <signal> [--evidence ...] [--recommendation ...]")
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	f, err := continuity.RecordFeedback(wd, fs.Arg(0), continuity.Feedback{
		EpisodeID:      *episode,
		Source:         *source,
		Signal:         fs.Arg(1),
		Evidence:       *evidence,
		Recommendation: *recommendation,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Recorded feedback %s\n", f.ID)
	return nil
}

func runSpaceMemory(args []string) error {
	fs := flag.NewFlagSet("space memory", flag.ContinueOnError)
	id := fs.String("id", "", "Memory item id")
	kind := fs.String("kind", "observation", "Memory kind")
	scope := fs.String("scope", "", "Scope")
	target := fs.String("target", "", "Target")
	source := fs.String("source", "user", "Source")
	weight := fs.Float64("weight", 0.5, "Memory weight")
	status := fs.String("status", "provisional", "Status")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: openmelon space memory <space-id> <content...> [--id mem-x] [--kind observation]")
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	item, err := continuity.RecordMemoryItem(wd, fs.Arg(0), continuity.MemoryItem{
		ID:      *id,
		Kind:    *kind,
		Scope:   *scope,
		Target:  *target,
		Content: strings.Join(fs.Args()[1:], " "),
		Source:  *source,
		Weight:  *weight,
		Status:  *status,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Recorded memory item %s\n", item.ID)
	return nil
}

func runSpacePromote(args []string) error {
	fs := flag.NewFlagSet("space promote", flag.ContinueOnError)
	reason := fs.String("reason", "", "Why this memory is confirmed")
	target := fs.String("target", "", "Decision target")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 3 {
		return fmt.Errorf("usage: openmelon space promote <space-id> <memory-id> <decision...>")
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	d, err := continuity.PromoteMemoryItem(wd, fs.Arg(0), continuity.MemoryPromotion{
		ItemID:   fs.Arg(1),
		Decision: strings.Join(fs.Args()[2:], " "),
		Reason:   *reason,
		Target:   *target,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Promoted memory into decision %s\n", d.ID)
	return nil
}

func runSpaceEpisode(args []string) error {
	fs := flag.NewFlagSet("space episode", flag.ContinueOnError)
	id := fs.String("id", "", "Episode id (default slug from topic/title)")
	title := fs.String("title", "", "Episode title")
	status := fs.String("status", "draft", "Episode status")
	brief := fs.String("brief", "", "Episode brief markdown")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: openmelon space episode <space-id> <topic...> [--id ...] [--brief ...]")
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	ep, err := continuity.CreateEpisode(wd, fs.Arg(0), continuity.Episode{
		ID:     *id,
		Title:  *title,
		Topic:  strings.Join(fs.Args()[1:], " "),
		Status: *status,
		Brief:  *brief,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Created episode %s\n", ep.ID)
	return nil
}

func runSpaceAsset(args []string) error {
	fs := flag.NewFlagSet("space asset", flag.ContinueOnError)
	id := fs.String("id", "", "Asset id (default slug from description/kind)")
	kind := fs.String("kind", "", "Asset kind, e.g. background, character, typography, prompt")
	status := fs.String("status", "active", "Asset status")
	reuse := fs.String("reuse", "", "Reuse policy")
	weight := fs.Float64("weight", 1.0, "Asset weight")
	var files stringSlice
	var tags stringSlice
	fs.Var(&files, "file", "Related file path (repeatable)")
	fs.Var(&tags, "tag", "Tag (repeatable)")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: openmelon space asset <space-id> <description...> [--kind ...] [--file path]...")
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	a, err := continuity.RegisterAsset(wd, fs.Arg(0), continuity.Asset{
		ID:          *id,
		Kind:        *kind,
		Status:      *status,
		Description: strings.Join(fs.Args()[1:], " "),
		ReusePolicy: *reuse,
		Files:       files,
		Tags:        tags,
		Weight:      *weight,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Registered asset %s\n", a.ID)
	return nil
}

func runSpaceAssetWeight(args []string) error {
	fs := flag.NewFlagSet("space asset-weight", flag.ContinueOnError)
	status := fs.String("status", "", "Optional new status")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 3 {
		return fmt.Errorf("usage: openmelon space asset-weight <space-id> <asset-id> <weight> [--status archived]")
	}
	var weight float64
	if _, err := fmt.Sscanf(fs.Arg(2), "%f", &weight); err != nil {
		return fmt.Errorf("asset-weight: invalid weight %q", fs.Arg(2))
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	a, err := continuity.UpdateAssetWeight(wd, fs.Arg(0), fs.Arg(1), weight, *status)
	if err != nil {
		return err
	}
	fmt.Printf("Updated asset %s weight to %.2f", a.ID, a.Weight)
	if a.Status != "" {
		fmt.Printf(" (%s)", a.Status)
	}
	fmt.Println()
	return nil
}

func runSpaceCompact(args []string) error {
	fs := flag.NewFlagSet("space compact", flag.ContinueOnError)
	draft := fs.Bool("draft", false, "Print a compaction draft instead of recording it")
	summary := fs.String("summary", "", "Compaction summary to record")
	scope := fs.String("scope", "space", "Compaction scope")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: openmelon space compact <space-id> [--draft | --summary ...]")
	}
	wd, proj, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	if *draft || strings.TrimSpace(*summary) == "" {
		body, err := continuity.BuildCompactionDraft(wd, proj.ID, fs.Arg(0))
		if err != nil {
			return err
		}
		if *draft || strings.TrimSpace(*summary) == "" {
			fmt.Print(body)
			if *draft {
				return nil
			}
			return fmt.Errorf("space compact: pass --summary to record a compaction, or --draft to only print the draft")
		}
	}
	c, err := continuity.RecordSpaceCompaction(wd, fs.Arg(0), continuity.SpaceCompaction{
		Summary: *summary,
		Scope:   *scope,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Recorded compaction %s\n", c.ID)
	return nil
}
