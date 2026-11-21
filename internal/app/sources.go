package app

import (
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"codie-video-player/internal/video"
)

// sourceEntry is one of the sources of videos: built in, or a plugin's.
type sourceEntry struct {
	id   string
	name string
	src  video.VideoSource
	// grant is the plugin whose programs need the listener's yes, when it
	// hasn't said it yet.
	grant grantable
}

// grantable is a plugin that wants to start programs, once the listener says
// so. It's an interface so the app needn't hold the plugin whole.
type grantable interface {
	Grant(on bool) error
	Granted() bool
}

// ensureBundledPlugins copies the plugins the app ships with into the
// plugins' folder, the ones not there yet: they're then ordinary plugins,
// which updates take and the listener can take out.
func ensureBundledPlugins(bundled embed.FS) {
	entries, err := fs.ReadDir(bundled, "plugins")
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dst := filepath.Join(pluginsDir(), e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := os.CopyFS(dst, mustSub(bundled, "plugins/"+e.Name())); err != nil {
			log.Printf("bundled plugin %s: %v", e.Name(), err)
		} else {
			log.Printf("installed bundled plugin %s", e.Name())
		}
	}
}

func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// sourceNames is the sources' ids in order, for logs.
func sourceNames(entries []sourceEntry) string {
	names := make([]string, 0, len(entries))
	for i := range entries {
		names = append(names, entries[i].id)
	}
	return strings.Join(names, ",")
}

// sourceName is the showing source's id.
func (a *app) sourceName() string {
	if a.sourceEntry == nil {
		return ""
	}
	return a.sourceEntry.id
}
