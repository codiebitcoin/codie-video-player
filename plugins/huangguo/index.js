// Huangguo short dramas, as the site's own page reads them. The catalog, the
// search, the ranks and the streams come from the site's API, which asks for
// nothing signed. Covers come back as pictures the app fetches itself, with
// the headers this plugin's plugin.json names. Nothing of the site's is kept
// or served here. For study only.

const BASE = "https://huangguoai.com";
const UA = "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/120 Mobile Safari/537.36";
const PER_PAGE = 20;

const t = (text) => host.t(text);

function fail(kind, message) {
  const e = new Error(message || kind);
  e.code = kind;
  return e;
}

// get asks the site and unwraps its {status, data} envelope. An answer with
// no envelope is taken as it is.
async function get(path) {
  const res = await fetch(BASE + path, {
    headers: { "User-Agent": UA, Accept: "application/json" },
  });
  if (!res.ok) throw fail("unavailable", `HTTP ${res.status}: ${path}`);
  const body = await res.json();
  if (body && body.data !== undefined && body.data !== null) {
    if (body.status === 0 && (body.data === "" || (Array.isArray(body.data) && !body.data.length))) {
      throw fail("unavailable", body.msg || path);
    }
    return body.data;
  }
  return body;
}

function dramasOf(data) {
  if (!data) return [];
  if (Array.isArray(data)) return data;
  if (Array.isArray(data.items) && data.items.length) return data.items;
  if (Array.isArray(data.list)) return data.list;
  return [];
}

function pagesOf(data) {
  const n = data && data.pagination && data.pagination.pages;
  return Number(n) || 0;
}

function scoreOf(v) {
  if (v == null || v === "") return "";
  return String(v);
}

function yearOf(created) {
  const y = Number(String(created || "").slice(0, 4));
  return y > 1900 ? y : 0;
}

// itemOf is a card of the grid's for a drama of the site's.
function itemOf(r) {
  const id = r.id != null ? r.id : r.video_id;
  const count = Number(r.episode_count || r.total_episodes || 0);
  let subtitle = (r.author && r.author.name) || "";
  if (count > 0) subtitle = subtitle ? `${subtitle} · ${count} 集` : `${count} 集`;
  return {
    id: String(id),
    title: r.title || "",
    cover: r.cover || "",
    subtitle,
    year: yearOf(r.created_at),
  };
}

let cats = null;

async function categories() {
  if (cats) return cats;
  const data = await get("/api/site/categories/video");
  const raw = [...(data.items || []), ...(data.list || [])];
  cats = raw
    .filter((c) => c && c.name)
    .map((c) => ({ id: String(c.id), name: c.name }));
  return cats;
}

// videoTabs: the ranks and the catalog first, then a chip to each of the
// site's own kinds of drama.
export async function videoTabs() {
  const tabs = [
    { id: "hot", label: t("Popular") },
    { id: "all", label: t("Latest") },
    { id: "year", label: t("This Year") },
  ];
  try {
    for (const c of await categories()) tabs.push({ id: `cat:${c.id}`, label: c.name });
  } catch (e) {
    console.warn(`videoTabs: categories: ${e.message}`);
  }
  return tabs;
}

// videoList is a page of what a chip lists. The year chip keeps what came out
// this year out of the catalog's own pages.
export async function videoList({ tab, page }) {
  const n = Math.max(1, +page || 1);
  if (tab === "hot") {
    const data = await get("/api/ranks/hot");
    return { items: dramasOf(data).map(itemOf), more: false };
  }
  if (tab === "all" || String(tab || "").startsWith("cat:")) {
    const cate = tab === "all" ? "" : `&cate_id=${encodeURIComponent(String(tab).slice(4))}`;
    const data = await get(`/api/videos?page=${n}${cate}`);
    const pages = pagesOf(data);
    return { items: dramasOf(data).map(itemOf), more: pages ? n < pages : dramasOf(data).length >= PER_PAGE };
  }
  if (tab === "year") {
    const data = await get(`/api/videos?page=${n}`);
    const year = new Date().getFullYear();
    const pages = pagesOf(data);
    return {
      items: dramasOf(data).map(itemOf).filter((it) => it.year === year),
      more: pages ? n < pages : false,
    };
  }
  throw fail("unsupported", t("no such page"));
}

// videoSearch is what the site's search says, a page of twenty, which the
// offset counts.
export async function videoSearch({ query, offset }) {
  const pg = Math.floor(Math.max(0, +offset || 0) / PER_PAGE) + 1;
  const data = await get(`/api/search?q=${encodeURIComponent(query)}&page=${pg}`);
  const items = dramasOf(data).map(itemOf);
  return { items, more: items.length >= PER_PAGE };
}

// videoItem is a drama and its episodes. A drama that doesn't list them gets
// one to each of the count it gives, numbered, which is how its own page plays.
export async function videoItem({ id }) {
  const r = await get(`/api/videos/${encodeURIComponent(id)}`);
  const card = itemOf(r);
  let episodes = (r.episodes || []).map((ep) => ({
    id: String(ep.ep_num),
    title: `第${ep.ep_num}集`,
    seconds: Number(ep.duration) || 0,
  }));
  const count = Number(r.episode_count || r.total_episodes || 0);
  if (!episodes.length) {
    episodes = [];
    for (let n = 1; n <= count; n++) episodes.push({ id: String(n), title: `第${n}集` });
  }
  return {
    ...card,
    intro: r.description || r.synopsis || "",
    finished: !!r.is_finished,
    score: scoreOf(r.score),
    episodes,
  };
}

// videoPlay is the HLS address an episode plays through, its signature already
// on it. The picture-and-sound stream comes first, then the one without sound
// of its own, then the preview.
export async function videoPlay({ id, episode }) {
  const ep = episode || "1";
  const res = await fetch(`${BASE}/api/videos/${encodeURIComponent(id)}/episodes/${encodeURIComponent(ep)}/playback`, {
    method: "POST",
    headers: { "User-Agent": UA, "Content-Type": "application/json" },
    body: "{}",
  });
  const body = await res.json().catch(() => null);
  const streams = body && body.data && body.data.streams;
  const url = streams && (streams.main || streams.hevc || streams.preview);
  if (!url) throw fail("unavailable", (body && body.msg) || t("no stream for this episode"));
  return { url, type: "hls" };
}
