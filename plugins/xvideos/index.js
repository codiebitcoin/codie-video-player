// XVideos as a source of videos. The grid and the search read the site's own
// pages: a card carries the video's key, title, cover and length, and the
// video's page carries its name, cover, description and length as its JSON-LD
// does. The page's own player setup names the streams. Adult content: the app
// ships this off, and the settings turn it on.

const BASE = "https://www.xvideos.com";
const UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36";
const CAP = 60;

const t = (text) => host.t(text);

function fail(kind, message) {
  const e = new Error(message || kind);
  e.code = kind;
  return e;
}

// unescape reads the few entities the site writes into its text, &comma;
// among them.
function unescape(s) {
  const named = {
    "&amp;": "&",
    "&quot;": '"',
    "&apos;": "'",
    "&lt;": "<",
    "&gt;": ">",
    "&nbsp;": " ",
    "&comma;": ",",
    "&period;": ".",
    "&hellip;": "…",
    "&mdash;": "—",
    "&ndash;": "–",
    "&lsquo;": "‘",
    "&rsquo;": "’",
    "&ldquo;": "“",
    "&rdquo;": "”",
  };
  let out = String(s == null ? "" : s);
  for (const [entity, text] of Object.entries(named)) {
    out = out.replace(new RegExp(entity, "g"), text);
  }
  return out.replace(/&#(\d+);/g, (_, n) => String.fromCodePoint(Number(n)));
}

// page reads one of the site's pages, as its own browser reads it.
async function page(path) {
  const res = await fetch(BASE + path, {
    headers: {
      "User-Agent": UA,
      "Accept-Language": "en-US,en;q=0.9",
      Accept: "text/html,application/xhtml+xml",
    },
  });
  if (!res.ok) throw fail("unavailable", `HTTP ${res.status}: ${path}`);
  return res.text();
}

// videoID is a card's link as the app keeps it: the site's key for the video,
// and its slug, without the numbers the listing puts between them.
function videoID(href) {
  const m = /^\/video\.([0-9a-z]+)\/(.+)$/.exec(href);
  if (!m) return "";
  let parts = m[2].split("/").filter(Boolean);
  if (parts.length >= 3 && /^\d+$/.test(parts[0]) && /^(THUMBNUM|0)$/i.test(parts[1])) {
    parts = parts.slice(2);
  }
  return `${m[1]}/${parts.join("/")}`;
}

// itemsOf reads the cards of a listing or a search page. Each carries the
// video's link, its cover in data-src, and its title and length under them.
function itemsOf(html) {
  const items = [];
  const blocks = String(html).split(/<div id="video-thumb-|<div id="video_/).slice(1);
  for (const block of blocks) {
    const link = /href="(\/video\.[^"]+)"/.exec(block);
    const id = link ? videoID(link[1]) : "";
    const picture = /data-src="(https:\/\/[^"]+)"/.exec(block);
    const title = /class="title"[^>]*\btitle="([^"]+)"/.exec(block) || /<a[^>]+\btitle="([^"]+)"/.exec(block);
    const length = /class="duration">([^<]+)</.exec(block);
    if (!id || !picture || !title) continue;
    const name = unescape(title[1]).trim();
    if (!name) continue;
    items.push({
      id,
      title: name,
      // A listing leaves the cover's number to its own script, which rotates
      // them; the first one is always there.
      cover: unescape(picture[1]).replace("THUMBNUM", "1"),
      subtitle: length ? unescape(length[1]).trim() : "",
    });
    if (items.length >= CAP) break;
  }
  return items;
}

// moreOf says whether the page has a page after it.
function moreOf(html) {
  return /<a[^>]+class="[^"]*next-page/.test(String(html));
}

const TABS = [
  { id: "new", label: "New" },
  { id: "best", label: "Best of Month" },
  { id: "japanese", label: "Japanese" },
];

export async function videoTabs() {
  return TABS.map((chip) => ({ id: chip.id, label: t(chip.label) }));
}

export async function videoList({ tab, page: n }) {
  const num = Math.max(1, +n || 1);
  if (tab === "best") {
    // The month's best is one page, whatever page the app asks for.
    if (num > 1) return { items: [], more: false };
    const html = await page("/best");
    return { items: itemsOf(html), more: false };
  }
  if (tab === "japanese") {
    const html = await page(`/?k=japanese&p=${num - 1}`);
    return { items: itemsOf(html), more: moreOf(html) };
  }
  if (tab !== "new") throw fail("unsupported", t("no such page"));
  const html = await page(num === 1 ? "/" : `/new/${num - 1}`);
  return { items: itemsOf(html), more: moreOf(html) };
}

export async function videoSearch({ query, offset }) {
  const n = Math.floor(Math.max(0, +offset || 0) / 20) + 1;
  const html = await page(`/?k=${encodeURIComponent(query)}&p=${n - 1}`);
  return { items: itemsOf(html), more: moreOf(html) };
}

// jsonLdOf is the page's JSON-LD for the video.
function jsonLdOf(html) {
  const m = /<script type="application\/ld\+json">([\s\S]*?)<\/script>/.exec(String(html));
  if (!m) return null;
  try {
    return JSON.parse(m[1]);
  } catch (e) {
    console.warn(`videoItem: JSON-LD: ${e.message}`);
    return null;
  }
}

function metaOf(html, name) {
  const first = new RegExp(`<meta[^>]+(?:property|name)="${name}"[^>]+content="([^"]*)"`).exec(html);
  if (first) return first[1];
  const second = new RegExp(`<meta[^>]+content="([^"]*)"[^>]+(?:property|name)="${name}"`).exec(html);
  return second ? second[1] : "";
}

// isoSeconds reads a length such as PT00H10M00S, as the JSON-LD writes it.
function isoSeconds(iso) {
  const m = /^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?$/.exec(String(iso || ""));
  if (!m) return 0;
  return (+m[1] || 0) * 3600 + (+m[2] || 0) * 60 + (+m[3] || 0);
}

// videoItem is the video's page: its name, cover and description, and the one
// episode it is.
export async function videoItem({ id }) {
  const html = await page(`/video.${id}`);
  const ld = jsonLdOf(html) || {};
  const thumbnail = Array.isArray(ld.thumbnailUrl) ? ld.thumbnailUrl[0] : ld.thumbnailUrl;
  const name = unescape(ld.name || metaOf(html, "og:title")).trim() || String(id);
  return {
    id: String(id),
    title: name,
    cover: unescape(thumbnail || metaOf(html, "og:image")),
    intro: unescape(ld.description || metaOf(html, "description")).slice(0, 600),
    episodes: [{ id: String(id), title: name, seconds: isoSeconds(ld.duration) }],
  };
}

export async function videoPlay({ id }) {
  const html = await page(`/video.${id}`);
  const hls = /setVideoHLS\('([^']+)'\)/.exec(html);
  if (hls) return { url: unescape(hls[1]), type: "hls" };
  const mp4 = /setVideoUrlHigh\('([^']+)'\)/.exec(html) || /setVideoUrlLow\('([^']+)'\)/.exec(html);
  if (!mp4) throw fail("unavailable", t("no playable stream for this video"));
  return { url: unescape(mp4[1]), type: "mp4" };
}
