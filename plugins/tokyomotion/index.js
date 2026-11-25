// TokyoMotion (tokyomotion.net) as a source of videos: a Japanese site of
// amateur and clip videos. The grid and the search read the site's own pages,
// whose cards carry the video's number, title, cover and length, and the
// video's page carries its name, cover and description. Signing in on the
// site's page brings the listener's favorites. The stream is the page's own
// source, which wants the site's referer with it. Adult content: the app ships
// this off, and the settings turn it on.

const BASE = "https://www.tokyomotion.net";
const UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36";
const PER_PAGE = 20;

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

function headers() {
  const h = {
    "User-Agent": UA,
    "Accept-Language": "en-US,en;q=0.9",
    Accept: "text/html,application/xhtml+xml",
  };
  if (signedIn()) h.Cookie = session();
  return h;
}

// page reads one of the site's pages, as its own browser reads it, with the
// session's cookie when there is one.
async function page(path) {
  const res = await fetch(BASE + path, { headers: headers() });
  if (!res.ok) throw fail("unavailable", `HTTP ${res.status}: ${path}`);
  return res.text();
}

// nameOf reads a profile page's name off its title, "TOKYO28' Profile - …".
function nameOf(html) {
  const m = /<title>\s*([^<]*?)\s*&#0?39;?\s*Profile/i.exec(String(html));
  return m ? unescape(m[1]).trim() : "";
}

// account is who the listener is on the site, or null while signed out.
// /user/me is the listener's own profile; signed out, it sends the page to
// the sign-in form.
export async function account() {
  if (!signedIn()) return null;
  if (meCache && Date.now() - meCache.at < 300000) return meCache.me;
  try {
    const res = await fetch(BASE + "/user/me", { headers: headers() });
    const html = await res.text();
    const pathname = res.url ? new URL(res.url).pathname : "";
    if (/^\/(login|signup)/.test(pathname)) return null;
    // Pages carry the sign-in form in a modal; only a profile has these.
    if (!pathname && !/Favorite Videos|favorite_video_/i.test(html)) return null;
    let name = "";
    const m = /^\/user\/([^/]+)/.exec(pathname);
    if (m && m[1] !== "me") name = decodeURIComponent(m[1]);
    if (!name) name = nameOf(html);
    if (!name) return host.storage.get("account") || null;
    const me = { id: name, name, imageUrl: "" };
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
    untilChanged: ["AVS", "PHPSESSID", "user_id", "userid"],
    userAgent: UA,
  });
  host.storage.set("cookie", cookie);
  meCache = null;
  if (!(await account())) {
    endSession();
    throw fail("signIn", t("TokyoMotion didn't take that sign-in: try again"));
  }
}

// ---- what the site lists ----

// itemsOf reads the cards of a listing or a search page. Each carries the
// video's number in its link, its title under the cover, and its cover and
// length on it.
function itemsOf(html) {
  const items = [];
  for (const block of String(html).split('<div class="well well-sm">').slice(1)) {
    const link = /<a href="\/video\/(\d+)\/[^"]*"/.exec(block);
    const picture = /<img[^>]*?\bsrc="(https:\/\/[^"]+)"/.exec(block);
    const title = /class="video-title[^"]*"[^>]*>([^<]+)</.exec(block);
    const length = /class="duration">([^<]*)</.exec(block);
    if (!link || !picture || !title) continue;
    const name = unescape(title[1]).trim();
    if (!name) continue;
    items.push({
      id: link[1],
      title: name,
      cover: unescape(picture[1]),
      subtitle: length ? unescape(length[1]).trim() : "",
    });
    if (items.length >= PER_PAGE) break;
  }
  return items;
}

