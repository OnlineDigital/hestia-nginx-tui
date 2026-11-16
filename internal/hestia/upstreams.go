package hestia

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/onlinedigital/hestia-nginx-tui/internal/upstream"
)

// Each upstream lives in its own file, <StateDir>/upstreams/<name>.conf, and a
// single glob include in nginx's http{} (conf.d) pulls them all in. Because the
// files are plain nginx text they can be edited freely.

func (e *Env) upstreamIncludeFile() string {
	return filepath.Join(e.NginxConfD, "hestia-nginx-tui-upstreams.conf")
}

func (e *Env) UpstreamPath(name string) string {
	return filepath.Join(e.UpstreamDir(), name+".conf")
}

// UpstreamEntry is one stored upstream.
type UpstreamEntry struct {
	upstream.Upstream
	Path   string
	Text   string
	Parsed bool     // false if the file has no recognisable upstream block
	UsedBy []string // domains whose locations reference it
}

// ListUpstreams loads every stored upstream and finds where it is used.
func (e *Env) ListUpstreams() ([]UpstreamEntry, error) {
	files, _ := filepath.Glob(filepath.Join(e.UpstreamDir(), "*.conf"))
	sort.Strings(files)
	used := e.upstreamUsage()
	var out []UpstreamEntry
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(f), ".conf")
		ent := UpstreamEntry{Path: f, Text: string(b)}
		if u, ok := upstream.Parse(string(b)); ok {
			ent.Upstream, ent.Parsed = u, true
		} else {
			ent.Upstream = upstream.Upstream{Name: name, Complex: true}
		}
		ent.UsedBy = used[ent.Name]
		out = append(out, ent)
	}
	return out, nil
}

var passRe = regexp.MustCompile(`(?m)^\s*(?:proxy|fastcgi|grpc|uwsgi|scgi)_pass\s+(?:[a-z]+://)?([A-Za-z0-9_.-]+)\s*[;/]`)

// upstreamUsage maps upstream name -> domains referencing it in their locations.
func (e *Env) upstreamUsage() map[string][]string {
	out := map[string][]string{}
	dirs, _ := os.ReadDir(e.SitesDir())
	for _, d := range dirs {
		b, err := os.ReadFile(e.SrcPath(d.Name()))
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, m := range passRe.FindAllStringSubmatch(string(b), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out[m[1]] = append(out[m[1]], d.Name())
			}
		}
	}
	return out
}

func (e *Env) ensureUpstreamInclude() error {
	want := "# managed by hestia-nginx-tui\ninclude " + e.UpstreamDir() + "/*.conf;\n"
	if b, err := os.ReadFile(e.upstreamIncludeFile()); err == nil && string(b) == want {
		return nil
	}
	return writeFileAtomic(e.upstreamIncludeFile(), []byte(want), 0o644)
}

// SaveUpstream writes (or replaces) the upstream file, validates the whole
// nginx config and reloads. On any failure the previous state is restored.
// text is the full file content.
func (e *Env) SaveUpstream(name, text string) (string, error) {
	if !upstream.ValidName(name) {
		return "", fmt.Errorf("invalid upstream name %q", name)
	}
	if _, ok := upstream.Parse(text); !ok {
		return "", errors.New("no `upstream name { ... }` block in the text")
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	path := e.UpstreamPath(name)
	prev, hadPrev := []byte(nil), false
	if b, err := os.ReadFile(path); err == nil {
		prev, hadPrev = b, true
	}
	restore := func() {
		if hadPrev {
			_ = writeFileAtomic(path, prev, 0o644)
		} else {
			_ = os.Remove(path)
		}
	}
	if err := e.ensureUpstreamInclude(); err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	return e.validateAndReload(restore, "upstream "+name+" saved")
}

// DeleteUpstream removes an upstream; if nginx still needs it (a location
// references it) the deletion is rolled back.
func (e *Env) DeleteUpstream(name string) (string, error) {
	if !upstream.ValidName(name) {
		return "", fmt.Errorf("invalid upstream name %q", name)
	}
	path := e.UpstreamPath(name)
	prev, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	restore := func() { _ = writeFileAtomic(path, prev, 0o644) }
	return e.validateAndReload(restore, "upstream "+name+" deleted")
}

func (e *Env) validateAndReload(restore func(), okMsg string) (string, error) {
	var log strings.Builder
	l, err := e.NginxTest()
	log.WriteString(l)
	if err != nil {
		restore()
		log.WriteString("✗ nginx -t failed: previous state restored, nothing changed\n")
		return log.String(), errors.New("nginx rejected the configuration")
	}
	l, err = e.NginxReload()
	log.WriteString(l)
	if err != nil {
		return log.String(), errors.New("nginx reload failed")
	}
	log.WriteString("✓ " + okMsg + " (nginx reloaded)\n")
	return log.String(), nil
}
