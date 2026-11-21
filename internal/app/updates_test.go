package app

import "testing"

func TestNewer(t *testing.T) {
	// A release is newer by its three numbers, tags with or without their v.
	tests := []struct {
		tag, v string
		want   bool
	}{
		{"v0.2.0", "0.1.0", true},
		{"0.1.1", "0.1.0", true},
		{"v0.1.27", "0.1.27", false},
		{"0.1.26", "0.1.27", false},
		{"1.0.0", "0.9.9", true},
		{"not-a-version", "0.1.0", false},
		{"", "0.1.0", false},
	}
	for _, tt := range tests {
		if got := newer(tt.tag, tt.v); got != tt.want {
			t.Errorf("newer(%q, %q) = %v, want %v", tt.tag, tt.v, got, tt.want)
		}
	}
}

func TestSemver(t *testing.T) {
	// The three numbers come out of a tag; words besides a leading v fail.
	if n, ok := semver("v1.2.3"); !ok || n != [3]int{1, 2, 3} {
		t.Errorf("semver v1.2.3 = %v %v", n, ok)
	}
	if _, ok := semver("1.2.3-beta"); ok {
		t.Errorf("semver took a suffix")
	}
	if _, ok := semver("1.2"); ok {
		t.Errorf("semver took two numbers")
	}
}
