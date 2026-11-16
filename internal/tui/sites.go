package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/onlinedigital/hestia-nginx-tui/internal/fuzzy"
	"github.com/onlinedigital/hestia-nginx-tui/internal/hestia"
)

type sitesView struct {
	all    []hestia.Site
	input  textinput.Model
	view   []fuzzy.Ranked
	cursor int
	off    int
	w, h   int
}

func newSitesView() *sitesView {
	ti := textinput.New()
	ti.Prompt = "  "
	ti.Placeholder = "fuzzy search: domain, user, template…  (e.g. \"shop laravel\")"
	ti.PlaceholderStyle = sDim
	ti.Focus()
	return &sitesView{input: ti}
}

func (v *sitesView) resize(w, h int) { v.w, v.h = w, h; v.fix() }

func (v *sitesView) listH() int { return max(3, v.h-6) }

func (v *sitesView) setSites(s []hestia.Site, keep string) {
	v.all = s
	v.refilter(keep)
}

func (v *sitesView) refilter(keep string) {
	if keep == "" {
		keep = v.currentDomain()
	}
	v.view = fuzzy.Filter(v.input.Value(), len(v.all), func(i int) string { return v.all[i].SearchText() })
	v.cursor = 0
	for i, r := range v.view {
		if v.all[r.Index].Domain == keep {
			v.cursor = i
			break
		}
	}
	v.off = 0
	v.fix()
}

func (v *sitesView) fix() {
	if v.cursor >= len(v.view) {
		v.cursor = max(0, len(v.view)-1)
	}
	if v.cursor < v.off {
		v.off = v.cursor
	}
	if lh := v.listH(); v.cursor >= v.off+lh {
		v.off = v.cursor - lh + 1
	}
}

func (v *sitesView) current() (hestia.Site, bool) {
	if v.cursor < 0 || v.cursor >= len(v.view) {
		return hestia.Site{}, false
	}
	return v.all[v.view[v.cursor].Index], true
}

func (v *sitesView) currentDomain() string {
	if s, ok := v.current(); ok {
		return s.Domain
	}
	return ""
}

func (a *App) updateSites(msg tea.Msg) tea.Cmd {
	v := a.sv
	switch m := msg.(type) {
	case tea.MouseMsg:
		switch {
		case m.Button == tea.MouseButtonWheelUp:
			v.cursor = max(0, v.cursor-2)
		case m.Button == tea.MouseButtonWheelDown:
			v.cursor = min(len(v.view)-1, v.cursor+2)
		case m.Button == tea.MouseButtonLeft && m.Action == tea.MouseActionPress:
			row := m.Y - 5 // header(1) + search box(3) + column titles(1)
			if row >= 0 && row < v.listH() && v.off+row < len(v.view) {
				idx := v.off + row
				if idx == v.cursor {
					return a.siteMenu()
				}
				v.cursor = idx
			}
		}
		v.fix()
		return nil
	case tea.KeyMsg:
		switch m.String() {
		case "ctrl+c", "ctrl+q":
			return tea.Quit
		case "esc":
			if v.input.Value() != "" {
				v.input.SetValue("")
				v.refilter("")
			}
			return nil
		case "up", "ctrl+p":
			v.cursor = max(0, v.cursor-1)
		case "down", "ctrl+n":
			v.cursor = min(len(v.view)-1, v.cursor+1)
		case "pgup":
			v.cursor = max(0, v.cursor-v.listH())
		case "pgdown":
			v.cursor = min(len(v.view)-1, v.cursor+v.listH())
		case "enter":
			return a.siteMenu()
		case "tab":
			a.mode = modeUpstreams
			a.reloadUpstreams()
			return nil
		case "ctrl+r":
			a.reloadSites("")
			a.reloadUpstreams()
			a.setToast("reloaded", false)
			return nil
		case "f1":
			a.showHelp()
			return nil
		case "ctrl+u":
			return a.startUpdate()
		default:
			var cmd tea.Cmd
			old := v.input.Value()
			v.input, cmd = v.input.Update(m)
			if v.input.Value() != old {
				v.refilter("")
			}
			return cmd
		}
		v.fix()
	}
	return nil
}

