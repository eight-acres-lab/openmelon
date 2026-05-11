package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/registry"
)

func TestRegistry_RegisterDispatchAndUnknown(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{
		Spec: Spec{Name: "echo", Description: "x", Parameters: json.RawMessage(`{}`)},
		Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
			return map[string]any{"got": string(raw)}, nil
		},
	})
	got, err := reg.Dispatch(context.Background(), "echo", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if m := got.(map[string]any); m["got"] != `{"x":1}` {
		t.Errorf("unexpected: %+v", m)
	}
	_, err = reg.Dispatch(context.Background(), "ghost", nil)
	if !errors.Is(err, ErrUnknownTool) {
		t.Errorf("expected ErrUnknownTool, got %v", err)
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{Spec: Spec{Name: "a", Parameters: json.RawMessage(`{}`)}, Handler: noopHandler})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	reg.Register(Tool{Spec: Spec{Name: "a", Parameters: json.RawMessage(`{}`)}, Handler: noopHandler})
}

func TestSafeJoinAllowsRelativeUnderBase(t *testing.T) {
	wd := t.TempDir()
	out, err := safeJoin(wd, "subdir/file.txt")
	if err != nil {
		t.Fatalf("safeJoin: %v", err)
	}
	if !strings.HasPrefix(out, wd) {
		t.Errorf("not under base: %q", out)
	}
}

func TestSafeJoinRejectsEscape(t *testing.T) {
	wd := t.TempDir()
	if _, err := safeJoin(wd, "../../etc/passwd"); err == nil {
		t.Error("expected error for parent-dir escape")
	}
	if _, err := safeJoin(wd, "/etc/passwd"); err == nil {
		t.Error("expected error for absolute path outside base")
	}
}

