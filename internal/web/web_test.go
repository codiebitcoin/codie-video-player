package web

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codie-video-player/internal/source/huangguo"
	"codie-video-player/internal/video"
)

// mapFS is a static folder in memory.
type mapFS map[string][]byte

func (m *mapFS) Open(name string) (fs.File, error) {
	if b, ok := (*m)[name]; ok {
		return &memFile{name: name, Reader: strings.NewReader(string(b))}, nil
	}
	return nil, os.ErrNotExist
}

type memFile struct {
	name string
	*strings.Reader
}

func (f *memFile) Stat() (fs.FileInfo, error) { return memInfo{f.name, int64(f.Len())}, nil }
func (f *memFile) Close() error               { return nil }

type memInfo struct {
	name string
	size int64
}

func (i memInfo) Name() string       { return i.name }
func (i memInfo) Size() int64        { return i.size }
func (i memInfo) Mode() fs.FileMode  { return 0o444 }
func (i memInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }

// fakeSource 回一套固定答案,给 API 的测试当源。
type fakeSource struct{ id, name string }

func (f *fakeSource) Info() video.Info     { return video.Info{ID: f.id, Name: f.name} }
func (f *fakeSource) Can(what string) bool { return what == "videoSearch" }
func (f *fakeSource) Tabs() ([]video.Tab, error) {
	return []video.Tab{{ID: "hot", Label: "热门"}}, nil
}
func (f *fakeSource) List(tab string, p int) (*video.List, error) {
	return &video.List{Items: []video.Item{{ID: "d1", Title: "测试剧", Source: f.id}}, More: p < 2}, nil
}
func (f *fakeSource) Search(q string, off int) (*video.VideoResult, error) {
	return &video.VideoResult{Items: []video.Item{{ID: "d1", Title: "测试剧"}}}, nil
}
func (f *fakeSource) Video(id string) (*video.Video, error) {
	return &video.Video{Item: video.Item{ID: id, Title: "测试剧"}, Episodes: []video.Episode{{ID: "e1", Title: "第1集"}, {ID: "e2", Title: "第2集"}}}, nil
}
func (f *fakeSource) Play(id, ep string) (*video.Stream, error) {
	return &video.Stream{URL: "http://127.0.0.1:1/x.m3u8", Type: "hls"}, nil
}

// testServer is a logged-in server over a fake source, with its tmp in the
// test's folder.
func testServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	opt := Options{
		DataDir:    dir,
		PluginsDir: filepath.Join(dir, "plugins"),
		Version:    "test",
		AuthUser:   "codie",
		AuthPass:   "pw123",
		Static:     testFS(),
	}
	s, err := New(opt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.sources = []source{{id: "fake", name: "测试源", src: &fakeSource{id: "fake", name: "测试源"}}}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts, dir
}

// testFS is the least a static handler can live on.
func testFS() *mapFS {
	return &mapFS{"index.html": []byte("<html>web</html>")}
}

