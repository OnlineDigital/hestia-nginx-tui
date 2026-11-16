package fuzzy

import (
	"reflect"
	"testing"
)

func TestTermBasics(t *testing.T) {
	if _, ok := Term("xyz", "example.com"); ok {
		t.Fatal("xyz must not match example.com")
	}
	m, ok := Term("exc", "example.com")
	if !ok {
		t.Fatal("exc should match example.com")
	}
	if !reflect.DeepEqual(m.Pos, []int{0, 1, 7}) && len(m.Pos) != 3 {
		t.Fatalf("unexpected positions %v", m.Pos)
	}
}

func TestPositionsAreValid(t *testing.T) {
	text := "my-shop.example.com"
	m, ok := Term("shex", text)
	if !ok {
		t.Fatal("no match")
	}
	r := []rune(text)
	want := []rune("shex")
	for i, p := range m.Pos {
		if r[p] != want[i] {
			t.Fatalf("pos %d -> %q, want %q", p, r[p], want[i])
		}
		if i > 0 && m.Pos[i] <= m.Pos[i-1] {
			t.Fatalf("positions not increasing: %v", m.Pos)
		}
	}
}

func TestBoundaryBeatsScattered(t *testing.T) {
	a, _ := Term("sh", "my-shop.com") // boundary + consecutive
	b, _ := Term("sh", "bushwhacker") // scattered inside a word
	if a.Score <= b.Score {
		t.Fatalf("boundary match should score higher: %d <= %d", a.Score, b.Score)
	}
}

func TestSmartCase(t *testing.T) {
	if _, ok := Term("Shop", "myshop.com"); ok {
		t.Fatal("upper-case pattern should be case sensitive")
	}
	if _, ok := Term("shop", "MyShop.com"); !ok {
		t.Fatal("lower-case pattern should be case insensitive")
	}
}

func TestMultiTermAND(t *testing.T) {
	if _, ok := Query("shop laravel", "shop.com admin laravel"); !ok {
		t.Fatal("both terms present")
	}
	if _, ok := Query("shop wordpress", "shop.com admin laravel"); ok {
		t.Fatal("second term missing, must fail")
	}
	m, ok := Query("", "anything")
	if !ok || m.Score != 0 {
		t.Fatal("empty query matches everything")
	}
}

func TestFilterOrder(t *testing.T) {
	items := []string{"zzz.com", "shop.example.com", "my-shop.net", "unrelated.org"}
	got := Filter("shop", len(items), func(i int) string { return items[i] })
	if len(got) != 2 {
		t.Fatalf("want 2 results, got %d", len(got))
	}
	if items[got[0].Index] != "shop.example.com" {
		t.Fatalf("prefix match should rank first, got %s", items[got[0].Index])
	}
	all := Filter("", len(items), func(i int) string { return items[i] })
	if len(all) != 4 || all[0].Index != 0 {
		t.Fatal("empty query must keep input order")
	}
}

func TestUnicode(t *testing.T) {
	m, ok := Term("șt", "știri.ro")
	if !ok || len(m.Pos) != 2 || m.Pos[0] != 0 {
		t.Fatalf("unicode match failed: %v %v", m, ok)
	}
}
