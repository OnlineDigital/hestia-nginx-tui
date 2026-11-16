package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/onlinedigital/hestia-nginx-tui/internal/fuzzy"
)

// popup is a modal drawn on top of the current screen.
type popup interface {
	Update(a *App, msg tea.Msg) tea.Cmd
	View(w, h int) string
}

// ---------------------------------------------------------------------------
// picker: a list with optional fuzzy filter. Used as menu, template chooser,
// upstream chooser...

type pickItem struct {
	Label  string
	Detail string
	Tag    string // short right-aligned marker, e.g. "current"
	Value  any
}

type pickerPopup struct {
	title   string
	sub     string
	items   []pickItem
	filter  bool
	input   textinput.Model
	view    []fuzzy.Ranked
	cursor  int
	off     int
	footer  string
	onPick  func(a *App, it pickItem) tea.Cmd
	onKey   func(a *App, p *pickerPopup, k tea.KeyMsg) (tea.Cmd, bool)
	width   int
	maxRows int
}

func newPicker(title string, items []pickItem, filter bool, onPick func(a *App, it pickItem) tea.Cmd) *pickerPopup {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(cPink)
	ti.Placeholder = "type to filter…"
	ti.Focus()
	p := &pickerPopup{title: title, items: items, filter: filter, input: ti, onPick: onPick, width: 64, maxRows: 12}
	p.refilter()
	return p
}

func (p *pickerPopup) refilter() {
	q := ""
	if p.filter {
		q = p.input.Value()
	}
	p.view = fuzzy.Filter(q, len(p.items), func(i int) string { return p.items[i].Label + " " + p.items[i].Detail })
	if p.cursor >= len(p.view) {
		p.cursor = max(0, len(p.view)-1)
	}
	p.fixScroll()
}

func (p *pickerPopup) fixScroll() {
	if p.cursor < p.off {
		p.off = p.cursor
	}
	if p.cursor >= p.off+p.maxRows {
		p.off = p.cursor - p.maxRows + 1
	}
}

func (p *pickerPopup) Update(a *App, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.MouseMsg:
		switch m.Button {
		case tea.MouseButtonWheelUp:
			p.cursor = max(0, p.cursor-1)
		case tea.MouseButtonWheelDown:
			p.cursor = min(len(p.view)-1, p.cursor+1)
		}
		p.fixScroll()
	case tea.KeyMsg:
		if p.onKey != nil {
			if cmd, handled := p.onKey(a, p, m); handled {
				return cmd
			}
		}
		switch m.String() {
		case "esc", "ctrl+c":
			a.popup = nil
			return nil
		case "up", "ctrl+p", "shift+tab":
			if p.cursor > 0 {
				p.cursor--
			}
		case "down", "ctrl+n", "tab":
			if p.cursor < len(p.view)-1 {
				p.cursor++
			}
		case "pgup":
			p.cursor = max(0, p.cursor-p.maxRows)
		case "pgdown":
			p.cursor = min(len(p.view)-1, p.cursor+p.maxRows)
		case "home":
			p.cursor = 0
		case "end":
			p.cursor = max(0, len(p.view)-1)
		case "enter":
			if len(p.view) == 0 {
				return nil
			}
			it := p.items[p.view[p.cursor].Index]
			a.popup = nil
			if p.onPick != nil {
				return p.onPick(a, it)
			}
			return nil
		default:
			if p.filter {
				var cmd tea.Cmd
				p.input, cmd = p.input.Update(m)
				p.cursor, p.off = 0, 0
				p.refilter()
				return cmd
			}
		}
		p.fixScroll()
	}
	return nil
}

func (p *pickerPopup) View(w, h int) string {
	iw := max(24, min(p.width, w-8))
	var rows []string
	rows = append(rows, lipgloss.NewStyle().Bold(true).Foreground(cAccent).Render(p.title))
	if p.sub != "" {
		rows = append(rows, sDim.Render(truncate(p.sub, iw)))
	}
	if p.filter {
		p.input.Width = iw - 4
		rows = append(rows, "", p.input.View())
	}
	rows = append(rows, "")
	end := min(len(p.view), p.off+p.maxRows)
	if len(p.view) == 0 {
		rows = append(rows, sDim.Render("  no match"))
	}
	for i := p.off; i < end; i++ {
		rk := p.view[i]
		it := p.items[rk.Index]
		sel := i == p.cursor
		tagW := lipgloss.Width(it.Tag)
		labW := iw - 4 - tagW
		if tagW > 0 {
			labW--
		}
		base, hi := sText, sHi
		if sel {
			base = lipgloss.NewStyle().Foreground(cText).Background(cSel).Bold(true)
			hi = lipgloss.NewStyle().Foreground(cPink).Background(cSel).Bold(true)
		}
		pos := map[int]bool{}
		for _, ps := range rk.Pos {
			if ps < len([]rune(it.Label)) {
				pos[ps] = true
			}
		}
		marker := "  "
		if sel {
			marker = lipgloss.NewStyle().Foreground(cPink).Background(cSel).Render("▌ ")
		}
		label := hl(it.Label, pos, base, hi, labW)
		tag := ""
		if tagW > 0 {
			tag = base.Render(" ") + lipgloss.NewStyle().Foreground(cGreen).Background(map[bool]lipgloss.Color{true: cSel, false: ""}[sel]).Render(it.Tag)
		}
		rows = append(rows, marker+label+tag)
		if sel && it.Detail != "" {
			// detail is shown under the selected row only, to keep lists compact
			rows = append(rows, "    "+sDim.Render(truncate(it.Detail, iw-4)))
		}
	}
	if len(p.view) > p.maxRows {
		rows = append(rows, sDim.Render(fmt.Sprintf("  %d/%d", p.cursor+1, len(p.view))))
	}
	foot := p.footer
	if foot == "" {
		foot = keyHints("↑↓", "move", "Enter", "select", "Esc", "close")
	}
	rows = append(rows, "", foot)
	return box(cAccent).Width(iw + 2).Render(strings.Join(rows, "\n"))
}

