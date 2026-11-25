// NicoNico (nicovideo.jp) as a source of videos. The lists and the search go
// through the site's own public APIs, which ask for nothing signed. A play
// goes through yt-dlp, which speaks to the site the way its own page does and
// names the streams. A video the site keeps behind a login has no stream here:
// this player doesn't sign in to NicoNico.

const PER_PAGE = 20;
const TIMEOUT = 25000;
const PLAY_TIMEOUT = 90000;
const NV = {
  "X-Frontend-Id": "6",
  "X-Frontend-Version": "0",
  "User-Agent": "Mozilla/5.0 (Macintosh) CodieVideoPlayer/0.1",
};

const FEEDS = [
  { id: "all", label: "Ranking", genre: "all" },
  { id: "music", label: "Music", genre: "music" },
  { id: "anime", label: "Anime", genre: "anime" },
  { id: "game", label: "Games", genre: "game" },
];

const t = (text) => host.t(text);

function fail(kind, message) {
  const e = new Error(message || kind);
  e.code = kind;
  return e;
}

function mmss(s) {
  s = Math.floor(Number(s) || 0);
  if (!s) return "";
  if (s >= 3600) {
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60);
    return `${h}:${String(m).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
  }
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

function watchURL(id) {
  return `https://www.nicovideo.jp/watch/${encodeURIComponent(id)}`;
}

const installed = {};

async function program(name, versionFlag) {
  if (installed[name]) return installed[name];
  const home = (typeof host.resourcesDir === "function" && host.resourcesDir()) || "";
  const places = [home && `${home}/bin/${name}`, name, `/opt/homebrew/bin/${name}`, `/usr/local/bin/${name}`].filter(Boolean);
  for (const path of places) {
    try {
      const proc = host.spawn(path, [versionFlag]);
      proc.closeStdin();
      while ((await proc.readLine()) !== null) {}
      if ((await proc.wait()) === 0) {
        installed[name] = path;
        return path;
      }
    } catch (e) {
      if (/allowed/.test(e.message)) throw fail("setup", t("Allow NicoNico to start programs in the settings' plugins page"));
    }
  }
  return null;
}

async function ytdlp(args, timeout = TIMEOUT) {
  const bin = await program("yt-dlp", "--version");
  if (!bin) throw fail("unavailable", t("yt-dlp is not installed on this machine"));
  const proc = host.spawn(bin, ["--no-warnings", "--no-progress", "--no-playlist", ...args]);
  proc.closeStdin();
  const timer = setTimeout(() => proc.kill(), timeout);
  const lines = [];
  for (let line; (line = await proc.readLine()) !== null; ) lines.push(line);
  const code = await proc.wait();
  clearTimeout(timer);
  if (code !== 0 || !lines.length) {
    const said = (proc.errorOutput() || "yt-dlp failed").trim().split("\n").pop();
    throw fail("unavailable", said || "yt-dlp failed");
  }
  return JSON.parse(lines[lines.length - 1]);
}

function itemOfRanking(it) {
  const views = it.count && it.count.view;
  return {
    id: it.id,
    title: (it.title || "").replace(/\s+/g, " ").trim(),
    cover: (it.thumbnail && (it.thumbnail.largeUrl || it.thumbnail.middleUrl || it.thumbnail.url)) || "",
    subtitle: views ? String(views) : "",
  };
}

function itemOfSearch(it) {
  return {
    id: it.contentId,
    title: (it.title || "").replace(/\s+/g, " ").trim(),
    cover: it.thumbnailUrl || "",
    subtitle: mmss(it.lengthSeconds),
    year: Number(String(it.startTime || "").slice(0, 4)) || 0,
  };
}

let rankCache = null; // { at, genre, items }

async function ranking(genre) {
  if (rankCache && rankCache.genre === genre && Date.now() - rankCache.at < 120000) return rankCache.items;
  const res = await fetch(`https://nvapi.nicovideo.jp/v1/ranking/genre/${encodeURIComponent(genre)}?term=24h`, { headers: NV });
  if (!res.ok) throw fail("unavailable", `ranking HTTP ${res.status}`);
  const body = await res.json();
  const items = ((body.data && body.data.items) || []).filter((it) => it && it.id);
  rankCache = { at: Date.now(), genre, items };
  return items;
}

