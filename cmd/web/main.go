// The web face of Codie Video Player: the same sources over HTTP, to watch
// in a browser. A server of its own — no windows, no tray — built for the
// Docker image, but happy on a desk too:
//
//	go run ./cmd/web -plugins ./plugins -data ./data
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"

	"codie-video-player/internal/i18n"
	"codie-video-player/internal/web"
)

// dist is the built web app, which the build puts here from front/dist
// (`make web-front`); what's committed is a stand-in page telling the
// builder to run it.
//
//go:embed all:dist
var dist embed.FS

// env is the environment variable, or the flag's default when unset.
func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	defaultAddr := env("LISTEN_ADDR", ":8080")
	defaultData := env("DATA_DIR", "data")
	defaultPlugins := env("PLUGINS_DIR", "plugins")
	addr := flag.String("addr", defaultAddr, "where to listen")
	dataDir := flag.String("data", defaultData, "where settings, history, and transcode temp go")
	pluginsDir := flag.String("plugins", defaultPlugins, "where the plugin folders are")
	showVersion := flag.Bool("version", false, "print the version and go")
	flag.Parse()

	i18n.SetLanguage("zh-CN")
	version := os.Getenv("VERSION")
	if version == "" {
		if b, err := os.ReadFile("VERSION"); err == nil {
			version = string(b)
		}
	}
	if version == "" {
		version = "dev"
	}
	if *showVersion {
		fmt.Println(version)
		return
	}

	front, err := fs.Sub(dist, "dist")
	if err != nil {
		log.Fatalf("dist: %v", err)
	}
	srv, err := web.New(web.Options{
		DataDir:    *dataDir,
		PluginsDir: *pluginsDir,
		Version:    version,
		AuthUser:   os.Getenv("AUTH_USER"),
		AuthPass:   os.Getenv("AUTH_PASS"),
		Static:     front,
	})
	if err != nil {
		log.Fatalf("web: %v", err)
	}
	log.Printf("codie-video-player web %s listening on %s", version, *addr)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}
