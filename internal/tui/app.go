// Package tui is the Bubble Tea interface of hestia-nginx-tui.
package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/onlinedigital/hestia-nginx-tui/internal/hestia"
	"github.com/onlinedigital/hestia-nginx-tui/internal/update"
)

// Version is the running release ("dev" for untagged builds); set by main.
var Version = "dev"

type updateCheckMsg struct{ tag string }

type mode int

const (
	modeSites mode = iota
	modeUpstreams
	modeForm
	modeEditor
)

// asyncResult is what background work reports back.
type asyncResult struct {
	Log   string
	Err   error
	Saved bool // the editor text reached disk even if the apply step failed
}

type asyncMsg struct {
	res  asyncResult
	then func(a *App, r asyncResult) tea.Cmd
}

type nanoDoneMsg struct {
	tmpdir, path, orig string
	target             *editTarget
	err                error
}

// editTarget describes what the editor is editing and how to persist it.
type editTarget struct {
	title    string
	hint     string
	readOnly bool
	text     string
	filename string
	// save writes the text only. nil means saving is inseparable from applying.
	save func(text string) error
	// buildApply is called on the UI goroutine and returns the work to run in
	// the background (save + apply). nil disables apply.
	buildApply func(a *App, text string) func() asyncResult
	// onExit runs when the editor is closed.
	onExit func(a *App) tea.Cmd
}

// App is the root Bubble Tea model.
type App struct {
	env  *hestia.Env
	w, h int
	mode mode

	sv *sitesView
	uv *upstreamsView
	fv *formView

	ed *Editor
	et *editTarget

	latest   string // newer release found on GitHub, if any
	popup    popup
	toast    string
	toastErr bool
	warn     string
}

func New(env *hestia.Env) *App {
	a := &App{env: env}
	a.sv = newSitesView()
	a.uv = &upstreamsView{}
	a.ed = NewEditor()
	if os.Geteuid() != 0 && !env.Sandbox {
		a.warn = "not running as root: applying changes will fail"
	}
	a.reloadSites("")
	a.reloadUpstreams()
	return a
}

func (a *App) Init() tea.Cmd {
	if Version == "dev" || os.Getenv("HNT_NO_UPDATE_CHECK") != "" {
		return nil
	}
	return func() tea.Msg { // one quick request, silently ignored on any failure
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		tag, err := update.LatestTag(ctx)
		if err != nil || !update.IsNewer(Version, tag) {
			return nil
		}
		return updateCheckMsg{tag}
	}
}

// startUpdate asks for confirmation and then updates the binary in place.
func (a *App) startUpdate() tea.Cmd {
	body := "Check GitHub for the latest release and replace this binary in place?\n(the download is verified against checksums.txt)"
	if a.latest != "" {
		body = "Update " + Version + " → " + a.latest + "?\nThe binary is replaced in place (sha256 verified); restart to use it."
	}
	a.popup = &confirmPopup{title: "Update hestia-nginx-tui", body: body, yes: "update", onYes: func(a *App) tea.Cmd {
		return a.async("Updating…", func() asyncResult {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			res, err := update.Run(ctx, Version, "")
			if err == nil && res.Updated {
				res.Log += "\n✓ done: quit and start hestia-nginx-tui again to use " + res.Tag + "\n"
			}
			return asyncResult{Log: res.Log, Err: err}
		}, func(a *App, r asyncResult) tea.Cmd {
			if r.Err == nil {
				a.latest = ""
			}
			title := "Update"
			if r.Err != nil {
				title = "Update failed"
			}
			a.showResult(title, r, nil)
			return nil
		})
	}}
	return nil
}

func (a *App) setToast(s string, isErr bool) { a.toast, a.toastErr = s, isErr }

func (a *App) reloadSites(keepDomain string) {
	sites, err := a.env.LoadSites()
	if err != nil {
		a.setToast("cannot read sites: "+err.Error(), true)
	}
	a.sv.setSites(sites, keepDomain)
}

func (a *App) reloadUpstreams() {
	ups, _ := a.env.ListUpstreams()
	a.uv.set(ups)
}

