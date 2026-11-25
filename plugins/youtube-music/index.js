// YouTube as a source of videos. Every ask goes through yt-dlp, which speaks
// to YouTube the way its own site does: a search is a flat list of its
// results, a play is the streams it names. YouTube serves the picture and
// the sound apart more often than not, so a play hands the app both, and
// the app's own ffmpeg joins them before it plays.
//
// Reaching YouTube is the machine's own business: where the site is
// unreachable, everything here fails with what yt-dlp said.

const PER_PAGE = 20;
// Inside the host's own 30s call window, so yt-dlp's own complaint// surfaces rather than the host cutting the call off.
// A list is a flat search and comes back quickly; a play reads every
// format, which takes longer, so it gets its own.
const TIMEOUT = 25000;
const PLAY_TIMEOUT = 60000;

// url is a video's address on YouTube.
function url(id) {
  return `https://www.youtube.com/watch?v=${encodeURIComponent(id)}`;
}

// mmss is a count of seconds as a clock reads it, or nothing for none.
function mmss(s) {
  s = Math.floor(Number(s) || 0);
  if (!isFinite(s) || s <= 0) return "";
  if (s >= 3600) {
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
    return h + ":" + String(m).padStart(2, "0") + ":" + String(sec).padStart(2, "0");
  }
  return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
}

// installed remembers the programs found, one lookup to a run.
const installed = {};

// program finds a program by trying the places such things sit, and answers
// its path, or null when it isn't there. A plugin refused its programs says
// so plainly.
async function program(name, versionFlag) {
  if (installed[name]) return installed[name];
  // The app's own copy comes first: resources/bin, which ships per platform.
  // Windows finds the program of a path without its .exe.
  const home = (typeof host.resourcesDir === "function" && host.resourcesDir()) || "";
  const places = [
    home && `${home}/bin/${name}`,
    name,
    `/opt/homebrew/bin/${name}`,
    `/usr/local/bin/${name}`,
  ].filter(Boolean);
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
      if (/allowed/.test(e.message)) {
        throw fail("setup", "Allow YouTube to start programs in the settings' plugins page");
      }
    }
  }
  return null;
}

// ytdlp runs yt-dlp with args and answers its last stdout line, which is
// where its JSON lands. What went wrong comes out as an Error with the
// program's own complaint.
async function ytdlp(args, timeout = TIMEOUT) {
  const ytdlp = await program("yt-dlp", "--version");
  if (!ytdlp) {
    throw fail("unavailable", "yt-dlp is not installed on this machine");
  }
  const full = ["--no-warnings", "--no-progress", "--no-playlist", ...args];
  const proc = host.spawn(ytdlp, full);
  proc.closeStdin();
  const timer = setTimeout(() => proc.kill(), timeout);
  const lines = [];
  for (let line; (line = await proc.readLine()) !== null; ) lines.push(line);
  const code = await proc.wait();
  clearTimeout(timer);
  if (code !== 0 || !lines.length) {
    throw fail("unavailable", (proc.errorOutput() || "yt-dlp failed").trim().split("\n").pop() || "yt-dlp failed");
  }
  return JSON.parse(lines[lines.length - 1]);
}

// fail is an error of a kind the app knows: unavailable says the machine
// can't reach or play what was asked, unsupported says this source doesn't
// do it.
function fail(kind, message) {
  const e = new Error(message || kind);
  e.code = kind;
  return e;
}

// t is the text in the listener's language, from the plugin's locales.
const t = (text) => host.t(text);

// playable is a video that can be played here: a live stream, one still
// coming, or one already over has no stream of its own to hand the player,
// so it never reaches the grid.
function playable(e) {
  if (!e || !e.id) return false;
  if (e.is_live || e.live_status === "is_live" || e.live_status === "is_upcoming" || e.live_status === "post_live") {
    return false;
  }
  return true;
}

// itemOf is a card of the grid's for what a search found.
function itemOf(e) {
  return {
    id: e.id,
    title: (e.title || "").replace(/\s+/g, " ").trim(),
    cover: e.thumbnails && e.thumbnails.length
      ? e.thumbnails[e.thumbnails.length - 1].url
      : `https://i.ytimg.com/vi/${e.id}/hqdefault.jpg`,
    subtitle: [e.uploader || e.channel, mmss(e.duration)].filter(Boolean).join(" · "),
  };
}

// The chips the sidebar lists, each a search YouTube answers without an
// account. YouTube took its own trending and category pages down, and what
// it left of them yt-dlp can't read, so a chip is a search of its own.
// Live streams aren't listed: they have no stream this player can take.
const FEEDS = [
  { id: "hot", label: "Popular", query: "popular videos this week" },
  { id: "music", label: "Music", query: "official music video" },
  { id: "gaming", label: "Gaming", query: "gaming" },
  { id: "news", label: "News", query: "news" },
  { id: "movies", label: "Movies", query: "movie trailer" },
];

