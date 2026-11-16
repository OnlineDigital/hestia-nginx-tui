package tui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/onlinedigital/hestia-nginx-tui/internal/hestia"
	"github.com/onlinedigital/hestia-nginx-tui/internal/upstream"
)

// seedBaseFor is the stock template to pre-fill a site's locations from: the
// template the site uses now, or - when it already uses "tui" - the one its
// locations were originally seeded from (may be empty).
func (a *App) seedBaseFor(s hestia.Site) string {
	if !hestia.IsCustom(s.Template) {
		return s.Template
	}
	return a.env.ReadBase(s.Domain)
}

// pickBase asks for a stock template and calls then with it.
func (a *App) pickBase(title, sub, preselect string, then func(a *App, base string) tea.Cmd) tea.Cmd {
	tpls, err := a.env.StockTemplates()
	if err != nil {
		a.showResult("Cannot list templates", asyncResult{Err: err}, nil)
		return nil
	}
	var items []pickItem
	for _, t := range tpls {
		it := pickItem{Label: t.Name, Value: t.Name}
		if t.Name == preselect {
			it.Tag = "suggested"
		}
		items = append(items, it)
	}
	p := newPicker(title, items, true, func(a *App, it pickItem) tea.Cmd { return then(a, it.Value.(string)) })
	p.sub = sub
	for i, rk := range p.view {
		if p.items[rk.Index].Value == preselect {
			p.cursor = i
		}
	}
	p.fixScroll()
	a.popup = p
	return nil
}

// siteMenu is the popup shown on Enter.
func (a *App) siteMenu() tea.Cmd {
	s, ok := a.sv.current()
	if !ok {
		return nil
	}
	hasSrc, _ := a.env.HasCustomLocations(s.Domain)
	items := []pickItem{
		{Label: "Change template…", Detail: "Switch the site to another stock template (optionally with custom locations)", Value: "tpl"},
		{Label: "Edit custom Nginx locations", Detail: "Built-in editor · nano keys · soft wrap · shared by HTTP and HTTPS", Value: "edit"},
		{Label: "Edit custom locations in nano", Detail: "Runs the real nano inside this terminal", Value: "nano"},
		{Label: "View generated nginx config", Detail: "Read-only: what Hestia actually generated for this domain", Value: "view"},
		{Label: "Re-apply & reload", Detail: "Rebuild the domain config (v-change-web-domain-tpl … yes)", Value: "reapply"},
	}
	if hasSrc {
		items = append(items, pickItem{Label: "Re-seed locations from a template…", Detail: "Replace the site's locations with those of a stock template (old text kept in history/)", Value: "reset"})
	}
	p := newPicker(s.Domain, items, false, func(a *App, it pickItem) tea.Cmd {
		switch it.Value {
		case "tpl":
			return a.pickTemplate(s)
		case "edit":
			return a.editLocations(s, false)
		case "nano":
			return a.editLocations(s, true)
		case "view":
			return a.viewGenerated(s)
		case "reapply":
			return a.reapply(s)
		case "reset":
			return a.resetLocations(s)
		}
		return nil
	})
	tag := s.Template
	if hestia.IsCustom(s.Template) {
		tag += " (custom locations)"
		if b := a.env.ReadBase(s.Domain); b != "" {
			tag += " · seeded from " + b
		}
	}
	p.sub = s.User + " · " + tag
	p.width = 70
	a.popup = p
	return nil
}

// ---------------------------------------------------------------------------

func (a *App) pickTemplate(s hestia.Site) tea.Cmd {
	tpls, err := a.env.StockTemplates()
	if err != nil {
		a.showResult("Cannot list templates", asyncResult{Err: err}, nil)
		return nil
	}
	cur := a.seedBaseFor(s)
	custom := hestia.IsCustom(s.Template)
	var items []pickItem
	for _, t := range tpls {
		it := pickItem{Label: t.Name, Value: t.Name}
		if t.Name == s.Template {
			it.Tag = "current"
		} else if t.Name == cur {
			it.Tag = "seeded from"
		}
		items = append(items, it)
	}
	p := newPicker("Change template · "+s.Domain, items, true, nil)
	refresh := func() {
		if custom {
			p.sub = "Mode: CUSTOM (tui template + your locations) · Ctrl+T switches to stock"
		} else {
			p.sub = "Mode: STOCK template · Ctrl+T switches to custom locations (tui)"
		}
	}
	refresh()
	p.footer = keyHints("Enter", "apply", "^T", "stock/custom", "Esc", "close")
	p.onKey = func(a *App, p *pickerPopup, k tea.KeyMsg) (tea.Cmd, bool) {
		if k.String() == "ctrl+t" {
			custom = !custom
			refresh()
			return nil, true
		}
		return nil, false
	}
	p.onPick = func(a *App, it pickItem) tea.Cmd {
		name := it.Value.(string)
		if custom {
			return a.applyCustomFrom(s, name)
		}
		env := a.env
		return a.async("Applying template "+name+"…", func() asyncResult {
			log, err := env.ApplyStock(s, name)
			return asyncResult{Log: log, Err: err}
		}, a.afterApply(s.Domain, "Template applied"))
	}
	a.popup = p
	return nil
}

