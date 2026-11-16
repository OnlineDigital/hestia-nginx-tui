package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/onlinedigital/hestia-nginx-tui/internal/hestia"
	"github.com/onlinedigital/hestia-nginx-tui/internal/upstream"
)

// ---------------------------------------------------------------------------
// list

type upstreamsView struct {
	items  []hestia.UpstreamEntry
	cursor int
	off    int
}

func (v *upstreamsView) set(items []hestia.UpstreamEntry) {
	v.items = items
	if v.cursor >= len(items) {
		v.cursor = max(0, len(items)-1)
	}
}

func (v *upstreamsView) current() (hestia.UpstreamEntry, bool) {
	if v.cursor < 0 || v.cursor >= len(v.items) {
		return hestia.UpstreamEntry{}, false
	}
	return v.items[v.cursor], true
}

func (a *App) updateUpstreams(msg tea.Msg) tea.Cmd {
	v := a.uv
	switch m := msg.(type) {
	case tea.MouseMsg:
		switch m.Button {
		case tea.MouseButtonWheelUp:
			v.cursor = max(0, v.cursor-1)
		case tea.MouseButtonWheelDown:
			v.cursor = min(len(v.items)-1, v.cursor+1)
		}
	case tea.KeyMsg:
		switch m.String() {
		case "ctrl+c", "ctrl+q":
			return tea.Quit
		case "esc", "tab", "shift+tab":
			a.mode = modeSites
		case "up", "k", "ctrl+p":
			v.cursor = max(0, v.cursor-1)
		case "down", "j", "ctrl+n":
			v.cursor = min(len(v.items)-1, v.cursor+1)
		case "n":
			a.openForm(nil)
		case "enter", "e":
			if u, ok := v.current(); ok {
				if !u.Parsed || u.Complex {
					return a.editUpstreamRaw(u)
				}
				a.openForm(&u)
			}
		case "r":
			if u, ok := v.current(); ok {
				return a.editUpstreamRaw(u)
			}
		case "d", "delete":
			if u, ok := v.current(); ok {
				return a.confirmDeleteUpstream(u)
			}
		case "ctrl+r":
			a.reloadUpstreams()
			a.setToast("reloaded", false)
		case "f1":
			a.showHelp()
		case "ctrl+u":
			return a.startUpdate()
		}
	}
	return nil
}

func (a *App) confirmDeleteUpstream(u hestia.UpstreamEntry) tea.Cmd {
	body := "Delete upstream \"" + u.Name + "\"? nginx is validated first; if a location still uses it the deletion is rolled back."
	if len(u.UsedBy) > 0 {
		body += "\n\nStill referenced by: " + strings.Join(u.UsedBy, ", ")
	}
	a.popup = &confirmPopup{title: "Delete upstream", body: body, yes: "delete", danger: true, onYes: func(a *App) tea.Cmd {
		env := a.env
		return a.async("Deleting "+u.Name+"…", func() asyncResult {
			log, err := env.DeleteUpstream(u.Name)
			return asyncResult{Log: log, Err: err}
		}, func(a *App, r asyncResult) tea.Cmd {
			a.reloadUpstreams()
			title := "Upstream deleted"
			if r.Err != nil {
				title = "Not deleted"
			}
			a.showResult(title, r, nil)
			return nil
		})
	}}
	return nil
}

// upstreamTarget builds the editor target for a raw upstream file. Saving and
// applying are the same operation: nginx -t gates every write.
func (a *App) upstreamTarget(name, text string, onExit func(a *App) tea.Cmd) *editTarget {
	env := a.env
	return &editTarget{
		title:    "upstream · " + name,
		text:     text,
		filename: name + ".conf",
		hint: "Full nginx text of upstream \"" + name + "\" – any directive nginx supports is allowed.\n" +
			"Saving validates with nginx -t, rolls back on error and reloads nginx.\n" +
			"Use it in a location as: proxy_pass http://" + name + ";",
		buildApply: func(a *App, t string) func() asyncResult {
			return func() asyncResult {
				u, ok := upstream.Parse(t)
				if !ok {
					return asyncResult{Err: fmt.Errorf("no `upstream %s { … }` block found", name)}
				}
				if u.Name != name {
					return asyncResult{Err: fmt.Errorf("the block is named %q but this file is %q: create a new upstream instead of renaming", u.Name, name)}
				}
				log, err := env.SaveUpstream(name, t)
				return asyncResult{Log: log, Err: err, Saved: err == nil}
			}
		},
		onExit: onExit,
	}
}

