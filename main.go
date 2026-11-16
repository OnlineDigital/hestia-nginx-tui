// hestia-nginx-tui: interactive manager for per-site nginx locations and
// upstreams on a HestiaCP server. Run it as root.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/onlinedigital/hestia-nginx-tui/internal/hestia"
	"github.com/onlinedigital/hestia-nginx-tui/internal/tui"
	"github.com/onlinedigital/hestia-nginx-tui/internal/update"
)

// version is injected at release time (-ldflags "-X main.version=...").
var version = "dev"

const usage = `hestia-nginx-tui - HestiaCP nginx templates, custom locations and upstreams

usage:
  hestia-nginx-tui            start the interactive TUI (run as root)
  hestia-nginx-tui update     update this binary to the latest GitHub release
  hestia-nginx-tui version
  hestia-nginx-tui render     re-render every custom site's locations from its
                              .src file and reload nginx (use after a PHP version
                              change, which moves %backend_lsnr%)
  hestia-nginx-tui help

environment overrides (mostly for testing):
  HNT_HESTIA HNT_HOME HNT_STATE HNT_CONFD HNT_PHP_DIR HNT_NGINX_BIN HNT_RELOAD HNT_EDITOR
  HNT_NO_UPDATE_CHECK=1   do not look for a newer release at startup
`

func main() {
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "help", "-h", "--help":
		os.Stdout.WriteString(usage)
		return
	case "version", "-v", "--version":
		fmt.Println("hestia-nginx-tui", version)
		return
	case "update":
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		res, err := update.Run(ctx, version, "")
		fmt.Print(res.Log)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	tui.Version = version

	env, err := hestia.LoadEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if cmd == "render" {
		log, err := env.RenderAll()
		fmt.Print(log)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if cmd != "" {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	p := tea.NewProgram(tui.New(env), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
