package huangguo

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// server answers each path with what the site answers, and Base points at it.
func server(t *testing.T, paths map[string]string) {
	t.Helper()
	old := Base
	mux := http.NewServeMux()
	for path, body := range paths {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		})
	}
	Base = httptest.NewServer(mux).URL
	t.Cleanup(func() {
		Base = old
	})
}

// The catalog comes out as dramas with what the site says of each, and the
// page count with it.
func TestCatalog(t *testing.T) {
	server(t, map[string]string{
		"/api/videos": `{"status":1,"data":{"items":[
			{"id":12,"title":"神瞳觉醒","cover":"https://pic.example/c","author":{"name":"黄果官方"},
			 "episode_count":21,"is_finished":true,"created_at":"2026-08-07 19:16:47","score":8.9,
			 "description":"当死去的父亲留下笑话。"},
			{"id":13,"title":"没有简介的剧","total_episodes":6}
		],"pagination":{"page":1,"pages":92,"size":20,"total":1824}}}`,
	})
	items, pages, err := Catalog(context.Background(), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if pages != 92 || len(items) != 2 {
		t.Fatalf("catalog: %d items, %d pages", len(items), pages)
	}
	it := items[0]
	if it.ID != 12 || it.Title != "神瞳觉醒" || it.EpisodeCount != 21 || !it.Finished ||
		it.Subtitle != "黄果官方 · 21 集" || it.Score != "8.9" || it.Year() != 2026 {
		t.Errorf("first drama: %+v", it)
	}
	if items[1].EpisodeCount != 6 || items[1].Subtitle == "" {
		t.Errorf("the count of a drama that only says total_episodes: %+v", items[1])
	}
}

// A catalog through a category's filter asks with the cate_id.
func TestCatalogByCategory(t *testing.T) {
	var asked string
	old := Base
	Base = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.RawQuery
		w.Write([]byte(`{"status":1,"data":{"items":[],"pagination":{"page":1,"pages":1}}}`))
	})).URL
	t.Cleanup(func() { Base = old })
	if _, _, err := Catalog(context.Background(), 24, 2); err != nil {
		t.Fatal(err)
	}
	if asked != "page=2&cate_id=24" {
		t.Errorf("asked %q", asked)
	}
}

// The search answers the dramas of what was asked, the query escaped.
func TestSearch(t *testing.T) {
	server(t, map[string]string{
		"/api/search": `{"status":1,"data":{"items":[{"id":1,"title":"爱"}]}}`,
	})
	items, err := Search(context.Background(), "爱 & 幻", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "爱" {
		t.Errorf("search: %+v", items)
	}
}

// A drama's page comes with its episodes, numbered when the site lists none.
func TestDrama(t *testing.T) {
	server(t, map[string]string{
		"/api/videos/12": `{"status":1,"data":{"id":12,"title":"神瞳觉醒","description":"当死去的父亲留下一枚神秘玻璃珠",
			"episodes":[{"ep_num":1,"title":"第1集","duration":207},{"ep_num":2,"title":"第二集","duration":181}]}}`,
		"/api/videos/13": `{"status":1,"data":{"id":13,"title":"六集无剧集的剧","episode_count":6}}`,
	})
	d, err := Drama(context.Background(), 12)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "神瞳觉醒" || len(d.Episodes) != 2 || d.Episodes[1].Title != "第二集" || d.Episodes[1].Seconds != 181 {
		t.Errorf("drama: %+v", d)
	}
	if d, err = Drama(context.Background(), 13); err != nil || len(d.Episodes) != 6 || d.Episodes[5].Num != 6 {
		t.Errorf("episodes of a count alone: %+v, %v", d, err)
	}
}

// The stream of an episode is the site's main address, falling back through
// hevc and preview; one it doesn't give says why.
func TestPlay(t *testing.T) {
	server(t, map[string]string{
		"/api/videos/12/episodes/1/playback": `{"status":1,"data":{"streams":{"main":"https://cdn.example/main.m3u8?auth_key=x"}}}`,
		"/api/videos/12/episodes/2/playback": `{"status":1,"data":{"streams":{"hevc":"https://cdn.example/hevc.m3u8"}}}`,
		"/api/videos/12/episodes/3/playback": `{"status":0,"msg":"视频不存在或不可播放"}`,
	})
	for ep, want := range map[int]string{1: "https://cdn.example/main.m3u8?auth_key=x", 2: "https://cdn.example/hevc.m3u8"} {
		got, err := Play(context.Background(), 12, ep)
		if err != nil || got != want {
			t.Errorf("play %d: %q, %v", ep, got, err)
		}
	}
	if _, err := Play(context.Background(), 12, 3); err == nil || err.Error() != "视频不存在或不可播放" {
		t.Errorf("playing what isn't there: %v", err)
	}
}

// The categories come out as the site's own kinds of drama.
func TestCategories(t *testing.T) {
	server(t, map[string]string{
		"/api/site/categories/video": `{"status":1,"data":{"items":[
			{"id":27,"name":"AI魔改"},{"id":26,"name":"AI换脸"},{"id":25,"name":"漫剧"},{"id":24,"name":"短剧"}]}}`,
	})
	cats, err := Categories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 4 || cats[0] != (Category{ID: 27, Name: "AI魔改"}) {
		t.Errorf("categories: %+v", cats)
	}
}

// A cover that comes encrypted goes back to the picture under the key the
// site publishes, and one that comes plain goes as it is.
func TestCover(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	block, _ := aes.NewCipher(imageKey)
	enc := make([]byte, len(jpeg))
	cipher.NewCBCEncrypter(block, imageIV).CryptBlocks(enc, jpeg)
	server(t, map[string]string{
		"/plain.jpeg":   string(jpeg),
		"/encrypted":    string(enc),
		"/garbage":      "not a picture at all, really",
		"/encryptedbad": string(enc[:8]),
	})
	for path, wantErr := range map[string]bool{"/plain.jpeg": false, "/encrypted": false, "/garbage": true, "/encryptedbad": true} {
		_, err := Cover(context.Background(), Base+path)
		if (err != nil) != wantErr {
			t.Errorf("%s: %v", path, err)
		}
	}
}

// What went wrong reads as it is, for the page's error state.
func TestErrors(t *testing.T) {
	server(t, map[string]string{"/api/videos": "not json at all"})
	if _, _, err := Catalog(context.Background(), 0, 1); err == nil {
		t.Error("catalog of nonsense answered nothing")
	}
	if _, err := Rank(context.Background(), "hot"); err == nil && !errors.Is(err, err) {
		t.Error("unreachable")
	}
}
