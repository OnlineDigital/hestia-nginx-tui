// Command screenshots renders the TUI screens used in the README as ANSI
// files (docs/screenshots/*.ans) against the fake Hestia from
// scripts/sandbox.sh. Run through scripts/screenshots.sh.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/onlinedigital/hestia-nginx-tui/internal/hestia"
	"github.com/onlinedigital/hestia-nginx-tui/internal/tui"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// pump feeds the messages produced by a command back into the model.
func pump(a *tui.App, cmd tea.Cmd) {
	var run func(c tea.Cmd, depth int)
	run = func(c tea.Cmd, depth int) {
		if c == nil || depth > 20 {
			return
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		select {
		case m := <-done:
			switch m := m.(type) {
			case tea.BatchMsg:
				for _, sub := range m {
					run(sub, depth+1)
				}
			case nil:
			default:
				if fmt.Sprintf("%T", m) == "spinner.TickMsg" {
					return
				}
				_, next := a.Update(m)
				run(next, depth+1)
			}
		case <-time.After(150 * time.Millisecond):
		}
	}
	run(cmd, 0)
}

func send(a *tui.App, msgs ...tea.Msg) {
	for _, m := range msgs {
		_, cmd := a.Update(m)
		pump(a, cmd)
	}
}

func typeText(a *tui.App, s string) {
	for _, r := range s {
		send(a, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func key(t tea.KeyType) tea.Msg { return tea.KeyMsg{Type: t} }

func save(dir, name string, a *tui.App) {
	must(os.WriteFile(filepath.Join(dir, name+".ans"), []byte(a.View()), 0o644))
	fmt.Println("wrote", name)
}

func main() {
	out := "docs/screenshots"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	must(os.MkdirAll(out, 0o755))
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)

	env, err := hestia.LoadEnv()
	must(err)

	// ---- prepare a lived-in server -------------------------------------
	sites, _ := env.LoadSites()
	byDomain := map[string]hestia.Site{}
	for _, s := range sites {
		byDomain[s.Domain] = s
	}
	_, err = env.SaveUpstream("app_backend", "# managed by hestia-nginx-tui - use it as: proxy_pass http://app_backend;\n"+
		"upstream app_backend {\n\tleast_conn;\n\tserver 10.0.0.5:8080 weight=2;\n\tserver 10.0.0.6:8080;\n\tserver 10.0.0.7:8080 backup;\n\tkeepalive 32;\n\tkeepalive_timeout 60s;\n}\n")
	must(err)
	_, err = env.SaveUpstream("node_ws", "upstream node_ws {\n\tserver 127.0.0.1:3000;\n\tkeepalive 16;\n}\n")
	must(err)
	shop := byDomain["shop.example.com"]
	must(env.ReseedLocations(shop, "laravel"))
	src, _ := env.ReadLocations(shop.Domain)
	must(env.SaveLocations(shop, src+"\nlocation /api/ {\n\tproxy_pass http://app_backend;\n\tproxy_http_version 1.1;\n\tproxy_set_header Connection \"\";\n\tproxy_set_header Host $host;\n}\n"))
	_, err = env.ApplyCustom(shop, "laravel")
	must(err)
	crm := byDomain["bobs-crm.test"]
	_, err = env.ApplyCustom(crm, "laravel")
	must(err)

	// ---- 1. site list ----------------------------------------------------
	a := tui.New(env)
	send(a, tea.WindowSizeMsg{Width: 124, Height: 30})
	for i := 0; i < 6; i++ { // land on shop.example.com (custom)
		send(a, key(tea.KeyDown))
	}
	save(out, "sites", a)

	// ---- 2. fuzzy search + action menu ------------------------------------
	a = tui.New(env)
	send(a, tea.WindowSizeMsg{Width: 124, Height: 30})
	typeText(a, "bob tui")
	send(a, key(tea.KeyEnter))
	save(out, "menu", a)

	// ---- 3. editor (soft wrap, nano keys) ---------------------------------
	a = tui.New(env)
	send(a, tea.WindowSizeMsg{Width: 124, Height: 34})
	typeText(a, "shop")
	send(a, key(tea.KeyEnter), key(tea.KeyDown), key(tea.KeyEnter))
	// scroll down to the php + /api/ part
	for i := 0; i < 90; i++ {
		send(a, key(tea.KeyDown))
	}
	save(out, "editor", a)

	// ---- 4. insert upstream (F2) ------------------------------------------
	send(a, tea.KeyMsg{Type: tea.KeyF2})
	save(out, "insert-upstream", a)

	// ---- 5. upstreams -----------------------------------------------------
	a = tui.New(env)
	send(a, tea.WindowSizeMsg{Width: 124, Height: 30})
	send(a, key(tea.KeyTab))
	save(out, "upstreams", a)

	// ---- 6. upstream form ---------------------------------------------------
	typeText(a, "n")
	typeText(a, "api_pool")
	send(a, key(tea.KeyTab))
	typeText(a, "10.0.0.21:9000, 10.0.0.22:9000 weight=2")
	save(out, "upstream-form", a)

	// ---- 7. apply result with automatic rollback ------------------------------
	a = tui.New(env)
	send(a, tea.WindowSizeMsg{Width: 124, Height: 34})
	typeText(a, "crm")
	send(a, key(tea.KeyEnter), key(tea.KeyDown), key(tea.KeyEnter))
	a.AppendForDemo("\nBADDIRECTIVE on;")
	send(a, key(tea.KeyCtrlS))
	save(out, "rollback", a)
}
