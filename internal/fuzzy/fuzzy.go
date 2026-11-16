// Package fuzzy is a small fzf-style fuzzy matcher (no external dependency).
//
// It scores a pattern against a text with a Smith-Waterman-like dynamic
// programme: matches at word boundaries and consecutive matches are rewarded,
// gaps are penalised. A query may contain several space separated terms; all
// of them must match (AND semantics, like fzf).
package fuzzy

import (
	"sort"
	"strings"
	"unicode"
)

const (
	scoreMatch    = 16
	scoreGapStart = 3
	scoreGapExt   = 1
	bonusBoundary = 8
	bonusConsec   = 4
	bonusCase     = 1
	bonusStart    = 4 // first pattern char matched at index 0 of the text
	bonusFirst    = 2 // multiplier applied to the boundary bonus of the first pattern char

	neg = -1 << 30
)

// Match is the result of matching a query against one text.
type Match struct {
	Score int
	Pos   []int // matched rune indexes in the text, ascending, unique
}

func isSep(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

func boundaryBonus(t []rune, j int) int {
	if j == 0 {
		return bonusBoundary
	}
	p, c := t[j-1], t[j]
	if isSep(p) && !isSep(c) {
		return bonusBoundary
	}
	if unicode.IsLower(p) && unicode.IsUpper(c) {
		return bonusBoundary - 1
	}
	return 0
}

// Term matches a single term (no spaces) against text.
// Matching is case-insensitive unless the term contains an upper-case letter.
func Term(term, text string) (Match, bool) {
	p := []rune(term)
	t := []rune(text)
	if len(p) == 0 {
		return Match{}, true
	}
	if len(p) > len(t) {
		return Match{}, false
	}
	sensitive := false
	for _, r := range p {
		if unicode.IsUpper(r) {
			sensitive = true
			break
		}
	}
	fold := func(r rune) rune {
		if sensitive {
			return r
		}
		return unicode.ToLower(r)
	}
	pf := make([]rune, len(p))
	for i, r := range p {
		pf[i] = fold(r)
	}
	tf := make([]rune, len(t))
	for i, r := range t {
		tf[i] = fold(r)
	}

	// Fast reject: pattern must be a subsequence.
	k := 0
	for _, r := range tf {
		if k < len(pf) && r == pf[k] {
			k++
		}
	}
	if k < len(pf) {
		return Match{}, false
	}

	m, n := len(pf), len(tf)
	// H[i][j]: best score with pf[i] matched exactly at tf[j].
	// F[i][j]: best "H[i][k] minus gap penalty" over k <= j-2 (gap of >=1 chars before j).
	H := make([][]int, m)
	F := make([][]int, m)
	Fk := make([][]int, m) // argmax k for F
	viaGap := make([][]bool, m)
	for i := 0; i < m; i++ {
		H[i] = make([]int, n)
		F[i] = make([]int, n)
		Fk[i] = make([]int, n)
		viaGap[i] = make([]bool, n)
		for j := 0; j < n; j++ {
			H[i][j], F[i][j], Fk[i][j] = neg, neg, -1
		}
	}
	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			// F row i, column j depends on H[i][j-2] and F[i][j-1].
			if j >= 2 {
				best, bk := neg, -1
				if H[i][j-2] > neg {
					best, bk = H[i][j-2]-scoreGapStart, j-2
				}
				if j >= 1 && F[i][j-1] > neg && F[i][j-1]-scoreGapExt > best {
					best, bk = F[i][j-1]-scoreGapExt, Fk[i][j-1]
				}
				F[i][j], Fk[i][j] = best, bk
			}
			if tf[j] != pf[i] {
				continue
			}
			bonus := boundaryBonus(t, j)
			if i == 0 {
				bonus *= bonusFirst
				if j == 0 {
					bonus += bonusStart
				}
			}
			cs := 0
			if t[j] == p[i] {
				cs = bonusCase
			}
			base := scoreMatch + bonus + cs
			if i == 0 {
				H[i][j] = base
				continue
			}
			best, gap := neg, false
			if j >= 1 && H[i-1][j-1] > neg {
				best = H[i-1][j-1] + bonusConsec
			}
			if F[i-1][j] > best {
				best, gap = F[i-1][j], true
			}
			if best == neg {
				continue
			}
			H[i][j] = base + best
			viaGap[i][j] = gap
		}
	}
	bestJ, bestS := -1, neg
	for j := 0; j < n; j++ {
		if H[m-1][j] > bestS {
			bestS, bestJ = H[m-1][j], j
		}
	}
	if bestJ < 0 {
		return Match{}, false
	}
	pos := make([]int, m)
	j := bestJ
	for i := m - 1; i >= 0; i-- {
		pos[i] = j
		if i == 0 {
			break
		}
		if viaGap[i][j] {
			j = Fk[i-1][j]
		} else {
			j--
		}
	}
	return Match{Score: bestS, Pos: pos}, true
}

// Query matches a (possibly multi-term) query against text. All terms must
// match. An empty query matches everything with score 0.
func Query(query, text string) (Match, bool) {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return Match{}, true
	}
	total := 0
	seen := map[int]bool{}
	for _, t := range terms {
		m, ok := Term(t, text)
		if !ok {
			return Match{}, false
		}
		total += m.Score
		for _, p := range m.Pos {
			seen[p] = true
		}
	}
	pos := make([]int, 0, len(seen))
	for p := range seen {
		pos = append(pos, p)
	}
	sort.Ints(pos)
	// Mild preference for shorter haystacks.
	total -= len([]rune(text)) / 8
	return Match{Score: total, Pos: pos}, true
}

// Ranked is one filtered item: Index refers to the original slice.
type Ranked struct {
	Index int
	Match
}

// Filter returns the items matching query, best first. Ties keep input order.
// With an empty query every item is returned in input order.
func Filter(query string, n int, text func(i int) string) []Ranked {
	out := make([]Ranked, 0, n)
	empty := strings.TrimSpace(query) == ""
	for i := 0; i < n; i++ {
		m, ok := Query(query, text(i))
		if ok {
			out = append(out, Ranked{Index: i, Match: m})
		}
	}
	if !empty {
		sort.SliceStable(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	}
	return out
}
