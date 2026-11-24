package app

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"codie-video-player/internal/plugin"
	"codie-video-player/internal/video"
)

// sourceEntry is one of the sources of videos: built in, or a plugin's.
type sourceEntry struct {
	id   string
	name string
	src  video.VideoSource
	// plugin is the plugin of a plugin's source, for what's beyond the video
	// functions: its account, and its sign-in. Built in, it's nil.
	plugin *plugin.Plugin
}

// ensureBundledPlugins copies the plugins the app ships with into the
// plugins' folder: the ones not there yet, and the ones the app ships a
// newer version of than the folder holds. They're then ordinary plugins,
// which the listener can take out.
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
		if fresh, err := os.ReadDir(dst); err == nil && len(fresh) > 0 {
			if shipped, disk := bundledVersion(bundled, e.Name()), diskVersion(dst); shipped != "" &&
				newer(shipped, disk) {
				log.Printf("bundled plugin %s: %s takes over %s", e.Name(), shipped, disk)
				os.RemoveAll(dst)
			} else {
				continue
			}
		}
		if err := os.CopyFS(dst, mustSub(bundled, "plugins/"+e.Name())); err != nil {
			log.Printf("bundled plugin %s: %v", e.Name(), err)
		} else {
			log.Printf("installed bundled plugin %s", e.Name())
		}
	}
}

// bundledVersion is the version the app ships of a plugin, by its
// plugin.json.
func bundledVersion(bundled embed.FS, name string) string {
	b, err := fs.ReadFile(bundled, "plugins/"+name+"/plugin.json")
	if err != nil {
		return ""
	}
	var p struct {
		Version string `json:"version"`
	}
	json.Unmarshal(b, &p)
	return p.Version
}

// diskVersion is the version sitting in the plugins' folder.
func diskVersion(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return ""
	}
	var p struct {
		Version string `json:"version"`
	}
	json.Unmarshal(b, &p)
	return p.Version
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
