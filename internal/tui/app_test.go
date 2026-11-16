package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/onlinedigital/hestia-nginx-tui/internal/hestia"
)

func sandboxEnv(t *testing.T) *hestia.Env {
	t.Helper()
	root := t.TempDir()
	hd := filepath.Join(root, "hestia")
	home := filepath.Join(root, "home")
	state := filepath.Join(root, "state")
	tpl := filepath.Join(hd, "data/templates/web/nginx/php-fpm")
	for _, d := range []string{tpl, filepath.Join(hd, "bin"), filepath.Join(hd, "conf"), filepath.Join(hd, "data/users/admin"), filepath.Join(hd, "data/users/bob"), filepath.Join(root, "confd"), filepath.Join(root, "php")} {
		os.MkdirAll(d, 0o755)
	}
	w := func(p, s string, m os.FileMode) {
		if err := os.WriteFile(p, []byte(s), m); err != nil {
			t.Fatal(err)
		}
	}
	for _, ext := range []string{"tpl", "stpl"} {
		b, err := os.ReadFile("../hestia/testdata/laravel." + ext)
		if err != nil {
			t.Fatal(err)
		}
		w(filepath.Join(tpl, "laravel."+ext), string(b), 0o644)
		d, err := os.ReadFile("../hestia/testdata/default." + ext)
		if err != nil {
			t.Fatal(err)
		}
		w(filepath.Join(tpl, "default."+ext), string(d), 0o644)
	}
	w(filepath.Join(hd, "conf/hestia.conf"), "WEB_SYSTEM='nginx'\nWEB_BACKEND='php-fpm'\n", 0o644)
	w(filepath.Join(hd, "data/users/admin/web.conf"),
		"DOMAIN='shop.example.com' IP='10.0.0.1' ALIAS='www.shop.example.com' TPL='laravel' SSL='yes' BACKEND='PHP-8_2'\n"+
			"DOMAIN='blog.example.com' IP='10.0.0.1' ALIAS='' TPL='default' SSL='no' BACKEND='PHP-8_2'\n", 0o644)
	w(filepath.Join(hd, "data/users/bob/web.conf"), "DOMAIN='bob.test' IP='10.0.0.2' TPL='laravel' SSL='no' BACKEND='PHP-8_2'\n", 0o644)
	w(filepath.Join(hd, "bin/v-change-web-domain-tpl"), "#!/usr/bin/env bash\nsed -i \"/DOMAIN='$2'/ s/TPL='[^']*'/TPL='$3'/\" "+hd+"/data/users/$1/web.conf\necho \"Hestia: $2 -> $3 (restart=$4)\"\n", 0o755)
	w(filepath.Join(root, "nginx"), "#!/usr/bin/env bash\nif [ \"$1\" = -t ]; then if grep -rq BADDIRECTIVE "+state+" 2>/dev/null; then echo 'nginx: [emerg] unknown directive \"BADDIRECTIVE\" in locations.conf:3' >&2; exit 1; fi; echo 'nginx: configuration file test is successful'; fi\n", 0o755)
	t.Setenv("HNT_HESTIA", hd)
	t.Setenv("HNT_HOME", home)
	t.Setenv("HNT_STATE", state)
	t.Setenv("HNT_CONFD", filepath.Join(root, "confd"))
	t.Setenv("HNT_PHP_DIR", filepath.Join(root, "php"))
	t.Setenv("HNT_NGINX_BIN", filepath.Join(root, "nginx"))
	t.Setenv("HNT_RELOAD", "true")
	env, err := hestia.LoadEnv()
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// pump feeds a command's messages back into the model, dropping spinner ticks.
func pump(a *App, cmd tea.Cmd) {
	var run func(c tea.Cmd, depth int)
	run = func(c tea.Cmd, depth int) {
		if c == nil || depth > 20 {
			return
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		var msg tea.Msg
		select {
		case msg = <-done:
		case <-time.After(120 * time.Millisecond): // cursor blink timers etc.
			return
		}
		switch m := msg.(type) {
		case tea.BatchMsg:
			for _, sub := range m {
				run(sub, depth+1)
			}
		case spinner.TickMsg, nil:
		default:
			_, next := a.Update(m)
			run(next, depth+1)
		}
	}
	run(cmd, 0)
}

func send(a *App, msgs ...tea.Msg) {
	for _, m := range msgs {
		_, cmd := a.Update(m)
		pump(a, cmd)
	}
}

func typeInto(a *App, s string) {
	for _, r := range s {
		send(a, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func screen(a *App) string { return ansi.Strip(a.View()) }

func TestFullFlow(t *testing.T) {
	env := sandboxEnv(t)
	a := New(env)
	send(a, tea.WindowSizeMsg{Width: 130, Height: 36})

	s := screen(a)
	t.Logf("\n%s", s)
	for _, want := range []string{"shop.example.com", "blog.example.com", "bob.test", "3 sites", "Upstreams"} {
		if !strings.Contains(s, want) {
			t.Fatalf("sites screen lacks %q", want)
		}
	}
	if lines := strings.Split(a.View(), "\n"); len(lines) != 36 {
		t.Fatalf("view must be exactly the terminal height, got %d lines", len(lines))
	}

	// fuzzy search narrows the list across users
	typeInto(a, "bbt")
	s = screen(a)
	if !strings.Contains(s, "bob.test") || strings.Contains(s, "blog.example.com") {
		t.Fatalf("fuzzy filter failed:\n%s", s)
	}

	// Enter opens the action popup
	send(a, tea.KeyMsg{Type: tea.KeyEnter})
	s = screen(a)
	t.Logf("\n%s", s)
	if !strings.Contains(s, "Edit custom Nginx locations") || !strings.Contains(s, "Change template") {
		t.Fatalf("menu missing:\n%s", s)
	}

	// choose "Edit custom Nginx locations"
	send(a, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyEnter})
	if a.mode != modeEditor {
		t.Fatalf("should be in the editor, mode=%v popup=%T", a.mode, a.popup)
	}
	s = screen(a)
	t.Logf("\n%s", s)
	for _, want := range []string{"location = /favicon.ico", "Save+Apply", "nano-tui"} {
		if !strings.Contains(s, want) {
			t.Fatalf("editor lacks %q", want)
		}
	}

	if !strings.Contains(a.ed.Text(), "fastcgi_pass %backend_lsnr%;") {
		t.Fatal("seed should keep the Hestia placeholders")
	}
	// add a location at the end and Save & Apply with Ctrl+S
	send(a, tea.KeyMsg{Type: tea.KeyCtrlEnd})
	if a.ed.r == 0 {
		send(a, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}, Alt: true}) // no-op, keep going
	}
	a.ed.r, a.ed.c = len(a.ed.lines)-1, len(a.ed.lines[len(a.ed.lines)-1])
	send(a, tea.KeyMsg{Type: tea.KeyEnter})
	typeInto(a, "location /api/ {")
	send(a, tea.KeyMsg{Type: tea.KeyEnter})
	typeInto(a, "return 204;")
	send(a, tea.KeyMsg{Type: tea.KeyCtrlS})

	s = screen(a)
	t.Logf("\n%s", s)
	if !strings.Contains(s, "Applied") || !strings.Contains(s, "v-change-web-domain-tpl") {
		t.Fatalf("apply result popup missing:\n%s", s)
	}
	src, _ := env.ReadLocations("bob.test")
	if !strings.Contains(src, "location /api/") {
		t.Fatalf("edit not saved:\n%s", src)
	}
	loc, _ := os.ReadFile(env.LocPath("bob.test"))
	if !strings.Contains(string(loc), "return 204;") {
		t.Fatal("rendered locations missing the edit")
	}
	sites, _ := env.LoadSites()
	for _, st := range sites {
		if st.Domain == "bob.test" && st.Template != "tui" {
			t.Fatalf("template is %s", st.Template)
		}
	}

	// close popup, exit the editor (unmodified now) back to the list
	send(a, tea.KeyMsg{Type: tea.KeyEsc})
	send(a, tea.KeyMsg{Type: tea.KeyCtrlX})
	if a.mode != modeSites {
		t.Fatalf("expected to be back on the site list, mode=%v pm=%v modified=%v", a.mode, a.ed.pm, a.ed.Modified())
	}
	if !strings.Contains(screen(a), "◆ custom locations") {
		t.Fatalf("detail panel should flag the custom site:\n%s", screen(a))
	}
}

func TestBadEditShowsNginxErrorAndKeepsEditor(t *testing.T) {
	env := sandboxEnv(t)
	a := New(env)
	send(a, tea.WindowSizeMsg{Width: 120, Height: 34})
	typeInto(a, "bob")
	send(a, tea.KeyMsg{Type: tea.KeyEnter}, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyEnter})
	a.ed.r, a.ed.c = len(a.ed.lines)-1, len(a.ed.lines[len(a.ed.lines)-1])
	send(a, tea.KeyMsg{Type: tea.KeyEnter})
	typeInto(a, "BADDIRECTIVE on;")
	send(a, tea.KeyMsg{Type: tea.KeyCtrlS})
	s := screen(a)
	t.Logf("\n%s", s)
	if !strings.Contains(s, "BADDIRECTIVE") || !strings.Contains(s, "nothing was changed") {
		t.Fatalf("nginx error not surfaced:\n%s", s)
	}
	send(a, tea.KeyMsg{Type: tea.KeyEsc})
	if a.mode != modeEditor {
		t.Fatal("editor must stay open so the user can fix the text")
	}
	if !strings.Contains(a.ed.Text(), "BADDIRECTIVE") {
		t.Fatal("user text must not be lost")
	}
}

func TestUpstreamFormAndInsertSnippet(t *testing.T) {
	env := sandboxEnv(t)
	a := New(env)
	send(a, tea.WindowSizeMsg{Width: 130, Height: 36})
	send(a, tea.KeyMsg{Type: tea.KeyTab}) // upstreams tab
	if a.mode != modeUpstreams {
		t.Fatal("tab should switch to upstreams")
	}
	typeInto(a, "n")
	if a.mode != modeForm {
		t.Fatal("n opens the form")
	}
	typeInto(a, "app_backend")
	send(a, tea.KeyMsg{Type: tea.KeyTab})
	typeInto(a, "10.0.0.5:8080, 10.0.0.6:8080 weight=2")
	s := screen(a)
	t.Logf("\n%s", s)
	for _, want := range []string{"upstream app_backend {", "server 10.0.0.6:8080 weight=2;", "keepalive 32;", "ready to save"} {
		if !strings.Contains(s, want) {
			t.Fatalf("form preview lacks %q", want)
		}
	}
	send(a, tea.KeyMsg{Type: tea.KeyCtrlS})
	if !strings.Contains(screen(a), "Upstream saved") {
		t.Fatalf("save failed:\n%s", screen(a))
	}
	send(a, tea.KeyMsg{Type: tea.KeyEsc}) // close result
	s = screen(a)
	t.Logf("\n%s", s)
	if a.mode != modeUpstreams || !strings.Contains(s, "app_backend") {
		t.Fatalf("should list the new upstream:\n%s", s)
	}

	// use it from a site's editor with F2
	send(a, tea.KeyMsg{Type: tea.KeyTab}) // back to sites
	typeInto(a, "bob")
	send(a, tea.KeyMsg{Type: tea.KeyEnter}, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyEnter})
	a.ed.r, a.ed.c = len(a.ed.lines)-1, len(a.ed.lines[len(a.ed.lines)-1])
	send(a, tea.KeyMsg{Type: tea.KeyEnter})
	send(a, tea.KeyMsg{Type: tea.KeyF2})
	if !strings.Contains(screen(a), "Insert upstream") {
		t.Fatalf("F2 picker missing:\n%s", screen(a))
	}
	send(a, tea.KeyMsg{Type: tea.KeyEnter})                                // choose app_backend
	send(a, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyEnter}) // keepalive variant
	txt := a.ed.Text()
	if !strings.Contains(txt, "proxy_pass http://app_backend;") || !strings.Contains(txt, `proxy_set_header Connection "";`) {
		t.Fatalf("snippet not inserted:\n%s", txt)
	}
}

