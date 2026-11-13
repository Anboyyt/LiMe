(() => {
  'use strict';

  const $ = (sel, root = document) => root.querySelector(sel);
  const view = $('#view');

  const state = {
    token: localStorage.getItem('lime.app.token') || '',
    user: JSON.parse(localStorage.getItem('lime.app.user') || 'null'),
    views: [],
    list: null,
  };

  const LOCAL_PLAYERS = [
    ['potplayer', 'PotPlayer'],
    ['vlc', 'VLC'],
    ['mpv', 'MPV'],
    ['iina', 'IINA（macOS）'],
    ['infuse', 'Infuse'],
  ];

  const TYPE = { Movie: '电影', Series: '剧集', Season: '季', Episode: '单集' };

  // 产品版本（/health 公开接口，不依赖登录）
  fetch('/health').then((r) => r.json()).then((d) => {
    const el = $('#brandVer');
    if (el && d && d.productVersion) el.textContent = ' ' + d.productVersion;
  }).catch(() => {});

  // ---------- 主题 ----------
  function applyTheme(theme) {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('lime.theme', theme);
    const btn = $('#themeBtn');
    if (btn) btn.textContent = theme === 'light' ? '☾' : '☀';
  }

  function initTheme() {
    const saved = localStorage.getItem('lime.theme');
    const light = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches;
    applyTheme(saved || (light ? 'light' : 'dark'));
  }
  initTheme();

  // ---------- 工具 ----------
  const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));

  function fmtTicks(t) {
    if (!t) return '';
    const s = Math.floor(t / 10000000);
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    return h ? `${h}:${String(m).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
             : `${m}:${String(s % 60).padStart(2, '0')}`;
  }

  let toastTimer = 0;
  function toast(msg, isErr = false) {
    const el = $('#toast');
    el.textContent = msg;
    el.className = 'toast' + (isErr ? ' err' : '');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => el.classList.add('hidden'), 2800);
  }

  // ---------- API ----------
  let pending = 0;

  function setPending(delta) {
    pending = Math.max(0, pending + delta);
    const bar = $('#loadingBar');
    if (bar) bar.classList.toggle('active', pending > 0);
  }

  async function api(path, opts = {}) {
    const headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) };
    if (state.token) headers['X-Emby-Token'] = state.token;
    setPending(1);
    try {
      const res = await fetch(path, { ...opts, headers });
      if (res.status === 401) {
        logout();
        throw new Error('登录已失效');
      }
      if (!res.ok) {
        const text = await res.text().catch(() => '');
        throw new Error(text || `HTTP ${res.status}`);
      }
      if (res.status === 204) return null;
      const ct = res.headers.get('content-type') || '';
      return ct.includes('json') ? res.json() : res.text();
    } finally {
      setPending(-1);
    }
  }

  // ---------- 登录 ----------
  function showLogin() {
    $('#app').classList.add('hidden');
    $('#login').classList.remove('hidden');
    $('#luser').focus();
  }

  function logout() {
    const token = state.token;
    if (token) {
      fetch('/Sessions/Logout', { method: 'POST', headers: { 'X-Emby-Token': token } }).catch(() => {});
    }
    state.token = '';
    state.user = null;
    localStorage.removeItem('lime.app.token');
    localStorage.removeItem('lime.app.user');
    showLogin();
  }

  async function doLogin(ev) {
    ev.preventDefault();
    const err = $('#loginErr');
    err.classList.add('hidden');
    const username = $('#luser').value.trim();
    const pw = $('#lpass').value;
    try {
      const res = await fetch('/Users/AuthenticateByName', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': 'MediaBrowser Client="LiMe Web", Device="Browser", DeviceId="lime-app", Version="1.0"',
        },
        body: JSON.stringify({ Username: username, Pw: pw }),
      });
      if (!res.ok) throw new Error(res.status === 401 ? '用户名或密码错误' : `登录失败（${res.status}）`);
      const data = await res.json();
      state.token = data.AccessToken;
      state.user = data.User;
      localStorage.setItem('lime.app.token', state.token);
      localStorage.setItem('lime.app.user', JSON.stringify(state.user));
      $('#lpass').value = '';
      await enterApp();
    } catch (e) {
      err.textContent = e.message;
      err.classList.remove('hidden');
    }
  }

  async function enterApp() {
    $('#login').classList.add('hidden');
    $('#app').classList.remove('hidden');
    state.user = await api('/Users/Me');
    localStorage.setItem('lime.app.user', JSON.stringify(state.user));
    $('#userName').textContent = state.user.Name;
    await loadViews();
    if (!location.hash) location.hash = '#/';
    else await route();
  }

  async function loadViews() {
    try {
      const data = await api(`/Users/${state.user.Id}/Views`);
      state.views = data.Items || [];
    } catch {
      state.views = [];
    }
    renderNav();
  }

  function renderNav() {
    const hash = location.hash || '#/';
    const links = [`<a href="#/" class="${hash === '#/' ? 'active' : ''}">首页</a>`];
    for (const v of state.views) {
      const h = `#/library/${encodeURIComponent(v.Id)}`;
      links.push(`<a href="${h}" class="${hash === h ? 'active' : ''}">${esc(v.Name)}</a>`);
    }
    links.push(`<a href="#/favorites" class="${hash === '#/favorites' ? 'active' : ''}">收藏</a>`);
    $('#nav').innerHTML = links.join('');
  }

  // ---------- 图片 ----------
  function imageUrl(it, kind = 'Primary') {
    let tag = '';
    if (kind === 'Primary') tag = it.ImageTags && it.ImageTags.Primary;
    else if (kind === 'Logo') tag = it.ImageTags && it.ImageTags.Logo;
    else tag = (it.BackdropImageTags || [])[0];
    if (!tag) return '';
    // logo 是透明 PNG，服务端不做缩放（缩放会转 JPEG 丢 alpha）。
    if (kind === 'Logo') {
      return `/Items/${encodeURIComponent(it.Id)}/Images/Logo?tag=${encodeURIComponent(tag)}`;
    }
    // 让服务端按用途缩放，手机不必下载原图。
    const w = kind === 'Primary' ? 600 : 1600;
    return `/Items/${encodeURIComponent(it.Id)}/Images/${kind}?tag=${encodeURIComponent(tag)}&maxWidth=${w}`;
  }

  function posterHTML(it) {
    const url = imageUrl(it);
    const playable = it.Type === 'Movie' || it.Type === 'Episode';
    const hint = playable ? '<div class="play-hint"><span>▶</span></div>' : '';
    if (url) {
      return `<div class="poster"><img class="poster-img" loading="lazy" src="${esc(url)}" alt=""
        onload="this.classList.add('loaded')">${hint}</div>`;
    }
    return `<div class="poster"><div class="placeholder">${esc((it.Name || '?').slice(0, 1))}</div>${hint}</div>`;
  }

  function card(it) {
    const ud = it.UserData || {};
    const pct = ud.PlayedPercentage || 0;
    const meta = cardMeta(it);
    return `<a class="card" href="#/item/${encodeURIComponent(it.Id)}">
      ${posterHTML(it)}
      <div class="card-title">${esc(it.Name)}</div>
      <div class="card-meta">${meta || ''}</div>
      <div class="progress${pct ? '' : ' no-progress'}"><i style="width:${pct}%"></i></div>
    </a>`;
  }

  // 卡片第二行：年份单独一行；剧集补集号、未看集数和已看状态。
  function cardMeta(it) {
    const bits = [];
    if (it.ProductionYear) bits.push(`<span class="year">${it.ProductionYear}</span>`);
    const ep = epLabel(it);
    if (ep) {
      bits.push(`<span>${ep}</span>`);
    } else if (it.Type === 'Series' && it.UserData && it.UserData.UnplayedItemCount > 0) {
      bits.push(`<span>${it.UserData.UnplayedItemCount} 集未看</span>`);
    }
    if (it.UserData && it.UserData.Played) bits.push('<span class="seen">已看</span>');
    return bits.join('<span class="dot">·</span>');
  }

  function epLabel(it) {
    if (it.Type !== 'Episode' || it.ParentIndexNumber == null) return '';
    return 'S' + String(it.ParentIndexNumber).padStart(2, '0') +
      'E' + String(it.IndexNumber || 0).padStart(2, '0');
  }

  // 首页精选：同一批条目里每天固定选一条，避免每次刷新都换。
  function dailyPick(items) {
    if (!items.length) return null;
    const d = new Date();
    const seed = `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;
    let hash = 0;
    for (let i = 0; i < seed.length; i++) hash = (hash * 31 + seed.charCodeAt(i)) | 0;
    return items[Math.abs(hash) % items.length];
  }

  // ---------- 路由 ----------
  let routeSeq = 0;
  let routeTimer = 0;

  function skeletonHTML() {
    const card = '<div class="skeleton-card"><div class="skeleton-poster"></div><div class="skeleton-line"></div></div>';
    return `<div class="page"><div class="skeleton-rail">${card.repeat(6)}</div>
      <div class="skeleton-rail">${card.repeat(6)}</div></div>`;
  }

  async function route() {
    const seq = ++routeSeq;
    renderNav();

    // 慢请求超过 180ms 才显示骨架，避免快速切换时闪一下。
    clearTimeout(routeTimer);
    routeTimer = setTimeout(() => {
      if (seq === routeSeq) view.innerHTML = skeletonHTML();
    }, 180);

    const raw = (location.hash || '#/').replace(/^#\/?/, '');
    const [name, ...rest] = raw.split('/');
    const arg = rest.join('/');
    try {
      switch (name) {
        case 'library': await renderLibrary(decodeURIComponent(arg), undefined, seq); break;
        case 'item': await renderItem(decodeURIComponent(arg), seq); break;
        case 'search': await renderSearch(decodeURIComponent(arg), seq); break;
        case 'genre': await renderBrowse('genre', decodeURIComponent(arg), seq); break;
        case 'person': await renderBrowse('person', decodeURIComponent(arg), seq); break;
        case 'favorites': await renderFavorites(seq); break;
        default: await renderHome(seq);
      }
    } catch (e) {
      if (seq !== routeSeq) return;
      view.innerHTML = `<div class="empty">加载失败：${esc(e.message)}<br><br>
        <button class="btn" id="retryBtn">重试</button></div>`;
      const btn = $('#retryBtn');
      if (btn) btn.onclick = () => route();
    } finally {
      clearTimeout(routeTimer);
    }
  }

  // ---------- 首页 ----------
  async function renderHome(seq) {
    const uid = state.user.Id;
    const [hero, resume, latest] = await Promise.all([
      api('/Items?Recursive=true&IncludeItemTypes=Movie,Series&SortBy=DateCreated&SortOrder=Descending&Limit=8'),
      api(`/Users/${uid}/Items/Resume?Limit=12`),
      api('/Items?Recursive=true&IncludeItemTypes=Movie,Series&SortBy=DateCreated&SortOrder=Descending&Limit=12'),
    ]);
    if (seq !== undefined && seq !== routeSeq) return;
    if (!state.views.length) await loadViews();
    if (seq !== undefined && seq !== routeSeq) return;

    const heroItems = (hero.Items || []).filter((i) => i.BackdropImageTags?.length || i.ImageTags?.Primary);
    const h = dailyPick(heroItems.length ? heroItems : (hero.Items || []));

    let html = '';
    if (h) {
      const bg = imageUrl(h, 'Backdrop') || imageUrl(h);
      const meta = [
        TYPE[h.Type] || h.Type,
        h.CommunityRating ? `★ ${h.CommunityRating.toFixed(1)}` : '',
        h.OfficialRating,
      ].filter(Boolean).map((x) => `<span class="badge">${esc(x)}</span>`).join('');
      const canPlay = h.Type === 'Movie' || h.Type === 'Episode';
      html += `<section class="hero" ${bg ? `style="background-image:url('${esc(bg)}')"` : ''}>
        <div class="hero-inner">
          <span class="hero-eyebrow">LiMe 精选</span>
          <h1 class="hero-title">${esc(h.Name)}</h1>
          ${h.ProductionYear ? `<div class="title-year">${h.ProductionYear}</div>` : ''}
          <div class="hero-meta">${meta}</div>
          ${h.Overview ? `<p class="hero-overview">${esc(h.Overview)}</p>` : ''}
          <div class="hero-actions">
            ${canPlay ? `<button class="btn primary" data-hero-play="${esc(h.Id)}">▶ 播放</button>` : ''}
            <a class="btn ${canPlay ? 'ghost' : 'primary'}" href="#/item/${encodeURIComponent(h.Id)}">查看详情</a>
          </div>
        </div>
      </section>`;
    }

    const resumeItems = resume.Items || [];
    if (resumeItems.length) html += rail('继续观看', resumeItems);
    const latestItems = latest.Items || [];
    if (latestItems.length) html += rail('最近添加', latestItems);

    if (!html) html = '<div class="empty">媒体库还是空的</div>';
    if (seq !== undefined && seq !== routeSeq) return;
    view.innerHTML = html;
    view.querySelectorAll('[data-hero-play]').forEach((b) => {
      b.onclick = () => playById(b.dataset.heroPlay);
    });
    window.scrollTo({ top: 0 });
  }

  function rail(title, items) {
    return `<section class="rail">
      <div class="rail-head"><h2>${esc(title)}</h2><span class="rail-count">${items.length}</span></div>
      <div class="rail-scroll">${items.map(card).join('')}</div>
    </section>`;
  }

  // ---------- 媒体库 ----------
  function librarySorts(type) {
    const sorts = [
      { key: 'SortName', label: '名称' },
      { key: 'ProductionYear', label: '年份' },
    ];
    if (type === 'Series') sorts.push({ key: 'LatestEpisode', label: '最新一集' });
    sorts.push({ key: 'DateCreated', label: '最近添加' });
    return sorts;
  }

  function sortOrder(sort) {
    return sort === 'SortName' ? 'Ascending' : 'Descending';
  }

  async function renderLibrary(id, sort = 'SortName', seq) {
    if (!state.views.length) await loadViews();
    const lib = state.views.find((v) => v.Id === id);
    const type = lib && lib.CollectionType === 'tvshows' ? 'Series' : 'Movie';
    const title = lib ? lib.Name : '媒体库';
    const order = sortOrder(sort);

    const data = await api('/Items?' + new URLSearchParams({
      ParentId: id,
      Recursive: 'true',
      IncludeItemTypes: type,
      SortBy: sort,
      SortOrder: order,
      Limit: '60',
      StartIndex: '0',
    }));

    state.list = { id, type, sort, order, items: data.Items || [], total: data.TotalRecordCount || 0 };

    const sortButtons = librarySorts(type).map((s) => `
      <button class="btn ${sort === s.key ? 'primary' : ''}" data-sort="${s.key}">${s.label}</button>`).join('');

    if (seq !== undefined && seq !== routeSeq) return;
    view.innerHTML = `<div class="page">
      <div class="page-head">
        <h1>${esc(title)}</h1>
        ${sortButtons}
        <span class="muted" id="listMeta"></span>
      </div>
      <div class="grid" id="grid"></div>
      <div style="text-align:center;margin-top:24px" id="moreWrap"></div>
    </div>`;

    view.querySelectorAll('[data-sort]').forEach((b) => {
      b.onclick = () => renderLibrary(id, b.dataset.sort, ++routeSeq);
    });
    paintGrid();
    window.scrollTo({ top: 0 });
  }

  function paintGrid() {
    const list = state.list;
    const grid = $('#grid');
    if (!list || !grid) return;
    grid.innerHTML = list.items.length
      ? list.items.map(card).join('')
      : '<div class="empty" style="grid-column:1/-1">没有条目</div>';
    const meta = $('#listMeta');
    if (meta) meta.textContent = `共 ${list.total} 条，已加载 ${list.items.length} 条`;
    const more = $('#moreWrap');
    if (more) {
      if (list.items.length < list.total) {
        more.innerHTML = '<button class="btn" id="loadMore">加载更多</button>';
        $('#loadMore').onclick = loadMore;
      } else {
        more.innerHTML = '';
      }
    }
  }

  async function loadMore() {
    const list = state.list;
    if (!list) return;
    const data = await api('/Items?' + new URLSearchParams({
      ParentId: list.id,
      Recursive: 'true',
      IncludeItemTypes: list.type,
      SortBy: list.sort,
      SortOrder: list.order || sortOrder(list.sort),
      Limit: '60',
      StartIndex: String(list.items.length),
    }));
    list.items.push(...(data.Items || []));
    list.total = data.TotalRecordCount || list.total;
    paintGrid();
  }

  // ---------- 搜索 ----------
  const SEARCH_SORTS = {
    recent: ['DateCreated', 'Descending'],
    name: ['SortName', 'Ascending'],
    year: ['ProductionYear', 'Descending'],
    rating: ['CommunityRating', 'Descending'],
  };

  async function renderSearch(term, seq) {
    if (!state.genres) {
      try {
        const g = await api('/Genres');
        state.genres = (g.Items || []).map((x) => x.Name).filter(Boolean).sort();
      } catch (e) {
        state.genres = [];
      }
    }
    const f = state.searchFilters || (state.searchFilters = { type: 'Movie,Series,Episode', genre: '', year: '', sort: 'recent' });
    const [sortBy, sortOrder] = SEARCH_SORTS[f.sort] || SEARCH_SORTS.recent;
    const params = new URLSearchParams({
      Recursive: 'true',
      SearchTerm: term,
      IncludeItemTypes: f.type,
      SortBy: sortBy,
      SortOrder: sortOrder,
      Limit: '60',
    });
    if (f.genre) params.set('Genres', f.genre);
    if (f.year) params.set('Years', f.year);
    const data = await api('/Items?' + params);

    const typeOpts = [
      ['Movie,Series,Episode', '全部类型'],
      ['Movie', '电影'],
      ['Series', '剧集'],
      ['Episode', '单集'],
    ].map(([v, label]) => `<option value="${v}" ${f.type === v ? 'selected' : ''}>${label}</option>`).join('');
    const genreOpts = ['<option value="">全部分类</option>']
      .concat(state.genres.map((g) => `<option value="${esc(g)}" ${f.genre === g ? 'selected' : ''}>${esc(g)}</option>`))
      .join('');

    if (seq !== undefined && seq !== routeSeq) return;
    state.search = { term, items: data.Items || [], total: data.TotalRecordCount || 0 };

    view.innerHTML = `<div class="page">
      <div class="page-head"><h1>搜索：${esc(term)}</h1>
        <span class="muted">共 ${data.TotalRecordCount || 0} 条</span></div>
      <div class="filters">
        <select id="fType">${typeOpts}</select>
        <select id="fGenre">${genreOpts}</select>
        <input id="fYear" type="number" min="1900" max="2100" placeholder="年份" value="${esc(f.year)}">
        <select id="fSort">
          <option value="recent" ${f.sort === 'recent' || !f.sort ? 'selected' : ''}>最近添加</option>
          <option value="name" ${f.sort === 'name' ? 'selected' : ''}>名称</option>
          <option value="year" ${f.sort === 'year' ? 'selected' : ''}>年份</option>
          <option value="rating" ${f.sort === 'rating' ? 'selected' : ''}>评分</option>
        </select>
        <button class="btn" id="fReset">清除筛选</button>
      </div>
      <div class="grid" id="searchGrid">${(data.Items || []).map(card).join('') || '<div class="empty" style="grid-column:1/-1">没有结果</div>'}</div>
      <div style="text-align:center;margin-top:24px" id="searchMore"></div>
    </div>`;

    $('#fType').onchange = (e) => { f.type = e.target.value; renderSearch(term, ++routeSeq); };
    $('#fGenre').onchange = (e) => { f.genre = e.target.value; renderSearch(term, ++routeSeq); };
    $('#fYear').onchange = (e) => { f.year = e.target.value.trim(); renderSearch(term, ++routeSeq); };
    $('#fSort').onchange = (e) => { f.sort = e.target.value; renderSearch(term, ++routeSeq); };
    $('#fReset').onclick = () => {
      f.type = 'Movie,Series,Episode';
      f.genre = '';
      f.year = '';
      f.sort = 'recent';
      renderSearch(term, ++routeSeq);
    };
    paintSearchMore();
  }

  function paintSearchMore() {
    const s = state.search;
    const more = $('#searchMore');
    if (!more || !s) return;
    if (s.items.length < s.total) {
      more.innerHTML = '<button class="btn" id="searchLoadMore">加载更多</button>';
      $('#searchLoadMore').onclick = loadSearchMore;
    } else {
      more.innerHTML = '';
    }
  }

  async function loadSearchMore() {
    const s = state.search;
    const f = state.searchFilters || {};
    if (!s) return;
    const [sortBy, sortOrder] = SEARCH_SORTS[f.sort] || SEARCH_SORTS.recent;
    const params = new URLSearchParams({
      Recursive: 'true',
      SearchTerm: s.term,
      IncludeItemTypes: f.type || 'Movie,Series,Episode',
      SortBy: sortBy,
      SortOrder: sortOrder,
      Limit: '60',
      StartIndex: String(s.items.length),
    });
    if (f.genre) params.set('Genres', f.genre);
    if (f.year) params.set('Years', f.year);
    const data = await api('/Items?' + params);
    s.items.push(...(data.Items || []));
    s.total = data.TotalRecordCount || s.total;
    const grid = $('#searchGrid');
    if (grid) grid.innerHTML = s.items.map(card).join('');
    paintSearchMore();
  }

  // ---------- 收藏 ----------
  async function renderFavorites(seq) {
    const data = await api('/Items?' + new URLSearchParams({
      Recursive: 'true',
      IsFavorite: 'true',
      IncludeItemTypes: 'Movie,Series,Episode',
      SortBy: 'DateCreated',
      SortOrder: 'Descending',
      Limit: '60',
    }));
    if (seq !== undefined && seq !== routeSeq) return;
    const items = data.Items || [];
    view.innerHTML = `<div class="page">
      <div class="page-head"><h1>我的收藏</h1>
        <span class="muted">共 ${data.TotalRecordCount || 0} 条</span></div>
      <div class="grid">${items.map(card).join('') || '<div class="empty" style="grid-column:1/-1">还没有收藏，详情页点「收藏」即可</div>'}</div>
    </div>`;
    window.scrollTo({ top: 0 });
  }

  // ---------- 类型 / 演员浏览 ----------
  async function renderBrowse(kind, value, seq) {
    const params = new URLSearchParams({
      Recursive: 'true',
      IncludeItemTypes: 'Movie,Series',
      Limit: '60',
      StartIndex: '0',
      SortBy: 'DateCreated',
      SortOrder: 'Descending',
    });
    if (kind === 'genre') params.set('Genres', value);
    else params.set('Person', value);

    const data = await api('/Items?' + params);
    if (seq !== undefined && seq !== routeSeq) return;
    state.browse = { kind, value, items: data.Items || [], total: data.TotalRecordCount || 0 };
    const title = kind === 'genre' ? `类型：${value}` : `演员：${value}`;

    view.innerHTML = `<div class="page">
      <div class="page-head"><h1>${esc(title)}</h1>
        <span class="muted">共 ${data.TotalRecordCount || 0} 条</span></div>
      <div class="grid" id="browseGrid">${(data.Items || []).map(card).join('') || '<div class="empty" style="grid-column:1/-1">没有结果</div>'}</div>
      <div style="text-align:center;margin-top:24px" id="browseMore"></div>
    </div>`;
    paintBrowseMore();
    window.scrollTo({ top: 0 });
  }

  function paintBrowseMore() {
    const b = state.browse;
    const more = $('#browseMore');
    if (!b || !more) return;
    if (b.items.length < b.total) {
      more.innerHTML = '<button class="btn" id="browseLoadMore">加载更多</button>';
      $('#browseLoadMore').onclick = loadBrowseMore;
    } else {
      more.innerHTML = '';
    }
  }

  async function loadBrowseMore() {
    const b = state.browse;
    if (!b) return;
    const params = new URLSearchParams({
      Recursive: 'true',
      IncludeItemTypes: 'Movie,Series',
      Limit: '60',
      StartIndex: String(b.items.length),
      SortBy: 'DateCreated',
      SortOrder: 'Descending',
    });
    if (b.kind === 'genre') params.set('Genres', b.value);
    else params.set('Person', b.value);

    const data = await api('/Items?' + params);
    b.items.push(...(data.Items || []));
    b.total = data.TotalRecordCount || b.total;
    const grid = $('#browseGrid');
    if (grid) grid.innerHTML = b.items.map(card).join('');
    paintBrowseMore();
  }

  // ---------- 详情 ----------
  async function renderItem(id, seq) {
    const it = await api('/Items/' + encodeURIComponent(id));
    const ud = it.UserData || {};
    const playable = it.Type === 'Movie' || it.Type === 'Episode';

    let childrenHTML = '';
    let nextItem = null;
    let isContinue = false;
    if (it.Type === 'Series') {
      const [seasons, nextUp] = await Promise.all([
        api(`/Shows/${encodeURIComponent(id)}/Seasons`),
        api(`/Shows/NextUp?SeriesId=${encodeURIComponent(id)}&Limit=1`),
      ]);
      childrenHTML = childrenSection('季', seasons.Items || []);
      nextItem = (nextUp.Items || [])[0] || null;
      isContinue = !!nextItem;
      if (!nextItem) {
        // 没看过：回退到第一季第一集，提供「开始观看」。
        const firstSeason = (seasons.Items || [])[0];
        if (firstSeason) {
          const eps = await api(`/Shows/${encodeURIComponent(id)}/Episodes`);
          nextItem = (eps.Items || []).find((e) => e.SeasonId === firstSeason.Id) || null;
        }
      }
    } else if (it.Type === 'Season') {
      const eps = await api(`/Shows/${encodeURIComponent(it.SeriesId)}/Episodes?SeasonId=${encodeURIComponent(it.Id)}&Limit=500`);
      childrenHTML = episodeSection(eps.Items || []);
    } else if (it.Type === 'Episode' && it.SeriesId) {
      const eps = await api(`/Shows/${encodeURIComponent(it.SeriesId)}/Episodes`);
      const list = (eps.Items || []).slice().sort((a, b) =>
        (a.ParentIndexNumber || 0) - (b.ParentIndexNumber || 0) ||
        (a.IndexNumber || 0) - (b.IndexNumber || 0));
      const idx = list.findIndex((e) => e.Id === it.Id);
      if (idx >= 0 && idx + 1 < list.length) nextItem = list[idx + 1];
    }

    const bg = imageUrl(it, 'Backdrop') || imageUrl(it);
    const poster = imageUrl(it);
    const logo = imageUrl(it, 'Logo');
    const meta = [
      TYPE[it.Type] || it.Type,
      it.CommunityRating ? `★ ${it.CommunityRating.toFixed(1)}` : '',
      it.OfficialRating,
      it.RunTimeTicks ? fmtTicks(it.RunTimeTicks) : '',
    ].filter(Boolean).map((x) => `<span class="badge">${esc(x)}</span>`).join('');
    const progress = ud.Played
      ? '<span class="badge ok">已看</span>'
      : ud.PlaybackPositionTicks ? `<span class="badge warn">看到 ${fmtTicks(ud.PlaybackPositionTicks)}</span>` : '';

    const genres = (it.Genres || []).map((g) =>
      `<a class="genre" href="#/genre/${encodeURIComponent(g)}">${esc(g)}</a>`).join('');
    const cast = (it.People || []).slice(0, 8).map((p) =>
      `<a class="cast-name" href="#/person/${encodeURIComponent(p.Name)}">${esc(p.Name)}${p.Role ? `<em>${esc(p.Role)}</em>` : ''}</a>`).join('');

    const parents = [];
    if (it.SeriesId) parents.push(`<a class="btn" href="#/item/${encodeURIComponent(it.SeriesId)}">所属剧集</a>`);
    if (it.SeasonId) parents.push(`<a class="btn" href="#/item/${encodeURIComponent(it.SeasonId)}">所属季</a>`);

    let similarItems = [];
    if (['Movie', 'Series', 'Episode', 'Season'].includes(it.Type)) {
      try {
        const sim = await api(`/Items/${encodeURIComponent(id)}/Similar?Limit=12`);
        similarItems = sim.Items || [];
      } catch (e) {
        similarItems = [];
      }
    }

    const sources = it.MediaSources || [];
    const curPlayer = localStorage.getItem('lime.localPlayer.v2') || 'potplayer';
    const sourceHTML = sources.length ? `<div class="section">
      <h2>片源</h2>
      <div class="source-toolbar">
        <span class="badge">本地播放器</span>
        <select id="localPlayer">${LOCAL_PLAYERS.map(([k, l]) =>
          `<option value="${k}" ${curPlayer === k ? 'selected' : ''}>${l}</option>`).join('')}</select>
      </div>
      <div class="sources">${sources.map((s) => {
        const subs = (s.MediaStreams || []).filter((m) => m.Type === 'Subtitle').length;
        return `
        <div class="source-row">
          <div class="path">${esc(s.Name || 'Default')}<br>${esc(s.Path)}</div>
          <span class="badge">${esc(s.Container || '—')}</span>
          ${subs ? `<span class="badge">${subs} 字幕</span>` : ''}
          <button class="btn primary" data-local="${esc(s.Id)}" data-item="${esc(it.Id)}">播放</button>
          <button class="btn" data-playlist="${esc(s.Id)}" data-item="${esc(it.Id)}">播放列表</button>
          <button class="btn" data-copy="${esc(s.Path)}">复制直链</button>
          <button class="btn" data-play="${esc(s.Path)}" data-container="${esc(s.Container)}">网页播放</button>
        </div>`;
      }).join('')}
      </div></div>` : '';

    if (seq !== undefined && seq !== routeSeq) return;
    view.innerHTML = `
      <section class="detail-hero" ${bg ? `style="background-image:url('${esc(bg)}')"` : ''}>
        <button class="btn ghost back-btn" id="backBtn">← 返回</button>
        <div class="detail-inner">
          <div class="detail-poster">${poster ? `<img src="${esc(poster)}" alt="">` : ''}</div>
          <div class="detail-info">
            ${logo ? `<img class="detail-logo" src="${esc(logo)}" alt="">` : ''}
            <h1>${esc(it.Name)}</h1>
            ${it.ProductionYear ? `<div class="title-year">${it.ProductionYear}</div>` : ''}
            <div class="meta-row">${meta}${progress}${ud.IsFavorite ? '<span class="badge">★ 收藏</span>' : ''}</div>
            ${genres ? `<div class="genre-row">${genres}</div>` : ''}
            ${it.Overview ? `<p class="overview">${esc(it.Overview)}</p>` : ''}
            ${cast ? `<div class="cast-row"><span class="cast-label">演员</span>${cast}</div>` : ''}
            <div class="hero-actions">
              ${playable ? `<button class="btn primary" id="playBtn">${ud.PlaybackPositionTicks ? '继续播放' : '播放'}</button>` : ''}
              ${nextItem ? `<button class="btn ${playable ? '' : 'primary'}" id="nextBtn">${it.Type === 'Series' ? (isContinue ? '继续观看' : '开始观看') : '下一集'} ${epLabel(nextItem)}</button>` : ''}
              <button class="btn" id="playedBtn">${ud.Played ? '标记未看' : '标记已看'}</button>
              <button class="btn" id="favBtn">${ud.IsFavorite ? '取消收藏' : '收藏'}</button>
              ${parents.join('')}
            </div>
          </div>
        </div>
      </section>
      ${sourceHTML}
      ${childrenHTML}
      ${similarItems.length ? `<div class="section"><h2>相似推荐</h2>
        <div class="rail-scroll">${similarItems.map(card).join('')}</div></div>` : ''}`;

    if (playable) $('#playBtn').onclick = () => playItem(it);
    if (nextItem) $('#nextBtn').onclick = () => playById(nextItem.Id);
    $('#playedBtn').onclick = () => togglePlayed(it, ud);
    $('#favBtn').onclick = () => toggleFavorite(it, ud);
    $('#backBtn').onclick = () => history.back();

    const playerSel = $('#localPlayer');
    if (playerSel) {
      playerSel.onchange = () => localStorage.setItem('lime.localPlayer.v2', playerSel.value);
    }
    view.querySelectorAll('[data-copy]').forEach((b) => {
      b.onclick = () => copyText(b.dataset.copy);
    });
    view.querySelectorAll('[data-local]').forEach((b) => {
      b.onclick = () => playLocal(b.dataset.item, b.dataset.local);
    });
    view.querySelectorAll('[data-playlist]').forEach((b) => {
      b.onclick = () => playPlaylist(b.dataset.item, b.dataset.playlist);
    });
    view.querySelectorAll('[data-play]').forEach((b) => {
      b.onclick = () => {
        const idx = sources.findIndex((s) => s.Path === b.dataset.play);
        const src = idx >= 0 ? sources[idx] : null;
        playSource(it, b.dataset.play, b.dataset.container,
          ud.PlaybackPositionTicks || 0, src ? src.MediaStreams : [], sources, idx);
      };
    });
    view.querySelectorAll('[data-episode]').forEach((el) => {
      el.onclick = () => { location.hash = `#/item/${encodeURIComponent(el.dataset.episode)}`; };
    });
    window.scrollTo({ top: 0 });
  }

  function childrenSection(title, items) {
    if (!items.length) return '';
    return `<div class="section"><h2>${esc(title)}</h2><div class="grid">${items.map(card).join('')}</div></div>`;
  }

  function episodeSection(items) {
    if (!items.length) return '';
    return `<div class="section"><h2>单集</h2>${items.map((e) => {
      const ud = e.UserData || {};
      const pct = ud.PlayedPercentage || 0;
      const thumb = imageUrl(e);
      return `<div class="episode" data-episode="${esc(e.Id)}">
        ${thumb ? `<img class="thumb" src="${esc(thumb)}" alt="" loading="lazy">` : ''}
        <div class="num">${e.IndexNumber ? String(e.IndexNumber).padStart(2, '0') : ''}</div>
        <div class="body">
          <div class="name">${esc(e.Name)}</div>
          <div class="ov">${esc(e.Overview || '')}</div>
          ${pct ? `<div class="progress"><i style="width:${pct}%"></i></div>` : ''}
        </div>
        ${ud.Played ? '<span class="badge ok">已看</span>' : '<span class="go">▶</span>'}
      </div>`;
    }).join('')}</div>`;
  }

  // ---------- 操作 ----------
  async function togglePlayed(it, ud) {
    const id = encodeURIComponent(it.Id);
    const on = !!ud.Played;
    await api(`/Users/${state.user.Id}/PlayedItems/${id}`, { method: on ? 'DELETE' : 'POST' });
    toast(on ? '已标记为未看' : '已标记为已看');
    await route();
  }

  async function toggleFavorite(it, ud) {
    const id = encodeURIComponent(it.Id);
    const on = !!ud.IsFavorite;
    await api(`/Users/${state.user.Id}/FavoriteItems/${id}`, { method: on ? 'DELETE' : 'POST' });
    toast(on ? '已取消收藏' : '已收藏');
    await route();
  }

  async function copyText(text) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      ta.remove();
    }
    toast('已复制直链');
  }

  // ---------- 播放 ----------
  let session = null;

  async function playById(id) {
    const it = await api('/Items/' + encodeURIComponent(id));
    await playItem(it);
  }

  async function playItem(it) {
    if (!it.MediaSources || !it.MediaSources.length) {
      const full = await api('/Items/' + encodeURIComponent(it.Id));
      it = full;
    }
    const src = (it.MediaSources || [])[0];
    if (!src) return toast('没有片源', true);
    return playLocal(it.Id, src.Id);
  }

  // 唤起系统播放器。默认 PotPlayer，选择记在这台设备。
  async function playLocal(itemId, sourceId) {
    const player = localStorage.getItem('lime.localPlayer.v2') || 'potplayer';
    let path = `/web/api/play-target?itemId=${encodeURIComponent(itemId)}` +
      `&player=${encodeURIComponent(player)}`;
    if (sourceId) path += `&sourceId=${encodeURIComponent(sourceId)}`;
    const r = await api(path);
    location.href = r.ProtocolUrl;
    setTimeout(() => toast(`正在用 ${r.PlayerLabel} 打开；没反应就用「播放列表」`), 1200);
  }

  async function playPlaylist(itemId, sourceId) {
    const player = localStorage.getItem('lime.localPlayer.v2') || 'potplayer';
    const r = await api(`/web/api/play-target?itemId=${encodeURIComponent(itemId)}` +
      `&sourceId=${encodeURIComponent(sourceId)}&player=${encodeURIComponent(player)}`);
    location.href = r.M3uUrl;
  }

  function playSource(it, url, container, startTicks, streams, sources, sourceIdx) {
    const c = String(container || '').toLowerCase();
    if (!['mp4', 'm4v', 'webm'].includes(c)) {
      copyText(url);
      toast(`浏览器播不了 ${c || '该容器'}，已复制直链，交给 VLC / 官方客户端`);
      return;
    }
    openPlayer(it, url, startTicks, streams || [], sources || [], sourceIdx || 0);
  }

  function sessionID() {
    const bytes = new Uint8Array(16);
    if (window.crypto && crypto.getRandomValues) crypto.getRandomValues(bytes);
    return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
  }

  function savedPlaybackRate() {
    const rate = parseFloat(localStorage.getItem('lime.rate') || '1');
    return [0.75, 1, 1.25, 1.5, 2].includes(rate) ? rate : 1;
  }

  function openPlayer(it, url, startTicks, streams, sources, sourceIdx) {
    const player = $('#player');
    const video = $('#video');
    $('#playerTitle').textContent = it.Name;
    $('#playerHint').textContent = '浏览器直接播放，失败请用官方客户端';
    player.classList.remove('hidden');
    video.src = url;
    const rate = savedPlaybackRate();
    video.playbackRate = rate;
    $('#playerRate').value = String(rate);
    setupSubtitles(video, streams || []);
    setupSourceSelect(it, sources || [], sourceIdx || 0);

    const sid = sessionID();
    session = { id: sid, itemID: it.Id, timer: 0, sent: false };

    const send = (path, extra = {}) => {
      api(path, {
        method: 'POST',
        body: JSON.stringify({
          ItemId: it.Id,
          PlaySessionId: sid,
          PositionTicks: Math.round((video.currentTime || 0) * 10000000),
          IsPaused: video.paused,
          ...extra,
        }),
      }).catch(() => {});
    };

    video.onloadedmetadata = () => {
      if (startTicks > 0 && startTicks < video.duration * 10000000 * 0.95) {
        video.currentTime = startTicks / 10000000;
      }
      send('/Sessions/Playing');
      session.timer = setInterval(() => {
        if (!video.paused) send('/Sessions/Playing/Progress');
      }, 10000);
    };
    video.onwaiting = () => {
      $('#playerHint').textContent = '缓冲中…';
    };
    video.onplaying = () => {
      $('#playerHint').textContent = '浏览器直接播放，失败请用官方客户端';
    };
    // 暂停/恢复立刻上报一次，否则要等到下一次进度心跳，
    // 服务端和其他客户端的暂停状态会滞后。
    video.addEventListener('play', () => send('/Sessions/Playing/Progress'));
    video.addEventListener('pause', () => {
      if (!video.ended) send('/Sessions/Playing/Progress');
    });
    video.onerror = () => {
      $('#playerHint').innerHTML = '播放失败，<a href="#" id="playerRetry">重试</a>，或复制直链用 VLC / 官方客户端';
      const retry = $('#playerRetry');
      if (retry) {
        retry.onclick = (ev) => {
          ev.preventDefault();
          const pos = Math.round((video.currentTime || 0) * 10000000) || startTicks;
          openPlayer(it, url, pos, streams || [], sources || [], sourceIdx || 0);
        };
      }
    };
    video.onended = async () => {
      send('/Sessions/Playing/Stopped');
      if (session && session.timer) {
        clearInterval(session.timer);
        session.timer = 0;
      }
      const moved = await playNextEpisode(it);
      if (!moved) closePlayer();
    };
  }

  // 播放器内切换片源（多版本）。
  function setupSourceSelect(it, sources, sourceIdx) {
    const sel = $('#playerSrc');
    if (!sel) return;
    sel.innerHTML = sources.map((s, i) =>
      `<option value="${i}">${esc(s.Name || ('片源 ' + (i + 1)))}</option>`).join('');
    sel.disabled = sources.length < 2;
    sel.value = String(sourceIdx || 0);
    sel.onchange = () => {
      const i = parseInt(sel.value, 10);
      const s = sources[i];
      if (!s) return;
      const posTicks = Math.round(($('#video').currentTime || 0) * 10000000);
      playSource(it, s.Path, s.Container, posTicks, s.MediaStreams || [], sources, i);
    };
  }

  // ---------- 字幕 ----------
  let subObjectURL = '';

  function setupSubtitles(video, streams) {
    const select = $('#playerSub');
    const subs = streams.filter((s) => s.Type === 'Subtitle' && s.DeliveryUrl);
    select.innerHTML = '<option value="">关闭</option>' + subs.map((s, i) => {
      const label = s.Title || s.DisplayTitle || s.Language || ('字幕 ' + (i + 1));
      return `<option value="${i}">${esc(label)}</option>`;
    }).join('');
    select.disabled = !subs.length;
    select.value = '';
    clearSubtitle(video);

    select.onchange = async () => {
      clearSubtitle(video);
      const idx = select.value === '' ? -1 : parseInt(select.value, 10);
      if (idx < 0 || !subs[idx]) return;
      const s = subs[idx];
      try {
        const text = await loadSubtitleText(s);
        subObjectURL = URL.createObjectURL(new Blob([text], { type: 'text/vtt' }));
        const track = document.createElement('track');
        track.kind = 'subtitles';
        track.label = s.Title || '字幕';
        track.srclang = s.Language || 'zh';
        track.src = subObjectURL;
        track.default = true;
        video.appendChild(track);
        const tracks = video.textTracks;
        if (tracks && tracks.length) tracks[tracks.length - 1].mode = 'showing';
      } catch (e) {
        toast(e.message || '字幕加载失败', true);
        select.value = '';
      }
    };
  }

  function clearSubtitle(video) {
    video.querySelectorAll('track').forEach((t) => t.remove());
    if (subObjectURL) {
      URL.revokeObjectURL(subObjectURL);
      subObjectURL = '';
    }
  }

  async function loadSubtitleText(s) {
    const fmt = String(s.Codec || '').toLowerCase();
    if (fmt === 'ass' || fmt === 'ssa') {
      throw new Error('ASS/SSA 字幕暂不支持浏览器渲染，请用官方客户端');
    }
    const res = await fetch(s.DeliveryUrl);
    if (!res.ok) throw new Error('字幕请求失败');
    const raw = await res.text();
    if (fmt === 'srt' || /-->\s*\d{2}:\d{2}:\d{2},\d{3}/.test(raw)) {
      return 'WEBVTT\n\n' + raw.replace(/\r+/g, '').replace(/(\d{2}:\d{2}:\d{2}),(\d{3})/g, '$1.$2');
    }
    return /^\s*WEBVTT/.test(raw) ? raw : 'WEBVTT\n\n' + raw;
  }

  // 播完自动下一集；没有下一集返回 false。
  async function playNextEpisode(it) {
    if (!it.SeriesId) return false;
    try {
      const r = await api(`/Shows/NextUp?SeriesId=${encodeURIComponent(it.SeriesId)}&Limit=5`);
      const next = (r.Items || []).find((e) => e.Id !== it.Id);
      if (!next) return false;
      toast('下一集：' + (next.Name || ''));
      await playById(next.Id);
      return true;
    } catch (e) {
      return false;
    }
  }

  function closePlayer() {
    const video = $('#video');
    clearSubtitle(video);
    if (session && session.timer) {
      clearInterval(session.timer);
      if (!video.ended) {
        api('/Sessions/Playing/Stopped', {
          method: 'POST',
          body: JSON.stringify({
            ItemId: session.itemID,
            PlaySessionId: session.id,
            PositionTicks: Math.round((video.currentTime || 0) * 10000000),
          }),
        }).catch(() => {});
      }
      session = null;
    }
    video.pause();
    video.removeAttribute('src');
    video.load();
    $('#player').classList.add('hidden');
  }

  // ---------- 事件 ----------
  window.addEventListener('hashchange', route);
  $('#loginForm').addEventListener('submit', doLogin);
  $('#logout').addEventListener('click', logout);
  $('#themeBtn').addEventListener('click', () => {
    applyTheme(document.documentElement.dataset.theme === 'light' ? 'dark' : 'light');
  });
  $('#playerClose').addEventListener('click', closePlayer);
  $('#player').addEventListener('click', (e) => { if (e.target.id === 'player') closePlayer(); });
  $('#playerRate').addEventListener('change', (e) => {
    const rate = parseFloat(e.target.value) || 1;
    $('#video').playbackRate = rate;
    localStorage.setItem('lime.rate', String(rate));
  });
  window.addEventListener('keydown', (e) => {
    if ($('#player').classList.contains('hidden')) return;
    const tag = (e.target.tagName || '').toUpperCase();
    if (tag === 'SELECT' || tag === 'INPUT' || tag === 'TEXTAREA') return;
    const video = $('#video');
    switch (e.key) {
      case ' ':
        e.preventDefault();
        video.paused ? video.play() : video.pause();
        break;
      case 'ArrowLeft':
        video.currentTime = Math.max(0, video.currentTime - 10);
        break;
      case 'ArrowRight':
        video.currentTime = Math.min(video.duration || Infinity, video.currentTime + 10);
        break;
      case 'f':
      case 'F':
        if (video.requestFullscreen) video.requestFullscreen();
        break;
      case 'Escape':
        closePlayer();
        break;
    }
  });
  $('#search').addEventListener('keydown', (e) => {
    if (e.key !== 'Enter') return;
    const term = e.target.value.trim();
    if (term) location.hash = '#/search/' + encodeURIComponent(term);
  });

  (async () => {
    if (!state.token) return showLogin();
    try {
      await enterApp();
    } catch {
      showLogin();
    }
  })();
})();
