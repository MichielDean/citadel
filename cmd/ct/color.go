package main

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// isTerminal reports whether stdout is connected to a terminal.
func isTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// col wraps s in an ANSI color code if stdout is a terminal; returns s unchanged otherwise.
func col(code, s string) string {
	if !isTerminal() {
		return s
	}
	return code + s + colorReset
}

// statusCode returns the ANSI color code for a display status string.
func statusCode(dStatus string) string {
	switch dStatus {
	case "flowing":
		return colorGreen
	case "queued":
		return colorYellow
	case "awaiting":
		return colorYellow
	case "stagnant":
		return colorRed
	case "delivered":
		return colorDim
	default:
		return ""
	}
}

// statusIcon returns a colored icon character for the given display status.
func statusIcon(dStatus string) string {
	switch dStatus {
	case "flowing":
		return col(colorGreen, "●")
	case "queued":
		return col(colorYellow, "○")
	case "awaiting":
		return col(colorYellow, "⏸")
	case "stagnant":
		return col(colorRed, "✗")
	case "delivered":
		return col(colorDim, "✓")
	default:
		return " "
	}
}

// statusCell returns a fixed visual-width status cell: icon + space + colored padded text.
// width is the total visual character width (icon + space + text).
func statusCell(dStatus string, width int) string {
	icon := statusIcon(dStatus)
	textWidth := width - 2 // icon(1) + space(1)
	if textWidth < 1 {
		textWidth = 1
	}
	text := padRight(dStatus, textWidth)
	code := statusCode(dStatus)
	if code != "" && isTerminal() {
		text = code + text + colorReset
	}
	return icon + " " + text
}

// termWidth returns the terminal width, defaulting to 80.
func termWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// truncate shortens s to at most max runes, appending "…" if it was longer.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

// skillDesc reads the SKILL.md at localPath and returns a short description excerpt.
// It checks for a `description:` key in YAML frontmatter first, then falls back to the
// first non-blank, non-heading line.
func skillDesc(localPath string) string {
	data, err := os.ReadFile(localPath) // #nosec G304 — path is from the skills package
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	// Try YAML frontmatter.
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for _, line := range lines[1:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == "---" {
				break
			}
			if strings.HasPrefix(line, "description:") {
				desc := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
				return truncate(desc, 50)
			}
		}
	}
	// Fallback: first non-blank, non-heading line.
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		return truncate(trimmed, 50)
	}
	return ""
}
