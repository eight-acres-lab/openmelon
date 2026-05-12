// Package projectx is openmelon's per-project state — the on-disk
// equivalent of a Claude Code project.
//
// A project's OpenMelon state lives under <workdir>/.openmelon/. User-facing
// deliverables live in visible project directories such as <workdir>/outputs/.
// The global registry in userconfig.Projects only holds (id → workdir)
// pointers.
//
// Layout:
//
//	<workdir>/.openmelon/
//	  project.json        the file this package owns
//	  characters/         per-character subdirs (registry package)
//	  references/         per-reference subdirs (registry package)
//	  materials/          opaque material pool (registry package)
//	  sessions/           multi-turn session state
//	  index.jsonl         flat search index (search package)
//	  history.jsonl       full conversation log
//
//	<workdir>/outputs/
//	  sessions/<id>/      generated drafts from one run
//	  artifacts/<slug>/   promoted deliverables
//
// projectx itself owns project.json + the "is this a project root"
// detection. Nothing else.
//
// "projectx" instead of "project" because internal/project already
// exists for the legacy 0.1 workflow loader. The two will eventually
// converge (likely by deleting internal/project once nobody runs the
// declarative workflow path) but for now they coexist.
package projectx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DirName is the per-project state directory. Always ".openmelon".
const DirName = ".openmelon"

// DefaultOutputDirName is the default visible project directory for user-facing
// generated files. Hidden .openmelon paths are reserved for runtime state.
const DefaultOutputDirName = "outputs"

// FileName is the per-project config file. Always "project.json".
const FileName = "project.json"

// Project is the on-disk shape of <workdir>/.openmelon/project.json.
type Project struct {
	// ID is the project slug. Must be kebab-case (lowercase letters,
	// digits, hyphens). Stable for the life of the project — used as
	// the registry key.
	ID string `json:"id"`

	// Name is the human-readable label. Free text.
	Name string `json:"name"`

	// Description is one to a few sentences explaining what this project
	// is about — "this is my AI commentary content account, posts on X
	// 3x/week, target is software engineers". Sent to the LLM as
	// context on every run.
	Description string `json:"description,omitempty"`

	// Persona is the creator's voice / tone guidance. Sent to the LLM.
	Persona string `json:"persona,omitempty"`

	// Constraints is a flat list of "rules of the house" — things the
	// agent must not do for this project. e.g. "no clickbait headlines",
	// "no medical advice".
	Constraints []string `json:"constraints,omitempty"`

	// Defaults override userconfig.Defaults for this project only.
	// Empty strings fall back to user defaults.
	Defaults Defaults `json:"defaults,omitempty"`

	// Providers holds project-scoped provider connection settings.
	// Values here override ~/.openmelon/config.json providers and
	// credentials.json/env fallbacks.
	Providers map[string]ProviderConfig `json:"providers,omitempty"`

	// Settings collects per-project agent behavior toggles. Edited via
	// the /settings TUI panel; surfaced in `openmelon project show`.
	Settings Settings `json:"settings,omitempty"`

	// CreatedAt is set by Init at first write and never changed.
	CreatedAt time.Time `json:"created_at"`
}

// BashPermissionMode controls how the bash tool decides whether to
// run a given command.
type BashPermissionMode string

const (
	// BashModeStrict (default): every bash command needs explicit
	// user approval. The judge LLM only flags BLOCK to refuse
	// destructive commands; everything else asks the user.
	BashModeStrict BashPermissionMode = "strict"

	// BashModeAuto: judge LLM classifies into AUTO / ASK / BLOCK.
	// Read-only inspection commands run without asking; ambiguous
	// commands prompt; dangerous ones are refused.
	BashModeAuto BashPermissionMode = "auto"

	// BashModeTrusted: every bash runs without asking. Equivalent to
	// Claude Code's --dangerously-skip-permissions; only enable when
	// you trust the model's judgment for the project at hand
	// (e.g. throwaway scratchpad, no secrets).
	BashModeTrusted BashPermissionMode = "trusted"
)

