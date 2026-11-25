package app

import (
	"log"
	"os"
	"path/filepath"
)

// setupLog keeps what the app logs on disk, where it can be read whatever way
// the app was started: logs/app.log under the app's own folder, and stderr as
// before when there's one to write to. The file starts over each run, so it
// holds the last run whole.
//
// The file comes first on purpose. A windowed app on Windows has no console,
// and its stderr is a handle that fails every write; writing there first would
// lose the line before the file saw it.
func setupLog() {
	dir := userDataDir("logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	out := &logWriter{file: f, stderr: useStderr()}
	log.SetOutput(out)
	log.SetFlags(log.LstdFlags)
}

// logWriter writes every line to the file, and to stderr when there is one,
// without letting a failed stderr take the file's line with it.
type logWriter struct {
	file   *os.File
	stderr bool
}

func (w *logWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	if w.stderr {
		os.Stderr.Write(p) // the file is what must land; this is a courtesy
	}
	return n, err
}

// useStderr says whether stderr is worth writing to: a terminal or a pipe has
// one, a windowed Windows app none.
func useStderr() bool {
	if fi, err := os.Stderr.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return true
	}
	return false
}
