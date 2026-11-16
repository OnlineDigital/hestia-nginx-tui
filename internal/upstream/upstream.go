// Package upstream models an nginx `upstream { }` block: generate it from a
// form, and parse an existing (possibly hand-edited) block back without
// losing any directive it does not understand.
package upstream

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Methods are the load-balancing methods offered by the form.
var Methods = []string{"round-robin", "least_conn", "ip_hash", "random"}

type Server struct {
	Addr string // 10.0.0.5:8080, [::1]:9000, unix:/run/app.sock, host:80
	Opts string // weight=2 max_fails=3 backup ...
}

type Upstream struct {
	Name              string
	Servers           []Server
	Method            string // one of Methods; "" = round-robin
	Keepalive         int    // 0 = omit
	KeepaliveTimeout  string // e.g. 60s, empty = omit
	KeepaliveRequests int    // 0 = omit
	// Extra holds every other line of the block, verbatim, so a hand-edited
	// upstream round-trips through the form without losing anything.
	Extra []string
	// Complex is set when the file contains more than one top-level
	// statement; such files are only editable as raw text.
	Complex bool
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// ValidName reports whether s is usable as an upstream name.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// Validate performs only the checks that would corrupt the generated file.
// Everything else is left to `nginx -t`, so nothing is restricted needlessly.
func (u Upstream) Validate() error {
	if !ValidName(u.Name) {
		return fmt.Errorf("name %q: use letters, digits, _ . - (no spaces)", u.Name)
	}
	if len(u.Servers) == 0 {
		return fmt.Errorf("add at least one server (ip:port)")
	}
	for _, s := range u.Servers {
		if s.Addr == "" || strings.ContainsAny(s.Addr+s.Opts, ";{}\n\"'") {
			return fmt.Errorf("server %q contains invalid characters", s.Addr)
		}
	}
	return nil
}

// ParseServers parses "10.0.0.1:8080, 10.0.0.2:8080 weight=2 backup".
func ParseServers(in string) []Server {
	var out []Server
	for _, part := range strings.Split(in, ",") {
		f := strings.Fields(part)
		if len(f) == 0 {
			continue
		}
		out = append(out, Server{Addr: f[0], Opts: strings.Join(f[1:], " ")})
	}
	return out
}

// FormatServers is the inverse of ParseServers.
func FormatServers(s []Server) string {
	parts := make([]string, len(s))
	for i, sv := range s {
		parts[i] = strings.TrimSpace(sv.Addr + " " + sv.Opts)
	}
	return strings.Join(parts, ", ")
}

const header = "# managed by hestia-nginx-tui - use it as: proxy_pass http://%s;"

// Render generates the nginx block.
func (u Upstream) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, header+"\n", u.Name)
	fmt.Fprintf(&b, "upstream %s {\n", u.Name)
	switch u.Method {
	case "least_conn", "ip_hash", "random":
		fmt.Fprintf(&b, "\t%s;\n", u.Method)
	}
	for _, s := range u.Servers {
		fmt.Fprintf(&b, "\tserver %s;\n", strings.TrimSpace(s.Addr+" "+s.Opts))
	}
	if u.Keepalive > 0 {
		fmt.Fprintf(&b, "\tkeepalive %d;\n", u.Keepalive)
	}
	if u.KeepaliveTimeout != "" {
		fmt.Fprintf(&b, "\tkeepalive_timeout %s;\n", u.KeepaliveTimeout)
	}
	if u.KeepaliveRequests > 0 {
		fmt.Fprintf(&b, "\tkeepalive_requests %d;\n", u.KeepaliveRequests)
	}
	for _, l := range u.Extra {
		fmt.Fprintf(&b, "\t%s\n", strings.TrimSpace(l))
	}
	b.WriteString("}\n")
	return b.String()
}

var (
	upHeadRe = regexp.MustCompile(`(?m)^\s*upstream\s+([^\s{]+)\s*\{`)
	kaRe     = regexp.MustCompile(`^keepalive\s+(\d+)\s*;`)
	kaTimeRe = regexp.MustCompile(`^keepalive_timeout\s+([^;\s]+)\s*;`)
	kaReqRe  = regexp.MustCompile(`^keepalive_requests\s+(\d+)\s*;`)
	srvRe    = regexp.MustCompile(`^server\s+([^;]*);`)
)

// Parse reads a block produced by Render or edited by hand. Unknown lines
// are kept in Extra. ok is false when no upstream block is present.
func Parse(text string) (Upstream, bool) {
	loc := upHeadRe.FindStringSubmatchIndex(text)
	if loc == nil {
		return Upstream{}, false
	}
	u := Upstream{Name: text[loc[2]:loc[3]]}
	// find the matching close brace
	depth, end := 1, -1
	for i := loc[1]; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return Upstream{}, false
	}
	// Anything meaningful before/after the block makes the file "complex".
	for _, part := range []string{text[:loc[0]], text[end+1:]} {
		for _, l := range strings.Split(part, "\n") {
			l = strings.TrimSpace(l)
			if l != "" && !strings.HasPrefix(l, "#") {
				u.Complex = true
			}
		}
	}
	for _, raw := range strings.Split(text[loc[1]:end], "\n") {
		l := strings.TrimSpace(raw)
		if l == "" {
			continue
		}
		switch {
		case l == "least_conn;" || l == "ip_hash;" || l == "random;":
			u.Method = strings.TrimSuffix(l, ";")
		case kaRe.MatchString(l):
			u.Keepalive, _ = strconv.Atoi(kaRe.FindStringSubmatch(l)[1])
		case kaTimeRe.MatchString(l):
			u.KeepaliveTimeout = kaTimeRe.FindStringSubmatch(l)[1]
		case kaReqRe.MatchString(l):
			u.KeepaliveRequests, _ = strconv.Atoi(kaReqRe.FindStringSubmatch(l)[1])
		case srvRe.MatchString(l):
			f := strings.Fields(srvRe.FindStringSubmatch(l)[1])
			if len(f) > 0 {
				u.Servers = append(u.Servers, Server{Addr: f[0], Opts: strings.Join(f[1:], " ")})
			}
		case strings.HasPrefix(l, "# managed by hestia-nginx-tui"):
			// our own header, regenerated
		default:
			u.Extra = append(u.Extra, l)
		}
	}
	return u, true
}

// Snippets are the text blocks inserted into the editor. They are inserted as
// plain text: nothing about them is locked, edit freely afterwards.
type SnippetKind int

const (
	SnipProxyPass SnippetKind = iota // just the proxy_pass line
	SnipKeepalive                    // proxy_pass + the directives keepalive needs
	SnipLocation                     // a full location block
)

var SnippetNames = []string{
	"proxy_pass only",
	"proxy_pass + keepalive & proxy headers",
	"complete location block",
}

// Snippet builds the text to insert for an upstream name.
func Snippet(name string, kind SnippetKind) string {
	pass := "proxy_pass http://" + name + ";"
	keep := []string{
		pass,
		"proxy_http_version 1.1;",
		`proxy_set_header Connection "";`,
		"proxy_set_header Host $host;",
		"proxy_set_header X-Real-IP $remote_addr;",
		"proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;",
		"proxy_set_header X-Forwarded-Proto $scheme;",
	}
	switch kind {
	case SnipKeepalive:
		return strings.Join(keep, "\n")
	case SnipLocation:
		var b strings.Builder
		b.WriteString("location / {\n")
		for _, l := range keep {
			b.WriteString("\t" + l + "\n")
		}
		b.WriteString("}")
		return b.String()
	}
	return pass
}
