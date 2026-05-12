package repl

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	replOrderedListRe = regexp.MustCompile(`^(\s*)(\d+)[.)]\s+(.*)$`)
	replLinkRe        = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

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
					b.WriteString(mutedStyle.Render("  " + fenceLang))
					b.WriteByte('\n')
				}
			}
			continue
		}

		if inFence {
			b.WriteString(replToolSummaryStyle.Render("  " + line))
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
		case isMarkdownHeading(trimmed):
			_, text := splitMarkdownHeading(trimmed)
			b.WriteString(markdownHeadingStyle.Render(renderMarkdownInline(text)))
		case isMarkdownRule(trimmed):
			b.WriteString(dividerStyle.Render(markdownRuleLine(width)))
		case isMarkdownTableDelimiter(trimmed):
			continue
		case isMarkdownTableRow(trimmed):
			b.WriteString(renderMarkdownTableRow(trimmed))
		case strings.HasPrefix(trimmed, ">"):
			text := strings.TrimSpace(strings.TrimLeft(trimmed, ">"))
			b.WriteString(mutedStyle.Render("> " + renderMarkdownInline(text)))
		case isMarkdownUnorderedList(trimmed):
			text := strings.TrimSpace(trimmed[1:])
			b.WriteString("  " + mutedStyle.Render("- ") + renderMarkdownInline(text))
		case replOrderedListRe.MatchString(line):
			m := replOrderedListRe.FindStringSubmatch(line)
			text := strings.TrimSpace(m[3])
			b.WriteString("  " + mutedStyle.Render(m[2]+". ") + renderMarkdownInline(text))
		default:
			b.WriteString(renderMarkdownInline(line))
		}

		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func markdownRuleLine(width int) string {
	if width <= 0 || width > 80 {
		width = 40
	}
	if width < 8 {
		width = 8
	}
	return strings.Repeat("─", width)
}

func isMarkdownHeading(line string) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	return n > 0 && n <= 6 && n < len(line) && unicode.IsSpace(rune(line[n]))
}

func splitMarkdownHeading(line string) (int, string) {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	return n, strings.TrimSpace(line[n:])
}

func isMarkdownRule(line string) bool {
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

func isMarkdownUnorderedList(line string) bool {
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

func isMarkdownTableRow(line string) bool {
	return strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|") && strings.Count(line, "|") >= 2
}

func isMarkdownTableDelimiter(line string) bool {
	if !isMarkdownTableRow(line) {
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

func renderMarkdownTableRow(line string) string {
	parts := strings.Split(strings.Trim(line, "|"), "|")
	for i := range parts {
		parts[i] = renderMarkdownInline(strings.TrimSpace(parts[i]))
	}
	return strings.Join(parts, mutedStyle.Render("  |  "))
}

func renderMarkdownInline(s string) string {
	s = renderMarkdownLinks(s)
	s = renderMarkdownDelimited(s, "`", func(v string) string {
		return replToolSummaryStyle.Render(v)
	})
	s = renderMarkdownDelimited(s, "**", func(v string) string {
		return markdownBoldStyle.Render(v)
	})
	s = renderMarkdownDelimited(s, "__", func(v string) string {
		return markdownBoldStyle.Render(v)
	})
	return s
}

func renderMarkdownLinks(s string) string {
	return replLinkRe.ReplaceAllStringFunc(s, func(match string) string {
		parts := replLinkRe.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		label := strings.TrimSpace(parts[1])
		url := strings.TrimSpace(parts[2])
		if label == "" || url == "" {
			return match
		}
		return markdownLinkStyle.Render(label) + mutedStyle.Render(" ("+url+")")
	})
}

func renderMarkdownDelimited(s, delim string, render func(string) string) string {
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
