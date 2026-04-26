package engine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/pointeight/skillplus-engine/registry"
)

type fakeRunner struct {
	outputs map[string]map[string]any
}

func (r fakeRunner) Run(ctx context.Context, skill registry.Skill, input map[string]any) ([]byte, error) {
	output, ok := r.outputs[skill.Manifest.Slug]
	if !ok {
		return nil, errors.New("missing fake output for " + skill.Manifest.Slug)
	}
	return json.Marshal(output)
}

func TestProcessDispatchesRunsAndAggregates(t *testing.T) {
	skills := []registry.Skill{
		{Manifest: registry.Manifest{Slug: "lang-detect", Version: "0.1.0", Runtime: "python", DispatchHints: registry.DispatchHints{InvokeWhen: []registry.Condition{{ContentType: "text"}}}}},
		{Manifest: registry.Manifest{Slug: "text-stats", Version: "0.1.0", Runtime: "go", DispatchHints: registry.DispatchHints{InvokeWhen: []registry.Condition{{ContentType: "text"}}}}},
	}

	eng := New(skills, fakeRunner{outputs: map[string]map[string]any{
		"lang-detect": {"detected_lang": "zh-Hans", "script": "Han", "confidence": 1.0},
		"text-stats":  {"char_count": 2, "word_count": 2},
	}})

	bface, err := eng.Process(context.Background(), &PostInput{PostID: 1, Text: "你好"})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if bface.Lang != "zh-Hans" {
		t.Fatalf("expected detected lang zh-Hans, got %q", bface.Lang)
	}
	if len(bface.SkillOutputs) != 2 {
		t.Fatalf("expected 2 skill outputs, got %d", len(bface.SkillOutputs))
	}
	if _, ok := bface.SkillOutputs["lang-detect"]; !ok {
		t.Fatal("expected lang-detect output")
	}
	if _, ok := bface.SkillOutputs["text-stats"]; !ok {
		t.Fatal("expected text-stats output")
	}
	if len(bface.SkillsApplied) != 2 {
		t.Fatalf("expected 2 skills applied, got %d", len(bface.SkillsApplied))
	}
	if bface.SkillsApplied[0].Status != "ok" {
		t.Fatalf("expected first skill status ok, got %q", bface.SkillsApplied[0].Status)
	}
}

func TestProcessUsesMixedContentWhenMediaPresent(t *testing.T) {
	skills := []registry.Skill{{Manifest: registry.Manifest{Slug: "mixed-skill", Version: "0.1.0", Runtime: "python", DispatchHints: registry.DispatchHints{InvokeWhen: []registry.Condition{{ContentType: "mixed"}}}}}}
	eng := New(skills, fakeRunner{outputs: map[string]map[string]any{"mixed-skill": {"ok": true}}})

	bface, err := eng.Process(context.Background(), &PostInput{PostID: 1, Text: "caption", MediaURLs: []string{"file://image.png"}})
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	if len(bface.SkillOutputs) != 1 {
		t.Fatalf("expected mixed skill to run, got %d outputs", len(bface.SkillOutputs))
	}
}
