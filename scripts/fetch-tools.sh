#!/bin/sh
# fetch-tools.sh 把随包的 ffmpeg 和 yt-dlp 下到 resources/<平台>/bin。
# 用法: scripts/fetch-tools.sh darwin windows-amd64 linux-amd64 linux-arm64
# 不带参数时下当前机器的那一份。已经在的不重下。
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$root"

proxy=${https_proxy:-${HTTPS_PROXY:-}}
if [ -z "$proxy" ] && curl -fsS -m 2 -o /dev/null http://10.10.32.14:7890 2>/dev/null; then
	proxy=http://10.10.32.14:7890
fi
[ -n "$proxy" ] && echo "fetch-tools: proxy $proxy"

fetch() { # url dest
	if [ -s "$2" ]; then
		echo "fetch-tools: have $2"
		return
	fi
	mkdir -p "$(dirname "$2")"
	tmp=$2.part
	curl -fL --retry 3 ${proxy:+--proxy "$proxy"} -o "$tmp" "$1"
	mv "$tmp" "$2"
	chmod +x "$2" 2>/dev/null || true
}

# 压缩包里的一个程序解出来。$1 是包地址，$2 是程序名，$3 是落点。
# 同一个包里的第二个程序不会重下：包按地址留在缓存里。
unpack() { # url name dest
	if [ -s "$3" ]; then
		echo "fetch-tools: have $3"
		return
	fi
	mkdir -p "$(dirname "$3")"
	cache=${TMPDIR:-/tmp}/fetch-tools-cache
	mkdir -p "$cache"
	arc=$cache/$(echo "$1" | cksum | cut -d' ' -f1)
	if [ ! -s "$arc" ]; then
		curl -fL --retry 3 ${proxy:+--proxy "$proxy"} -o "$arc.part" "$1"
		mv "$arc.part" "$arc"
	fi
	tmp=$(mktemp -d)
	case "$1" in
	*.zip) unzip -q "$arc" -d "$tmp" ;;
	*.tar.xz) tar -xJf "$arc" -C "$tmp" ;;
	esac
	found=$(find "$tmp" -type f -name "$2" | head -1)
	[ -n "$found" ] || { echo "fetch-tools: $2 not in $1" >&2; exit 1; }
	mv "$found" "$3"
	chmod +x "$3"
	rm -rf "$tmp"
}

ytbase=https://github.com/yt-dlp/yt-dlp/releases/latest/download
ffbase=https://github.com/yt-dlp/FFmpeg-Builds/releases/download/latest
macbase=https://ffmpeg.martin-riedl.de/download/macos

one() {
	case "$1" in
	darwin)
		# yt-dlp 是 universal 的,两个架构共用;ffmpeg 要按架构各放一份。
		fetch "$ytbase/yt-dlp_macos" resources/darwin/bin/yt-dlp
		unpack "$macbase/arm64/1789931890_9.0.2/ffmpeg.zip" ffmpeg resources/darwin-arm64/bin/ffmpeg
		unpack "$macbase/arm64/1789931890_9.0.2/ffprobe.zip" ffprobe resources/darwin-arm64/bin/ffprobe
		unpack "$macbase/amd64/1789931890_9.0.2/ffmpeg.zip" ffmpeg resources/darwin-amd64/bin/ffmpeg
		unpack "$macbase/amd64/1789931890_9.0.2/ffprobe.zip" ffprobe resources/darwin-amd64/bin/ffprobe ;;
	windows-amd64)
		fetch "$ytbase/yt-dlp.exe" resources/windows-amd64/bin/yt-dlp.exe
		unpack "$ffbase/ffmpeg-master-latest-win64-gpl.zip" ffmpeg.exe resources/windows-amd64/bin/ffmpeg.exe
		unpack "$ffbase/ffmpeg-master-latest-win64-gpl.zip" ffprobe.exe resources/windows-amd64/bin/ffprobe.exe ;;
	linux-amd64)
		fetch "$ytbase/yt-dlp_linux" resources/linux-amd64/bin/yt-dlp
		unpack "$ffbase/ffmpeg-master-latest-linux64-gpl.tar.xz" ffmpeg resources/linux-amd64/bin/ffmpeg
		unpack "$ffbase/ffmpeg-master-latest-linux64-gpl.tar.xz" ffprobe resources/linux-amd64/bin/ffprobe ;;
	linux-arm64)
		fetch "$ytbase/yt-dlp_linux_aarch64" resources/linux-arm64/bin/yt-dlp
		unpack "$ffbase/ffmpeg-master-latest-linuxarm64-gpl.tar.xz" ffmpeg resources/linux-arm64/bin/ffmpeg
		unpack "$ffbase/ffmpeg-master-latest-linuxarm64-gpl.tar.xz" ffprobe resources/linux-arm64/bin/ffprobe ;;
	*) echo "fetch-tools: unknown platform $1" >&2; exit 1 ;;
	esac
}

if [ $# -eq 0 ]; then
	case "$(uname -s)-$(uname -m)" in
	Darwin-*) set -- darwin ;;
	Linux-x86_64) set -- linux-amd64 ;;
	Linux-aarch64) set -- linux-arm64 ;;
	*) echo "fetch-tools: say which platform" >&2; exit 1 ;;
	esac
fi
for p in "$@"; do one "$p"; done
echo "fetch-tools: done"