// feedFresh is how long what a feed gave stands in for its list, so paging
// a chip doesn't run yt-dlp again for the same one.
const feedFresh = 120000;

// feedDepth is how many a chip asks for at once. A page shows PER_PAGE, and
// asking for far more is what makes a chip slow: yt-dlp reads every one.
const feedDepth = PER_PAGE * 2;

let feedCache = null; // { at, tab, entries }

async function feed(tab) {
  const chip = FEEDS.find((f) => f.id === tab);
  if (!chip) throw fail("unsupported", t("no such page"));
  if (feedCache && feedCache.tab === tab && Date.now() - feedCache.at < feedFresh) {
    return feedCache.entries;
  }
  const j = await ytdlp(["--flat-playlist", "--dump-single-json", `ytsearch${feedDepth}:${chip.query}`]);
  const entries = (j.entries || []).filter(playable);
  feedCache = { at: Date.now(), tab, entries };
  return entries;
}

// videoTabs: the chips the sidebar lists. Live streams stay out: a live
// one has no stream this player can take, so it would only fail to play.
export async function videoTabs() {
  return FEEDS.map((f) => ({ id: f.id, label: t(f.label) }));
}

// videoList is a page of what a chip lists, pages cut out of what its feed
// gave, pages from one.
export async function videoList({ tab, page }) {
  const n = Math.max(1, +page || 1);
  const entries = await feed(String(tab || ""));
  return {
    items: entries.slice((n - 1) * PER_PAGE, n * PER_PAGE).map(itemOf),
    more: n * PER_PAGE < entries.length,
  };
}

// videoSearch is page of YouTube's answers to a query, PER_PAGE to a page,
// by the offset.
export async function videoSearch({ query, offset }) {
  const off = Math.max(0, +offset || 0);
  const n = off + PER_PAGE;
  const j = await ytdlp(["--flat-playlist", "--dump-single-json", `ytsearch${n}:${query}`]);
  const entries = (j.entries || []).filter(playable);
  const items = entries.slice(off, n).map(itemOf);
  return { items, more: entries.length >= n };
}

// videoItem is a video and its one episode, itself.
export async function videoItem({ id }) {
  const j = await ytdlp(["--dump-single-json", url(id)], PLAY_TIMEOUT);
  return {
    id: String(id),
    title: j.title || id,
    cover: `https://i.ytimg.com/vi/${id}/hqdefault.jpg`,
    intro: (j.description || "").slice(0, 600),
    episodes: [{ id: String(id), title: j.title || "", seconds: Math.floor(j.duration || 0) }],
  };
}

// pick finds the worthiest stream of a video's formats: a stream with both
// picture and sound first, else the two worthiest apart.
function pick(formats) {
  const withUrl = (formats || []).filter((f) => f && f.url);
  const height = (f) => Math.max(+(f.height || 0), +(f.width || 0));
  const both = withUrl
    .filter((f) => f.vcodec && f.vcodec !== "none" && f.acodec && f.acodec !== "none")
    .sort((a, b) => height(b) - height(a));
  if (both.length) {
    return { url: both[0].url, audioUrl: "", ext: both[0].ext || "mp4" };
  }
  const picture = withUrl
    .filter((f) => f.vcodec && f.vcodec !== "none")
    .sort((a, b) => height(b) - height(a));
  const sound = withUrl
    .filter((f) => f.acodec && f.acodec !== "none" && (!f.vcodec || f.vcodec === "none"))
    .sort((a, b) => +((b.abr || 0) + "") > +((a.abr || 0) + "") ? 1 : -1);
  if (picture.length && sound.length) {
    return { url: picture[0].url, audioUrl: sound[0].url, ext: picture[0].ext || "mp4" };
  }
  return null;
}

// videoPlay answers the streams of an episode: one whole stream when
// YouTube serves picture and sound together, the two apart otherwise, for
// the app to join.
export async function videoPlay({ id }) {
  const j = await ytdlp(["--dump-single-json", url(id)], PLAY_TIMEOUT);
  if (!playable(j)) {
    throw fail("unavailable", "live streams can't be played here");
  }
  const picked = pick(j.formats) || (j.url ? { url: j.url, audioUrl: "", ext: j.ext || "mp4" } : null);
  if (!picked) {
    throw fail("unavailable", "no playable stream for this video");
  }
  const type = picked.ext === "m3u8" || picked.ext === "hls" ? "hls" : "mp4";
  return picked.audioUrl
    ? { url: picked.url, audioUrl: picked.audioUrl, type }
    : { url: picked.url, type };
}
