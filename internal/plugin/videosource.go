package plugin

import (
	"net/url"
	"strings"

	"codie-video-player/internal/video"
)

// The answers of a plugin name their dramas without their source, which is
// the plugin: it can't get that wrong, and needn't know its own ID.

// VideoSource is what a plugin's video exports answer through.
type VideoSource struct{ p *Plugin }

func (v *VideoSource) Info() video.Info {
	return video.Info{ID: v.p.manifest.ID, Name: v.p.t(v.p.manifest.Name), Version: v.p.manifest.Version}
}

// Can reports whether the plugin exports the video function named whole, such
// as videoSearch or videoTabs.
func (v *VideoSource) Can(what string) bool { return v.p.Can(what) }

// stamp marks everything a video answer brought as the plugin's, so the grid
// knows whom to ask about it.
func (v *VideoSource) stamp(items []video.Item) {
	for i := range items {
		items[i].Source = v.p.manifest.ID
	}
}

// Tabs are the chips of the source: its ranks, its kinds of drama, whatever
// it makes up.
func (v *VideoSource) Tabs() ([]video.Tab, error) {
	var out []video.Tab
	if err := v.p.call("videoTabs", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// List is a page of what a tab lists, by the tab's ID, pages from one.
func (v *VideoSource) List(tabID string, page int) (*video.List, error) {
	var out video.List
	if err := v.p.call("videoList", map[string]any{"tab": tabID, "page": page}, &out); err != nil {
		return nil, err
	}
	v.stamp(out.Items)
	return &out, nil
}

func (v *VideoSource) Search(query string, offset int) (*video.VideoResult, error) {
	var r video.VideoResult
	if err := v.p.call("videoSearch", map[string]any{"query": query, "offset": offset}, &r); err != nil {
		return nil, err
	}
	v.stamp(r.Items)
	return &r, nil
}

func (v *VideoSource) Video(id string) (*video.Video, error) {
	var out video.Video
	if err := v.p.call("videoItem", map[string]string{"id": id}, &out); err != nil {
		return nil, err
	}
	out.Source = v.p.manifest.ID
	return &out, nil
}

func (v *VideoSource) Play(id, ep string) (*video.Stream, error) {
	var s video.Stream
	if err := v.p.call("videoPlay", map[string]string{"id": id, "episode": ep}, &s); err != nil {
		return nil, err
	}
	// The plugin's requestHeaders for video go with it, under what the
	// stream says itself.
	if u, err := url.Parse(s.URL); err == nil {
		for k, h := range v.p.HeadersFor(u, KindVideo) {
			if s.Headers == nil {
				s.Headers = map[string]string{}
			}
			if !hasHeader(s.Headers, k) {
				s.Headers[k] = h
			}
		}
	}
	return &s, nil
}

var _ video.VideoSource = (*VideoSource)(nil)

// hasHeader reports whether headers has name, in any case.
func hasHeader(headers map[string]string, name string) bool {
	for k := range headers {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}
