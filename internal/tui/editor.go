package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// A minimal full-screen text editor with nano's key bindings and mandatory
// soft word-wrap. It is intentionally self-contained: it knows nothing about
// Hestia, it just emits events (save, save+apply, exit, insert upstream).

const tabWidth = 4

func rw(r rune) int {
	if r == '\t' {
		return tabWidth
	}
	w := runewidth.RuneWidth(r)
	if w < 0 {
		return 0
	}
	return w
}

type pos struct{ r, c int }

func (a pos) less(b pos) bool { return a.r < b.r || (a.r == b.r && a.c < b.c) }

type snap struct {
	lines [][]rune
	r, c  int
}

type evKind int

const (
	evNone evKind = iota
	evSave
	evApply
	evExit
	evInsertUpstream
)

type edEvent struct {
	kind      evKind
	exitAfter bool
}

type pmode int

const (
	pmNone pmode = iota
	pmSearch
	pmReplaceFrom
	pmReplaceTo
	pmGoto
	pmWrite
	pmExit
	pmHelp
)

// seg is a soft-wrapped slice [s,e) of a logical line.
type seg struct{ s, e int }

// wrapLine word-wraps r to width cells. Breaks happen after whitespace; a
// word longer than the width is hard-broken.
func wrapLine(r []rune, width int) []seg {
	if width < 1 {
		width = 1
	}
	var out []seg
	s := 0
	for s < len(r) {
		x, last, i := 0, -1, s
		for ; i < len(r); i++ {
			w := rw(r[i])
			if x+w > width && i > s {
				break
			}
			x += w
			if r[i] == ' ' || r[i] == '\t' {
				last = i + 1
			}
		}
		if i >= len(r) {
			out = append(out, seg{s, len(r)})
			s = len(r)
			break
		}
		e := i
		if last > s {
			e = last
		}
		out = append(out, seg{s, e})
		s = e
	}
	if len(out) == 0 {
		out = []seg{{0, 0}}
	}
	return out
}

func segFor(segs []seg, col int) int {
	for k, sg := range segs {
		if col < sg.e || k == len(segs)-1 {
			return k
		}
	}
	return 0
}

func cellWidth(r []rune) int {
	w := 0
	for _, x := range r {
		w += rw(x)
	}
	return w
}

// Editor is the nano-like editor model.
type Editor struct {
	lines [][]rune
	r, c  int
	goal  int
	top   int // first visible visual row
	w, h  int

	ReadOnly bool
	Title    string
	Hint     string // extra help text (placeholders etc.)
	modified bool

	mark      *pos
	markShift bool

	cut        string
	cutLines   bool
	lastWasCut bool

	undo, redo []snap
	lastEdit   string

	pm         pmode
	prompt     textinput.Model
	replFrom   string
	lastSearch string
	helpTop    int

	msg    string
	msgErr bool
}

func NewEditor() *Editor {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 0
	return &Editor{lines: [][]rune{{}}, prompt: ti}
}

// SetText replaces the buffer.
func (e *Editor) SetText(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	parts := strings.Split(s, "\n")
	e.lines = make([][]rune, len(parts))
	for i, p := range parts {
		e.lines[i] = []rune(p)
	}
	e.r, e.c, e.top, e.goal = 0, 0, 0, -1
	e.modified = false
	e.mark = nil
	e.undo, e.redo = nil, nil
	e.pm = pmNone
	e.msg = ""
}

// Text returns the buffer with a trailing newline.
func (e *Editor) Text() string {
	var b strings.Builder
	for _, l := range e.lines {
		b.WriteString(string(l))
		b.WriteByte('\n')
	}
	return b.String()
}

func (e *Editor) Modified() bool            { return e.modified }
func (e *Editor) MarkSaved()                { e.modified = false }
func (e *Editor) SetSize(w, h int)          { e.w, e.h = w, h; e.fix() }
func (e *Editor) Notify(s string, err bool) { e.msg, e.msgErr = s, err }

func (e *Editor) bodyH() int {
	h := e.h - 4
	if h < 1 {
		h = 1
	}
	return h
}

func (e *Editor) gutterW() int { return len(fmt.Sprint(len(e.lines))) + 1 }

func (e *Editor) textW() int {
	w := e.w - e.gutterW() - 1 // last column reserved for the end-of-line cursor
	if w < 8 {
		w = 8
	}
	return w
}

func (e *Editor) layout() [][]seg {
	tw := e.textW()
	out := make([][]seg, len(e.lines))
	for i, l := range e.lines {
		out[i] = wrapLine(l, tw)
	}
	return out
}

// cursorVisualRow returns the cursor's absolute visual row and x offset.
func (e *Editor) cursorVisual(all [][]seg) (row, x int) {
	for i := 0; i < e.r; i++ {
		row += len(all[i])
	}
	k := segFor(all[e.r], e.c)
	row += k
	x = cellWidth(e.lines[e.r][all[e.r][k].s:e.c])
	return
}