// Settings collects per-project agent behavior toggles.
//
// Empty values mean "use the global default in userconfig.Config" or
// the hard-coded fall-back if no global is set.
type Settings struct {
	// BashPermissionMode selects the approval gate for the bash tool.
	// Empty defaults to BashModeStrict.
	BashPermissionMode BashPermissionMode `json:"bash_permission_mode,omitempty"`

	// ReasoningEffort is the model thinking-depth sent with each agent
	// request when the provider supports it. Empty defaults to "xhigh"
	// for GPT-5-family OpenAI/OpenRouter models and provider default
	// elsewhere.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// EffectiveBashMode returns the mode the runtime should use, applying
// the strict default when no value is configured.
func (s Settings) EffectiveBashMode() BashPermissionMode {
	switch s.BashPermissionMode {
	case BashModeAuto, BashModeTrusted:
		return s.BashPermissionMode
	}
	return BashModeStrict
}

// EffectiveReasoningEffort returns the configured model thinking depth.
// Empty means callers should pick a model-aware default.
func (s Settings) EffectiveReasoningEffort() string {
	switch strings.ToLower(strings.TrimSpace(s.ReasoningEffort)) {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(s.ReasoningEffort))
	default:
		return ""
	}
}

// Defaults are per-project overrides for the model + locale knobs.
// Mirror of userconfig.Defaults so projects can pin their own.
type Defaults struct {
	LLMProvider   string `json:"llm_provider,omitempty"`
	LLMModel      string `json:"llm_model,omitempty"`
	ImageProvider string `json:"image_provider,omitempty"`
	ImageModel    string `json:"image_model,omitempty"`
	VisionModel   string `json:"vision_model,omitempty"`
	Locale        string `json:"locale,omitempty"`
}

