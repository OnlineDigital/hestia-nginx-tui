package hestia

import (
	"os"
	"strings"
	"testing"
)

func TestSplitLaravel(t *testing.T) {
	raw, err := os.ReadFile("testdata/laravel.tpl")
	if err != nil {
		t.Fatal(err)
	}
	inc := "\tinclude /etc/hestia-nginx-tui/sites/%domain%/locations.con[f];\n"
	sp, err := SplitServer(string(raw), inc)
	if err != nil {
		t.Fatal(err)
	}
	// root, index, favicon, robots, dotfiles, "/" => 6 site specific statements
	if sp.Count != 6 {
		t.Fatalf("want 6 extracted statements, got %d", sp.Count)
	}
	tpl := sp.Template
	if !strings.Contains(tpl, "include /etc/hestia-nginx-tui/sites/%domain%/locations.con[f];") {
		t.Fatal("include missing")
	}
	// The skeleton keeps Hestia plumbing and drops everything site specific.
	for _, must := range []string{"listen      %ip%:%web_port%;", "server_name", "access_log", "error_log",
		"nginx.forcessl.conf*", "location /error/", "location /vstats/", "phpmyadmin.inc", "nginx.conf_*"} {
		if !strings.Contains(tpl, must) {
			t.Fatalf("skeleton lost %q:\n%s", must, tpl)
		}
	}
	for _, gone := range []string{"root ", "index ", "favicon", "robots", "livewire", "fastcgi_pass", "try_files"} {
		if strings.Contains(tpl, gone) {
			t.Fatalf("skeleton still contains %q:\n%s", gone, tpl)
		}
	}
	seed := sp.Seed
	for _, must := range []string{"root        %docroot%/public;", "index       index.php", "location = /favicon.ico",
		"fastcgi_pass %backend_lsnr%;", "livewire", `location ~ /\.(?!well-known\/)`} {
		if !strings.Contains(seed, must) {
			t.Fatalf("seed lacks %q:\n%s", must, seed)
		}
	}
	if strings.Contains(seed, "location /error/") || strings.Contains(seed, "listen") || strings.Contains(seed, "\tlocation = /favicon") {
		t.Fatal("seed must not contain skeleton directives and must be dedented")
	}
	if strings.Count(seed, "{") != strings.Count(seed, "}") {
		t.Fatal("seed braces unbalanced")
	}
	// root and index are adjacent directives: no blank line between them
	if !strings.Contains(seed, ";\nindex") {
		t.Fatalf("directives should be packed together:\n%s", seed)
	}
}

func TestSplitStplKeepsSslPlumbing(t *testing.T) {
	raw, _ := os.ReadFile("testdata/laravel.stpl")
	sp, err := SplitServer(string(raw), "\tinclude X;\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{"listen      %ip%:%web_ssl_port% ssl;", "ssl_certificate     %ssl_pem%;", "$anti_replay = 307",
		"nginx.hsts.conf*", "nginx.ssl.conf_*", "include X;"} {
		if !strings.Contains(sp.Template, must) {
			t.Fatalf("stpl skeleton lost %q:\n%s", must, sp.Template)
		}
	}
	// proxy_hide_header Upgrade is site specific on purpose: it blocks websockets
	if strings.Contains(sp.Template, "proxy_hide_header") || !strings.Contains(sp.Seed, "proxy_hide_header Upgrade") {
		t.Fatal("proxy_hide_header must move to the editable part")
	}
}

func TestParserHandlesQuotesAndComments(t *testing.T) {
	src := "server {\n  # location { in comment\n  location /a { return 200 \"}{\"; }\n  location /error/ { alias /x; }\n}\n"
	sp, err := SplitServer(src, "  include x;\n")
	if err != nil || sp.Count != 1 {
		t.Fatalf("n=%d err=%v", sp.Count, err)
	}
	if !strings.Contains(sp.Seed, `return 200 "}{";`) || !strings.Contains(sp.Template, "/error/") {
		t.Fatalf("bad split:\n%s\n---\n%s", sp.Template, sp.Seed)
	}
}

func TestNothingToExtractStillInsertsInclude(t *testing.T) {
	sp, err := SplitServer("server {\n\tlisten 80;\n}\n", "\tinclude X;\n")
	if err != nil || sp.Count != 0 || !strings.Contains(sp.Template, "include X;") {
		t.Fatalf("%v %+v", err, sp)
	}
}
