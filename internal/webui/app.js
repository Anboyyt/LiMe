(() => {
  'use strict';

  const $ = (sel, root = document) => root.querySelector(sel);
  const state = {
    token: localStorage.getItem('lime.token') || '',
    user: JSON.parse(localStorage.getItem('lime.user') || 'null'),
    stats: null,
    list: null,
    libEdit: null,
    libBrowsePath: '',
    libPaths: null,
    libDraft: null,
    libCache: [],
  };

  // ---------- 主题 ----------
  // 图标用内联 SVG，避免部分安卓机型缺少 ☀/☾ 字形显示成方块。
  const ICON_SUN = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>';
  const ICON_MOON = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/></svg>';

  function applyTheme(theme) {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('lime.theme', theme);
    const btn = $('#themeBtn');
    if (btn) btn.innerHTML = theme === 'light' ? ICON_MOON : ICON_SUN;
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

  const TYPE_LABEL = { Movie: '电影', Series: '剧集', Season: '季', Episode: '单集' };
  // 与后端 localPlayers 保持一致；新增播放器两边都要改。
  const LOCAL_PLAYERS = [
    ['vlc', 'VLC'],
    ['potplayer', 'PotPlayer'],
    ['mpv', 'MPV'],
    ['iina', 'IINA（macOS）'],
    ['infuse', 'Infuse（iOS / macOS）'],
  ];

  function fmtTicks(t) {
    if (!t) return '0:00';
    const total = Math.floor(t / 10000000);
    const h = Math.floor(total / 3600);
    const m = Math.floor((total % 3600) / 60);
    const s = total % 60;
    return h > 0
      ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
      : `${m}:${String(s).padStart(2, '0')}`;
  }

  function fmtBytes(n) {
    if (!n) return '—';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return `${n.toFixed(i > 0 ? 1 : 0)} ${units[i]}`;
  }

  function fmtUnix(t) {
    return t ? new Date(t * 1000).toLocaleString('zh-CN', { hour12: false }) : '—';
  }

  function fmtUptime(sec) {
    const d = Math.floor(sec / 86400);
    const h = Math.floor((sec % 86400) / 3600);
    const m = Math.floor((sec % 3600) / 60);
    if (d) return `${d} 天 ${h} 小时`;
    if (h) return `${h} 小时 ${m} 分`;
    return `${m} 分`;
  }

  let toastTimer = 0;
  function toast(msg, isErr = false) {
    const el = $('#toast');
    el.textContent = msg;
    el.className = 'toast' + (isErr ? ' err' : '');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => el.classList.add('hidden'), 3000);
  }

  // ---------- API ----------
  async function api(path, opts = {}) {
    const headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) };
    if (state.token) headers['X-Emby-Token'] = state.token;
    const res = await fetch(path, { ...opts, headers });
    if (res.status === 401) {
      logout();
      throw new Error('登录已失效，请重新登录');
    }
    if (!res.ok) {
      const text = await res.text().catch(() => '');
      throw new Error(text || `HTTP ${res.status}`);
    }
    if (res.status === 204) return null;
    const ct = res.headers.get('content-type') || '';
    return ct.includes('application/json') ? res.json() : res.text();
  }

  // ---------- 登录 ----------
  function isAdmin() {
    return !!(state.user && state.user.Policy && state.user.Policy.IsAdministrator);
  }

  function applyRoleUI() {
    const admin = isAdmin();
    const nu = $('#navUsers');
    const nk = $('#navKeys');
    const nl = $('#navLibraries');
    const nh = $('#navHistory');
    const nn = $('#navNotify');
    if (nu) nu.classList.toggle('hidden', !admin);
    if (nk) nk.classList.toggle('hidden', !admin);
    if (nl) nl.classList.toggle('hidden', !admin);
    if (nh) nh.classList.toggle('hidden', !admin);
    if (nn) nn.classList.toggle('hidden', !admin);
  }

  function showLogin() {
    $('#app').classList.add('hidden');
    $('#login').classList.remove('hidden');
    $('#luser').focus();
  }

  function logout() {
    const token = state.token;
    if (token) {
      // 吊销服务端 token；失败也不影响本地退出
      fetch('/Sessions/Logout', { method: 'POST', headers: { 'X-Emby-Token': token } })
        .catch(() => {});
    }
    state.token = '';
    state.user = null;
    localStorage.removeItem('lime.token');
    localStorage.removeItem('lime.user');
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
          'Authorization': 'MediaBrowser Client="LiMe Web", Device="Browser", DeviceId="lime-web", Version="1.0"',
        },
        body: JSON.stringify({ Username: username, Pw: pw }),
      });
      if (!res.ok) throw new Error(res.status === 401 ? '用户名或密码错误' : `登录失败（${res.status}）`);
      const data = await res.json();
      state.token = data.AccessToken;
      state.user = data.User;
      localStorage.setItem('lime.token', state.token);
      localStorage.setItem('lime.user', JSON.stringify(state.user));
      $('#lpass').value = '';
      await enterApp();
    } catch (e) {
      err.textContent = e.message;
      err.classList.remove('hidden');
    }
  }

  // ---------- 启动 ----------
  async function enterApp() {
    // 校验 token 并刷新用户信息
    const me = await api('/Users/Me');
    // 运维面板只对管理员开放，普通用户直接回媒体库。
    if (!(me.Policy && me.Policy.IsAdministrator)) {
      location.replace('/');
      return;
    }
    $('#login').classList.add('hidden');
    $('#app').classList.remove('hidden');
    state.user = me;
    localStorage.setItem('lime.user', JSON.stringify(me));
    $('#userName').textContent = me.Name;
    applyRoleUI();
    await ensureStats();
    if (!location.hash) {
      location.hash = '#/overview'; // 触发 hashchange → route
    } else {
      await route();
    }
  }

  async function ensureStats() {
    state.stats = await api('/web/api/stats');
    renderLibNav();
    renderSideFoot();
    return state.stats;
  }

  function renderLibNav() {
    const libs = state.stats?.Stats?.Libraries || [];
    $('#navLibs').innerHTML = libs.map((l) =>
      `<a href="#/library/${encodeURIComponent(l.ID)}" data-nav="lib">${esc(l.Name)}
         <span class="muted" style="float:right">${l.Items}</span></a>`
    ).join('');
    markActiveNav();
  }

  function renderSideFoot() {
    const s = state.stats;
    if (!s) return;
    const scan = s.Scan || {};
    const dot = scan.Running ? 'running' : (scan.LastError ? 'fail' : 'done');
    $('#sideFoot').innerHTML =
      `${esc(s.ServerName)} <span class="muted">${esc(s.ProductVersion || 'dev')}</span><br>
       已运行 ${fmtUptime(s.UptimeSec || 0)}<br>
       <span class="dot ${dot}"></span> ${scan.Running ? '扫描中' : '空闲'}`;
  }

  function markActiveNav() {
    const hash = location.hash || '#/overview';
    document.querySelectorAll('#nav a').forEach((a) => {
      a.classList.toggle('active', a.getAttribute('href') === hash);
    });
  }

  // ---------- 路由 ----------
  async function route() {
    markActiveNav();
    const raw = (location.hash || '#/overview').replace(/^#\/?/, '');
    const [name, ...rest] = raw.split('/');
    const arg = rest.join('/');
    if (name !== 'libraries') state.libEdit = null;
    try {
      switch (name) {
        case 'library': await renderLibrary(decodeURIComponent(arg)); break;
        case 'libraries': await renderLibraries(); break;
        case 'item': await renderItem(decodeURIComponent(arg)); break;
        case 'search': await renderSearch(decodeURIComponent(arg)); break;
        case 'history': await renderHistory(); break;
        case 'logs': await renderLogs(); break;
        case 'notify': await renderNotify(); break;
        case 'users': await renderUsers(); break;
        case 'keys': await renderKeys(); break;
        case 'system': await renderSystem(); break;
        default: await renderOverview();
      }
    } catch (e) {
      $('#view').innerHTML = `<div class="empty">加载失败：${esc(e.message)}</div>`;
    }
  }

  // ---------- 概览 ----------
  async function renderOverview() {
    const [s, sessions] = await Promise.all([
      api('/web/api/stats'),
      api('/Sessions').catch(() => []),
    ]);
    state.stats = s;
    renderLibNav();
    renderSideFoot();
    $('#crumb').textContent = '概览';

    const st = s.Stats || {};
    const scan = s.Scan || {};
    const byType = st.ItemsByType || {};
    let scanLine;
    if (scan.Running) {
      const pct = scan.Total ? Math.min(100, Math.round(((scan.Current || 0) * 100) / scan.Total)) : 0;
      const lib = scan.Library ? `《${esc(scan.Library)}》` : '';
      scanLine = `<span class="dot running"></span> 扫描中${lib} ${scan.Current || 0}/${scan.Total || '?'}（${pct}%）`;
    } else if (scan.LastError) {
      scanLine = `<span class="dot fail"></span> 上次扫描有错误：${esc(scan.LastError)}`;
    } else {
      scanLine = `<span class="dot done"></span> 上次扫描 ${fmtUnix(scan.LastFinished)}，处理 ${scan.LastCount || 0} 条`;
    }

    const sc = s.Scrape || {};
    let scrapeLine;
    if (!s.ScrapeEnabled) {
      scrapeLine = '未配置 <code>LIME_TMDB_API_KEY</code>';
    } else if (sc.Running) {
      scrapeLine = `<span class="dot running"></span> 刮削中 ${sc.Processed || 0}/${sc.Total || 0}`;
    } else if (sc.LastError) {
      scrapeLine = `<span class="dot fail"></span> 上次刮削出错：${esc(sc.LastError)}`;
    } else if (sc.Partial) {
      scrapeLine = `<span class="dot partial"></span> ${esc(sc.LastNote || '上次刮削未跑完，剩余条目下次继续')}`;
    } else if (sc.LastFinished) {
      scrapeLine = `<span class="dot done"></span> 上次刮削 ${fmtUnix(sc.LastFinished)}，处理 ${sc.Processed || 0} 条`;
    } else {
      scrapeLine = '<span class="dot"></span> 还没刮削过';
    }

    const rows = (st.Libraries || []).map((l) => `
      <tr>
        <td><a href="#/library/${encodeURIComponent(l.ID)}">${esc(l.Name)}</a></td>
        <td>${esc(l.Kind)}</td>
        <td class="muted" style="word-break:break-all">${esc(l.Path)}</td>
        <td>${l.Items}</td>
      </tr>`).join('') || `<tr><td colspan="4" class="muted">没有媒体库</td></tr>`;

    const sessionRows = (sessions || []).map((x) => {
      const it = x.NowPlayingItem;
      const pos = x.PlayState?.PositionTicks;
      return `<tr>
        <td>${esc(x.UserName || '—')}</td>
        <td>${esc(x.DeviceName || '—')}<div class="muted" style="font-size:11px">${esc(x.Client || '')}</div></td>
        <td>${it ? esc(it.Name) : '<span class="muted">空闲</span>'}</td>
        <td>${pos ? fmtTicks(pos) : '—'}</td>
        <td class="muted">${x.LastActivityDate ? new Date(x.LastActivityDate).toLocaleString('zh-CN', { hour12: false }) : '—'}</td>
      </tr>`;
    }).join('') || '<tr><td colspan="5" class="muted">当前没有正在播放的会话</td></tr>';

    $('#view').innerHTML = `
      <div class="cards">
        <div class="card"><div class="k">媒体库</div><div class="v">${(st.Libraries || []).length}</div></div>
        <div class="card"><div class="k">条目总数</div><div class="v">${st.TotalItems || 0}</div></div>
        <div class="card"><div class="k">片源</div><div class="v">${st.MediaSources || 0}</div></div>
        <div class="card"><div class="k">播放记录</div><div class="v">${st.Playstates || 0}</div></div>
        <div class="card"><div class="k">用户</div><div class="v">${st.Users || 0}</div></div>
      </div>

      <div class="panel">
        <h3>正在播放（30 分钟内）</h3>
        <table>
          <thead><tr><th>用户</th><th>设备</th><th>条目</th><th>进度</th><th>最后活动</th></tr></thead>
          <tbody>${sessionRows}</tbody>
        </table>
      </div>

      <div class="panel">
        <h3>扫描</h3>
        <div class="row" style="margin-bottom:10px">${scanLine}</div>
        <div class="row">
          <button data-action="scan" ${scan.Running ? 'disabled' : ''}>立即全量扫描</button>
          <span class="muted">电影 ${byType.Movie || 0} · 剧集 ${byType.Series || 0} · 季 ${byType.Season || 0} · 单集 ${byType.Episode || 0}</span>
        </div>
      </div>

      <div class="panel">
        <h3>刮削（TMDB）</h3>
        <div class="row" style="margin-bottom:10px">${scrapeLine}</div>
        <div class="row">
          <button data-action="scrape" ${(!s.ScrapeEnabled || sc.Running) ? 'disabled' : ''}>立即刮削</button>
          <button data-action="scrape-avatars" ${(!s.ScrapeEnabled || sc.Running) ? 'disabled' : ''}>仅补演员头像</button>
          <span class="muted">按目录里的 TMDB ID 精确匹配，不搜索、不猜片</span>
        </div>
      </div>

      <div class="panel">
        <h3>媒体库</h3>
        <table>
          <thead><tr><th>名称</th><th>类型</th><th>路径</th><th>条目</th></tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>`;

    // 扫描/刮削进行中时自动刷新进度
    if (scan.Running || sc.Running) {
      setTimeout(() => {
        if ((location.hash || '#/overview') === '#/overview') route();
      }, 3000);
    }
  }

  // ---------- 媒体库管理 ----------
  const KIND_LABEL = { movies: '电影', tvshows: '剧集', mixed: '混合', auto: '自动识别' };
  const COVER_STYLE_LABEL = { collage: '斜角海报墙', minimal: '简约模糊', latest: '库内最新背景图' };

  // 浏览目录会重渲染表单，先把名称/类型收回 state，避免输入丢失。
  function collectLibDraft() {
    const name = $('#libName');
    const kind = $('#libKind');
    if (name && kind) {
      state.libDraft = { name: name.value, kind: kind.value };
    }
  }

  async function renderLibraries() {
    $('#crumb').textContent = '媒体库管理';
    const libs = await api('/web/api/libraries');
    state.libCache = libs || [];
    const admin = isAdmin();
    const covers = admin ? await api('/web/api/library-covers').catch(() => null) : null;
    const edit = state.libEdit
      ? ((libs || []).find((l) => l.ID === state.libEdit.ID) || state.libEdit)
      : null;
    const draft = state.libDraft || {};
    const nameVal = draft.name !== undefined ? draft.name : (edit ? edit.Name : '');
    const kindVal = draft.kind || (edit ? edit.Kind : 'auto');
    const editPaths = edit
      ? ((edit.Paths && edit.Paths.length ? edit.Paths : [edit.Path]).filter(Boolean))
      : [];
    const kindOptions = ['movies', 'tvshows', 'mixed', 'auto']
      .map((k) => `<option value="${k}" ${kindVal === k ? 'selected' : ''}>${KIND_LABEL[k]}</option>`)
      .join('');

    // 目录浏览器：当前目录就是选中的路径，下拉框进入下一级。
    if (!state.libPaths) {
      state.libPaths = edit
        ? ((edit.Paths && edit.Paths.length ? edit.Paths : [edit.Path]).filter(Boolean).slice())
        : [];
    }

    const rows = (libs || []).map((l) => `
      <tr draggable="true" data-id="${esc(l.ID)}">
        <td>${esc(l.Name)}</td>
        <td>${esc(KIND_LABEL[l.Kind] || l.Kind)}</td>
        <td class="muted" style="word-break:break-all">${esc((l.Paths && l.Paths.length ? l.Paths : [l.Path]).join(' / '))}</td>
        <td>${l.Items || 0}</td>
        ${admin ? `<td style="white-space:nowrap">
          <button data-action="move-lib" data-id="${esc(l.ID)}" data-dir="up" title="上移">↑</button>
          <button data-action="move-lib" data-id="${esc(l.ID)}" data-dir="down" title="下移">↓</button>
          <button data-action="scan-lib" data-id="${esc(l.ID)}" data-name="${esc(l.Name)}">扫描</button>
          <button data-action="edit-lib" data-id="${esc(l.ID)}" data-name="${esc(l.Name)}" data-path="${esc(l.Path)}" data-kind="${esc(l.Kind)}">编辑</button>
          <button class="danger" data-action="delete-lib" data-id="${esc(l.ID)}" data-name="${esc(l.Name)}">删除</button>
        </td>` : ''}
      </tr>`).join('') || `<tr><td colspan="${admin ? 5 : 4}" class="muted">还没有媒体库</td></tr>`;

    const form = admin ? `
      <div class="panel">
        <h3>${edit ? '编辑媒体库：' + esc(edit.Name) : '添加媒体库'}</h3>
        <div class="row">
          <input id="libName" placeholder="媒体库名称，如 电影" style="max-width:180px" value="${esc(nameVal)}">
          <select id="libKind" style="max-width:160px">${kindOptions}</select>
          <button class="primary" data-action="save-lib">${edit ? '保存' : '创建媒体库'}</button>
          ${edit ? '<button data-action="cancel-lib">关闭</button>' : ''}
        </div>
        <div class="lib-folders">
          <div class="folders-head">
            <span class="muted">媒体文件夹</span>
            <button data-action="open-dir-picker">+ 添加文件夹</button>
          </div>
          <div class="path-list" id="libPathList"></div>
        </div>
        <p class="muted" style="margin-bottom:0">
          可以添加多个文件夹；新目录要先在 docker-compose 的 volumes 里挂载。
        </p>
      </div>` : '';

    const coverPanel = admin && covers ? `
      <div class="panel">
        <h3>库封面</h3>
        <div class="kv">
          <div><b>当前样式</b><span>${COVER_STYLE_LABEL[covers.Style] || esc(covers.Style)}</span></div>
          <div><b>自定义封面</b><span>${covers.CustomCount ? `${covers.CustomCount} 个库（优先于自动封面）` : '无'}</span></div>
        </div>
        <div class="row" style="margin-top:12px">
          <select id="coverStyle" style="max-width:200px">
            ${['collage', 'minimal', 'latest'].map((k) =>
              `<option value="${k}" ${covers.Style === k ? 'selected' : ''}>${COVER_STYLE_LABEL[k]}</option>`).join('')}
          </select>
          <button class="primary" data-action="save-cover-style">保存样式</button>
          ${covers.CustomCount ? '<button data-action="use-auto-covers">改用自动封面</button>' : ''}
        </div>
        <div class="cover-grid">
          ${(covers.Libraries || []).map((l) => `
            <figure class="cover-tile">
              <img loading="lazy" alt="${esc(l.Name)}" src="/Items/${encodeURIComponent(l.ID)}/Images/Primary?tag=${encodeURIComponent(l.Tag || covers.Style)}&v=${encodeURIComponent(covers.Style)}&still=1&maxWidth=480">
              <figcaption><span>${esc(l.Name)}</span><em>${l.Custom ? '自定义' : (COVER_STYLE_LABEL[covers.Style] || '')}</em></figcaption>
            </figure>`).join('') || '<p class="muted">还没有媒体库</p>'}
        </div>
        <p class="muted" style="margin:8px 0 0">
          预览是各库当前封面；自动封面按库名取「标题 + 英文副标题 + 背景色」，与 lime-cover 的配置一致。
          「改用自动封面」会把已有自定义封面改名成 *.bak 备份，不会删除。
        </p>
      </div>` : '';

    $('#view').innerHTML = `${form}${coverPanel}
      <div class="panel">
        <h3>媒体库</h3>
        <table>
          <thead><tr><th>名称</th><th>类型</th><th>路径</th><th>条目</th>${admin ? '<th></th>' : ''}</tr></thead>
          <tbody id="libTableBody">${rows}</tbody>
        </table>
      </div>`;

    renderLibPathList();
    setupLibDrag();
  }

  // ---------- 媒体库拖拽排序 ----------
  let libDragID = null;

  function setupLibDrag() {
    const tbody = $('#libTableBody');
    if (!tbody) return;
    tbody.querySelectorAll('tr[data-id]').forEach((tr) => {
      tr.addEventListener('dragstart', (e) => {
        libDragID = tr.dataset.id;
        tr.classList.add('dragging');
        if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move';
      });
      tr.addEventListener('dragend', () => {
        tr.classList.remove('dragging');
        tbody.querySelectorAll('tr').forEach((x) => x.classList.remove('drop-target'));
        libDragID = null;
      });
      tr.addEventListener('dragover', (e) => {
        if (!libDragID || tr.dataset.id === libDragID) return;
        e.preventDefault();
        tr.classList.add('drop-target');
      });
      tr.addEventListener('dragleave', () => tr.classList.remove('drop-target'));
      tr.addEventListener('drop', async (e) => {
        e.preventDefault();
        tr.classList.remove('drop-target');
        const fromID = libDragID;
        const toID = tr.dataset.id;
        if (!fromID || fromID === toID) return;
        const ids = (state.libCache || []).map((l) => l.ID);
        const from = ids.indexOf(fromID);
        const to = ids.indexOf(toID);
        if (from < 0 || to < 0) return;
        ids.splice(from, 1);
        ids.splice(to, 0, fromID);
        try {
          await api('/web/api/libraries/reorder', {
            method: 'POST',
            body: JSON.stringify({ Ids: ids }),
          });
          await ensureStats();
          await renderLibraries();
          toast('顺序已保存');
        } catch (err) {
          toast(err.message || '排序失败', true);
        }
      });
    });
  }

  // ---------- 文件夹选择弹窗（Emby 式） ----------
  let dirState = null;

  async function openDirPicker(start) {
    dirState = { path: start || '/strm', dirs: [] };
    $('#dirModal').classList.remove('hidden');
    await loadDirPicker();
  }

  async function loadDirPicker() {
    if (!dirState) return;
    try {
      const b = await api('/web/api/browse?path=' + encodeURIComponent(dirState.path));
      dirState.path = b.Path || dirState.path;
      dirState.dirs = b.Dirs || [];
    } catch (e) {
      toast(e.message || '读取目录失败', true);
      return;
    }
    renderDirPicker();
  }

  function renderDirPicker() {
    $('#dirCurrent').textContent = dirState.path;
    const list = $('#dirList');
    list.innerHTML = dirState.dirs.length
      ? dirState.dirs.map((d) => `<div class="dir-row">
          <button class="dir-open" data-path="${esc(d.Path)}">📁 ${esc(d.Name)}</button>
          <button class="dir-choose" data-path="${esc(d.Path)}">选择</button>
        </div>`).join('')
      : '<div class="muted">没有子目录，可直接选择当前文件夹</div>';
    list.querySelectorAll('.dir-open').forEach((b) => {
      b.onclick = async () => {
        dirState.path = b.dataset.path;
        await loadDirPicker();
      };
    });
    list.querySelectorAll('.dir-choose').forEach((b) => {
      b.onclick = () => addLibPath(b.dataset.path);
    });
  }

  function addLibPath(p) {
    if (!state.libPaths) state.libPaths = [];
    if (!state.libPaths.includes(p)) state.libPaths.push(p);
    $('#dirModal').classList.add('hidden');
    renderLibPathList();
    toast('已添加：' + p);
  }

  function renderLibPathList() {
    const list = $('#libPathList');
    if (!list) return;
    const paths = state.libPaths || [];
    list.innerHTML = paths.length
      ? paths.map((p, i) => `<div class="path-item">
          <code>${esc(p)}</code>
          <button data-action="remove-lib-path" data-idx="${i}">移除</button>
        </div>`).join('')
      : '<span class="muted">还没有文件夹，点「+ 添加文件夹」</span>';
  }

  // ---------- 媒体库 / 搜索 ----------
  function defaultType(lib) {
    if (!lib) return 'Movie';
    return lib.Kind === 'tvshows' ? 'Series' : 'Movie';
  }

  async function renderLibrary(id) {
    await ensureStats();
    const lib = (state.stats.Stats.Libraries || []).find((l) => l.ID === id);
    if (!lib) {
      $('#view').innerHTML = '<div class="empty">媒体库不存在</div>';
      return;
    }
    $('#crumb').textContent = lib.Name;
    state.list = { mode: 'library', libId: id, type: defaultType(lib), term: '', items: [], total: 0 };
    renderListShell(lib.Name, lib);
    await fetchPage(true);
  }

  async function renderSearch(term) {
    await ensureStats();
    $('#crumb').textContent = `搜索：${term}`;
    state.list = { mode: 'search', libId: '', type: '', term, items: [], total: 0 };
    renderListShell('', null);
    await fetchPage(true);
  }

  function renderListShell(title, lib) {
    const tabs = [];
    if (lib) {
      const kinds = lib.Kind === 'tvshows'
        ? [['Series', '剧集'], ['Season', '季'], ['Episode', '单集']]
        : lib.Kind === 'movies'
          ? [['Movie', '电影']]
          : [['Movie', '电影'], ['Series', '剧集'], ['Episode', '单集']];
      for (const [type, label] of kinds) {
        tabs.push(`<button data-action="set-type" data-type="${type}"
          class="${state.list.type === type ? 'active' : ''}">${label}</button>`);
      }
    }
    $('#view').innerHTML = `
      ${tabs.length ? `<div class="tabs">${tabs}</div>` : ''}
      <div id="listMeta" class="muted" style="margin-bottom:10px"></div>
      <div class="grid" id="itemGrid"></div>
      <div class="more" id="moreWrap"></div>`;
  }

  async function fetchPage(reset) {
    const list = state.list;
    if (!list) return;
    const p = new URLSearchParams();
    if (list.mode === 'library') {
      p.set('ParentId', list.libId);
      p.set('Recursive', 'true');
      if (list.type) p.set('IncludeItemTypes', list.type);
    } else {
      p.set('SearchTerm', list.term);
      p.set('Recursive', 'true');
      p.set('IncludeItemTypes', 'Movie,Series,Episode');
    }
    p.set('SortBy', list.mode === 'search' ? 'SortName' : 'SortName');
    p.set('SortOrder', 'Ascending');
    p.set('Limit', '60');
    p.set('StartIndex', String(reset ? 0 : list.items.length));

    const data = await api('/Items?' + p.toString());
    if (reset) list.items = [];
    list.items.push(...(data.Items || []));
    list.total = data.TotalRecordCount || 0;
    paintList();
  }

  function paintList() {
    const list = state.list;
    if (!list) return;
    const grid = $('#itemGrid');
    if (!grid) return;
    if (!list.items.length) {
      grid.innerHTML = '<div class="empty" style="grid-column:1/-1">没有条目</div>';
    } else {
      grid.innerHTML = list.items.map(itemCard).join('');
    }
    const meta = $('#listMeta');
    if (meta) meta.textContent = `共 ${list.total} 条，已加载 ${list.items.length} 条`;
    const more = $('#moreWrap');
    if (more) {
      more.innerHTML = list.items.length < list.total
        ? '<button data-action="more">加载更多</button>' : '';
    }
  }

  function itemCard(it) {
    const ud = it.UserData || {};
    const badges = [];
    if (ud.Played) badges.push('<span class="badge ok">已看</span>');
    if (ud.PlaybackPositionTicks) badges.push(`<span class="badge warn">${fmtTicks(ud.PlaybackPositionTicks)}</span>`);
    if (ud.IsFavorite) badges.push('<span class="badge">★</span>');
    if (ud.UnplayedItemCount) badges.push(`<span class="badge">${ud.UnplayedItemCount} 未看</span>`);
    const ep = it.Type === 'Episode' && it.ParentIndexNumber != null
      ? ` S${String(it.ParentIndexNumber).padStart(2, '0')}E${String(it.IndexNumber || 0).padStart(2, '0')}` : '';
    const pct = ud.PlayedPercentage || 0;

    const showPoster = it.Type === 'Movie' || it.Type === 'Series' || it.Type === 'Season';
    const tag = it.ImageTags && it.ImageTags.Primary;
    const poster = !showPoster ? ''
      : tag
        ? `<img class="poster" loading="lazy" alt=""
             src="/Items/${esc(it.Id)}/Images/Primary?tag=${esc(tag)}&maxWidth=400">`
        : `<div class="poster placeholder">${esc((it.Name || '?').slice(0, 1))}</div>`;

    const canPlay = it.Type === 'Movie' || it.Type === 'Episode';
    return `<div class="item-card" data-action="open-item" data-id="${esc(it.Id)}">
      ${canPlay ? `<button class="card-play" title="用本地播放器播放"
        data-action="play-local" data-item="${esc(it.Id)}">▶</button>` : ''}
      ${poster}
      <div class="name">${esc(it.Name)}</div>
      <div class="meta">${it.ProductionYear ? it.ProductionYear + ' · ' : ''}${TYPE_LABEL[it.Type] || it.Type}${ep}</div>
      <div class="badges">${badges.join('')}</div>
      ${pct ? `<div class="progress"><i style="width:${pct}%"></i></div>` : ''}
    </div>`;
  }

  // ---------- 条目详情 ----------
  async function renderItem(id) {
    const it = await api('/Items/' + encodeURIComponent(id));
    $('#crumb').textContent = it.Name;
    const ud = it.UserData || {};
    const sources = it.MediaSources || [];
    const curPlayer = localStorage.getItem('lime.localPlayer.v2') || 'potplayer';

    let children = '';
    if (it.Type === 'Series') {
      const seasons = await api(`/Shows/${encodeURIComponent(id)}/Seasons`);
      children = renderChildren('季', seasons.Items || []);
    } else if (it.Type === 'Season') {
      const eps = await api(`/Shows/${encodeURIComponent(it.SeriesId)}/Episodes`);
      const mine = (eps.Items || []).filter((e) => e.SeasonId === it.Id);
      children = renderChildren('单集', mine);
    }

    const sourceRows = sources.map((s) => {
      const streams = (s.MediaStreams || []).map((m) => {
        const parts = [m.Type, m.Codec];
        if (m.Width) parts.push(`${m.Width}x${m.Height}`);
        if (m.Channels) parts.push(`${m.Channels}ch`);
        if (m.VideoRange && m.VideoRange !== 'SDR') parts.push(m.VideoRange);
        return parts.filter(Boolean).join(' ');
      }).join(' · ');
      return `<tr>
        <td>${esc(s.Name || 'Default')}<div class="muted" style="font-size:11px;word-break:break-all">${esc(s.Path)}</div></td>
        <td>${esc(s.Container || '—')}<div class="muted" style="font-size:11px">${fmtBytes(s.Size)}</div></td>
        <td class="muted" style="max-width:260px">${esc(streams)}</td>
        <td style="white-space:nowrap">
          <button class="primary" data-action="play-local" data-item="${esc(it.Id)}" data-source="${esc(s.Id)}">播放</button>
          <button data-action="playlist" data-item="${esc(it.Id)}" data-source="${esc(s.Id)}">播放列表</button>
          <button data-action="copy" data-path="${esc(s.Path)}">复制直链</button>
          <button data-action="play" data-path="${esc(s.Path)}" data-container="${esc(s.Container)}">网页播放</button>
        </td>
      </tr>`;
    }).join('') || '<tr><td colspan="4" class="muted">没有片源</td></tr>';

    const playLine = ud.Played
      ? '<span class="badge ok">已看</span>'
      : ud.PlaybackPositionTicks
        ? `<span class="badge warn">看到 ${fmtTicks(ud.PlaybackPositionTicks)}</span>`
        : '<span class="badge">未看</span>';

    const links = [];
    if (it.SeriesId) links.push(`<a href="#/item/${encodeURIComponent(it.SeriesId)}">所属剧集</a>`);
    if (it.SeasonId) links.push(`<a href="#/item/${encodeURIComponent(it.SeasonId)}">所属季</a>`);

    const posterTag = it.ImageTags && it.ImageTags.Primary;
    const posterHTML = posterTag
      ? `<img class="detail-poster" alt="" src="/Items/${esc(it.Id)}/Images/Primary?tag=${esc(posterTag)}&maxWidth=700">`
      : '';
    const logoTag = it.ImageTags && it.ImageTags.Logo;
    const logoHTML = logoTag
      ? `<img class="detail-logo" alt="" src="/Items/${esc(it.Id)}/Images/Logo?tag=${esc(logoTag)}">`
      : '';

    $('#view').innerHTML = `
      <div class="row" style="margin-bottom:14px">
        <button data-action="back">← 返回</button>
        <span class="muted">${links.join(' · ')}</span>
      </div>

      <div class="panel">
        <div class="detail-head">
          ${posterHTML}
          <div class="detail-body">
            ${logoHTML}
            <h3>${esc(it.Name)}</h3>
            ${it.OriginalTitle && it.OriginalTitle !== it.Name
              ? `<div class="muted" style="margin:-6px 0 8px">${esc(it.OriginalTitle)}</div>` : ''}
            <div class="kv">
              <div><b>类型</b><span>${TYPE_LABEL[it.Type] || it.Type}</span></div>
              <div><b>年份</b><span>${it.ProductionYear || '—'}</span></div>
              <div><b>TMDB</b><span>${esc(it.TmdbId || '—')}</span></div>
              <div><b>时长</b><span>${it.RunTimeTicks ? fmtTicks(it.RunTimeTicks) : '未知'}</span></div>
              ${it.PremiereDate ? `<div><b>首播</b><span>${esc(it.PremiereDate)}</span></div>` : ''}
              ${it.CommunityRating ? `<div><b>评分</b><span>${it.CommunityRating.toFixed(1)}</span></div>` : ''}
              <div><b>状态</b><span>${playLine}${ud.IsFavorite ? ' <span class="badge">★ 已收藏</span>' : ''}</span></div>
              ${it.Overview ? `<div><b>简介</b><span>${esc(it.Overview)}</span></div>` : ''}
            </div>
            <div class="row" style="margin-top:14px">
              ${sources.length ? `<button class="primary" data-action="play-local"
                data-item="${esc(it.Id)}">▶ 播放</button>` : ''}
              <button data-action="toggle-played" data-id="${esc(it.Id)}" data-played="${ud.Played ? '1' : '0'}">
                ${ud.Played ? '标记未看' : '标记已看'}</button>
              <button data-action="toggle-fav" data-id="${esc(it.Id)}" data-fav="${ud.IsFavorite ? '1' : '0'}">
                ${ud.IsFavorite ? '取消收藏' : '收藏'}</button>
            </div>
          </div>
        </div>
      </div>

      <div class="panel">
        <h3>片源</h3>
        <div class="row" style="margin-bottom:8px;align-items:center">
          <span class="muted">本地播放器</span>
          <select id="localPlayer" style="max-width:200px">
            ${LOCAL_PLAYERS.map(([k, l]) =>
              `<option value="${k}" ${curPlayer === k ? 'selected' : ''}>${l}</option>`).join('')}
          </select>
          <span class="muted">选择会记在这台设备；点「播放」直接唤起，不生效就用「播放列表」</span>
        </div>
        <table class="sources">
          <thead><tr><th>名称 / 直链</th><th>容器 / 大小</th><th>流</th><th></th></tr></thead>
          <tbody>${sourceRows}</tbody>
        </table>
      </div>

      <div id="playerSlot"></div>
      ${children}`;
  }

  function renderChildren(title, items) {
    if (!items.length) return '';
    return `<div class="panel">
      <h3>${title}</h3>
      <div class="grid">${items.map(itemCard).join('')}</div>
    </div>`;
  }

  function showPlayer(url, container) {
    const slot = $('#playerSlot');
    if (!slot) return;
    const ok = ['mp4', 'm4v', 'webm'].includes(String(container || '').toLowerCase());
    slot.innerHTML = `<div class="panel player-wrap">
      <h3>浏览器播放</h3>
      ${ok ? '' : `<p class="muted">容器 ${esc(container || '未知')} 浏览器多半播不了（服务端不转码）。
        播不出来就点「复制直链」，交给 VLC / PotPlayer。</p>`}
      <video controls autoplay src="${esc(url)}"></video>
    </div>`;
    slot.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }

  async function copyText(text) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // http 页面没有 clipboard API，退回 execCommand
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

  // 唤起本地播放器。直接跳协议，不碰剪贴板；协议没注册时用「播放列表」回退。
  async function playWithLocalPlayer(itemId, sourceId) {
    const sel = $('#localPlayer');
    const player = (sel && sel.value) || localStorage.getItem('lime.localPlayer.v2') || 'potplayer';
    localStorage.setItem('lime.localPlayer.v2', player);
    let target = `/web/api/play-target?itemId=${encodeURIComponent(itemId)}` +
      `&player=${encodeURIComponent(player)}`;
    if (sourceId) target += `&sourceId=${encodeURIComponent(sourceId)}`;
    const r = await api(target);
    window.location.href = r.ProtocolUrl;
    setTimeout(() => toast(`正在用 ${r.PlayerLabel} 打开；没反应就点「播放列表」`), 1200);
  }

  async function downloadPlaylist(itemId, sourceId) {
    const sel = $('#localPlayer');
    const player = (sel && sel.value) || localStorage.getItem('lime.localPlayer.v2') || 'potplayer';
    localStorage.setItem('lime.localPlayer.v2', player);
    const r = await api(`/web/api/play-target?itemId=${encodeURIComponent(itemId)}` +
      `&sourceId=${encodeURIComponent(sourceId)}&player=${encodeURIComponent(player)}`);
    window.location.href = r.M3uUrl;
  }

  // ---------- 用户 ----------
  const RATING_LABELS = {
    0: '不限制',
    1: '1 级 · G/TV-Y',
    2: '2 级 · PG',
    3: '3 级 · PG-13/TV-14',
    4: '4 级 · R/15',
    5: '5 级 · 18+/TV-MA',
  };

  function ratingLabel(v) { return RATING_LABELS[v] || RATING_LABELS[0]; }

  function ratingOptions(current) {
    return Object.entries(RATING_LABELS).map(([v, label]) =>
      `<option value="${v}"${Number(current) === Number(v) ? ' selected' : ''}>${esc(label)}</option>`
    ).join('');
  }

  async function renderUsers() {
    $('#crumb').textContent = '用户';
    const admin = isAdmin();
    const all = await api('/Users');
    const users = admin ? all : all.filter((u) => u.Id === state.user.Id);

    const rows = users.map((u) => {
      const policy = u.Policy || {};
      const self = u.Id === state.user.Id;
      const lastLogin = u.LastLoginDate
        ? new Date(u.LastLoginDate).toLocaleString('zh-CN', { hour12: false }) : '—';
      const maxRating = policy.MaxParentalRating || 0;
      const ratingCell = admin && !policy.IsAdministrator
        ? `<select data-action="set-rating" data-id="${esc(u.Id)}">${ratingOptions(maxRating)}</select>`
        : `<span class="muted">${maxRating ? ratingLabel(maxRating) : '不限制'}</span>`;
      return `<tr>
        <td>${esc(u.Name)}${self ? ' <span class="badge">当前</span>' : ''}</td>
        <td>${policy.IsAdministrator ? '<span class="badge ok">管理员</span>' : '<span class="badge">普通用户</span>'}</td>
        <td>${policy.IsDisabled ? '<span class="badge warn">已禁用</span>' : '<span class="badge ok">正常</span>'}</td>
        <td>${ratingCell}</td>
        <td class="muted">${esc(lastLogin)}</td>
        <td style="white-space:nowrap">
          <button data-action="rename-user" data-id="${esc(u.Id)}" data-name="${esc(u.Name)}">重命名</button>
          <button data-action="reset-pw" data-id="${esc(u.Id)}" data-name="${esc(u.Name)}">
            ${self ? '修改密码' : '重置密码'}</button>
          ${admin && !policy.IsAdministrator ? `
            <button data-action="user-libs" data-id="${esc(u.Id)}" data-name="${esc(u.Name)}">库权限</button>` : ''}
          ${admin && !self ? `
            <button data-action="toggle-disabled" data-id="${esc(u.Id)}" data-disabled="${policy.IsDisabled ? '1' : '0'}">
              ${policy.IsDisabled ? '启用' : '禁用'}</button>
            <button data-action="delete-user" data-id="${esc(u.Id)}" data-name="${esc(u.Name)}">删除</button>` : ''}
        </td>
      </tr>`;
    }).join('') || '<tr><td colspan="6" class="muted">没有用户</td></tr>';

    const createPanel = admin ? `
      <div class="panel">
        <h3>新建用户</h3>
        <div class="row">
          <input id="newUserName" placeholder="用户名" style="max-width:180px">
          <input id="newUserPass" type="password" placeholder="密码（可留空）" style="max-width:220px">
          <button data-action="create-user">创建</button>
        </div>
        <p class="muted" style="margin-bottom:0">普通用户可以浏览和播放，不能改配置、管理用户或生成 API Key。</p>
      </div>` : `
      <div class="panel"><p class="muted" style="margin:0">普通用户只能修改自己的密码和用户名。</p></div>`;

    $('#view').innerHTML = `${createPanel}
      <div class="panel">
        <h3>用户</h3>
        <table>
          <thead><tr><th>用户名</th><th>权限</th><th>状态</th><th>分级限制</th><th>最后登录</th><th></th></tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>`;
  }

  // ---------- 日志 ----------
  async function renderLogs() {
    $('#crumb').textContent = '日志';
    const data = await api('/web/api/logs?limit=300');
    const rows = (data.Entries || []).map((e) => {
      const attrs = Object.entries(e.Attrs || {}).map(([k, v]) => `${k}=${v}`).join(' ');
      return `<tr>
        <td class="muted" style="white-space:nowrap">${e.Time ? new Date(e.Time).toLocaleString('zh-CN', { hour12: false }) : '—'}</td>
        <td>${esc(e.Level || '')}</td>
        <td>${esc(e.Message || '')}${attrs ? `<div class="muted" style="font-size:11px;word-break:break-all">${esc(attrs)}</div>` : ''}</td>
      </tr>`;
    }).join('') || '<tr><td colspan="3" class="muted">还没有日志</td></tr>';

    $('#view').innerHTML = `<div class="panel">
      <h3>运行日志（最近 300 条）</h3>
      <div class="row" style="margin-bottom:10px">
        <button data-action="refresh-logs">刷新</button>
        <button data-action="export-logs">导出日志</button>
        <span class="muted">内存里只保留最近 1000 条；完整日志在 docker logs</span>
      </div>
      <table>
        <thead><tr><th>时间</th><th>级别</th><th>内容</th></tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
  }

  // ---------- 播放记录 ----------
  async function renderHistory() {
    $('#crumb').textContent = '播放记录';
    const list = await api('/web/api/playback-history?limit=300');
    const label = { start: '开始', pause: '暂停', unpause: '继续', stop: '停止' };
    const rows = (list || []).map((h) => `
      <tr>
        <td class="muted" style="white-space:nowrap">${h.CreatedAt ? new Date(h.CreatedAt * 1000).toLocaleString('zh-CN', { hour12: false }) : '—'}</td>
        <td>${esc(h.UserName || '—')}</td>
        <td>${esc(h.DeviceName || '—')}</td>
        <td>${esc(h.ItemName || '—')}<div class="muted" style="font-size:11px">${esc(h.ItemType || '')}</div></td>
        <td>${esc(label[h.Event] || h.Event)}</td>
        <td>${h.PositionTicks ? fmtTicks(h.PositionTicks) : '—'}</td>
      </tr>`).join('') || '<tr><td colspan="6" class="muted">最近一周没有播放记录</td></tr>';

    $('#view').innerHTML = `<div class="panel">
      <h3>播放记录（最近 7 天，最多 300 条）</h3>
      <table>
        <thead><tr><th>时间</th><th>用户</th><th>设备</th><th>条目</th><th>事件</th><th>进度</th></tr></thead>
        <tbody>${rows}</tbody>
      </table>
    </div>`;
  }

  // ---------- API Key ----------
  async function renderKeys() {
    $('#crumb').textContent = 'API Key';
    let keys;
    try {
      keys = await api('/Auth/Keys');
    } catch (e) {
      $('#view').innerHTML = `<div class="empty">${esc(e.message)}<br>只有管理员可以管理 API Key。</div>`;
      return;
    }

    const rows = (keys || []).map((k) => `
      <tr>
        <td>${esc(k.AppName)}</td>
        <td class="muted" style="font-family:monospace">${esc(String(k.AccessToken).slice(0, 8))}…</td>
        <td class="muted">${k.DateCreated ? new Date(k.DateCreated).toLocaleString('zh-CN', { hour12: false }) : '—'}</td>
        <td style="white-space:nowrap">
          <button data-action="copy-key" data-key="${esc(k.AccessToken)}">复制</button>
          <button data-action="delete-key" data-key="${esc(k.AccessToken)}">删除</button>
        </td>
      </tr>`).join('') || '<tr><td colspan="4" class="muted">还没有 API Key</td></tr>';

    $('#view').innerHTML = `
      <div class="panel">
        <h3>生成 API Key</h3>
        <div class="row">
          <input id="keyApp" placeholder="用途，如 Infuse / 脚本" style="max-width:240px">
          <button data-action="create-key">生成</button>
          <span class="muted">生成后可直接作为 <code>X-Emby-Token</code> 或 <code>?api_key=</code> 使用</span>
        </div>
      </div>
      <div class="panel">
        <h3>现有 Key</h3>
        <table>
          <thead><tr><th>用途</th><th>Token</th><th>创建时间</th><th></th></tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>`;
  }

  // ---------- 系统 ----------
  async function renderSystem() {
    $('#crumb').textContent = '系统';
    const [info, cfg, scan, settings, backdrops, posters, failures, stats] = await Promise.all([
      api('/System/Info'),
      api('/System/Configuration'),
      api('/web/api/scan'),
      api('/web/api/settings'),
      api('/web/api/images/backdrops'),
      api('/web/api/images/posters'),
      api('/web/api/scrape/failures?limit=1'),
      api('/web/api/stats'),
    ]);
    const fc = (k) => (failures.Counts && failures.Counts[k]) || 0;
    const infoRows = [
      ['服务器名', info.ServerName],
      ['LiMe 版本', stats.ProductVersion || 'dev'],
      ['Jellyfin 兼容版本', info.Version],
      ['服务器 ID', info.Id],
      ['地址', info.LocalAddress],
      ['数据目录', info.ProgramDataPath],
      ['架构', info.SystemArchitecture],
    ].map(([k, v]) => `<div><b>${k}</b><span>${esc(v || '—')}</span></div>`).join('');

    const cfgRows = Object.entries(cfg || {}).map(([k, v]) =>
      `<div><b>${esc(k)}</b><span>${esc(Array.isArray(v) ? v.join(', ') : v)}</span></div>`).join('');

    const scanRows = [
      ['状态', scan.Running ? '扫描中' : '空闲'],
      ['上次开始', fmtUnix(scan.LastStarted)],
      ['上次结束', fmtUnix(scan.LastFinished)],
      ['上次条数', String(scan.LastCount || 0)],
      ['上次错误', scan.LastError || '—'],
    ].map(([k, v]) => `<div><b>${k}</b><span>${esc(v)}</span></div>`).join('');

    // 单次刮削时长上限：预设常用值，非预设值（环境变量配置的）也原样显示。
    const curTimeout = settings.ScrapeTimeoutSeconds || 0;
    const presetTimeouts = [3600, 10800, 21600, 43200, 86400, 172800, 0];
    let timeoutOptions = presetTimeouts.map((v) =>
      `<option value="${v}" ${curTimeout === v ? 'selected' : ''}>${scrapeTimeoutLabel(v)}</option>`);
    if (!presetTimeouts.includes(curTimeout)) {
      timeoutOptions.unshift(`<option value="${curTimeout}" selected>${scrapeTimeoutLabel(curTimeout)}（当前）</option>`);
    }

    $('#view').innerHTML = `
      <div class="panel"><h3>服务器</h3><div class="kv">${infoRows}</div></div>
      <div class="panel"><h3>扫描</h3><div class="kv">${scanRows}</div>
        <div class="row" style="margin-top:12px"><button data-action="scan">立即全量扫描</button></div></div>
      <div class="panel"><h3>TMDB 刮削</h3>
        <div class="kv">
          <div><b>API Key</b><span>${settings.TMDBKeyCount
            ? `${settings.TMDBKeyCount} 个：${esc((settings.TMDBKeyHints || []).join('、'))}`
            : '未配置'}</span></div>
          <div><b>代理</b><span>${esc(settings.TMDBProxy || '—')}</span></div>
          <div><b>状态</b><span>${settings.ScrapeEnabled ? '已启用' : '未启用'}</span></div>
          <div><b>单次上限</b><span>${scrapeTimeoutLabel(curTimeout)}</span></div>
        </div>
        <div class="row" style="margin-top:12px">
          <input id="tmdbKey" placeholder="添加一个 API Key（留空不改）" style="max-width:320px">
          <input id="tmdbProxy" placeholder="代理，如 http://192.168.5.133:7890"
            style="max-width:320px" value="${esc(settings.TMDBProxy || '')}">
          <button class="primary" data-action="save-settings">保存</button>
          ${settings.TMDBKeyCount ? '<button data-action="clear-tmdb-keys">清空 Key</button>' : ''}
        </div>
        <div class="row" style="margin-top:12px">
          <select id="scrapeTimeout" style="max-width:160px">${timeoutOptions.join('')}</select>
          <button class="primary" data-action="save-settings">保存</button>
          <span class="muted">单次刮削上限，到点自动暂停，剩余条目下次继续；选「不限」则一次跑完</span>
        </div>
        <p class="muted" style="margin:8px 0 0">支持多个 Key，刮削时自动轮询；遇到 429 会换下一个。</p>
      </div>
      <div class="panel"><h3>定时任务</h3>
        <div class="kv">
          <div><b>每日增量扫描</b><span>${settings.ScanDailyAt ? esc(settings.ScanDailyAt) : '未启用'}</span></div>
          <div><b>每日刮削</b><span>${settings.ScrapeDailyAt ? esc(settings.ScrapeDailyAt) : '未启用'}</span></div>
          <div><b>数据库备份</b><span>${settings.BackupDailyAt ? `每天 ${esc(settings.BackupDailyAt)}，保留 ${settings.BackupKeep} 份` : '未启用'}</span></div>
        </div>
        <div class="row" style="margin-top:12px">
          <input id="scanDailyAt" placeholder="扫描 HH:MM，留空关闭" style="max-width:200px"
            value="${esc(settings.ScanDailyAt || '')}">
          <input id="scrapeDailyAt" placeholder="刮削 HH:MM，留空关闭" style="max-width:200px"
            value="${esc(settings.ScrapeDailyAt || '')}">
          <input id="backupDailyAt" placeholder="备份 HH:MM，off=关闭" style="max-width:200px"
            value="${esc(settings.BackupDailyAt || 'off')}">
          <input id="backupKeep" type="number" min="1" max="30" placeholder="保留份数" style="max-width:120px"
            value="${settings.BackupKeep || 3}">
          <button class="primary" data-action="save-settings">保存</button>
        </div>
        <div class="row" style="margin-top:12px">
          <button data-action="backup-now">立即备份</button>
        </div>
        <p class="muted" style="margin:8px 0 0">扫描和刮削互不联动，按各自时间独立执行；数据库每天自动备份，保留最近几份。</p>
      </div>
      <div class="panel"><h3>实时监控</h3>
        <div class="row">
          <label class="check-field">
            <input type="checkbox" id="realtimeMonitor" ${settings.RealtimeMonitor ? 'checked' : ''}>
            <span>实时监控媒体库目录</span>
          </label>
          <button class="primary" data-action="save-settings">保存</button>
        </div>
        <p class="muted" style="margin:8px 0 0">新 .strm 出现后立即增量解析并补刮削，等价于 Emby 的实时监控；关闭后只靠定时或手动扫描。</p>
      </div>
      <div class="panel"><h3>播放预热</h3>
        <div class="kv">
          <div><b>状态</b><span>${settings.ProbeWarmup ? '已启用' : '已关闭'}</span></div>
          <div><b>范围</b><span>${probeScopeLabel(settings.ProbeScope)}</span></div>
          <div><b>时间窗</b><span>${settings.ProbeWindow === 'all' ? '全天' : esc(settings.ProbeWindow)}</span></div>
          <div><b>单次最长</b><span>${settings.ProbeMaxRun === '0' ? '不限' : esc(settings.ProbeMaxRun)}</span></div>
          <div><b>待探测</b><span>收藏 ${settings.ProbePendingFavorites || 0} / 收藏+在追 ${settings.ProbePendingWatching || 0} / 全部 ${settings.ProbePendingTotal || 0}</span></div>
        </div>
        <div class="row" style="margin-top:12px">
          <label class="check-field">
            <input type="checkbox" id="probeWarmup" ${settings.ProbeWarmup ? 'checked' : ''}>
            <span>启用预热</span>
          </label>
          <select id="probeScope">
            <option value="watching" ${settings.ProbeScope === 'watching' ? 'selected' : ''}>收藏 + 在追（推荐）</option>
            <option value="favorites" ${settings.ProbeScope === 'favorites' ? 'selected' : ''}>只预热收藏</option>
            <option value="recent" ${settings.ProbeScope === 'recent' ? 'selected' : ''}>最近 7 天 + 观看进度</option>
            <option value="all" ${settings.ProbeScope === 'all' ? 'selected' : ''}>全库</option>
          </select>
          <input id="probeWindow" placeholder="03:00-06:00，all=全天" style="max-width:220px"
            value="${esc(settings.ProbeWindow || 'all')}">
          <input id="probeMaxRun" placeholder="单次最长，如 2h、0=不限" style="max-width:180px"
            value="${esc(settings.ProbeMaxRun || '2h')}">
          <button class="primary" data-action="save-settings">保存</button>
        </div>
        <p class="muted" style="margin:8px 0 0">后台预探测片源并缓存轨道信息，之后起播不再等 ffprobe。全库提取建议放凌晨窗口慢慢跑，随时可关。</p>
      </div>
      <div class="panel"><h3>片头片尾</h3>
        <div class="kv">
          <div><b>状态</b><span>${settings.SegmentExtract ? '已启用' : '已关闭'}</span></div>
          <div><b>回填范围</b><span>${probeScopeLabel(settings.SegmentScope)}</span></div>
          <div><b>自动触发</b><span>${settings.SegmentAutoAfter === '0' ? '关闭' : '单集观看 ' + esc(settings.SegmentAutoAfter) + ' 后补齐整剧'}</span></div>
          <div><b>待分析</b><span>${settings.SegmentPending || 0} 个条目</span></div>
        </div>
        <div class="row" style="margin-top:12px">
          <label class="check-field">
            <input type="checkbox" id="segmentExtract" ${settings.SegmentExtract ? 'checked' : ''}>
            <span>启用提取</span>
          </label>
          <select id="segmentScope">
            <option value="watching" ${settings.SegmentScope === 'watching' ? 'selected' : ''}>收藏 + 在追（推荐）</option>
            <option value="favorites" ${settings.SegmentScope === 'favorites' ? 'selected' : ''}>只回填收藏</option>
            <option value="recent" ${settings.SegmentScope === 'recent' ? 'selected' : ''}>最近 7 天 + 观看进度</option>
            <option value="all" ${settings.SegmentScope === 'all' ? 'selected' : ''}>全库</option>
          </select>
          <input id="segmentAutoAfter" placeholder="自动触发，如 5m、0=关闭" style="max-width:200px"
            value="${esc(settings.SegmentAutoAfter || '5m')}">
          <button class="primary" data-action="save-settings">保存</button>
          <button data-action="scan-segments">立即提取一批</button>
        </div>
        <p class="muted" style="margin:8px 0 0">章节随预热探测顺手获取，零额外流量；没有标准章节的剧之后再接音频指纹。单集看到设定时长后自动补齐整部剧，避免冷开场/片头位置每集不同导致的漏检。</p>
      </div>
      <div class="panel"><h3>背景图清晰度</h3>
        <div class="kv">
          <div><b>状态</b><span>${backdrops.Running
            ? `回填中 ${backdrops.Processed || 0}/${backdrops.Total || '?'}`
            : (backdrops.LastFinished
              ? `上次完成 ${fmtUnix(backdrops.LastFinished)}，处理 ${backdrops.Processed || 0} 张`
              : '未运行')}</span></div>
          ${backdrops.LastError ? `<div><b>上次错误</b><span>${esc(backdrops.LastError)}</span></div>` : ''}
        </div>
        <div class="row" style="margin-top:12px">
          <button data-action="refresh-backdrops" ${backdrops.Running ? 'disabled' : ''}>重新下载背景图（原图）</button>
        </div>
        <p class="muted" style="margin:8px 0 0">按 TMDB 原图重新下载电影/剧集背景图，替换被压到 500 宽的旧图。约 2.1 万张，预计多占 15GB；小时级任务，期间可正常播放。</p>
      </div>
      <div class="panel"><h3>海报清晰度</h3>
        <div class="kv">
          <div><b>状态</b><span>${posters.Running
            ? `回填中 ${posters.Processed || 0}/${posters.Total || '?'}`
            : (posters.LastFinished
              ? `上次完成 ${fmtUnix(posters.LastFinished)}，处理 ${posters.Processed || 0} 张`
              : '未运行')}</span></div>
          ${posters.LastError ? `<div><b>上次错误</b><span>${esc(posters.LastError)}</span></div>` : ''}
        </div>
        <div class="row" style="margin-top:12px">
          <button data-action="refresh-posters" ${posters.Running ? 'disabled' : ''}>重新下载海报（w780）</button>
        </div>
        <p class="muted" style="margin:8px 0 0">把电影/剧集海报从 w500 升到 w780，换的是同一张图的高清版。约 2.2 万张，预计多占约 3GB；不会覆盖面板里手工上传的封面。</p>
      </div>
      <div class="panel"><h3>刮削失败报告</h3>
        <div class="kv">
          <div><b>TMDB ID 失效</b><span>${fc('not_found')} 条</span></div>
          <div><b>接口错误</b><span>${fc('api_error')} 条</span></div>
          <div><b>海报下载失败</b><span>${fc('poster')} 条</span></div>
          <div><b>背景图下载失败</b><span>${fc('backdrop')} 条</span></div>
          <div><b>标题图下载失败</b><span>${fc('logo')} 条</span></div>
        </div>
        <div class="kv" style="margin-top:8px">
          <div><b>存量：没 TMDB ID</b><span>${failures.Health.NoTmdbId || 0}</span></div>
          <div><b>存量：有 ID 但没刮过</b><span>${failures.Health.Unscraped || 0}</span></div>
          <div><b>存量：缺海报 / 缺背景图</b><span>${failures.Health.MissingPoster || 0} / ${failures.Health.MissingBackdrop || 0}</span></div>
        </div>
        <div class="row" style="margin-top:12px">
          <button data-action="download-failures">下载 CSV</button>
        </div>
        <p class="muted" style="margin:8px 0 0">刮削/回填过程中失败过的条目会记在这里，修好后自动消失。「缺背景图」多半是 TMDB 本身只有海报、没有背景图，不是下载失败。</p>
      </div>
      <div class="panel"><h3>配置</h3><div class="kv">${cfgRows}</div></div>`;
  }

  // ---------- 通知 ----------
  async function renderNotify() {
    $('#crumb').textContent = '通知';
    const settings = await api('/web/api/settings');

    // TGEvents：'' 或 all 表示全开，none 表示全关。
    const tgEvents = (settings.TGEvents || '').split(',').filter(Boolean);
    const tgAllOn = !settings.TGEvents || settings.TGEvents === 'all';
    const tgChecked = (e) => (tgAllOn || tgEvents.includes(e)) ? 'checked' : '';

    $('#view').innerHTML = `
      <div class="panel"><h3>Telegram 播放通知</h3>
        <div class="kv">
          <div><b>状态</b><span>${settings.TGEnabled
            ? (settings.TGReady ? '已启用' : '已启用，但 Token / Chat ID 不完整')
            : '已关闭'}</span></div>
          <div><b>Bot Token</b><span>${settings.TGBotTokenSet ? esc(settings.TGBotTokenHint) : '未配置'}</span></div>
          <div><b>Chat ID</b><span>${esc(settings.TGChatID || '未配置')}</span></div>
          <div><b>推送事件</b><span>${tgEventsLabel(settings.TGEvents)}</span></div>
        </div>
        <div class="row" style="margin-top:12px">
          <input id="tgToken" type="password" placeholder="Bot Token（留空不改）" autocomplete="off" style="max-width:340px">
          <input id="tgChatId" placeholder="Chat ID，群组为负数" style="max-width:220px"
            value="${esc(settings.TGChatID || '')}">
          <input id="tgProxy" placeholder="代理，如 http://192.168.5.133:7890" style="max-width:300px"
            value="${esc(settings.TGProxy || '')}">
        </div>
        <div class="row" style="margin-top:12px">
          <label class="check-field">
            <input type="checkbox" id="tgEnabled" ${settings.TGEnabled ? 'checked' : ''}>
            <span>启用通知</span>
          </label>
          <label class="check-field"><input type="checkbox" class="tg-event" value="start" ${tgChecked('start')}><span>开始</span></label>
          <label class="check-field"><input type="checkbox" class="tg-event" value="pause" ${tgChecked('pause')}><span>暂停</span></label>
          <label class="check-field"><input type="checkbox" class="tg-event" value="resume" ${tgChecked('resume')}><span>继续</span></label>
          <label class="check-field"><input type="checkbox" class="tg-event" value="stop" ${tgChecked('stop')}><span>停止</span></label>
          <button class="primary" data-action="save-settings">保存</button>
          <button data-action="tg-test">发送测试消息</button>
        </div>
        <p class="muted" style="margin:8px 0 0">把播放、暂停、继续、停止推送到 Telegram；消息包含用户、设备、片名和进度。Token 找 @BotFather 要，Chat ID 找 @userinfobot。NAS 直连 api.telegram.org 不通时填代理。</p>
      </div>
      <div class="panel"><h3>消息预览</h3>
        <pre style="white-space:pre-wrap;line-height:1.7;margin:0">▶️ 开始播放
👤 Lime · 客厅电视 (Android TV)
📺 三体 S01E03 · 第 3 集
⏱ 12:34 / 45:30 (27%)
🕐 11-10 21:34</pre>
      </div>`;
  }

  function probeScopeLabel(scope) {
    const labels = { watching: '收藏 + 在追', favorites: '只预热收藏', recent: '最近 7 天 + 观看进度', all: '全库' };
    return labels[scope] || esc(scope || '—');
  }

  function scrapeTimeoutLabel(sec) {
    if (!sec) return '不限';
    if (sec % 3600 === 0) return `${sec / 3600} 小时`;
    if (sec % 60 === 0) return `${sec / 60} 分钟`;
    return `${sec} 秒`;
  }

  function tgEventsLabel(raw) {
    if (!raw || raw === 'all') return '全部事件';
    if (raw === 'none') return '未选择';
    const labels = { start: '开始', pause: '暂停', resume: '继续', stop: '停止' };
    return raw.split(',').filter(Boolean).map((e) => labels[e] || esc(e)).join('、');
  }

  // ---------- 动作 ----------
  async function handleAction(action, el) {
    switch (action) {
      case 'open-item':
        location.hash = '#/item/' + encodeURIComponent(el.dataset.id);
        break;
      case 'back':
        history.length > 1 ? history.back() : (location.hash = '#/overview');
        break;
      case 'save-settings': {
        // 同一套保存逻辑被多个页面复用，读不到的字段就跳过。
        const val = (sel) => {
          const el = $(sel);
          return el ? (el.value || '').trim() : undefined;
        };
        const body = {};
        const key = val('#tmdbKey');
        const proxy = val('#tmdbProxy');
        if (proxy !== undefined) body.Proxy = proxy;
        const scanDailyAt = val('#scanDailyAt');
        if (scanDailyAt !== undefined) body.ScanDailyAt = scanDailyAt;
        const scrapeDailyAt = val('#scrapeDailyAt');
        if (scrapeDailyAt !== undefined) body.ScrapeDailyAt = scrapeDailyAt;
        const scrapeTimeoutEl = $('#scrapeTimeout');
        if (scrapeTimeoutEl) body.ScrapeTimeoutSeconds = parseInt(scrapeTimeoutEl.value || '21600', 10);
        const realtimeEl = $('#realtimeMonitor');
        if (realtimeEl) body.RealtimeMonitor = realtimeEl.checked;
        const probeWarmupEl = $('#probeWarmup');
        if (probeWarmupEl) {
          body.ProbeWarmup = probeWarmupEl.checked;
          body.ProbeScope = $('#probeScope').value;
          body.ProbeWindow = ($('#probeWindow').value || '').trim();
          body.ProbeMaxRun = ($('#probeMaxRun').value || '').trim();
        }
        const segmentExtractEl = $('#segmentExtract');
        if (segmentExtractEl) {
          body.SegmentExtract = segmentExtractEl.checked;
          body.SegmentScope = $('#segmentScope').value;
          body.SegmentAutoAfter = ($('#segmentAutoAfter').value || '0').trim();
        }
        const backupAtEl = $('#backupDailyAt');
        if (backupAtEl) {
          body.BackupDailyAt = (backupAtEl.value || 'off').trim();
          body.BackupKeep = parseInt($('#backupKeep').value || '3', 10);
        }
        const tgEnabledEl = $('#tgEnabled');
        if (tgEnabledEl) {
          body.TGEnabled = tgEnabledEl.checked;
          body.TGChatID = ($('#tgChatId').value || '').trim();
          body.TGProxy = ($('#tgProxy').value || '').trim();
          const tgToken = ($('#tgToken').value || '').trim();
          if (tgToken) body.TGBotToken = tgToken;
          const events = Array.from(document.querySelectorAll('.tg-event:checked')).map((el) => el.value);
          body.TGEvents = events.length === 4 ? 'all' : (events.length === 0 ? 'none' : events.join(','));
        }
        if (key) body.ApiKey = key;
        await api('/web/api/settings', { method: 'PUT', body: JSON.stringify(body) });
        toast('配置已保存');
        await route();
        break;
      }
      case 'clear-tmdb-keys': {
        if (!confirm('清空所有 TMDB API Key？')) break;
        await api('/web/api/settings', { method: 'PUT', body: JSON.stringify({ ClearKeys: true }) });
        toast('已清空 TMDB Key');
        await route();
        break;
      }
      case 'refresh-logs': {
        await renderLogs();
        break;
      }
      case 'scan-segments': {
        const r = await api('/web/api/segments/scan?limit=50', { method: 'POST' });
        toast(`已排队 ${r.Queued || 0} 个条目；完成后刷新页面看「待分析」数量`);
        break;
      }
      case 'export-logs': {
        const res = await fetch('/web/api/logs/export', {
          headers: state.token ? { 'X-Emby-Token': state.token } : {},
        });
        if (!res.ok) { toast('导出失败：HTTP ' + res.status); break; }
        const blob = await res.blob();
        const a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = 'lime-logs-' + new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-') + '.txt';
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(a.href), 5000);
        break;
      }
      case 'tg-test': {
        try {
          await api('/web/api/notify/test', { method: 'POST' });
          toast('测试消息已发送，去 Telegram 看看');
        } catch (e) {
          toast('发送失败：' + (e.message || e));
        }
        break;
      }
      case 'refresh-backdrops': {
        if (!confirm('重新下载约 2.1 万张背景图，预计多占 15GB，期间会持续请求 TMDB。确定开始？')) break;
        await api('/web/api/images/backdrops', { method: 'POST' });
        toast('已开始回填背景图，稍后刷新本页查看进度');
        setTimeout(route, 1500);
        break;
      }
      case 'refresh-posters': {
        if (!confirm('重新下载约 2.2 万张海报（w500 → w780），预计多占约 3GB。确定开始？')) break;
        await api('/web/api/images/posters', { method: 'POST' });
        toast('已开始回填海报，稍后刷新本页查看进度');
        setTimeout(route, 1500);
        break;
      }
      case 'download-failures': {
        const res = await fetch('/web/api/scrape/failures?format=csv', {
          headers: state.token ? { 'X-Emby-Token': state.token } : {},
        });
        if (!res.ok) { toast('下载失败：HTTP ' + res.status); break; }
        const blob = await res.blob();
        const a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = 'scrape-failures-' + new Date().toISOString().slice(0, 10) + '.csv';
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(a.href), 5000);
        break;
      }
      case 'backup-now': {
        const r = await api('/web/api/backup', { method: 'POST' });
        toast(`备份完成：${r.File}（${r.MB} MB）`);
        break;
      }
      case 'scan': {
        const r = await api('/web/api/scan', { method: 'POST' });
        toast('扫描已开始');
        if (state.stats) state.stats.Scan = r.Scan || state.stats.Scan;
        setTimeout(route, 800);
        break;
      }
      case 'scrape-avatars': {
        await api('/web/api/scrape?mode=avatars', { method: 'POST' });
        toast('已开始补演员头像');
        await route();
        break;
      }
      case 'scrape': {
        await api('/web/api/scrape', { method: 'POST' });
        toast('刮削已开始');
        setTimeout(route, 800);
        break;
      }
      case 'set-type':
        state.list.type = el.dataset.type;
        document.querySelectorAll('.tabs button').forEach((b) =>
          b.classList.toggle('active', b.dataset.type === el.dataset.type));
        state.list.items = [];
        state.list.total = 0;
        await fetchPage(true);
        break;
      case 'more':
        await fetchPage(false);
        break;
      case 'toggle-played': {
        const id = el.dataset.id;
        const on = el.dataset.played === '1';
        await api(`/Users/${encodeURIComponent(state.user.Id)}/PlayedItems/${encodeURIComponent(id)}`,
          { method: on ? 'DELETE' : 'POST' });
        toast(on ? '已标记为未看' : '已标记为已看');
        await route();
        break;
      }
      case 'toggle-fav': {
        const id = el.dataset.id;
        const on = el.dataset.fav === '1';
        await api(`/Users/${encodeURIComponent(state.user.Id)}/FavoriteItems/${encodeURIComponent(id)}`,
          { method: on ? 'DELETE' : 'POST' });
        toast(on ? '已取消收藏' : '已收藏');
        await route();
        break;
      }
      case 'save-cover-style': {
        await api('/web/api/library-covers', {
          method: 'POST',
          body: JSON.stringify({ Style: $('#coverStyle').value }),
        });
        toast('库封面样式已保存');
        await renderLibraries();
        break;
      }
      case 'use-auto-covers': {
        if (!confirm('把自定义封面备份为 *.bak，并改用自动生成的海报墙封面？')) break;
        const r = await api('/web/api/library-covers', {
          method: 'POST',
          body: JSON.stringify({ UseAuto: true }),
        });
        toast(`已备份 ${(r && r.BackedUp) || 0} 张自定义封面，后台重新生成中`);
        await renderLibraries();
        break;
      }
      case 'save-lib': {
        const name = ($('#libName').value || '').trim();
        const kind = $('#libKind').value;
        const paths = (state.libPaths || []).slice();
        if (!name) throw new Error('库名称不能为空');
        if (!paths.length) throw new Error('请先添加至少一个文件夹');
        if (state.libEdit) {
          await api(`/web/api/libraries/${encodeURIComponent(state.libEdit.ID)}`,
            { method: 'PUT', body: JSON.stringify({ Name: name, Paths: paths, Kind: kind }) });
          toast('已保存');
        } else {
          const lib = await api('/web/api/libraries',
            { method: 'POST', body: JSON.stringify({ Name: name, Paths: paths, Kind: kind }) });
          await api(`/web/api/libraries/${encodeURIComponent(lib.ID)}/scan`, { method: 'POST' });
          toast('媒体库已创建，开始扫描');
        }
        state.libEdit = null;
        state.libPaths = null;
        state.libDraft = null;
        await ensureStats();
        await route();
        break;
      }
      case 'open-dir-picker':
        await openDirPicker('/strm');
        break;
      case 'remove-lib-path':
        state.libPaths = (state.libPaths || []).filter((_, i) => i !== Number(el.dataset.idx));
        renderLibPathList();
        break;
      case 'edit-lib': {
        const lib = (state.libCache || []).find((l) => l.ID === el.dataset.id);
        if (!lib) break;
        state.libEdit = lib;
        state.libDraft = null;
        state.libPaths = (lib.Paths && lib.Paths.length ? lib.Paths : [lib.Path]).filter(Boolean).slice();
        await renderLibraries();
        break;
      }
      case 'cancel-lib':
        state.libEdit = null;
        state.libDraft = null;
        state.libPaths = null;
        await renderLibraries();
        break;
      case 'move-lib':
        await api(`/web/api/libraries/${encodeURIComponent(el.dataset.id)}/move?dir=${el.dataset.dir}`,
          { method: 'POST' });
        await ensureStats();
        await renderLibraries();
        break;
      case 'scan-lib':
        await api(`/web/api/libraries/${encodeURIComponent(el.dataset.id)}/scan`, { method: 'POST' });
        toast(`开始扫描：${el.dataset.name}`);
        setTimeout(route, 800);
        break;
      case 'delete-lib': {
        if (!confirm(`删除媒体库“${el.dataset.name}”？库内条目和播放记录会一起删除，不可恢复。`)) break;
        await api(`/web/api/libraries/${encodeURIComponent(el.dataset.id)}`, { method: 'DELETE' });
        toast('媒体库已删除');
        state.libEdit = null;
        await ensureStats();
        await route();
        break;
      }
      case 'create-user': {
        const name = ($('#newUserName').value || '').trim();
        const pw = $('#newUserPass').value || '';
        if (!name) throw new Error('用户名不能为空');
        await api('/Users/New', { method: 'POST', body: JSON.stringify({ Name: name, Password: pw }) });
        toast('用户已创建');
        await route();
        break;
      }
      case 'rename-user': {
        const name = prompt('新用户名：', el.dataset.name);
        if (name === null) break;
        const trimmed = name.trim();
        if (!trimmed) throw new Error('用户名不能为空');
        await api(`/Users/${encodeURIComponent(el.dataset.id)}`,
          { method: 'POST', body: JSON.stringify({ Name: trimmed }) });
        toast('用户名已修改');
        await route();
        break;
      }
      case 'reset-pw':
        openPwModal(el.dataset.id, el.dataset.name, el.dataset.id === state.user.Id);
        break;
      case 'user-libs':
        await openUserLibs(el.dataset.id, el.dataset.name);
        break;
      case 'toggle-disabled': {
        const wasDisabled = el.dataset.disabled === '1';
        await api(`/Users/${encodeURIComponent(el.dataset.id)}/Policy`,
          { method: 'POST', body: JSON.stringify({ IsDisabled: !wasDisabled }) });
        toast(wasDisabled ? '已启用' : '已禁用');
        await route();
        break;
      }
      case 'delete-user': {
        if (!confirm(`确定删除用户「${el.dataset.name}」？`)) break;
        await api(`/Users/${encodeURIComponent(el.dataset.id)}`, { method: 'DELETE' });
        toast('用户已删除');
        await route();
        break;
      }
      case 'create-key': {
        const app = (($('#keyApp') || {}).value || '').trim() || 'API Key';
        await api('/Auth/Keys?App=' + encodeURIComponent(app), { method: 'POST' });
        toast('API Key 已生成');
        await route();
        break;
      }
      case 'delete-key':
        await api('/Auth/Keys/' + encodeURIComponent(el.dataset.key), { method: 'DELETE' });
        toast('API Key 已删除');
        await route();
        break;
      case 'copy-key':
        await copyText(el.dataset.key);
        break;
      case 'copy':
        await copyText(el.dataset.path);
        break;
      case 'play-local':
        await playWithLocalPlayer(el.dataset.item, el.dataset.source);
        break;
      case 'playlist':
        await downloadPlaylist(el.dataset.item, el.dataset.source);
        break;
      case 'play':
        showPlayer(el.dataset.path, el.dataset.container);
        break;
    }
  }

  // ---------- 库权限弹窗 ----------
  let libAccessUser = null;

  async function openUserLibs(id, name) {
    libAccessUser = { id, name };
    const data = await api(`/web/api/users/${encodeURIComponent(id)}/libraries`);
    const libs = data.Libraries || [];
    const access = new Set(data.Access || []);
    const all = access.size === 0;
    $('#libAccessTitle').textContent = `媒体库权限：${name}`;
    $('#libAccessList').innerHTML = libs.map((l) => `
      <label class="access-row">
        <input type="checkbox" value="${esc(l.ID)}" ${all || access.has(l.ID) ? 'checked' : ''}>
        <span>${esc(l.Name)}</span>
      </label>`).join('') || '<span class="muted">还没有媒体库</span>';
    $('#libAccessModal').classList.remove('hidden');
  }

  async function saveUserLibs() {
    if (!libAccessUser) return;
    const boxes = Array.from(document.querySelectorAll('#libAccessList input[type=checkbox]'));
    const checked = boxes.filter((b) => b.checked).map((b) => b.value);
    // 全部勾选等价于不限制，后端用空数组表示默认全部。
    const ids = boxes.length && checked.length === boxes.length ? [] : checked;
    await api(`/web/api/users/${encodeURIComponent(libAccessUser.id)}/libraries`, {
      method: 'PUT',
      body: JSON.stringify({ LibraryIds: ids }),
    });
    $('#libAccessModal').classList.add('hidden');
    toast('库权限已保存');
  }

  // ---------- 密码弹窗 ----------
  let pwTarget = null;

  function openPwModal(id, name, self) {
    pwTarget = { id, name, self };
    $('#pwTitle').textContent = self ? '修改我的密码' : `重置「${name}」的密码`;
    $('#pwCurrent').classList.toggle('hidden', !self);
    $('#pwCurrent').value = '';
    $('#pwNew').value = '';
    $('#pwConfirm').value = '';
    $('#pwErr').classList.add('hidden');
    $('#pwModal').classList.remove('hidden');
    (self ? $('#pwCurrent') : $('#pwNew')).focus();
  }

  function closePwModal() {
    pwTarget = null;
    $('#pwModal').classList.add('hidden');
  }

  async function submitPwModal(ev) {
    ev.preventDefault();
    if (!pwTarget) return;
    const err = $('#pwErr');
    err.classList.add('hidden');

    const current = $('#pwCurrent').value;
    const next = $('#pwNew').value;
    const confirm = $('#pwConfirm').value;
    if (next !== confirm) {
      err.textContent = '两次输入的新密码不一致';
      err.classList.remove('hidden');
      return;
    }

    try {
      const body = pwTarget.self
        ? { CurrentPw: current, NewPw: next }
        : { NewPw: next, ResetPassword: true };
      await api(`/Users/${encodeURIComponent(pwTarget.id)}/Password`,
        { method: 'POST', body: JSON.stringify(body) });
      const msg = pwTarget.self ? '密码已修改' : '密码已重置';
      closePwModal();
      toast(msg);
    } catch (e) {
      const text = String(e.message || '');
      err.textContent = text.includes('current password') ? '当前密码不正确' : text;
      err.classList.remove('hidden');
    }
  }

  async function setUserRating(id, rating) {
    await api(`/Users/${encodeURIComponent(id)}/Policy`, {
      method: 'POST',
      body: JSON.stringify({ MaxParentalRating: rating }),
    });
    toast(rating ? `分级限制已设为 ${ratingLabel(rating)}` : '分级限制已关闭');
  }

  // ---------- 事件绑定 ----------
  document.addEventListener('click', (e) => {
    const el = e.target.closest('[data-action]');
    if (!el) return;
    if (el.tagName === 'SELECT') return; // select 交给 change 处理
    e.preventDefault();
    handleAction(el.dataset.action, el).catch((err) => toast(err.message, true));
  });

  document.addEventListener('change', (e) => {
    const el = e.target.closest('select[data-action="set-rating"]');
    if (!el) return;
    setUserRating(el.dataset.id, Number(el.value)).catch((err) => toast(err.message, true));
  });

  window.addEventListener('hashchange', route);

  $('#loginForm').addEventListener('submit', doLogin);
  $('#themeBtn').addEventListener('click', () => {
    applyTheme(document.documentElement.dataset.theme === 'light' ? 'dark' : 'light');
  });
  $('#logout').addEventListener('click', logout);
  $('#pwForm').addEventListener('submit', submitPwModal);
  $('#pwCancel').addEventListener('click', closePwModal);
  $('#dirPick').addEventListener('click', () => {
    if (dirState && dirState.path) addLibPath(dirState.path);
  });
  $('#dirUp').addEventListener('click', async () => {
    if (!dirState) return;
    const parent = dirState.path.replace(/\/[^/]+\/?$/, '') || '/strm';
    dirState.path = parent.startsWith('/strm') ? parent : '/strm';
    await loadDirPicker();
  });
  $('#dirCancel').addEventListener('click', () => $('#dirModal').classList.add('hidden'));
  $('#dirModal').addEventListener('click', (e) => {
    if (e.target.id === 'dirModal') $('#dirModal').classList.add('hidden');
  });
  $('#libAccessCancel').addEventListener('click', () => $('#libAccessModal').classList.add('hidden'));
  $('#libAccessSave').addEventListener('click', () => {
    saveUserLibs().catch((err) => toast(err.message, true));
  });
  $('#libAccessModal').addEventListener('click', (e) => {
    if (e.target.id === 'libAccessModal') e.target.classList.add('hidden');
  });
  $('#scanBtn').addEventListener('click', () => {
    handleAction('scan', $('#scanBtn')).catch((err) => toast(err.message, true));
  });
  $('#search').addEventListener('keydown', (e) => {
    if (e.key !== 'Enter') return;
    const term = e.target.value.trim();
    if (!term) return;
    location.hash = '#/search/' + encodeURIComponent(term);
  });

  // 启动：有 token 先试，失效则回登录页
  (async () => {
    if (!state.token) {
      showLogin();
      return;
    }
    try {
      await enterApp();
    } catch {
      showLogin();
    }
  })();
})();