export async function videoTabs() {
  return FEEDS.map((f) => ({ id: f.id, label: t(f.label) }));
}

export async function videoList({ tab, page }) {
  const chip = FEEDS.find((f) => f.id === tab);
  if (!chip) throw fail("unsupported", t("no such page"));
  const n = Math.max(1, +page || 1);
  const items = (await ranking(chip.genre)).map(itemOfRanking);
  return { items: items.slice((n - 1) * PER_PAGE, n * PER_PAGE), more: n * PER_PAGE < items.length };
}

export async function videoSearch({ query, offset }) {
  const off = Math.max(0, +offset || 0);
  const url = new URL("https://snapshot.search.nicovideo.jp/api/v2/snapshot/video/contents/search");
  url.searchParams.set("q", query);
  url.searchParams.set("targets", "title,tags");
  url.searchParams.set("fields", "contentId,title,thumbnailUrl,lengthSeconds,startTime");
  url.searchParams.set("_sort", "-viewCounter");
  url.searchParams.set("_offset", String(off));
  url.searchParams.set("_limit", String(PER_PAGE));
  url.searchParams.set("_context", "codie-video-player");
  const res = await fetch(url, { headers: { "User-Agent": NV["User-Agent"] } });
  if (!res.ok) throw fail("unavailable", `search HTTP ${res.status}`);
  const body = await res.json();
  const items = (body.data || []).map(itemOfSearch);
  return { items, more: items.length >= PER_PAGE };
}

export async function videoItem({ id }) {
  const j = await ytdlp(["--dump-single-json", watchURL(id)], PLAY_TIMEOUT);
  return {
    id: String(id),
    title: j.title || id,
    cover: j.thumbnail || (j.thumbnails && j.thumbnails.length && j.thumbnails[j.thumbnails.length - 1].url) || "",
    intro: String(j.description || "").slice(0, 600),
    episodes: [{ id: String(id), title: j.title || "", seconds: Math.floor(j.duration || 0) }],
  };
}

// isHLS 看协议:ニコニコ 的播放列表扩展名也是 mp4,只看扩展名会认错。
function isHLS(f) {
  if (!f) return false;
  if (/m3u8|hls/i.test(f.ext || "")) return true;
  return /m3u8/i.test(String(f.protocol || ""));
}

// worth 让一个文件的直链排在播放列表前面,再按高度取高的。
function worth(f) {
  const height = Math.max(+(f.height || 0), +(f.width || 0));
  return (isHLS(f) ? 0 : 10000) + height;
}

function pick(formats) {
  const withUrl = (formats || []).filter((f) => f && f.url);
  const height = (f) => Math.max(+(f.height || 0), +(f.width || 0));
  const both = withUrl
    .filter((f) => f.vcodec && f.vcodec !== "none" && f.acodec && f.acodec !== "none")
    .sort((a, b) => worth(b) - worth(a));
  if (both.length) return { url: both[0].url, ext: both[0].ext || "mp4" };
  const picture = withUrl.filter((f) => f.vcodec && f.vcodec !== "none").sort((a, b) => worth(b) - worth(a));
  const sound = withUrl.filter((f) => f.acodec && f.acodec !== "none" && (!f.vcodec || f.vcodec === "none"));
  if (picture.length && sound.length) return { url: picture[0].url, audioUrl: sound[0].url, ext: picture[0].ext || "mp4" };
  if (picture.length) return { url: picture[0].url, ext: picture[0].ext || "mp4" };
  return null;
}

export async function videoPlay({ id }) {
  const j = await ytdlp(["--dump-single-json", watchURL(id)], PLAY_TIMEOUT);
  const picked = pick(j.formats) || (j.url ? { url: j.url, ext: j.ext || "mp4" } : null);
  if (!picked) throw fail("unavailable", t("no playable stream for this video"));
  const hls = isHLS(picked) || /m3u8/.test(picked.url);
  return picked.audioUrl
    ? { url: picked.url, audioUrl: picked.audioUrl, type: hls ? "hls" : "mp4" }
    : { url: picked.url, type: hls ? "hls" : "mp4" };
}
