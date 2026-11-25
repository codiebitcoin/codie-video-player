package web

import (
	"encoding/json"
	"os"
	"path/filepath"

	"codie-video-player/internal/i18n"
)

// settings is what the server keeps of the viewer's, the same file the desktop
// app writes, so a folder added in one shows in the other when they share a
// data directory. Only the fields the web face reads and writes are here.
type settings struct {
	Source string `json:"source,omitempty"`
	// Language is what the app's words read in: en, zh-CN or ja. Empty is English.
	Language string `json:"language,omitempty"`
	// LocalFolders are the folders the local source plays, each one a group,
	// in the order the viewer added them.
	LocalFolders []string `json:"localFolders,omitempty"`
	// DisabledPlugins are the plugins turned off. Huangguo starts off: it ships
	// with the app, and comes on from the settings when wanted.
	DisabledPlugins []string `json:"disabledPlugins,omitempty"`
	// PluginDefaultsApplied are the plugins whose start-off default the app
	// already applied, so turning one on isn't undone by the next run.
	PluginDefaultsApplied []string `json:"pluginDefaultsApplied,omitempty"`
	// DisabledSources is what an older file called the same switch, when
	// Huangguo was built in. It's read once and folded into DisabledPlugins.
	DisabledSources []string `json:"disabledSources,omitempty"`
	// SourcesChosen marks a settings file that names its sources, so a first
	// run can start Huangguo off without turning it off again later.
	SourcesChosen bool `json:"sourcesChosen,omitempty"`

	path string
	// rest is every other key of the file, kept so a save doesn't drop what
	// the desktop wrote and this face doesn't know.
	rest map[string]json.RawMessage
}

func (s *settings) save() {
	if s.path == "" {
		return
	}
	out := map[string]any{}
	for k, v := range s.rest {
		out[k] = v
	}
	out["source"] = s.Source
	out["language"] = s.Language
	out["localFolders"] = s.LocalFolders
	out["disabledPlugins"] = s.DisabledPlugins
	out["pluginDefaultsApplied"] = s.PluginDefaultsApplied
	out["disabledSources"] = s.DisabledSources
	out["sourcesChosen"] = s.SourcesChosen
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(s.path), 0o700)
	os.WriteFile(s.path, b, 0o600)
}

// loadSettings reads the desktop's settings file, or starts one that has
// Huangguo off, as a first run of the desktop does.
func loadSettings(dir string) *settings {
	s := &settings{path: filepath.Join(dir, "settings.json"), rest: map[string]json.RawMessage{}}
	if b, err := os.ReadFile(s.path); err == nil {
		var raw map[string]json.RawMessage
		if json.Unmarshal(b, &raw) == nil {
			take := func(key string, dst any) {
				if v, ok := raw[key]; ok {
					json.Unmarshal(v, dst)
					delete(raw, key)
				}
			}
			take("source", &s.Source)
			take("language", &s.Language)
			take("localFolders", &s.LocalFolders)
			take("disabledPlugins", &s.DisabledPlugins)
			take("pluginDefaultsApplied", &s.PluginDefaultsApplied)
			take("disabledSources", &s.DisabledSources)
			take("sourcesChosen", &s.SourcesChosen)
			s.rest = raw
		}
	}
	if s.Language != i18n.English && s.Language != i18n.Chinese && s.Language != i18n.Japanese {
		s.Language = i18n.English
	}
	// A first run starts Huangguo off. It still ships with the app, as a
	// plugin, and the settings turn it on. An older file that turned the
	// built-in source off keeps it off, now as a plugin.
	if !s.SourcesChosen {
		if !containsString(s.DisabledPlugins, "huangguo") {
			s.DisabledPlugins = append(s.DisabledPlugins, "huangguo")
		}
		s.SourcesChosen = true
		s.save()
	} else if containsString(s.DisabledSources, "huangguo") && !containsString(s.DisabledPlugins, "huangguo") {
		s.DisabledPlugins = append(s.DisabledPlugins, "huangguo")
		s.save()
	}
	return s
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// applyPluginDefaults turns the plugins that ask to start off off, once each:
// a plugin the viewer turned on after that stays on. It answers whether it
// changed the settings, for the caller to save them.
func applyPluginDefaults(s *settings, defaults []string) bool {
	changed := false
	for _, id := range defaults {
		if containsString(s.PluginDefaultsApplied, id) {
			continue
		}
		s.PluginDefaultsApplied = append(s.PluginDefaultsApplied, id)
		changed = true
		if !containsString(s.DisabledPlugins, id) {
			s.DisabledPlugins = append(s.DisabledPlugins, id)
		}
	}
	return changed
}
