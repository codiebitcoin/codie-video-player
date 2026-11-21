package main

import (
	"fmt"
	"os"

	"codie-video-player/internal/plugin"
	"codie-video-player/internal/video"
)

// 直调一个视频插件的各个导出,看真实回答:
//
//	go run ./cmd/video-probe plugins/bilibili /tmp/probe-data hot
func main() {
	p, err := plugin.Load(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Println("load:", err)
		return
	}
	defer p.Close()
	v := p.Video()
	if v == nil {
		fmt.Println("no video source")
		return
	}
	fmt.Println("info:", v.Info())
	if v.Can("videoTabs") {
		tabs, err := v.Tabs()
		fmt.Printf("tabs: %+v, err: %v\n", tabs, err)
	}
	tab := "hot"
	if len(os.Args) > 3 {
		tab = os.Args[3]
	}
	if v.Can("videoList") {
		l, err := v.List(tab, 1)
		if err != nil {
			fmt.Printf("list(%q) err: %v\n", tab, err)
		} else {
			fmt.Printf("list(%q): %d items, more=%v\n", tab, len(l.Items), l.More)
			for i, it := range l.Items {
				if i == 3 {
					break
				}
				fmt.Printf("  [%d] %s (%s)\n", i, it.Title, it.ID)
			}
		}
	}
	if v.Can("videoSearch") {
		r, err := v.Search("test", 0)
		if r == nil {
			r = &video.VideoResult{}
		}
		fmt.Printf("search: %d items, err: %v\n", len(r.Items), err)
	}
}
