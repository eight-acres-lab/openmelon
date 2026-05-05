package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/userconfig"
)

// runProject dispatches `openmelon project <list|use|show>`.
func runProject(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: openmelon project <list|use|show> ...")
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		return runProjectList(args[1:])
	case "use":
		return runProjectUse(args[1:])
	case "show":
		return runProjectShow(args[1:])
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
