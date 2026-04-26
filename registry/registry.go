// Copyright 2026 Point Eight AI Pte. Ltd.
// Licensed under the Apache License, Version 2.0

package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Registry stores locally loaded Skill-Plus skills.
type Registry struct {
	skills []Skill
}

// LoadFromDir loads skills from immediate child directories under root.
func LoadFromDir(root string) (*Registry, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read skills root %q: %w", root, err)
	}

	var skills []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dir := filepath.Join(root, entry.Name())
		manifestPath := filepath.Join(dir, "skill.yaml")
		data, err := os.ReadFile(manifestPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read manifest %q: %w", manifestPath, err)
		}

		var manifest Manifest
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("parse manifest %q: %w", manifestPath, err)
		}
		if err := validateManifest(manifest, manifestPath); err != nil {
			return nil, err
		}
		if manifest.Entrypoint == "" {
			manifest.Entrypoint = "main"
		}
		if manifest.Output.Format == "" {
			manifest.Output.Format = "json"
		}
		if manifest.Output.MaxSizeBytes == 0 {
			manifest.Output.MaxSizeBytes = 8192
		}

		skills = append(skills, Skill{Dir: dir, Manifest: manifest})
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Manifest.Slug < skills[j].Manifest.Slug
	})

	return &Registry{skills: skills}, nil
}

// Skills returns a copy of the loaded skill list.
func (r *Registry) Skills() []Skill {
	if r == nil || len(r.skills) == 0 {
		return nil
	}
	out := make([]Skill, len(r.skills))
	copy(out, r.skills)
	return out
}

func validateManifest(manifest Manifest, path string) error {
	if manifest.Slug == "" {
		return fmt.Errorf("manifest %q missing slug", path)
	}
	if manifest.Version == "" {
		return fmt.Errorf("manifest %q missing version", path)
	}
	if manifest.Name == "" {
		return fmt.Errorf("manifest %q missing name", path)
	}
	if manifest.Description == "" {
		return fmt.Errorf("manifest %q missing description", path)
	}
	if manifest.Runtime == "" {
		return fmt.Errorf("manifest %q missing runtime", path)
	}
	return nil
}
