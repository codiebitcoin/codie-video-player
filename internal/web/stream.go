package web

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codie-video-player/internal/i18n"
	"codie-video-player/internal/video"
)

// The play sessions: each one turns what a source answered for an episode
// into HLS the browser plays. Streams already in the browser's shape go
// through a plain copy, which keeps up with the network; the rest are
// transcoded, which take minutes. The playlist grows as the file makes —
// the player starts at its start and waits for more, as an event stream.

// sessionFFmpegTimeout is how long one episode's making may take.
const sessionFFmpegTimeout = 30 * time.Minute

// sessionIdle is how long a session sits unwatched before its file and its
// ffmpeg go.
var sessionIdle = 10 * time.Minute

// maxConcurrentSessions is how many episodes may make at once; the rest
// queue, first asked first served.
const maxConcurrentSessions = 2

// session is one episode making or made.
type session struct {
	id       string
	srcID    string
	itemID   string
	item     video.Item
	ep       video.Episode
	at       int
	dir      string
	state    string // queued | making | ready | error | stopped
	err      string
	cancel   context.CancelFunc
	done     chan struct{} // closed exactly once, when the session is over
	done1    sync.Once
	mu       sync.Mutex
	lastSeen time.Time
	seen     func() // swapped in tests to skip the real clock
}

// sessionManager holds the sessions and the queue.
type sessionManager struct {
	dir string

	mu       sync.Mutex
	sessions map[string]*session
	sem      chan struct{}
}

// newSessionManager makes the manager and sweeps what a previous run left.
func newSessionManager(dir string) (*sessionManager, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if leftovers, err := os.ReadDir(dir); err == nil {
		for _, e := range leftovers {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
	m := &sessionManager{
		dir:      dir,
		sessions: map[string]*session{},
		sem:      make(chan struct{}, maxConcurrentSessions),
	}
	go m.janitor()
	return m, nil
}

// janitor stops the sessions nobody has asked for in a while.
func (m *sessionManager) janitor() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		m.janitorStep()
	}
}

// janitorStep is one sweep of the idle sessions.
func (m *sessionManager) janitorStep() {
	m.mu.Lock()
	var stale []*session
	for _, s := range m.sessions {
		s.mu.Lock()
		idle := time.Since(s.lastSeen)
		s.mu.Unlock()
		if idle > sessionIdle {
			stale = append(stale, s)
		}
	}
	m.mu.Unlock()
	for _, s := range stale {
		log.Printf("session %s: idle %s, stopping it", s.id, sessionIdle)
		m.Stop(s.id)
	}
}

// start asks the source for the episode's stream and starts making it HLS.
// It answers at once; the session's state says how far it has come.
func (m *sessionManager) start(so source, item video.Item, ep video.Episode, at int) (*session, error) {
	s := &session{
		id:       randomToken(),
		srcID:    so.id,
		itemID:   item.ID,
		item:     item,
		ep:       ep,
		at:       at,
		dir:      filepath.Join(m.dir, randomToken()),
		state:    "queued",
		done:     make(chan struct{}),
		lastSeen: time.Now(),
	}
	s.seen = func() {
		s.mu.Lock()
		s.lastSeen = time.Now()
		s.mu.Unlock()
	}
	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		s.fail(err.Error())
		return s, nil
	}
	stream, err := so.src.Play(item.ID, ep.ID)
	if err != nil {
		s.fail(videoProblem(err, i18n.T("This episode won’t play")))
		return s, nil
	}
	if stream == nil || stream.URL == "" {
		s.fail("这一集没有可播的流")
		return s, nil
	}
	go func() {
		// The queue: two episodes make at once, the rest in line, whoever
		// was asked first served first.
		m.sem <- struct{}{}
		defer func() { <-m.sem; s.closeDone() }()
		s.mu.Lock()
		if s.state == "stopped" {
			s.mu.Unlock()
			return
		}
		s.state = "making"
		s.mu.Unlock()
		m.make(s, stream)
	}()
	return s, nil
}

// closeDone lets whoever waits on the session go, however it ended.
func (s *session) closeDone() {
	s.done1.Do(func() { close(s.done) })
}

// fail puts a session in its error state and lets its waiters go; nothing
// was made worth keeping, so the folder goes too.
func (s *session) fail(msg string) {
	s.mu.Lock()
	s.state, s.err = "error", msg
	dir := s.dir
	s.mu.Unlock()
	os.RemoveAll(dir)
	s.closeDone()
}

// finish marks a session ready — its playlist grows from here.
func (s *session) finish() {
	s.mu.Lock()
	if s.state == "making" {
		s.state = "ready"
	}
	s.mu.Unlock()
	s.closeDone()
}

