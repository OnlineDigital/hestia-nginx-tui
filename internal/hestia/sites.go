package hestia

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Site is one web domain of one Hestia user.
type Site struct {
	User      string
	Domain    string
	IP        string
	Aliases   string
	Template  string // current TPL value, e.g. "laravel" or "tui-laravel"
	Backend   string // e.g. PHP-8_2
	SSL       bool
	Suspended bool
	Raw       map[string]string
}

// IsCustom reports whether a template is the generated "tui" one.
func IsCustom(tpl string) bool { return tpl == CustomTemplate }

// SearchText is the haystack the fuzzy finder runs on.
func (s Site) SearchText() string { return s.Domain + " " + s.User + " " + s.Template }

// LoadSites reads every user's web.conf.
func (e *Env) LoadSites() ([]Site, error) {
	usersDir := filepath.Join(e.HestiaDir, "data", "users")
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		return nil, err
	}
	var sites []Site
	for _, u := range entries {
		if !u.IsDir() && u.Type()&os.ModeSymlink == 0 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(usersDir, u.Name(), "web.conf"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			kv := ParseKV(line)
			d := kv["DOMAIN"]
			if d == "" || !ValidName(d) || !ValidName(u.Name()) {
				continue
			}
			sites = append(sites, Site{
				User:      u.Name(),
				Domain:    d,
				IP:        kv["IP"],
				Aliases:   kv["ALIAS"],
				Template:  kv["TPL"],
				Backend:   kv["BACKEND"],
				SSL:       kv["SSL"] == "yes",
				Suspended: kv["SUSPENDED"] == "yes",
				Raw:       kv,
			})
		}
	}
	sort.SliceStable(sites, func(i, j int) bool { return sites[i].Domain < sites[j].Domain })
	return sites, nil
}

// HasCustomLocations reports whether the site has a locations file, and when
// it was last modified.
func (e *Env) HasCustomLocations(domain string) (bool, time.Time) {
	st, err := os.Stat(e.SrcPath(domain))
	if err != nil {
		return false, time.Time{}
	}
	return true, st.ModTime()
}

// ReadLocations returns the saved editable locations of a site.
func (e *Env) ReadLocations(domain string) (string, bool) {
	b, err := os.ReadFile(e.SrcPath(domain))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// SaveLocations stores the editable locations, keeping the previous version
// in history/ (last 20 kept).
func (e *Env) SaveLocations(site Site, text string) error {
	if !ValidName(site.Domain) {
		return fmt.Errorf("invalid domain %q", site.Domain)
	}
	if prev, err := os.ReadFile(e.SrcPath(site.Domain)); err == nil && string(prev) != text {
		hd := e.HistoryDir(site.Domain)
		if err := os.MkdirAll(hd, 0o755); err == nil {
			name := filepath.Join(hd, "locations-"+time.Now().Format("20060102-150405")+".src")
			_ = os.WriteFile(name, prev, 0o644)
			pruneHistory(hd, 20)
		}
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return writeFileAtomic(e.SrcPath(site.Domain), []byte(text), 0o644)
}

func pruneHistory(dir string, keep int) {
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) <= keep {
		return
	}
	names := make([]string, 0, len(ents))
	for _, en := range ents {
		names = append(names, en.Name())
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-keep] {
		_ = os.Remove(filepath.Join(dir, n))
	}
}

// BackendListen resolves %backend_lsnr% for a site from its php-fpm pool.
func (e *Env) BackendListen(s Site) string {
	var candidates []string
	if v := strings.TrimPrefix(s.Backend, "PHP-"); v != s.Backend {
		candidates = append(candidates, filepath.Join(e.PHPDir, strings.ReplaceAll(v, "_", "."), "fpm", "pool.d", s.Domain+".conf"))
	}
	if g, _ := filepath.Glob(filepath.Join(e.PHPDir, "*", "fpm", "pool.d", s.Domain+".conf")); len(g) > 0 {
		candidates = append(candidates, g...)
	}
	for _, c := range candidates {
		cfg := readConf(c)
		if l := cfg["listen"]; l != "" {
			if strings.Contains(l, "/") {
				return "unix:" + l
			}
			return l
		}
		// php-fpm uses "listen = value" with spaces; readConf trims them already.
	}
	return "unix:/run/php/php-fpm-" + s.Domain + ".sock"
}

// Render substitutes Hestia's %placeholders% for one site.
func (e *Env) Render(s Site, text string) string {
	docroot := filepath.Join(e.HomeDir, s.User, "web", s.Domain, "public_html")
	sdocroot := docroot
	if s.Raw["SSL_HOME"] == "single" {
		sdocroot = filepath.Join(e.HomeDir, s.User, "web", s.Domain, "public_shtml")
	}
	idn := s.Domain
	aliases := strings.ReplaceAll(s.Aliases, ",", " ")
	r := strings.NewReplacer(
		"%home%", e.HomeDir,
		"%user%", s.User,
		"%domain_idn%", idn,
		"%domain%", s.Domain,
		"%alias_idn%", aliases,
		"%docroot%", docroot,
		"%sdocroot%", sdocroot,
		"%backend_lsnr%", e.BackendListen(s),
		"%ip%", s.IP,
		"%web_port%", e.WebPort,
		"%web_ssl_port%", e.WebSSLPort,
		"%proxy_extensions%", strings.ReplaceAll(s.Raw["PROXY_EXT"], ",", "|"),
	)
	return r.Replace(text)
}

// Placeholders lists what Render understands (shown in the editor help).
var Placeholders = []string{
	"%domain%", "%domain_idn%", "%alias_idn%", "%user%", "%home%", "%docroot%", "%sdocroot%",
	"%backend_lsnr%", "%ip%", "%web_port%", "%web_ssl_port%", "%proxy_extensions%",
}
