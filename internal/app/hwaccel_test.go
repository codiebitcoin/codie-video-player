package app

import (
	"testing"

	"codie-video-player/internal/video"
)

func TestHWAccelArgs(t *testing.T) {
	saved := hwaccelDecode
	defer func() { hwaccelDecode = saved }()
	hwaccelDecode = "-hwaccel videotoolbox"
	s := &video.Stream{URL: "http://x/v.mp4", Type: "mp4"}
	got := codieFFmpegArgs(s, 1, false, "out.mp4", 0, false)
	found := false
	for i, a := range got {
		if a == "-hwaccel" && i+1 < len(got) && got[i+1] == "videotoolbox" {
			found = true
		}
	}
	if !found {
		t.Errorf("hwaccel on = %v, want -hwaccel videotoolbox before the input", got)
	}
	hwaccelDecode = ""
	got = codieFFmpegArgs(s, 1, false, "out.mp4", 0, false)
	for _, a := range got {
		if a == "-hwaccel" {
			t.Errorf("hwaccel off still asks: %v", got)
			break
		}
	}
}
