package tui

import (
	"strings"
	"testing"
)

func TestRenderMarkdownBasicStructure(t *testing.T) {
	got := renderMarkdown(`# Plan

This is **important** and ` + "`stable`" + `.

- First
- Second

> Confirm before canon.

` + "```go" + `
fmt.Println("ok")
` + "```" + `
`)

	for _, want := range []string{
		"Plan",
		"important",
		"stable",
		"- First",
		"- Second",
		"> Confirm before canon.",
		"fmt.Println",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered markdown missing %q:\n%s", want, got)
		}
	}
	for _, raw := range []string{"# Plan", "**important**", "`stable`", "```"} {
		if strings.Contains(got, raw) {
			t.Fatalf("rendered markdown leaked raw marker %q:\n%s", raw, got)
		}
	}
}

func TestStreamingMarkdownRendersIntoTranscript(t *testing.T) {
	m := newModel(modelInit{})
	m.appendStreamingText("# Title\n")
	m.appendStreamingText("\n- One")
	m.flushStreamingText()

	got := m.transcript.String()
	if !strings.Contains(got, "Title") || !strings.Contains(got, "- One") {
		t.Fatalf("streamed transcript missing rendered markdown:\n%s", got)
	}
	if strings.Contains(got, "# Title") {
		t.Fatalf("streamed transcript leaked raw heading:\n%s", got)
	}
}