// make runs ffmpeg for the episode, copy first and transcode only when the
// stream isn't already in the browser's shape.
func (m *sessionManager) make(s *session, stream *video.Stream) {
	ffmpeg := findFFmpeg()
	if ffmpeg == "" {
		s.fail("服务器要装了 ffmpeg 才能把剧集转给浏览器")
		return
	}
	args := hlsArgs(stream, probePlayable(stream), s.dir)
	ctx, cancel := context.WithTimeout(context.Background(), sessionFFmpegTimeout)
	s.cancel = cancel
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var out bytes.Buffer
	cmd.Stderr = &out
	log.Printf("session %s: ffmpeg starts", s.id)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			s.mu.Lock()
			if s.state != "stopped" {
				s.state, s.err = "error", "这一集转得实在太慢,超时了"
			}
			s.mu.Unlock()
		} else {
			log.Printf("session %s: ffmpeg: %v: %s", s.id, err, tailOf(out.String()))
			s.fail("ffmpeg: " + tailOf(out.String()))
			return
		}
	}
	s.finish()
}

// stop kills a session's making and takes its file away.
func (m *sessionManager) Stop(id string) {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	s.mu.Lock()
	queued := s.state == "queued"
	if s.cancel != nil {
		s.cancel()
	}
	s.state = "stopped"
	s.mu.Unlock()
	// 排队中的会话没有 ffmpeg 可杀,等它的 goroutine 自己从队里出来;
	// 制作中的,取消信号一给 ffmpeg 就停,done 随即合上。
	if !queued {
		<-s.done
	} else {
		s.closeDone()
	}
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	os.RemoveAll(s.dir)
}

// get finds a session, touching its idle clock.
func (m *sessionManager) get(id string) *session {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s != nil {
		s.seen()
	}
	return s
}

// ---- the sessions' API ----

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Source string `json:"source"`
		Item   string `json:"item"`
		Ep     string `json:"ep"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil || q.Item == "" {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	so := s.sourceOf(q.Source)
	if so == nil {
		http.Error(w, `{"error":"no such source"}`, http.StatusNotFound)
		return
	}
	// The drama whole, for the episode's place in it, then the episode.
	v, err := so.src.Video(q.Item)
	if err != nil {
			s.writeJSON(w, map[string]any{"error": videoProblem(err, i18n.T("Couldn’t load this drama"))})
		return
	}
	item, ep, at := video.Item{ID: q.Item, Source: q.Source}, video.Episode{ID: q.Ep}, 0
	if v != nil {
		item = v.Item
		item.Source = q.Source
		for i := range v.Episodes {
			if v.Episodes[i].ID == q.Ep {
				ep, at = v.Episodes[i], i
				break
			}
		}
	}
	// It lands in the history at once, so 继续观看 knows it; how far comes
	// with the progress posts.
	s.lib.Watch(item, ep, at, 0, 0, false)
	sess, err := s.sessions.start(*so, item, ep, at)
	if err != nil {
		s.writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	sess.mu.Lock()
	state, serr := sess.state, sess.err
	sess.mu.Unlock()
	s.writeJSON(w, map[string]any{"id": sess.id, "state": state, "error": serr})
}

func (s *Server) handleSessionState(w http.ResponseWriter, r *http.Request) {
	sess := s.sessions.get(r.PathValue("id"))
	if sess == nil {
		http.Error(w, `{"error":"no such session"}`, http.StatusNotFound)
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	s.writeJSON(w, map[string]any{"id": sess.id, "state": sess.state, "error": sess.err})
}

func (s *Server) handleSessionStop(w http.ResponseWriter, r *http.Request) {
	s.sessions.Stop(r.PathValue("id"))
	w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	sess := s.sessions.get(r.PathValue("id"))
	if sess == nil {
		http.Error(w, `{"error":"no such session"}`, http.StatusNotFound)
		return
	}
	var q struct {
		Pos  float64 `json:"pos"`
		Dur  float64 `json:"dur"`
		Done bool    `json:"done"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	s.lib.SetProgress(sess.itemID, q.Pos, q.Dur, q.Done)
	w.Write([]byte(`{"ok":true}`))
}