func (a *App) editUpstreamRaw(u hestia.UpstreamEntry) tea.Cmd {
	t := a.upstreamTarget(u.Name, u.Text, func(a *App) tea.Cmd { a.mode = modeUpstreams; a.reloadUpstreams(); return nil })
	a.openEditor(t)
	return nil
}

func (a *App) viewUpstreams() string {
	v := a.uv
	w := a.w
	out := []string{a.header("upstreams")}
	detailW := 0
	if w >= 100 {
		detailW = 46
	}
	listW := w - detailW
	if detailW > 0 {
		listW--
	}
	h := a.h - 3
	list := []string{"  " + padRight(sDim.Render("NAME"), 18) + " " + padRight(sDim.Render("SERVERS"), max(10, listW-2-18-1-8-6-4)) + " " + sDim.Render("KEEP") + "  " + sDim.Render("USED")}
	if len(v.items) == 0 {
		list = append(list, "", sDim.Render("   no upstreams yet"), "",
			"   "+sKey.Render("n")+sKeyDesc.Render(" create one: ip:port, keepalive, then use it from any location with F2 in the editor"))
	}
	if v.cursor < v.off {
		v.off = v.cursor
	}
	if v.cursor >= v.off+h-1 {
		v.off = v.cursor - (h - 1) + 1
	}
	for i := v.off; i < min(len(v.items), v.off+h-1); i++ {
		u := v.items[i]
		sel := i == v.cursor
		bg := lipgloss.Color("")
		if sel {
			bg = cSel
		}
		st := func(fg lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(bg) }
		serversW := max(10, listW-2-18-1-8-6-4)
		servers := upstream.FormatServers(u.Servers)
		if !u.Parsed {
			servers = "(raw / unrecognised)"
		}
		ka := "—"
		if u.Keepalive > 0 {
			ka = strconv.Itoa(u.Keepalive)
		}
		used := "—"
		if n := len(u.UsedBy); n > 0 {
			used = fmt.Sprintf("%d site", n)
			if n > 1 {
				used += "s"
			}
		}
		mk := "  "
		if sel {
			mk = st(cPink).Bold(true).Render("▌ ")
		}
		row := mk + st(cAccent).Bold(true).Render(padRight(plainTrunc(u.Name, 18), 18)) + st(cText).Render(" ") +
			st(cText).Render(padRight(plainTrunc(servers, serversW), serversW)) + st(cText).Render(" ") +
			st(cGreen).Render(padRight(ka, 4)) + st(cText).Render("  ") + st(cDim).Render(used)
		list = append(list, row)
	}
	for len(list) < h {
		list = append(list, "")
	}
	left := strings.Join(list, "\n")
	if detailW > 0 {
		left = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", a.upstreamDetail(detailW, h))
	}
	out = append(out, left)
	out = append(out, a.footerLine(keyHints("n", "new", "Enter/e", "edit form", "r", "raw edit", "d", "delete", "Tab", "sites", "^C", "quit")))
	return strings.Join(out, "\n")
}

func colorize(text string, w int) string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		rs := []rune(line)
		cls := classify(rs)
		var sb strings.Builder
		for i, r := range rs {
			if r == '\t' {
				sb.WriteString("    ")
				continue
			}
			sb.WriteString(clsStyles[cls[i]].Render(string(r)))
		}
		out = append(out, truncate(sb.String(), w))
	}
	return strings.Join(out, "\n")
}

func (a *App) upstreamDetail(w, h int) string {
	u, ok := a.uv.current()
	if !ok {
		return box(cDim).Width(w - 2).Height(h - 2).Render(sDim.Render("nothing selected"))
	}
	l := []string{
		lipgloss.NewStyle().Bold(true).Foreground(cAccent).Render(u.Name), "",
		colorize(u.Text, w-4), "",
		sHi.Render("use in a location:"),
		sText.Render("  proxy_pass http://" + u.Name + ";"),
	}
	if u.Keepalive > 0 {
		l = append(l, sDim.Render("  keepalive needs also:"), sText.Render("  proxy_http_version 1.1;"), sText.Render(`  proxy_set_header Connection "";`))
	}
	if len(u.UsedBy) > 0 {
		l = append(l, "", sLabel.Render("used by"), sValue.Render(truncate(strings.Join(u.UsedBy, ", "), w-4)))
	}
	return box(cDim).Width(w - 2).Height(h - 2).Render(strings.Join(l, "\n"))
}

// ---------------------------------------------------------------------------
// form