// ProviderConfig mirrors userconfig.ProviderConfig without importing
// userconfig (which would create an import cycle).
type ProviderConfig struct {
	APIKey  string `json:"api_key,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
}

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ErrNotAProject is returned by Load when the given workdir has no
// .openmelon/project.json file.
var ErrNotAProject = errors.New("projectx: not an openmelon project (no .openmelon/project.json)")

// ErrAlreadyInitialized is returned by Init when project.json already
// exists. Callers can decide whether to overwrite via Save.
var ErrAlreadyInitialized = errors.New("projectx: project already initialized")

// ConfigPath returns <workdir>/.openmelon/project.json.
func ConfigPath(workdir string) string {
	return filepath.Join(workdir, DirName, FileName)
}

// StateDir returns <workdir>/.openmelon.
func StateDir(workdir string) string {
	return filepath.Join(workdir, DirName)
}

// OutputDir returns the default visible directory for user-facing outputs.
func OutputDir(workdir string) string {
	return filepath.Join(workdir, DefaultOutputDirName)
}

// SessionOutputDir returns the visible output directory for one generation
// session. The session transcript remains under .openmelon/sessions.
func SessionOutputDir(workdir, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "session"
	}
	return filepath.Join(OutputDir(workdir), "sessions", sessionID)
}

// ArtifactOutputDir returns the default visible directory for a promoted
// artifact bucket.
func ArtifactOutputDir(workdir, slug, timestamp string) string {
	return filepath.Join(OutputDir(workdir), "artifacts", slug, timestamp)
}

// ResolveOutputDir resolves a user/model-selected output directory inside the
// project. Empty requested uses fallback, then <workdir>/outputs. Paths under
// .openmelon are rejected because that directory is reserved for internal
// state.
func ResolveOutputDir(workdir, requested, fallback string) (string, error) {
	absWorkdir, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}
	requested = strings.TrimSpace(requested)
	out := fallback
	if requested != "" {
		if filepath.IsAbs(requested) {
			out = requested
		} else {
			out = filepath.Join(absWorkdir, requested)
		}
	}
	if strings.TrimSpace(out) == "" {
		out = OutputDir(absWorkdir)
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		return "", err
	}
	if !pathInside(absWorkdir, absOut) {
		return "", fmt.Errorf("projectx: output dir %q escapes project workdir", requested)
	}
	if pathInside(StateDir(absWorkdir), absOut) {
		return "", fmt.Errorf("projectx: output dir %q is inside .openmelon; choose a visible project directory", requested)
	}
	return absOut, nil
}

// Init creates a new project at workdir. Errors if one is already
// there — use Save to overwrite an existing project.json.
//
// id and name are required; description/persona/constraints/defaults
// can be edited later via Save.
func Init(workdir, id, name string) (*Project, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("projectx: name is required")
	}
	if _, err := os.Stat(ConfigPath(workdir)); err == nil {
		return nil, ErrAlreadyInitialized
	}
	for _, sub := range []string{"characters", "references", "materials", "sessions", "spaces"} {
		if err := os.MkdirAll(filepath.Join(StateDir(workdir), sub), 0o755); err != nil {
			return nil, fmt.Errorf("projectx: mkdir %s: %w", sub, err)
		}
	}
	if err := os.MkdirAll(OutputDir(workdir), 0o755); err != nil {
		return nil, fmt.Errorf("projectx: mkdir outputs: %w", err)
	}
	p := &Project{
		ID:        id,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}
	if err := Save(workdir, p); err != nil {
		return nil, err
	}
	if err := EnsureGitignore(workdir); err != nil {
		return nil, fmt.Errorf("projectx: write .gitignore: %w", err)
	}
	return p, nil
}

// gitignoreContent is the body written to <workdir>/.openmelon/.gitignore.
//
// Listed entries are scoped to the .openmelon dir (the file lives
// inside it), so "credentials.json" matches .openmelon/credentials.json
// and "sessions/" matches .openmelon/sessions/. Both contain user-
// sensitive material (API keys, conversation transcripts, generated
// drafts possibly drawn from personal photos).
//
// Things deliberately NOT excluded:
//   - characters/ + references/  user-curated content; usually wants to commit
//   - spaces/                    creative continuity state; usually wants to commit
//   - materials/                 ambiguous; left to the user
//   - project.json               always commit
const gitignoreContent = `# openmelon — auto-generated. Edit if you want different defaults.
# These paths are relative to this .openmelon/ directory.

# API keys (never commit).
credentials.json

# Per-run conversation transcripts + generated images.
sessions/

# Legacy hidden outputs. New user-facing outputs go under ../outputs/.
artifacts/
`

// EnsureGitignore writes <workdir>/.openmelon/.gitignore if it doesn't
// already exist. Idempotent: existing files are left alone (so users
// can edit them).
func EnsureGitignore(workdir string) error {
	dir := StateDir(workdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(path); err == nil {
		return nil // already present, don't clobber
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(gitignoreContent), 0o644)
}

// Load reads the project.json under workdir. Returns ErrNotAProject if
// the file does not exist.
func Load(workdir string) (*Project, error) {
	path := ConfigPath(workdir)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotAProject, workdir)
		}
		return nil, fmt.Errorf("projectx: read %s: %w", path, err)
	}
	var p Project
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("projectx: parse %s: %w", path, err)
	}
	if err := ValidateID(p.ID); err != nil {
		return nil, fmt.Errorf("projectx: %s: %w", path, err)
	}
	return &p, nil
}

// Save writes project.json. Always overwrites.
func Save(workdir string, p *Project) error {
	if err := ValidateID(p.ID); err != nil {
		return err
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("projectx: name is required")
	}
	if err := os.MkdirAll(StateDir(workdir), 0o755); err != nil {
		return fmt.Errorf("projectx: mkdir state: %w", err)
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("projectx: marshal: %w", err)
	}
	path := ConfigPath(workdir)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// Discover walks up from start looking for a .openmelon/project.json.
// Returns the project's workdir (the directory that contains
// .openmelon/), or "" if none is found before the filesystem root.
func Discover(start string) (string, error) {
	cur, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(cur, DirName, FileName)); err == nil {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", nil
		}
		cur = parent
	}
}

func pathInside(base, candidate string) bool {
	rel, err := filepath.Rel(base, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// ValidateID returns nil if id is a valid project slug.
//
// Rules: starts with a lowercase letter, only lowercase letters /
// digits / hyphens, no leading/trailing hyphens, length 2..64. Same
// shape as skillplus tag rules so people don't have to learn two.
func ValidateID(id string) error {
	if len(id) < 2 || len(id) > 64 {
		return fmt.Errorf("projectx: id %q must be 2..64 chars", id)
	}
	if !slugRe.MatchString(id) {
		return fmt.Errorf("projectx: id %q must be kebab-case (lowercase letter start, then [a-z0-9-])", id)
	}
	if strings.HasSuffix(id, "-") {
		return fmt.Errorf("projectx: id %q must not end with a hyphen", id)
	}
	if strings.Contains(id, "--") {
		return fmt.Errorf("projectx: id %q must not contain consecutive hyphens", id)
	}
	return nil
}