func TestListCharactersTool_FiltersBySubstring(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "ai-talks", "AI Talks"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	for _, c := range []registry.AddOptions{
		{Kind: registry.KindCharacter, Slug: "lao-wang", Name: "Lao Wang", Description: "vendor"},
		{Kind: registry.KindCharacter, Slug: "xiao-li", Name: "Xiao Li", Description: "photographer"},
	} {
		if _, err := registry.Add(wd, c); err != nil {
			t.Fatalf("registry add: %v", err)
		}
	}
	env := &Env{Workdir: wd}
	tool := listCharactersTool(env)
	res, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"vendor"}`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	list := res.([]map[string]any)
	if len(list) != 1 || list[0]["slug"] != "lao-wang" {
		t.Errorf("unexpected hits: %+v", list)
	}
}

func TestGetCharacterTool_ReturnsAbsoluteImagePaths(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "ai-talks", "AI Talks"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	srcImg := wd + "/portrait.png"
	if err := writeMinPNG(srcImg); err != nil {
		t.Fatalf("write png: %v", err)
	}
	if _, err := registry.Add(wd, registry.AddOptions{
		Kind: registry.KindCharacter, Slug: "lao-wang", Name: "Lao Wang",
		ImagePath: srcImg, ImageName: "portrait",
	}); err != nil {
		t.Fatalf("registry add: %v", err)
	}
	env := &Env{Workdir: wd}
	res, err := getCharacterTool(env).Handler(context.Background(), json.RawMessage(`{"slug":"lao-wang"}`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	m := res.(map[string]any)
	paths, ok := m["image_paths"].([]string)
	if !ok || len(paths) != 1 {
		t.Fatalf("image_paths: %+v", m["image_paths"])
	}
	if !strings.HasPrefix(paths[0], wd) {
		t.Errorf("not absolute under wd: %q", paths[0])
	}
}

func TestSearchTool_ReturnsRankedHits(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "ai-talks", "AI Talks"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	if _, err := registry.Add(wd, registry.AddOptions{
		Kind: registry.KindCharacter, Slug: "lao-wang", Name: "Lao Wang",
		Description: "vendor", Tags: []string{"vendor"},
	}); err != nil {
		t.Fatalf("registry add: %v", err)
	}
	res, err := searchTool(&Env{Workdir: wd}).Handler(context.Background(), json.RawMessage(`{"query":"vendor"}`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	list := res.([]map[string]any)
	if len(list) != 1 || list[0]["slug"] != "lao-wang" {
		t.Errorf("unexpected hits: %+v", list)
	}
}

func TestContinuityTools_CreateSpaceAndContextPacket(t *testing.T) {
	wd := t.TempDir()
	proj, err := projectx.Init(wd, "creator", "Creator")
	if err != nil {
		t.Fatalf("project init: %v", err)
	}
	env := &Env{Workdir: wd, Project: proj}
	if _, err := createSpaceTool(env).Handler(context.Background(), json.RawMessage(`{
		"id":"tennis-anime",
		"name":"Tennis Anime",
		"platform":"short-video",
		"audience":"beginners",
		"description":"Anime tennis lessons",
		"tags":["tennis","anime"],
		"assumptions":"# Assumptions\n\n- Maybe use a playful tone.\n"
	}`)); err != nil {
		t.Fatalf("create_space: %v", err)
	}
	if _, err := recordDecisionTool(env).Handler(context.Background(), json.RawMessage(`{
		"space_id":"tennis-anime",
		"decision":"Use clean anime illustration.",
		"target":"visual_style"
	}`)); err != nil {
		t.Fatalf("record_decision: %v", err)
	}
	res, err := getContextPacketTool(env).Handler(context.Background(), json.RawMessage(`{"space_id":"tennis-anime"}`))
	if err != nil {
		t.Fatalf("get_context_packet: %v", err)
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), "tennis-anime") || !strings.Contains(string(b), "clean anime") {
		t.Fatalf("packet missing continuity state: %s", string(b))
	}
	if strings.Contains(string(b), "Keep it playful") {
		t.Fatalf("create_space assumptions should not be promoted to canon: %s", string(b))
	}
}

func TestContinuityTools_DraftSpaceRequiresActivationBeforeEpisode(t *testing.T) {
	wd := t.TempDir()
	proj, err := projectx.Init(wd, "creator", "Creator")
	if err != nil {
		t.Fatalf("project init: %v", err)
	}
	env := &Env{Workdir: wd, Project: proj}
	if _, err := createSpaceTool(env).Handler(context.Background(), json.RawMessage(`{
		"id":"tennis-lessons",
		"name":"Tennis Lessons"
	}`)); err != nil {
		t.Fatalf("create_space: %v", err)
	}
	res, err := createEpisodeTool(env).Handler(context.Background(), json.RawMessage(`{
		"space_id":"tennis-lessons",
		"topic":"First lesson"
	}`))
	if err != nil {
		t.Fatalf("create_episode dispatch: %v", err)
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), "space tennis-lessons is draft") {
		t.Fatalf("expected draft rejection, got %s", string(b))
	}
	if _, err := activateSpaceTool(env).Handler(context.Background(), json.RawMessage(`{
		"space_id":"tennis-lessons",
		"decision":"User confirmed this should become a continuing tennis lesson series."
	}`)); err != nil {
		t.Fatalf("activate_space: %v", err)
	}
	res, err = createEpisodeTool(env).Handler(context.Background(), json.RawMessage(`{
		"space_id":"tennis-lessons",
		"id":"first-lesson",
		"topic":"First lesson"
	}`))
	if err != nil {
		t.Fatalf("create_episode after activation: %v", err)
	}
	b, _ = json.Marshal(res)
	if !strings.Contains(string(b), "first-lesson") {
		t.Fatalf("expected episode, got %s", string(b))
	}
}

func noopHandler(_ context.Context, _ json.RawMessage) (any, error) { return nil, nil }

func writeMinPNG(path string) error {
	return os.WriteFile(path, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 13}, 0o644)
}
