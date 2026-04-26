package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromDirLoadsSkillManifests(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "zeta", `slug: zeta
version: 0.1.0
name: Zeta Skill
description: Last skill
runtime: python
entrypoint: main
`)
	writeSkill(t, root, "alpha", `slug: alpha
version: 1.2.3
name: Alpha Skill
description: First skill
runtime: go
entrypoint: main
`)

	reg, err := LoadFromDir(root)
	if err != nil {
		t.Fatalf("LoadFromDir returned error: %v", err)
	}

	skills := reg.Skills()
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}
	if skills[0].Manifest.Slug != "alpha" || skills[1].Manifest.Slug != "zeta" {
		t.Fatalf("expected skills sorted by slug, got %q then %q", skills[0].Manifest.Slug, skills[1].Manifest.Slug)
	}
	if skills[0].Dir != filepath.Join(root, "alpha") {
		t.Fatalf("unexpected skill dir: %s", skills[0].Dir)
	}
}

func TestLoadFromDirSkipsDirectoriesWithoutManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	reg, err := LoadFromDir(root)
	if err != nil {
		t.Fatalf("LoadFromDir returned error: %v", err)
	}
	if len(reg.Skills()) != 0 {
		t.Fatalf("expected no skills, got %d", len(reg.Skills()))
	}
}

func TestLoadFromDirRejectsMissingRequiredFields(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "broken", `slug: broken
version: 0.1.0
runtime: python
`)

	_, err := LoadFromDir(root)
	if err == nil {
		t.Fatal("expected error for missing required fields")
	}
}

func writeSkill(t *testing.T, root, slug, manifest string) {
	t.Helper()
	dir := filepath.Join(root, slug)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}
