# 网页版的镜像：前端构建 → Go 构建 → alpine 运行时（ffmpeg/yt-dlp 在内）。
# 桌面应用不进镜像；插件目录原样带上。

FROM node:20-alpine AS front
WORKDIR /app/front
COPY front/package.json front/package-lock.json* ./
RUN npm ci --no-audit --no-fund || npm install --no-audit --no-fund
COPY front/ ./
RUN npm run build

FROM golang:1.27-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=front /app/front/dist ./cmd/web/dist
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/codie-video-web ./cmd/web

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata ffmpeg yt-dlp curl
WORKDIR /app
COPY --from=build /out/codie-video-web ./codie-video-web
COPY plugins ./plugins
RUN mkdir -p /app/data
VOLUME /app/data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD curl -sf http://127.0.0.1:8080/api/health || exit 1
ENTRYPOINT ["./codie-video-web"]
CMD ["-addr", ":8080", "-data", "/app/data", "-plugins", "/app/plugins"]