// login answers with a logged-in client.
func login(t *testing.T, ts *httptest.Server) *http.Client {
	t.Helper()
	res, err := http.Post(ts.URL+"/api/login", "application/json",
		string2body(`{"user":"codie","pass":"pw123"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("login said %d", res.StatusCode)
	}
	client := ts.Client()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("jar: %v", err)
	}
	u, _ := url.Parse(ts.URL)
	jar.SetCookies(u, res.Cookies())
	client.Jar = jar
	return client
}

func TestLoginGate(t *testing.T) {
	// 没登录的 API 一律 401,登录后放行;错密码不行。
	_, ts, _ := testServer(t)

	if res, err := http.Get(ts.URL + "/api/sources"); err != nil || res.StatusCode != 401 {
		t.Errorf("sources without login = %d, want 401", res.StatusCode)
	} else {
		res.Body.Close()
	}
	res, err := http.Post(ts.URL+"/api/login", "application/json", string2body(`{"user":"codie","pass":"wrong"}`))
	if err != nil || res.StatusCode != 401 {
		t.Errorf("wrong pass = %d, want 401", res.StatusCode)
	}
	client := login(t, ts)
	res2, err := client.Get(ts.URL + "/api/sources")
	if err != nil || res2.StatusCode != 200 {
		t.Errorf("sources after login = %d, want 200", res2.StatusCode)
	}
}

func TestRandomPasswordFallback(t *testing.T) {
	// 环境里不给账密时,自己生成一对,别让服务裸奔。
	a := newAuth("", "")
	if a.user == "" || a.pass == "" {
		t.Error("no pair made for an empty environment")
	}
	if len(a.pass) < 8 {
		t.Errorf("made-up password %q is too short", a.pass)
	}
}

func TestSearchGroupsBySource(t *testing.T) {
	// 搜索按源分组,条目带上了源的 id。
	s, ts, _ := testServer(t)
	_ = s
	client := login(t, ts)
	res, err := client.Get(ts.URL + "/api/search?q=test")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer res.Body.Close()
	var out struct {
		Groups []searchGroup `json:"groups"`
	}
	if json.NewDecoder(res.Body).Decode(&out) != nil || len(out.Groups) != 1 {
		t.Fatalf("search said %v", out)
	}
	g := out.Groups[0]
	if g.Source != "fake" || len(g.Items) != 1 || g.Items[0].Source != "fake" {
		t.Errorf("group = %+v", g)
	}
}

func TestVideoCarriesProgress(t *testing.T) {
	// 详情带着观看进度;写进去的进度读得回来(续播的数据链)。
	s, ts, _ := testServer(t)
	client := login(t, ts)
	s.lib.Watch(video.Item{ID: "d1", Source: "fake", Title: "测试剧"}, video.Episode{ID: "e1"}, 0, 42, 300, false)

	res, err := client.Get(ts.URL + "/api/video/fake/d1")
	if err != nil {
		t.Fatalf("video: %v", err)
	}
	defer res.Body.Close()
	var out struct {
		Progress *struct {
			Pos  float64 `json:"pos"`
			EpID string  `json:"epId"`
		} `json:"progress"`
		Followed bool `json:"followed"`
	}
	if json.NewDecoder(res.Body).Decode(&out) != nil || out.Progress == nil {
		t.Fatalf("video said %v", out)
	}
	if out.Progress.Pos != 42 || out.Progress.EpID != "e1" {
		t.Errorf("progress = %+v", out.Progress)
	}
}

func TestProgressEndpointWritesLibrary(t *testing.T) {
	// 播放会话的进度上报落进库;会话的流问不到时,报告错误状态而不是挂死。
	s, ts, _ := testServer(t)
	client := login(t, ts)

	res, err := client.Post(ts.URL+"/api/play", "application/json",
		string2body(`{"source":"fake","item":"d1","ep":"e1"}`))
	if err != nil {
		t.Fatalf("play: %v", err)
	}
	defer res.Body.Close()
	var out struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if json.NewDecoder(res.Body).Decode(&out) != nil || out.ID == "" {
		t.Fatalf("play said %v", out)
	}
	// 流指向 127.0.0.1:1,probe 大概率失败 → 会话走向 error;这个路径必须不挂死。
	sess := s.sessions.get(out.ID)
	<-sess.done
	sess.mu.Lock()
	state := sess.state
	sess.mu.Unlock()
	if state != "error" {
		t.Errorf("dead stream gave state %q, want error", state)
	}
	if _, err := os.Stat(sess.dir); !os.IsNotExist(err) {
		t.Errorf("failed session's dir is still there: %v", err)
	}

	// 进度接口按 itemID 写库(哪怕会话已经停了,历史也留得住)。
	s.lib.Watch(video.Item{ID: "d1", Source: "fake"}, video.Episode{ID: "e1"}, 0, 0, 0, false)
	res2, err := client.Post(ts.URL+"/api/session/"+out.ID+"/progress", "application/json",
		string2body(`{"pos":100,"dur":300,"done":false}`))
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	res2.Body.Close()
	if p := s.lib.ProgressOf("d1"); p == nil || p.Pos != 100 {
		t.Errorf("library progress = %+v, want pos 100", p)
	}
}

func TestSessionStopCleansUp(t *testing.T) {
	// 停掉的会话:目录删掉,管理表里除名。
	s, ts, _ := testServer(t)
	client := login(t, ts)
	res, _ := client.Post(ts.URL+"/api/play", "application/json", string2body(`{"source":"fake","item":"d1","ep":"e1"}`))
	var out struct {
		ID string `json:"id"`
	}
	json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()
	sess := s.sessions.get(out.ID)
	<-sess.done

	res2, err := client.Post(ts.URL+"/api/play", "application/json", string2body(`{"source":"fake","item":"d1","ep":"e1"}`))
	if err != nil {
		t.Fatalf("play again: %v", err)
	}
	res2.Body.Close()

	if err := del2(ts, client, "/api/session/"+out.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	s.sessions.mu.Lock()
	_, ok := s.sessions.sessions[out.ID]
	s.sessions.mu.Unlock()
	if ok {
		t.Error("stopped session stayed in the table")
	}
	if _, err := os.Stat(sess.dir); !os.IsNotExist(err) {
		t.Errorf("stopped session's dir survived: %v", err)
	}
}

func TestIdleSessionsAreReaped(t *testing.T) {
	// 没人看的会话到了期限会被收走:目录和表里都除名。
	s, ts, _ := testServer(t)
	client := login(t, ts)
	res, _ := client.Post(ts.URL+"/api/play", "application/json", string2body(`{"source":"fake","item":"d1","ep":"e1"}`))
	var out struct {
		ID string `json:"id"`
	}
	json.NewDecoder(res.Body).Decode(&out)
	res.Body.Close()
	sess := s.sessions.get(out.ID)
	<-sess.done

	old := sessionIdle
	sessionIdle = 30 * time.Millisecond
	t.Cleanup(func() { sessionIdle = old })
	time.Sleep(80 * time.Millisecond) // janitor 每分钟一巡,这里直接调一次
	s.sessions.janitorStep()

	s.sessions.mu.Lock()
	_, ok := s.sessions.sessions[out.ID]
	s.sessions.mu.Unlock()
	if ok {
		t.Error("idle session survived the reap")
	}
	if _, err := os.Stat(sess.dir); !os.IsNotExist(err) {
		t.Errorf("idle session's dir survived: %v", err)
	}
}

func TestHlsArgs(t *testing.T) {
	// 浏览器能直接吃的流走 copy;吃不了的才转码。画面声音分开的流,两路都进。
	playlist := "/tmp/x/index.m3u8"
	s := &video.Stream{URL: "http://x/v.m3u8", Type: "hls"}
	got := hlsArgs(s, true, "/tmp/x")
	if !hasArg(got, "-c", "copy") || !hasArg(got, "-f", "hls") || !hasArg(got, "-hls_list_size", "0") {
		t.Errorf("as-is = %v", got)
	}
	got = hlsArgs(s, false, playlist[len(playlist)-len("/tmp/x"):]+"/x")
	if hasArg(got, "-c", "copy") || !hasArg(got, "-c:v", "libx264") {
		t.Errorf("transcode = %v", got)
	}
	s2 := &video.Stream{URL: "http://x/v.mp4", AudioURL: "http://x/a.mp3"}
	got = hlsArgs(s2, true, "/tmp/x")
	if !hasArg(got, "-map", "0:v:0") || !hasArg(got, "-map", "1:a:0?") {
		t.Errorf("apart streams = %v, want both inputs mapped", got)
	}
	if !hasArg(got, "-hls_segment_filename", "/tmp/x/seg%d.ts") {
		t.Errorf("segments land elsewhere: %v", got)
	}
}

func hasArg(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}

// janitorOnce 手动巡一遍(测试里不真的等一分钟)。
func (m *sessionManager) janitorOnce() {
	m.janitorStep()
}

func string2body(s string) io.Reader {
	return strings.NewReader(s)
}

func del2(ts *httptest.Server, client *http.Client, path string) error {
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+path, nil)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

// slowSource 的搜索要 30 秒,测搜索期限。
type slowSource struct{ fakeSource }

func (s *slowSource) Search(q string, off int) (*video.VideoResult, error) {
	time.Sleep(30 * time.Second)
	return &video.VideoResult{}, nil
}

func TestSearchDeadline(t *testing.T) {
	// 一个源卡 30 秒,搜索也要在期限前后回来,卡住的源给空组。
	s, ts, _ := testServer(t)
	s.sources = append(s.sources, source{id: "slow", name: "慢源", src: &struct {
		fakeSource
	}{fakeSource{id: "slow", name: "慢源"}}})
	s.sources[1].src = &slowSource{fakeSource{id: "slow", name: "慢源"}}
	client := login(t, ts)
	begin := time.Now()
	res, err := client.Get(ts.URL + "/api/search?q=test")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer res.Body.Close()
	took := time.Since(begin)
	if took > 15*time.Second {
		t.Errorf("search took %s, past the deadline", took)
	}
	var out struct {
		Groups []searchGroup `json:"groups"`
	}
	json.NewDecoder(res.Body).Decode(&out)
	if len(out.Groups) != 2 {
		t.Errorf("groups = %d, want 2", len(out.Groups))
	}
}

func TestSearchWithRealHuangguoDeadline(t *testing.T) {
	// 真内置源(这台机器连不上它,拨号超时)卡在场,搜索也要按期回来。
	s, ts, _ := testServer(t)
	s.sources = append(s.sources, source{id: "huangguo", name: "黄果短剧", src: &huangguo.Source{}})
	client := login(t, ts)
	begin := time.Now()
	res, err := client.Get(ts.URL + "/api/search?q=test")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer res.Body.Close()
	if took := time.Since(begin); took > 15*time.Second {
		t.Errorf("search took %s", took)
	}
}
