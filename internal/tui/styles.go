package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// Palette (Dracula-inspired, works on dark terminals).
var (
	cAccent = lipgloss.Color("#BD93F9")
	cPink   = lipgloss.Color("#FF79C6")
	cGreen  = lipgloss.Color("#50FA7B")
	cYellow = lipgloss.Color("#F1FA8C")
	cOrange = lipgloss.Color("#FFB86C")
	cRed    = lipgloss.Color("#FF5555")
	cCyan   = lipgloss.Color("#8BE9FD")
	cDim    = lipgloss.Color("#6272A4")
	cText   = lipgloss.Color("#F8F8F2")
	cSel    = lipgloss.Color("#44475A")
	cPanel  = lipgloss.Color("#21222C")
	cBar    = lipgloss.Color("#343746")
)

var (
	sTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#282A36")).Background(cAccent).Padding(0, 1)
	sBadge   = lipgloss.NewStyle().Foreground(cText).Background(cBar).Padding(0, 1)
	sDim     = lipgloss.NewStyle().Foreground(cDim)
	sText    = lipgloss.NewStyle().Foreground(cText)
	sOK      = lipgloss.NewStyle().Foreground(cGreen)
	sErr     = lipgloss.NewStyle().Foreground(cRed).Bold(true)
	sWarn    = lipgloss.NewStyle().Foreground(cYellow)
	sKey     = lipgloss.NewStyle().Foreground(cText).Background(cBar).Bold(true).Padding(0, 1)
	sKeyDesc = lipgloss.NewStyle().Foreground(cDim)
	sLabel   = lipgloss.NewStyle().Foreground(cDim).Width(10)
	sValue   = lipgloss.NewStyle().Foreground(cText)
	sHi      = lipgloss.NewStyle().Foreground(cPink).Bold(true)
)

func box(color lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Padding(0, 1)
}

// keyHint renders "key desc" pairs for footers.
func keyHints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, sKey.Render(pairs[i])+sKeyDesc.Render(" "+pairs[i+1]))
	}
	return strings.Join(parts, "  ")
}

func padRight(s string, w int) string {
	if d := w - ansi.StringWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// truncate cuts s (which may contain ANSI) to w cells, adding an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

func plainTrunc(s string, w int) string {
	if runewidth.StringWidth(s) <= w {
		return s
	}
	return runewidth.Truncate(s, w, "…")
}

// overlay draws pop on top of base with its top-left corner at (x, y).
func overlay(base, pop string, x, y int) string {
	bl := strings.Split(base, "\n")
	pl := strings.Split(pop, "\n")
	pw := 0
	for _, l := range pl {
		if w := ansi.StringWidth(l); w > pw {
			pw = w
		}
	}
	for i, l := range pl {
		row := y + i
		if row < 0 || row >= len(bl) {
			continue
		}
		b := bl[row]
		left := ansi.Truncate(b, x, "")
		if d := x - ansi.StringWidth(left); d > 0 {
			left += strings.Repeat(" ", d)
		}
		right := ansi.TruncateLeft(b, x+pw, "")
		bl[row] = left + padRight(l, pw) + right
	}
	return strings.Join(bl, "\n")
}

func overlayCenter(base, pop string, w, h int) string {
	pw := lipgloss.Width(pop)
	ph := lipgloss.Height(pop)
	x := (w - pw) / 2
	y := (h - ph) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return overlay(base, pop, x, y)
}

// hl renders text with the runes listed in pos highlighted, padded/truncated to maxw cells.
func hl(text string, pos map[int]bool, base, hi lipgloss.Style, maxw int) string {
	if runewidth.StringWidth(text) > maxw {
		text = runewidth.Truncate(text, maxw, "…")
	}
	var b strings.Builder
	used := 0
	for i, r := range []rune(text) {
		st := base
		if pos[i] {
			st = hi
		}
		b.WriteString(st.Render(string(r)))
		used += runewidth.RuneWidth(r)
	}
	if used < maxw {
		b.WriteString(base.Render(strings.Repeat(" ", maxw-used)))
	}
	return b.String()
}

func lipglossWidth(s string) int { return ansi.StringWidth(s) }
