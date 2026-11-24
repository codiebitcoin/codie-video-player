// Package huangguo is the client of the Huangguo short-drama site, which the
// app reads as its own page does: the catalog, the search, the ranks and the
// streams come from the site's API, which asks for nothing signed, and the
// covers come encrypted under a key the site publishes for its own page.
// Nothing of the site's is kept or served here. For study only.
package huangguo

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"codie-video-player/internal/proxy"
)

// Base is the site the client asks. Tests point it at a test server.
var Base = "https://huangguoai.com"

// UA is what the site's own page asks as: a phone's browser.
const UA = "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/120 Mobile Safari/537.36"

// The covers are encrypted under this key and IV, as the site's
// crypto-image-protocol.js publishes to its page.
var (
	imageKey = []byte("f5d965df75336270")
	imageIV  = []byte("97b60394abc2fbe1")
)

var httpc = &http.Client{
	Timeout:   30 * time.Second,
	Transport: proxiedTransport(),
}

// proxiedTransport is the client's ways: the default's, with the app's HTTP
// proxy in front of what it asks, the local addresses excepted.
func proxiedTransport() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = proxy.Func
	return t
}

// Item is a drama as the lists show it.
type Item struct {
	ID           int64
	Title        string
	Cover        string // the site's address; Cover reads the picture out of it
	Subtitle     string // who made it
	EpisodeCount int
	Finished     bool
	CreatedAt    string // as the site has it, 2026-10-12 21:38:58
	Score        string
	Intro        string
}

// Year is the year the drama came out, 0 when it doesn't say.
func (it Item) Year() int {
	y, _ := strconv.Atoi(strings.TrimSpace(it.CreatedAt[:min(4, len(it.CreatedAt))]))
	return y
}

// Category is one of the site's own kinds of drama.
type Category struct {
	ID   int64
	Name string
}

// Episode is one part of a drama.
type Episode struct {
	Num     int
	Title   string
	Seconds int
}

// Detail is a drama and its episodes.
type Detail struct {
	Item
	Episodes []Episode
}

// get asks the site and unwraps its {status, data} envelope.
func get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UA)
	req.Header.Set("Accept", "application/json")
	res, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, path)
	}
	return unwrap(io.LimitReader(res.Body, 32<<20), out)
}

// unwrap reads the site's answer: what its data carries, or the answer itself
// when it carries no envelope, in whatever of the shapes it uses.
func unwrap(r io.Reader, out any) error {
	var env struct {
		Status int             `json:"status"`
		Msg    string          `json:"msg"`
		Data   json.RawMessage `json:"data"`
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.Data != nil {
		if env.Status == 0 && len(env.Data) == 0 {
			return errors.New(env.Msg)
		}
		raw = env.Data
	}
	if err := json.Unmarshal(raw, out); err != nil {
		// Some answers carry a list where a page was expected, and the other
		// way; the caller's shapes below take both.
		return err
	}
	return nil
}

// page is what the catalog and the search answer: the dramas, and how far
// their pages go.
type page struct {
	Items      []rawItem `json:"items"`
	Pagination struct {
		Page  int `json:"page"`
		Pages int `json:"pages"`
	} `json:"pagination"`
	List []rawItem `json:"list"`
}

func (p *page) dramas() []rawItem {
	if len(p.Items) > 0 {
		return p.Items
	}
	return p.List
}

// rawItem is a drama as the site names its fields.
type rawItem struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Cover       string `json:"cover"`
	Description string `json:"description"`
	Synopsis    string `json:"synopsis"`
	Author      struct {
		Name string `json:"name"`
	} `json:"author"`
	EpisodeCount int    `json:"episode_count"`
	TotalEpisode int    `json:"total_episodes"`
	IsFinished   bool   `json:"is_finished"`
	CreatedAt    string `json:"created_at"`
	Score        any    `json:"score"`
}

func (r rawItem) item() Item {
	it := Item{
		ID:           r.ID,
		Title:        r.Title,
		Cover:        r.Cover,
		Subtitle:     r.Author.Name,
		EpisodeCount: r.EpisodeCount,
		Finished:     r.IsFinished,
		CreatedAt:    r.CreatedAt,
	}
	if it.EpisodeCount == 0 {
		it.EpisodeCount = r.TotalEpisode
	}
	if it.EpisodeCount > 0 {
		count := fmt.Sprintf("%d 集", it.EpisodeCount)
		if it.Subtitle != "" {
			it.Subtitle += " · " + count
		} else {
			it.Subtitle = count
		}
	}
	it.Intro = r.Description
	if it.Intro == "" {
		it.Intro = r.Synopsis
	}
	switch s := r.Score.(type) {
	case string:
		it.Score = s
	case float64:
		it.Score = strconv.FormatFloat(s, 'f', -1, 64)
	}
	return it
}

