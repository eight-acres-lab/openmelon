// Package search is openmelon's grep-style search across all on-disk
// content libraries (characters, references, materials).
//
// Design intent: deliberately not vector. The corpus is small (hundreds
// of items per project, not millions), the queries are operator-style
// (tags + substring), and a fresh-pull grep over the registry is fast
// enough that adding an index server / embedding model isn't justified.
//
// The query language is intentionally tiny:
//
//	bare token         substring match in name OR description (case-insensitive)
//	tag:foo            require tag "foo" exactly
//	kind:character     restrict to one Kind
//	-token             negative substring match
//
// Multiple terms are AND'd. Order doesn't matter. Tokens with internal
// whitespace must be quoted with double quotes.
//
// Result ranking is "items with the most positive substring hits in
// description first, then alphabetic by slug" — no TF/IDF, no vectors.
package search

import (
	"fmt"
	"sort"
	"strings"

	"github.com/eight-acres-lab/openmelon/internal/registry"
)

// Hit is one search result.
type Hit struct {
	Item  *registry.Item
	Score int // higher = better; ties broken by slug
}

// Query holds a parsed query.
type Query struct {
	Substrings []string        // positive substring matches (case-insensitive)
	Negatives  []string        // negative substring matches
	Tags       []string        // required tags (exact match)
	Kinds      []registry.Kind // restrict to these kinds; empty = all
}

// Parse turns a raw query string into a Query.
//
// Tokens are space-separated; double-quoted spans count as one token.
// Recognized prefixes:
//
//	tag:<value>      require tag <value>
//	kind:<value>     restrict to <value>; legal: character|reference|material
//	-<token>         exclude items containing <token>
//	<token>          require items containing <token> in name or description
func Parse(raw string) (*Query, error) {
	tokens, err := tokenize(raw)
	if err != nil {
		return nil, err
	}
	q := &Query{}
	for _, t := range tokens {
		if t == "" {
			continue
		}
		switch {
		case strings.HasPrefix(t, "tag:"):
			val := strings.ToLower(strings.TrimPrefix(t, "tag:"))
			if val != "" {
				q.Tags = append(q.Tags, val)
			}
		case strings.HasPrefix(t, "kind:"):
			val := strings.ToLower(strings.TrimPrefix(t, "kind:"))
			switch val {
			case "character", "characters":
				q.Kinds = append(q.Kinds, registry.KindCharacter)
			case "reference", "references", "ref", "refs":
				q.Kinds = append(q.Kinds, registry.KindReference)
			case "material", "materials":
				q.Kinds = append(q.Kinds, registry.KindMaterial)
			default:
				return nil, fmt.Errorf("search: unknown kind: %q", val)
			}
		case strings.HasPrefix(t, "-") && len(t) > 1:
			q.Negatives = append(q.Negatives, strings.ToLower(t[1:]))
		default:
			q.Substrings = append(q.Substrings, strings.ToLower(t))
		}
	}
	return q, nil
}

// Run executes a parsed query against a project's registry.
func Run(workdir string, q *Query) ([]Hit, error) {
	kinds := q.Kinds
	if len(kinds) == 0 {
		kinds = []registry.Kind{registry.KindCharacter, registry.KindReference, registry.KindMaterial}
	}
	var hits []Hit
	for _, kind := range kinds {
		items, err := registry.List(workdir, kind)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if hit, ok := score(item, q); ok {
				hits = append(hits, hit)
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Item.Kind != hits[j].Item.Kind {
			return hits[i].Item.Kind < hits[j].Item.Kind
		}
		return hits[i].Item.Slug < hits[j].Item.Slug
	})
	return hits, nil
}

// score evaluates one item against the query. Returns (Hit, true) if the
// item matches; (Hit{}, false) otherwise.
func score(item *registry.Item, q *Query) (Hit, bool) {
	hay := strings.ToLower(item.Name + "\n" + item.Description)
	for _, neg := range q.Negatives {
		if strings.Contains(hay, neg) {
			return Hit{}, false
		}
	}
	for _, want := range q.Tags {
		ok := false
		for _, have := range item.Tags {
			if strings.EqualFold(have, want) {
				ok = true
				break
			}
		}
		if !ok {
			return Hit{}, false
		}
	}
	score := 0
	for _, sub := range q.Substrings {
		// A description hit is worth more than a name hit because
		// descriptions are longer and more specific. Tag hits get a
		// small bonus too — if you typed "vendor" and a tag literally
		// is "vendor", that's a strong match.
		hits := strings.Count(hay, sub)
		if hits == 0 {
			return Hit{}, false
		}
		score += hits
		for _, t := range item.Tags {
			if strings.Contains(strings.ToLower(t), sub) {
				score += 2
			}
		}
	}
	if len(q.Substrings) == 0 && len(q.Tags) == 0 {
		// Empty query (after stripping kind:/negatives) matches all
		// items in the chosen kinds, with score 0.
		score = 0
	}
	return Hit{Item: item, Score: score}, true
}

// tokenize splits raw on whitespace, honoring "double-quoted" spans.
func tokenize(raw string) ([]string, error) {
	var tokens []string
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && (c == ' ' || c == '\t' || c == '\n') {
			if b.Len() > 0 {
				tokens = append(tokens, b.String())
				b.Reset()
			}
			continue
		}
		b.WriteByte(c)
	}
	if inQuote {
		return nil, fmt.Errorf("search: unbalanced quote in query")
	}
	if b.Len() > 0 {
		tokens = append(tokens, b.String())
	}
	return tokens, nil
}
