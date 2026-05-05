// builtin.go — the standard openmelon tool set.
//
// Tools come in two flavors:
//
//  1. Read-only project introspection: list_characters, get_character,
//     list_references, get_reference, search.
//  2. Side-effecting actions: generate_image, save_artifact,
//     compile_skill, finish.
//
// Side-effecting tools take a *Env that bundles the project workdir +
// any external clients (skillplus, image generator). Read-only tools
// only need the workdir.
//
// Registration is opt-in per tool — the runtime asks for "all read-only"
// or "everything" depending on whether keys are configured.

package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eight-acres-lab/openmelon/internal/imagegen"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/registry"
	"github.com/eight-acres-lab/openmelon/internal/search"
	"github.com/eight-acres-lab/openmelon/internal/skillplus"
)

// Env bundles all the dependencies the side-effecting tools need.
//
// SessionDir is the per-run directory under .openmelon/sessions/<id>/
// where intermediate artifacts (generated image bytes, prompts) are
// written. Required for generate_image / edit_image / save_artifact.
type Env struct {
	Workdir    string
	Project    *projectx.Project
	SessionDir string

	// Optional: nil means the matching tool is not registered. Runtime
	// decides which to wire based on what's configured.
	Compiler *skillplus.Compiler
	ImageGen imagegen.Generator
}

// RegisterAll registers the full tool set into reg. Side-effecting
// tools are registered only when their dependency in env is non-nil
// (e.g. generate_image needs env.ImageGen).
//
// Panics on duplicate registration — call this exactly once per
// Registry.
func RegisterAll(reg *Registry, env *Env) {
	// Read-only.
	reg.Register(listCharactersTool(env))
	reg.Register(getCharacterTool(env))
	reg.Register(listReferencesTool(env))
	reg.Register(getReferenceTool(env))
	reg.Register(searchTool(env))
	reg.Register(readFileTool(env))

	// Side-effecting.
	if env.Compiler != nil {
		reg.Register(compileSkillTool(env))
	}
	if env.ImageGen != nil {
		reg.Register(generateImageTool(env))
	}
	reg.Register(saveArtifactTool(env))
	reg.Register(finishTool())
}

// --- read-only tools ---

func listCharactersTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "list_characters",
			Description: "List all characters registered in this project. Optional substring filter on name+description.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Optional substring to filter by"}
				}
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct{ Query string }
			_ = json.Unmarshal(raw, &args)
			items, err := registry.List(env.Workdir, registry.KindCharacter)
			if err != nil {
				return nil, err
			}
			out := []map[string]any{}
			for _, it := range items {
				if args.Query != "" {
					hay := strings.ToLower(it.Name + " " + it.Description)
					if !strings.Contains(hay, strings.ToLower(args.Query)) {
						continue
					}
				}
				out = append(out, map[string]any{
					"slug":        it.Slug,
					"name":        it.Name,
					"description": it.Description,
					"tags":        it.Tags,
					"images":      len(it.Images),
				})
			}
			return out, nil
		},
	}
}

func getCharacterTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "get_character",
			Description: "Fetch a character's full details, including absolute paths to its portrait images so you can pass them as references to generate_image.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"slug": {"type": "string"}
				},
				"required": ["slug"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct{ Slug string }
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			it, err := registry.Get(env.Workdir, registry.KindCharacter, args.Slug)
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			return characterJSON(env.Workdir, it), nil
		},
	}
}

func listReferencesTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "list_references",
			Description: "List all reference images in this project — typically named scenes, lighting setups, or composition templates.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string"}
				}
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct{ Query string }
			_ = json.Unmarshal(raw, &args)
			items, err := registry.List(env.Workdir, registry.KindReference)
			if err != nil {
				return nil, err
			}
			out := []map[string]any{}
			for _, it := range items {
				if args.Query != "" {
					hay := strings.ToLower(it.Name + " " + it.Description)
					if !strings.Contains(hay, strings.ToLower(args.Query)) {
						continue
					}
				}
				out = append(out, map[string]any{
					"slug":        it.Slug,
					"name":        it.Name,
					"description": it.Description,
					"tags":        it.Tags,
					"images":      len(it.Images),
				})
			}
			return out, nil
		},
	}
}

func getReferenceTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "get_reference",
			Description: "Fetch a reference image's full details, including its absolute on-disk path so you can pass it to generate_image.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"slug": {"type": "string"}
				},
				"required": ["slug"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct{ Slug string }
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			it, err := registry.Get(env.Workdir, registry.KindReference, args.Slug)
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			return referenceJSON(env.Workdir, it), nil
		},
	}
}

func searchTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "search",
			Description: "Grep across the project's characters / references / materials. Supports tag:foo, kind:character, -negative, \"quoted phrases\". Returns a ranked list.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string"}
				},
				"required": ["query"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct{ Query string }
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			q, err := search.Parse(args.Query)
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			hits, err := search.Run(env.Workdir, q)
			if err != nil {
				return nil, err
			}
			out := []map[string]any{}
			for _, h := range hits {
				out = append(out, map[string]any{
					"kind":        string(h.Item.Kind),
					"slug":        h.Item.Slug,
					"name":        h.Item.Name,
					"description": h.Item.Description,
					"tags":        h.Item.Tags,
					"score":       h.Score,
				})
			}
			return out, nil
		},
	}
}

func readFileTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "read_file",
			Description: "Read a UTF-8 text file from inside the project workdir. Paths are resolved relative to the project root and may not escape it.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string"}
				},
				"required": ["path"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct{ Path string }
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			abs, err := safeJoin(env.Workdir, args.Path)
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			b, err := os.ReadFile(abs)
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			return map[string]any{"path": args.Path, "content": string(b)}, nil
		},
	}
}

// --- side-effecting tools ---

func compileSkillTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "compile_skill",
			Description: "Compile a skillplus package and return its compiled prompt + output schema. Use this when a registered skill exists for the task.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"skill": {"type": "string", "description": "skill spec, e.g. skillplus:food-street-realism or path:/abs/dir"},
					"locale": {"type": "string"},
					"model_profile": {"type": "string"},
					"vars": {"type": "object", "additionalProperties": {"type": "string"}}
				},
				"required": ["skill"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Skill        string
				Locale       string
				ModelProfile string `json:"model_profile"`
				Vars         map[string]string
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			if args.Locale == "" {
				args.Locale = "zh-CN"
			}
			if args.ModelProfile == "" {
				args.ModelProfile = "gpt-image-family"
			}
			compiled, err := env.Compiler.CompileRaw(ctx, &skillplus.CompileRequest{
				PackagePath:  args.Skill, // compiler resolves skillplus:... too
				Target:       "openmelon",
				ModelProfile: args.ModelProfile,
				Locale:       args.Locale,
				Vars:         args.Vars,
			})
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			var compiledMap map[string]any
			if err := json.Unmarshal(compiled, &compiledMap); err != nil {
				return map[string]any{"error": "compiler returned invalid JSON"}, nil
			}
			return compiledMap, nil
		},
	}
}

func generateImageTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "generate_image",
			Description: "Generate a single image and save it into the current session. Optionally pass reference_images (absolute paths) to anchor the result to known characters or scenes.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"prompt": {"type": "string"},
					"reference_images": {
						"type": "array",
						"items": {"type": "string", "description": "absolute path"}
					},
					"size": {"type": "string", "description": "WxH, vendor-default if omitted"},
					"label": {"type": "string", "description": "short label saved into the session metadata, e.g. \"draft-1\""}
				},
				"required": ["prompt"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Prompt          string
				ReferenceImages []string `json:"reference_images"`
				Size            string
				Label           string
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			refs := make([][]byte, 0, len(args.ReferenceImages))
			for _, p := range args.ReferenceImages {
				b, err := os.ReadFile(p)
				if err != nil {
					return map[string]any{"error": fmt.Sprintf("read reference %s: %v", p, err)}, nil
				}
				refs = append(refs, b)
			}
			res, err := env.ImageGen.Generate(ctx, imagegen.GenerateOptions{
				Prompt:           args.Prompt,
				Size:             args.Size,
				ReferenceImages:  refs,
			})
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			label := args.Label
			if label == "" {
				label = "image"
			}
			ts := time.Now().UTC().Format("150405")
			ext := extensionFor(res.ContentType)
			outName := fmt.Sprintf("%s-%s%s", label, ts, ext)
			outPath := filepath.Join(env.SessionDir, outName)
			if err := os.MkdirAll(env.SessionDir, 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(outPath, res.Data, 0o644); err != nil {
				return nil, err
			}
			h := sha256.Sum256(res.Data)
			return map[string]any{
				"path":       outPath,
				"label":      label,
				"sha256":     hex.EncodeToString(h[:]),
				"size_bytes": res.SizeBytes,
				"prompt":     args.Prompt,
			}, nil
		},
	}
}

func saveArtifactTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "save_artifact",
			Description: "Promote a session image to a permanent artifact under .openmelon/artifacts/<slug>/<timestamp>/.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"slug": {"type": "string", "description": "kebab-case label for this artifact bucket"},
					"image_path": {"type": "string", "description": "absolute path returned by an earlier generate_image call"},
					"prompt": {"type": "string", "description": "the prompt used; recorded for provenance"}
				},
				"required": ["slug", "image_path"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Slug      string
				ImagePath string `json:"image_path"`
				Prompt    string
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			if err := registry.ValidateSlug(args.Slug); err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			b, err := os.ReadFile(args.ImagePath)
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			ts := time.Now().UTC().Format("20060102-150405")
			outDir := filepath.Join(projectx.StateDir(env.Workdir), "artifacts", args.Slug, ts)
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return nil, err
			}
			ext := filepath.Ext(args.ImagePath)
			if ext == "" {
				ext = ".png"
			}
			outPath := filepath.Join(outDir, "image"+ext)
			if err := os.WriteFile(outPath, b, 0o644); err != nil {
				return nil, err
			}
			if args.Prompt != "" {
				_ = os.WriteFile(filepath.Join(outDir, "prompt.txt"), []byte(args.Prompt), 0o644)
			}
			h := sha256.Sum256(b)
			return map[string]any{
				"path":   outPath,
				"sha256": hex.EncodeToString(h[:]),
			}, nil
		},
	}
}

func finishTool() Tool {
	return Tool{
		Spec: Spec{
			Name:        "finish",
			Description: "Signal that you've completed the task. Provide a one- to two-paragraph summary the user will see, plus any final artifact paths.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"summary": {"type": "string"},
					"artifacts": {
						"type": "array",
						"items": {"type": "string"},
						"description": "Absolute paths to final outputs"
					}
				},
				"required": ["summary"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			// finish is a sentinel — its return value is read by the
			// runtime which then exits the loop.
			var args struct {
				Summary   string
				Artifacts []string
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			return map[string]any{"summary": args.Summary, "artifacts": args.Artifacts, "ok": true}, nil
		},
	}
}

// --- helpers ---

func characterJSON(workdir string, it *registry.Item) map[string]any {
	out := map[string]any{
		"slug":        it.Slug,
		"name":        it.Name,
		"description": it.Description,
		"tags":        it.Tags,
		"extra":       it.Extra,
	}
	out["image_paths"] = absoluteImagePaths(workdir, registry.KindCharacter, it)
	return out
}

func referenceJSON(workdir string, it *registry.Item) map[string]any {
	out := map[string]any{
		"slug":        it.Slug,
		"name":        it.Name,
		"description": it.Description,
		"tags":        it.Tags,
		"extra":       it.Extra,
	}
	out["image_paths"] = absoluteImagePaths(workdir, registry.KindReference, it)
	return out
}

func absoluteImagePaths(workdir string, kind registry.Kind, it *registry.Item) []string {
	if len(it.Images) == 0 {
		return nil
	}
	base := filepath.Join(projectx.StateDir(workdir), kindDir(kind), it.Slug)
	out := make([]string, len(it.Images))
	for i, n := range it.Images {
		out[i] = filepath.Join(base, n)
	}
	return out
}

// kindDir returns the on-disk subdirectory for a kind. Mirrors registry's
// internal mapping but kept local so we don't expose an internal helper.
func kindDir(k registry.Kind) string {
	switch k {
	case registry.KindCharacter:
		return "characters"
	case registry.KindReference:
		return "references"
	case registry.KindMaterial:
		return "materials"
	}
	return ""
}

// safeJoin returns base/path as an absolute path, returning an error if
// path tries to escape base via "..".
func safeJoin(base, path string) (string, error) {
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) {
		// Allow absolute paths only if they live under base.
		absBase, _ := filepath.Abs(base)
		if !strings.HasPrefix(clean, absBase+string(filepath.Separator)) && clean != absBase {
			return "", fmt.Errorf("path %q escapes project workdir", path)
		}
		return clean, nil
	}
	out := filepath.Join(base, clean)
	rel, err := filepath.Rel(base, out)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q escapes project workdir", path)
	}
	return out, nil
}

func extensionFor(contentType string) string {
	switch contentType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	}
	return ".png"
}
