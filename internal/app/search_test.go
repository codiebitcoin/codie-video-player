package app

import (
	"testing"

	"codie-video-player/internal/video"
)

func TestSearchItemsFiltersBySource(t *testing.T) {
	// The grid shows every source's answers, or one source's alone.
	a := &app{searchFilter: "all"}
	a.searchGroups = []searchGroup{
		{id: "huangguo", name: "黄果", items: []video.Item{{ID: "1"}, {ID: "2"}}},
		{id: "bilibili", name: "B站", items: []video.Item{{ID: "3"}}},
	}
	if got := len(a.searchItems()); got != 3 {
		t.Errorf("all = %d items, want 3", got)
	}
	a.searchFilter = "bilibili"
	if got := len(a.searchItems()); got != 1 {
		t.Errorf("one source = %d items, want 1", got)
	}
}

func TestSearchProblems(t *testing.T) {
	// What went wrong says itself source by source; a clean search says
	// nothing.
	a := &app{}
	a.searchGroups = []searchGroup{
		{id: "a", name: "甲", err: "搜索失败: no such page"},
		{id: "b", name: "乙", items: []video.Item{{ID: "1"}}},
	}
	if got := a.searchProblems(); got != "甲：搜索失败: no such page" {
		t.Errorf("searchProblems = %q", got)
	}
	a.searchGroups[0].err = ""
	if got := a.searchProblems(); got != "" {
		t.Errorf("clean search said %q", got)
	}
}

func TestRateLabel(t *testing.T) {
	// A speed reads the way a viewer knows it.
	tests := map[float64]string{
		1: "1×", 1.25: "1.25×", 2: "2×", 0.5: "0.5×",
	}
	for r, want := range tests {
		if got := rateLabel(r); got != want {
			t.Errorf("rateLabel(%v) = %q, want %q", r, got, want)
		}
	}
}

func TestControlsMayLeave(t *testing.T) {
	// The controls only step out while an episode plays here, whole, and
	// with no popover in the way.
	a := &app{player: nil}
	if a.controlsMayLeave() {
		t.Errorf("no player, but the controls may leave")
	}
}
