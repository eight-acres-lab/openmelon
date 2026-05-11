package userconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
)

func TestResolveProviderPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OPENMELON_HOME", home)
	t.Setenv("OPENAI_API_KEY", "env-key")
	t.Setenv("OPENAI_BASE_URL", "https://env.example.com")

	if err := SaveConfig(&Config{
		Providers: map[string]ProviderConfig{
			"openai": {APIKey: "global-key", BaseURL: "https://global.example.com"},
		},
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	wd := t.TempDir()
	p, err := projectx.Init(wd, "demo", "Demo")
	if err != nil {
		t.Fatalf("project init: %v", err)
	}
	p.Providers = map[string]projectx.ProviderConfig{
		"openai": {APIKey: "project-key", BaseURL: "https://project.example.com"},
	}
	if err := projectx.Save(wd, p); err != nil {
		t.Fatalf("project save: %v", err)
	}

	got := ResolveProvider(wd, "openai")
	if got.APIKey != "project-key" || got.BaseURL != "https://project.example.com" {
		t.Fatalf("project config should win: %+v", got)
	}

	p.Providers = nil
	if err := projectx.Save(wd, p); err != nil {
		t.Fatalf("project save: %v", err)
	}
	got = ResolveProvider(wd, "openai")
	if got.APIKey != "global-key" || got.BaseURL != "https://global.example.com" {
		t.Fatalf("global config should win after project removed: %+v", got)
	}

	if err := os.Remove(filepath.Join(home, "config.json")); err != nil {
		t.Fatal(err)
	}
	got = ResolveProvider(wd, "openai")
	if got.APIKey != "env-key" || got.BaseURL != "https://env.example.com" {
		t.Fatalf("env should win after config removed: %+v", got)
	}
}
