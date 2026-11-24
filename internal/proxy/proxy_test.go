package proxy

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in     string
		want   string // the scheme://host:port of it, "" for none
	}{
		{"", ""},
		{"  ", ""},
		{"10.10.32.14:7890", "http://10.10.32.14:7890"},
		{"http://10.10.32.14:7890", "http://10.10.32.14:7890"},
		{"https://proxy.lan:8443", "https://proxy.lan:8443"},
		{"ftp://nope:1", ""},
		{"http://", ""},
		{"just a word", ""},
	}
	for _, c := range cases {
		u := parse(c.in)
		got := ""
		if u != nil {
			got = u.String()
		}
		if got != c.want {
			t.Errorf("parse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLocal(t *testing.T) {
	local := []string{"localhost", "127.0.0.1", "::1", "192.168.1.5", "10.1.2.3", "172.16.0.9", "printer.local", ""}
	for _, h := range local {
		if !local_(h) {
			t.Errorf("local(%q) = false, want true", h)
		}
	}
	world := []string{"huangguoai.com", "api.bilibili.com", "8.8.8.8", "www.youtube.com"}
	for _, h := range world {
		if local_(h) {
			t.Errorf("local(%q) = true, want false", h)
		}
	}
}

// local_ is the unexported one under test.
var local_ = local

func TestFunc(t *testing.T) {
	defer Set("")
	req := func(host string) *http.Request {
		r, _ := http.NewRequest("GET", "https://"+host+"/x", nil)
		return r
	}
	ask := func(host string) string {
		u, err := Func(req(host))
		if err != nil {
			t.Fatalf("Func(%s): %v", host, err)
		}
		if u == nil {
			return ""
		}
		return u.Host
	}
	// Nothing set: everything direct.
	Set("")
	if got := ask("api.bilibili.com"); got != "" {
		t.Errorf("no proxy set: %q went through %q", "api.bilibili.com", got)
	}
	// Set: the world through it, the local ones still direct.
	Set("10.10.32.14:7890")
	if got := ask("api.bilibili.com"); got != "10.10.32.14:7890" {
		t.Errorf("proxy set: %q went through %q", "api.bilibili.com", got)
	}
	for _, h := range []string{"127.0.0.1", "192.168.1.1", "10.0.0.2", "nas.local"} {
		if got := ask(h); got != "" {
			t.Errorf("proxy set: local %q went through %q", h, got)
		}
	}
	// A broken setting is none at all.
	Set("not a proxy")
	if got := ask("api.bilibili.com"); got != "" {
		t.Errorf("broken setting: %q went through %q", "api.bilibili.com", got)
	}
}

func TestEnvVars(t *testing.T) {
	defer Set("")
	Set("")
	if env := EnvVars(); env != nil {
		t.Errorf("no proxy set: env vars %v", env)
	}
	Set("10.10.32.14:7890")
	env := EnvVars()
	if len(env) == 0 {
		t.Fatal("proxy set: no env vars")
	}
	join := ""
	for _, e := range env {
		join += e + " "
	}
	if _, err := url.Parse("http://10.10.32.14:7890"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HTTP_PROXY=http://10.10.32.14:7890", "NO_PROXY=localhost,"} {
		found := false
		for _, e := range env {
			if e == want || strings.HasPrefix(e, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("env vars missing %s…: %v", want, join)
		}
	}
}
