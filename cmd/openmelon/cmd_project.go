package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/eight-acres-lab/openmelon/internal/onboard"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/userconfig"
)

// runProject dispatches `openmelon project <subcommand>`.
func runProject(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: openmelon project <list|use|show|set-key|unset-key|keys> ...")
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		return runProjectList(args[1:])
	case "use":
		return runProjectUse(args[1:])
	case "show":
		return runProjectShow(args[1:])
	case "set-key":
		return runProjectSetKey(args[1:])
	case "unset-key":
		return runProjectUnsetKey(args[1:])
	case "keys":
		return runProjectKeys(args[1:])
	default:
		return fmt.Errorf("unknown project subcommand: %q", args[0])
	}
}

func runProjectList(args []string) error {
	fs := flag.NewFlagSet("project list", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}
	projects, err := userconfig.LoadProjects()
	if err != nil {
		return err
	}
	cfg, err := userconfig.LoadConfig()
	if err != nil {
		return err
	}
	if len(projects.Entries) == 0 {
		fmt.Println("No projects registered. Run `openmelon init` in a project dir.")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tWORKDIR\tCURRENT")
	for _, e := range projects.Entries {
		mark := ""
		if e.ID == cfg.CurrentProject {
			mark = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.ID, e.Name, e.Workdir, mark)
	}
	return tw.Flush()
}

func runProjectUse(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: openmelon project use <id>")
	}
	id := args[0]
	if err := userconfig.SetCurrent(id); err != nil {
		return err
	}
	if err := userconfig.MarkUsed(id); err != nil {
		return err
	}
	fmt.Printf("Current project: %s\n", id)
	return nil
}

func runProjectShow(args []string) error {
	wd, _, err := resolveProjectWorkdir(args)
	if err != nil {
		return err
	}
	p, err := projectx.Load(wd)
	if err != nil {
		return err
	}
	fmt.Printf("ID:           %s\n", p.ID)
	fmt.Printf("Name:         %s\n", p.Name)
	fmt.Printf("Workdir:      %s\n", wd)
	if p.Description != "" {
		fmt.Printf("Description:  %s\n", p.Description)
	}
	if p.Persona != "" {
		fmt.Printf("Persona:      %s\n", p.Persona)
	}
	if len(p.Constraints) > 0 {
		fmt.Println("Constraints:")
		for _, c := range p.Constraints {
			fmt.Printf("  - %s\n", c)
		}
	}
	if p.Defaults != (projectx.Defaults{}) {
		fmt.Println("Defaults:")
		if p.Defaults.LLMProvider != "" {
			fmt.Printf("  llm_provider:   %s\n", p.Defaults.LLMProvider)
		}
		if p.Defaults.LLMModel != "" {
			fmt.Printf("  llm_model:      %s\n", p.Defaults.LLMModel)
		}
		if p.Defaults.ImageProvider != "" {
			fmt.Printf("  image_provider: %s\n", p.Defaults.ImageProvider)
		}
		if p.Defaults.ImageModel != "" {
			fmt.Printf("  image_model:    %s\n", p.Defaults.ImageModel)
		}
		if p.Defaults.Locale != "" {
			fmt.Printf("  locale:         %s\n", p.Defaults.Locale)
		}
	}
	if p.Settings != (projectx.Settings{}) {
		fmt.Println("Settings:")
		if p.Settings.BashPermissionMode != "" {
			fmt.Printf("  bash_permission_mode: %s\n", p.Settings.EffectiveBashMode())
		}
		if p.Settings.ReasoningEffort != "" {
			fmt.Printf("  reasoning_effort:    %s\n", p.Settings.EffectiveReasoningEffort())
		}
	}
	printKeySources(wd)
	return nil
}

