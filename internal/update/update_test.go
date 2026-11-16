package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v0.1.0", "v0.1.1", true},
		{"0.1.0", "v0.2.0", true},
		{"v1.9.0", "v1.10.0", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2.4", "v1.2.3", false},
		{"dev", "v0.0.1", true},
		{"v1.0.0", "garbage", false},
		{"v1.0", "v1.0.1", true},
	}
	for _, c := range cases {
		if got := IsNewer(c.cur, c.latest); got != c.want {
			t.Errorf("IsNewer(%q,%q)=%v want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func tarball(t *testing.T, name string, body []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		n string
		b []byte
	}{{"README.md", []byte("readme")}, {name, body}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.n, Mode: 0o755, Size: int64(len(f.b)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(f.b)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func fakeGitHub(t *testing.T, tag string, newBin []byte, corrupt bool) *httptest.Server {
	asset, err := AssetName(tag)
	if err != nil {
		t.Skip(err)
	}
	archive := tarball(t, BinaryName, newBin)
	sum := sha256.Sum256(archive)
	hexsum := hex.EncodeToString(sum[:])
	if corrupt {
		hexsum = strings.Repeat("0", 64)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/org/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/org/repo/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/org/repo/releases/download/"+tag+"/"+asset, func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/org/repo/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n%s  other.tar.gz\n", hexsum, asset, strings.Repeat("a", 64))
	})
	srv := httptest.NewServer(mux)
	oldBase, oldRepo := BaseURL, Repo
	BaseURL, Repo = srv.URL, "org/repo"
	t.Cleanup(func() { srv.Close(); BaseURL, Repo = oldBase, oldRepo })
	return srv
}

func TestLatestTag(t *testing.T) {
	fakeGitHub(t, "v0.3.1", []byte("x"), false)
	tag, err := LatestTag(context.Background())
	if err != nil || tag != "v0.3.1" {
		t.Fatalf("%q %v", tag, err)
	}
}

func TestRunUpdatesBinaryAtomically(t *testing.T) {
	fakeGitHub(t, "v0.2.0", []byte("NEW-BINARY"), false)
	target := filepath.Join(t.TempDir(), "hestia-nginx-tui")
	os.WriteFile(target, []byte("OLD"), 0o755)

	res, err := Run(context.Background(), "v0.1.0", target)
	if err != nil || !res.Updated {
		t.Fatalf("%v\n%s", err, res.Log)
	}
	if b, _ := os.ReadFile(target); string(b) != "NEW-BINARY" {
		t.Fatalf("binary not replaced: %q", b)
	}
	if st, _ := os.Stat(target); st.Mode().Perm()&0o111 == 0 {
		t.Fatal("must stay executable")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".hestia-nginx-tui-new-*")); len(left) != 0 {
		t.Fatal("temp file left behind")
	}
}

func TestRunAlreadyLatest(t *testing.T) {
	fakeGitHub(t, "v0.2.0", []byte("NEW"), false)
	target := filepath.Join(t.TempDir(), "bin")
	os.WriteFile(target, []byte("OLD"), 0o755)
	res, err := Run(context.Background(), "v0.2.0", target)
	if err != nil || res.Updated || !strings.Contains(res.Log, "up to date") {
		t.Fatalf("%v %+v", err, res)
	}
	if b, _ := os.ReadFile(target); string(b) != "OLD" {
		t.Fatal("must not touch the binary when up to date")
	}
}

func TestRunRejectsBadChecksum(t *testing.T) {
	fakeGitHub(t, "v0.2.0", []byte("EVIL"), true)
	target := filepath.Join(t.TempDir(), "bin")
	os.WriteFile(target, []byte("OLD"), 0o755)
	if _, err := Run(context.Background(), "v0.1.0", target); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "OLD" {
		t.Fatal("binary must be untouched after a failed verification")
	}
}

func TestNoReleases(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	old, oldRepo := BaseURL, Repo
	BaseURL, Repo = srv.URL, "org/repo"
	defer func() { BaseURL, Repo = old, oldRepo }()
	if _, err := LatestTag(context.Background()); err == nil {
		t.Fatal("expected an error when there is no release")
	}
}
