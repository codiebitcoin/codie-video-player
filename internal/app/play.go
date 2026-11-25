package app

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	mygovideo "github.com/egoist/mygo/ui/video"

	"codie-video-player/internal/i18n"
	"codie-video-player/internal/video"
)

// How an episode comes to the player: the site gives an HLS address, which
// macOS plays as it is. Where it can't — a playlist, or picture and sound
// apart — ffmpeg turns it into a fragmented mp4 served from this machine, and
// the player starts on it at once: the whole episode no longer has to be made
// before the first second shows.

// ffmpegTimeout is how long one pass of ffmpeg may stay silent and idle.
const ffmpegTimeout = 10 * time.Minute

// openEpisodeStream opens what an episode plays through. The second answer is
// a file made for the player, which the caller gives up when another comes.
// The third says the start was already baked into the stream, so the player
// mustn't seek to it again. It's a variable so tests can put a fake in.
var openEpisodeStream = func(s *video.Stream, startS float64) (*ui.VideoPlayer, string, bool, error) {
	switch {
	case s.Type == "file":
		// A file on this machine plays as it is; the proxy never sees it.
		p, err := mygovideo.Open(s.URL)
		return p, "", false, err
	case s.AudioURL != "", len(s.Headers) > 0, s.Type == "hls" && runtime.GOOS != "darwin":
		return streamThroughFFmpeg(s, startS)
	default:
		p, err := mygovideo.OpenURL(s.URL)
		return p, "", false, err
	}
}

// streamThroughFFmpeg runs ffmpeg into a fragmented mp4 served from this
// machine, and opens the player on that address: the picture starts as soon as
// ffmpeg has the first frames, not when the whole episode is made. startS,
// when above zero, is where the pass begins.
func streamThroughFFmpeg(s *video.Stream, startS float64) (*ui.VideoPlayer, string, bool, error) {
	ffmpeg := findFFmpeg()
	if ffmpeg == "" {
		return nil, "", false, errors.New(i18n.T("This episode needs ffmpeg to play; install it with your package manager and try again"))
	}
	args := streamFFmpegArgs(s, startS)
	cmd := exec.Command(ffmpeg, args...)
	cmd.Env = proxyEnv()
	stderr := &sayBuffer{} // what ffmpeg said, for a pass that goes nowhere
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, "", false, err
	}
	buf := newStreamBuf()
	go func() {
		_, copyErr := io.Copy(buf, out)
		buf.finish(copyErr, cmd.Wait())
	}()
	srv, addr, err := serveStream(buf)
	if err != nil {
		cmd.Process.Kill()
		return nil, "", false, err
	}
	p, err := mygovideo.OpenURL(addr)
	if err != nil {
		srv.Close()
		cmd.Process.Kill()
		return nil, "", false, err
	}
	registerStream(p, &running{cmd: cmd, srv: srv, said: stderr})
	return p, "", true, nil
}

// running is one pass of ffmpeg, and the server its picture comes through.
type running struct {
	cmd  *exec.Cmd
	srv  *http.Server
	said *sayBuffer
}

var (
	runningMu sync.Mutex
	runningBy = map[*ui.VideoPlayer]*running{}
)

// registerStream keeps the pass with its player, so stopping the player stops
// what feeds it.
func registerStream(p *ui.VideoPlayer, r *running) {
	runningMu.Lock()
	runningBy[p] = r
	runningMu.Unlock()
}

// stopStream ends the pass behind a player, if one feeds it.
func stopStream(p *ui.VideoPlayer) {
	runningMu.Lock()
	r := runningBy[p]
	delete(runningBy, p)
	runningMu.Unlock()
	if r == nil {
		return
	}
	r.srv.Close()
	if r.cmd.Process != nil {
		r.cmd.Process.Kill()
	}
	if said := strings.TrimSpace(r.said.String()); said != "" {
		log.Printf("ffmpeg: %s", tailLine([]byte(said)))
	}
}