// printKeySources writes a "Credentials:" block showing which provider
// keys are configured for this project, where they came from (project /
// global / none), and a masked value.
func printKeySources(wd string) {
	providers := []string{"openrouter", "openai", "anthropic"}
	type row struct{ provider, source, value string }
	var rows []row
	for _, p := range providers {
		resolved := userconfig.ResolveProvider(wd, p)
		if resolved.APIKey == "" {
			continue
		}
		src := resolved.KeySource
		if src == "" {
			src = "unknown"
		}
		rows = append(rows, row{provider: p, source: src, value: maskKey(resolved.APIKey)})
	}
	if len(rows) == 0 {
		return
	}
	fmt.Println("Credentials:")
	for _, r := range rows {
		fmt.Printf("  %-11s %s  (%s)\n", r.provider+":", r.source, r.value)
	}
}

func maskKey(k string) string {
	if len(k) <= 8 {
		return "•••"
	}
	return k[:4] + "…" + k[len(k)-4:]
}

// runProjectSetKey is `openmelon project set-key [<provider>]`.
//
// Without a provider arg, opens the interactive wizard (provider picker
// + masked key input). With a provider arg, skips the picker.
func runProjectSetKey(args []string) error {
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	hint := ""
	if len(args) > 0 {
		hint = args[0]
	}
	_, ok, err := onboard.RunProjectKeyWizard(wd, hint)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "cancelled.")
	}
	return nil
}

// runProjectUnsetKey is `openmelon project unset-key <provider>`.
func runProjectUnsetKey(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: openmelon project unset-key <provider>")
	}
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	creds, err := userconfig.LoadProjectCredentials(wd)
	if err != nil {
		return err
	}
	if _, had := creds.APIKeys[args[0]]; !had {
		fmt.Printf("No project-scoped key set for %s (nothing to remove).\n", args[0])
		return nil
	}
	if err := userconfig.UnsetProjectAPIKey(wd, args[0]); err != nil {
		return err
	}
	fmt.Printf("Removed project key for %s.\n", args[0])
	return nil
}

// runProjectKeys is `openmelon project keys`. Lists what keys are
// configured for the current project (project + global, masked).
func runProjectKeys(_ []string) error {
	wd, _, err := resolveProjectWorkdir(nil)
	if err != nil {
		return err
	}
	providers := []string{"openrouter", "openai", "anthropic"}
	any := false
	for _, p := range providers {
		k, src := userconfig.ResolveAPIKey(wd, p)
		if src == userconfig.SourceNone {
			fmt.Printf("  %-11s (none)\n", p+":")
			continue
		}
		any = true
		fmt.Printf("  %-11s %s  (%s)\n", p+":", src, maskKey(k))
	}
	if !any {
		fmt.Fprintln(os.Stderr, "No API keys configured. Run `openmelon setup` (global) or `openmelon project set-key` (project-scoped).")
	}
	return nil
}

// resolveProjectWorkdir returns (workdir, project, error). Resolution
// order:
//
//  1. -C <dir> in args (consumed if present at the front)
//  2. projectx.Discover(cwd) — walks up looking for .openmelon/
//  3. userconfig.CurrentProject → workdir from registry
//
// Returns ErrNoCurrentProject if all three miss.
func resolveProjectWorkdir(args []string) (string, *projectx.Project, error) {
	// Flag stripping: support a leading -C <dir> on subcommands that
	// don't otherwise want to define their own -C.
	if len(args) >= 2 && args[0] == "-C" {
		wd, err := filepath.Abs(args[1])
		if err != nil {
			return "", nil, err
		}
		p, err := projectx.Load(wd)
		if err != nil {
			return "", nil, err
		}
		return wd, p, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	if wd, err := projectx.Discover(cwd); err == nil && wd != "" {
		p, err := projectx.Load(wd)
		if err != nil {
			return "", nil, err
		}
		return wd, p, nil
	}
	cfg, err := userconfig.LoadConfig()
	if err != nil {
		return "", nil, err
	}
	if cfg.CurrentProject == "" {
		return "", nil, userconfig.ErrNoCurrentProject
	}
	entry, err := userconfig.Lookup(cfg.CurrentProject)
	if err != nil {
		return "", nil, err
	}
	p, err := projectx.Load(entry.Workdir)
	if err != nil {
		return "", nil, err
	}
	return entry.Workdir, p, nil
}