// Categories lists the site's own kinds of drama: 短剧, 漫剧 and so on.
func Categories(ctx context.Context) ([]Category, error) {
	var p struct {
		Items []struct {
			ID   any    `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
		List []struct {
			ID   any    `json:"id"`
			Name string `json:"name"`
		} `json:"list"`
	}
	if err := get(ctx, "/api/site/categories/video", &p); err != nil {
		return nil, err
	}
	var out []Category
	for _, raw := range append(p.Items, p.List...) {
		if raw.Name == "" {
			continue
		}
		switch id := raw.ID.(type) {
		case float64:
			out = append(out, Category{ID: int64(id), Name: raw.Name})
		case string:
			n, _ := strconv.ParseInt(id, 10, 64)
			out = append(out, Category{ID: n, Name: raw.Name})
		}
	}
	return out, nil
}

// Catalog is a page of the catalog, of a category when cateID says one;
// pages is how many pages the whole of it has.
func Catalog(ctx context.Context, cateID int64, pg int) ([]Item, int, error) {
	path := fmt.Sprintf("/api/videos?page=%d", pg)
	if cateID > 0 {
		path += "&cate_id=" + strconv.FormatInt(cateID, 10)
	}
	var p page
	if err := get(ctx, path, &p); err != nil {
		return nil, 0, err
	}
	items := make([]Item, 0, len(p.dramas()))
	for _, r := range p.dramas() {
		items = append(items, r.item())
	}
	return items, p.Pagination.Pages, nil
}

// Rank is one of the site's lists, the hottest dramas first.
func Rank(ctx context.Context, kind string) ([]Item, error) {
	var p page
	if err := get(ctx, "/api/ranks/"+kind, &p); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(p.dramas()))
	for _, r := range p.dramas() {
		items = append(items, r.item())
	}
	return items, nil
}

// Search is what the site's search says of what's asked, a page at a time.
func Search(ctx context.Context, q string, pg int) ([]Item, error) {
	var p page
	if err := get(ctx, "/api/search?q="+queryEscape(q)+"&page="+strconv.Itoa(pg), &p); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(p.dramas()))
	for _, r := range p.dramas() {
		items = append(items, r.item())
	}
	return items, nil
}

// Drama is a drama and its episodes, as the site's own detail page reads it.
// A drama whose episodes it doesn't list gets one to each of the count it
// gives, numbered.
func Drama(ctx context.Context, id int64) (*Detail, error) {
	var raw struct {
		rawItem
		Episodes []struct {
			Num      int    `json:"ep_num"`
			Title    string `json:"title"`
			Duration int    `json:"duration"`
		} `json:"episodes"`
	}
	if err := get(ctx, "/api/videos/"+strconv.FormatInt(id, 10), &raw); err != nil {
		return nil, err
	}
	d := &Detail{Item: raw.item()}
	for _, ep := range raw.Episodes {
		title := ep.Title
		if title == "" {
			title = fmt.Sprintf("第%d集", ep.Num)
		}
		d.Episodes = append(d.Episodes, Episode{Num: ep.Num, Title: title, Seconds: ep.Duration})
	}
	if len(d.Episodes) == 0 {
		for n := 1; n <= d.EpisodeCount; n++ {
			d.Episodes = append(d.Episodes, Episode{Num: n, Title: fmt.Sprintf("第%d集", n)})
		}
	}
	return d, nil
}

// Play is the HLS address an episode plays through, the signature it needs
// already on it. A drama that gives nothing for the episode says so.
func Play(ctx context.Context, id int64, ep int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/videos/%d/episodes/%d/playback", Base, id, ep), strings.NewReader("{}"))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UA)
	req.Header.Set("Content-Type", "application/json")
	res, err := httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var env struct {
		Status int    `json:"status"`
		Msg    string `json:"msg"`
		Data   struct {
			Streams struct {
				Main    string `json:"main"`
				HEVC    string `json:"hevc"`
				Preview string `json:"preview"`
			} `json:"streams"`
		} `json:"data"`
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err := json.Unmarshal(body, &env); err != nil {
		return "", err
	}
	if url := env.Data.Streams.Main; url != "" {
		return url, nil
	}
	if url := env.Data.Streams.HEVC; url != "" {
		return url, nil
	}
	if url := env.Data.Streams.Preview; url != "" {
		return url, nil
	}
	if env.Msg != "" {
		return "", errors.New(env.Msg)
	}
	return "", errors.New("no stream for this episode")
}

// Cover reads the picture of a cover address out: the site hands most of them
// over encrypted, under the key it publishes, and some as plain pictures
// already. What comes back is picture bytes either way, or an error.
func Cover(ctx context.Context, url string) ([]byte, error) {
	if url == "" {
		return nil, errors.New("no cover")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UA)
	res, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if isPicture(data) {
		return data, nil
	}
	plain, err := decryptImage(data)
	if err != nil {
		return nil, err
	}
	if !isPicture(plain) {
		return nil, errors.New("cover neither plain nor encrypted picture")
	}
	return plain, nil
}

func isPicture(b []byte) bool {
	return bytes.HasPrefix(b, []byte{0xff, 0xd8}) || bytes.HasPrefix(b, []byte{0x89, 'P', 'N', 'G'})
}

// decryptImage takes the blocks of an encrypted cover back to the picture,
// exactly as the site's page does: CBC, and no padding taken off.
func decryptImage(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(imageKey)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("encrypted cover isn't whole blocks")
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, imageIV).CryptBlocks(out, data)
	return out, nil
}

func queryEscape(s string) string {
	return url.QueryEscape(strings.TrimSpace(s))
}
