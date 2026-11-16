package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+x":
		return tea.KeyMsg{Type: tea.KeyCtrlX}
	case "ctrl+o":
		return tea.KeyMsg{Type: tea.KeyCtrlO}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+w":
		return tea.KeyMsg{Type: tea.KeyCtrlW}
	case "alt+u":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}, Alt: true}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func typeStr(e *Editor, s string) {
	for _, r := range s {
		switch r {
		case '\n':
			e.Update(key("enter"))
		case ' ':
			e.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
		default:
			e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
}

func newEd() *Editor {
	e := NewEditor()
	e.SetSize(60, 20)
	return e
}

func TestWrapLine(t *testing.T) {
	r := []rune("alpha beta gamma delta")
	segs := wrapLine(r, 11)
	var got []string
	for _, s := range segs {
		got = append(got, string(r[s.s:s.e]))
	}
	want := []string{"alpha beta ", "gamma delta"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q want %q", got, want)
	}
	// word longer than width is hard broken
	long := []rune("abcdefghijklmnop")
	segs = wrapLine(long, 5)
	if len(segs) != 4 || segs[0].e != 5 {
		t.Fatalf("hard break failed: %v", segs)
	}
	// empty line yields one segment
	if s := wrapLine(nil, 10); len(s) != 1 {
		t.Fatal("empty")
	}
}

func TestTypingUndoAndWrapView(t *testing.T) {
	e := newEd()
	typeStr(e, "location / {\ntry_files $uri =404;\n}")
	// auto indent after '{'
	if got := e.Text(); got != "location / {\n\ttry_files $uri =404;\n\t}\n" {
		t.Fatalf("unexpected text %q", got)
	}
	if !e.Modified() {
		t.Fatal("should be modified")
	}
	e.Update(key("alt+u"))
	e.Update(key("alt+u"))
	if strings.Contains(e.Text(), "}") {
		t.Fatalf("undo should have removed the brace: %q", e.Text())
	}
}

func TestCutPasteLines(t *testing.T) {
	e := newEd()
	e.SetText("one\ntwo\nthree\n")
	e.Update(key("down")) // on "two"
	e.Update(key("ctrl+k"))
	if e.Text() != "one\nthree\n" {
		t.Fatalf("cut: %q", e.Text())
	}
	e.Update(key("down")) // end
	e.Update(key("ctrl+u"))
	if e.Text() != "one\ntwo\nthree\n" {
		t.Fatalf("paste: %q", e.Text())
	}
}

func TestSearchSelects(t *testing.T) {
	e := newEd()
	e.SetText("alpha\nbeta needle here\ngamma\n")
	e.Update(key("ctrl+w"))
	typeStr(e, "NEEDLE")
	e.Update(key("enter"))
	if e.r != 1 || e.c != len("beta needle") {
		t.Fatalf("cursor at %d,%d", e.r, e.c)
	}
	if a, b, ok := e.selection(); !ok || e.text(a, b) != "needle" {
		t.Fatal("match should be selected")
	}
}

func TestSaveEvents(t *testing.T) {
	e := newEd()
	typeStr(e, "x")
	ev, _ := e.Update(key("ctrl+s"))
	if ev.kind != evApply {
		t.Fatalf("ctrl+s should save & apply, got %v", ev.kind)
	}
	e.Update(key("ctrl+o"))
	ev, _ = e.Update(key("s"))
	if ev.kind != evSave {
		t.Fatalf("ctrl+o then s should save, got %v", ev.kind)
	}
	ev, _ = e.Update(key("ctrl+x"))
	if ev.kind != evNone || e.pm != pmExit {
		t.Fatal("exit on modified buffer must prompt")
	}
	ev, _ = e.Update(key("n"))
	if ev.kind != evExit {
		t.Fatal("N discards and exits")
	}
}

func TestViewWrapsLongLines(t *testing.T) {
	e := NewEditor()
	e.SetSize(40, 12)
	e.SetText("fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name some more words here to force wrapping\n")
	v := e.View()
	if !strings.Contains(v, "↪") {
		t.Fatalf("expected a continuation marker in:\n%s", v)
	}
	for _, l := range strings.Split(v, "\n") {
		if w := lipglossWidth(l); w > 40 {
			t.Fatalf("line wider than the terminal (%d): %q", w, l)
		}
	}
}

func TestVerticalMoveAcrossWrap(t *testing.T) {
	e := NewEditor()
	e.SetSize(20, 12)
	e.SetText(strings.Repeat("word ", 20) + "\nshort\n")
	e.Update(key("down")) // stays in the same logical line (wrapped)
	if e.r != 0 || e.c == 0 {
		t.Fatalf("down should move inside the wrapped line: r=%d c=%d", e.r, e.c)
	}
}
