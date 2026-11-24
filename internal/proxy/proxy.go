// Package proxy holds the app's one HTTP proxy setting: what the sources',
// the plugins' and the app's own requests to the world go through. What
// stays local — this machine, the paired device, the intranet behind it —
// goes direct, whatever the setting says. Nothing here reads the
// environment's own proxy variables: the setting is the app's alone.
package proxy

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

// setting is the proxy as the listener typed it, empty for none.
var setting atomic.Value // string

// parsed is the setting as an address, nil for none or not one.
var parsed atomic.Pointer[url.URL]

// Set takes the proxy address, "10.0.0.14:7890" or "http://10.0.0.14:7890"
// alike. An empty one takes the proxy away, and everything goes direct.
func Set(addr string) {
	setting.Store(strings.TrimSpace(addr))
	if u := parse(addr); u != nil {
		parsed.Store(u)
	} else {
		parsed.Store(nil)
	}
}

// parse reads a proxy address, "host:port" taken as http, as its own kind of
// URL: scheme http or https, a host, a port. Anything else is no proxy.
func parse(addr string) *url.URL {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil
	}
	return u
}

// Get is the proxy address as set, empty for none.
func Get() string {
	s, _ := setting.Load().(string)
	return s
}

// Func is the Proxy a transport asks of every request: the setting's proxy
// for the world's addresses, nothing for the local ones, nothing at all
// when none is set.
func Func(req *http.Request) (*url.URL, error) {
	u := parsed.Load()
	if u == nil {
		return nil, nil
	}
	if local(req.URL.Hostname()) {
		return nil, nil
	}
	return u, nil
}

// local says whether an address stays on this machine or its intranet, and
// so goes direct however the proxy is set: this machine's own names and
// addresses, the private ranges, and the link-local ones. The paired codie
// device sits on the intranet, which is why it goes direct.
func local(host string) bool {
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	return false
}

// EnvVars is the proxy as environment variables for a program the app
// starts, such as yt-dlp or ffmpeg, whose fetches then go through it too.
// Nothing local is named in no_proxy, so those still go direct. With no
// proxy set, there is nothing to say.
func EnvVars() []string {
	u := parsed.Load()
	if u == nil {
		return nil
	}
	a := u.String()
	return []string{
		"HTTP_PROXY=" + a,
		"HTTPS_PROXY=" + a,
		"http_proxy=" + a,
		"https_proxy=" + a,
		"NO_PROXY=" + noProxy,
		"no_proxy=" + noProxy,
	}
}

// noProxy is what EnvVars tells the programs to keep direct.
const noProxy = "localhost,127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local"
