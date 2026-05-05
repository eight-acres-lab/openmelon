package tui

// style.go — lipgloss styles + theme constants used across the TUI.
//
// Color palette (256-color, falls back gracefully on dim terminals):
//   accent       — input border, prompt arrow
//   tool         — ⏺ tool_name lines
//   toolResult   — ⎿ result lines (dim)
//   muted        — status bar, hints
//   warn         — quit-armed indicator
//   err          — error messages

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	colorAccent     = lipgloss.Color("4")  // blue
	colorTool       = lipgloss.Color("6")  // cyan
	colorToolResult = lipgloss.Color("8")  // bright black
	colorMuted      = lipgloss.Color("8")  // bright black (= dim gray)
	colorWarn       = lipgloss.Color("3")  // yellow
	colorErr        = lipgloss.Color("1")  // red
	colorPromptArr  = lipgloss.Color("4")
)

var (
	styleStatusBar = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleHelp = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleToolName = lipgloss.NewStyle().
			Foreground(colorTool).
			Bold(true)

	styleToolArgs = lipgloss.NewStyle().
			Foreground(colorTool)

	styleToolResult = lipgloss.NewStyle().
			Foreground(colorToolResult)

	styleErr = lipgloss.NewStyle().
			Foreground(colorErr).
			Bold(true)

	styleWarn = lipgloss.NewStyle().
			Foreground(colorWarn)

	styleSpinner = lipgloss.NewStyle().
			Foreground(colorAccent)

	styleUserPrompt = lipgloss.NewStyle().
			Foreground(colorPromptArr).
			Bold(true)

	// stylePromptArrow is the dim, simple "› " glyph the textarea
	// uses as its prompt — no bold, no accent color, just a slight
	// brightness so the cursor is the visual anchor.
	stylePromptArrow = lipgloss.NewStyle().
				Foreground(colorMuted)

	stylePaletteActive = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	stylePaletteName = lipgloss.NewStyle()

	stylePaletteHelp = lipgloss.NewStyle().
				Foreground(colorMuted)

	// headerStyle is for selector / wizard titles inside the TUI.
	headerStyle = lipgloss.NewStyle().Bold(true)
)

// spinnerVerbs are content-creation themed. Rotated every ~2 seconds
// while the runtime is working.
var spinnerVerbs = []string{
	"Sketching",
	"Drafting",
	"Composing",
	"Imagining",
	"Storyboarding",
	"Curating",
	"Brewing",
	"Mulling",
	"Framing",
	"Plotting",
	"Refining",
	"Pondering",
}

// spinnerVerb returns the verb for tick i.
func spinnerVerb(i int) string {
	return spinnerVerbs[i%len(spinnerVerbs)]
}
