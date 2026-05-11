package continuity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
)

func TestCreateSpaceWritesFiles(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "creator", "Creator"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	sp, err := CreateSpace(wd, CreateSpaceOptions{
		ID:          "tennis-anime",
		Name:        "Tennis Anime",
		Platform:    "short-video",
		Audience:    "beginners",
		Description: "Teach tennis with anime panels.",
		Tags:        []string{"tennis", "anime", "tennis"},
		Assumptions: "# Assumptions\n\n- Maybe use a playful tone.\n",
	})
	if err != nil {
		t.Fatalf("CreateSpace: %v", err)
	}
	if sp.Status != "draft" || len(sp.Tags) != 2 {
		t.Fatalf("space fields: %+v", sp)
	}
	for _, p := range []string{
		SpaceFileName,
		AssumptionsFileName,
		CanonFileName,
		MemoryFileName,
		PlanFileName,
		filepath.Join(EpisodesDirName),
		filepath.Join(AssetsDirName),
	} {
		if _, err := os.Stat(filepath.Join(SpaceDir(wd, sp.ID), p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	canon, err := ReadCanon(wd, sp.ID)
	if err != nil {
		t.Fatalf("ReadCanon: %v", err)
	}
	if strings.Contains(canon, "Keep it playful") {
		t.Fatalf("create space should not promote assumptions into canon: %q", canon)
	}
}

func TestSearchSpacesRanksMatches(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "creator", "Creator"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	if _, err := CreateSpace(wd, CreateSpaceOptions{ID: "tennis-anime", Name: "Tennis Anime", Tags: []string{"tennis"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateSpace(wd, CreateSpaceOptions{ID: "food-reviews", Name: "Food Reviews", Tags: []string{"food"}}); err != nil {
		t.Fatal(err)
	}
	hits, err := SearchSpaces(wd, "tennis")
	if err != nil {
		t.Fatalf("SearchSpaces: %v", err)
	}
	if len(hits) != 1 || hits[0].Space.ID != "tennis-anime" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}

func TestContextPacketIncludesRecentState(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "creator", "Creator"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	if _, err := CreateSpace(wd, CreateSpaceOptions{ID: "tennis-anime", Name: "Tennis Anime"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateEpisode(wd, "tennis-anime", Episode{ID: "too-soon", Topic: "Too soon"}); err == nil {
		t.Fatal("expected draft space to reject durable episode creation")
	}
	if _, _, err := ActivateSpace(wd, "tennis-anime", Decision{Decision: "User confirmed the core tennis anime direction."}); err != nil {
		t.Fatalf("ActivateSpace: %v", err)
	}
	if _, err := RecordDecision(wd, "tennis-anime", Decision{Decision: "Use clean anime style."}); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if _, err := RecordFeedback(wd, "tennis-anime", Feedback{Signal: "pace_too_fast"}); err != nil {
		t.Fatalf("RecordFeedback: %v", err)
	}
	if _, err := CreateEpisode(wd, "tennis-anime", Episode{ID: "serve-basics", Topic: "Serve basics", Brief: "Teach serving."}); err != nil {
		t.Fatalf("CreateEpisode: %v", err)
	}
	if _, err := RegisterAsset(wd, "tennis-anime", Asset{ID: "court-bg", Kind: "background", Description: "Default court."}); err != nil {
		t.Fatalf("RegisterAsset: %v", err)
	}
	p, err := BuildContextPacket(wd, "creator", "tennis-anime")
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if p.Space.ID != "tennis-anime" || p.Space.Status != "active" || p.Assumptions == "" || p.Canon == "" || len(p.RecentDecisions) != 2 || len(p.RecentFeedback) != 1 || len(p.RecentEpisodes) != 1 || len(p.Assets) != 1 {
		t.Fatalf("packet missing state: %+v", p)
	}
}

func TestSelectedContextPacketAppliesLimitsAndRanksAssets(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "creator", "Creator"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	if _, err := CreateSpace(wd, CreateSpaceOptions{ID: "tennis-anime", Name: "Tennis Anime"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ActivateSpace(wd, "tennis-anime", Decision{Decision: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"one", "two", "three"} {
		if _, err := RecordDecision(wd, "tennis-anime", Decision{Decision: d}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := RegisterAsset(wd, "tennis-anime", Asset{ID: "generic-room", Description: "plain room", Weight: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterAsset(wd, "tennis-anime", Asset{ID: "serve-court", Description: "court for serving drills", Weight: 1, Tags: []string{"serve"}}); err != nil {
		t.Fatal(err)
	}
	p, err := BuildSelectedContextPacket(wd, "creator", "tennis-anime", SelectionOptions{
		Query:        "serve lesson",
		MaxDecisions: 2,
		MaxAssets:    2,
	})
	if err != nil {
		t.Fatalf("BuildSelectedContextPacket: %v", err)
	}
	if len(p.RecentDecisions) != 2 || p.Selection.DecisionLimit != 2 {
		t.Fatalf("decision limit not applied: %+v", p.Selection)
	}
	if len(p.Selection.Truncated) == 0 || !containsString(p.Selection.Truncated, "recent_decisions") {
		t.Fatalf("expected truncation marker, got %+v", p.Selection.Truncated)
	}
	if len(p.Assets) < 2 || p.Assets[0].ID != "serve-court" {
		t.Fatalf("query asset ranking failed: %+v", p.Assets)
	}
}

func TestMemoryItemPromotionCreatesDecision(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "creator", "Creator"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	if _, err := CreateSpace(wd, CreateSpaceOptions{ID: "tennis-anime", Name: "Tennis Anime"}); err != nil {
		t.Fatal(err)
	}
	item, err := RecordMemoryItem(wd, "tennis-anime", MemoryItem{
		ID:      "mem-tone",
		Content: "Audience likes calmer explanations.",
	})
	if err != nil {
		t.Fatalf("RecordMemoryItem: %v", err)
	}
	if item.Status != "provisional" || item.Weight != 0.5 {
		t.Fatalf("memory defaults: %+v", item)
	}
	dec, err := PromoteMemoryItem(wd, "tennis-anime", MemoryPromotion{
		ItemID:   "mem-tone",
		Decision: "Use calmer explanations for beginners.",
	})
	if err != nil {
		t.Fatalf("PromoteMemoryItem: %v", err)
	}
	if dec.Scope != "memory" || dec.Target != "mem-tone" {
		t.Fatalf("promotion decision mismatch: %+v", dec)
	}
}

func TestPlanWorkflowModes(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "creator", "Creator"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	p, err := PlanWorkflow(wd, "continue tennis")
	if err != nil {
		t.Fatalf("PlanWorkflow new: %v", err)
	}
	if p.Mode != "new_space" || !p.NeedsConfirmation {
		t.Fatalf("new workflow mismatch: %+v", p)
	}
	if _, err := CreateSpace(wd, CreateSpaceOptions{ID: "tennis-anime", Name: "Tennis Anime", Tags: []string{"tennis"}}); err != nil {
		t.Fatal(err)
	}
	p, err = PlanWorkflow(wd, "continue tennis")
	if err != nil {
		t.Fatalf("PlanWorkflow draft: %v", err)
	}
	if p.Mode != "confirm_space" || p.SpaceID != "tennis-anime" {
		t.Fatalf("draft workflow mismatch: %+v", p)
	}
	if _, _, err := ActivateSpace(wd, "tennis-anime", Decision{Decision: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	p, err = PlanWorkflow(wd, "continue tennis")
	if err != nil {
		t.Fatalf("PlanWorkflow active: %v", err)
	}
	if p.Mode != "continue_space" || p.NeedsConfirmation {
		t.Fatalf("active workflow mismatch: %+v", p)
	}
}

func TestCompactionDraftAndRecord(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "creator", "Creator"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	if _, err := CreateSpace(wd, CreateSpaceOptions{ID: "tennis-anime", Name: "Tennis Anime"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ActivateSpace(wd, "tennis-anime", Decision{Decision: "confirmed tennis anime"}); err != nil {
		t.Fatal(err)
	}
	body, err := BuildCompactionDraft(wd, "creator", "tennis-anime")
	if err != nil {
		t.Fatalf("BuildCompactionDraft: %v", err)
	}
	if !strings.Contains(body, "Confirmed Decisions") || !strings.Contains(body, "confirmed tennis anime") {
		t.Fatalf("draft missing decision: %s", body)
	}
	c, err := RecordSpaceCompaction(wd, "tennis-anime", SpaceCompaction{Summary: "Stable anime tennis series."})
	if err != nil {
		t.Fatalf("RecordSpaceCompaction: %v", err)
	}
	if c.Scope != "space" || c.ID == "" {
		t.Fatalf("compaction defaults: %+v", c)
	}
}

func containsString(vals []string, want string) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
}
