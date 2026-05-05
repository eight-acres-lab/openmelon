// Package userconfig is openmelon's global per-user state.
//
// Layout under $OPENMELON_HOME (default ~/.openmelon):
//
//	config.json     defaults: API keys (or env-passthrough), default models, current project
//	projects.json   registry of known projects: id → workdir
//	cache/          downloaded artifacts (future)
//
// The config file is intentionally JSON, not YAML — the rest of the
// runtime is zero-dep stdlib and we keep it that way. Skillplus packages
// are still YAML; we read those by shelling to the `skillplus` CLI, not
// by parsing them in Go.
package userconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Config is the on-disk shape of ~/.openmelon/config.json.
//
// All fields are optional. Empty values mean "fall back to env / vendor
// default". The CLI never writes API keys here — keys live in the user's
// shell env. We persist only model + provider preferences.
type Config struct {
	// CurrentProject is the project ID used when no -C / cwd-discovered
	// project applies. Empty means "no global default".
	CurrentProject string `json:"current_project,omitempty"`

	// Defaults are the agent defaults applied when no per-project or
	// per-invocation override is given.
	Defaults Defaults `json:"defaults,omitempty"`
}

// Defaults are the agent defaults used in the absence of any override.
type Defaults struct {
	// LLMProvider is one of "auto", "anthropic", "openai", "openrouter".
	LLMProvider string `json:"llm_provider,omitempty"`
	// LLMModel is a vendor-specific model id, e.g. "x-ai/grok-4".
	LLMModel string `json:"llm_model,omitempty"`
	// ImageProvider is one of "openai", "openrouter".
	ImageProvider string `json:"image_provider,omitempty"`
	// ImageModel is a vendor-specific model id, e.g.
	// "google/gemini-2.5-flash-image".
	ImageModel string `json:"image_model,omitempty"`
	// VisionModel is the model id used by add_character / add_reference
	// to auto-write a description for an image. Empty → reuse LLMModel
	// if it's vision-capable, else require explicit.
	VisionModel string `json:"vision_model,omitempty"`
	// Locale is the default skill compile locale.
	Locale string `json:"locale,omitempty"`
}

// ProjectEntry is one row in projects.json — the global registry that
// maps a project ID (slug) to its workdir on disk.
type ProjectEntry struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Workdir    string    `json:"workdir"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

// Projects is the on-disk shape of ~/.openmelon/projects.json.
type Projects struct {
	Entries []ProjectEntry `json:"entries"`
}

// ErrNoCurrentProject is returned by ResolveCurrent when there is no
// CWD-discovered project AND no global default.
var ErrNoCurrentProject = errors.New("userconfig: no current project — run `openmelon init` in a project directory or `openmelon project use <id>`")

// ErrProjectNotFound is returned by Lookup when the given id is not
// registered in projects.json.
var ErrProjectNotFound = errors.New("userconfig: project not found")

// Home returns the resolved $OPENMELON_HOME path. Default ~/.openmelon.
func Home() (string, error) {
	if h := os.Getenv("OPENMELON_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("userconfig: resolve user home: %w", err)
	}
	return filepath.Join(home, ".openmelon"), nil
}

// EnsureHome creates $OPENMELON_HOME (and cache/) on first use.
func EnsureHome() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(home, "cache"), 0o755); err != nil {
		return "", fmt.Errorf("userconfig: create %s: %w", home, err)
	}
	return home, nil
}

// LoadConfig reads ~/.openmelon/config.json. Missing file → empty Config
// (not an error — first run is fine).
func LoadConfig() (*Config, error) {
	home, err := Home()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, "config.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("userconfig: read %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("userconfig: parse %s: %w", path, err)
	}
	return &c, nil
}

// SaveConfig writes ~/.openmelon/config.json (creating $OPENMELON_HOME
// if needed).
func SaveConfig(c *Config) error {
	home, err := EnsureHome()
	if err != nil {
		return err
	}
	path := filepath.Join(home, "config.json")
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("userconfig: marshal config: %w", err)
	}
	return atomicWrite(path, append(b, '\n'))
}

// LoadProjects reads ~/.openmelon/projects.json. Missing file → empty
// list (not an error).
func LoadProjects() (*Projects, error) {
	home, err := Home()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, "projects.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Projects{Entries: []ProjectEntry{}}, nil
		}
		return nil, fmt.Errorf("userconfig: read %s: %w", path, err)
	}
	var p Projects
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("userconfig: parse %s: %w", path, err)
	}
	if p.Entries == nil {
		p.Entries = []ProjectEntry{}
	}
	return &p, nil
}

// SaveProjects writes ~/.openmelon/projects.json with entries sorted by
// id for stable diffs.
func SaveProjects(p *Projects) error {
	home, err := EnsureHome()
	if err != nil {
		return err
	}
	if p.Entries == nil {
		p.Entries = []ProjectEntry{}
	}
	sort.Slice(p.Entries, func(i, j int) bool { return p.Entries[i].ID < p.Entries[j].ID })
	path := filepath.Join(home, "projects.json")
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("userconfig: marshal projects: %w", err)
	}
	return atomicWrite(path, append(b, '\n'))
}

// Register adds (or updates) a project entry. Idempotent: if id already
// exists, the workdir + name are overwritten and last_used_at is bumped.
func Register(id, name, workdir string) error {
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return fmt.Errorf("userconfig: resolve workdir: %w", err)
	}
	projects, err := LoadProjects()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	found := false
	for i := range projects.Entries {
		if projects.Entries[i].ID == id {
			projects.Entries[i].Name = name
			projects.Entries[i].Workdir = abs
			projects.Entries[i].LastUsedAt = now
			found = true
			break
		}
	}
	if !found {
		projects.Entries = append(projects.Entries, ProjectEntry{
			ID:         id,
			Name:       name,
			Workdir:    abs,
			CreatedAt:  now,
			LastUsedAt: now,
		})
	}
	return SaveProjects(projects)
}

// Lookup returns the registry entry for id, or ErrProjectNotFound.
func Lookup(id string) (*ProjectEntry, error) {
	projects, err := LoadProjects()
	if err != nil {
		return nil, err
	}
	for i := range projects.Entries {
		if projects.Entries[i].ID == id {
			e := projects.Entries[i]
			return &e, nil
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrProjectNotFound, id)
}

// SetCurrent updates config.current_project. Errors if the id is not
// registered.
func SetCurrent(id string) error {
	if _, err := Lookup(id); err != nil {
		return err
	}
	c, err := LoadConfig()
	if err != nil {
		return err
	}
	c.CurrentProject = id
	return SaveConfig(c)
}

// MarkUsed bumps last_used_at on the given id (best-effort; missing id
// is a no-op).
func MarkUsed(id string) error {
	projects, err := LoadProjects()
	if err != nil {
		return err
	}
	changed := false
	for i := range projects.Entries {
		if projects.Entries[i].ID == id {
			projects.Entries[i].LastUsedAt = time.Now().UTC()
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	return SaveProjects(projects)
}

// atomicWrite writes to path via a temp file + rename so partial writes
// can never corrupt config.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