// async runs work in the background behind a spinner popup.
func (a *App) async(label string, work func() asyncResult, then func(a *App, r asyncResult) tea.Cmd) tea.Cmd {
	b := newBusy(label)
	a.popup = b
	return tea.Batch(b.sp.Tick, func() tea.Msg { return asyncMsg{res: work(), then: then} })
}

// showResult opens the transcript popup.
func (a *App) showResult(title string, r asyncResult, onClose func(a *App) tea.Cmd) {
	a.popup = &resultPopup{title: title, body: r.Log + errLine(r.Err), ok: r.Err == nil, onClose: onClose}
}

func errLine(err error) string {
	if err == nil {
		return ""
	}
	return "✗ " + err.Error() + "\n"
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = m.Width, m.Height
		a.ed.SetSize(a.w, a.h)
		a.sv.resize(a.w, a.h)
		return a, nil
	case asyncMsg:
		if _, ok := a.popup.(*busyPopup); ok {
			a.popup = nil
		}
		return a, m.then(a, m.res)
	case nanoDoneMsg:
		return a, a.nanoDone(m)
	case updateCheckMsg:
		a.latest = m.tag
		return a, nil
	case spinner.TickMsg:
		if a.popup != nil {
			return a, a.popup.Update(a, msg)
		}
		return a, nil
	}

	if a.popup != nil {
		switch msg.(type) {
		case tea.KeyMsg, tea.MouseMsg:
			return a, a.popup.Update(a, msg)
		}
		return a, nil
	}

	if _, ok := msg.(tea.KeyMsg); ok {
		a.toast = ""
	}

	switch a.mode {
	case modeSites:
		return a, a.updateSites(msg)
	case modeUpstreams:
		return a, a.updateUpstreams(msg)
	case modeForm:
		return a, a.updateForm(msg)
	case modeEditor:
		return a, a.updateEditor(msg)
	}
	return a, nil
}

func (a *App) View() string {
	if a.w == 0 || a.h == 0 {
		return "loading…"
	}
	var base string
	switch a.mode {
	case modeSites:
		base = a.viewSites()
	case modeUpstreams:
		base = a.viewUpstreams()
	case modeForm:
		base = a.viewForm()
	case modeEditor:
		base = a.ed.View()
	}
	base = fit(base, a.w, a.h)
	if a.popup != nil {
		base = overlayCenter(base, a.popup.View(a.w, a.h), a.w, a.h)
	}
	return base
}

