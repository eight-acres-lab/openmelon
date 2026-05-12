package repl

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/session"
	"golang.org/x/term"
)

var (
	accentColor = lipgloss.Color("6")
	mutedColor  = lipgloss.Color("8")
	whiteColor  = lipgloss.Color("7")

	promptArrowStyle     = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
	statusLineStyle      = lipgloss.NewStyle().Foreground(whiteColor)
	promptHintStyle      = lipgloss.NewStyle().Foreground(whiteColor)
	dividerStyle         = lipgloss.NewStyle().Foreground(mutedColor)
	mutedStyle           = lipgloss.NewStyle().Foreground(mutedColor)
	helpStyle            = lipgloss.NewStyle().Foreground(mutedColor)
	warnStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	errorStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	commandNameStyle     = lipgloss.NewStyle().Foreground(accentColor)
	markdownHeadingStyle = lipgloss.NewStyle().Bold(true)
	markdownBoldStyle    = lipgloss.NewStyle().Bold(true)
	markdownLinkStyle    = lipgloss.NewStyle().Underline(true)
	paletteActiveStyle   = lipgloss.NewStyle().
				Foreground(lipgloss.Color("0")).
				Background(accentColor).
				Bold(true).
				Padding(0, 1)
	logoStyle = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
)

const rightWrapBuffer = 4

type bannerInfo struct {
	Workdir         string
	Project         *projectx.Project
	Session         *session.Session
	ResumedFrom     string
	LLMTag          string
	ImageTag        string
	BashMode        projectx.BashPermissionMode
	ReasoningEffort string
}

func printBanner(w io.Writer, info bannerInfo) {
	fmt.Fprintln(w, logoStyle.Render("OpenMelon"))
	fmt.Fprintln(w, statusLineStyle.Render(composeStatusLine(info)))
	if info.Session != nil {
		fmt.Fprintf(w, "%s\n", helpStyle.Render("session "+filepath.Base(info.Session.Dir)))
	}
	if info.ResumedFrom != "" {
		fmt.Fprintf(w, "%s\n", helpStyle.Render("resumed from: "+info.ResumedFrom))
	}
	fmt.Fprintln(w, helpStyle.Render("Type a request, /help for commands, Ctrl+C twice to quit."))
	fmt.Fprintln(w)
}

func clearScreen(w io.Writer) {
	fmt.Fprint(w, "\033[2J\033[H")
}

func composeStatusLine(info bannerInfo) string {
	parts := []string{"project"}
	if info.Project != nil {
		parts = append(parts, info.Project.ID)
		if info.Project.Name != "" && info.Project.Name != info.Project.ID {
			parts = append(parts, "("+info.Project.Name+")")
		}
	}
	if info.LLMTag != "" {
		parts = append(parts, "model "+info.LLMTag)
	}
	if info.ImageTag != "" {
		parts = append(parts, "image "+info.ImageTag)
	}
	if info.ReasoningEffort != "" {
		parts = append(parts, "reasoning "+info.ReasoningEffort)
	}
	if info.BashMode != "" {
		parts = append(parts, "bash "+string(info.BashMode))
	}
	if info.Workdir != "" {
		parts = append(parts, filepath.Base(info.Workdir))
	}
	return strings.Join(parts, " · ")
}

func composePromptStatusLine(info bannerInfo) string {
	parts := make([]string, 0, 3)
	if info.LLMTag != "" {
		parts = append(parts, shortModelTag(info.LLMTag))
	}
	if info.ReasoningEffort != "" {
		parts = append(parts, info.ReasoningEffort)
	}
	if info.Project != nil && info.Project.ID != "" {
		parts = append(parts, info.Project.ID)
	} else if info.Workdir != "" {
		parts = append(parts, filepath.Base(info.Workdir))
	}
	if len(parts) == 0 {
		return "OpenMelon"
	}
	return strings.Join(parts, " · ")
}

func shortModelTag(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	if i := strings.LastIndex(tag, ":"); i >= 0 && i+1 < len(tag) {
		tag = tag[i+1:]
	}
	if i := strings.LastIndex(tag, "/"); i >= 0 && i+1 < len(tag) {
		tag = tag[i+1:]
	}
	return tag
}

func terminalWidth(w io.Writer) int {
	if f, ok := w.(interface{ Fd() uintptr }); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return 80
}

func wrappedTextWidth(w io.Writer, prefix string) int {
	width := terminalWidth(w) - ansi.StringWidth(prefix) - rightWrapBuffer
	if width < 12 {
		return 12
	}
	return width
}

func wrapDisplayLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	if width < 12 {
		width = 12
	}
	wrapped := ansi.Wrap(line, width, "/._=&?:,")
	if wrapped == "" {
		return []string{""}
	}
	lines := strings.Split(wrapped, "\n")
	indent := leadingPlainIndent(line)
	if indent != "" {
		cont := indent + "  "
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) != "" {
				lines[i] = cont + strings.TrimLeft(lines[i], " ")
			}
		}
	}
	return lines
}

func leadingPlainIndent(s string) string {
	i := 0
	for i < len(s) {
		switch s[i] {
		case ' ', '\t':
			i++
		default:
			return s[:i]
		}
	}
	return s
}