// applyCustomFrom applies the tui template using locations derived from the
// stock template base. If the site already has locations the user decides
// whether to keep them or replace them with those of base.
func (a *App) applyCustomFrom(s hestia.Site, base string) tea.Cmd {
	env := a.env
	run := func(a *App, replace bool) tea.Cmd {
		return a.async("Applying custom locations…", func() asyncResult {
			var log string
			if replace {
				if err := env.ReseedLocations(s, base); err != nil {
					return asyncResult{Err: err}
				}
				log = "✓ locations replaced with those of \"" + base + "\" (old text kept in history/)\n"
			}
			l, err := env.ApplyCustom(s, base)
			return asyncResult{Log: log + l, Err: err}
		}, a.afterApply(s.Domain, "Custom locations applied"))
	}
	if ex, _ := env.HasCustomLocations(s.Domain); !ex {
		return run(a, false)
	}
	p := newPicker("Locations already exist for "+s.Domain, []pickItem{
		{Label: "Keep my current locations", Detail: "Apply the tui template with the text you already have", Value: false},
		{Label: "Replace with the locations of \"" + base + "\"", Detail: "Your current text is kept in history/", Value: true},
	}, false, func(a *App, it pickItem) tea.Cmd { return run(a, it.Value.(bool)) })
	p.width = 74
	a.popup = p
	return nil
}

func (a *App) afterApply(domain, okTitle string) func(a *App, r asyncResult) tea.Cmd {
	return func(a *App, r asyncResult) tea.Cmd {
		a.reloadSites(domain)
		a.reloadUpstreams()
		title := okTitle
		if r.Err != nil {
			title = "Failed (nothing was changed on nginx)"
		}
		a.showResult(title, r, nil)
		return nil
	}
}

func (a *App) reapply(s hestia.Site) tea.Cmd {
	env := a.env
	return a.async("Re-applying "+s.Template+"…", func() asyncResult {
		var log string
		var err error
		if hestia.IsCustom(s.Template) {
			log, err = env.ApplyCustom(s, "")
		} else {
			log, err = env.ApplyStock(s, s.Template)
		}
		return asyncResult{Log: log, Err: err}
	}, a.afterApply(s.Domain, "Re-applied"))
}

// ---------------------------------------------------------------------------
// custom locations

// locationsTarget builds the editor target. text/state describe where the
// initial content comes from; base is the template it was pre-filled from.
func (a *App) locationsTarget(s hestia.Site, text, state, base string) *editTarget {
	env := a.env
	domain := s.Domain
	hint := "Site " + s.Domain + " (" + s.User + ") · " + state + "\n" +
		"Saved to " + env.SrcPath(s.Domain) + "\n" +
		"Included by BOTH the HTTP (tui.tpl) and HTTPS (tui.stpl) vhost, so it holds everything site specific:\n" +
		"root, index, locations and any other directive.\n" +
		"Placeholders (resolved when applying): " + strings.Join(hestia.Placeholders, " ") + "\n" +
		"F2 inserts proxy_pass for one of your upstreams."
	for _, w := range env.StockWarnings(base) {
		hint += "\nWARNING: " + w
	}
	return &editTarget{
		title:    "locations · " + s.Domain + " · " + state,
		hint:     hint,
		text:     text,
		filename: s.Domain + "-locations.conf",
		save:     func(t string) error { return env.SaveLocations(s, t) },
		buildApply: func(a *App, t string) func() asyncResult {
			cur := s
			for _, x := range a.sv.all { // use the freshest site data (template may have changed)
				if x.Domain == domain {
					cur = x
				}
			}
			return func() asyncResult {
				if err := env.SaveLocations(cur, t); err != nil {
					return asyncResult{Err: err}
				}
				env.RecordBase(domain, base)
				log, err := env.ApplyCustom(cur, base)
				return asyncResult{Log: log, Err: err, Saved: true}
			}
		},
		onExit: func(a *App) tea.Cmd {
			a.mode = modeSites
			a.reloadSites(domain)
			return nil
		},
	}
}

