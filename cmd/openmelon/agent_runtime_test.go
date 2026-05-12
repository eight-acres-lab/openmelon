package main

import (
	"strings"
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
)

func TestProjectSystemPromptTreatsTypographyAsContinuityContext(t *testing.T) {
	prompt := buildProjectSystemPrompt(&projectx.Project{ID: "p", Name: "Project"}, []string{"generate_image", "bash"})
	for _, want := range []string{
		"Treat typography the same way as background or character continuity",
		"not a local font lookup",
		"Do not use bash to discover local fonts",
		"Generate visual outputs through `generate_image`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, prompt)
		}
	}
}
