// Package local is the source of videos that live on this machine: folders the
// viewer adds, each folder one group, every video file in it one episode. It
// plays nothing from the network, so the app's HTTP proxy never touches it.
package local

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codie-video-player/internal/video"
)

// ID is the source's id, which the library keeps entries under.
const ID = "local"

// videoExts are the files a folder's group lists, lower case with the dot.
var videoExts = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true, ".mkv": true, ".webm": true,
	".avi": true, ".flv": true, ".ts": true, ".m3u8": true,
}

// Source plays the folders the viewer added.
type Source struct {
	// Folders are the groups, each a path on this machine, in the order the
	// viewer added them.
	Folders []string
}

// Info introduces the source.
func (s *Source) Info() video.Info {
	return video.Info{ID: ID, Name: "Local", Version: "1"}
}

// Can reports what the source does. It has no search of its own: a folder's
// group is filtered where it shows.
func (s *Source) Can(what string) bool {
	switch what {
	case "videoTabs", "videoList", "videoItem", "videoPlay":
		return true
	default:
		return false
	}
}

// Tabs are the groups: one chip to a folder, named by the folder, plus the
// one that asks for another folder.
func (s *Source) Tabs() ([]video.Tab, error) {
	tabs := make([]video.Tab, 0, len(s.Folders)+1)
	for _, dir := range s.Folders {
		tabs = append(tabs, video.Tab{ID: "dir:" + dir, Label: filepath.Base(dir)})
	}
	tabs = append(tabs, video.Tab{ID: "add", Label: "Add Folder"})
	return tabs, nil
}

// List is one folder's videos, one page of them all. A folder that went away
// lists nothing.
func (s *Source) List(tabID string, page int) (*video.List, error) {
	if page > 1 {
		return &video.List{}, nil
	}
	dir, ok := strings.CutPrefix(tabID, "dir:")
	if !ok {
		return &video.List{}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return &video.List{}, nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	items := make([]video.Item, len(names))
	for i, name := range names {
		items[i] = video.Item{
			ID:     filepath.Join(dir, name),
			Source: ID,
			Title:  strings.TrimSuffix(name, filepath.Ext(name)),
		}
	}
	return &video.List{Items: items}, nil
}

// Search isn't a thing this source does.
func (s *Source) Search(string, int) (*video.VideoResult, error) {
	return nil, video.ErrUnsupported
}

// Video is one file as a drama of a single episode, so it plays like any other.
func (s *Source) Video(id string) (*video.Video, error) {
	name := filepath.Base(id)
	return &video.Video{
		Item: video.Item{ID: id, Source: ID, Title: strings.TrimSuffix(name, filepath.Ext(name))},
		Episodes: []video.Episode{{
			ID:    id,
			Title: strings.TrimSuffix(name, filepath.Ext(name)),
		}},
	}, nil
}

// Play answers the file itself. A playlist plays as HLS; everything else as a
// file the player opens directly.
func (s *Source) Play(id, ep string) (*video.Stream, error) {
	path := ep
	if path == "" {
		path = id
	}
	if _, err := os.Stat(path); err != nil {
		return nil, video.Fail(video.ErrUnavailable, path)
	}
	kind := "file"
	if strings.EqualFold(filepath.Ext(path), ".m3u8") {
		kind = "hls"
	}
	return &video.Stream{URL: path, Type: kind}, nil
}
