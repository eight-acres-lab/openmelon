package userconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// withTmpHome points $OPENMELON_HOME at a fresh tmpdir for the test, so
// no test ever touches the real ~/.openmelon.
func withTmpHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OPENMELON_HOME", dir)
	return dir
}

func TestEnsureHomeCreatesDir(t *testing.T) {
	home := withTmpHome(t)
	got, err := EnsureHome()
	if err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	if got != home {
		t.Errorf("home mismatch: got %q want %q", got, home)
	}
	if _, err := os.Stat(filepath.Join(home, "cache")); err != nil {
		t.Errorf("cache dir not created: %v", err)
	}
}

func TestLoadConfigMissingReturnsEmpty(t *testing.T) {
	withTmpHome(t)
	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.CurrentProject != "" || c.Defaults.LLMProvider != "" {
		t.Errorf("expected empty Config, got %+v", c)
	}
}

func TestSaveAndLoadConfigRoundtrip(t *testing.T) {
	withTmpHome(t)
	in := &Config{
		CurrentProject: "ai-talks",
		Defaults: Defaults{
			LLMProvider:   "openrouter",
			LLMModel:      "x-ai/grok-4",
			ImageProvider: "openrouter",
			ImageModel:    "google/gemini-2.5-flash-image",
			Locale:        "zh-CN",
		},
	}
	if err := SaveConfig(in); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	out, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if out.CurrentProject != in.CurrentProject {
		t.Errorf("current_project: got %q want %q", out.CurrentProject, in.CurrentProject)
	}
	if out.Defaults != in.Defaults {
		t.Errorf("defaults mismatch: got %+v want %+v", out.Defaults, in.Defaults)
	}
}

func TestConfigTrustHandlesSymlinkedPaths(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	cfg := &Config{TrustedDirs: []string{link}}
	if !cfg.IsTrusted(filepath.Join(target, "child")) {
		t.Fatal("expected symlink-equivalent target child to be trusted")
	}
}

func TestRegisterAndLookup(t *testing.T) {
	withTmpHome(t)
	wd := t.TempDir()
	if err := Register("ai-talks", "AI Talks", wd); err != nil {
		t.Fatalf("Register: %v", err)
	}
	e, err := Lookup("ai-talks")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if e.Name != "AI Talks" {
		t.Errorf("name: got %q want %q", e.Name, "AI Talks")
	}
	abs, _ := filepath.Abs(wd)
	if e.Workdir != abs {
		t.Errorf("workdir: got %q want %q", e.Workdir, abs)
	}
	if e.CreatedAt.IsZero() {
		t.Error("created_at not set")
	}
}

func TestRegisterIsIdempotentAndUpdatesOnReregister(t *testing.T) {
	withTmpHome(t)
	wd1 := t.TempDir()
	wd2 := t.TempDir()
	if err := Register("ai-talks", "AI Talks", wd1); err != nil {
		t.Fatalf("Register #1: %v", err)
	}
	created, err := Lookup("ai-talks")
	if err != nil {
		t.Fatalf("Lookup #1: %v", err)
	}
	if err := Register("ai-talks", "AI Talks (renamed)", wd2); err != nil {
		t.Fatalf("Register #2: %v", err)
	}
	updated, err := Lookup("ai-talks")
	if err != nil {
		t.Fatalf("Lookup #2: %v", err)
	}
	if updated.Name != "AI Talks (renamed)" {
		t.Errorf("name not updated: %q", updated.Name)
	}
	abs2, _ := filepath.Abs(wd2)
	if updated.Workdir != abs2 {
		t.Errorf("workdir not updated: got %q want %q", updated.Workdir, abs2)
	}
	// The original CreatedAt should survive the re-register.
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("created_at changed across re-register: %v vs %v", updated.CreatedAt, created.CreatedAt)
	}

	projects, err := LoadProjects()
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	if len(projects.Entries) != 1 {
		t.Errorf("expected 1 entry after idempotent re-register, got %d", len(projects.Entries))
	}
}

func TestLookupMissingReturnsErrProjectNotFound(t *testing.T) {
	withTmpHome(t)
	_, err := Lookup("does-not-exist")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSetCurrentRequiresRegistration(t *testing.T) {
	withTmpHome(t)
	if err := SetCurrent("ghost"); err == nil {
		t.Fatal("expected error setting unregistered project as current")
	}
}

func TestSetCurrentPersistsToConfig(t *testing.T) {
	withTmpHome(t)
	wd := t.TempDir()
	if err := Register("ai-talks", "AI Talks", wd); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := SetCurrent("ai-talks"); err != nil {
		t.Fatalf("SetCurrent: %v", err)
	}
	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.CurrentProject != "ai-talks" {
		t.Errorf("current_project: got %q want ai-talks", c.CurrentProject)
	}
}

func TestProjectsPersistedSortedById(t *testing.T) {
	withTmpHome(t)
	for _, id := range []string{"zebra", "alpha", "bravo"} {
		if err := Register(id, id, t.TempDir()); err != nil {
			t.Fatalf("Register %s: %v", id, err)
		}
	}
	projects, err := LoadProjects()
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	want := []string{"alpha", "bravo", "zebra"}
	if len(projects.Entries) != len(want) {
		t.Fatalf("entries len %d, want %d", len(projects.Entries), len(want))
	}
	for i, id := range want {
		if projects.Entries[i].ID != id {
			t.Errorf("[%d] id: got %q want %q", i, projects.Entries[i].ID, id)
		}
	}
}
