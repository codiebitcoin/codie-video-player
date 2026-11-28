package app

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// hwaccelOnce keeps the ask to ffmpeg to one; hwaccelName is what it answers.
var (
	hwaccelOnce  sync.Once
	hwaccelNames = map[string]string{
		"darwin": "-hwaccel videotoolbox",
	}
)

// initHWAccel asks the ffmpeg the app uses whether it decodes with the
// machine's own video hardware, and keeps the flag that turns it on. A
// machine without such hardware, or an ffmpeg without it compiled in, leaves
// the flag empty: nothing asked, nothing different.
func initHWAccel() {
	flag, ok := hwaccelNames[runtime.GOOS]
	if !ok {
		return
	}
	ffmpeg := findFFmpeg()
	if ffmpeg == "" {
		return
	}
	out, err := exec.Command(ffmpeg, "-hide_banner", "-hwaccels").Output()
	if err != nil {
		return
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	// The flag names the hardware; it only goes when the ffmpeg answers that
	// it has it.
	hw := map[string]string{
		"darwin": "videotoolbox",
	}
	if have[hw[runtime.GOOS]] {
		hwaccelDecode = flag
	}
}

// ensureHWAccel asks once, on the first transcode, and answers nothing.
func ensureHWAccel() {
	hwaccelOnce.Do(initHWAccel)
}
