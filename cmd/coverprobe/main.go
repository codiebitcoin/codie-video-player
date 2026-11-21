//go:build coverprobe

package main

import (
	"context"
	"fmt"

	"codie-video-player/internal/plugin"
	"codie-video-player/internal/source/huangguo"
)

func main() {
	p, err := plugin.Load("plugins/bilibili", "/tmp/probe-data4")
	if err != nil {
		fmt.Println("load:", err)
		return
	}
	defer p.Close()
	v := p.Video()
	l, err := v.List("hot", 1)
	fmt.Println("err:", err)
	for i, it := range l.Items {
		if i >= 3 {
			break
		}
		fmt.Printf("cover[%d]: %q\n", i, it.Cover)
		if it.Cover != "" {
			data, err := huangguo.Cover(context.Background(), it.Cover)
			fmt.Printf("  huangguo.Cover: %d bytes, err=%v\n", len(data), err)
		}
	}
}