func (a *App) showHelp() {
	body := strings.Join([]string{
		"Sites       type = fuzzy search   ↑/↓ PgUp/PgDn move   Enter actions   Tab upstreams   Ctrl+R reload   Ctrl+C quit",
		"Actions     change template · edit custom locations (built-in editor or nano) · view generated config · reset · re-apply",
		"Custom      each site gets <state>/sites/<domain>/locations.src, shared by the HTTP (.tpl) and HTTPS (.stpl) template",
		"Editor      nano keys, soft wrap:  ^O write  ^S save+apply  ^X exit  ^W search  ^\\ replace  ^K/^U cut/paste  M-U undo  F2 insert upstream",
		"Upstreams   Tab from the site list: n new · e edit form · r raw edit · d delete.  Reference them as proxy_pass http://<name>;",
		"Updates     Ctrl+U updates the binary from GitHub releases (also: hestia-nginx-tui update)",
		"Safety      every apply runs nginx -t first and rolls back automatically if nginx rejects the config",
	}, "\n")
	a.popup = &resultPopup{title: "Help", body: body, ok: true}
}

// ---------------------------------------------------------------------------
// view

func (a *App) header(active string) string {
	tab := func(name string, on bool) string {
		if on {
			return lipgloss.NewStyle().Bold(true).Foreground(cText).Background(cSel).Padding(0, 1).Render(name)
		}
		return lipgloss.NewStyle().Foreground(cDim).Padding(0, 1).Render(name)
	}
	left := sTitle.Render("◆ HESTIA·NGINX") + " " + tab("Sites", active == "sites") + tab("Upstreams", active == "upstreams")
	users := map[string]bool{}
	custom := 0
	for _, s := range a.sv.all {
		users[s.User] = true
		if hestia.IsCustom(s.Template) {
			custom++
		}
	}
	right := sDim.Render(fmt.Sprintf("%d sites · %d users · %d custom", len(a.sv.all), len(users), custom))
	if a.warn != "" {
		right = sWarn.Render("⚠ "+a.warn) + "  " + right
	}
	if a.latest != "" {
		right = lipgloss.NewStyle().Foreground(cGreen).Bold(true).Render("⬆ "+a.latest+" available · ^U") + "  " + right
	}
	gap := a.w - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap < 1 {
		gap = 1
	}
	return lipgloss.NewStyle().Width(a.w).Background(cPanel).Render(left + strings.Repeat(" ", gap) + right)
}

func (a *App) footerLine(hints string) string {
	if a.toast != "" {
		st := sOK
		if a.toastErr {
			st = sErr
		}
		return " " + st.Render(a.toast)
	}
	return " " + hints
}

func (a *App) viewSites() string {
	v := a.sv
	w := a.w
	var out []string
	out = append(out, a.header("sites"))

	sb := box(cAccent).Width(w - 2)
	v.input.Width = w - 8
	out = append(out, sb.Render(sHi.Render("❯")+v.input.View()))

	detailW := 0
	if w >= 110 {
		detailW = 44
	}
	listW := w - detailW
	if detailW > 0 {
		listW--
	}

	// columns
	userW, tplW, domMax := 4, 8, 18
	for _, s := range v.all {
		domMax = max(domMax, len([]rune(s.Domain))+2)
		userW = max(userW, min(14, len([]rune(s.User))))
		tplW = max(tplW, min(24, len([]rune(s.Template))))
	}
	domW := listW - 2 - userW - tplW - 4 - 4
	domW = min(domW, domMax)
	if domW < 10 {
		domW = 10
	}
	titles := "  " + padRight(sDim.Render("DOMAIN"), domW) + " " + padRight(sDim.Render("USER"), userW) + " " + padRight(sDim.Render("TEMPLATE"), tplW) + " " + sDim.Render("SSL")
	list := []string{padRight(titles, listW)}

	end := min(len(v.view), v.off+v.listH())
	for i := v.off; i < end; i++ {
		rk := v.view[i]
		s := v.all[rk.Index]
		sel := i == v.cursor
		bg := lipgloss.Color("")
		if sel {
			bg = cSel
		}
		mk := func(fg lipgloss.Color, bold bool) lipgloss.Style {
			st := lipgloss.NewStyle().Foreground(fg).Background(bg)
			if bold {
				st = st.Bold(true)
			}
			return st
		}
		dl, ul := len([]rune(s.Domain)), len([]rune(s.User))
		dp, up, tp := map[int]bool{}, map[int]bool{}, map[int]bool{}
		for _, p := range rk.Pos {
			switch {
			case p < dl:
				dp[p] = true
			case p > dl && p <= dl+ul:
				up[p-dl-1] = true
			case p > dl+ul+1:
				tp[p-dl-ul-2] = true
			}
		}
		hi := mk(cPink, true)
		domFg, tplFg := cText, cDim
		if s.Suspended {
			domFg = cRed
		}
		if hestia.IsCustom(s.Template) {
			tplFg = cAccent
		}
		marker := "  "
		if sel {
			marker = mk(cPink, true).Render("▌ ")
		}
		sslMark := mk(cDim, false).Render(" ○ ")
		if s.SSL {
			sslMark = mk(cGreen, false).Render(" ● ")
		}
		row := marker + hl(s.Domain, dp, mk(domFg, sel), hi, domW) + mk(cText, false).Render(" ") +
			hl(s.User, up, mk(cCyan, false), hi, userW) + mk(cText, false).Render(" ") +
			hl(s.Template, tp, mk(tplFg, false), hi, tplW) + sslMark
		list = append(list, row)
	}
	if len(v.view) == 0 {
		list = append(list, "", sDim.Render("   no site matches your search"))
	}
	for len(list) < v.listH()+1 {
		list = append(list, "")
	}
	left := strings.Join(list, "\n")
	if detailW > 0 {
		left = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", a.detailPanel(detailW, v.listH()+1))
	}
	out = append(out, left)

	if len(v.view) > 0 {
		out = append(out, a.footerLine(keyHints("Enter", "actions", "Tab", "upstreams", "^R", "reload", "F1", "help", "^C", "quit")+
			sDim.Render(fmt.Sprintf("   %d/%d", v.cursor+1, len(v.view)))))
	} else {
		out = append(out, a.footerLine(keyHints("Esc", "clear search", "^C", "quit")))
	}
	return strings.Join(out, "\n")
}

