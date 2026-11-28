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

// sslCertFile is the system's certificate bundle, which the ffmpeg the app
// ships asks for: its OpenSSL looks for a CA file where macOS keeps none,
// and every site whose chain came from a newer authority then fails with
// "certificate verify failed". Pointing SSL_CERT_FILE at the system's store
// mends the asks. Windows keeps its own store in the system.
var sslCertFile = map[string]string{
	"darwin": "/etc/ssl/cert.pem",
}

// hwaccelEncoder is the hardware H.264 encoder's name, where the machine has
// one: turning an episode with it runs about twice as fast as with libx264,
// which keeps the segments of slow sources ahead of the watching. An empty
// name keeps the software encoder.
var hwaccelEncoder = map[string]string{
	"darwin": "h264_videotoolbox",
}
