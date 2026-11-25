// Pornhub as a source of videos. The grid and the search read the site's own
// pages: a card carries the video's key, title, cover and length, and the
// video's page carries its name, cover, description and length as its JSON-LD
// does. Signing in on the site's page brings the listener's favorites. A play
// goes through yt-dlp, which speaks to the site the way its own page does and
// signs the streams. Adult content: the app ships this off, and the settings
// turn it on.

const BASE = "https://www.pornhub.com";
const UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36";
const PER_PAGE = 20;
const TIMEOUT = 25000;
const PLAY_TIMEOUT = 90000;

const t = (text) => host.t(text);

function fail(kind, message) {
  const e = new Error(message || kind);
  e.code = kind;
  return e;
}

function unescape(s) {
  return String(s == null ? "" : s)
    .replace(/&quot;/g, '"')
    .replace(/&#0?39;/g, "'")
    .replace(/&apos;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&nbsp;/g, " ")
    .replace(/&amp;/g, "&");
}

// ---- the session ----

const session = () => host.storage.get("cookie") || "";
const signedIn = () => session() !== "";

let meCache = null; // { at, me }

function endSession() {
  host.storage.remove("cookie");
  host.storage.remove("account");
  meCache = null;
}

// page reads one of the site's pages, as its own browser reads it, with the
// session's cookie when there is one.
async function page(path) {
  const headers = {
    "User-Agent": UA,
    "Accept-Language": "en-US,en;q=0.9",
    Accept: "text/html,application/xhtml+xml",
  };
  if (signedIn()) headers.Cookie = session();
  const res = await fetch(BASE + path, { headers });
  if (!res.ok) throw fail("unavailable", `HTTP ${res.status}: ${path}`);
  return res.text();
}

// usernameOf reads the signed-in listener's name off the header's profile
// menu, whose link is their page. Without one, they aren't signed in.
function usernameOf(html) {
  const at = String(html).indexOf('id="topRightProfileMenu"');
  if (at < 0) return "";
  const menu = String(html).slice(at, at + 30000);
  const m = /href="\/users\/([^"\/?#]+)"/.exec(menu);
  if (!m) return "";
  return /^(login|signup|search|me)$/i.test(m[1]) ? "" : m[1];
}

function avatarOf(html) {
  const at = String(html).indexOf('id="topRightProfileMenu"');
  if (at < 0) return "";
  const menu = String(html).slice(at, at + 30000);
  const m = /<img[^>]+src="(https:\/\/[^"]+)"/.exec(menu);
  return m ? unescape(m[1]) : "";
}

// account is who the listener is on the site, or null while signed out.
export async function account() {
  if (!signedIn()) return null;
  if (meCache && Date.now() - meCache.at < 300000) return meCache.me;
  try {
    const html = await page("/");
    if (/isLoggedInUser\s*=\s*0/.test(html)) {
      endSession();
      return null;
    }
    const name = usernameOf(html);
    if (!name) return host.storage.get("account") || null;
    const me = { id: name, name, imageUrl: avatarOf(html) };
    host.storage.set("account", me);
    meCache = { at: Date.now(), me };
    return me;
  } catch (e) {
    // Offline, the last known account stands.
    return host.storage.get("account") || null;
  }
}

// signIn shows the site's own sign-in page. The site keeps its session's
// cookie name and renews it at sign-in, so a new value is what says it took.
export async function signIn() {
  if (typeof host.webSignIn !== "function") {
    throw fail("setup", t("This app can't show the site's sign-in page"));
  }
  const { cookie } = await host.webSignIn({
    url: `${BASE}/login`,
    cookiesOf: `${BASE}/`,
    untilChanged: ["sessid", "ss", "PHPSESSID"],
    userAgent: UA,
  });
  host.storage.set("cookie", cookie);
  meCache = null;
  if (!(await account())) {
    endSession();
    throw fail("signIn", t("Pornhub didn't take that sign-in: try again"));
  }
}

// itemsOf reads the cards of a listing or a search page. Each carries the
// video's key, its title and cover on the link, and its length under the
// cover.
function itemsOf(html) {
  const items = [];
  for (const block of String(html).split('<li class="pcVideoListItem').slice(1)) {
    const key = /data-video-vkey="([^"]+)"/.exec(block);
    const link = /<a[^>]+href="\/view_video\.php\?viewkey=([^"]+)"[^>]*\btitle="([^"]+)"/.exec(block);
    const picture = /<img[^>]*?\bsrc="(https:\/\/[^"]+)"/.exec(block);
    const length = /<var[^>]*class="[^"]*\bduration\b[^"]*"[^>]*>([^<]+)<\/var>/.exec(block);
    const id = (link && link[1]) || (key && key[1]);
    if (!id || !link || !picture) continue;
    const title = unescape(link[2]).trim();
    if (!title) continue;
    items.push({
      id,
      title,
      cover: unescape(picture[1]),
      subtitle: length ? length[1].trim() : "",
    });
    if (items.length >= PER_PAGE) break;
  }
  return items;
}

const TABS = [
  { id: "trending", label: "Trending", path: (n) => `/video?o=tr&page=${n}` },
  { id: "hot", label: "Hottest", path: (n) => `/video?o=ht&page=${n}` },
  { id: "viewed", label: "Most Viewed", path: (n) => `/video?o=mv&page=${n}` },
  { id: "japanese", label: "Japanese", path: (n) => `/video/search?search=japanese&page=${n}` },
];

