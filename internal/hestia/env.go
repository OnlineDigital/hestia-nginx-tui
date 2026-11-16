// Package hestia talks to a HestiaCP installation: it lists sites, builds
// custom nginx templates and applies them through the official v-* commands.
package hestia

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Env holds every path/command the tool depends on. All of them can be
// overridden through HNT_* environment variables (used for sandboxed tests).
type Env struct {
	HestiaDir  string // /usr/local/hestia
	HomeDir    string // /home
	StateDir   string // /etc/hestia-nginx-tui
	NginxConfD string // /etc/nginx/conf.d
	PHPDir     string // /etc/php
	NginxBin   string // nginx
	ReloadCmd  string // optional shell command overriding the reload step

	WebSystem   string // nginx | apache2
	WebBackend  string // php-fpm | ""
	ProxySystem string
	WebPort     string
	WebSSLPort  string

	Sandbox bool // true when any HNT_* override is active (skips the root check)
}

var kvRe = regexp.MustCompile(`([A-Za-z0-9_]+)='([^']*)'`)

// ParseKV parses Hestia's KEY='value' KEY2='value 2' format.
func ParseKV(line string) map[string]string {
	out := map[string]string{}
	for _, m := range kvRe.FindAllStringSubmatch(line, -1) {
		out[m[1]] = m[2]
	}
	return out
}

func readConf(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `'"`)
	}
	return out
}

func envOr(key, def string) (string, bool) {
	if v := os.Getenv(key); v != "" {
		return v, true
	}
	return def, false
}

// LoadEnv discovers the Hestia installation and returns the environment.
func LoadEnv() (*Env, error) {
	e := &Env{}
	var ov bool
	var o bool

	global := readConf("/etc/hestiacp/hestia.conf")
	hdef := global["HESTIA"]
	if hdef == "" {
		hdef = "/usr/local/hestia"
	}
	e.HestiaDir, o = envOr("HNT_HESTIA", hdef)
	ov = ov || o
	e.StateDir, o = envOr("HNT_STATE", "/etc/hestia-nginx-tui")
	ov = ov || o
	e.NginxConfD, o = envOr("HNT_CONFD", "/etc/nginx/conf.d")
	ov = ov || o
	e.PHPDir, o = envOr("HNT_PHP_DIR", "/etc/php")
	ov = ov || o
	e.NginxBin, o = envOr("HNT_NGINX_BIN", "nginx")
	ov = ov || o
	e.ReloadCmd = os.Getenv("HNT_RELOAD")
	ov = ov || e.ReloadCmd != ""
	e.Sandbox = ov

	conf := readConf(filepath.Join(e.HestiaDir, "conf", "hestia.conf"))
	home := conf["HOMEDIR"]
	if home == "" {
		home = global["HOMEDIR"]
	}
	if home == "" {
		home = "/home"
	}
	e.HomeDir, o = envOr("HNT_HOME", home)
	e.Sandbox = e.Sandbox || o
	e.WebSystem = conf["WEB_SYSTEM"]
	e.WebBackend = conf["WEB_BACKEND"]
	e.ProxySystem = conf["PROXY_SYSTEM"]
	e.WebPort = conf["WEB_PORT"]
	e.WebSSLPort = conf["WEB_SSL_PORT"]
	if e.WebPort == "" {
		e.WebPort = "80"
	}
	if e.WebSSLPort == "" {
		e.WebSSLPort = "443"
	}

	if st, err := os.Stat(filepath.Join(e.HestiaDir, "data", "users")); err != nil || !st.IsDir() {
		return e, fmt.Errorf("HestiaCP not found at %s (set HNT_HESTIA to override)", e.HestiaDir)
	}
	if e.WebSystem != "nginx" {
		return e, fmt.Errorf("WEB_SYSTEM is %q: only the \"nginx\" (nginx + php-fpm) web system is supported", e.WebSystem)
	}
	return e, nil
}

// ---------------------------------------------------------------------------
// paths

func (e *Env) BinDir() string { return filepath.Join(e.HestiaDir, "bin") }

// TemplateDir is where Hestia keeps the nginx web templates, e.g.
// /usr/local/hestia/data/templates/web/nginx/php-fpm.
func (e *Env) TemplateDir() string {
	d := filepath.Join(e.HestiaDir, "data", "templates", "web", e.WebSystem)
	if e.WebBackend != "" {
		d = filepath.Join(d, e.WebBackend)
	}
	return d
}

func (e *Env) SitesDir() string        { return filepath.Join(e.StateDir, "sites") }
func (e *Env) UpstreamDir() string     { return filepath.Join(e.StateDir, "upstreams") }
func (e *Env) SiteDir(d string) string { return filepath.Join(e.SitesDir(), d) }

// SrcPath is the file the user edits (contains %placeholders%).
func (e *Env) SrcPath(domain string) string { return filepath.Join(e.SiteDir(domain), "locations.src") }

// LocPath is the rendered file included by the generated template.
func (e *Env) LocPath(domain string) string {
	return filepath.Join(e.SiteDir(domain), "locations.conf")
}

func (e *Env) HistoryDir(domain string) string { return filepath.Join(e.SiteDir(domain), "history") }

// DomainConfPaths are the files Hestia generates for a domain.
func (e *Env) DomainConfPaths(s Site) (http, https string) {
	d := filepath.Join(e.HomeDir, s.User, "conf", "web", s.Domain)
	return filepath.Join(d, "nginx.conf"), filepath.Join(d, "nginx.ssl.conf")
}

// ---------------------------------------------------------------------------
// validation (these values end up in file paths and command arguments)

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func ValidName(s string) bool { return safeName.MatchString(s) && !strings.Contains(s, "..") }

// ---------------------------------------------------------------------------
// command execution

// Run executes a command, returning a transcript ("$ cmd\noutput") and an error.
func (e *Env) Run(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "PATH="+e.BinDir()+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	log := "$ " + name + " " + strings.Join(args, " ") + "\n" + strings.TrimRight(string(out), "\n")
	if ctx.Err() == context.DeadlineExceeded {
		err = errors.New("command timed out")
	}
	return strings.TrimRight(log, "\n") + "\n", err
}

// NginxTest runs `nginx -t`.
func (e *Env) NginxTest() (string, error) { return e.Run(30*time.Second, e.NginxBin, "-t") }

// NginxReload reloads nginx (systemctl first, `nginx -s reload` as fallback).
func (e *Env) NginxReload() (string, error) {
	if e.ReloadCmd != "" {
		return e.Run(30*time.Second, "sh", "-c", e.ReloadCmd)
	}
	log, err := e.Run(30*time.Second, "systemctl", "reload", "nginx")
	if err == nil {
		return log, nil
	}
	l2, err2 := e.Run(30*time.Second, e.NginxBin, "-s", "reload")
	return log + l2, err2
}

// ---------------------------------------------------------------------------
// atomic file helpers

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