const (
	fName = iota
	fServers
	fMethod
	fKeepalive
	fKATimeout
	fKAReqs
	fCount
)

var formLabels = [fCount]string{"Name", "Servers", "Balancing", "keepalive", "keepalive_timeout", "keepalive_requests"}

type formView struct {
	inputs [fCount]textinput.Model // fMethod unused
	method int
	focus  int
	edit   bool // editing an existing upstream (name is fixed)
	extra  []string
	err    string
}

func (a *App) openForm(u *hestia.UpstreamEntry) {
	f := &formView{}
	for i := range f.inputs {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 0
		f.inputs[i] = ti
	}
	f.inputs[fName].Placeholder = "app_backend"
	f.inputs[fServers].Placeholder = "10.0.0.5:8080, 10.0.0.6:8080 weight=2"
	f.inputs[fKeepalive].SetValue("32")
	f.inputs[fKeepalive].Placeholder = "0 = off"
	f.inputs[fKATimeout].Placeholder = "60s (nginx default)"
	f.inputs[fKAReqs].Placeholder = "1000 (nginx default)"
	if u != nil {
		f.edit = true
		f.inputs[fName].SetValue(u.Name)
		f.inputs[fServers].SetValue(upstream.FormatServers(u.Servers))
		for i, m := range upstream.Methods {
			if m == u.Method {
				f.method = i
			}
		}
		f.inputs[fKeepalive].SetValue(zeroEmpty(u.Keepalive))
		f.inputs[fKATimeout].SetValue(u.KeepaliveTimeout)
		f.inputs[fKAReqs].SetValue(zeroEmpty(u.KeepaliveRequests))
		f.extra = u.Extra
		f.focus = fServers
	}
	f.focusField(f.focus)
	a.fv = f
	a.mode = modeForm
}

func zeroEmpty(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func (f *formView) focusField(i int) {
	for k := range f.inputs {
		f.inputs[k].Blur()
	}
	f.focus = i
	if i != fMethod {
		f.inputs[i].Focus()
	}
}

func (f *formView) next(d int) {
	i := f.focus
	for {
		i = (i + d + fCount) % fCount
		if i == fName && f.edit {
			continue
		}
		break
	}
	f.focusField(i)
}

// build turns the form into an Upstream.
func (f *formView) build() (upstream.Upstream, error) {
	u := upstream.Upstream{
		Name:             strings.TrimSpace(f.inputs[fName].Value()),
		Servers:          upstream.ParseServers(f.inputs[fServers].Value()),
		KeepaliveTimeout: strings.TrimSpace(f.inputs[fKATimeout].Value()),
		Extra:            f.extra,
	}
	if m := upstream.Methods[f.method]; m != "round-robin" {
		u.Method = m
	}
	var err error
	if s := strings.TrimSpace(f.inputs[fKeepalive].Value()); s != "" {
		if u.Keepalive, err = strconv.Atoi(s); err != nil || u.Keepalive < 0 {
			return u, fmt.Errorf("keepalive must be a number (0 = off)")
		}
	}
	if s := strings.TrimSpace(f.inputs[fKAReqs].Value()); s != "" {
		if u.KeepaliveRequests, err = strconv.Atoi(s); err != nil || u.KeepaliveRequests < 0 {
			return u, fmt.Errorf("keepalive_requests must be a number")
		}
	}
	if strings.ContainsAny(u.KeepaliveTimeout, ";{} \"'") {
		return u, fmt.Errorf("keepalive_timeout looks invalid (e.g. 60s)")
	}
	return u, u.Validate()
}

func (a *App) updateForm(msg tea.Msg) tea.Cmd {
	f := a.fv
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "esc":
		a.mode = modeUpstreams
		return nil
	case "ctrl+c":
		return tea.Quit
	case "tab", "down":
		f.next(1)
		return nil
	case "shift+tab", "up":
		f.next(-1)
		return nil
	case "ctrl+s":
		return a.saveForm()
	case "ctrl+e":
		u, _ := f.build()
		if u.Name == "" {
			f.err = "type a name first"
			return nil
		}
		if len(u.Servers) == 0 {
			u.Servers = []upstream.Server{{Addr: "127.0.0.1:8080"}}
		}
		a.openEditor(a.upstreamTarget(u.Name, u.Render(), func(a *App) tea.Cmd {
			a.mode = modeUpstreams
			a.reloadUpstreams()
			return nil
		}))
		return nil
	case "enter":
		if f.focus == fKAReqs {
			return a.saveForm()
		}
		f.next(1)
		return nil
	}
	if f.focus == fMethod {
		switch k.String() {
		case "left", "h":
			f.method = (f.method + len(upstream.Methods) - 1) % len(upstream.Methods)
		case "right", "l", " ", "space":
			f.method = (f.method + 1) % len(upstream.Methods)
		}
		return nil
	}
	var cmd tea.Cmd
	f.inputs[f.focus], cmd = f.inputs[f.focus].Update(k)
	f.err = ""
	return cmd
}