func (a *App) editLocations(s hestia.Site, nano bool) tea.Cmd {
	open := func(a *App, text, state, base string) tea.Cmd {
		t := a.locationsTarget(s, text, state, base)
		if nano {
			return a.openNano(t)
		}
		a.openEditor(t)
		return nil
	}
	if text, ok := a.env.ReadLocations(s.Domain); ok {
		return open(a, text, "saved locations", a.env.ReadBase(s.Domain))
	}
	seed := func(a *App, base string) tea.Cmd {
		text, err := a.env.SeedLocations(base)
		if err != nil {
			a.showResult("Cannot pre-fill locations", asyncResult{Err: fmt.Errorf("from template %q: %w", base, err)}, nil)
			return nil
		}
		return open(a, text, "NEW - pre-filled from the \""+base+"\" template", base)
	}
	base := a.seedBaseFor(s)
	if base == "" || base == "suspended" {
		return a.pickBase("Pre-fill locations from…", "This site already uses tui, pick the stock template to start from", "default", seed)
	}
	return seed(a, base)
}

func (a *App) viewGenerated(s hestia.Site) tea.Cmd {
	hp, sp := a.env.DomainConfPaths(s)
	var b strings.Builder
	found := false
	for _, p := range []string{hp, sp} {
		if data, err := os.ReadFile(p); err == nil {
			found = true
			fmt.Fprintf(&b, "######## %s ########\n\n%s\n", p, data)
		}
	}
	if !found {
		a.showResult("Nothing to show", asyncResult{Err: fmt.Errorf("no generated config found at %s", hp)}, nil)
		return nil
	}
	t := &editTarget{
		title:    "generated config · " + s.Domain,
		text:     b.String(),
		readOnly: true,
		onExit:   func(a *App) tea.Cmd { a.mode = modeSites; return nil },
	}
	a.openEditor(t)
	return nil
}

func (a *App) resetLocations(s hestia.Site) tea.Cmd {
	return a.pickBase("Re-seed locations · "+s.Domain, "Pick the stock template whose locations should replace the current ones",
		a.seedBaseFor(s), func(a *App, base string) tea.Cmd {
			a.popup = &confirmPopup{
				title:  "Replace custom locations",
				body:   "Replace the saved locations of " + s.Domain + " with those of the stock \"" + base + "\" template?\nThe current text is kept in history/. Nothing is applied until you apply.",
				yes:    "replace",
				danger: true,
				onYes: func(a *App) tea.Cmd {
					if err := a.env.ReseedLocations(s, base); err != nil {
						a.showResult("Re-seed failed", asyncResult{Err: err}, nil)
					} else {
						a.setToast("locations replaced with those of "+base+" — edit/apply to use them", false)
					}
					return nil
				},
			}
			return nil
		})
}

// ---------------------------------------------------------------------------
// upstream snippet insertion inside the editor (F2)

func (a *App) pickUpstreamSnippet() tea.Cmd {
	a.reloadUpstreams()
	if len(a.uv.items) == 0 {
		a.ed.Notify("No upstreams yet: create one from the Upstreams tab", true)
		return nil
	}
	var items []pickItem
	for _, u := range a.uv.items {
		items = append(items, pickItem{
			Label:  u.Name,
			Detail: upstream.FormatServers(u.Servers) + keepaliveText(u.Upstream),
			Value:  u.Name,
		})
	}
	p := newPicker("Insert upstream", items, true, func(a *App, it pickItem) tea.Cmd {
		name := it.Value.(string)
		var kinds []pickItem
		for i, n := range upstream.SnippetNames {
			kinds = append(kinds, pickItem{Label: n, Detail: strings.SplitN(upstream.Snippet(name, upstream.SnippetKind(i)), "\n", 2)[0], Value: upstream.SnippetKind(i)})
		}
		kp := newPicker("Insert "+name+" as…", kinds, false, func(a *App, k pickItem) tea.Cmd {
			a.ed.InsertSnippet(upstream.Snippet(name, k.Value.(upstream.SnippetKind)))
			return nil
		})
		kp.sub = "Inserted as plain text – edit it freely afterwards"
		a.popup = kp
		return nil
	})
	p.sub = "Pick the upstream to reference with proxy_pass"
	a.popup = p
	return nil
}

func keepaliveText(u upstream.Upstream) string {
	if u.Keepalive > 0 {
		return fmt.Sprintf("  · keepalive %d", u.Keepalive)
	}
	return ""
}
