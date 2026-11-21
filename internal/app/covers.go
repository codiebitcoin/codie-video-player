package app

import (
	"bytes"
	"context"
	"io"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"codie-video-player/internal/source/huangguo"
)

// coverReferers is the page a picture host wants to be asked from — some
// don't give their pictures to whoever asks, only to their own site.
var coverReferers = map[string]string{
	"hdslb.com": "https://www.bilibili.com/",
	"ytimg.com": "https://www.youtube.com/",
	"ggpht.com": "https://www.youtube.com/",
}

// coverClient fetches the pictures that don't need the built-in source's
// ways.
var coverClient = &http.Client{Timeout: 20 * time.Second}

// fetchCover brings a cover's bytes home: the built-in source's own through
// its client, which decrypts what the site keeps scrambled, and the rest
// straight, with whatever Referer their host insists on.
func fetchCover(url string) ([]byte, error) {
	for host, referer := range coverReferers {
		if strings.Contains(url, host) {
			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Referer", referer)
			req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh) CodieVideoPlayer/0.1")
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
	}
	return huangguo.Cover(context.Background(), url)
}

// covers keeps the pictures of the cards, decoded and ready to draw. What the
// site keeps encrypted comes back as picture bytes from the client; this only
// decodes and holds them, the least recently used going first when it holds
// more than it should.
type coverCache struct {
	post func(func())

	mu   sync.Mutex
	bmps map[string]*ui.Bitmap
	busy map[string]bool
	at   map[string]int64 // when each was last asked for, for the drop
	seq  int64
	sem  chan struct{}
}

// coverLimit is how many pictures it keeps. A few hundred covers of a grid
// take some hundreds of megabytes of their pixels while they show and a
// fraction of that after; past the limit, the oldest go.
const coverLimit = 400

func newCovers(post func(func())) *coverCache {
	return &coverCache{
		post: post,
		bmps: map[string]*ui.Bitmap{},
		busy: map[string]bool{},
		at:   map[string]int64{},
		sem:  make(chan struct{}, 6),
	}
}

// get returns the picture at url, or nil while it loads, which it starts.
// Each picture that lands redraws the window, which asks for it again and
// gets it.
func (c *coverCache) get(url string) *ui.Bitmap {
	if url == "" {
		return nil
	}
	c.mu.Lock()
	if b := c.bmps[url]; b != nil {
		c.seq++
		c.at[url] = c.seq
		c.mu.Unlock()
		return b
	}
	if c.busy[url] {
		c.mu.Unlock()
		return nil
	}
	c.busy[url] = true
	c.mu.Unlock()
	go func() {
		c.sem <- struct{}{}
		data, err := fetchCover(url)
		<-c.sem
		var bmp *ui.Bitmap
		if err != nil {
			log.Printf("cover: %v", err)
		} else if img, _, derr := image.Decode(bytes.NewReader(data)); derr == nil {
			bmp = ui.NewBitmap(img)
		} else {
			log.Printf("cover: %v", derr)
		}
		c.post(func() {
			c.mu.Lock()
			delete(c.busy, url)
			if bmp != nil {
				c.bmps[url] = bmp
				c.seq++
				c.at[url] = c.seq
				for len(c.bmps) > coverLimit {
					oldest := ""
					var at int64
					for u, n := range c.at {
						if oldest == "" || n < at {
							oldest, at = u, n
						}
					}
					delete(c.bmps, oldest)
					delete(c.at, oldest)
				}
			}
			c.mu.Unlock()
		})
	}()
	return nil
}