// streamFFmpegArgs is what ffmpeg gets for a stream the player watches as it
// is made: one fragmented mp4 on stdout, its pieces independent so a player
// can start on the first of them.
func streamFFmpegArgs(s *video.Stream, startS float64) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	hdr := ffmpegHeaders(s.Headers)
	input := func(url string) {
		if hdr != "" {
			args = append(args, "-headers", hdr)
		}
		if startS > 1 {
			args = append(args, "-ss", strconv.FormatFloat(startS, 'f', 3, 64))
		}
		args = append(args, "-reconnect", "1", "-reconnect_streamed", "1",
			"-reconnect_delay_max", "5", "-rw_timeout", "15000000",
			"-i", url)
	}
	input(s.URL)
	if s.AudioURL != "" {
		input(s.AudioURL)
		args = append(args, "-map", "0:v:0", "-map", "1:a:0?")
	} else {
		args = append(args, "-map", "0:v:0?", "-map", "0:a:0?")
	}
	args = append(args,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=ceil(iw/2)*2:ceil(ih/2)*2",
		"-c:a", "aac", "-b:a", "160k",
		"-f", "mp4", "-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		"pipe:1")
	return args
}

// streamBuf holds what ffmpeg has written so far, for the one reader serving
// it: a reader waits on what isn't there yet instead of taking it for the end.
type streamBuf struct {
	mu    sync.Mutex
	cond  *sync.Cond
	data  []byte
	ended bool
	err   error
}

func newStreamBuf() *streamBuf {
	b := &streamBuf{}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *streamBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.data = append(b.data, p...)
	b.mu.Unlock()
	b.cond.Broadcast()
	return len(p), nil
}

// finish says the pass is over, with whatever became of it.
func (b *streamBuf) finish(copyErr, waitErr error) {
	b.mu.Lock()
	b.ended = true
	switch {
	case waitErr != nil:
		b.err = waitErr
	default:
		b.err = copyErr
	}
	b.mu.Unlock()
	b.cond.Broadcast()
}

// serveStream answers the stream's address, writing what ffmpeg has made as it
// makes it.
func serveStream(b *streamBuf) (*http.Server, string, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/s.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		at := 0
		for {
			b.mu.Lock()
			for at >= len(b.data) && !b.ended {
				b.cond.Wait()
			}
			chunk := append([]byte(nil), b.data[at:]...)
			ended := b.ended
			b.mu.Unlock()
			if len(chunk) > 0 {
				if _, err := w.Write(chunk); err != nil {
					return
				}
				at += len(chunk)
				if flusher != nil {
					flusher.Flush()
				}
			}
			if ended {
				return
			}
		}
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	return srv, "http://" + ln.Addr().String() + "/s.mp4", nil
}

// ffmpegHeaders is headers as ffmpeg's -headers takes them: one line to each,
// in a steady order.
func ffmpegHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(headers[name])
		b.WriteString("\r\n")
	}
	return b.String()
}

// findFFmpeg finds ffmpeg: the one the app ships, then one on PATH, then
// the usual places.
func findFFmpeg() string {
	if p := bundledProgram("ffmpeg"); p != "" {
		return p
	}
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

// bundledProgram is a program the app ships, or "" when this build ships
// none. It looks in bin/ first, then beside the resources themselves: a
// Windows build lays a helper next to the executable. Windows finds it
// without the .exe.
func bundledProgram(name string) string {
	dir, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return ""
	}
	for _, p := range []string{filepath.Join(dir, "bin", name), filepath.Join(dir, name)} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if runtime.GOOS == "windows" {
			if _, err := os.Stat(p + ".exe"); err == nil {
				return p + ".exe"
			}
		}
	}
	return ""
}

// tailLine is the end of what a program wrote, to say why it stopped.
func tailLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return lines[len(lines)-1]
}

// scrubState is how far the episode is, and whether the pointer is on it.
type scrubState struct {
	v      float64
	held   bool
	active bool
}

// clock formats seconds as minutes and seconds.
func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// sayBuffer keeps the end of what a program said on stderr, for a pass that
// goes nowhere and has nothing else to show for itself.
type sayBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *sayBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.b.Write(p)
	if w.b.Len() > 8<<10 {
		kept := w.b.String()
		w.b.Reset()
		w.b.WriteString(kept[len(kept)-4<<10:])
	}
	return len(p), nil
}

func (w *sayBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}
