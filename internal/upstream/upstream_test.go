package upstream

import (
	"strings"
	"testing"
)

func TestRenderParseRoundTrip(t *testing.T) {
	u := Upstream{
		Name:              "app_backend",
		Servers:           ParseServers("10.0.0.5:8080, 10.0.0.6:8080 weight=2 backup, unix:/run/app.sock"),
		Method:            "least_conn",
		Keepalive:         32,
		KeepaliveTimeout:  "60s",
		KeepaliveRequests: 1000,
	}
	if err := u.Validate(); err != nil {
		t.Fatal(err)
	}
	text := u.Render()
	for _, want := range []string{"upstream app_backend {", "least_conn;", "server 10.0.0.6:8080 weight=2 backup;", "keepalive 32;", "keepalive_timeout 60s;", "keepalive_requests 1000;"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	got, ok := Parse(text)
	if !ok || got.Complex {
		t.Fatalf("parse failed ok=%v complex=%v", ok, got.Complex)
	}
	if got.Render() != text {
		t.Fatalf("round trip differs:\n%s\n---\n%s", text, got.Render())
	}
}

func TestParseKeepsUnknownDirectives(t *testing.T) {
	src := "upstream x {\n\tzone x 64k;\n\tserver 1.2.3.4:80 max_fails=3;\n\tsticky cookie srv_id;\n\tkeepalive 8;\n}\n"
	u, ok := Parse(src)
	if !ok {
		t.Fatal("no parse")
	}
	if len(u.Extra) != 2 || u.Keepalive != 8 || len(u.Servers) != 1 {
		t.Fatalf("%+v", u)
	}
	out := u.Render()
	if !strings.Contains(out, "zone x 64k;") || !strings.Contains(out, "sticky cookie srv_id;") {
		t.Fatalf("unknown directives lost:\n%s", out)
	}
}

func TestComplexFile(t *testing.T) {
	u, ok := Parse("upstream a { server 1.1.1.1; }\nmap $a $b { default 1; }\n")
	if !ok || !u.Complex {
		t.Fatal("extra statements must mark the file as complex")
	}
}

func TestValidate(t *testing.T) {
	if (Upstream{Name: "bad name", Servers: []Server{{Addr: "1.1.1.1:80"}}}).Validate() == nil {
		t.Fatal("space in name must fail")
	}
	if (Upstream{Name: "a"}).Validate() == nil {
		t.Fatal("no servers must fail")
	}
	if (Upstream{Name: "a", Servers: []Server{{Addr: "1.1.1.1:80;}"}}}).Validate() == nil {
		t.Fatal("injection must fail")
	}
}

func TestSnippets(t *testing.T) {
	s := Snippet("app", SnipKeepalive)
	if !strings.Contains(s, "proxy_pass http://app;") || !strings.Contains(s, `Connection ""`) || !strings.Contains(s, "proxy_http_version 1.1;") {
		t.Fatal(s)
	}
	if !strings.HasPrefix(Snippet("app", SnipLocation), "location / {") {
		t.Fatal("location snippet")
	}
}