// ---------------------------------------------------------------------------
// confirm

type confirmPopup struct {
	title, body string
	yes         string
	danger      bool
	onYes       func(a *App) tea.Cmd
}

func (c *confirmPopup) Update(a *App, msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "y", "Y", "enter":
			a.popup = nil
			return c.onYes(a)
		case "n", "N", "esc", "ctrl+c":
			a.popup = nil
		}
	}
	return nil
}

func (c *confirmPopup) View(w, h int) string {
	col := cYellow
	if c.danger {
		col = cRed
	}
	yes := c.yes
	if yes == "" {
		yes = "yes"
	}
	body := lipgloss.NewStyle().Width(min(70, w-10)).Render(c.body)
	return box(col).Render(
		lipgloss.NewStyle().Bold(true).Foreground(col).Render(c.title) + "\n\n" + body + "\n\n" +
			keyHints("Y/Enter", yes, "N/Esc", "cancel"))
}

// ---------------------------------------------------------------------------
// result: transcript of commands, scrollable

type resultPopup struct {
	title   string
	body    string
	ok      bool
	top     int
	onClose func(a *App) tea.Cmd
}

func (r *resultPopup) lines(w int) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(r.body, "\n"), "\n") {
		st := sText
		switch {
		case strings.HasPrefix(l, "✓"):
			st = sOK
		case strings.HasPrefix(l, "✗"):
			st = sErr
		case strings.HasPrefix(l, "$ "):
			st = lipgloss.NewStyle().Foreground(cCyan)
		case strings.Contains(l, "[emerg]") || strings.Contains(l, "[error]"):
			st = sErr
		}
		for len([]rune(l)) > w {
			out = append(out, st.Render(string([]rune(l)[:w])))
			l = string([]rune(l)[w:])
		}
		out = append(out, st.Render(l))
	}
	return out
}

func (r *resultPopup) Update(a *App, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		if m, ok := msg.(tea.MouseMsg); ok {
			if m.Button == tea.MouseButtonWheelUp {
				r.top = max(0, r.top-2)
			} else if m.Button == tea.MouseButtonWheelDown {
				r.top += 2
			}
		}
		return nil
	}
	switch k.String() {
	case "up", "ctrl+p":
		r.top = max(0, r.top-1)
	case "down", "ctrl+n":
		r.top++
	case "pgup":
		r.top = max(0, r.top-10)
	case "pgdown":
		r.top += 10
	default:
		a.popup = nil
		if r.onClose != nil {
			return r.onClose(a)
		}
	}
	return nil
}

func (r *resultPopup) View(w, h int) string {
	iw := min(100, w-8)
	maxRows := max(4, h-12)
	ls := r.lines(iw)
	if r.top > len(ls)-maxRows {
		r.top = max(0, len(ls)-maxRows)
	}
	end := min(len(ls), r.top+maxRows)
	col, head := cGreen, "✓ "+r.title
	if !r.ok {
		col, head = cRed, "✗ "+r.title
	}
	body := strings.Join(ls[r.top:end], "\n")
	more := ""
	if len(ls) > maxRows {
		more = sDim.Render(fmt.Sprintf("  lines %d-%d of %d (↑↓ scroll)", r.top+1, end, len(ls)))
	}
	return box(col).Width(iw + 2).Render(
		lipgloss.NewStyle().Bold(true).Foreground(col).Render(head) + "\n\n" + body + "\n" + more + "\n" +
			keyHints("any key", "close"))
}

// ---------------------------------------------------------------------------
// busy

type busyPopup struct {
	label string
	sp    spinner.Model
}

func newBusy(label string) *busyPopup {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = lipgloss.NewStyle().Foreground(cPink)
	return &busyPopup{label: label, sp: sp}
}

func (b *busyPopup) Update(a *App, msg tea.Msg) tea.Cmd {
	if t, ok := msg.(spinner.TickMsg); ok {
		var cmd tea.Cmd
		b.sp, cmd = b.sp.Update(t)
		return cmd
	}
	return nil
}

func (b *busyPopup) View(w, h int) string {
	return box(cAccent).Padding(1, 3).Render(b.sp.View() + " " + b.label)
}
