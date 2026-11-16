/* fnOS 对象存储管理控制台。
   刻意使用原生 JS：无构建步骤、不依赖 CDN，可嵌入 fnOS 桌面
   iframe 运行，也支持离线使用。所有服务端数据在写入 DOM 前均已转义。 
   该项目使用古法编程历经33天，上线1.0版本
   起初只用于自己的博客，可预览 https://www.infowe.site/post/104 该文章内，就是采用的当前项目构建的*/
(() => {
  "use strict";

  const app = document.getElementById("app");
  const boot = document.getElementById("boot");
  const pageEl = () => document.getElementById("page");
  const modalRoot = document.getElementById("modal-root");
  const toastRoot = document.getElementById("toast-root");

  const state = {
    session: null,
    route: "overview",
    buckets: [],
    keys: [],
    overview: null,
    settings: null,
    activity: [],
    revealedSecrets: new Set(),
    connectTab: "aws",
    bannerDismissed: sessionStorage.getItem("fnos-oss-banner") === "1",
    prefixFilter: "",
  };

  // ------------------------------------------------------------- utilities
  const apiUrl = (p) => new URL(String(p).replace(/^\/+/, ""), document.baseURI).toString();

  async function api(path, opts = {}) {
    const init = { ...opts, headers: { ...(opts.headers || {}) } };
    if (init.body && !(init.body instanceof FormData)) {
      init.headers["Content-Type"] = "application/json";
      if (typeof init.body !== "string") init.body = JSON.stringify(init.body);
    }
    const res = await fetch(apiUrl(path), init);
    let data = null;
    const text = await res.text();
    if (text) {
      try { data = JSON.parse(text); } catch { data = { raw: text }; }
    }
    if (!res.ok) {
      const err = new Error((data && (data.error || data.message)) || `请求失败（HTTP ${res.status}）`);
      err.status = res.status;
      err.data = data;
      throw err;
    }
    return data;
  }

  const esc = (v) => String(v == null ? "" : v)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;").replace(/'/g, "&#39;");

  const enc = encodeURIComponent;

  function fmtBytes(n) {
    n = Number(n) || 0;
    if (n < 1024) return `${n} B`;
    const units = ["KB", "MB", "GB", "TB", "PB"];
    let v = n / 1024, i = 0;
    while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
    return `${v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2)} ${units[i]}`;
  }
  const fmtNum = (n) => (Number(n) || 0).toLocaleString("zh-CN");
  function fmtDate(v) {
    if (!v) return "—";
    const d = new Date(v);
    if (isNaN(d)) return "—";
    return d.toLocaleString("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
  }
  function fmtAgo(v) {
    if (!v) return "—";
    const s = Math.max(0, (Date.now() - new Date(v).getTime()) / 1000);
    if (s < 60) return "刚刚";
    if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
    if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
    return `${Math.floor(s / 86400)} 天前`;
  }
  function fmtUptime(sec) {
    sec = Number(sec) || 0;
    const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
    if (d) return `${d} 天 ${h} 小时`;
    if (h) return `${h} 小时 ${m} 分`;
    return `${m} 分钟`;
  }
  const q = (s) => new URLSearchParams(s);

  // ------------------------------------------------------------- icons
  const ICONS = {
    overview: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
    bucket: '<path d="M4 7h16l-1.6 12.2a2 2 0 0 1-2 1.8H7.6a2 2 0 0 1-2-1.8L4 7Z"/><path d="M8 7V5a4 4 0 0 1 8 0v2"/><path d="M4 11h16"/>',
    key: '<circle cx="8" cy="15" r="4"/><path d="m11 12 9-9"/><path d="m17 6 3 3"/><path d="m14 9 2 2"/>',
    plug: '<path d="M9 2v6M15 2v6"/><path d="M6 8h12v3a6 6 0 0 1-6 6 6 6 0 0 1-6-6V8Z"/><path d="M12 17v5"/>',
    shield: '<path d="M12 3 5 6v6c0 4.4 3 7.7 7 9 4-1.3 7-4.6 7-9V6l-7-3Z"/><path d="m9.5 12 1.8 1.8L15 10"/>',
    settings: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.8 2.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.5V21h-4v-.1a1.7 1.7 0 0 0-1-1.5 1.7 1.7 0 0 0-1.9.3l-.1.1L4.2 17l.1-.1a1.7 1.7 0 0 0 .3-1.9 1.7 1.7 0 0 0-1.5-1H3v-4h.1a1.7 1.7 0 0 0 1.5-1 1.7 1.7 0 0 0-.3-1.9L4.2 7 7 4.2l.1.1a1.7 1.7 0 0 0 1.9.3 1.7 1.7 0 0 0 1-1.5V3h4v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 7l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.5 1H21v4h-.1a1.7 1.7 0 0 0-1.5 1Z"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    refresh: '<path d="M20 11a8 8 0 1 0-2.3 5.7"/><path d="M20 4v7h-7"/>',
    trash: '<path d="M4 7h16"/><path d="M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/><path d="m6 7 1 13a1 1 0 0 0 1 .9h8a1 1 0 0 0 1-.9l1-13"/><path d="M10 11v6M14 11v6"/>',
    download: '<path d="M12 3v12"/><path d="m7 10 5 5 5-5"/><path d="M4 21h16"/>',
    archive: '<rect x="3" y="4" width="18" height="4" rx="1"/><path d="M5 8v11a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8"/><path d="M10 12h4"/>',
    upload: '<path d="M12 21V9"/><path d="m7 14 5-5 5 5"/><path d="M4 3h16"/>',
    copy: '<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1"/>',
    eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/>',
    eyeoff: '<path d="m3 3 18 18"/><path d="M10.6 5.2A10.9 10.9 0 0 1 12 5c6.5 0 10 7 10 7a17 17 0 0 1-3 3.8"/><path d="M6.1 6.1C3.4 8 2 12 2 12s3.5 7 10 7a10 10 0 0 0 4.3-1"/><path d="M9.9 9.9a3 3 0 0 0 4.2 4.2"/>',
    external: '<path d="M14 4h6v6"/><path d="M20 4 10 14"/><path d="M20 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1h5"/>',
    back: '<path d="M19 12H5"/><path d="m11 18-6-6 6-6"/>',
    database: '<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 1.7 3.6 3 8 3s8-1.3 8-3V5"/><path d="M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/>',
    disk: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 4v6h10V4"/><path d="M7 16h.01M11 16h6"/>',
    activity: '<path d="M3 12h4l2.5-7 5 14L17 12h4"/>',
    clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
    search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
    folder: '<path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6Z"/>',
    file: '<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9Z"/><path d="M14 3v6h6"/>',
    close: '<path d="M6 6l12 12M18 6 6 18"/>',
    check: '<path d="m4 12 5 5L20 6"/>',
    alert: '<path d="M12 3 2.5 20h19L12 3Z"/><path d="M12 9v5"/><path d="M12 17h.01"/>',
    lock: '<rect x="4" y="10" width="16" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
    moon: '<path d="M20 14.5A8 8 0 0 1 9.5 4 8.5 8.5 0 1 0 20 14.5Z"/>',
    info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5"/><path d="M12 8h.01"/>',
    network: '<rect x="9" y="2" width="6" height="6" rx="1"/><rect x="3" y="16" width="6" height="6" rx="1"/><rect x="15" y="16" width="6" height="6" rx="1"/><path d="M12 8v4M6 16v-2h12v2"/>',
    terminal: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3"/><path d="M13 15h4"/>',
    book: '<path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2Z"/>',
    server: '<rect x="3" y="4" width="18" height="6" rx="2"/><rect x="3" y="14" width="18" height="6" rx="2"/><path d="M7 7h.01M7 17h.01"/>',
    link: '<path d="M10 13a5 5 0 0 0 7.1 0l2-2a5 5 0 0 0-7.1-7.1l-1 1"/><path d="M14 11a5 5 0 0 0-7.1 0l-2 2a5 5 0 0 0 7.1 7.1l1-1"/>',
    share: '<circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><path d="m8.6 10.6 6.8-4.2M8.6 13.4l6.8 4.2"/>',
    arrowup: '<path d="m6 15 6-6 6 6"/>',
    menu: '<path d="M4 7h16M4 12h16M4 17h16"/>',
    ring: '<circle cx="11" cy="11" r="6.4"/><rect x="12.4" y="12.4" width="5.6" height="5.6" rx="1.6" fill="currentColor" stroke="none"/>',
  };
  const icon = (name, cls = "") =>
    `<svg class="${cls}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[name] || ""}</svg>`;

  // ------------------------------------------------------------- theme
  // fnOS 1.2.0401+ exposes the theme through the open API:
  // getPlatformConfig().theme and the "os/theme" host event. "auto" prefers
  // those, falls back to sniffing a same-origin desktop iframe, then the OS
  // preference. A manual choice in the top bar or settings overrides all.
  const THEME_KEY = "fnos-oss-theme";
  const THEME_MODES = ["auto", "light", "dark"];
  let sdkTheme = "";

  function normalizeTheme(v) {
    const s = typeof v === "string" ? v.trim().toLowerCase() : "";
    if (s === "dark" || s === "night" || s === "30") return "dark";
    if (s === "light" || s === "day") return "light";
    return "";
  }

  function systemTheme() {
    return window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }

  function parentTheme() {
    try {
      const p = window.parent;
      if (!p || p === window || !p.document) return "";
      const nodes = [p.document.documentElement, p.document.body].filter(Boolean);
      // Semi Design (the fnOS desktop) puts theme-mode on html/body.
      for (const el of nodes) {
        const attr = normalizeTheme(el.getAttribute("theme-mode")) ||
          normalizeTheme(el.getAttribute("data-theme")) ||
          normalizeTheme(el.dataset && el.dataset.theme);
        if (attr) return attr;
      }
      // The Semi colour variables live on body, not documentElement.
      for (const el of nodes) {
        const style = p.getComputedStyle(el);
        const bg = style.getPropertyValue("--semi-color-bg-0") || style.getPropertyValue("--semi-color-bg-1");
        const m = /rgba?\((\d+)[,\s]+(\d+)[,\s]+(\d+)/.exec(bg || "");
        if (m) {
          const lum = 0.2126 * +m[1] + 0.7152 * +m[2] + 0.0722 * +m[3];
          return lum < 128 ? "dark" : "light";
        }
      }
      // Older fnOS stores the mode as a number string; 30 means dark.
      return normalizeTheme(p.localStorage.getItem("fnos-theme-mode")) ||
        normalizeTheme(p.localStorage.getItem("fnos-theme")) ||
        normalizeTheme(p.localStorage.getItem("theme-mode"));
    } catch (e) { /* cross-origin or detached parent */ }
    return "";
  }

  function themePreference() {
    const v = localStorage.getItem(THEME_KEY);
    return THEME_MODES.includes(v) ? v : "auto";
  }

  function resolvedTheme() {
    const pref = themePreference();
    if (pref === "light" || pref === "dark") return pref;
    // A live same-origin desktop reading wins over the SDK's startup snapshot;
    // the snapshot is what makes cross-origin hosts follow along too.
    return parentTheme() || sdkTheme || systemTheme();
  }

  function applyTheme() {
    const theme = resolvedTheme();
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
    applyThemeIcon();
  }

  function watchTheme() {
    const mq = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;
    if (mq) mq.addEventListener("change", () => { if (themePreference() === "auto") applyTheme(); });
    window.addEventListener("storage", (e) => {
      if (e.key === THEME_KEY || e.key === "fnos-theme-mode" || e.key === "fnos-theme" || e.key === "theme-mode") applyTheme();
    });
    try {
      const p = window.parent;
      if (p && p !== window) {
        new MutationObserver(() => { if (themePreference() === "auto") applyTheme(); })
          .observe(p.document.documentElement, { attributes: true, attributeFilter: ["class", "style", "data-theme", "theme-mode"] });
        if (p.document.body) {
          new MutationObserver(() => { if (themePreference() === "auto") applyTheme(); })
            .observe(p.document.body, { attributes: true, attributeFilter: ["class", "style", "data-theme", "theme-mode"] });
        }
      }
    } catch (e) { /* cross-origin parent */ }
  }

  // Reads the host theme through fnOS' open API and keeps it in sync:
  // getPlatformConfig().theme for the current value, $on("os/theme") for
  // changes. The SDK is already vendored for the folder picker, so no extra
  // download. Both calls are no-ops outside the Web host.
  async function initHostTheme() {
    let sdk;
    try {
      sdk = await loadTrimSDK();
    } catch (e) { return; }

    const applyHost = (theme) => {
      const next = normalizeTheme(theme);
      if (!next) return;
      sdkTheme = next;
      if (themePreference() === "auto") applyTheme();
    };

    try {
      const cfg = await sdk.getPlatformConfig();
      applyHost(cfg && cfg.theme);
    } catch (e) { /* standalone browser or older fnOS */ }

    if (sdk.isWeb === true && sdk.isStandaloneWeb === false) {
      try {
        await sdk.$on("os/theme", applyHost);
      } catch (e) { /* $on is Web-host only */ }
    }
  }

  // ------------------------------------------------------------- shell
  function renderShell() {
    const s = state.session || {};
    const user = s.user || {};
    const name = user.name || "管理员";
    const initial = (name[0] || "A").toUpperCase();
    const nav = [
      ["overview", "概览", "overview"],
      ["buckets", "存储桶", "bucket"],
      ["keys", "访问密钥", "key"],
      ["connect", "连接配置", "plug"],
    ];
    const nav2 = [
      ["network", "网络与安全", "shield"],
      ["settings", "设置", "settings"],
      ["docs", "开发文档", "book"],
    ];
    const item = ([r, label, ic]) =>
      r === "docs"
        ? `<button class="nav-item" data-act="help" data-nav="docs">${icon(ic)}<span>${label}</span></button>`
        : `<a class="nav-item" href="#/${r}" data-nav="${r}">${icon(ic)}<span>${label}</span></a>`;
    app.innerHTML = `
      <div class="nav-scrim" data-act="nav-close" aria-hidden="true"></div>
      <aside class="sidebar" id="sidebar">
        <div class="side-head">
          <div class="side-brand">
            <span class="side-brand-mark">${icon("ring")}</span>
            <span class="side-brand-text"><b>轻量对象存储</b><i>fnOS OSS</i></span>
          </div>
          <button class="icon-btn side-close" data-act="nav-close" title="收起导航" aria-label="收起导航">${icon("close")}</button>
        </div>
        <nav class="nav">
          <div class="nav-label">存储管理</div>
          ${nav.map(item).join("")}
          <div class="nav-label">系统</div>
          ${nav2.map(item).join("")}
        </nav>
        <div class="side-foot">
          <div class="side-status">
            <div class="row"><span>服务状态</span><span style="display:flex;align-items:center;gap:6px"><i class="dot"></i><b>运行中</b></span></div>
            <div class="row"><span>S3 端口</span><b id="side-port">${esc(state.overview ? state.overview.port : "—")}</b></div>
          </div>
        </div>
      </aside>
      <div class="main">
        <header class="topbar">
          <div class="topbar-left">
            <button class="icon-btn nav-toggle" data-act="nav-open" title="打开导航" aria-label="打开导航" aria-controls="sidebar" aria-expanded="false">${icon("menu")}</button>
            <div class="crumb" id="crumb"></div>
          </div>
          <div class="top-actions">
            <button class="icon-btn" data-act="theme" title="切换外观">${icon("moon")}</button>
            <button class="icon-btn" data-act="help" title="开发文档">${icon("book")}</button>
            <div class="user-chip">
              <div class="avatar">${esc(initial)}</div>
              <div class="user-meta"><b>${esc(name)}</b><span>${s.devMode ? "开发模式" : "飞牛管理员"}</span></div>
            </div>
          </div>
        </header>
        <div class="content"><div id="page" class="page"></div></div>
      </div>`;
    app.hidden = false;
    boot.hidden = true;
    applyThemeIcon();
    updateNav();
    setCrumb();
    updateSidePort();
    watchViewport();
  }

  function updateSidePort() {
    const el = document.getElementById("side-port");
    if (!el) return;
    const port = state.overview && state.overview.port;
    el.textContent = port ? String(port) : "—";
  }

  function updateNav() {
    app.querySelectorAll("[data-nav]").forEach((el) => {
      el.classList.toggle("active", el.dataset.nav === state.route);
    });
  }

  // Mobile navigation is an off-canvas drawer: the toggle opens it, the scrim,
  // the close button, Esc and picking a destination all dismiss it.
  function setNavOpen(open) {
    app.classList.toggle("nav-open", open);
    document.body.classList.toggle("nav-lock", open);
    const toggle = app.querySelector('[data-act="nav-open"]');
    if (toggle) toggle.setAttribute("aria-expanded", open ? "true" : "false");
  }

  function closeNav() {
    if (app.classList.contains("nav-open")) setNavOpen(false);
  }

  function watchViewport() {
    const mq = window.matchMedia ? window.matchMedia("(min-width: 901px)") : null;
    if (mq) mq.addEventListener("change", (e) => { if (e.matches) closeNav(); });
  }

  function setCrumb(extra) {
    const labels = {
      overview: "概览", buckets: "存储桶", keys: "访问密钥",
      connect: "连接配置", network: "网络与安全", settings: "设置",
    };
    const crumb = document.getElementById("crumb");
    if (!crumb) return;
    crumb.innerHTML = extra
      ? `<span>${esc(labels[state.route] || "")}</span><span class="sep">/</span><b>${esc(extra)}</b>`
      : `<b>${esc(labels[state.route] || "轻量对象存储")}</b>`;
    document.title = labels[state.route] ? `${labels[state.route]} · 轻量对象存储` : "轻量对象存储";
  }

  function applyThemeIcon() {
    const btn = app.querySelector('[data-act="theme"]');
    if (!btn) return;
    const dark = document.documentElement.dataset.theme === "dark";
    btn.innerHTML = icon(dark ? "sun" : "moon");
    btn.title = dark ? "切换到浅色" : "切换到深色";
  }

  // ------------------------------------------------------------- gate
  function renderGate(title, message, code) {
    app.innerHTML = `<div class="gate"><div class="gate-card">
      <div class="gate-icon">${icon("lock")}</div>
      <h1>${esc(title)}</h1>
      <p>${esc(message)}</p>
      ${code ? `<div class="code">${esc(code)}</div>` : ""}
      <div class="pill-row" style="justify-content:center">
        <button class="btn" data-act="reload">重新连接</button>
        <a class="btn btn-primary" href="${esc(location.pathname)}">刷新页面</a>
      </div>
    </div></div>`;
    app.hidden = false;
    boot.hidden = true;
  }

  // ------------------------------------------------------------- overview
  async function renderOverview() {
    page("");
    const [ov, act] = await Promise.all([
      api("/api/overview"),
      api("/api/activity?limit=10"),
    ]);
    state.overview = ov;
    state.activity = act.items || [];
    const st = ov.stats || {};
    const total = Number(st.storageTotal) || 0;
    const used = Number(st.size) || 0;
    const pct = total > 0 ? Math.min(100, (used / total) * 100) : 0;
    const settings = state.settings || {};
    const endpoint = settings.publicEndpoint || ov.endpoint;
    const preview = ov.bucketsPreview || [];

    page(`
      <div class="page-head">
        <div><h1>概览</h1><p>本地 S3 对象存储的运行状态、容量与访问入口。</p></div>
        <div class="head-actions">
          <button class="btn" data-act="refresh">${icon("refresh")}刷新</button>
          <button class="btn btn-primary" data-act="new-bucket">${icon("plus")}新建存储桶</button>
        </div>
      </div>
      ${state.bannerDismissed ? "" : banner()}
      <div class="stack" style="margin-top:16px">
        <div class="stat-grid">
          ${stat("存储桶", fmtNum(st.buckets), "个", "bucket")}
          ${stat("对象总数", fmtNum(st.objects), "个", "file", `未完成的分段上传 ${fmtNum(st.uploads)} 个`)}
          ${stat("已用容量", fmtBytes(used), "", "database", `共 ${fmtNum(st.buckets)} 个存储桶`)}
          ${stat("运行时长", fmtUptime(ov.uptimeSeconds), "", "clock", `v${esc(ov.version)}`)}
        </div>
        <div class="grid2">
          <div class="card">
            <div class="card-head"><h2>存储容量</h2><span class="badge">${total ? "本地存储空间" : "仅统计对象"}</span></div>
            <div class="card-body">
              <div style="display:flex;justify-content:space-between;align-items:flex-end;margin-bottom:10px">
                <div><div class="muted tiny">对象占用</div><div style="font-size:24px;font-weight:650;letter-spacing:-.02em">${fmtBytes(used)}</div></div>
                <div style="text-align:right"><div class="muted tiny">${total ? "可用空间" : "对象数量"}</div><div class="strong">${total ? fmtBytes(st.storageFree) : fmtNum(st.objects) + " 个"}</div></div>
              </div>
              <div class="progress"><i style="width:${total ? Math.max(pct, 0.6) : 0}%"></i></div>
              <div class="legend"><span>${total ? `已使用 ${pct.toFixed(1)}%` : "数据目录位于应用数据卷"}</span><span>${total ? `总容量 ${fmtBytes(total)}` : ""}</span></div>
              <div class="divider"></div>
              <div class="kv">
                <div class="row"><span class="k">${icon("server")}S3 Endpoint</span><span class="v"><code class="mono">${esc(endpoint)}</code><span class="badge ${ov.publicEndpoint ? "ok" : "warn"}">${ov.publicEndpoint ? "反向代理" : "局域网直连"}</span><button class="icon-btn" data-act="copy" data-copy="${esc(endpoint)}" title="复制">${icon("copy")}</button></span></div>
                <div class="row"><span class="k">${icon("network")}区域 Region</span><span class="v mono">${esc(ov.region)}</span></div>
                <div class="row"><span class="k">${icon("shield")}管理入口</span><span class="v"><span class="badge ok">飞牛统一网关</span><code class="mono">${esc(ov.gatewayPrefix)}</code></span></div>
              </div>
            </div>
          </div>
          <div class="card">
            <div class="card-head"><h2>服务状态</h2><span class="badge ok"><i class="dot"></i>运行中</span></div>
            <div class="card-body">
              <div class="kv">
                <div class="row"><span class="k">${icon("server")}TCP 端口</span><span class="v"><b>${esc(ov.port)}</b><span class="badge outline">仅 S3 API</span></span></div>
                <div class="row"><span class="k">${icon("database")}数据目录</span><span class="v mono">${esc(ov.dataDir || "")}</span></div>
                <div class="row"><span class="k">${icon("key")}访问密钥</span><span class="v">${fmtNum(ov.keys.total)} 个 <span class="badge ${ov.keys.enabled ? "ok" : "warn"}">${fmtNum(ov.keys.enabled)} 启用</span></span></div>
                <div class="row"><span class="k">${icon("activity")}最近一小时请求</span><span class="v">${fmtNum(ov.requestsLastHour)} 次</span></div>
                <div class="row"><span class="k">${icon("clock")}启动时间</span><span class="v">${fmtDate(ov.startedAt)}</span></div>
              </div>
            </div>
          </div>
        </div>
        <div class="grid2-even">
          <div class="card">
            <div class="card-head"><h2>存储桶</h2><a class="btn btn-sm btn-ghost" href="#/buckets">全部 ${icon("external")}</a></div>
            ${preview.length ? `<div class="table-wrap"><table class="table"><thead><tr><th>名称</th><th>策略</th><th>创建时间</th></tr></thead><tbody>
              ${preview.map((b) => `<tr class="clickable" data-act="open-bucket" data-bucket="${esc(enc(b.name))}">
                <td><div class="name-cell"><span class="ico">${icon("bucket")}</span><div class="txt"><b>${esc(b.name)}</b><span title="${esc(b.rootPath || "s3://" + b.name)}">${b.rootPath ? "目录 · " + esc(b.rootPath) : "s3://" + esc(b.name)}</span></div></div></td>
                <td>${policyBadge(b.policy)}</td><td class="dim nowrap">${fmtDate(b.created)}</td></tr>`).join("")}
            </tbody></table></div>` : empty("还没有存储桶", "创建一个存储桶开始存放对象。", "新建存储桶", "new-bucket")}
          </div>
          <div class="card">
            <div class="card-head"><h2>最近活动</h2><span class="hint tiny">仅保留最近 500 条</span></div>
            ${state.activity.length ? `<div class="table-wrap"><table class="table"><thead><tr><th>时间</th><th>请求</th><th>状态</th><th style="text-align:right">耗时</th></tr></thead><tbody>
              ${state.activity.map(activityRow).join("")}
            </tbody></table></div>` : `<div class="empty"><div class="e-icon">${icon("activity")}</div><b>暂无请求</b><p>使用 S3 客户端连接后，请求会显示在这里。</p></div>`}
          </div>
        </div>
      </div>`);
    setCrumb();
  }

  function banner() {
    return `<div class="banner warn">
      <div class="b-icon">${icon("shield")}</div>
      <div class="b-body">
        <b>TCP 端口未经过飞牛网关，请勿直接暴露到公网</b>
        <p>S3 服务监听在 TCP ${esc(state.overview ? state.overview.port : "")} 端口，仅建议在局域网内使用。如需公网访问，请使用 Lucky、frp 或 Nginx 反向代理，并启用 HTTPS 与访问控制；不要在路由器上直接映射该端口。</p>
        <div class="b-actions">
          <a class="btn btn-sm" href="#/network">${icon("shield")}查看公网访问方案</a>
          <button class="btn btn-sm btn-ghost" data-act="dismiss-banner">不再提示</button>
        </div>
      </div>
    </div>`;
  }

  function stat(label, value, unit, ic, sub) {
    return `<div class="stat">
      <div class="stat-top"><span class="stat-icon">${icon(ic)}</span>${esc(label)}</div>
      <div class="value">${esc(value)}${unit ? `<small>${esc(unit)}</small>` : ""}</div>
      <div class="sub">${sub ? esc(sub) : "&nbsp;"}</div>
    </div>`;
  }

  function policyBadge(p) {
    return p === "public-read"
      ? `<span class="badge warn">公共读</span>`
      : `<span class="badge">私有</span>`;
  }

  function activityRow(a) {
    const ok = Number(a.status) >= 200 && Number(a.status) < 400;
    return `<tr>
      <td class="dim nowrap">${fmtAgo(a.time)}</td>
      <td><div style="display:flex;align-items:center;gap:6px;min-width:0"><span class="method ${esc(a.method)}">${esc(a.method)}</span><span class="path-cell">${esc(a.path)}</span></div></td>
      <td><span class="activity-status ${ok ? "" : "err"}">${esc(a.status || "—")}</span></td>
      <td class="dim nowrap" style="text-align:right">${a.duration ? (a.duration / 1e6).toFixed(0) + " ms" : "—"}</td>
    </tr>`;
  }

  function empty(title, text, actionLabel, action) {
    return `<div class="empty">
      <div class="e-icon">${icon("bucket")}</div>
      <b>${esc(title)}</b><p>${esc(text)}</p>
      ${actionLabel ? `<button class="btn btn-sm btn-soft" data-act="${esc(action)}">${icon("plus")}${esc(actionLabel)}</button>` : ""}
    </div>`;
  }

  // ------------------------------------------------------------- buckets
  async function renderBuckets() {
    page("");
    const data = await api("/api/buckets");
    state.buckets = data.items || [];
    const rows = state.buckets.map((b) => `
      <tr class="clickable" data-act="open-bucket" data-bucket="${esc(enc(b.name))}">
        <td><div class="name-cell"><span class="ico">${icon("bucket")}</span><div class="txt"><b>${esc(b.name)}</b><span title="${esc(b.rootPath || "s3://" + b.name)}">${b.rootPath ? "目录 · " + esc(b.rootPath) : "s3://" + esc(b.name)}</span></div></div></td>
        <td><button class="badge policy ${b.policy === "public-read" ? "warn" : ""}" data-act="toggle-policy" data-bucket="${esc(enc(b.name))}" data-policy="${esc(b.policy)}" title="点击切换访问策略">${b.policy === "public-read" ? "公共读" : "私有"}</button></td>
        <td>${fmtNum(b.objects)}</td>
        <td class="nowrap">${fmtBytes(b.size)}</td>
        <td class="dim nowrap hide-sm">${fmtDate(b.created)}</td>
        <td><div class="actions">
          <button class="icon-btn" data-act="download-bucket" data-bucket="${esc(enc(b.name))}" title="打包下载">${icon("archive")}</button>
          <button class="icon-btn" data-act="copy" data-copy="${esc("s3://" + b.name)}" title="复制路径">${icon("copy")}</button>
          <button class="icon-btn" data-act="delete-bucket" data-bucket="${esc(enc(b.name))}" title="删除">${icon("trash")}</button>
        </div></td>
      </tr>`).join("");

    page(`
      <div class="page-head">
        <div><h1>存储桶</h1><p>管理对象存储容器与访问策略。公共读桶允许匿名下载其中的对象。</p></div>
        <div class="head-actions"><button class="btn btn-primary" data-act="new-bucket">${icon("plus")}新建存储桶</button></div>
      </div>
      <div class="card">
        ${state.buckets.length ? `<div class="table-wrap"><table class="table">
          <thead><tr><th>名称</th><th>访问策略</th><th>对象数</th><th>占用空间</th><th class="hide-sm">创建时间</th><th></th></tr></thead>
          <tbody>${rows}</tbody></table></div>`
        : empty("还没有存储桶", "存储桶是对象的容器，名称需为 3-63 位小写字母、数字、点或连字符。", "新建存储桶", "new-bucket")}
      </div>
      <div class="banner info" style="margin-top:16px">
        <div class="b-icon">${icon("info")}</div>
        <div class="b-body"><b>路径风格寻址</b><p>本服务使用 path-style：<code class="mono">http://NAS:端口/存储桶/对象</code>。请在客户端开启 “force path style / 路径风格” 选项。</p></div>
      </div>`);
    setCrumb();
  }

  async function renderBucketDetail(rawName, params) {
    const name = decodeURIComponent(rawName);
    const prefix = params.get("prefix") || "";
    state.route = "buckets";
    updateNav();
    setCrumb(name);
    page("");
    const data = await api(`/api/buckets/${enc(name)}/objects?prefix=${enc(prefix)}&delimiter=/`);
    const bucketList = await api("/api/buckets");
    state.buckets = bucketList.items || [];
    const meta = state.buckets.find((b) => b.name === name) || { name, policy: "private", objects: 0, size: 0 };
    const qs = state.prefixFilter.toLowerCase();
    const objects = (data.items || []).filter((o) => !qs || o.key.toLowerCase().includes(qs));
    const prefixes = (data.prefixes || []).filter((p) => !qs || p.toLowerCase().includes(qs));
    const parts = prefix.split("/").filter(Boolean);
    const crumbs = [`<button data-act="crumb" data-prefix="">${esc(name)}</button>`];
    parts.forEach((part, i) => {
      const p = parts.slice(0, i + 1).join("/") + "/";
      crumbs.push(`<span class="sep">/</span><button data-act="crumb" data-prefix="${esc(enc(p))}">${esc(part)}</button>`);
    });
    crumbs.push(`<span class="sep">/</span><span class="current">${prefix ? "" : "根目录"}</span>`);

    page(`
      <div class="breadcrumb"><a class="btn btn-sm btn-ghost" href="#/buckets" style="margin-right:4px">${icon("back")}存储桶</a>${crumbs.join("")}</div>
      <div class="page-head">
        <div><h1 style="display:flex;align-items:center;gap:10px">${esc(name)} ${policyBadge(meta.policy)}</h1>
        <p>${fmtNum(meta.objects)} 个对象 · 已使用 ${fmtBytes(meta.size)} · ${prefix ? "当前前缀 " + esc(prefix) : "根目录"}${meta.rootPath ? ` · <span class="mono">${esc(meta.rootPath)}</span>` : ""}</p></div>
        <div class="head-actions">
          <button class="btn" data-act="toggle-policy" data-bucket="${esc(enc(name))}" data-policy="${esc(meta.policy)}">${icon("shield")}${meta.policy === "public-read" ? "设为私有" : "设为公共读"}</button>
          <button class="btn" data-act="download-bucket" data-bucket="${esc(enc(name))}" data-prefix="${esc(enc(prefix))}">${icon("archive")}打包下载</button>
          <button class="btn" data-act="bucket-root" data-bucket="${esc(enc(name))}" data-root="${esc(meta.rootPath || "")}" title="更换绑定的飞牛文件夹">${icon("folder")}存储位置</button>
          <button class="btn" data-act="refresh">${icon("refresh")}刷新</button>
          <button class="btn btn-primary" data-act="upload" data-bucket="${esc(enc(name))}">${icon("upload")}上传对象</button>
        </div>
      </div>
      <div class="card">
        <div class="filter-bar">
          <div class="search">${icon("search")}<input class="input" id="obj-filter" placeholder="筛选当前目录中的对象…" value="${esc(state.prefixFilter)}"></div>
          <span class="hint tiny">${prefix ? "支持前缀浏览；筛选仅作用于当前已加载列表" : "最多显示 500 项"}</span>
          <div class="grow"></div>
          <button class="btn btn-sm" data-act="delete-bucket" data-bucket="${esc(enc(name))}">${icon("trash")}删除存储桶</button>
        </div>
        <div class="table-wrap">
          <table class="table">
            <thead><tr><th>名称</th><th>大小</th><th class="hide-sm">修改时间</th><th>操作</th></tr></thead>
            <tbody>
              ${prefixes.map((p) => {
                const label = p.slice(prefix.length).replace(/\/$/, "");
                return `<tr class="clickable" data-act="crumb" data-prefix="${esc(enc(p))}">
                  <td><div class="name-cell"><span class="ico folder">${icon("folder")}</span><div class="txt"><b>${esc(label)}/</b></div></div></td>
                  <td class="dim">—</td><td class="dim hide-sm">—</td><td></td></tr>`;
              }).join("")}
              ${objects.map((o) => {
                const label = o.key.slice(prefix.length);
                return `<tr>
                  <td><div class="name-cell"><span class="ico">${icon("file")}</span><div class="txt"><b title="${esc(o.key)}">${esc(label)}</b><span>${esc(o.contentType || "")}</span></div></div></td>
                  <td class="nowrap">${fmtBytes(o.size)}</td>
                  <td class="dim hide-sm nowrap">${fmtDate(o.modified)}</td>
                  <td><div class="actions">
                    <button class="icon-btn" data-act="download-object" data-bucket="${esc(enc(name))}" data-key="${esc(enc(o.key))}" title="下载">${icon("download")}</button>
                    <button class="icon-btn" data-act="share-object" data-bucket="${esc(enc(name))}" data-key="${esc(enc(o.key))}" title="分享链接">${icon("share")}</button>
                    <button class="icon-btn" data-act="delete-object" data-bucket="${esc(enc(name))}" data-key="${esc(enc(o.key))}" title="删除">${icon("trash")}</button>
                  </div></td>
                </tr>`;
              }).join("")}
              ${(!prefixes.length && !objects.length) ? `<tr><td colspan="4"><div class="empty"><div class="e-icon">${icon("upload")}</div><b>${state.prefixFilter ? "没有匹配的对象" : "此目录为空"}</b><p>点击右上角“上传对象”，或将文件拖到本页面。</p></div></td></tr>` : ""}
            </tbody>
          </table>
        </div>
        ${data.truncated ? `<div class="card-foot tiny dim">列表已截断，仅显示前 ${data.items.length} 项。请使用前缀筛选继续浏览。</div>` : ""}
      </div>
      <div class="banner info" style="margin-top:16px">
        <div class="b-icon">${icon("link")}</div>
        <div class="b-body"><b>客户端路径</b><p>访问此存储桶：<code class="mono">s3://${esc(name)}/${esc(prefix)}</code> · Endpoint 见“连接配置”页面。</p></div>
      </div>`);

    const filter = document.getElementById("obj-filter");
    if (filter) {
      filter.addEventListener("input", (e) => {
        state.prefixFilter = e.target.value;
        clearTimeout(filter._t);
        filter._t = setTimeout(() => {
          renderBucketDetail(rawName, params).catch((err) => toast(err.message, "err"));
        }, 220);
      });
      filter.focus();
      filter.setSelectionRange(filter.value.length, filter.value.length);
    }
  }

  // ------------------------------------------------------------- keys
  async function renderKeys() {
    page("");
    const data = await api("/api/keys");
    state.keys = data.items || [];
    const rows = state.keys.map((k) => {
      const revealed = state.revealedSecrets.has(k.accessKey);
      return `<tr>
        <td><div class="name-cell"><span class="ico">${icon("key")}</span><div class="txt"><b>${esc(k.label || "未命名")}</b><span>创建于 ${fmtDate(k.created)}</span></div></div></td>
        <td><div class="row-inline"><code class="mono">${esc(k.accessKey)}</code><button class="icon-btn" data-act="copy" data-copy="${esc(k.accessKey)}" title="复制 Access Key">${icon("copy")}</button></div></td>
        <td><div class="row-inline"><code class="mono" style="min-width:120px">${revealed ? esc(k.secretKey) : "••••••••••••••••"}</code>
          <button class="icon-btn" data-act="reveal" data-access="${esc(k.accessKey)}" title="${revealed ? "隐藏" : "显示"} Secret">${icon(revealed ? "eyeoff" : "eye")}</button>
          <button class="icon-btn" data-act="copy" data-copy="${esc(k.secretKey)}" title="复制 Secret">${icon("copy")}</button></div></td>
        <td><button class="switch ${k.enabled ? "on" : ""}" data-act="toggle-key" data-access="${esc(k.accessKey)}" data-enabled="${k.enabled}" title="${k.enabled ? "停用" : "启用"}"></button></td>
        <td class="dim hide-sm nowrap">${k.lastUsed ? fmtAgo(k.lastUsed) : "从未使用"}</td>
        <td><div class="actions"><button class="icon-btn" data-act="delete-key" data-access="${esc(k.accessKey)}" title="删除">${icon("trash")}</button></div></td>
      </tr>`;
    }).join("");

    page(`
      <div class="page-head">
        <div><h1>访问密钥</h1><p>S3 客户端使用 Access Key / Secret Key 进行 SigV4 签名认证。</p></div>
        <div class="head-actions"><button class="btn btn-primary" data-act="new-key">${icon("plus")}新建密钥</button></div>
      </div>
      <div class="banner warn" style="margin-bottom:16px">
        <div class="b-icon">${icon("lock")}</div>
        <div class="b-body"><b>Secret Key 等同密码，请妥善保管</b><p>可在本页查看和复制 Secret Key。建议为不同客户端分别创建密钥，便于审计与单独停用。</p></div>
      </div>
      <div class="card">
        ${state.keys.length ? `<div class="table-wrap"><table class="table">
          <thead><tr><th>标签</th><th>Access Key</th><th>Secret Key</th><th>启用</th><th class="hide-sm">最近使用</th><th></th></tr></thead>
          <tbody>${rows}</tbody></table></div>` : empty("还没有访问密钥", "创建一对 Access Key 与 Secret Key，然后配置到 S3 客户端。", "新建密钥", "new-key")}
      </div>
      <div class="callout" style="margin-top:16px">S3 Signature Version 必须为 <b>v4</b>。Region 可填写 <code class="mono">us-east-1</code>（或保持服务端默认值），并使用路径风格寻址。</div>`);
    setCrumb();
  }

  // ------------------------------------------------------------- connect
  async function renderConnect() {
    page("");
    const [ov, keys] = await Promise.all([api("/api/overview"), api("/api/keys")]);
    state.overview = ov;
    state.keys = keys.items || [];
    const endpoint = ov.publicEndpoint || ov.endpoint;
    const enabled = state.keys.filter((k) => k.enabled);
    const k = enabled[0] || state.keys[0] || { accessKey: "YOUR_ACCESS_KEY", secretKey: "YOUR_SECRET_KEY" };
    const region = ov.region || "us-east-1";
    const snippets = {
      aws: `# AWS CLI v2
export AWS_ACCESS_KEY_ID='${k.accessKey}'
export AWS_SECRET_ACCESS_KEY='${k.secretKey}'
export AWS_DEFAULT_REGION='${region}'

aws --endpoint-url ${endpoint} s3 ls
aws --endpoint-url ${endpoint} s3 mb s3://mybucket
aws --endpoint-url ${endpoint} s3 cp ./photo.jpg s3://mybucket/photos/photo.jpg
aws --endpoint-url ${endpoint} s3 presign s3://mybucket/photos/photo.jpg --expires-in 3600`,
      rclone: `# rclone.conf
[fnos-oss]
type = s3
provider = Other
access_key_id = ${k.accessKey}
secret_access_key = ${k.secretKey}
endpoint = ${endpoint}
region = ${region}
force_path_style = true

# 命令示例
rclone lsd fnos-oss:
rclone copy ./data fnos-oss:mybucket/data
rclone mount fnos-oss:mybucket Z: --vfs-cache-mode writes`,
      boto3: `import boto3
from botocore.config import Config

s3 = boto3.client(
    "s3",
    endpoint_url="${endpoint}",
    aws_access_key_id="${k.accessKey}",
    aws_secret_access_key="${k.secretKey}",
    region_name="${region}",
    config=Config(s3={"addressing_style": "path"}),
)

s3.create_bucket(Bucket="mybucket")
s3.upload_file("photo.jpg", "mybucket", "photos/photo.jpg")
print(s3.list_objects_v2(Bucket="mybucket", Prefix="photos/"))`,
      other: `# 通用 S3 客户端填写方式
Endpoint        ${endpoint}
Access Key      ${k.accessKey}
Secret Key      ${k.secretKey}
Region          ${region}
Signature       AWS Signature V4
Addressing      Path style / 路径风格
TLS             局域网 http，公网请使用 https 反代`,
    };
    const tabs = [["aws", "AWS CLI"], ["rclone", "rclone"], ["boto3", "Python boto3"], ["other", "其他客户端"]];

    page(`
      <div class="page-head">
        <div><h1>连接配置</h1><p>将下面的参数填入任意兼容 S3 的客户端即可开始使用。</p></div>
        <div class="head-actions"><button class="btn" data-act="copy" data-copy="${esc(snippetText(snippets[state.connectTab]))}">${icon("copy")}复制当前示例</button></div>
      </div>
      ${!enabled.length ? `<div class="banner warn" style="margin-bottom:16px"><div class="b-icon">${icon("alert")}</div><div class="b-body"><b>没有启用的访问密钥</b><p>示例中的密钥为占位符。请先到“访问密钥”页面创建并启用一对密钥。</p><div class="b-actions"><a class="btn btn-sm" href="#/keys">前往访问密钥</a></div></div></div>` : ""}
      ${ov.publicEndpoint ? "" : `<div class="banner info" style="margin-bottom:16px"><div class="b-icon">${icon("network")}</div><div class="b-body"><b>当前显示局域网地址</b><p>未配置公网 / 反向代理地址，Endpoint 使用本机局域网 IP 与 S3 端口，仅支持 HTTP。需要公网访问时，请先用 Lucky / Nginx 反向代理并启用 HTTPS，再在“设置”中填写反代地址。</p><div class="b-actions"><a class="btn btn-sm" href="#/network">查看反代方案</a><a class="btn btn-sm btn-ghost" href="#/settings">去设置</a></div></div></div>`}
      <div class="stack">
        <div class="grid2-even">
          <div class="card"><div class="card-head"><h2>连接参数</h2><span class="badge primary">Path style</span></div><div class="card-body">
            ${copyRow("Endpoint", endpoint, "http")}
            ${copyRow("Region", region, "network")}
            ${copyRow("Access Key", k.accessKey, "key")}
            <div class="kv" style="margin-top:4px">
              <div class="row"><span class="k">${icon("shield")}签名版本</span><span class="v">AWS Signature V4</span></div>
              <div class="row"><span class="k">${icon("link")}寻址方式</span><span class="v">Path style（必须）</span></div>
              <div class="row"><span class="k">${icon("network")}传输加密</span><span class="v">${endpoint.startsWith("https") ? '<span class="badge ok">HTTPS</span>' : '<span class="badge warn">HTTP（仅建议局域网）</span>'}</span></div>
            </div>
          </div></div>
          <div class="card"><div class="card-head"><h2>填写提示</h2></div><div class="card-body">
            <div class="steps">
              <div class="step"><div><b>开启路径风格寻址</b><p>不同客户端的名称不同：Force path style、Path-style access、Use path style 等。</p></div></div>
              <div class="step"><div><b>Region 使用 us-east-1</b><p>本服务不校验区域，但多数客户端要求填写，建议保持默认。</p></div></div>
              <div class="step"><div><b>公网请使用 HTTPS</b><p>通过 Lucky / Nginx 反向代理到本机 TCP 端口，不要在路由器直接映射明文端口。</p></div></div>
              <div class="step"><div><b>先创建存储桶</b><p>S3 客户端通常不会自动建桶，请先在“存储桶”页面创建。</p></div></div>
            </div>
          </div></div>
        </div>
        <div class="card">
          <div class="card-head"><h2>客户端示例</h2><div class="tabs">${tabs.map(([id, label]) => `<button class="tab ${state.connectTab === id ? "active" : ""}" data-act="connect-tab" data-tab="${id}">${label}</button>`).join("")}</div></div>
          <div class="card-body"><div class="code-block"><button class="btn btn-sm btn-copy" data-act="copy" data-copy="${esc(snippetText(snippets[state.connectTab]))}">${icon("copy")}复制</button><pre>${esc(snippets[state.connectTab])}</pre></div></div>
        </div>
      </div>`);
    setCrumb();
  }

  function snippetText(s) { return String(s || ""); }

  function copyRow(label, value, ic) {
    return `<div class="kv"><div class="row"><span class="k">${icon(ic)}${esc(label)}</span><span class="v" style="flex:1;justify-content:flex-end">
      <code class="mono" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:100%">${esc(value)}</code>
      <button class="icon-btn" data-act="copy" data-copy="${esc(value)}" title="复制">${icon("copy")}</button></span></div></div>`;
  }

  // ------------------------------------------------------------- network
  async function renderNetwork() {
    page("");
    const [ov, settingsData] = await Promise.all([api("/api/overview"), api("/api/settings")]);
    state.overview = ov;
    const endpoint = settingsData.settings.publicEndpoint || ov.endpoint;
    page(`
      <div class="page-head">
        <div><h1>网络与安全</h1><p>对象存储的两种访问路径：客户端 S3 端口与管理端飞牛网关。</p></div>
      </div>
      <div class="banner warn">
        <div class="b-icon">${icon("shield")}</div>
        <div class="b-body"><b>TCP ${esc(ov.port)} 端口只应暴露在可信网络</b>
        <p>S3 使用 Access Key / Secret Key 签名，但端口本身不包含登录态。公网访问必须经过 HTTPS 反向代理，并建议叠加来源 IP 限制或额外的访问认证。飞牛应用中心不负责该 TCP 端口的公网防护。</p></div>
      </div>
      <div class="stack" style="margin-top:16px">
        <div class="card">
          <div class="card-head"><h2>访问路径</h2><span class="badge">双通道</span></div>
          <div class="card-body">
            <div class="grid2-even">
              <div class="callout ok" style="border-radius:10px">
                <b style="display:block;margin-bottom:8px">S3 客户端 → TCP ${esc(ov.port)}</b>
                <div class="mono tiny" style="line-height:1.9">客户端 / SDK / rclone<br>↓<br>${esc(endpoint)}<br>↓<br>本机 ${esc(ov.port)} 端口</div>
                <div class="tiny dim" style="margin-top:8px">仅提供 S3 API，不提供管理页面。</div>
              </div>
              <div class="callout" style="border-radius:10px">
                <b style="display:block;margin-bottom:8px">管理员 → 飞牛统一网关</b>
                <div class="mono tiny" style="line-height:1.9">飞牛桌面 / 浏览器<br>↓<br>${esc(ov.gatewayPrefix)}<br>↓<br>Unix Socket（target/app.sock）</div>
                <div class="tiny dim" style="margin-top:8px">由飞牛校验登录态，仅管理员可进入。</div>
              </div>
            </div>
          </div>
        </div>
        <div class="grid2">
          <div class="card">
            <div class="card-head"><h2>使用 Lucky 反向代理</h2><span class="badge primary">推荐</span></div>
            <div class="card-body">
              <div class="steps">
                <div class="step"><div><b>准备域名与 HTTPS 证书</b><p>在 Lucky 中申请或导入证书，使用一个独立子域名，例如 <code class="mono">s3.example.com</code>。</p></div></div>
                <div class="step"><div><b>新建 Web 服务 / 反向代理规则</b><p>前端地址填写域名并启用 HTTPS；后端地址填写 <code class="mono">http://127.0.0.1:${esc(ov.port)}</code>。</p></div></div>
                <div class="step"><div><b>保持路径与 Host 头</b><p>开启路径透传，保留客户端原始路径；请求体上限调大以支持大文件上传。</p></div></div>
                <div class="step"><div><b>只代理 S3 端口</b><p>不要把管理端网关或 NAS 管理页面暴露到公网。管理端请始终从飞牛桌面进入。</p></div></div>
              </div>
              <div class="callout warn" style="margin-top:14px">不要在路由器上把 ${esc(ov.port)} 端口以 TCP 方式直接映射到公网。HTTP 明文会泄露签名与数据，且没有额外访问控制。</div>
            </div>
          </div>
          <div class="card">
            <div class="card-head"><h2>上线前检查</h2></div>
            <div class="card-body">
              <div class="kv">
                ${checkRow("使用 HTTPS 访问", "防止凭据和对象内容在公网明文传输", true)}
                ${checkRow("限制来源 IP", "在 Lucky / 防火墙中只放行可信网络", true)}
                ${checkRow("使用独立密钥", "为每个客户端创建密钥，泄露后可单独停用", true)}
                ${checkRow("关闭匿名写入", "本服务不提供匿名 PUT；公共读桶仅允许匿名下载", true)}
                ${checkRow("定期轮换密钥", "在“访问密钥”页面删除旧密钥并创建新密钥", false)}
              </div>
            </div>
          </div>
        </div>
        <div class="card">
          <div class="card-head"><h2>端口修改</h2><span class="badge outline">应用中心</span></div>
          <div class="card-body">
            <div class="grid2-even">
              <div>
                <div class="kv">
                  <div class="row"><span class="k">${icon("server")}当前 S3 端口</span><span class="v"><b class="mono" style="font-size:15px">${esc(ov.port)}</b></span></div>
                  <div class="row"><span class="k">${icon("network")}监听地址</span><span class="v mono">0.0.0.0:${esc(ov.port)}</span></div>
                  <div class="row"><span class="k">${icon("shield")}管理端端口</span><span class="v">不占用 TCP 端口（使用飞牛网关）</span></div>
                </div>
              </div>
              <div>
                <div class="callout">
                  <b>安装时可自定义端口</b>：安装向导中填写 S3 TCP 端口。<br>
                  <b>安装后修改</b>：飞牛应用中心 → 轻量对象存储 → 应用设置 → 修改端口 → 保存。应用会自动重启并监听新端口。
                </div>
                <div class="tiny dim" style="margin-top:8px">修改端口后，请同步更新 S3 客户端、反向代理和防火墙规则。</div>
              </div>
            </div>
          </div>
        </div>
      </div>`);
    setCrumb();
  }

  function checkRow(title, text, done) {
    return `<div class="row"><span class="k">${icon(done ? "check" : "clock")}${esc(title)}</span><span class="v tiny dim" style="max-width:60%">${esc(text)}</span></div>`;
  }

  // ------------------------------------------------------------- settings
  async function renderSettings() {
    page("");
    const data = await api("/api/settings");
    state.settings = data.settings;
    const network = data.network || {};
    const endpoint = state.settings.publicEndpoint || network.endpoint || "";
    page(`
      <div class="page-head">
        <div><h1>设置</h1><p>管理分享链接域名、跨域策略与上传限制。</p></div>
        <div class="head-actions"><button class="btn btn-primary" data-act="save-settings">${icon("check")}保存设置</button></div>
      </div>
      <div class="stack">
        <div class="card">
          <div class="card-head"><h2>网络</h2><span class="badge">保存后生效</span></div>
          <div class="card-body">
            <div class="grid2-even">
              <div class="kv">
                <div class="row"><span class="k">S3 TCP 端口</span><span class="v"><b class="mono">${esc(network.port)}</b><a class="btn btn-sm btn-ghost" href="#/network">修改说明</a></span></div>
                <div class="row"><span class="k">区域 Region</span><span class="v mono">${esc(network.region)}</span></div>
                <div class="row"><span class="k">网关前缀</span><span class="v mono">${esc(network.gatewayPrefix)}</span></div>
              </div>
              <div class="field">
                <label for="public-endpoint">公网 / 反向代理地址</label>
                <input class="input" id="public-endpoint" placeholder="https://s3.example.com" value="${esc(state.settings.publicEndpoint || "")}">
                <div class="help">用于页面展示和生成分享链接。留空时显示本机局域网 IP 与 S3 端口（仅 HTTP）；通过 Lucky 反代后请填写 HTTPS 地址。</div>
                ${state.settings.publicEndpoint ? "" : `<div class="tiny dim" style="margin-top:6px">当前展示地址：<code class="mono">${esc(network.endpoint || "")}</code></div>`}
              </div>
            </div>
          </div>
        </div>
        <div class="grid2-even">
          <div class="card">
            <div class="card-head"><h2>跨域 CORS</h2><span class="badge">浏览器直传</span></div>
            <div class="card-body">
              <div class="field">
                <label for="cors-origins">允许的来源（每行一个）</label>
                <textarea class="textarea" id="cors-origins" placeholder="*">${esc((state.settings.corsOrigins || []).join("\n"))}</textarea>
                <div class="help">用于浏览器直传 / 直读对象。生产环境建议填写具体域名，不要使用 *。</div>
              </div>
            </div>
          </div>
          <div class="card">
            <div class="card-head"><h2>上传限制</h2></div>
            <div class="card-body">
              <div class="field">
                <label for="max-upload">单对象最大大小（MB）</label>
                <input class="input" id="max-upload" type="number" min="0" step="1" value="${state.settings.maxUploadSize ? Math.round(state.settings.maxUploadSize / (1024 * 1024)) : 0}">
                <div class="help">填 0 表示不限制。限制同时作用于 PUT 与分段上传的单个分段。</div>
              </div>
            </div>
          </div>
        </div>
        <div class="card">
          <div class="card-head"><h2>备份与恢复</h2><span class="badge">建议定期下载</span></div>
          <div class="card-body">
            <div class="grid2-even">
              <div>
                <div class="strong" style="margin-bottom:6px">完整备份</div>
                <p class="muted tiny" style="margin-top:0">包含元数据与所有存放在应用数据卷中的对象，适合整机迁移或灾备。绑定飞牛文件夹的桶不在其中，请单独备份对应目录。</p>
                <button class="btn" data-act="download-backup" data-scope="full">${icon("archive")}下载完整备份（.tar.gz）</button>
              </div>
              <div>
                <div class="strong" style="margin-bottom:6px">仅元数据</div>
                <p class="muted tiny" style="margin-top:0">只包含桶、对象索引、访问密钥与设置，体积很小，适合日常留档。恢复时停止应用、改名为 <code class="mono">meta.db</code>，覆盖 <code class="mono">${esc(network.dataDir || "数据目录")}/meta.db</code>。</p>
                <button class="btn" data-act="download-backup" data-scope="meta">${icon("database")}下载 meta.db</button>
              </div>
            </div>
            <div class="callout" style="margin-top:14px">备份在服务运行时流式生成，元数据来自一致性快照；对象较多时需要一段时间，期间读写不受影响。</div>
          </div>
        </div>
        <div class="card">
          <div class="card-head"><h2>界面</h2></div>
          <div class="card-body">
            <div class="field" style="max-width:300px">
              <label for="theme-mode">外观</label>
              <select class="input" id="theme-mode">
                <option value="auto"${themePreference() === "auto" ? " selected" : ""}>跟随飞牛桌面</option>
                <option value="light"${themePreference() === "light" ? " selected" : ""}>浅色</option>
                <option value="dark"${themePreference() === "dark" ? " selected" : ""}>深色</option>
              </select>
              <div class="help">默认跟随飞牛桌面的深色 / 浅色主题；手动选择后会保持该外观。</div>
            </div>
          </div>
        </div>
        <div class="card">
          <div class="card-head"><h2>关于</h2></div>
          <div class="card-body">
            <div class="grid3">
              <div><div class="tiny dim">版本</div><div class="strong">v${esc(state.session.version)}</div></div>
              <div><div class="tiny dim">数据位置</div><div class="mono">${esc(network.dataDir || "")}</div></div>
              <div><div class="tiny dim">管理入口</div><div class="mono">${esc(network.gatewayPrefix || "")}</div></div>
            </div>
            <div class="divider"></div>
            <div class="muted tiny">管理端通过飞牛统一网关访问，不额外占用 TCP 端口；S3 数据端口仅用于兼容 S3 协议的客户端。卸载应用前请备份应用数据卷。</div>
          </div>
        </div>
      </div>`);
    setCrumb();
  }

  // ------------------------------------------------------------- page helpers
  function page(html) {
    const el = pageEl();
    if (!el) return;
    el.innerHTML = html || `<div class="card"><div class="card-body"><div class="empty"><div class="e-icon">${icon("refresh")}</div><b>正在加载…</b></div></div></div>`;
  }

  // ------------------------------------------------------------- actions
  async function copyText(text) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
    }
    toast("已复制到剪贴板", "ok");
  }

  function toast(message, type = "") {
    const el = document.createElement("div");
    el.className = `toast ${type}`;
    el.innerHTML = `${icon(type === "err" ? "alert" : "check")}<span>${esc(message)}</span>`;
    toastRoot.appendChild(el);
    setTimeout(() => { el.style.opacity = "0"; el.style.transition = "opacity .2s"; setTimeout(() => el.remove(), 220); }, 3200);
  }

  function modal(html, opts = {}) {
    closeModal();
    const mask = document.createElement("div");
    mask.className = "modal-mask";
    mask.innerHTML = `<div class="modal ${opts.wide ? "wide" : ""} ${opts.className || ""}" role="dialog" aria-modal="true">${html}</div>`;
    modalRoot.appendChild(mask);
    const first = mask.querySelector("input, textarea, select, button");
    if (first) setTimeout(() => first.focus(), 30);
    return mask;
  }
  function closeModal() {
    modalRoot.querySelectorAll(".modal-mask").forEach((el) => el.remove());
  }

  // Built-in documentation: connection recipes, hardening guidance and
  // troubleshooting, so the console stays useful on an offline NAS.
  function helpModal() {
    const ov = state.overview || {};
    const port = ov.port || 9100;
    const endpoint = String(ov.publicEndpoint || ov.endpoint || `http://<NAS-IP>:${port}`).replace(/\/+$/, "");
    const region = ov.region || "us-east-1";
    const dataDir = ov.dataDir || "${TRIM_PKGVAR}/data";
    const code = (body) => `<div class="code-block"><button class="btn btn-sm btn-copy" data-act="copy" data-copy="${esc(body)}">${icon("copy")}复制</button><pre>${esc(body)}</pre></div>`;
    const docs = [
      {
        id: "start",
        title: "快速开始",
        body: `
          <h4>快速开始</h4>
          <p>单机部署的 S3 兼容对象存储：管理端挂在飞牛统一网关内，S3 API 使用独立 TCP 端口 <b>${port}</b>。</p>
          <ol class="docs-steps">
            <li><b>确认服务在运行。</b>左侧状态卡显示“运行中”，端口可在“设置 → 网络”中修改。</li>
            <li><b>准备访问密钥。</b>在“访问密钥”页面复制默认密钥，或为每个客户端单独新建。</li>
            <li><b>配置客户端。</b>Endpoint 填 <code class="mono">${esc(endpoint)}</code>，寻址方式必须为 path style，签名使用 AWS Signature V4。</li>
            <li><b>开始使用。</b>新建存储桶后即可上传、浏览和生成分享链接。</li>
          </ol>
          <p class="docs-note">管理端地址 <code class="mono">/app/fnos-oss</code> 只能通过飞牛桌面或统一网关进入；S3 端口不提供管理界面。</p>`,
      },
      {
        id: "clients",
        title: "连接客户端",
        body: `
          <h4>连接 S3 客户端</h4>
          <p>所有兼容 S3 的工具都使用同一组参数：</p>
          <div class="kv">
            <div class="row"><span class="k">Endpoint</span><span class="v mono">${esc(endpoint)}</span></div>
            <div class="row"><span class="k">Region</span><span class="v mono">${esc(region)}</span></div>
            <div class="row"><span class="k">寻址方式</span><span class="v">Path style（必须）</span></div>
            <div class="row"><span class="k">签名版本</span><span class="v">AWS Signature V4</span></div>
            <div class="row"><span class="k">传输加密</span><span class="v">${ov.publicEndpoint ? "由反向代理提供 HTTPS" : "HTTP，仅限可信局域网"}</span></div>
          </div>
          <h5>rclone</h5>
          ${code(`[fnos-oss]
type = s3
provider = Other
access_key_id = <Access Key>
secret_access_key = <Secret Key>
endpoint = ${endpoint}
region = ${region}
force_path_style = true`)}
          ${code(`rclone lsd fnos-oss:
rclone copy ./data fnos-oss:mybucket/data
rclone mount fnos-oss:mybucket /mnt/oss --vfs-cache-mode writes`)}
          <h5>AWS CLI</h5>
          ${code(`export AWS_ACCESS_KEY_ID='<Access Key>'
export AWS_SECRET_ACCESS_KEY='<Secret Key>'
export AWS_DEFAULT_REGION=${region}
aws --endpoint-url ${endpoint} s3 ls
aws --endpoint-url ${endpoint} s3 cp ./a.bin s3://mybucket/a.bin`)}
          <p>Windows PowerShell 用 <code class="mono">$env:AWS_ACCESS_KEY_ID = "&lt;Access Key&gt;"</code> 设置同名环境变量。</p>
          <h5>boto3</h5>
          ${code(`from botocore.config import Config
import boto3

s3 = boto3.client(
    "s3",
    endpoint_url="${endpoint}",
    aws_access_key_id="<Access Key>",
    aws_secret_access_key="<Secret Key>",
    region_name="${region}",
    config=Config(s3={"addressing_style": "path"}),
)
print(s3.list_buckets()["Buckets"])`)}`,
      },
      {
        id: "objects",
        title: "存储桶与分享",
        body: `
          <h4>存储桶、对象与分享</h4>
          <p>存储桶名称 3–63 位，只能包含小写字母、数字、点和连字符，创建后不可重命名。</p>
          <ul class="docs-list">
            <li><b>私有（private）：</b>默认策略，只有携带有效签名的请求可读写。</li>
            <li><b>公共读（public-read）：</b>匿名用户可以下载对象，但不能列出桶内容、上传或删除。</li>
            <li><b>分享链接：</b>在对象行点击“分享”生成预签名 URL，可选下载或上传并设置有效期。链接由当前启用的密钥签名，删除对象或停用密钥后立即失效。</li>
            <li><b>目录浏览：</b>以 <code class="mono">/</code> 作为分隔符模拟目录，支持 prefix + delimiter 前缀浏览。</li>
            <li><b>跨域（CORS）：</b>浏览器直传或直读需要在“网络与安全”配置允许来源；生产环境请填写具体域名，不要使用 <code class="mono">*</code>。</li>
          </ul>`,
      },
      {
        id: "folder",
        title: "绑定飞牛文件夹",
        body: `
          <h4>把存储桶绑定到飞牛文件夹</h4>
          <p>默认情况下对象保存在应用数据卷里，按哈希分片，文件名和对象键无关。新建存储桶时把“存储位置”改为“飞牛文件夹”，对象就会按对象键写成真实文件，可以直接在文件管理器、SMB 或其他应用里访问。</p>
          <ol class="docs-steps">
            <li>点击“新建存储桶”，填写桶名称。</li>
            <li>“存储位置”选择“飞牛文件夹”，点击“选择目录”。</li>
            <li>在系统选择器中选中目标目录并确认授权。此操作需要管理员身份，系统会把该目录的访问权限授予本应用用户。</li>
            <li>创建后，对象键 <code class="mono">2024/a.jpg</code> 即对应 <code class="mono">目录/2024/a.jpg</code>。</li>
          </ol>
          <ul class="docs-list">
            <li><b>以文件系统为准：</b>通过 SMB 或文件管理器放进去的文件，S3 立即可见，无需重建索引。</li>
            <li><b>一个目录只能绑定一个桶：</b>重复绑定会被拒绝，避免两个桶互相覆盖文件。</li>
            <li><b>键即路径：</b>不允许空段、<code class="mono">.</code>、<code class="mono">..</code> 段和以 <code class="mono">/</code> 结尾的键；同一路径不能既是文件又是目录，冲突时返回 <code class="mono">409 KeyConflict</code>。</li>
            <li><b>元数据：</b>Content-Type、ETag 与自定义头仍记在 <code class="mono">meta.db</code>；文件在外部被修改后，这些记录会按大小与修改时间自动失效，Content-Type 回退为按扩展名推断。</li>
            <li><b>删除桶不会删除目录：</b>桶目录归用户所有，删除桶只移除记录；目录非空时不允许删除桶。</li>
            <li><b>已有桶不受影响：</b>升级前创建的桶继续使用内部存储，两种模式可以并存。</li>
          </ul>
          <h4>目录选择失败怎么办</h4>
          <p>选择器依赖飞牛开放 API：应用包必须在 <code class="mono">manifest</code> 中声明 <code class="mono">micro_app=true</code>、<code class="mono">disable_authorization_path=false</code>，并在 <code class="mono">config/resource</code> 中声明 Scope <code class="mono">trim.file.sharedAccess</code>；系统需要 1.2.0401 及以上。</p>
          <ul class="docs-list">
            <li><b>提示“应用鉴权异常”（code 1003103）：</b>通常是安装的包没有开启授权目录。升级到 1.0.1 并在应用中心重新安装后重试。</li>
            <li><b>提示“仅管理员可进行此操作”：</b>换用飞牛管理员账号，或改为手动输入已授权的路径。</li>
            <li><b>提示 Scope 不足（code 1000002）：</b><code class="mono">config/resource</code> 没有生效，重新安装应用包。</li>
            <li><b>兜底方式：</b>在飞牛“应用设置 → 授权目录”里手动添加目录，然后在建桶弹窗中直接输入这个绝对路径。管理端会在选择失败时自动查询已授权目录数量，便于判断是 Scope 问题还是权限问题。</li>
          </ul>`,
      },
      {
        id: "uploads",
        title: "上传与大文件",
        body: `
          <h4>上传与分段上传</h4>
          <p>单个 PUT 与分段上传的每个分段都受“设置 → 上传限制”约束，填 0 表示不限制；该限制针对单次请求体，不改变总容量。</p>
          <ul class="docs-list">
            <li>大文件交给 rclone、AWS CLI 或 SDK 自动分段，避免单个过大的请求体。</li>
            <li>支持 <code class="mono">aws-chunked</code> 流式上传与分块验签。</li>
            <li>上传先写临时文件，成功后才进入对象目录，中断的请求不会留下半截对象。</li>
            <li>未完成的分段上传通过 <code class="mono">ListMultipartUploads</code> 查看，可用 <code class="mono">AbortMultipartUpload</code> 清理。</li>
          </ul>`,
      },
      {
        id: "security",
        title: "密钥与安全",
        body: `
          <h4>访问密钥与安全</h4>
          <ul class="docs-list">
            <li>按客户端或用途分别创建密钥，泄露时只需停用对应的一条。</li>
            <li>Secret Key 等同密码，不要写入前端代码、公开仓库或截图。</li>
            <li>停用密钥会立即拒绝所有使用该密钥的签名请求和分享链接。</li>
            <li>管理端依赖飞牛网关注入的登录态，仅管理员可进入；S3 TCP 端口不经过飞牛登录校验。</li>
            <li>建议定期轮换密钥，并在“访问密钥”页关注“最近使用”时间。</li>
          </ul>`,
      },
      {
        id: "public",
        title: "公网访问",
        body: `
          <h4>公网访问与反向代理</h4>
          <p>S3 端口 <b>${port}</b> 默认只适合可信局域网。需要公网访问时，请用 Lucky、frp 或 Nginx 反代到 <code class="mono">http://127.0.0.1:${port}</code>，并叠加 HTTPS、来源 IP 限制和访问控制；不要在路由器上直接映射该端口。</p>
          <h5>Nginx 示例</h5>
          ${code(`server {
    listen 443 ssl http2;
    server_name s3.example.com;

    ssl_certificate     /etc/ssl/s3.example.com.pem;
    ssl_certificate_key /etc/ssl/s3.example.com.key;

    client_max_body_size 0;
    proxy_request_buffering off;

    location / {
        proxy_pass http://127.0.0.1:${port};
        # $http_host 保留端口；$host 会丢掉非 443 端口，导致预签名链接失效
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_read_timeout 3600s;
    }
}`)}
          <h5>Lucky</h5>
          <p>新建 Web 服务：前端填 <code class="mono">https://s3.example.com</code> 与证书，后端填 <code class="mono">http://127.0.0.1:${port}</code>，调大请求体上限和超时。<b>Host 必须保留端口</b>，否则预签名链接会返回 <code class="mono">SignatureDoesNotMatch</code>。</p>
          <p>子规则操作模式切到「定制模式」，在「高级设置」里任选一种方式传递 Host：</p>
          <ul class="docs-list">
            <li><b>自定义配置</b>：填 <code class="mono">proxy_set_header Host $http_host;</code></li>
            <li><b>HTTP 后端请求头</b>：新增 Header，名称 <code class="mono">Host</code>，值 <code class="mono">$http_host</code></li>
          </ul>
          <p>同时确认「使用目标地址 Host 请求头」处于关闭状态（开启后 Host 会变成后端地址）。调试时可在自定义配置里临时加一行 <code class="mono">add_header X-Debug-Host $http_host always;</code>，用 <code class="mono">curl -sI</code> 检查响应头出现 <code class="mono">X-Debug-Host: 域名:端口</code> 后再删除。</p>
          <img class="docs-shot" src="assets/lucky-host.svg" alt="Lucky 高级设置：HTTP 后端请求头 Host 与自定义配置">
          <p class="docs-note">图中同时演示了「后端请求头」和「自定义配置」两种写法，实际配置任选一种即可。</p>
          <p>完成后在“设置 → 网络”填写 <code class="mono">https://s3.example.com</code>，页面展示与分享链接会自动切换。管理端始终从飞牛桌面进入，不要通过反向代理暴露。</p>`,
      },
      {
        id: "private",
        title: "私有桶与网页显示",
        body: `
          <h4>私有桶如何在网页中显示</h4>
          <p>桶策略改为 <code class="mono">private</code> 后，裸链接返回 <code class="mono">403 AccessDenied</code>，网页必须使用后端签发的预签名 URL。签名在业务后端本地计算，不会额外访问 NAS。</p>
          <h5>1. 管理端临时验证</h5>
          <p>存储桶 → 文件 → “分享链接”会生成 1 小时有效的签名 URL。该接口需要飞牛管理员登录态，不能直接暴露给网页访客。</p>
          <h5>2. 业务后端签发</h5>
          ${code(`// Node.js + @aws-sdk/client-s3 + @aws-sdk/s3-request-presigner
const s3 = new S3Client({
  region: "us-east-1",
  endpoint: "https://s3.example.com:7755",
  forcePathStyle: true,
  credentials: { accessKeyId: AK, secretAccessKey: SK },
});

const url = await getSignedUrl(
  s3,
  new GetObjectCommand({ Bucket: "demo-bucket", Key: key }),
  { expiresIn: 600 }
);`)}
          <h5>3. 前端使用</h5>
          <p>向业务后端请求 <code class="mono">url</code>，拿到后直接赋给 <code class="mono">img.src</code>；链接过期或加载失败时重新请求。数据库只保存对象键，不要保存签名 URL。</p>
          <div class="callout warn" style="margin:10px 0">
            <b>三个必查项：</b>endpoint 必须包含浏览器访问的端口；SDK 必须开启 path-style；Region 与服务端设置一致。任何一项不对都会返回 <code class="mono">SignatureDoesNotMatch</code>。
          </div>
          <p>有效期范围为 1 秒 ~ 7 天，页面展示建议 5~15 分钟；Secret Key 只能保存在服务端。Node / Python / Go / PHP 完整示例、错误码与验收命令见“下载开发者文档”。</p>`,
      },
      {
        id: "data",
        title: "数据与备份",
        body: `
          <h4>数据、备份与恢复</h4>
          <p>数据目录：<code class="mono">${esc(dataDir)}</code>，包含 <code class="mono">meta.db</code>（bbolt 元数据）、<code class="mono">objects/</code>（对象内容）、<code class="mono">uploads/</code> 与 <code class="mono">tmp/</code>。</p>
          <ol class="docs-steps">
            <li>在应用中心停止“轻量对象存储”。</li>
            <li>整体复制上述数据目录，保持目录结构。</li>
            <li>恢复时先停止应用，再用备份覆盖目录，然后启动。</li>
          </ol>
          <h4>备份整个应用</h4>
          <p>“设置 → 备份与恢复”可以直接下载备份：完整备份是 <code class="mono">.tar.gz</code>，包含一致性快照的 <code class="mono">meta.db</code> 与全部内部存储对象；只下载元数据则得到一个很小的 <code class="mono">.db</code> 文件，足够恢复桶、密钥与设置。备份在服务运行时流式生成，不需要停机。</p>
          <h4>导出单个存储桶</h4>
          <p>“存储桶”列表每行的压缩包图标会把整个桶打包成 zip 下载；进入桶后点“打包下载”只导出当前目录。导出由服务端实时压缩，不占用额外磁盘空间，但不会显示进度条，超大桶请改用 <code class="mono">rclone</code> 或 <code class="mono">aws s3 sync</code>。</p>
          <h4>迁移到另一台飞牛</h4>
          <p>桶、密钥与对象索引都保存在 <code class="mono">meta.db</code> 中，可以随数据目录整体搬走；密钥为明文存储，迁移后无需重新创建。</p>
          <div class="callout" style="margin:10px 0 14px">
            <b>数据库放哪里：</b>旧机的数据库是 <code class="mono">${esc(dataDir)}/meta.db</code>。新机的固定位置是
            <code class="mono">/volX/@appdata/fnos-oss/data/meta.db</code>，其中 <code class="mono">X</code> 是应用安装所在的存储空间编号（一般是 <code class="mono">1</code>）。
            该目录在文件管理器里默认隐藏，可在“设置 → 关于 → 数据位置”核对当前机器的实际路径。
          </div>
          <ol class="docs-steps">
            <li><b>旧机</b>：应用中心停止“轻量对象存储”，再到“设置 → 备份与恢复”下载完整备份（<code class="mono">.tar.gz</code>）或 <code class="mono">meta.db</code>。</li>
            <li><b>新机</b>：安装同版本并启动一次，让系统建好数据卷与专用应用用户，然后再次停止。这一步不能省，否则目标目录不存在、属主也不对。</li>
            <li><b>恢复完整备份</b>：上传到新机后解包到<b>数据目录本身</b>，不要多套一层目录：
              <br><code class="mono">sudo tar -C /volX/@appdata/fnos-oss/data -xzf fnos-oss-backup-日期.tar.gz</code>
              <br>包内顶层就是 <code class="mono">meta.db</code>、<code class="mono">objects/</code>、<code class="mono">uploads/</code>。</li>
            <li><b>只恢复 meta.db</b>：把下载到的 <code class="mono">fnos-oss-meta-日期.db</code> 改名为 <code class="mono">meta.db</code>，覆盖
              <code class="mono">/volX/@appdata/fnos-oss/data/meta.db</code>。它只恢复桶、密钥与对象索引，对象文件仍需另外拷贝
              <code class="mono">objects/</code>，否则会出现“列表有对象、下载却 404”。</li>
            <li><b>修正属主</b>：
              <br><code class="mono">sudo chown -R --reference=/volX/@appdata/fnos-oss/data /volX/@appdata/fnos-oss/data</code>
              <br>属主不对时，表现为桶里是空的、或写入报权限错误。</li>
            <li><b>启动应用</b>，然后进入绑定了飞牛文件夹的桶，点击“存储位置”改成新机器上的实际目录。这里只改指向，不移动文件。</li>
          </ol>
          <p class="docs-note">应用运行时持有 <code class="mono">meta.db</code> 的写锁，必须在停止状态下覆盖，否则可能无效甚至损坏数据库。卸载应用会停止服务并删除应用文件；对象数据位于应用数据卷，请在卸载前确认是否需要备份。</p>`,
      },
      {
        id: "limits",
        title: "能力与限制",
        body: `
          <h4>能力与限制</h4>
          <p><b>已支持：</b>SigV4 Header 签名与预签名 URL（GET / PUT）、aws-chunked 流式上传、桶与对象 CRUD、ListObjects V1 / V2、前缀与分隔符浏览、Range 下载、CopyObject、DeleteObjects、CORS、分段上传（Create / UploadPart / Complete / Abort / ListParts / ListUploads）。</p>
          <p><b>不支持：</b>对象版本控制、生命周期、对象锁、静态网站托管、Bucket Notification、S3 Select、多租户 IAM / ACL、跨节点复制与纠删码。</p>
          <p>桶策略只有 <code class="mono">private</code> 与 <code class="mono">public-read</code>；服务本身不终止 TLS。</p>`,
      },
      {
        id: "trouble",
        title: "故障排查",
        body: `
          <h4>故障排查</h4>
          <div class="docs-faq">
            <p><b>403 SignatureDoesNotMatch</b>核对 Access Key / Secret Key、Region 是否为 <code class="mono">${region}</code>、客户端是否使用 SigV4，并检查 NAS 与客户端时钟。</p>
            <p><b>301 / PermanentRedirect</b>客户端没有开启 path style，请改为路径风格寻址。</p>
            <p><b>连接超时或被拒绝</b>确认端口 <b>${port}</b> 未被占用、防火墙已放行；修改端口后同步更新客户端、反向代理与安全组。</p>
            <p><b>413 或大文件失败</b>调大反向代理的请求体上限与超时，并检查“设置 → 上传限制”。</p>
            <p><b>管理端提示从飞牛桌面进入</b>管理端只接受飞牛网关注入的登录态，不能直连 Unix Socket 或通过 S3 端口访问。</p>
            <p><b>分享链接失效</b>对象已删除或签名密钥已停用，重新生成即可。</p>
          </div>`,
      },
    ];
    modal(`
      <div class="modal-head">
        <div><h3>开发文档</h3><p>连接、安全、反向代理、私有桶与排错的完整说明${(state.session || {}).version ? ` · v${esc(state.session.version)}` : ""}</p></div>
      </div>
      <div class="modal-body docs-body">
        <div class="docs-layout">
          <aside class="docs-toc">
            <div class="docs-toc-title">目录</div>
            ${docs.map((d) => `<button data-act="doc-jump" data-doc="${d.id}">${esc(d.title)}</button>`).join("")}
            <a class="docs-download" href="assets/developer-guide.md" download="fnos-oss-developer-guide.md" title="下载 Markdown 格式的开发者文档">${icon("download")}<span>下载开发者文档</span></a>
          </aside>
          <div class="docs-main help-body">
            ${docs.map((d) => `<section id="doc-${d.id}">${d.body}</section>`).join("")}
          </div>
        </div>
      </div>
      <div class="modal-foot"><button class="btn btn-primary" data-modal-cancel>知道了</button></div>
    `, { className: "docs" });
  }

  function confirmDialog({ title, text, confirmText = "确认删除", danger = true }) {
    return new Promise((resolve) => {
      const mask = modal(`
        <div class="modal-head"><div><h3>${esc(title)}</h3><p>${esc(text)}</p></div></div>
        <div class="modal-foot">
          <button class="btn" data-modal-cancel>取消</button>
          <button class="btn ${danger ? "btn-danger" : "btn-primary"}" data-modal-ok>${esc(confirmText)}</button>
        </div>`);
      const done = (v) => { mask.remove(); resolve(v); };
      mask.querySelector("[data-modal-cancel]").onclick = () => done(false);
      mask.querySelector("[data-modal-ok]").onclick = () => done(true);
      mask.addEventListener("click", (e) => { if (e.target === mask) done(false); });
    });
  }

  function newBucketModal() {
    modal(`
      <form data-form="new-bucket">
        <div class="modal-head"><div><h3>新建存储桶</h3><p>存储桶名称全局唯一，创建后不可重命名。</p></div></div>
        <div class="modal-body">
          <div class="field">
            <label for="bucket-name">名称</label>
            <input class="input" id="bucket-name" name="name" placeholder="例如 photos" autocomplete="off" required>
            <div class="help">3-63 位小写字母、数字、点或连字符，必须以字母或数字开头和结尾。</div>
          </div>
          <div class="field">
            <label for="bucket-storage">存储位置</label>
            <select class="input" id="bucket-storage" name="storage">
              <option value="internal">应用数据卷（默认，推荐）</option>
              <option value="folder">飞牛文件夹（对象存为真实文件）</option>
            </select>
          </div>
          <div class="field" id="bucket-folder-field" hidden>
            <label for="bucket-root">目标目录</label>
            <div class="row-inline">
              <input class="input mono" id="bucket-root" name="rootPath" placeholder="点击右侧按钮选择并授权目录" readonly>
              <button type="button" class="btn btn-sm" data-act="pick-folder">${icon("folder")}选择目录</button>
            </div>
            <div class="help">通过飞牛文件选择器授权（需要管理员）。对象键 <code class="mono">a/b.jpg</code> 会保存为 <code class="mono">目录/a/b.jpg</code>，在文件管理器里可以直接访问；已授权的目录不能同时绑定多个存储桶。</div>
          </div>
        </div>
        <div class="modal-foot"><button type="button" class="btn" data-modal-cancel>取消</button><button class="btn btn-primary" type="submit">创建</button></div>
      </form>`);
    const storage = document.getElementById("bucket-storage");
    const folderField = document.getElementById("bucket-folder-field");
    storage.addEventListener("change", () => {
      folderField.hidden = storage.value !== "folder";
    });
  }

  // Re-pointing a bucket is the migration path: after copying the data volume to
  // another NAS the folder path usually differs, and no file has to be moved.
  function bucketRootModal(name, current) {
    modal(`
      <form data-form="bucket-root" data-bucket="${esc(enc(name))}">
        <div class="modal-head"><div><h3>存储位置</h3><p>${esc(name)}</p></div></div>
        <div class="modal-body">
          <div class="field">
            <label for="bucket-root">目标目录</label>
            <div class="row-inline">
              <input class="input mono" id="bucket-root" name="rootPath" value="${esc(current)}" placeholder="留空表示存放于应用数据卷">
              <button type="button" class="btn btn-sm" data-act="pick-folder">${icon("folder")}选择目录</button>
            </div>
            <div class="help">只改变指向，不移动文件。迁移到另一台飞牛后，把这里改成新机器上的实际目录即可继续读写原有文件；留空则改回应用数据卷内的存储。</div>
          </div>
        </div>
        <div class="modal-foot"><button type="button" class="btn" data-modal-cancel>取消</button><button class="btn btn-primary" type="submit">保存</button></div>
      </form>`);
  }

  function newKeyModal() {
    modal(`
      <form data-form="new-key">
        <div class="modal-head"><div><h3>新建访问密钥</h3><p>建议按客户端或用途命名，便于日后停用。</p></div></div>
        <div class="modal-body"><div class="field">
          <label for="key-label">标签</label>
          <input class="input" id="key-label" name="label" placeholder="例如 备份脚本 / 手机相册" autocomplete="off">
        </div></div>
        <div class="modal-foot"><button type="button" class="btn" data-modal-cancel>取消</button><button class="btn btn-primary" type="submit">创建并生成</button></div>
      </form>`);
  }

  // fnOS only exposes the folder picker through its micro-app JS SDK. The SDK
  // is vendored into assets so an offline NAS never needs a CDN.
  let trimSDKPromise = null;
  function loadTrimSDK() {
    if (!trimSDKPromise) {
      const url = new URL("assets/trim-sdk.js", document.baseURI).href;
      trimSDKPromise = import(url).then((mod) => new mod.TrimApp());
    }
    return trimSDKPromise;
  }

  // When the host cannot open the picker (standalone browser, non-admin, old
  // fnOS) fall back to manual entry so the form never becomes a dead end.
  function allowManualRoot(msg) {
    const input = document.getElementById("bucket-root");
    if (!input) return;
    input.readOnly = false;
    input.focus();
    toast(msg, "err");
  }

  // fnOS 返回的是业务码，不是 HTTP 状态码。把常见的几种翻译成“该改什么”，
  // 否则用户只会看到一句笼统的“应用鉴权异常”。
  function describeSDKError(res) {
    const msg = res && res.msg ? res.msg : "目录授权失败";
    const code = Number(res && res.code);
    const detail = {
      1: "仅管理员可进行此操作，请用管理员账号登录飞牛",
      1000001: "飞牛登录状态已失效，请重新登录",
      1000002: "应用缺少文件授权 Scope，请升级到 1.0.1 后重新安装",
      1003103: "应用权限校验失败，请升级到 1.0.1 并在应用中心重新安装",
      1000030: "当前路径不支持该操作，请换一个目录",
      1000701: "目录不存在，请重新选择",
    }[code];
    const suffix = Number.isFinite(code) ? `（code ${code}）` : "";
    return (detail ? `${msg}：${detail}` : msg) + suffix;
  }

  // Ask the backend what the host actually granted. An empty list after a
  // failed picker call almost always means the declared api-scope never took
  // effect, which points at an old package rather than a bad directory.
  async function sharedAccessHint() {
    try {
      const diag = await api("/api/shared-folders");
      if (!diag.available) return "";
      if (!diag.paths || !diag.paths.length) {
        return "；当前应用未获得任何目录授权，请确认已安装 1.0.1 并在应用设置中授权目录";
      }
      return `；当前已授权 ${diag.paths.length} 个目录`;
    } catch (e) {
      return "";
    }
  }

  async function pickBucketFolder() {
    const input = document.getElementById("bucket-root");
    if (!input) return;
    try {
      const sdk = await loadTrimSDK();
      if (sdk.ready) await sdk.ready();
      if (sdk.isStandaloneWeb) {
        allowManualRoot("目录选择需要在飞牛桌面中打开本应用，可直接输入已授权的绝对路径");
        return;
      }
      const res = await sdk.pickSharedFile({ title: "选择存储目录", okText: "授权并选择" });
      if (!res) return;
      if (res.code !== 0) {
        const extra = await sharedAccessHint();
        allowManualRoot(describeSDKError(res) + extra + "。也可直接输入已授权的绝对路径");
        return;
      }
      const picked = (res.data || [])[0];
      if (!picked) return;
      input.value = picked;
      toast("目录已授权", "ok");
    } catch (err) {
      const msg = err && err.message ? err.message : String(err);
      allowManualRoot("无法打开飞牛目录选择器：" + msg + "。可直接输入已授权的绝对路径。");
    }
  }

  async function uploadFiles(bucket, prefix, files) {
    if (!files || !files.length) return;
    const list = Array.from(files);
    toast(`正在上传 ${list.length} 个文件…`);
    let ok = 0;
    for (const file of list) {
      const fd = new FormData();
      fd.append("key", file.name);
      fd.append("file", file, file.name);
      try {
        await api(`/api/buckets/${enc(bucket)}/upload?prefix=${enc(prefix)}`, { method: "POST", body: fd });
        ok++;
      } catch (err) {
        toast(`${file.name}：${err.message}`, "err");
      }
    }
    if (ok) toast(`已上传 ${ok} 个文件`, "ok");
    route();
  }

  async function shareModal(bucket, key) {
    const mask = modal(`
      <div class="modal-head"><div><h3>分享链接</h3><p>生成带签名的临时链接，无需暴露密钥。</p></div></div>
      <div class="modal-body"><div class="empty" style="padding:24px 8px"><b>正在生成…</b></div></div>`);
    try {
      const data = await api(`/api/buckets/${enc(bucket)}/presign`, {
        method: "POST", body: { key, method: "GET", expires: 3600 },
      });
      mask.querySelector(".modal-body").innerHTML = `
        <div class="field">
          <label>下载链接（1 小时有效）</label>
          <div class="copy-field"><code title="${esc(data.url)}">${esc(data.url)}</code><button class="btn btn-sm" data-act="copy" data-copy="${esc(data.url)}">${icon("copy")}复制</button></div>
          <div class="help">链接由当前启用的 Access Key 签名。对象删除或密钥停用后链接立即失效。</div>
        </div>
        <div class="divider"></div>
        <div class="mono tiny" style="word-break:break-all;color:var(--text-3)">${esc(key)}</div>`;
      mask.querySelector(".modal-foot")?.remove();
    } catch (err) {
      mask.querySelector(".modal-body").innerHTML = `<div class="callout warn">${esc(err.message)}</div>`;
    }
  }

  // ------------------------------------------------------------- routing
  async function route() {
    const raw = location.hash.replace(/^#/, "") || "/overview";
    const [pathPart, query] = raw.split("?");
    const seg = pathPart.split("/").filter(Boolean);
    const r = seg[0] || "overview";
    state.route = r;
    updateNav();
    const params = q(query || "");
    try {
      if (r === "overview") await renderOverview();
      else if (r === "buckets" && seg[1]) await renderBucketDetail(seg[1], params);
      else if (r === "buckets") await renderBuckets();
      else if (r === "keys") await renderKeys();
      else if (r === "connect") await renderConnect();
      else if (r === "network") await renderNetwork();
      else if (r === "settings") await renderSettings();
      else { location.hash = "#/overview"; return; }
      setCrumb(seg[1] && r === "buckets" ? decodeURIComponent(seg[1]) : undefined);
      updateSidePort();
    } catch (err) {
      if (err.status === 403) {
        renderGate("需要飞牛管理员权限", err.message, "admin_required");
        return;
      }
      page(`<div class="banner warn"><div class="b-icon">${icon("alert")}</div><div class="b-body"><b>加载失败</b><p>${esc(err.message)}</p><div class="b-actions"><button class="btn btn-sm" data-act="reload">重试</button></div></div></div>`);
    }
  }

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") closeNav();
  });

  // ------------------------------------------------------------- events
  document.addEventListener("click", async (e) => {
    if (e.target.closest("[data-nav]")) closeNav();
    const el = e.target.closest("[data-act]");
    if (!el) {
      if (e.target.classList.contains("modal-mask")) closeModal();
      return;
    }
    const act = el.dataset.act;
    try {
      switch (act) {
        case "nav-open": setNavOpen(true); break;
        case "nav-close": closeNav(); break;
        case "doc-jump": {
          const target = document.getElementById(`doc-${el.dataset.doc}`);
          if (target) target.scrollIntoView({ behavior: "smooth", block: "start" });
          break;
        }
        case "refresh": await route(); break;
        case "reload": location.reload(); break;
        case "theme": {
          const next = resolvedTheme() === "dark" ? "light" : "dark";
          localStorage.setItem(THEME_KEY, next);
          applyTheme();
          toast(next === "dark" ? "已切换为深色外观" : "已切换为浅色外观");
          break;
        }
        case "help": helpModal(); break;
        case "copy": await copyText(el.dataset.copy || ""); break;
        case "dismiss-banner": state.bannerDismissed = true; sessionStorage.setItem("fnos-oss-banner", "1"); await route(); break;
        case "new-bucket": newBucketModal(); break;
        case "bucket-root": bucketRootModal(decodeURIComponent(el.dataset.bucket), el.dataset.root || ""); break;
        case "download-backup": {
          const scope = el.dataset.scope || "full";
          toast(scope === "meta" ? "正在生成元数据快照…" : "正在生成完整备份…");
          window.location.href = apiUrl(`/api/backup?scope=${enc(scope)}`);
          break;
        }
        case "pick-folder": await pickBucketFolder(); break;
        case "new-key": newKeyModal(); break;
        case "open-bucket": location.hash = `#/buckets/${el.dataset.bucket}`; break;
        case "crumb": {
          const prefix = decodeURIComponent(el.dataset.prefix || "");
          const seg = location.hash.replace(/^#\/buckets\//, "").split("?")[0];
          location.hash = `#/buckets/${seg}${prefix ? "?prefix=" + enc(prefix) : ""}`;
          break;
        }
        case "toggle-policy": {
          const b = decodeURIComponent(el.dataset.bucket);
          const next = el.dataset.policy === "public-read" ? "private" : "public-read";
          await api(`/api/buckets/${enc(b)}/policy`, { method: "PATCH", body: { policy: next } });
          toast(next === "public-read" ? "已设为公共读：匿名用户可下载对象" : "已设为私有", "ok");
          await route();
          break;
        }
        case "delete-bucket": {
          const b = decodeURIComponent(el.dataset.bucket);
          if (!await confirmDialog({ title: `删除存储桶 ${b}？`, text: "只有空存储桶可以删除。该操作不可撤销。" })) break;
          await api(`/api/buckets/${enc(b)}`, { method: "DELETE" });
          toast("存储桶已删除", "ok");
          location.hash = "#/buckets";
          if (location.hash === "#/buckets") await route();
          break;
        }
        case "upload": {
          const bucket = decodeURIComponent(el.dataset.bucket);
          const hash = location.hash;
          const pm = /[?&]prefix=([^&]*)/.exec(hash);
          const prefix = pm ? decodeURIComponent(pm[1]) : "";
          const input = document.createElement("input");
          input.type = "file";
          input.multiple = true;
          input.onchange = () => uploadFiles(bucket, prefix, input.files);
          input.click();
          break;
        }
        case "download-object": {
          const b = decodeURIComponent(el.dataset.bucket);
          const k = decodeURIComponent(el.dataset.key);
          window.location.href = apiUrl(`/api/buckets/${enc(b)}/object?key=${enc(k)}&download=1`);
          break;
        }
        case "download-bucket": {
          const b = decodeURIComponent(el.dataset.bucket);
          const prefix = el.dataset.prefix ? decodeURIComponent(el.dataset.prefix) : "";
          toast(prefix ? "正在打包当前目录…" : "正在打包整个存储桶…");
          window.location.href = apiUrl(`/api/buckets/${enc(b)}/archive${prefix ? `?prefix=${enc(prefix)}` : ""}`);
          break;
        }
        case "delete-object": {
          const b = decodeURIComponent(el.dataset.bucket);
          const k = decodeURIComponent(el.dataset.key);
          if (!await confirmDialog({ title: "删除对象？", text: k })) break;
          await api(`/api/buckets/${enc(b)}/object?key=${enc(k)}`, { method: "DELETE" });
          toast("对象已删除", "ok");
          await route();
          break;
        }
        case "share-object": {
          const b = decodeURIComponent(el.dataset.bucket);
          const k = decodeURIComponent(el.dataset.key);
          await shareModal(b, k);
          break;
        }
        case "reveal": {
          const a = el.dataset.access;
          if (state.revealedSecrets.has(a)) state.revealedSecrets.delete(a); else state.revealedSecrets.add(a);
          await renderKeys();
          break;
        }
        case "toggle-key": {
          const access = el.dataset.access;
          const enabled = el.dataset.enabled === "true";
          await api(`/api/keys/${enc(access)}`, { method: "PATCH", body: { enabled: !enabled } });
          toast(!enabled ? "密钥已启用" : "密钥已停用", "ok");
          await renderKeys();
          break;
        }
        case "delete-key": {
          const access = el.dataset.access;
          if (!await confirmDialog({ title: "删除访问密钥？", text: "使用该密钥的客户端将立即无法通过认证。" })) break;
          await api(`/api/keys/${enc(access)}`, { method: "DELETE" });
          toast("密钥已删除", "ok");
          await renderKeys();
          break;
        }
        case "connect-tab": state.connectTab = el.dataset.tab; await renderConnect(); break;
        case "save-settings": await saveSettings(); break;
        case "modal-close": closeModal(); break;
        default: break;
      }
    } catch (err) {
      toast(err.message || "操作失败", "err");
    }
  });

  document.addEventListener("click", (e) => {
    if (e.target.closest("[data-modal-cancel]")) closeModal();
  });

  document.addEventListener("change", (e) => {
    if (e.target && e.target.id === "theme-mode") {
      localStorage.setItem(THEME_KEY, e.target.value);
      applyTheme();
      toast("外观已更新");
    }
  });

  document.addEventListener("submit", async (e) => {
    const form = e.target.closest("form[data-form]");
    if (!form) return;
    e.preventDefault();
    const kind = form.dataset.form;
    const fd = new FormData(form);
    try {
      if (kind === "new-bucket") {
        const name = String(fd.get("name") || "").trim();
        if (!name) return;
        const storage = String(fd.get("storage") || "internal");
        const rootPath = storage === "folder" ? String(fd.get("rootPath") || "").trim() : "";
        if (storage === "folder" && !rootPath) {
          toast("请先选择并授权一个目录", "err");
          return;
        }
        await api("/api/buckets", { method: "POST", body: { name, rootPath } });
        closeModal();
        toast(rootPath ? `存储桶 ${name} 已绑定到 ${rootPath}` : `存储桶 ${name} 已创建`, "ok");
        await route();
      } else if (kind === "bucket-root") {
        const bucket = decodeURIComponent(form.dataset.bucket);
        const rootPath = String(fd.get("rootPath") || "").trim();
        const data = await api(`/api/buckets/${enc(bucket)}`, { method: "PATCH", body: { rootPath } });
        closeModal();
        toast(data.bucket && data.bucket.rootPath ? `已指向 ${data.bucket.rootPath}` : "已改回应用数据卷", "ok");
        await route();
      } else if (kind === "new-key") {
        const data = await api("/api/keys", { method: "POST", body: { label: String(fd.get("label") || "") } });
        closeModal();
        state.revealedSecrets.add(data.key.accessKey);
        toast("密钥已创建，请复制并保存", "ok");
        await route();
      } else if (kind === "settings") {
        await saveSettings();
      }
    } catch (err) {
      toast(err.message || "操作失败", "err");
    }
  });

  async function saveSettings() {
    const endpoint = document.getElementById("public-endpoint");
    const cors = document.getElementById("cors-origins");
    const max = document.getElementById("max-upload");
    if (!endpoint || !cors || !max) return;
    const origins = cors.value.split(/\n+/).map((s) => s.trim()).filter(Boolean);
    await api("/api/settings", {
      method: "PATCH",
      body: {
        publicEndpoint: endpoint.value.trim(),
        corsOrigins: origins,
        maxUploadSize: Math.max(0, Number(max.value) || 0) * 1024 * 1024,
      },
    });
    toast("设置已保存", "ok");
    state.settings = null;
    await route();
  }

  // Drag & drop upload on bucket pages.
  document.addEventListener("dragover", (e) => {
    if (state.route !== "buckets" || !location.hash.includes("/buckets/")) return;
    e.preventDefault();
  });
  document.addEventListener("drop", (e) => {
    if (state.route !== "buckets" || !location.hash.includes("/buckets/")) return;
    e.preventDefault();
    const seg = location.hash.replace(/^#\/buckets\//, "").split("?")[0];
    const pm = /[?&]prefix=([^&]*)/.exec(location.hash);
    uploadFiles(decodeURIComponent(seg), pm ? decodeURIComponent(pm[1]) : "", e.dataTransfer.files);
  });

  window.addEventListener("hashchange", () => {
    state.prefixFilter = "";
    route();
  });

  // ------------------------------------------------------------- boot
  async function bootApp() {
    applyTheme();
    watchTheme();
    initHostTheme();
    try {
      const [session, overview] = await Promise.all([
        api("/api/session"),
        api("/api/overview").catch(() => null),
      ]);
      state.session = session;
      state.overview = overview;
      if (!session.gateway) {
        renderGate("请从飞牛桌面打开", "管理端仅允许通过飞牛统一网关访问。请回到飞牛 fnOS 桌面，点击“轻量对象存储”图标进入。", "gateway_required");
        return;
      }
      if (!session.isAdmin) {
        renderGate("需要飞牛管理员权限", "当前账号不是管理员，无法管理对象存储。请使用管理员账号登录飞牛后重试。", "admin_required");
        return;
      }
      renderShell();
      if (!location.hash) location.hash = "#/overview";
      await route();
    } catch (err) {
      renderGate("无法连接管理端", err.message || "请确认应用正在运行。", "connection_failed");
    }
  }

  bootApp();
})();
