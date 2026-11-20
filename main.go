package main

import (
	"embed"
	"log"

	"codie-video-player/internal/app"
)

// plugins are the sources of videos the app ships with: bilibili and YouTube
// Music, installed into the plugins folder on first run.
//
//go:embed all:plugins
var bundledPlugins embed.FS

func main() {
	if err := app.Run(bundledPlugins); err != nil {
		log.Fatal(err)
	}
}
