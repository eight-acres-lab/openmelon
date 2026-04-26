// Copyright 2026 Point Eight AI Pte. Ltd.
// Licensed under the Apache License, Version 2.0

// Package main is the entry point for the Skill-Plus execution engine.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/pointeight/skillplus-engine/engine"
	"github.com/pointeight/skillplus-engine/registry"
	"github.com/pointeight/skillplus-engine/runner"
)

func main() {
	skillsDir := flag.String("skills", "../skillplus/skills", "directory containing local Skill-Plus skill directories")
	text := flag.String("text", "", "text content to process")
	lang := flag.String("lang", "", "optional BCP-47 language hint")
	timeout := flag.Duration("timeout", 15*time.Second, "per-skill timeout")
	flag.Parse()

	if *text == "" {
		fmt.Fprintln(os.Stderr, "-text is required")
		os.Exit(2)
	}

	reg, err := registry.LoadFromDir(*skillsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load skills: %v\n", err)
		os.Exit(1)
	}

	eng := engine.New(reg.Skills(), runner.NewLocal(*timeout))
	bface, err := eng.Process(context.Background(), &engine.PostInput{
		Text: *text,
		Lang: *lang,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "process content: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(bface); err != nil {
		fmt.Fprintf(os.Stderr, "encode b-face: %v\n", err)
		os.Exit(1)
	}
}