export async function videoTabs() {
  const tabs = TABS.map((chip) => ({ id: chip.id, label: t(chip.label) }));
  if (signedIn() && (await account())) tabs.push({ id: "fav:favorites", label: t("Favorites") });
  return tabs;
}

export async function videoList({ tab, page: n }) {
  const num = Math.max(1, +n || 1);
  if (tab === "fav:favorites") {
    const me = await account();
    if (!me) throw fail("signIn", t("Sign in to Pornhub to see your favorites"));
    const items = itemsOf(await page(`/users/${encodeURIComponent(me.name)}/videos/favorites?page=${num}`));
    return { items, more: items.length >= PER_PAGE };
  }
  const chip = TABS.find((c) => c.id === tab);
  if (!chip) throw fail("unsupported", t("no such page"));
  const items = itemsOf(await page(chip.path(num)));
  return { items, more: items.length >= PER_PAGE };
}

export async function videoSearch({ query, offset }) {
  const n = Math.floor(Math.max(0, +offset || 0) / PER_PAGE) + 1;
  const items = itemsOf(await page(`/video/search?search=${encodeURIComponent(query)}&page=${n}`));
  return { items, more: items.length >= PER_PAGE };
}

// videoObject is the page's JSON-LD for the video: the one with its length.
function videoObject(html) {
  for (const m of String(html).matchAll(/<script type="application\/ld\+json">([\s\S]*?)<\/script>/g)) {
    try {
      const data = JSON.parse(m[1]);
      if (data && (data.duration || data["@type"] === "VideoObject")) return data;
    } catch (e) {
      console.warn(`videoItem: JSON-LD: ${e.message}`);
    }
  }
  return null;
}

function metaOf(html, property) {
  const first = new RegExp(`<meta[^>]+property="${property}"[^>]+content="([^"]*)"`).exec(html);
  if (first) return first[1];
  const second = new RegExp(`<meta[^>]+content="([^"]*)"[^>]+property="${property}"`).exec(html);
  return second ? second[1] : "";
}

// isoSeconds reads a length such as PT00H38M48S, as the JSON-LD writes it.
function isoSeconds(iso) {
  const m = /^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?$/.exec(String(iso || ""));
  if (!m) return 0;
  return (+m[1] || 0) * 3600 + (+m[2] || 0) * 60 + (+m[3] || 0);
}

// videoItem is the video's page: its name, cover and description, and the one
// episode it is.
export async function videoItem({ id }) {
  const html = await page(`/view_video.php?viewkey=${encodeURIComponent(id)}`);
  const ld = videoObject(html) || {};
  const name = unescape(ld.name || metaOf(html, "og:title")).trim() || String(id);
  const thumbnail = Array.isArray(ld.thumbnailUrl) ? ld.thumbnailUrl[0] : ld.thumbnailUrl;
  return {
    id: String(id),
    title: name,
    cover: unescape(thumbnail || metaOf(html, "og:image")),
    intro: unescape(ld.description || metaOf(html, "og:description")).slice(0, 600),
    episodes: [{ id: String(id), title: name, seconds: isoSeconds(ld.duration) }],
  };
}

const installed = {};

// program finds yt-dlp: the one the app ships, one on PATH, or one of the
// usual places.
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
      if (/allowed/.test(e.message)) throw fail("setup", t("Allow Pornhub to start programs in the settings' plugins page"));
    }
  }
  return null;
}

async function ytdlp(args, timeout = TIMEOUT) {
  const bin = await program("yt-dlp", "--version");
  if (!bin) throw fail("setup", t("yt-dlp is not installed on this machine"));
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

// streamOf picks the tallest picture the listener's resolution allows, whole
// with its sound when the site has one that is.
function streamOf(formats) {
  const cap = +(typeof host.setting === "function" ? host.setting("maxHeight") : 0) || 0;
  const height = (f) => Math.max(+(f.height || 0), +(f.width || 0));
  const withURL = (formats || []).filter((f) => f && f.url);
  const fits = (list) => (cap ? list.filter((f) => !height(f) || height(f) <= cap) : list);
  const tallest = (list) => list.slice().sort((a, b) => height(b) - height(a))[0];
  const type = (f) => (/m3u8|hls/.test(f.protocol || "") || /\.m3u8/.test(f.url) ? "hls" : "mp4");

  const whole = withURL.filter((f) => f.vcodec && f.vcodec !== "none" && f.acodec && f.acodec !== "none");
  const one = tallest(fits(whole)) || tallest(whole);
  if (one) return { url: one.url, type: type(one) };

  const picture = withURL.filter((f) => f.vcodec && f.vcodec !== "none");
  const sound = withURL.filter((f) => f.acodec && f.acodec !== "none" && (!f.vcodec || f.vcodec === "none"));
  const video = tallest(fits(picture)) || tallest(picture);
  if (!video) return null;
  const audio = tallest(sound);
  return audio
    ? { url: video.url, audioUrl: audio.url, type: type(video) }
    : { url: video.url, type: type(video) };
}

export async function videoPlay({ id }) {
  const url = `${BASE}/view_video.php?viewkey=${encodeURIComponent(id)}`;
  const j = await ytdlp(["--dump-single-json", url], PLAY_TIMEOUT);
  const stream = streamOf(j.formats) || (j.url ? { url: j.url, type: /\.m3u8/.test(j.url) ? "hls" : "mp4" } : null);
  if (!stream) throw fail("unavailable", t("no playable stream for this video"));
  return stream;
}