func (a *App) detailPanel(w, h int) string {
	s, ok := a.sv.current()
	if !ok {
		return box(cDim).Width(w - 2).Height(h - 2).Render(sDim.Render("no site selected"))
	}
	kv := func(k, v string) string {
		if v == "" {
			v = sDim.Render("—")
		}
		return sLabel.Render(k) + sValue.Render(v)
	}
	iw := w - 4
	var l []string
	l = append(l, lipgloss.NewStyle().Bold(true).Foreground(cAccent).Render(truncate(s.Domain, iw)))
	l = append(l, "")
	l = append(l, kv("user", s.User), kv("ip", s.IP))
	if s.Aliases != "" {
		l = append(l, kv("aliases", truncate(strings.ReplaceAll(s.Aliases, ",", " "), iw-10)))
	}
	l = append(l, kv("template", s.Template))
	l = append(l, kv("php", s.Backend))
	ssl := "no"
	if s.SSL {
		ssl = sOK.Render("yes")
	}
	l = append(l, kv("ssl", ssl))
	if s.Suspended {
		l = append(l, kv("status", sErr.Render("suspended")))
	}
	l = append(l, "")
	if hestia.IsCustom(s.Template) {
		l = append(l, sHi.Render("◆ custom locations"))
		l = append(l, kv("seeded", a.env.ReadBase(s.Domain)))
	} else if ex, _ := a.env.HasCustomLocations(s.Domain); ex {
		l = append(l, sWarn.Render("custom locations saved, not active"))
	} else {
		l = append(l, sDim.Render("stock template"))
	}
	if ex, t := a.env.HasCustomLocations(s.Domain); ex {
		l = append(l, kv("edited", ago(t)))
		l = append(l, sDim.Render(truncate(a.env.SrcPath(s.Domain), iw)))
	}
	hp, _ := a.env.DomainConfPaths(s)
	if _, err := os.Stat(hp); err == nil {
		l = append(l, "", sDim.Render(truncate(hp, iw)))
	}
	if ups := a.upstreamsUsedBy(s.Domain); len(ups) > 0 {
		l = append(l, "", sLabel.Render("upstreams")+sValue.Render(strings.Join(ups, ", ")))
	}
	return box(cDim).Width(w - 2).Height(h - 2).Render(strings.Join(l, "\n"))
}

func (a *App) upstreamsUsedBy(domain string) []string {
	var out []string
	for _, u := range a.uv.items {
		for _, d := range u.UsedBy {
			if d == domain {
				out = append(out, u.Name)
			}
		}
	}
	return out
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}
