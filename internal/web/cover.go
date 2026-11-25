package web

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	)

// pluginHeaders merges what every known plugin wants for the URL, for its
// own hosts only.
func (s *Server) pluginHeaders(u *url.URL) map[string]string {
	var out map[string]string
	for _, p := range s.plugins {
		for k, v := range p.HeadersFor(u, "image") {
			if out == nil {
				out = map[string]string{}
			}
			if _, set := out[k]; !set {
				out[k] = v
			}
		}
	}
	return out
}

// coverReferers is the page a picture host wants to be asked from — some
// don't give their pictures to whoever asks, only to their own site.
var coverReferers = map[string]string{
	"hdslb.com": "https://www.bilibili.com/",
	"ytimg.com": "https://www.youtube.com/",
	"ggpht.com": "https://www.youtube.com/",
	"nimg.jp":   "https://www.nicovideo.jp/",
}

// coverClient fetches the pictures that don't need the built-in source's
// ways.
var coverClient = &http.Client{Timeout: 20 * time.Second}

// coverCache keeps the covers' bytes, the least recently used going first
// when it holds more than it should. One fetch per URL, however many ask at
// once.
type coverCache struct {
	// headers answers the extra headers a plugin wants for its own picture
	// hosts, merged over every plugin the server knows.
	headers func(u *url.URL) map[string]string

	mu    sync.Mutex
	bytes map[string][]byte
	busy  map[string]*sync.WaitGroup
	errs  map[string]error
	at    map[string]int64
	seq   int64
}

// coverLimit is how many covers it keeps; past it, the oldest go.
const coverLimit = 256

func newCoverCache(headers func(u *url.URL) map[string]string) *coverCache {
	return &coverCache{
		headers: headers,
		bytes:   map[string][]byte{},
		busy:    map[string]*sync.WaitGroup{},
		errs:    map[string]error{},
		at:      map[string]int64{},
	}
}

// get answers the cover's bytes, fetching them the once.
func (c *coverCache) get(rawurl string) ([]byte, error) {
	c.mu.Lock()
	if b, ok := c.bytes[rawurl]; ok {
		c.seq++
		c.at[rawurl] = c.seq
		c.mu.Unlock()
		return b, nil
	}
	if wg, ok := c.busy[rawurl]; ok {
		c.mu.Unlock()
		wg.Wait()
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.bytes[rawurl], c.errs[rawurl]
	}
	var wg sync.WaitGroup
	wg.Add(1)
	c.busy[rawurl] = &wg
	c.mu.Unlock()

	data, err := c.fetchCover(rawurl)

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.busy, rawurl)
	wg.Done()
	if err != nil {
		c.errs[rawurl] = err
		return nil, err
	}
	delete(c.errs, rawurl)
	c.bytes[rawurl] = data
	c.seq++
	c.at[rawurl] = c.seq
	for len(c.bytes) > coverLimit {
		oldest := ""
		var at int64
		for u, n := range c.at {
			if oldest == "" || n < at {
				oldest, at = u, n
			}
		}
		delete(c.bytes, oldest)
		delete(c.at, oldest)
	}
	return data, nil
}

// fetchCover brings a cover's bytes home: the built-in source's own through
// its client, which decrypts what the site keeps scrambled, and the rest
// straight, with whatever their host insists on.
func (c *coverCache) fetchCover(rawurl string) ([]byte, error) {
	u, err := url.Parse(rawurl)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("bad cover url")
	}
		headers := c.headers(u)
		for host, referer := range coverReferers {
			if strings.Contains(rawurl, host) {
				if headers == nil {
					headers = map[string]string{}
				}
				if _, ok := headers["Referer"]; !ok {
					headers["Referer"] = referer
				}
				break
			}
		}
		req, err := http.NewRequest(http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh) CodieVideoPlayer/0.1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := coverClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("cover answered " + res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, 32<<20))
}

// handleCover answers a cover's bytes, cached.
func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	rawurl := r.URL.Query().Get("u")
	if rawurl == "" {
		http.Error(w, `{"error":"no url"}`, http.StatusBadRequest)
		return
	}
	data, err := s.covers.get(rawurl)
	if err != nil {
		http.Error(w, `{"error":"cover failed"}`, http.StatusBadGateway)
		return
	}
	// The bytes' own shape says jpeg or png; the sources only ever deal in
	// those two, so the sniff is safe.
	ct := "image/jpeg"
	if strings.HasPrefix(string(data), "\x89PNG") {
		ct = "image/png"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "max-age=86400")
	w.Write(data)
}
