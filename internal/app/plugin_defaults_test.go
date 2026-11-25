package app

import (
	"slices"
	"testing"
)

func TestApplyPluginDefaults(t *testing.T) {
	tests := []struct {
		name     string
		settings settings
		defaults []string
		disabled []string
		applied  []string
		changed  bool
	}{
		{
			name:     "first sight turns it off and notes it",
			defaults: []string{"pornhub"},
			disabled: []string{"pornhub"},
			applied:  []string{"pornhub"},
			changed:  true,
		},
		{
			name:     "a plugin already off is just noted",
			settings: settings{DisabledPlugins: []string{"pornhub"}},
			defaults: []string{"pornhub"},
			disabled: []string{"pornhub"},
			applied:  []string{"pornhub"},
			changed:  true,
		},
		{
			name:     "turning it on stands",
			settings: settings{PluginDefaultsApplied: []string{"pornhub"}},
			defaults: []string{"pornhub"},
			applied:  []string{"pornhub"},
		},
		{
			name:     "nothing default-off changes nothing",
			settings: settings{DisabledPlugins: []string{"huangguo"}},
			disabled: []string{"huangguo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.settings
			if changed := applyPluginDefaults(&got, tt.defaults); changed != tt.changed {
				t.Errorf("changed = %v, want %v", changed, tt.changed)
			}
			if !slices.Equal(got.DisabledPlugins, tt.disabled) {
				t.Errorf("disabled = %v, want %v", got.DisabledPlugins, tt.disabled)
			}
			if !slices.Equal(got.PluginDefaultsApplied, tt.applied) {
				t.Errorf("applied = %v, want %v", got.PluginDefaultsApplied, tt.applied)
			}
		})
	}
}
