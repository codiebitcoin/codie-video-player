package huangguo

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"codie-video-player/internal/video"
)

// Source is the built-in Huangguo client as a source of videos, its tabs
// being the ranks and kinds of drama the site itself has.
type Source struct {
	cats    []Category
	catsAt  time.Time
	catsErr error
}

func (Source) Info() video.Info {
	return video.Info{ID: "huangguo", Name: "黄果短剧"}
}

// Can says what the built-in source does: everything a source of videos can.
func (Source) Can(what string) bool {
	switch what {
	case "videoTabs", "videoList", "videoSearch", "videoItem", "videoPlay":
		return true
	}
	return false
}

// tabs asks the site's categories once, and answers the chips: the ranks and
// the catalog first, a category to each after.
func (s *Source) tabs(ctx context.Context) []video.Tab {
	if s.cats == nil && s.catsErr == nil && s.catsAt.IsZero() {
		if cats, err := Categories(ctx); err == nil {
			s.cats, s.catsAt = cats, time.Now()
		} else {
			s.catsErr = err
		}
	}
	tabs := []video.Tab{
		{ID: "hot", Label: "热门"},
		{ID: "all", Label: "最新"},
		{ID: "year", Label: "今年"},
	}
	for _, c := range s.cats {
		tabs = append(tabs, video.Tab{ID: "cat:" + strconv.FormatInt(c.ID, 10), Label: c.Name})
	}
	return tabs
}

// Tabs are the chips over the grid.
func (s *Source) Tabs() ([]video.Tab, error) {
	tabs := s.tabs(context.Background())
	return tabs, s.catsErr
}

// List is a page of what a tab lists. The year tab pages through the catalog
// and keeps what came out this year, asking for the next page while the page
// it got gave none, so that a grid of this year's dramas is what shows.
func (s *Source) List(tabID string, page int) (*video.List, error) {
	ctx := context.Background()
	switch {
	case tabID == "hot":
		items, err := Rank(ctx, "hot")
		if err != nil {
			return nil, err
		}
		return &video.List{Items: itemsOf(items), More: false}, nil
	case tabID == "all", stringsHavePrefix(tabID, "cat:"):
		var cate int64
		if tabID != "all" {
			cate, _ = strconv.ParseInt(stringsTrimPrefix(tabID, "cat:"), 10, 64)
		}
		items, pages, err := Catalog(ctx, cate, page)
		if err != nil {
			return nil, err
		}
		return &video.List{Items: itemsOf(items), More: page < pages}, nil
	case tabID == "year":
		items, pages, err := Catalog(ctx, 0, page)
		if err != nil {
			return nil, err
		}
		year := time.Now().Year()
		out := &video.List{More: page < pages}
		for _, it := range items {
			if it.Year() == year {
				out.Items = append(out.Items, itemOf(it))
			}
		}
		// A page that gave none of the year, and the catalog goes on: the
		// page after is what the app asks next, and it wants something in it.
		if len(out.Items) == 0 && out.More {
			out.More = page < pages
		}
		return out, nil
	}
	return nil, video.Fail(video.ErrUnsupported, "没有这个分类")
}

// Search is what the site's search says of what's asked, a page of twenty to
// a page, which the offset counts.
func (s *Source) Search(query string, offset int) (*video.VideoResult, error) {
	pg := offset/20 + 1
	items, err := Search(context.Background(), query, pg)
	if err != nil {
		return nil, err
	}
	return &video.VideoResult{Items: itemsOf(items), More: len(items) >= 20}, nil
}

// Video is a drama and its episodes, named 第N集 as the site's own titles
// repeat the drama's name.
func (s *Source) Video(id string) (*video.Video, error) {
	n, _ := strconv.ParseInt(id, 10, 64)
	d, err := Drama(context.Background(), n)
	if err != nil {
		return nil, err
	}
	out := &video.Video{
		Item:     itemOf(d.Item),
		Intro:    d.Intro,
		Finished: d.Finished,
		Score:    d.Score,
	}
	for _, ep := range d.Episodes {
		out.Episodes = append(out.Episodes, video.Episode{
			ID:      strconv.Itoa(ep.Num),
			Title:   fmt.Sprintf("第%d集", ep.Num),
			Seconds: ep.Seconds,
		})
	}
	return out, nil
}

// Play is the HLS address an episode plays through, its signature already on
// it.
func (s *Source) Play(id, ep string) (*video.Stream, error) {
	n, _ := strconv.ParseInt(id, 10, 64)
	epn, _ := strconv.Atoi(ep)
	url, err := Play(context.Background(), n, epn)
	if err != nil {
		if err.Error() != "" {
			return nil, video.Fail(video.ErrUnavailable, err.Error())
		}
		return nil, video.ErrUnavailable
	}
	return &video.Stream{URL: url, Type: "hls"}, nil
}

// itemOf is a card of the grid's for a drama of the site's.
func itemOf(it Item) video.Item {
	return video.Item{
		ID:       strconv.FormatInt(it.ID, 10),
		Title:    it.Title,
		Cover:    it.Cover,
		Subtitle: it.Subtitle,
		Year:     it.Year(),
	}
}

func itemsOf(items []Item) []video.Item {
	out := make([]video.Item, 0, len(items))
	for _, it := range items {
		out = append(out, itemOf(it))
	}
	return out
}

func stringsHavePrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func stringsTrimPrefix(s, prefix string) string {
	if stringsHavePrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}
