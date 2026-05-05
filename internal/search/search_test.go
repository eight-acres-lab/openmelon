package search

import (
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/registry"
)

func mustInit(t *testing.T) string {
	t.Helper()
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "ai-talks", "AI Talks"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	return wd
}

func mustAdd(t *testing.T, wd string, opts registry.AddOptions) {
	t.Helper()
	if _, err := registry.Add(wd, opts); err != nil {
		t.Fatalf("registry add %s/%s: %v", opts.Kind, opts.Slug, err)
	}
}

func seed(t *testing.T) string {
	wd := mustInit(t)
	mustAdd(t, wd, registry.AddOptions{
		Kind: registry.KindCharacter, Slug: "lao-wang", Name: "Lao Wang",
		Description: "Mid-50s street vendor with a quiet smile.",
		Tags:        []string{"character", "vendor", "elder"},
	})
	mustAdd(t, wd, registry.AddOptions{
		Kind: registry.KindCharacter, Slug: "xiao-li", Name: "Xiao Li",
		Description: "Young photographer documenting the night market.",
		Tags:        []string{"character", "photographer", "young"},
	})
	mustAdd(t, wd, registry.AddOptions{
		Kind: registry.KindReference, Slug: "kitchen-night", Name: "Kitchen at night",
		Description: "Warm-tone neon kitchen at 22:00, steam from a wok.",
		Tags:        []string{"scene", "kitchen", "night"},
	})
	return wd
}

func TestParseExtractsAllOperators(t *testing.T) {
	q, err := Parse(`vendor tag:character kind:character -photographer "night market"`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(q.Substrings) != 2 || q.Substrings[0] != "vendor" || q.Substrings[1] != "night market" {
		t.Errorf("substrings: %v", q.Substrings)
	}
	if len(q.Tags) != 1 || q.Tags[0] != "character" {
		t.Errorf("tags: %v", q.Tags)
	}
	if len(q.Kinds) != 1 || q.Kinds[0] != registry.KindCharacter {
		t.Errorf("kinds: %v", q.Kinds)
	}
	if len(q.Negatives) != 1 || q.Negatives[0] != "photographer" {
		t.Errorf("negatives: %v", q.Negatives)
	}
}

func TestParseRejectsUnbalancedQuote(t *testing.T) {
	if _, err := Parse(`foo "open`); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRejectsUnknownKind(t *testing.T) {
	if _, err := Parse(`kind:widget`); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestRunSubstringMatchesNameOrDescription(t *testing.T) {
	wd := seed(t)
	q, _ := Parse(`vendor`)
	hits, err := Run(wd, q)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].Item.Slug != "lao-wang" {
		t.Errorf("unexpected hits: %+v", hits)
	}
}

func TestRunTagFilterMustMatchExactly(t *testing.T) {
	wd := seed(t)
	q, _ := Parse(`tag:photographer`)
	hits, err := Run(wd, q)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].Item.Slug != "xiao-li" {
		t.Errorf("hits: %+v", hits)
	}
}

func TestRunKindRestriction(t *testing.T) {
	wd := seed(t)
	q, _ := Parse(`kind:reference`)
	hits, err := Run(wd, q)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].Item.Kind != registry.KindReference {
		t.Errorf("hits: %+v", hits)
	}
}

func TestRunNegativeSubstringExcludesMatches(t *testing.T) {
	wd := seed(t)
	q, _ := Parse(`kind:character -photographer`)
	hits, err := Run(wd, q)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].Item.Slug != "lao-wang" {
		t.Errorf("hits: %+v", hits)
	}
}

func TestRunCombinationOfTagAndSubstring(t *testing.T) {
	wd := seed(t)
	q, _ := Parse(`night tag:scene`)
	hits, err := Run(wd, q)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 || hits[0].Item.Slug != "kitchen-night" {
		t.Errorf("hits: %+v", hits)
	}
}

func TestRunCaseInsensitiveSubstring(t *testing.T) {
	wd := seed(t)
	q, _ := Parse(`VENDOR`)
	hits, err := Run(wd, q)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("expected 1 hit, got %d", len(hits))
	}
}

func TestRunOrdersByScoreThenSlug(t *testing.T) {
	wd := mustInit(t)
	mustAdd(t, wd, registry.AddOptions{
		Kind: registry.KindCharacter, Slug: "a1", Name: "A",
		Description: "vendor", Tags: []string{"vendor"},
	})
	mustAdd(t, wd, registry.AddOptions{
		Kind: registry.KindCharacter, Slug: "b1", Name: "B vendor vendor",
		Description: "vendor vendor", Tags: []string{"vendor"},
	})
	q, _ := Parse(`vendor`)
	hits, err := Run(wd, q)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	// b1 has more substring hits.
	if hits[0].Item.Slug != "b1" {
		t.Errorf("expected b1 first, got %s", hits[0].Item.Slug)
	}
}