func TestSiteOnTuiWithoutBaseAsksForTemplate(t *testing.T) {
	env := sandboxEnv(t)
	// pretend bob.test already runs the tui template but nothing is known about its origin
	sites, _ := env.LoadSites()
	var bob hestia.Site
	for _, s := range sites {
		if s.Domain == "bob.test" {
			bob = s
		}
	}
	if _, err := env.ApplyCustom(bob, "laravel"); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(env.SiteDir("bob.test"))

	a := New(env)
	send(a, tea.WindowSizeMsg{Width: 120, Height: 34})
	typeInto(a, "bob")
	send(a, tea.KeyMsg{Type: tea.KeyEnter}, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(screen(a), "Pre-fill locations from") {
		t.Fatalf("expected the template picker:\n%s", screen(a))
	}
	typeInto(a, "laravel")
	send(a, tea.KeyMsg{Type: tea.KeyEnter})
	if a.mode != modeEditor || !strings.Contains(a.ed.Text(), "livewire") {
		t.Fatal("editor should open pre-filled from laravel")
	}
}

func TestChangeTemplateCustomKeepOrReplace(t *testing.T) {
	env := sandboxEnv(t)
	sites, _ := env.LoadSites()
	var shop hestia.Site
	for _, s := range sites {
		if s.Domain == "shop.example.com" {
			shop = s
		}
	}
	if _, err := env.ApplyCustom(shop, "laravel"); err != nil {
		t.Fatal(err)
	}
	if err := env.SaveLocations(shop, "root /srv/mine;\nlocation /mine/ { return 200; }\n"); err != nil {
		t.Fatal(err)
	}
	a := New(env)
	send(a, tea.WindowSizeMsg{Width: 120, Height: 34})
	typeInto(a, "shop")
	send(a, tea.KeyMsg{Type: tea.KeyEnter}, tea.KeyMsg{Type: tea.KeyEnter}) // Change template…
	if !strings.Contains(screen(a), "Mode: CUSTOM") {
		t.Fatalf("a tui site should start in custom mode:\n%s", screen(a))
	}
	typeInto(a, "default")
	send(a, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(screen(a), "Locations already exist") {
		t.Fatalf("must ask keep/replace:\n%s", screen(a))
	}
	send(a, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyEnter}) // replace
	src, _ := env.ReadLocations("shop.example.com")
	if strings.Contains(src, "/mine/") || !strings.Contains(src, "root        %docroot%;") {
		t.Fatalf("locations should now come from default:\n%s", src)
	}
	if env.ReadBase("shop.example.com") != "default" {
		t.Fatal("base should be updated")
	}
}