func (a *App) saveForm() tea.Cmd {
	f := a.fv
	u, err := f.build()
	if err != nil {
		f.err = err.Error()
		return nil
	}
	env := a.env
	text := u.Render()
	return a.async("Saving upstream "+u.Name+"…", func() asyncResult {
		log, err := env.SaveUpstream(u.Name, text)
		return asyncResult{Log: log, Err: err}
	}, func(a *App, r asyncResult) tea.Cmd {
		a.reloadUpstreams()
		if r.Err == nil {
			a.mode = modeUpstreams
			for i, it := range a.uv.items {
				if it.Name == u.Name {
					a.uv.cursor = i
				}
			}
		}
		title := "Upstream saved"
		if r.Err != nil {
			title = "Not saved"
		}
		a.showResult(title, r, nil)
		return nil
	})
}

func (a *App) viewForm() string {
	f := a.fv
	w := a.w
	out := []string{a.header("upstreams")}
	title := "New upstream"
	if f.edit {
		title = "Edit upstream · " + f.inputs[fName].Value()
	}
	iw := w - 4
	if w >= 110 {
		iw = w/2 - 2
	}
	var rows []string
	rows = append(rows, lipgloss.NewStyle().Bold(true).Foreground(cAccent).Render(title), "")
	for i := 0; i < fCount; i++ {
		lab := formLabels[i]
		focus := i == f.focus
		ls := lipgloss.NewStyle().Foreground(cDim)
		if focus {
			ls = lipgloss.NewStyle().Foreground(cPink).Bold(true)
		}
		var val string
		switch {
		case i == fMethod:
			var parts []string
			for j, m := range upstream.Methods {
				if j == f.method {
					parts = append(parts, lipgloss.NewStyle().Foreground(cText).Background(cAccent).Padding(0, 1).Render(m))
				} else {
					parts = append(parts, sDim.Render(" "+m+" "))
				}
			}
			val = strings.Join(parts, "")
			if focus {
				val += sDim.Render("  ←/→")
			}
		case i == fName && f.edit:
			val = sText.Render(f.inputs[i].Value()) + sDim.Render("  (fixed)")
		default:
			f.inputs[i].Width = iw - 6
			val = f.inputs[i].View()
		}
		mark := "  "
		if focus {
			mark = sHi.Render("▌ ")
		}
		rows = append(rows, mark+ls.Render(lab), "  "+val, "")
	}
	if f.err != "" {
		rows = append(rows, sErr.Render("✗ "+f.err))
	} else if _, err := f.build(); err != nil {
		rows = append(rows, sWarn.Render("… "+err.Error()))
	} else {
		rows = append(rows, sOK.Render("✓ ready to save"))
	}
	rows = append(rows, "", sDim.Render("Servers: comma separated, each \"host:port [weight=N max_fails=N backup …]\"."),
		sDim.Render("keepalive pools idle connections; use it with proxy_http_version 1.1"),
		sDim.Render(`and proxy_set_header Connection "" (F2 in the editor inserts them).`),
		sDim.Render("Need something the form lacks? ^E opens the full text editor."))
	formBox := box(cAccent).Width(iw).Render(strings.Join(rows, "\n"))

	pw := w - iw - 5
	pu, _ := f.build()
	prev := "(fill the form to see the generated block)"
	if pu.Name != "" || len(pu.Servers) > 0 {
		name := pu.Name
		if name == "" {
			name = "…"
		}
		pu.Name = name
		prev = colorize(pu.Render(), pw-4)
	}
	preview := box(cDim).Width(max(20, pw)).Render(sDim.Render("preview — what will be written") + "\n\n" + prev)
	body := formBox
	if w >= 110 {
		body = lipgloss.JoinHorizontal(lipgloss.Top, formBox, " ", preview)
	} else {
		body = formBox + "\n" + preview
	}
	out = append(out, body)
	out = append(out, a.footerLine(keyHints("Tab", "next", "^S", "save (nginx -t + reload)", "^E", "raw editor", "Esc", "cancel")))
	return strings.Join(out, "\n")
}
