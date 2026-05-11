package parity

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/continuity"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/runtime"
	"github.com/eight-acres-lab/openmelon/internal/tools"
)

func TestCreatorWorkflow_NewTopicCreatesDraftButNotEpisode(t *testing.T) {
	wd := t.TempDir()
	proj, err := projectx.Init(wd, "creator", "Creator")
	if err != nil {
		t.Fatalf("project init: %v", err)
	}
	reg := tools.NewRegistry()
	tools.RegisterAll(reg, &tools.Env{Workdir: wd, Project: proj})
	model := &scriptedModel{t: t, responses: []llm.ChatResponse{
		toolResponse("c1", "create_space", `{
			"id":"tennis-anime",
			"name":"Tennis Anime",
			"description":"Anime tennis lessons",
			"assumptions":"# Assumptions\n\n- Provisional cheerful anime style.\n"
		}`),
		stopResponse("I created a draft space and need confirmation before episodes."),
	}}
	rt := &runtime.Runtime{LLM: model, Registry: reg}
	res, err := rt.Run(context.Background(), runtime.RunInput{SystemPrompt: "creator", UserInput: "Start a tennis anime lesson series"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Finished {
		t.Fatal("run did not finish")
	}
	sp, err := continuity.GetSpace(wd, "tennis-anime")
	if err != nil {
		t.Fatalf("space missing: %v", err)
	}
	if sp.Status != "draft" {
		t.Fatalf("space status = %q, want draft", sp.Status)
	}
	if _, err := continuity.CreateEpisode(wd, "tennis-anime", continuity.Episode{ID: "should-fail", Topic: "too soon"}); err == nil {
		t.Fatal("draft space allowed durable episode")
	}
	canon, _ := continuity.ReadCanon(wd, "tennis-anime")
	if strings.Contains(canon, "cheerful anime") {
		t.Fatalf("assumption leaked into canon: %s", canon)
	}
}

func TestCreatorWorkflow_ActivationEnablesEpisodeAndFeedbackContext(t *testing.T) {
	wd := t.TempDir()
	proj, err := projectx.Init(wd, "creator", "Creator")
	if err != nil {
		t.Fatalf("project init: %v", err)
	}
	if _, err := continuity.CreateSpace(wd, continuity.CreateSpaceOptions{ID: "tennis-anime", Name: "Tennis Anime"}); err != nil {
		t.Fatalf("create space: %v", err)
	}
	if _, _, err := continuity.ActivateSpace(wd, "tennis-anime", continuity.Decision{Decision: "User confirmed anime tennis lessons."}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := continuity.CreateEpisode(wd, "tennis-anime", continuity.Episode{ID: "serve-basics", Topic: "Serve basics"}); err != nil {
		t.Fatalf("episode: %v", err)
	}
	if _, err := continuity.RecordFeedback(wd, "tennis-anime", continuity.Feedback{
		EpisodeID:      "serve-basics",
		Signal:         "pace_too_fast",
		Recommendation: "Use fewer terms next episode.",
	}); err != nil {
		t.Fatalf("feedback: %v", err)
	}
	packet, err := continuity.BuildContextPacket(wd, proj.ID, "tennis-anime")
	if err != nil {
		t.Fatalf("packet: %v", err)
	}
	if packet.Space.Status != "active" || len(packet.RecentEpisodes) != 1 || len(packet.RecentFeedback) != 1 {
		t.Fatalf("context packet missing active continuity: %+v", packet)
	}
	if packet.RecentFeedback[0].Signal != "pace_too_fast" {
		t.Fatalf("feedback not preserved: %+v", packet.RecentFeedback)
	}
}

func TestCreatorWorkflow_ReuseExistingSpaceAndRankAssets(t *testing.T) {
	wd := t.TempDir()
	proj, err := projectx.Init(wd, "creator", "Creator")
	if err != nil {
		t.Fatalf("project init: %v", err)
	}
	if _, err := continuity.CreateSpace(wd, continuity.CreateSpaceOptions{ID: "tennis-anime", Name: "Tennis Anime", Tags: []string{"tennis"}}); err != nil {
		t.Fatalf("create tennis: %v", err)
	}
	if _, _, err := continuity.ActivateSpace(wd, "tennis-anime", continuity.Decision{Decision: "confirmed"}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := continuity.CreateSpace(wd, continuity.CreateSpaceOptions{ID: "food-review", Name: "Food Review", Tags: []string{"food"}}); err != nil {
		t.Fatalf("create food: %v", err)
	}
	if _, err := continuity.RegisterAsset(wd, "tennis-anime", continuity.Asset{ID: "weak-bg", Description: "experimental court", Weight: 0.2}); err != nil {
		t.Fatalf("weak asset: %v", err)
	}
	if _, err := continuity.RegisterAsset(wd, "tennis-anime", continuity.Asset{ID: "hero-bg", Description: "canonical court", Weight: 2.0}); err != nil {
		t.Fatalf("hero asset: %v", err)
	}
	hits, err := continuity.SearchSpaces(wd, "continue tennis")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 || hits[0].Space.ID != "tennis-anime" {
		t.Fatalf("did not rank existing tennis space first: %+v", hits)
	}
	packet, err := continuity.BuildContextPacket(wd, proj.ID, "tennis-anime")
	if err != nil {
		t.Fatalf("packet: %v", err)
	}
	if len(packet.Assets) < 2 || packet.Assets[0].ID != "hero-bg" {
		t.Fatalf("assets not ranked by weight: %+v", packet.Assets)
	}
}

func TestCreatorWorkflow_FeedbackCanDemoteAsset(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "creator", "Creator"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	if _, err := continuity.CreateSpace(wd, continuity.CreateSpaceOptions{ID: "tennis-anime", Name: "Tennis Anime"}); err != nil {
		t.Fatalf("create space: %v", err)
	}
	if _, err := continuity.RegisterAsset(wd, "tennis-anime", continuity.Asset{ID: "drifty-room", Description: "room with drift", Weight: 2.0}); err != nil {
		t.Fatalf("asset a: %v", err)
	}
	if _, err := continuity.RegisterAsset(wd, "tennis-anime", continuity.Asset{ID: "stable-room", Description: "stable room", Weight: 1.0}); err != nil {
		t.Fatalf("asset b: %v", err)
	}
	if _, err := continuity.UpdateAssetWeight(wd, "tennis-anime", "drifty-room", 0.1, "archived"); err != nil {
		t.Fatalf("demote: %v", err)
	}
	packet, err := continuity.BuildContextPacket(wd, "creator", "tennis-anime")
	if err != nil {
		t.Fatalf("packet: %v", err)
	}
	if packet.Assets[0].ID != "stable-room" {
		t.Fatalf("demoted asset still ranked first: %+v", packet.Assets)
	}
}

func TestCreatorWorkflow_SelectedContextCarriesBudgetAndReasons(t *testing.T) {
	wd := t.TempDir()
	proj, err := projectx.Init(wd, "creator", "Creator")
	if err != nil {
		t.Fatalf("project init: %v", err)
	}
	if _, err := continuity.CreateSpace(wd, continuity.CreateSpaceOptions{ID: "tennis-anime", Name: "Tennis Anime"}); err != nil {
		t.Fatalf("create space: %v", err)
	}
	if _, _, err := continuity.ActivateSpace(wd, "tennis-anime", continuity.Decision{Decision: "confirmed"}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	for _, d := range []string{"visual style", "voice", "episode format"} {
		if _, err := continuity.RecordDecision(wd, "tennis-anime", continuity.Decision{Decision: d}); err != nil {
			t.Fatalf("decision: %v", err)
		}
	}
	packet, err := continuity.BuildSelectedContextPacket(wd, proj.ID, "tennis-anime", continuity.SelectionOptions{Query: "continue", MaxDecisions: 1})
	if err != nil {
		t.Fatalf("packet: %v", err)
	}
	if packet.Selection == nil || packet.Selection.DecisionLimit != 1 || len(packet.Selection.Reasons) == 0 {
		t.Fatalf("selection metadata missing: %+v", packet.Selection)
	}
	if len(packet.RecentDecisions) != 1 || len(packet.Selection.Truncated) == 0 {
		t.Fatalf("selected context did not respect budget: %+v", packet)
	}
}

func TestCreatorWorkflow_MemoryStaysProvisionalUntilPromoted(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "creator", "Creator"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	if _, err := continuity.CreateSpace(wd, continuity.CreateSpaceOptions{ID: "tennis-anime", Name: "Tennis Anime"}); err != nil {
		t.Fatalf("create space: %v", err)
	}
	if _, err := continuity.RecordMemoryItem(wd, "tennis-anime", continuity.MemoryItem{
		ID:      "mem-calm",
		Content: "Maybe use calmer explanations.",
	}); err != nil {
		t.Fatalf("memory: %v", err)
	}
	packet, err := continuity.BuildContextPacket(wd, "creator", "tennis-anime")
	if err != nil {
		t.Fatalf("packet: %v", err)
	}
	if len(packet.RecentDecisions) != 0 {
		t.Fatalf("provisional memory became decision: %+v", packet.RecentDecisions)
	}
	if _, err := continuity.PromoteMemoryItem(wd, "tennis-anime", continuity.MemoryPromotion{
		ItemID:   "mem-calm",
		Decision: "Use calmer explanations.",
	}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	packet, err = continuity.BuildContextPacket(wd, "creator", "tennis-anime")
	if err != nil {
		t.Fatalf("packet after promote: %v", err)
	}
	if len(packet.RecentDecisions) != 1 || !strings.Contains(packet.RecentDecisions[0].Decision, "calmer") {
		t.Fatalf("promotion did not create decision: %+v", packet.RecentDecisions)
	}
}

func TestCreatorWorkflow_PendingInputAppliesBeforeNextModelCall(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Spec: tools.Spec{Name: "noop", Description: "noop", Parameters: json.RawMessage(`{"type":"object"}`)},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	})
	model := &scriptedModel{t: t, responses: []llm.ChatResponse{
		toolResponse("c1", "noop", `{}`),
		stopResponse("done"),
	}}
	drains := 0
	rt := &runtime.Runtime{
		LLM:      model,
		Registry: reg,
		DrainUserInput: func() []string {
			drains++
			if drains == 2 {
				return []string{"Make the next shot more playful."}
			}
			return nil
		},
	}
	if _, err := rt.Run(context.Background(), runtime.RunInput{SystemPrompt: "creator", UserInput: "continue"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(model.requests))
	}
	got := model.requests[1].Messages[len(model.requests[1].Messages)-1]
	if got.Role != llm.RoleUser || got.Content != "Make the next shot more playful." {
		t.Fatalf("pending input not included before next model call: %+v", got)
	}
}

type scriptedModel struct {
	t         *testing.T
	responses []llm.ChatResponse
	requests  []llm.ChatRequest
}

func (m *scriptedModel) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if len(m.responses) == 0 {
		m.t.Fatal("scripted model ran out of responses")
	}
	m.requests = append(m.requests, req)
	resp := m.responses[0]
	m.responses = m.responses[1:]
	return &resp, nil
}

func toolResponse(id, name, args string) llm.ChatResponse {
	return llm.ChatResponse{
		Message: llm.Message{
			Role:      llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: json.RawMessage(args)}},
		},
		FinishReason: llm.FinishToolCalls,
	}
}

func stopResponse(text string) llm.ChatResponse {
	return llm.ChatResponse{
		Message:      llm.Message{Role: llm.RoleAssistant, Content: text},
		FinishReason: llm.FinishStop,
	}
}
