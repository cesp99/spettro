package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	reCodeSpan = regexp.MustCompile("`([^`]+)`")
	reBold     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalic1  = regexp.MustCompile(`\*([^*]+)\*`)
	reItalic2  = regexp.MustCompile(`_([^_]+)_`)
	reLink     = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

func renderMarkdown(content string, width int) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}

	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")

	out := make([]string, 0, len(lines))
	inCode := false
	var codeLines []string

	for _, line := range lines {
		trim := strings.TrimSpace(line)

		if strings.HasPrefix(trim, "```") {
			if inCode {
				out = append(out, renderCodeBlock(strings.Join(codeLines, "\n"), width))
				codeLines = nil
				inCode = false
			} else {
				inCode = true
				codeLines = nil
			}
			continue
		}

		if inCode {
			codeLines = append(codeLines, line)
			continue
		}

		if trim == "" {
			out = append(out, "")
			continue
		}

		if level, title, ok := parseHeading(trim); ok {
			titleStyle := lipgloss.NewStyle().Bold(true).Foreground(colorText)
			if level == 1 {
				titleStyle = titleStyle.Underline(true)
			}
			out = append(out, titleStyle.Render(renderInlineMarkdown(title)))
			continue
		}

		if bullet, item, ok := parseListItem(line); ok {
			out = append(out, styleText.Render("  "+bullet+" "+renderInlineMarkdown(item)))
			continue
		}

		if quote, ok := parseQuote(line); ok {
			q := lipgloss.NewStyle().Foreground(colorMuted).Italic(true).Render(renderInlineMarkdown(quote))
			out = append(out, styleMuted.Render("│ ")+q)
			continue
		}

		if isHorizontalRule(trim) {
			ruleW := width
			if ruleW < 8 {
				ruleW = 24
			}
			out = append(out, styleDim.Render(strings.Repeat("─", ruleW-2)))
			continue
		}

		out = append(out, styleText.Render(renderInlineMarkdown(trim)))
	}

	if inCode {
		out = append(out, renderCodeBlock(strings.Join(codeLines, "\n"), width))
	}

	return strings.Join(out, "\n")
}

func renderInlineMarkdown(s string) string {
	if s == "" {
		return s
	}

	codePieces := map[string]string{}
	codeN := 0
	s = reCodeSpan.ReplaceAllStringFunc(s, func(m string) string {
		tok := fmt.Sprintf("\x00CODE%d\x00", codeN)
		codeN++
		parts := reCodeSpan.FindStringSubmatch(m)
		if len(parts) < 2 {
			codePieces[tok] = m
			return tok
		}
		codePieces[tok] = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D1D5DB")).
			Background(lipgloss.Color("#1F2937")).
			Render(" " + parts[1] + " ")
		return tok
	})

	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		parts := reBold.FindStringSubmatch(m)
		if len(parts) < 2 {
			return m
		}
		return styleBold.Render(parts[1])
	})

	s = reItalic1.ReplaceAllStringFunc(s, func(m string) string {
		parts := reItalic1.FindStringSubmatch(m)
		if len(parts) < 2 {
			return m
		}
		return lipgloss.NewStyle().Italic(true).Render(parts[1])
	})

	s = reItalic2.ReplaceAllStringFunc(s, func(m string) string {
		parts := reItalic2.FindStringSubmatch(m)
		if len(parts) < 2 {
			return m
		}
		return lipgloss.NewStyle().Italic(true).Render(parts[1])
	})

	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		parts := reLink.FindStringSubmatch(m)
		if len(parts) < 3 {
			return m
		}
		return styleText.Render(parts[1]) + " " + styleMuted.Render("("+parts[2]+")")
	})

	for tok, rendered := range codePieces {
		s = strings.ReplaceAll(s, tok, rendered)
	}

	return s
}

func renderCodeBlock(code string, width int) string {
	if strings.TrimSpace(code) == "" {
		return ""
	}
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#E5E7EB")).
		Background(lipgloss.Color("#111827")).
		Padding(0, 1)
	if width > 12 {
		style = style.MaxWidth(width)
	}
	return style.Render(code)
}

func parseHeading(line string) (int, string, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, "", false
	}
	if len(line) <= level || line[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(line[level+1:]), true
}

func parseListItem(line string) (string, string, bool) {
	trim := strings.TrimLeft(line, " \t")
	if trim == "" {
		return "", "", false
	}

	if len(trim) >= 2 {
		switch trim[0] {
		case '-', '*', '+':
			if trim[1] == ' ' {
				return "•", strings.TrimSpace(trim[2:]), true
			}
		}
	}

	i := 0
	for i < len(trim) && trim[i] >= '0' && trim[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(trim) && trim[i] == '.' && trim[i+1] == ' ' {
		return trim[:i] + ".", strings.TrimSpace(trim[i+2:]), true
	}

	return "", "", false
}

func parseQuote(line string) (string, bool) {
	trim := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trim, ">") {
		return "", false
	}
	q := strings.TrimSpace(strings.TrimPrefix(trim, ">"))
	return q, true
}

func isHorizontalRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	clean := strings.ReplaceAll(strings.ReplaceAll(line, " ", ""), "\t", "")
	if len(clean) < 3 {
		return false
	}
	for _, ch := range clean {
		if ch != '-' && ch != '*' && ch != '_' {
			return false
		}
	}
	return true
}

func prefixBlockWithBullet(bullet, block string) string {
	if strings.TrimSpace(block) == "" {
		return ""
	}
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		if i == 0 {
			lines[i] = bullet + " " + line
			continue
		}
		if strings.TrimSpace(line) == "" {
			lines[i] = ""
			continue
		}
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n")
}