// fit pads or crops a rendered screen to exactly w x h cells.
func fit(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	lines = lines[:h]
	for i, l := range lines {
		l = truncate(l, w)
		lines[i] = padRight(l, w)
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// editor integration

func (a *App) openEditor(t *editTarget) {
	a.et = t
	a.ed = NewEditor()
	a.ed.ReadOnly = t.readOnly
	a.ed.Title = t.title
	a.ed.Hint = t.hint
	a.ed.SetText(t.text)
	a.ed.SetSize(a.w, a.h)
	a.mode = modeEditor
}

func (a *App) closeEditor() tea.Cmd {
	var cmd tea.Cmd
	if a.et != nil && a.et.onExit != nil {
		cmd = a.et.onExit(a)
	}
	a.et = nil
	return cmd
}

func (a *App) updateEditor(msg tea.Msg) tea.Cmd {
	ev, cmd := a.ed.Update(msg)
	switch ev.kind {
	case evExit:
		return tea.Batch(cmd, a.closeEditor())
	case evSave:
		return tea.Batch(cmd, a.editorSave(ev.exitAfter))
	case evApply:
		return tea.Batch(cmd, a.editorApply(ev.exitAfter))
	case evInsertUpstream:
		return tea.Batch(cmd, a.pickUpstreamSnippet())
	}
	return cmd
}

func (a *App) editorSave(exitAfter bool) tea.Cmd {
	t := a.et
	if t == nil {
		return nil
	}
	if t.save == nil { // saving implies applying (e.g. upstream files)
		return a.editorApply(exitAfter)
	}
	text := a.ed.Text()
	if err := t.save(text); err != nil {
		a.ed.Notify("Error writing: "+err.Error(), true)
		return nil
	}
	a.ed.MarkSaved()
	a.ed.Notify("Wrote "+strconv.Itoa(len(a.ed.lines))+" lines  (not applied yet: ^S to apply)", false)
	if exitAfter {
		return a.closeEditor()
	}
	return nil
}

func (a *App) editorApply(exitAfter bool) tea.Cmd {
	t := a.et
	if t == nil || t.buildApply == nil {
		return nil
	}
	text := a.ed.Text()
	work := t.buildApply(a, text)
	return a.async("Saving and applying…", work, func(a *App, r asyncResult) tea.Cmd {
		if r.Saved || r.Err == nil {
			a.ed.MarkSaved()
		}
		a.reloadSites(a.sv.currentDomain())
		a.reloadUpstreams()
		title := "Applied"
		if r.Err != nil {
			title = "Apply failed (nothing was changed on nginx)"
		}
		a.showResult(title, r, func(a *App) tea.Cmd {
			if exitAfter && r.Err == nil {
				return a.closeEditor()
			}
			return nil
		})
		return nil
	})
}

// ---------------------------------------------------------------------------
// external nano

func (a *App) editorCommand(path string) *exec.Cmd {
	if v := os.Getenv("HNT_EDITOR"); v != "" {
		f := strings.Fields(v)
		return exec.Command(f[0], append(f[1:], path)...)
	}
	return exec.Command("nano", "--softwrap", "--nowrap", "--tabsize=4", "--linenumbers", path)
}

func (a *App) openNano(t *editTarget) tea.Cmd {
	dir, err := os.MkdirTemp("", "hnt-")
	if err != nil {
		a.setToast(err.Error(), true)
		return nil
	}
	name := t.filename
	if name == "" {
		name = "edit.conf"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(t.text), 0o600); err != nil {
		os.RemoveAll(dir)
		a.setToast(err.Error(), true)
		return nil
	}
	return tea.ExecProcess(a.editorCommand(path), func(err error) tea.Msg {
		return nanoDoneMsg{tmpdir: dir, path: path, orig: t.text, target: t, err: err}
	})
}

func (a *App) nanoDone(m nanoDoneMsg) tea.Cmd {
	defer os.RemoveAll(m.tmpdir)
	if m.err != nil {
		a.setToast("editor exited with error: "+m.err.Error(), true)
		return nil
	}
	b, err := os.ReadFile(m.path)
	if err != nil {
		a.setToast(err.Error(), true)
		return nil
	}
	text := string(b)
	if text == m.orig {
		a.setToast("no changes", false)
		return nil
	}
	t := m.target
	if t.save != nil {
		if err := t.save(text); err != nil {
			a.showResult("Could not save", asyncResult{Err: err}, nil)
			return nil
		}
		a.reloadSites(a.sv.currentDomain())
		if t.buildApply == nil {
			a.setToast("saved", false)
		}
	}
	if t.buildApply == nil {
		return nil
	}
	label := "Saved. Apply now (nginx -t, then reload)?"
	if t.save == nil {
		label = "Validate with nginx -t, save and reload nginx?"
	}
	a.popup = &confirmPopup{title: "Apply changes", body: label, yes: "save & apply", onYes: func(a *App) tea.Cmd {
		work := t.buildApply(a, text)
		return a.async("Applying…", work, func(a *App, r asyncResult) tea.Cmd {
			a.reloadSites(a.sv.currentDomain())
			a.reloadUpstreams()
			title := "Applied"
			if r.Err != nil {
				title = "Apply failed"
			}
			a.showResult(title, r, nil)
			return nil
		})
	}}
	return nil
}

// AppendForDemo appends text at the end of the open editor buffer. It exists
// for the screenshot generator (scripts/screenshots) only.
func (a *App) AppendForDemo(text string) {
	if a.mode != modeEditor {
		return
	}
	a.ed.r = len(a.ed.lines) - 1
	a.ed.c = len(a.ed.lines[a.ed.r])
	a.ed.snapshot("")
	a.ed.insertString(text)
	a.ed.fix()
}
