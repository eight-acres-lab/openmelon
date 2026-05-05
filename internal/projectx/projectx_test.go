package projectx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateIDAcceptsValidSlugs(t *testing.T) {
	for _, id := range []string{"a1", "ai-talks", "fitness", "badminton-club", "x9"} {
		if err := ValidateID(id); err != nil {
			t.Errorf("ValidateID(%q) unexpected error: %v", id, err)
		}
	}
}

func TestValidateIDRejectsBadSlugs(t *testing.T) {
	for _, id := range []string{
		"", "a", "AI-talks", "9foo", "-bar", "bar-", "foo--bar", "with space", "with_underscore",
	} {
		if err := ValidateID(id); err == nil {
			t.Errorf("ValidateID(%q) expected error", id)
		}
	}
}

func TestInitCreatesProjectAndStateDirs(t *testing.T) {
	wd := t.TempDir()
	p, err := Init(wd, "ai-talks", "AI Talks")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.ID != "ai-talks" || p.Name != "AI Talks" {
		t.Errorf("project mismatch: %+v", p)
	}
	if p.CreatedAt.IsZero() {
		t.Error("created_at not set")
	}
	for _, sub := range []string{"characters", "references", "materials", "artifacts", "sessions"} {
		if _, err := os.Stat(filepath.Join(StateDir(wd), sub)); err != nil {
			t.Errorf("expected subdir %s, got: %v", sub, err)
		}
	}
}

func TestInitTwiceIsErrAlreadyInitialized(t *testing.T) {
	wd := t.TempDir()
	if _, err := Init(wd, "ai-talks", "AI Talks"); err != nil {
		t.Fatalf("Init #1: %v", err)
	}
	_, err := Init(wd, "ai-talks", "AI Talks")
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Errorf("expected ErrAlreadyInitialized, got %v", err)
	}
}

func TestLoadReturnsErrNotAProjectForBareDir(t *testing.T) {
	wd := t.TempDir()
	_, err := Load(wd)
	if !errors.Is(err, ErrNotAProject) {
		t.Errorf("expected ErrNotAProject, got %v", err)
	}
}

func TestSaveRoundtrip(t *testing.T) {
	wd := t.TempDir()
	in, err := Init(wd, "ai-talks", "AI Talks")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	in.Description = "Daily commentary on AI infra news."
	in.Persona = "Skeptical, terse, technical."
	in.Constraints = []string{"no clickbait", "no benchmarks without methodology"}
	in.Defaults.LLMProvider = "openrouter"
	in.Defaults.LLMModel = "x-ai/grok-4"
	in.Defaults.ImageProvider = "openrouter"
	in.Defaults.ImageModel = "google/gemini-2.5-flash-image"
	in.Defaults.Locale = "zh-CN"
	if err := Save(wd, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(wd)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Description != in.Description {
		t.Errorf("desc mismatch")
	}
	if out.Persona != in.Persona {
		t.Errorf("persona mismatch")
	}
	if len(out.Constraints) != 2 || out.Constraints[0] != "no clickbait" {
		t.Errorf("constraints mismatch: %v", out.Constraints)
	}
	if out.Defaults != in.Defaults {
		t.Errorf("defaults mismatch: %+v vs %+v", out.Defaults, in.Defaults)
	}
}

func TestDiscoverFindsProjectRootFromSubdir(t *testing.T) {
	wd := t.TempDir()
	if _, err := Init(wd, "ai-talks", "AI Talks"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	deep := filepath.Join(wd, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := Discover(deep)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	wantAbs, _ := filepath.Abs(wd)
	gotAbs, _ := filepath.Abs(got)
	if gotAbs != wantAbs {
		t.Errorf("Discover: got %q want %q", gotAbs, wantAbs)
	}
}

func TestDiscoverReturnsEmptyWhenNoProject(t *testing.T) {
	wd := t.TempDir()
	got, err := Discover(wd)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty workdir, got %q", got)
	}
}
