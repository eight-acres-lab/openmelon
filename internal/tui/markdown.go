package tui

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

var (
	orderedListRe = regexp.MustCompile(`^(\s*)(\d+)[.)]\s+(.*)$`)
	linkRe        = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

type MarkdownRenderer interface {
	Render(markdown string, width int) string
}

type terminalMarkdownRenderer struct{}

func newMarkdownRenderer() MarkdownRenderer {
	return terminalMarkdownRenderer{}
}

func (terminalMarkdownRenderer) Render(src string, width int) string {
	return renderMarkdownWithWidth(src, width)
}

// renderMarkdown renders the small Markdown subset the assistant most
// often emits. It is intentionally lightweight: the TUI needs readable
// terminal output without adding another parsing dependency yet.
func renderMarkdown(src string) string {
	return renderMarkdownWithWidth(src, 0)
}

func renderMarkdownPlain(src string) string {
	return ansiPlain(renderMarkdownWithWidth(src, 0))
}

func ansiPlain(s string) string {
	return ansi.Strip(s)
}

func renderMarkdownWithWidth(src string, width int) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")

	var b strings.Builder
	inFence := false
	fenceLang := ""

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				inFence = false
				fenceLang = ""
			} else {
				inFence = true
				fenceLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				if fenceLang != "" {
					b.WriteString(styleMarkdownCodeLang.Render("  "+fenceLang) + "\n")
				}
			}
			continue
		}

		if inFence {
			b.WriteString(styleMarkdownCodeBlock.Render("  " + line))
			if i < len(lines)-1 {
				b.WriteByte('\n')
			}
			continue
		}

		if trimmed == "" {
			b.WriteByte('\n')
			continue
		}

		switch {
		case isHeading(trimmed):
			level, text := splitHeading(trimmed)
			if level <= 2 {
				b.WriteString(styleMarkdownHeading.Render(renderInline(text)))
			} else {
				b.WriteString(styleMarkdownSubheading.Render(renderInline(text)))
			}
		case isHorizontalRule(trimmed):
			b.WriteString(styleHelp.Render(ruleLine(width)))
		case isTableDelimiter(trimmed):
			continue
		case isTableRow(trimmed):
			b.WriteString(renderTableRow(trimmed))
		case strings.HasPrefix(trimmed, ">"):
			text := strings.TrimSpace(strings.TrimLeft(trimmed, ">"))
			b.WriteString(styleMarkdownQuote.Render("> " + renderInline(text)))
		case isUnorderedList(trimmed):
			text := strings.TrimSpace(trimmed[1:])
			b.WriteString("  " + styleMarkdownBullet.Render("- ") + renderInline(text))
		case orderedListRe.MatchString(line):
			m := orderedListRe.FindStringSubmatch(line)
			text := strings.TrimSpace(m[3])
			b.WriteString("  " + styleMarkdownBullet.Render(m[2]+". ") + renderInline(text))
		default:
			b.WriteString(renderInline(line))
		}

		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func ruleLine(width int) string {
	if width <= 0 || width > 80 {
		width = 40
	}
	if width < 8 {
		width = 8
	}
	return strings.Repeat("-", width)
}

func isHeading(line string) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	return n > 0 && n <= 6 && n < len(line) && unicode.IsSpace(rune(line[n]))
}

func splitHeading(line string) (int, string) {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	return n, strings.TrimSpace(line[n:])
}

func isHorizontalRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	for _, r := range line {
		if r != '-' && r != '*' && r != '_' {
			return false
		}
	}
	return true
}

func isUnorderedList(line string) bool {
	if len(line) < 2 {
		return false
	}
	switch line[0] {
	case '-', '*', '+':
		return unicode.IsSpace(rune(line[1]))
	default:
		return false
	}
}

func isTableRow(line string) bool {
	return strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|") && strings.Count(line, "|") >= 2
}

func isTableDelimiter(line string) bool {
	if !isTableRow(line) {
		return false
	}
	for _, cell := range strings.Split(strings.Trim(line, "|"), "|") {
		cell = strings.TrimSpace(cell)
		if cell == "" {
			return false
		}
		cell = strings.Trim(cell, ":")
		if len(cell) < 3 {
			return false
		}
		for _, r := range cell {
			if r != '-' {
				return false
			}
		}
	}
	return true
}

func renderTableRow(line string) string {
	parts := strings.Split(strings.Trim(line, "|"), "|")
	for i := range parts {
		parts[i] = renderInline(strings.TrimSpace(parts[i]))
	}
	return strings.Join(parts, styleHelp.Render("  |  "))
}

func renderInline(s string) string {
	s = renderLinks(s)
	s = renderDelimited(s, "`", func(v string) string {
		return styleMarkdownInlineCode.Render(v)
	})
	s = renderDelimited(s, "**", func(v string) string {
		return styleMarkdownBold.Render(v)
	})
	s = renderDelimited(s, "__", func(v string) string {
		return styleMarkdownBold.Render(v)
	})
	return s
}

func renderLinks(s string) string {
	return linkRe.ReplaceAllStringFunc(s, func(match string) string {
		parts := linkRe.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		label := strings.TrimSpace(parts[1])
		url := strings.TrimSpace(parts[2])
		if label == "" || url == "" {
			return match
		}
		return styleMarkdownLink.Render(label) + styleHelp.Render(" ("+url+")")
	})
}

func renderDelimited(s, delim string, render func(string) string) string {
	if delim == "" {
		return s
	}
	var b strings.Builder
	for {
		start := strings.Index(s, delim)
		if start < 0 {
			b.WriteString(s)
			break
		}
		end := strings.Index(s[start+len(delim):], delim)
		if end < 0 {
			b.WriteString(s)
			break
		}
		end += start + len(delim)
		inner := s[start+len(delim) : end]
		b.WriteString(s[:start])
		if strings.TrimSpace(inner) == "" {
			b.WriteString(delim + inner + delim)
		} else {
			b.WriteString(render(inner))
		}
		s = s[end+len(delim):]
	}
	return b.String()
}

func markdownLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
