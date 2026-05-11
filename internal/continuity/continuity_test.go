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
