package dispatcher

import (
	"testing"

	"github.com/pointeight/skillplus-engine/registry"
)

func TestMatchSelectsTextSkill(t *testing.T) {
	minLength := 3
	skills := []registry.Skill{
		{
			Manifest: registry.Manifest{
				Slug: "lang-detect",
				DispatchHints: registry.DispatchHints{
					InvokeWhen: []registry.Condition{{ContentType: "text", MinLength: &minLength}},
				},
			},
		},
	}

	matched := New(skills).Match(Input{ContentType: "text", TextLength: 5})
	if len(matched) != 1 || matched[0].Manifest.Slug != "lang-detect" {
		t.Fatalf("expected lang-detect to match, got %#v", matched)
	}
}

func TestMatchRejectsBelowMinLength(t *testing.T) {
	minLength := 10
	skills := []registry.Skill{{Manifest: registry.Manifest{Slug: "long-text", DispatchHints: registry.DispatchHints{InvokeWhen: []registry.Condition{{ContentType: "text", MinLength: &minLength}}}}}}

	matched := New(skills).Match(Input{ContentType: "text", TextLength: 9})
	if len(matched) != 0 {
		t.Fatalf("expected no matches, got %#v", matched)
	}
}

func TestMatchDoNotInvokeWins(t *testing.T) {
	skills := []registry.Skill{{Manifest: registry.Manifest{Slug: "text-only", DispatchHints: registry.DispatchHints{
		InvokeWhen:      []registry.Condition{{ContentType: "mixed"}},
		DoNotInvokeWhen: []registry.Condition{{ContentType: "mixed"}},
	}}}}

	matched := New(skills).Match(Input{ContentType: "mixed", TextLength: 20, HasMedia: true})
	if len(matched) != 0 {
		t.Fatalf("expected do_not_invoke_when to exclude skill, got %#v", matched)
	}
}

func TestMatchFiltersByLanguage(t *testing.T) {
	skills := []registry.Skill{{Manifest: registry.Manifest{Slug: "english-only", DispatchHints: registry.DispatchHints{InvokeWhen: []registry.Condition{{ContentType: "text", Language: "en"}}}}}}

	matched := New(skills).Match(Input{ContentType: "text", TextLength: 20, Lang: "zh-Hans"})
	if len(matched) != 0 {
		t.Fatalf("expected language mismatch to exclude skill, got %#v", matched)
	}
}
