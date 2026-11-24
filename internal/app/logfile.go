package app

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

// setupLog keeps what the app logs on disk as well, where it can be read
// whatever way the app was started: stderr as before, and logs/app.log under
// the app's own folder. The file starts over each run, so it holds the last
// run whole.
func setupLog() {
	dir := userDataDir("logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.SetFlags(log.LstdFlags)
}
