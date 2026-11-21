package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	mygovideo "github.com/egoist/mygo/ui/video"

	"codie-video-player/internal/i18n"
	"codie-video-player/internal/video"
)

// How an episode comes to the player: the site gives an HLS address, which
// macOS and Windows play as it is; Linux without GStreamer's HLS plugins
// takes a pass of ffmpeg first, which lands it in a file. An episode is
// short, so the pass takes a moment, not a wait.

// ffmpegTimeout is how long making an episode's file may take.
const ffmpegTimeout = 10 * time.Minute

// openEpisodeStream opens what an episode plays through. The second answer is
// a file made for the player, which the caller gives up when another comes.
// It's a variable so tests can put a fake in.
var openEpisodeStream = func(s *video.Stream) (*ui.VideoPlayer, string, error) {
	switch {
	case s.AudioURL != "", len(s.Headers) > 0, s.Type == "hls" && runtime.GOOS == "linux":
		return throughFFmpeg(s)
	default:
		p, err := mygovideo.OpenURL(s.URL)
		return p, "", err
	}
}

// throughFFmpeg makes the episode into one mp4 for the player: one whose
// picture and sound are apart gets them joined, and one the system won't
// read, or that asks for headers, lands in a file first.
func throughFFmpeg(s *video.Stream) (*ui.VideoPlayer, string, error) {
	ffmpeg := findFFmpeg()
	if ffmpeg == "" {
		return nil, "", errors.New(i18n.T("This episode needs ffmpeg to play; install it with your package manager and try again"))
	}
	f, err := os.CreateTemp("", "codie-video-*.mp4")
	if err != nil {
		return nil, "", err
	}
	dst := f.Name()
	f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), ffmpegTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, ffmpegArgs(s, dst)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(dst)
		if ctx.Err() != nil {
			return nil, "", errors.New(i18n.T("This episode timed out opening; try again"))
		}
		return nil, "", fmt.Errorf("%s", tailLine(out))
	}
	p, err := mygovideo.Open(dst)
	if err != nil {
		os.Remove(dst)
		return nil, "", err
	}
	return p, dst, nil
}

// ffmpegArgs builds ffmpeg's arguments for making dst of the stream: a stream
// whose picture and sound are apart comes out joined, and its headers go with
// every input it names.
func ffmpegArgs(s *video.Stream, dst string) []string {
	args := []string{"-hide_banner", "-loglevel", "error"}
	hdr := ffmpegHeaders(s.Headers)
	appendInput := func(url string) {
		if hdr != "" {
			args = append(args, "-headers", hdr)
		}
		args = append(args, "-i", url)
	}
	appendInput(s.URL)
	if s.AudioURL != "" {
		appendInput(s.AudioURL)
		args = append(args, "-map", "0:v:0", "-map", "1:a:0?")
	}
	return append(args, "-c", "copy", "-movflags", "+faststart", "-y", dst)
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

// findFFmpeg finds ffmpeg: on PATH, then the usual places.
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
