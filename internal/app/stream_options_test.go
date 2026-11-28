package app

import (
	"testing"

	"codie-video-player/internal/video"
)

func TestStreamOptions(t *testing.T) {
	network := []string{"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5", "-rw_timeout", "15000000"}
	tests := []struct {
		address string
		want    []string
	}{
		{"https://cdn.example/hls/index.m3u8", network},
		{"http://cdn.example/video.mp4", network},
		{"/Users/x/Movies/a.mp4", nil},
		{"", nil},
		{"ftp://x/y", nil},
	}
	for _, tt := range tests {
		if got := streamOptions(tt.address); !equalArgs(got, tt.want) {
			t.Errorf("streamOptions(%q) = %v, want %v", tt.address, got, tt.want)
		}
	}
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// codieSegmentArgs and codieFFmpegArgs take the reconnect options for a
// network input and leave them out for a file, which ffmpeg turns down.
func TestCodieArgsLeaveFileOptionsOut(t *testing.T) {
	file := &video.Stream{URL: "/tmp/a.mp4", Type: "file"}
	network := &video.Stream{URL: "https://cdn.example/v.m3u8", Type: "hls"}
	for name, args := range map[string][]string{
		"segment file":    codieSegmentArgs(file, 1, 0, 30, "", "/tmp/out.mp4"),
		"whole file":      codieFFmpegArgs(file, 1, false, "/tmp/out.mp4"),
		"segment network": codieSegmentArgs(network, 1, 0, 30, "", "/tmp/out.mp4"),
		"whole network":   codieFFmpegArgs(network, 1, false, "/tmp/out.mp4"),
	} {
		has := false
		for i, a := range args {
			if a == "-reconnect" {
				has = true
				break
			}
			_ = i
		}
		want := name[len(name)-6:] == "etwork"
		if has != want {
			t.Errorf("%s: reconnect options present = %v, want %v", name, has, want)
		}
	}
}