// favoriteItemsOf reads a favorites page, whose cards stand in a div of their
// own rather than the listing's.
function favoriteItemsOf(html) {
  const items = [];
  for (const block of String(html).split(/<div id="favorite_video_\d+"/).slice(1)) {
    const link = /<a href="\/video\/(\d+)\/[^"]*"/.exec(block);
    const picture = /<img[^>]*?\bsrc="(https:\/\/[^"]+)"/.exec(block);
    const title = /class="video-title[^"]*"[^>]*>([^<]+)</.exec(block);
    const length = /class="duration">([^<]*)</.exec(block);
    if (!link || !picture || !title) continue;
    const name = unescape(title[1]).trim();
    if (!name) continue;
    items.push({
      id: link[1],
      title: name,
      cover: unescape(picture[1]),
      subtitle: length ? unescape(length[1]).trim() : "",
    });
    if (items.length >= PER_PAGE) break;
  }
  return items;
}

const TABS = [
  { id: "new", label: "Most Recent", order: "mr" },
  { id: "watching", label: "Being Watched", order: "bw" },
  { id: "viewed", label: "Most Viewed", order: "mv" },
  { id: "top", label: "Top Rated", order: "tr" },
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
    if (!me) throw fail("signIn", t("Sign in to TokyoMotion to see your favorites"));
    const items = favoriteItemsOf(await page(`/user/${encodeURIComponent(me.id)}/favorite/videos?page=${num}`));
    return { items, more: items.length >= PER_PAGE };
  }
  const chip = TABS.find((c) => c.id === tab);
  if (!chip) throw fail("unsupported", t("no such page"));
  const items = itemsOf(await page(`/videos?o=${chip.order}&page=${num}`));
  return { items, more: items.length >= PER_PAGE };
}

export async function videoSearch({ query, offset }) {
  const n = Math.floor(Math.max(0, +offset || 0) / PER_PAGE) + 1;
  const q = encodeURIComponent(query);
  const items = itemsOf(await page(`/search?search_query=${q}&search_type=videos&page=${n}`));
  return { items, more: items.length >= PER_PAGE };
}

function metaOf(html, property) {
  const first = new RegExp(`<meta[^>]+property="${property}"[^>]+content="([^"]*)"`).exec(html);
  if (first) return first[1];
  const second = new RegExp(`<meta[^>]+content="([^"]*)"[^>]+property="${property}"`).exec(html);
  return second ? second[1] : "";
}

// secondsOf reads the page's own length, in seconds with a fraction.
function secondsOf(html) {
  const m = /<meta[^>]+(?:property="video:duration"|itemprop="duration")[^>]+content="([0-9.]+)"/.exec(html);
  return m ? Math.floor(Number(m[1]) || 0) : 0;
}

// videoItem is the video's page: its name, cover and description, and the one
// episode it is.
export async function videoItem({ id }) {
  const html = await page(`/video/${encodeURIComponent(id)}/x`);
  const name = unescape(metaOf(html, "og:title")).trim() || String(id);
  return {
    id: String(id),
    title: name,
    cover: unescape(metaOf(html, "og:image")),
    intro: unescape(metaOf(html, "og:description")).slice(0, 600),
    episodes: [{ id: String(id), title: name, seconds: secondsOf(html) }],
  };
}

export async function videoPlay({ id }) {
  const html = await page(`/video/${encodeURIComponent(id)}/x`);
  const sources = [...String(html).matchAll(/<source src="(https:\/\/[^"]+\/vsrc\/(?:hd|sd)\/[^"]+)"/g)].map((m) => m[1]);
  const hd = sources.find((url) => /\/vsrc\/hd\//.test(url));
  const sd = sources.find((url) => /\/vsrc\/sd\//.test(url));
  const cap = +(typeof host.setting === "function" ? host.setting("maxHeight") : 0) || 0;
  const url = (cap && cap <= 480 ? sd : hd) || hd || sd;
  if (!url) throw fail("unavailable", t("no stream for this video"));
  return {
    url,
    type: "mp4",
    // The site's own file turns away what doesn't come from its pages.
    headers: { Referer: `${BASE}/`, "User-Agent": UA },
  };
}
