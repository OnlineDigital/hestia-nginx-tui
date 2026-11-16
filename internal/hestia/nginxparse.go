package hestia

import (
	"errors"
	"regexp"
	"strings"
)

// Stmt is one nginx statement (a directive ending in ';' or a block).
type Stmt struct {
	Start, End int // byte span of the whole statement in the source text
	Name, Arg  string
	Block      bool
	BodyStart  int // index just after '{' (blocks only)
	BodyEnd    int // index of the matching '}' (blocks only)
}

// nextStmt scans the next statement in text[i:limit]. It understands
// comments, quoted strings and nested braces.
func nextStmt(text string, i, limit int) (Stmt, int, bool) {
	// skip whitespace and comments
	for i < limit {
		c := text[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
		} else if c == '#' {
			for i < limit && text[i] != '\n' {
				i++
			}
		} else {
			break
		}
	}
	if i >= limit {
		return Stmt{}, i, false
	}
	st := Stmt{Start: i}
	head := i
	for i < limit {
		c := text[i]
		switch c {
		case '#':
			for i < limit && text[i] != '\n' {
				i++
			}
		case '"', '\'':
			q := c
			i++
			for i < limit && text[i] != q {
				if text[i] == '\\' {
					i++
				}
				i++
			}
			i++
		case ';':
			st.End = i + 1
			fillHead(&st, text[head:i])
			return st, st.End, true
		case '{':
			st.Block = true
			st.BodyStart = i + 1
			fillHead(&st, text[head:i])
			depth := 1
			i++
			for i < limit && depth > 0 {
				switch text[i] {
				case '#':
					for i < limit && text[i] != '\n' {
						i++
					}
					continue
				case '"', '\'':
					q := text[i]
					i++
					for i < limit && text[i] != q {
						if text[i] == '\\' {
							i++
						}
						i++
					}
				case '{':
					depth++
				case '}':
					depth--
				}
				i++
			}
			st.BodyEnd = i - 1
			st.End = i
			return st, st.End, true
		case '}':
			// stray closing brace: stop
			return Stmt{}, i, false
		default:
			i++
		}
	}
	return Stmt{}, i, false
}

func fillHead(st *Stmt, head string) {
	head = strings.TrimSpace(head)
	f := strings.Fields(head)
	if len(f) > 0 {
		st.Name = f[0]
		st.Arg = strings.TrimSpace(strings.TrimPrefix(head, f[0]))
	}
}

// Statements returns the direct child statements of text[from:to].
func Statements(text string, from, to int) []Stmt {
	var out []Stmt
	i := from
	for {
		st, ni, ok := nextStmt(text, i, to)
		if !ok {
			break
		}
		out = append(out, st)
		i = ni
	}
	return out
}

// FindServer returns the first top-level server block.
func FindServer(text string) (Stmt, error) {
	for _, st := range Statements(text, 0, len(text)) {
		if st.Name == "server" && st.Block {
			return st, nil
		}
	}
	return Stmt{}, errors.New("no server { } block found")
}

// keepInTemplate reports whether a location is Hestia infrastructure that must
// stay in the template (error pages and web stats) rather than become part of
// the per-site editable snippet.
func keepInTemplate(st Stmt) bool {
	f := strings.Fields(st.Arg)
	if len(f) == 0 {
		return false
	}
	last := f[len(f)-1]
	return last == "/error/" || last == "/vstats/"
}

// plumbing are the standard Hestia includes that belong to the fixed skeleton.
var plumbing = []string{"nginx.forcessl.conf", "nginx.hsts.conf", "phpmyadmin.inc", "phppgadmin.inc", "nginx.conf_", "nginx.ssl.conf_"}

// extractable reports whether a direct child of the server block is
// site-specific (root, index, locations, extra directives) as opposed to the
// fixed Hestia skeleton (listen, server_name, logs, ssl, standard includes).
func extractable(st Stmt) bool {
	switch st.Name {
	case "listen", "server_name", "access_log", "error_log", "http2":
		return false
	case "location":
		return !keepInTemplate(st)
	case "include":
		for _, p := range plumbing {
			if strings.Contains(st.Arg, p) {
				return false
			}
		}
		return true
	case "if":
		return !strings.Contains(st.Arg, "$anti_replay")
	}
	return !strings.HasPrefix(st.Name, "ssl_")
}

func lineStart(text string, i int) int {
	for i > 0 && text[i-1] != '\n' {
		// stop if non-whitespace precedes on the line
		if text[i-1] != ' ' && text[i-1] != '\t' {
			return i
		}
		i--
	}
	return i
}

func lineEnd(text string, i int) int {
	j := i
	for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
		j++
	}
	if j < len(text) && text[j] == '\n' {
		return j + 1
	}
	if j >= len(text) {
		return len(text)
	}
	return i
}

var multiBlank = regexp.MustCompile(`\n{3,}`)

// Split is the result of SplitServer.
type Split struct {
	Template string // the template with every site-specific statement replaced by the include
	Seed     string // the extracted statements, dedented, still holding %placeholders%
	Count    int    // number of extracted statements
}

// SplitServer separates a Hestia template into a fixed skeleton and the
// site-specific part. Every extractable statement of the first server block
// (root, index, locations other than /error/ and /vstats/, extra directives)
// is removed and replaced, at the position of the first one, by includeBlock.
func SplitServer(text, includeBlock string) (Split, error) {
	srv, err := FindServer(text)
	if err != nil {
		return Split{}, err
	}
	type span struct {
		s, e  int
		block bool
	}
	var spans []span
	var chunks []string
	for _, st := range Statements(text, srv.BodyStart, srv.BodyEnd) {
		if !extractable(st) {
			continue
		}
		s, e := lineStart(text, st.Start), lineEnd(text, st.End)
		spans = append(spans, span{s, e, st.Block})
		chunks = append(chunks, dedent(strings.TrimRight(text[s:e], "\n")))
	}
	var b strings.Builder
	if len(spans) == 0 {
		at := lineStart(text, srv.BodyEnd)
		b.WriteString(text[:at] + includeBlock + text[at:])
	} else {
		prev := 0
		for i, sp := range spans {
			b.WriteString(text[prev:sp.s])
			if i == 0 {
				b.WriteString(includeBlock)
			}
			prev = sp.e
		}
		b.WriteString(text[prev:])
	}
	var seed strings.Builder
	for i, c := range chunks {
		if i > 0 {
			if spans[i].block || spans[i-1].block {
				seed.WriteString("\n\n")
			} else {
				seed.WriteString("\n")
			}
		}
		seed.WriteString(c)
	}
	if len(chunks) > 0 {
		seed.WriteString("\n")
	}
	return Split{
		Template: multiBlank.ReplaceAllString(b.String(), "\n\n"),
		Seed:     seed.String(),
		Count:    len(spans),
	}, nil
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "\t"):
			lines[i] = l[1:]
		case strings.HasPrefix(l, "    "):
			lines[i] = l[4:]
		}
	}
	return strings.Join(lines, "\n")
}