// handleSessionFiles serves the session's HLS: the playlist, with its end
// marked when the making is over, and the segments under the same prefix,
// so the playlist's relative names land here.
func (s *Server) handleSessionFiles(w http.ResponseWriter, r *http.Request) {
	sess := s.sessions.get(r.PathValue("id"))
	if sess == nil {
		http.Error(w, `{"error":"no such session"}`, http.StatusNotFound)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/api/session/"+sess.id+"/")
	if rel == "" || strings.Contains(rel, "..") {
		http.Error(w, `{"error":"bad path"}`, http.StatusBadRequest)
		return
	}
	full := filepath.Join(sess.dir, rel)
	if filepath.Dir(full) != sess.dir {
		http.Error(w, `{"error":"bad path"}`, http.StatusBadRequest)
		return
	}
	if strings.HasSuffix(rel, ".m3u8") {
		b, err := os.ReadFile(full)
		if err != nil {
			// Not there yet: the queue may still hold this session.
			sess.mu.Lock()
			state, serr := sess.state, sess.err
			sess.mu.Unlock()
			if state == "error" {
				http.Error(w, `{"error":"`+serr+`"}`, http.StatusBadGateway)
				return
			}
			http.Error(w, `{"error":"not yet"}`, http.StatusNotFound)
			return
		}
		sess.mu.Lock()
		over := sess.state == "ready" || sess.state == "error" || sess.state == "stopped"
		sess.mu.Unlock()
		text := string(b)
		if over && !strings.Contains(text, "#EXT-X-ENDLIST") {
			text += "#EXT-X-ENDLIST\n"
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(text))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, full)
}

// ---- the making of the ffmpeg command ----

// probePlayable says whether the stream plays in a browser as it is: H.264
// with AAC, no bigger than 1080p.
func probePlayable(s *video.Stream) bool {
	ffprobe := findFFprobe()
	if ffprobe == "" {
		return false // 不知道,就当要转
	}
	videoArgs := []string{"-v", "error"}
	if hdr := ffmpegHeaders(s.Headers); hdr != "" {
		videoArgs = append(videoArgs, "-headers", hdr)
	}
	vargs := append(videoArgs, "-select_streams", "v:0", "-show_entries", "stream=codec_name,width,height", "-of", "json")
	out, err := exec.Command(ffprobe, append(vargs, s.URL)...).Output()
	if err != nil {
		return false
	}
	var v struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
	}
	if json.Unmarshal(out, &v) != nil || len(v.Streams) == 0 {
		return false
	}
	st := v.Streams[0]
	if st.CodecName != "h264" || st.Width > 1920 || st.Height > 1080 {
		return false
	}
	if s.AudioURL != "" {
		aargs := append(videoArgs, "-select_streams", "a:0", "-show_entries", "stream=codec_name", "-of", "json")
		aout, err := exec.Command(ffprobe, append(aargs, s.AudioURL)...).Output()
		if err != nil {
			return false
		}
		var a struct {
			Streams []struct {
				CodecName string `json:"codec_name"`
			} `json:"streams"`
		}
		if json.Unmarshal(aout, &a) != nil || len(a.Streams) == 0 || a.Streams[0].CodecName != "aac" {
			return false
		}
	}
	return true
}

// hlsArgs is what ffmpeg gets to make an episode into HLS at DIR/index.m3u8:
// the inputs (the stream's headers on each, two when picture and sound come
// apart), the maps, and a plain copy when the stream is already in the
// browser's shape, a transcode when it isn't.
func hlsArgs(s *video.Stream, asIs bool, dir string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
	hdr := ffmpegHeaders(s.Headers)
	input := func(url string) {
		if hdr != "" {
			args = append(args, "-headers", hdr)
		}
		// A CDN read that stalls would hang the whole pass otherwise.
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1",
			"-reconnect_delay_max", "5", "-rw_timeout", "15000000")
		args = append(args, "-i", url)
	}
	input(s.URL)
	if s.AudioURL != "" {
		input(s.AudioURL)
		args = append(args, "-map", "0:v:0", "-map", "1:a:0?")
	} else {
		args = append(args, "-map", "0:v:0?", "-map", "0:a:0?")
	}
	if asIs {
		args = append(args, "-c", "copy")
	} else {
		args = append(args,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=ceil(iw/2)*2:ceil(ih/2)*2",
			"-c:a", "aac", "-b:a", "160k")
	}
	playlist := filepath.Join(dir, "index.m3u8")
	return append(args,
		"-f", "hls", "-hls_time", "4", "-hls_list_size", "0",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_segment_filename", filepath.Join(dir, "seg%d.ts"), playlist)
}

// tailOf is the last line of ffmpeg's say, for the error the viewer sees.
func tailOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// ffmpegHeaders is the stream's headers as ffmpeg asks for them.
func ffmpegHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	var b strings.Builder
	for k, v := range headers {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\r\n")
	}
	return b.String()
}

// findFFmpeg finds the ffmpeg binary: on the path, or where the Macs keep it.
func findFFmpeg() string {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// findFFprobe finds ffprobe beside ffmpeg.
func findFFprobe() string {
	if p, err := exec.LookPath("ffprobe"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/ffprobe", "/usr/local/bin/ffprobe"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
