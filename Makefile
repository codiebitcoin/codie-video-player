# web 版的构建与本地运行。桌面应用照旧用 `go tool mygo build`，不经这里。

front/dist: $(shell find front/src -type f) front/package.json front/index.html front/vite.config.js
	cd front && npm ci --no-audit --no-fund && npm run build

.PHONY: web-front web-build web-run web-test docker

web-front: front/dist
	rm -rf cmd/web/dist && cp -R front/dist cmd/web/dist

web-build: web-front
	GOTOOLCHAIN=auto go build -o build/web/codie-video-web ./cmd/web

web-run: web-front
	GOTOOLCHAIN=auto go run ./cmd/web -plugins ./plugins -data ./data -addr 127.0.0.1:8080

web-test:
	GOTOOLCHAIN=auto go test ./internal/... -count=1

docker:
	docker build -t codie-video-player:local .
