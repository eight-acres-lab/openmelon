package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/userconfig"
)

// runInit is `openmelon init [id]`.
//
// Initializes the cwd as an openmelon project: creates .openmelon/, writes
// project.json, and registers the project in ~/.openmelon/projects.json.
//
// id defaults to the basename of the cwd, slugified. --name defaults to
// the same. --workdir overrides cwd (but keep id explicit when you do).
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	name := fs.String("name", "", "Human-readable project name (default: id)")
	description := fs.String("description", "", "One-line summary of what this project is")
	workdir := fs.String("workdir", "", "Project root (default: cwd)")
	setCurrent := fs.Bool("set-current", true, "Make this the current project after init")
	if err := parseInterspersed(fs, args); err != nil {
		return err
	}

	wd := *workdir
	if wd == "" {
		var err error
		wd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("init: cwd: %w", err)
		}
	}
	wd, err := filepath.Abs(wd)
	if err != nil {
		return err
	}

	id := ""
	if fs.NArg() > 0 {
		id = fs.Arg(0)
	}
	if id == "" {
		id = slugFromBase(filepath.Base(wd))
	}
	if *name == "" {
		*name = id
	}

	p, err := projectx.Init(wd, id, *name)
	initialized := true
	if err != nil {
		if errors.Is(err, projectx.ErrAlreadyInitialized) {
			initialized = false
			p, err = projectx.Load(wd)
			if err != nil {
				return err
			}
			id = p.ID
			if *name == "" || *name == fs.Arg(0) {
				*name = p.Name
			}
		} else {
			return err
		}
	}
	if *description != "" {
		p.Description = *description
		if err := projectx.Save(wd, p); err != nil {
			return err
		}
	}
	if err := userconfig.Register(p.ID, p.Name, wd); err != nil {
		return fmt.Errorf("init: register: %w", err)
	}
	if *setCurrent {
		if err := userconfig.SetCurrent(p.ID); err != nil {
			return fmt.Errorf("init: set current: %w", err)
		}
	}
	if initialized {
		fmt.Printf("Initialized project %q at %s\n", p.ID, wd)
	} else {
		fmt.Printf("Registered existing project %q at %s\n", p.ID, wd)
	}
	if *setCurrent {
		fmt.Println("Set as current project.")
	}
	return nil
}

// slugFromBase converts a directory basename into a kebab-case slug.
// Falls back to "project" if the result would be empty after cleanup.
func slugFromBase(base string) string {
	base = strings.ToLower(base)
	var b strings.Builder
	prevHy := false
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHy = false
		case r == ' ' || r == '_' || r == '-' || r == '.':
			if !prevHy && b.Len() > 0 {
				b.WriteByte('-')
				prevHy = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	// projectx.ValidateID requires a leading letter.
	if out == "" || (out[0] < 'a' || out[0] > 'z') {
		out = "project-" + out
		out = strings.TrimRight(out, "-")
	}
	if len(out) < 2 {
		out = "project"
	}
	return out
}
