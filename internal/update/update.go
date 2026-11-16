// Package update implements self-update from GitHub releases: it finds the
// latest release, downloads the archive for the running architecture, checks
// its sha256 against checksums.txt and atomically replaces the binary.
package update

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// BinaryName is the executable inside the release archive.
	BinaryName = "hestia-nginx-tui"
	maxBinary  = 200 << 20
)

// Variables (not constants) so tests can point them to a local server.
var (
	Repo    = "onlinedigital/hestia-nginx-tui"
	BaseURL = "https://github.com"
)

func client() *http.Client { return &http.Client{Timeout: 60 * time.Second} }

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", BinaryName) // generic client name, nothing about the user
	return client().Do(req)
}

// LatestTag returns the tag of the latest release (e.g. "v1.2.3"). It follows
// github.com/<repo>/releases/latest, which redirects to the tag page, so it
// needs no API token and has no rate limit worth worrying about.
func LatestTag(ctx context.Context) (string, error) {
	c := client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BaseURL+"/"+Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", BinaryName)
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode > 399 || loc == "" {
		return "", fmt.Errorf("no release found (HTTP %d)", resp.StatusCode)
	}
	i := strings.LastIndex(loc, "/tag/")
	if i < 0 {
		return "", fmt.Errorf("unexpected redirect %q", loc)
	}
	return loc[i+len("/tag/"):], nil
}

func parseVersion(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// IsNewer reports whether latest is a higher version than current.
func IsNewer(current, latest string) bool {
	c, l := parseVersion(current), parseVersion(latest)
	if l == nil {
		return false
	}
	if c == nil { // "dev" or unparsable: any release is newer
		return true
	}
	for i := 0; i < max(len(c), len(l)); i++ {
		var a, b int
		if i < len(c) {
			a = c[i]
		}
		if i < len(l) {
			b = l[i]
		}
		if b != a {
			return b > a
		}
	}
	return false
}

// AssetName is the archive published for a version on this architecture.
func AssetName(version string) (string, error) {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return fmt.Sprintf("%s_%s_linux_%s.tar.gz", BinaryName, strings.TrimPrefix(version, "v"), runtime.GOARCH), nil
	}
	return "", fmt.Errorf("no release build for architecture %s", runtime.GOARCH)
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("download too large")
	}
	return b, nil
}

func checksumFor(sums []byte, file string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == file {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%s not listed in checksums.txt", file)
}

func extract(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(archive)))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s not found in the archive", BinaryName)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == BinaryName {
			b, err := io.ReadAll(io.LimitReader(tr, maxBinary+1))
			if err != nil {
				return nil, err
			}
			if len(b) > maxBinary {
				return nil, errors.New("binary too large")
			}
			return b, nil
		}
	}
}

// Result describes what Run did.
type Result struct {
	Log     string
	Tag     string // release that is now installed (or already was)
	Updated bool
}

// Run updates the binary at target (the running executable when empty) to the
// latest release. current is the running version ("dev" for untagged builds).
func Run(ctx context.Context, current, target string) (res Result, err error) {
	var log strings.Builder
	say := func(f string, a ...any) { fmt.Fprintf(&log, f+"\n", a...) }
	defer func() { res.Log = log.String() }()

	tag, err := LatestTag(ctx)
	if err != nil {
		return res, err
	}
	res.Tag = tag
	say("latest release: %s (running: %s)", tag, current)
	if current != "dev" && !IsNewer(current, tag) {
		say("✓ already up to date")
		return res, nil
	}
	asset, err := AssetName(tag)
	if err != nil {
		return res, err
	}
	base := BaseURL + "/" + Repo + "/releases/download/" + tag + "/"
	say("downloading %s", asset)
	archive, err := download(ctx, base+asset, maxBinary)
	if err != nil {
		return res, err
	}
	sums, err := download(ctx, base+"checksums.txt", 1<<20)
	if err != nil {
		return res, err
	}
	want, err := checksumFor(sums, asset)
	if err != nil {
		return res, err
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return res, errors.New("checksum mismatch: refusing to install")
	}
	say("✓ sha256 verified")
	bin, err := extract(archive)
	if err != nil {
		return res, err
	}

	if target == "" {
		if target, err = os.Executable(); err != nil {
			return res, err
		}
		if target, err = filepath.EvalSymlinks(target); err != nil {
			return res, err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".hestia-nginx-tui-new-*")
	if err != nil {
		return res, fmt.Errorf("cannot write next to %s (%v): run as root", target, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		cleanup()
		return res, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return res, err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		cleanup()
		return res, err
	}
	if err := os.Rename(tmpName, target); err != nil {
		cleanup()
		return res, err
	}
	res.Updated = true
	say("✓ %s updated to %s", target, tag)
	return res, nil
}
