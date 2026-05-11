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

	"github.com/eight-acres-lab/openmelon/internal/continuity"
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

	// Approve, when non-nil, is called by tools that need explicit
	// user confirmation before running (notably bash). Returns the
	// user's decision. Synchronous — the tool blocks until the user
	// answers via whatever UI is wired (TUI modal, stdin prompt).
	// nil means tools that need approval default-deny.
	Approve func(req ApprovalRequest) ApprovalDecision

	// JudgeBash, when non-nil, is called BEFORE Approve. It classifies
	// a command into AUTO / ASK / BLOCK; only ASK reaches the user.
	// Typically backed by a small LLM call. nil means every command
	// goes straight to Approve.
	JudgeBash func(ctx context.Context, command, description string) BashJudgement

	// IsBashAllowed returns true when the binary (extracted from the
	// command's first token) is on the per-session allowlist —
	// previous "Yes, always" decisions populate it. nil → never
	// auto-allow.
	IsBashAllowed func(binary string) bool

	// AllowBash adds binary to the per-session allowlist. Called by
	// the bash tool when the user picks "Yes, always" in the
	// approval modal.
	AllowBash func(binary string)

	// BashMode is the project's effective permission mode (strict /
	// auto / trusted). The bash tool reads this each call. Empty
	// string defaults to strict.
	BashMode string
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
	reg.Register(listSpacesTool(env))
	reg.Register(createSpaceTool(env))
	reg.Register(getContextPacketTool(env))
	reg.Register(activateSpaceTool(env))
	reg.Register(recordDecisionTool(env))
	reg.Register(recordFeedbackTool(env))
	reg.Register(createEpisodeTool(env))
	reg.Register(registerAssetTool(env))

	// Side-effecting.
	if env.Compiler != nil {
		reg.Register(compileSkillTool(env))
	}
	if env.ImageGen != nil {
		reg.Register(generateImageTool(env))
	}
	reg.Register(saveArtifactTool(env))
	reg.Register(bashTool(env))
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

func listSpacesTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "list_spaces",
			Description: "List or search creative continuity spaces. Use this before starting a long-running series, continuing one, or deciding whether a request belongs to an existing space.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Optional search query over space id, name, description, platform, audience, and tags"}
				}
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct{ Query string }
			_ = json.Unmarshal(raw, &args)
			if strings.TrimSpace(args.Query) != "" {
				hits, err := continuity.SearchSpaces(env.Workdir, args.Query)
				if err != nil {
					return nil, err
				}
				out := []map[string]any{}
				for _, h := range hits {
					out = append(out, map[string]any{
						"score":       h.Score,
						"id":          h.Space.ID,
						"name":        h.Space.Name,
						"status":      h.Space.Status,
						"platform":    h.Space.Platform,
						"audience":    h.Space.Audience,
						"description": h.Space.Description,
						"tags":        h.Space.Tags,
					})
				}
				return out, nil
			}
			spaces, err := continuity.ListSpaces(env.Workdir)
			if err != nil {
				return nil, err
			}
			out := []map[string]any{}
			for _, sp := range spaces {
				out = append(out, map[string]any{
					"id":          sp.ID,
					"name":        sp.Name,
					"status":      sp.Status,
					"platform":    sp.Platform,
					"audience":    sp.Audience,
					"description": sp.Description,
					"tags":        sp.Tags,
				})
			}
			return out, nil
		},
	}
}

func createSpaceTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "create_space",
			Description: "Create a draft creative continuity space for a durable series/account/campaign context. This tool stores only provisional assumptions, not confirmed canon. Ask concise clarification questions before treating assumptions as long-term rules.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "kebab-case space id"},
					"name": {"type": "string"},
					"platform": {"type": "string"},
					"audience": {"type": "string"},
					"description": {"type": "string"},
					"tags": {"type": "array", "items": {"type": "string"}},
					"assumptions": {"type": "string", "description": "Provisional setup assumptions and open questions. Low authority until the user confirms them."}
				},
				"required": ["id", "name"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				ID          string
				Name        string
				Platform    string
				Audience    string
				Description string
				Tags        []string
				Assumptions string
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			sp, err := continuity.CreateSpace(env.Workdir, continuity.CreateSpaceOptions{
				ID:          args.ID,
				Name:        args.Name,
				Platform:    args.Platform,
				Audience:    args.Audience,
				Description: args.Description,
				Tags:        args.Tags,
				Assumptions: args.Assumptions,
			})
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			return map[string]any{
				"id":          sp.ID,
				"name":        sp.Name,
				"status":      sp.Status,
				"description": sp.Description,
				"dir":         continuity.SpaceDir(env.Workdir, sp.ID),
				"next_action": "Ask the user to confirm or correct the provisional assumptions before recording decisions or treating them as canon.",
			}, nil
		},
	}
}

func getContextPacketTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "get_context_packet",
			Description: "Fetch the model-readable continuity context packet for a creative space: authority notes, provisional assumptions, confirmed canon, memory, plan, recent decisions, feedback, episodes, and assets. Use before producing or continuing content in that space.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"space_id": {"type": "string"}
				},
				"required": ["space_id"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SpaceID string `json:"space_id"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			projectID := ""
			if env.Project != nil {
				projectID = env.Project.ID
			}
			p, err := continuity.BuildContextPacket(env.Workdir, projectID, args.SpaceID)
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			return p, nil
		},
	}
}

func activateSpaceTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "activate_space",
			Description: "Activate a draft creative space after the user explicitly confirms the core direction. Records the confirmation as a decision. Use before creating durable episodes in a new space.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"space_id": {"type": "string"},
					"decision": {"type": "string", "description": "What the user confirmed"},
					"reason": {"type": "string"},
					"weight": {"type": "number"}
				},
				"required": ["space_id", "decision"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SpaceID  string `json:"space_id"`
				Decision string
				Reason   string
				Weight   float64
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			sp, d, err := continuity.ActivateSpace(env.Workdir, args.SpaceID, continuity.Decision{
				Decision: args.Decision,
				Reason:   args.Reason,
				Weight:   args.Weight,
			})
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			return map[string]any{
				"id":       sp.ID,
				"name":     sp.Name,
				"status":   sp.Status,
				"decision": d,
			}, nil
		},
	}
}

func recordDecisionTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "record_decision",
			Description: "Record a user-confirmed continuity decision for a creative space. Do not use for guesses; only record decisions the user accepted or clearly instructed.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"space_id": {"type": "string"},
					"scope": {"type": "string", "description": "space, episode, asset, style, character, scene"},
					"target": {"type": "string"},
					"decision": {"type": "string"},
					"reason": {"type": "string"},
					"weight": {"type": "number"}
				},
				"required": ["space_id", "decision"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SpaceID  string `json:"space_id"`
				Scope    string
				Target   string
				Decision string
				Reason   string
				Weight   float64
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			d, err := continuity.RecordDecision(env.Workdir, args.SpaceID, continuity.Decision{
				Scope:    args.Scope,
				Target:   args.Target,
				Decision: args.Decision,
				Reason:   args.Reason,
				Weight:   args.Weight,
			})
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			return d, nil
		},
	}
}

func recordFeedbackTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "record_feedback",
			Description: "Record user or audience feedback for a creative space so future production can adapt strategy, pacing, style, assets, or planning.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"space_id": {"type": "string"},
					"episode_id": {"type": "string"},
					"source": {"type": "string"},
					"signal": {"type": "string", "description": "normalized signal, e.g. pace_too_fast, style_worked, asset_drift"},
					"evidence": {"type": "string"},
					"recommendation": {"type": "string"}
				},
				"required": ["space_id", "signal"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SpaceID        string `json:"space_id"`
				EpisodeID      string `json:"episode_id"`
				Source         string
				Signal         string
				Evidence       string
				Recommendation string
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			f, err := continuity.RecordFeedback(env.Workdir, args.SpaceID, continuity.Feedback{
				EpisodeID:      args.EpisodeID,
				Source:         args.Source,
				Signal:         args.Signal,
				Evidence:       args.Evidence,
				Recommendation: args.Recommendation,
			})
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			return f, nil
		},
	}
}

func createEpisodeTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "create_episode",
			Description: "Create or register an episode under a creative space. Use for durable production units such as daily posts, videos, chapters, or content installments.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"space_id": {"type": "string"},
					"id": {"type": "string"},
					"title": {"type": "string"},
					"topic": {"type": "string"},
					"status": {"type": "string"},
					"brief": {"type": "string", "description": "Brief markdown"}
				},
				"required": ["space_id", "topic"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SpaceID string `json:"space_id"`
				ID      string
				Title   string
				Topic   string
				Status  string
				Brief   string
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			ep, err := continuity.CreateEpisode(env.Workdir, args.SpaceID, continuity.Episode{
				ID:     args.ID,
				Title:  args.Title,
				Topic:  args.Topic,
				Status: args.Status,
				Brief:  args.Brief,
			})
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			return ep, nil
		},
	}
}

func registerAssetTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "register_asset",
			Description: "Register a reusable continuity asset under a creative space. Assets can be images, backgrounds, characters, props, prompt fragments, shot specs, masks, or PSD/layered files.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"space_id": {"type": "string"},
					"id": {"type": "string"},
					"kind": {"type": "string"},
					"status": {"type": "string", "description": "active, canonical, experimental, rejected, archived"},
					"description": {"type": "string"},
					"reuse_policy": {"type": "string"},
					"files": {"type": "array", "items": {"type": "string"}},
					"tags": {"type": "array", "items": {"type": "string"}},
					"weight": {"type": "number"}
				},
				"required": ["space_id", "description"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				SpaceID     string `json:"space_id"`
				ID          string
				Kind        string
				Status      string
				Description string
				ReusePolicy string `json:"reuse_policy"`
				Files       []string
				Tags        []string
				Weight      float64
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			a, err := continuity.RegisterAsset(env.Workdir, args.SpaceID, continuity.Asset{
				ID:          args.ID,
				Kind:        args.Kind,
				Status:      args.Status,
				Description: args.Description,
				ReusePolicy: args.ReusePolicy,
				Files:       args.Files,
				Tags:        args.Tags,
				Weight:      args.Weight,
			})
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			return a, nil
		},
	}
}

// --- side-effecting tools ---

func compileSkillTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name:        "compile_skill",
			Description: "Compile a skillplus package and return its compiled prompt + output schema. Pass the BARE skill slug (e.g. \"brand-logo\"), not \"skillplus:brand-logo\".",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"skill": {
						"type": "string",
						"description": "Bare skill slug (e.g. \"brand-logo\", \"food-street-realism\") OR an absolute path to a .skillplus directory. Do NOT prefix with \"skillplus:\"."
					},
					"locale": {
						"type": "string",
						"description": "Locale to compile for. Allowed: \"zh-CN\" or \"en\". Default zh-CN.",
						"enum": ["zh-CN", "en"]
					},
					"model_profile": {
						"type": "string",
						"description": "Per-skill prompt overlay slug. Default \"gpt-image-family\"."
					},
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
			// Be forgiving: strip "skillplus:" / "path:" prefixes the
			// model might have learned from older docs. The CLI only
			// accepts a bare slug or an absolute path.
			args.Skill = strings.TrimPrefix(args.Skill, "skillplus:")
			args.Skill = strings.TrimPrefix(args.Skill, "path:")

			args.Locale = normalizeLocale(args.Locale)
			if args.ModelProfile == "" {
				args.ModelProfile = "gpt-image-family"
			}
			compiled, err := env.Compiler.CompileRaw(ctx, &skillplus.CompileRequest{
				PackagePath:  args.Skill,
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

// normalizeLocale maps loose locale strings the model might emit
// ("zh", "chinese", "cn") to the canonical values skillplus accepts
// ("zh-CN", "en"). Empty / unknown defaults to zh-CN.
func normalizeLocale(in string) string {
	v := strings.ToLower(strings.TrimSpace(in))
	switch v {
	case "", "zh", "zh-cn", "zh_cn", "chinese", "cn":
		return "zh-CN"
	case "en", "en-us", "english", "us":
		return "en"
	}
	// Unknown — pass through, let skillplus error if it's truly invalid.
	return in
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
				Prompt:          args.Prompt,
				Size:            args.Size,
				ReferenceImages: refs,
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