// fix keeps the cursor inside the viewport.
func (e *Editor) fix() {
	if e.r >= len(e.lines) {
		e.r = len(e.lines) - 1
	}
	if e.c > len(e.lines[e.r]) {
		e.c = len(e.lines[e.r])
	}
	all := e.layout()
	row, _ := e.cursorVisual(all)
	bh := e.bodyH()
	if row < e.top {
		e.top = row
	}
	if row >= e.top+bh {
		e.top = row - bh + 1
	}
	total := 0
	for _, s := range all {
		total += len(s)
	}
	if e.top > total-1 {
		e.top = total - 1
	}
	if e.top < 0 {
		e.top = 0
	}
}

// ---------------------------------------------------------------------------
// undo

func (e *Editor) snapshot(kind string) {
	if kind != "" && kind == e.lastEdit {
		e.modified = true
		return
	}
	e.lastEdit = kind
	cp := make([][]rune, len(e.lines))
	for i, l := range e.lines {
		cp[i] = append([]rune(nil), l...)
	}
	e.undo = append(e.undo, snap{cp, e.r, e.c})
	if len(e.undo) > 300 {
		e.undo = e.undo[1:]
	}
	e.redo = nil
	e.modified = true
}

func (e *Editor) cur() snap {
	cp := make([][]rune, len(e.lines))
	for i, l := range e.lines {
		cp[i] = append([]rune(nil), l...)
	}
	return snap{cp, e.r, e.c}
}

func (e *Editor) doUndo() {
	if len(e.undo) == 0 {
		e.Notify("Nothing to undo", false)
		return
	}
	e.redo = append(e.redo, e.cur())
	s := e.undo[len(e.undo)-1]
	e.undo = e.undo[:len(e.undo)-1]
	e.lines, e.r, e.c = s.lines, s.r, s.c
	e.lastEdit, e.modified, e.mark = "", true, nil
	e.Notify("Undid action", false)
}

func (e *Editor) doRedo() {
	if len(e.redo) == 0 {
		e.Notify("Nothing to redo", false)
		return
	}
	e.undo = append(e.undo, e.cur())
	s := e.redo[len(e.redo)-1]
	e.redo = e.redo[:len(e.redo)-1]
	e.lines, e.r, e.c = s.lines, s.r, s.c
	e.lastEdit, e.modified, e.mark = "", true, nil
	e.Notify("Redid action", false)
}

// ---------------------------------------------------------------------------
// text primitives

func (e *Editor) insertString(s string) {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	parts := strings.Split(s, "\n")
	line := e.lines[e.r]
	tail := append([]rune(nil), line[e.c:]...)
	head := append(append([]rune(nil), line[:e.c]...), []rune(parts[0])...)
	if len(parts) == 1 {
		e.lines[e.r] = append(head, tail...)
		e.c = len(head)
		return
	}
	newLines := make([][]rune, 0, len(parts))
	newLines = append(newLines, head)
	for _, p := range parts[1 : len(parts)-1] {
		newLines = append(newLines, []rune(p))
	}
	last := []rune(parts[len(parts)-1])
	col := len(last)
	newLines = append(newLines, append(last, tail...))
	rest := append([][]rune(nil), e.lines[e.r+1:]...)
	e.lines = append(append(e.lines[:e.r:e.r], newLines...), rest...)
	e.r += len(parts) - 1
	e.c = col
}

func (e *Editor) text(a, b pos) string {
	if b.less(a) {
		a, b = b, a
	}
	if a.r == b.r {
		return string(e.lines[a.r][a.c:b.c])
	}
	var sb strings.Builder
	sb.WriteString(string(e.lines[a.r][a.c:]))
	for i := a.r + 1; i < b.r; i++ {
		sb.WriteByte('\n')
		sb.WriteString(string(e.lines[i]))
	}
	sb.WriteByte('\n')
	sb.WriteString(string(e.lines[b.r][:b.c]))
	return sb.String()
}

func (e *Editor) deleteRange(a, b pos) {
	if b.less(a) {
		a, b = b, a
	}
	merged := append(append([]rune(nil), e.lines[a.r][:a.c]...), e.lines[b.r][b.c:]...)
	rest := append([][]rune(nil), e.lines[b.r+1:]...)
	e.lines = append(append(e.lines[:a.r:a.r], merged), rest...)
	e.r, e.c = a.r, a.c
}

func (e *Editor) selection() (pos, pos, bool) {
	if e.mark == nil {
		return pos{}, pos{}, false
	}
	a, b := *e.mark, pos{e.r, e.c}
	if b.less(a) {
		a, b = b, a
	}
	return a, b, a != b
}

func (e *Editor) deleteSelection() bool {
	a, b, ok := e.selection()
	if !ok {
		e.mark = nil
		return false
	}
	e.deleteRange(a, b)
	e.mark = nil
	return true
}

func leadingWS(r []rune) string {
	i := 0
	for i < len(r) && (r[i] == ' ' || r[i] == '\t') {
		i++
	}
	return string(r[:i])
}

// ---------------------------------------------------------------------------
// movement

func (e *Editor) move(sel bool, fn func()) {
	if sel && e.mark == nil {
		e.mark = &pos{e.r, e.c}
		e.markShift = true
	}
	fn()
	if !sel && e.mark != nil && e.markShift {
		e.mark = nil
	}
	e.lastEdit, e.lastWasCut = "", false
}

func (e *Editor) setGoalFromCursor() {
	all := e.layout()
	_, x := e.cursorVisual(all)
	e.goal = x
}

func (e *Editor) left() {
	if e.c > 0 {
		e.c--
	} else if e.r > 0 {
		e.r--
		e.c = len(e.lines[e.r])
	}
	e.goal = -1
}

func (e *Editor) right() {
	if e.c < len(e.lines[e.r]) {
		e.c++
	} else if e.r < len(e.lines)-1 {
		e.r++
		e.c = 0
	}
	e.goal = -1
}

// vmove moves by n visual rows (negative = up), keeping the goal column.
func (e *Editor) vmove(n int) {
	all := e.layout()
	if e.goal < 0 {
		_, e.goal = e.cursorVisual(all)
	}
	r, k := e.r, segFor(all[e.r], e.c)
	for ; n > 0; n-- {
		if k+1 < len(all[r]) {
			k++
		} else if r+1 < len(all) {
			r++
			k = 0
		} else {
			break
		}
	}
	for ; n < 0; n++ {
		if k > 0 {
			k--
		} else if r > 0 {
			r--
			k = len(all[r]) - 1
		} else {
			break
		}
	}
	sg := all[r][k]
	col, x := sg.s, 0
	for col < sg.e {
		w := rw(e.lines[r][col])
		if x+w > e.goal {
			break
		}
		x += w
		col++
	}
	if k < len(all[r])-1 && col >= sg.e {
		col = sg.e - 1
	}
	e.r, e.c = r, col
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

func (e *Editor) wordRight() {
	for {
		line := e.lines[e.r]
		for e.c < len(line) && isWordRune(line[e.c]) {
			e.c++
		}
		for e.c < len(line) && !isWordRune(line[e.c]) {
			e.c++
		}
		if e.c < len(line) || e.r == len(e.lines)-1 {
			break
		}
		e.r++
		e.c = 0
		if len(e.lines[e.r]) > 0 && isWordRune(e.lines[e.r][0]) {
			break
		}
	}
	e.goal = -1
}

func (e *Editor) wordLeft() {
	for {
		line := e.lines[e.r]
		for e.c > 0 && !isWordRune(line[e.c-1]) {
			e.c--
		}
		for e.c > 0 && isWordRune(line[e.c-1]) {
			e.c--
		}
		if e.c > 0 || e.r == 0 || (e.c == 0 && len(line) > 0 && isWordRune(line[0])) {
			break
		}
		e.r--
		e.c = len(e.lines[e.r])
	}
	e.goal = -1
}

// ---------------------------------------------------------------------------
// editing ops

func (e *Editor) typeRunes(rs []rune) {
	if e.ReadOnly {
		e.Notify("File is read-only", true)
		return
	}
	if e.markShift && e.mark != nil {
		e.snapshot("")
		e.deleteSelection()
	} else {
		e.snapshot("type")
	}
	e.insertString(string(rs))
	e.goal = -1
	e.lastWasCut = false
}

func (e *Editor) newline() {
	if e.ReadOnly {
		return
	}
	e.snapshot("")
	if e.markShift {
		e.deleteSelection()
	}
	before := e.lines[e.r][:e.c]
	ind := leadingWS(before)
	if strings.HasSuffix(strings.TrimRight(string(before), " \t"), "{") {
		ind += "\t"
	}
	e.insertString("\n" + ind)
	e.goal = -1
}

func (e *Editor) backspace() {
	if e.ReadOnly {
		return
	}
	if a, b, ok := e.selection(); ok && e.markShift {
		e.snapshot("")
		e.deleteRange(a, b)
		e.mark = nil
		return
	}
	if e.c == 0 && e.r == 0 {
		return
	}
	e.snapshot("bs")
	if e.c > 0 {
		e.deleteRange(pos{e.r, e.c - 1}, pos{e.r, e.c})
	} else {
		e.deleteRange(pos{e.r - 1, len(e.lines[e.r-1])}, pos{e.r, 0})
	}
	e.goal = -1
}

func (e *Editor) del() {
	if e.ReadOnly {
		return
	}
	if a, b, ok := e.selection(); ok && e.markShift {
		e.snapshot("")
		e.deleteRange(a, b)
		e.mark = nil
		return
	}
	if e.r == len(e.lines)-1 && e.c == len(e.lines[e.r]) {
		return
	}
	e.snapshot("del")
	if e.c < len(e.lines[e.r]) {
		e.deleteRange(pos{e.r, e.c}, pos{e.r, e.c + 1})
	} else {
		e.deleteRange(pos{e.r, e.c}, pos{e.r + 1, 0})
	}
}

func (e *Editor) cutText() {
	if e.ReadOnly {
		e.Notify("File is read-only", true)
		return
	}
	e.snapshot("")
	if a, b, ok := e.selection(); ok {
		e.cut, e.cutLines = e.text(a, b), false
		e.deleteRange(a, b)
		e.mark = nil
		e.lastWasCut = false
		return
	}
	line := string(e.lines[e.r]) + "\n"
	if e.lastWasCut && e.cutLines {
		e.cut += line
	} else {
		e.cut = line
	}
	e.cutLines, e.lastWasCut = true, true
	if len(e.lines) == 1 {
		e.lines[0] = nil
		e.c = 0
	} else {
		e.lines = append(e.lines[:e.r], e.lines[e.r+1:]...)
		if e.r >= len(e.lines) {
			e.r = len(e.lines) - 1
		}
		if e.c > len(e.lines[e.r]) {
			e.c = len(e.lines[e.r])
		}
	}
	e.goal = -1
}

func (e *Editor) copyText() {
	if a, b, ok := e.selection(); ok {
		e.cut, e.cutLines = e.text(a, b), false
		e.mark = nil
	} else {
		e.cut, e.cutLines = string(e.lines[e.r])+"\n", true
	}
	e.Notify("Copied", false)
}

func (e *Editor) uncut() {
	if e.ReadOnly {
		return
	}
	if e.cut == "" {
		e.Notify("Cut buffer is empty", true)
		return
	}
	e.snapshot("")
	if e.cutLines {
		ls := strings.Split(strings.TrimSuffix(e.cut, "\n"), "\n")
		add := make([][]rune, len(ls))
		for i, l := range ls {
			add[i] = []rune(l)
		}
		rest := append([][]rune(nil), e.lines[e.r:]...)
		e.lines = append(append(e.lines[:e.r:e.r], add...), rest...)
		e.r += len(add)
		e.c = min(e.c, len(e.lines[e.r]))
	} else {
		e.insertString(e.cut)
	}
	e.goal = -1
}

// InsertSnippet inserts text at the cursor, indenting continuation lines like the current line.
func (e *Editor) InsertSnippet(s string) {
	if e.ReadOnly {
		return
	}
	e.snapshot("")
	ind := leadingWS(e.lines[e.r][:e.c])
	if strings.TrimSpace(string(e.lines[e.r])) != "" && e.c > 0 && e.c == len(e.lines[e.r]) {
		// cursor at the end of a non-empty line: start the snippet on a new line
		e.insertString("\n" + ind)
	}
	e.insertString(strings.ReplaceAll(s, "\n", "\n"+ind))
	e.goal = -1
	e.Notify("Inserted snippet", false)
}

// ---------------------------------------------------------------------------
// search / replace / goto

func indexFold(hay, needle []rune, from int) int {
	if len(needle) == 0 {
		return -1
	}
	for i := from; i+len(needle) <= len(hay); i++ {
		ok := true
		for j := range needle {
			if unicode.ToLower(hay[i+j]) != unicode.ToLower(needle[j]) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func (e *Editor) find(q string, advance bool) bool {
	if q == "" {
		return false
	}
	nd := []rune(q)
	startC := e.c
	if advance {
		startC = e.c + 1
	}
	n := len(e.lines)
	for k := 0; k <= n; k++ {
		r := (e.r + k) % n
		from := 0
		if k == 0 {
			from = startC
		}
		if from > len(e.lines[r]) {
			continue
		}
		if k == n { // wrapped fully: only the part before the start
			if i := indexFold(e.lines[r], nd, 0); i >= 0 && i < startC {
				e.selectMatch(r, i, len(nd))
				e.Notify("Search Wrapped", false)
				return true
			}
			continue
		}
		if i := indexFold(e.lines[r], nd, from); i >= 0 {
			e.selectMatch(r, i, len(nd))
			if r < e.r || (r == e.r && i < e.c && k > 0) {
				e.Notify("Search Wrapped", false)
			}
			return true
		}
	}
	return false
}

func (e *Editor) selectMatch(r, c, n int) {
	e.mark = &pos{r, c}
	e.markShift = true
	e.r, e.c = r, c+n
	e.goal = -1
}

func (e *Editor) replaceAll(from, to string) int {
	e.snapshot("")
	nd := []rune(from)
	rep := []rune(to)
	count := 0
	for i, l := range e.lines {
		var out []rune
		j := 0
		for {
			k := indexFold(l, nd, j)
			if k < 0 {
				out = append(out, l[j:]...)
				break
			}
			out = append(out, l[j:k]...)
			out = append(out, rep...)
			j = k + len(nd)
			count++
		}
		e.lines[i] = out
	}
	e.c = min(e.c, len(e.lines[e.r]))
	return count
}

func (e *Editor) gotoLine(spec string) {
	var ln, col int
	spec = strings.TrimSpace(spec)
	n, _ := fmt.Sscanf(strings.Replace(spec, ",", " ", 1), "%d %d", &ln, &col)
	if n < 1 || ln < 1 {
		e.Notify("Invalid line number", true)
		return
	}
	e.r = min(ln-1, len(e.lines)-1)
	e.c = 0
	if col > 0 {
		e.c = min(col-1, len(e.lines[e.r]))
	}
	e.goal = -1
	e.mark = nil
}

// ---------------------------------------------------------------------------
// Update

func (e *Editor) startPrompt(m pmode, label, initial string) tea.Cmd {
	e.pm = m
	e.prompt.SetValue(initial)
	e.prompt.CursorEnd()
	e.prompt.Prompt = label
	e.prompt.PromptStyle = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	return e.prompt.Focus()
}

// Update handles input. The returned event is non-nil only when the host
// application has to act (save, apply, exit, ...).
func (e *Editor) Update(msg tea.Msg) (edEvent, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		e.SetSize(m.Width, m.Height)
		return edEvent{}, nil
	case tea.MouseMsg:
		if e.pm == pmHelp {
			return edEvent{}, nil
		}
		switch m.Button {
		case tea.MouseButtonWheelUp:
			e.move(false, func() { e.vmove(-3) })
		case tea.MouseButtonWheelDown:
			e.move(false, func() { e.vmove(3) })
		}
		e.fix()
		return edEvent{}, nil
	case tea.KeyMsg:
		ev, cmd := e.key(m)
		e.fix()
		return ev, cmd
	}
	return edEvent{}, nil
}

func (e *Editor) key(k tea.KeyMsg) (edEvent, tea.Cmd) {
	s := k.String()

	switch e.pm {
	case pmHelp:
		switch s {
		case "up", "ctrl+p":
			if e.helpTop > 0 {
				e.helpTop--
			}
		case "down", "ctrl+n":
			e.helpTop++
		case "pgup":
			e.helpTop = max(0, e.helpTop-e.bodyH())
		case "pgdown":
			e.helpTop += e.bodyH()
		default:
			e.pm = pmNone
		}
		return edEvent{}, nil
	case pmWrite:
		switch s {
		case "s", "S", "enter", "ctrl+o":
			e.pm = pmNone
			return edEvent{kind: evSave}, nil
		case "a", "A", "ctrl+s":
			e.pm = pmNone
			return edEvent{kind: evApply}, nil
		case "esc", "ctrl+c", "ctrl+g":
			e.pm = pmNone
			e.Notify("Cancelled", false)
		}
		return edEvent{}, nil
	case pmExit:
		switch s {
		case "y", "Y", "a", "A":
			e.pm = pmNone
			return edEvent{kind: evApply, exitAfter: true}, nil
		case "s", "S":
			e.pm = pmNone
			return edEvent{kind: evSave, exitAfter: true}, nil
		case "n", "N":
			e.pm = pmNone
			return edEvent{kind: evExit}, nil
		case "esc", "ctrl+c", "ctrl+g":
			e.pm = pmNone
			e.Notify("Cancelled", false)
		}
		return edEvent{}, nil
	case pmSearch, pmReplaceFrom, pmReplaceTo, pmGoto:
		switch s {
		case "esc", "ctrl+c", "ctrl+g":
			e.pm = pmNone
			e.Notify("Cancelled", false)
			return edEvent{}, nil
		case "enter":
			v := e.prompt.Value()
			switch e.pm {
			case pmSearch:
				e.pm = pmNone
				if v != "" {
					e.lastSearch = v
				}
				if e.lastSearch == "" || !e.find(e.lastSearch, false) {
					e.Notify(fmt.Sprintf("%q not found", e.lastSearch), true)
				}
			case pmReplaceFrom:
				if v == "" {
					e.pm = pmNone
					return edEvent{}, nil
				}
				e.replFrom = v
				e.lastSearch = v
				return edEvent{}, e.startPrompt(pmReplaceTo, "Replace with: ", "")
			case pmReplaceTo:
				e.pm = pmNone
				n := e.replaceAll(e.replFrom, v)
				e.Notify(fmt.Sprintf("Replaced %d occurrence(s)", n), false)
			case pmGoto:
				e.pm = pmNone
				e.gotoLine(v)
			}
			return edEvent{}, nil
		}
		var cmd tea.Cmd
		e.prompt, cmd = e.prompt.Update(k)
		return edEvent{}, cmd
	}

	e.msg = ""
	sel := strings.HasPrefix(s, "shift+")
	if sel {
		s = strings.TrimPrefix(s, "shift+")
	}
	switch s {
	// --- file / app
	case "ctrl+x":
		if e.modified && !e.ReadOnly {
			e.pm = pmExit
			return edEvent{}, nil
		}
		return edEvent{kind: evExit}, nil
	case "ctrl+o":
		if e.ReadOnly {
			e.Notify("File is read-only", true)
			return edEvent{}, nil
		}
		e.pm = pmWrite
		return edEvent{}, nil
	case "ctrl+s":
		if e.ReadOnly {
			return edEvent{}, nil
		}
		return edEvent{kind: evApply}, nil
	case "f2":
		if e.ReadOnly {
			return edEvent{}, nil
		}
		return edEvent{kind: evInsertUpstream}, nil
	case "ctrl+g", "f1":
		e.pm, e.helpTop = pmHelp, 0
		return edEvent{}, nil

	// --- search / goto
	case "ctrl+w":
		return edEvent{}, e.startPrompt(pmSearch, "Search: ", e.lastSearch)
	case "alt+w":
		if e.lastSearch == "" || !e.find(e.lastSearch, true) {
			e.Notify("No more matches", true)
		}
		return edEvent{}, nil
	case "ctrl+\\":
		if e.ReadOnly {
			return edEvent{}, nil
		}
		return edEvent{}, e.startPrompt(pmReplaceFrom, "Search (to replace): ", e.lastSearch)
	case "ctrl+_", "alt+g":
		return edEvent{}, e.startPrompt(pmGoto, "Enter line[,column]: ", "")
	case "ctrl+c":
		e.Notify(fmt.Sprintf("line %d/%d (%d%%), col %d/%d",
			e.r+1, len(e.lines), (e.r+1)*100/len(e.lines), e.c+1, len(e.lines[e.r])+1), false)
		return edEvent{}, nil
	case "ctrl+l":
		all := e.layout()
		row, _ := e.cursorVisual(all)
		e.top = max(0, row-e.bodyH()/2)
		return edEvent{}, nil

	// --- cut / copy / paste / undo / mark
	case "ctrl+k":
		e.cutText()
		return edEvent{}, nil
	case "alt+6", "alt+^":
		e.copyText()
		return edEvent{}, nil
	case "ctrl+u":
		e.uncut()
		return edEvent{}, nil
	case "alt+u":
		e.doUndo()
		return edEvent{}, nil
	case "alt+e":
		e.doRedo()
		return edEvent{}, nil
	case "alt+a":
		if e.mark != nil {
			e.mark = nil
			e.Notify("Mark unset", false)
		} else {
			e.mark = &pos{e.r, e.c}
			e.markShift = false
			e.Notify("Mark set", false)
		}
		return edEvent{}, nil

	// --- movement
	case "up", "ctrl+p":
		e.move(sel, func() { e.vmove(-1) })
	case "down", "ctrl+n":
		e.move(sel, func() { e.vmove(1) })
	case "left", "ctrl+b":
		e.move(sel, e.left)
	case "right", "ctrl+f":
		e.move(sel, e.right)
	case "ctrl+left", "alt+left", "alt+b":
		e.move(sel, e.wordLeft)
	case "ctrl+right", "alt+right", "alt+f", "ctrl+@":
		e.move(sel, e.wordRight)
	case "home", "ctrl+a":
		e.move(sel, func() { e.c = 0; e.goal = -1 })
	case "end", "ctrl+e":
		e.move(sel, func() { e.c = len(e.lines[e.r]); e.goal = -1 })
	case "pgup", "ctrl+y":
		e.move(sel, func() { e.vmove(-e.bodyH()) })
	case "pgdown", "ctrl+v":
		e.move(sel, func() { e.vmove(e.bodyH()) })
	case "ctrl+home", "alt+\\":
		e.move(sel, func() { e.r, e.c, e.goal = 0, 0, -1 })
	case "ctrl+end", "alt+/":
		e.move(sel, func() { e.r = len(e.lines) - 1; e.c = len(e.lines[e.r]); e.goal = -1 })

	// --- editing
	case "enter":
		e.newline()
	case "backspace", "ctrl+h":
		e.backspace()
	case "delete", "ctrl+d":
		e.del()
	case "tab":
		e.typeRunes([]rune{'\t'})
	case " ", "space":
		e.typeRunes([]rune{' '})
	default:
		if k.Type == tea.KeyRunes && !k.Alt && len(k.Runes) > 0 {
			e.typeRunes(k.Runes)
		} else if k.Type == tea.KeySpace {
			e.typeRunes([]rune{' '})
		}
	}
	return edEvent{}, nil
}

// ---------------------------------------------------------------------------
// View

var (
	stDirective = lipgloss.NewStyle().Foreground(cCyan)
	stComment   = lipgloss.NewStyle().Foreground(cDim).Italic(true)
	stString    = lipgloss.NewStyle().Foreground(cYellow)
	stVar       = lipgloss.NewStyle().Foreground(cOrange)
	stPlace     = lipgloss.NewStyle().Foreground(cPink)
	stPunct     = lipgloss.NewStyle().Foreground(cAccent)
	stNormal    = lipgloss.NewStyle().Foreground(cText)
	stLocation  = lipgloss.NewStyle().Foreground(cGreen).Bold(true)
)

const (
	clsNormal = iota
	clsDirective
	clsComment
	clsString
	clsVar
	clsPlace
	clsPunct
	clsLocation
)

// classify gives a rough nginx syntax class to every rune of a line.
func classify(r []rune) []uint8 {
	cls := make([]uint8, len(r))
	i := 0
	for i < len(r) && (r[i] == ' ' || r[i] == '\t') {
		i++
	}
	// first word = directive
	j := i
	for j < len(r) && r[j] != ' ' && r[j] != '\t' && r[j] != ';' && r[j] != '{' && r[j] != '#' {
		j++
	}
	kind := uint8(clsDirective)
	if string(r[i:j]) == "location" || string(r[i:j]) == "upstream" || string(r[i:j]) == "server" {
		kind = clsLocation
	}
	if i < len(r) && r[i] != '#' {
		for k := i; k < j; k++ {
			cls[k] = kind
		}
	}
	for k := j; k < len(r); k++ {
		switch {
		case r[k] == '#':
			for ; k < len(r); k++ {
				cls[k] = clsComment
			}
		case r[k] == '"' || r[k] == '\'':
			q := r[k]
			cls[k] = clsString
			for k++; k < len(r); k++ {
				cls[k] = clsString
				if r[k] == '\\' && k+1 < len(r) {
					k++
					cls[k] = clsString
					continue
				}
				if r[k] == q {
					break
				}
			}
		case r[k] == '$' && k+1 < len(r) && (unicode.IsLetter(r[k+1]) || r[k+1] == '{' || unicode.IsDigit(r[k+1])):
			cls[k] = clsVar
			for k++; k < len(r) && (unicode.IsLetter(r[k]) || unicode.IsDigit(r[k]) || r[k] == '_'); k++ {
				cls[k] = clsVar
			}
			k--
		case r[k] == '%':
			if end := indexRune(r, '%', k+1); end > k+1 && isPlaceholder(r[k+1:end]) {
				for x := k; x <= end; x++ {
					cls[x] = clsPlace
				}
				k = end
			}
		case r[k] == '{' || r[k] == '}' || r[k] == ';':
			cls[k] = clsPunct
		}
	}
	if i < len(r) && (r[i] == '}') {
		cls[i] = clsPunct
	}
	return cls
}

func isPlaceholder(r []rune) bool {
	for _, c := range r {
		if !(c == '_' || unicode.IsLetter(c)) {
			return false
		}
	}
	return len(r) > 0
}

func indexRune(r []rune, c rune, from int) int {
	for i := from; i < len(r); i++ {
		if r[i] == c {
			return i
		}
	}
	return -1
}

var clsStyles = [...]lipgloss.Style{
	clsNormal: stNormal, clsDirective: stDirective, clsComment: stComment, clsString: stString,
	clsVar: stVar, clsPlace: stPlace, clsPunct: stPunct, clsLocation: stLocation,
}

func (e *Editor) View() string {
	w := e.w
	if w < 20 {
		w = 20
	}
	// header
	mod := ""
	if e.modified {
		mod = lipgloss.NewStyle().Foreground(cYellow).Bold(true).Render(" ● Modified")
	}
	ro := ""
	if e.ReadOnly {
		ro = sDim.Render("  (read-only)")
	}
	head := sTitle.Render(" nano-tui ") + " " + sText.Render(plainTrunc(e.Title, w-30)) + ro + mod
	head = lipgloss.NewStyle().Width(w).Background(cBar).Render(head)

	var body []string
	if e.pm == pmHelp {
		body = e.helpLines()
	} else {
		body = e.bodyLines()
	}
	bh := e.bodyH()
	for len(body) < bh {
		body = append(body, "")
	}
	body = body[:bh]

	// message / prompt line
	var line string
	switch e.pm {
	case pmSearch, pmReplaceFrom, pmReplaceTo, pmGoto:
		line = e.prompt.View()
	case pmWrite:
		line = sWarn.Render("Write out: ") + keyHints("S/Enter", "save", "A", "save & apply (nginx -t + reload)", "Esc", "cancel")
	case pmExit:
		line = sWarn.Render("Save modified buffer? ") + keyHints("Y", "save & apply", "S", "save only", "N", "discard", "Esc", "cancel")
	default:
		if e.msg != "" {
			st := sOK
			if e.msgErr {
				st = sErr
			}
			line = st.Render("[ " + e.msg + " ]")
		} else if a, b, ok := e.selection(); ok && e.pm == pmNone {
			line = sDim.Render(fmt.Sprintf("[ %d chars selected ]", len([]rune(e.text(a, b)))))
		}
	}
	line = truncate(line, w)

	return strings.Join(append(append([]string{head}, body...), padRight(line, w), e.footer(w)), "\n")
}

func (e *Editor) bodyLines() []string {
	all := e.layout()
	gw := e.gutterW()
	bh := e.bodyH()
	selA, selB, hasSel := e.selection()
	gut := lipgloss.NewStyle().Foreground(cDim)
	curGut := lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	revCursor := lipgloss.NewStyle().Reverse(true)
	selBG := lipgloss.NewStyle().Background(cSel)

	out := make([]string, 0, bh)
	row := 0
	for li, segs := range all {
		if row+len(segs) <= e.top {
			row += len(segs)
			continue
		}
		var cls []uint8
		for k, sg := range segs {
			if row < e.top {
				row++
				continue
			}
			if len(out) >= bh {
				return out
			}
			if cls == nil {
				cls = classify(e.lines[li])
			}
			num := strings.Repeat(" ", gw)
			if k == 0 {
				st := gut
				if li == e.r {
					st = curGut
				}
				num = st.Render(fmt.Sprintf("%*d ", gw-1, li+1))
			} else {
				num = gut.Render(strings.Repeat(" ", gw-1) + "↪")
			}
			var sb strings.Builder
			sb.WriteString(num)
			emit := func(text string, st lipgloss.Style) {
				if text != "" {
					sb.WriteString(st.Render(text))
				}
			}
			last := k == len(segs)-1
			for c := sg.s; c < sg.e || (last && c == sg.e); c++ {
				isCursor := li == e.r && c == e.c && e.pm == pmNone
				inSel := false
				if hasSel {
					p := pos{li, c}
					inSel = !p.less(selA) && p.less(selB)
				}
				var ch rune = ' '
				if c < len(e.lines[li]) {
					ch = e.lines[li][c]
				}
				text := string(ch)
				if ch == '\t' {
					text = strings.Repeat(" ", tabWidth)
				}
				st := stNormal
				if c < len(cls) {
					st = clsStyles[cls[c]]
				}
				if inSel {
					st = st.Inherit(selBG)
				}
				if isCursor {
					st = revCursor.Foreground(lipgloss.Color("#F8F8F2"))
					if c < len(cls) {
						st = st.Foreground(clsStyles[cls[c]].GetForeground())
					}
					if ch == '\t' {
						text = " " + strings.Repeat(" ", tabWidth-1)
					}
				}
				emit(text, st)
			}
			out = append(out, sb.String())
			row++
		}
	}
	return out
}

func (e *Editor) footer(w int) string {
	items := [][2]string{
		{"^G", "Help"}, {"^O", "Write Out"}, {"^S", "Save+Apply"}, {"^W", "Where Is"},
		{"^K", "Cut"}, {"^U", "Paste"}, {"^X", "Exit"}, {"^\\", "Replace"},
		{"^_", "Go To Line"}, {"M-U", "Undo"}, {"M-E", "Redo"}, {"M-A", "Mark"}, {"F2", "Upstream"},
	}
	if e.ReadOnly {
		items = [][2]string{{"^G", "Help"}, {"^W", "Where Is"}, {"^_", "Go To Line"}, {"^X", "Close"}}
	}
	cell := func(it [2]string) string {
		return lipgloss.NewStyle().Reverse(true).Render(it[0]) + " " + sKeyDesc.Render(it[1])
	}
	half := (len(items) + 1) / 2
	row := func(its [][2]string) string {
		var parts []string
		for _, it := range its {
			parts = append(parts, cell(it))
		}
		return truncate(strings.Join(parts, "  "), w)
	}
	if len(items) <= 4 {
		return padRight(row(items), w) + "\n" + padRight("", w)
	}
	return padRight(row(items[:half]), w) + "\n" + padRight(row(items[half:]), w)
}

func (e *Editor) helpLines() []string {
	lines := []string{
		sTitle.Render(" Editor help ") + sDim.Render("  (↑/↓ scroll, any other key closes)"),
		"",
		sHi.Render("File") + "      ^O write out (save / save & apply)   ^S save & apply   ^X exit   F2 insert upstream",
		sHi.Render("Search") + "    ^W where is   M-W next match   ^\\ replace all   ^_ or M-G go to line",
		sHi.Render("Edit") + "      ^K cut line / selection   M-6 copy   ^U paste   M-U undo   M-E redo   ^D delete   Tab inserts a tab",
		sHi.Render("Select") + "    M-A set/unset mark, or Shift+arrows; then ^K / M-6",
		sHi.Render("Move") + "      arrows, ^B ^F ^P ^N, ^A/^E line start/end, ^Y/^V or PgUp/PgDn, Ctrl+←/→ by word, ^C position",
		sHi.Render("Wrap") + "      long lines are soft-wrapped at word boundaries (↪ marks a continuation)",
	}
	if e.Hint != "" {
		lines = append(lines, "", sHi.Render("This file"))
		for _, l := range strings.Split(e.Hint, "\n") {
			lines = append(lines, "  "+l)
		}
	}
	for i := 0; i < e.helpTop && len(lines) > 1; i++ {
		lines = lines[1:]
	}
	for i, l := range lines {
		lines[i] = truncate(l, e.w)
	}
	return lines
}
