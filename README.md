# LiMe

[![docker](https://github.com/Anboyyt/LiMe/actions/workflows/docker.yml/badge.svg)](https://github.com/Anboyyt/LiMe/actions/workflows/docker.yml)
[![Docker Pulls](https://img.shields.io/docker/pulls/lixxh/lime)](https://hub.docker.com/r/lixxh/lime)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Telegram Channel](https://img.shields.io/badge/Telegram-Channel-26A5E4?logo=telegram&logoColor=white)](https://t.me/Lime_see)
[![Telegram Chat](https://img.shields.io/badge/Telegram-Chat-229ED9?logo=telegram&logoColor=white)](https://t.me/Lime_seee)

一个用 Go 写的轻量媒体服务器，**兼容 Jellyfin 客户端**，专为「网盘 STRM + 302 直连」场景设计。

服务端不接触视频字节、不转码、不缓存 URL —— 播放时只回一个 `302`，让客户端直连云盘。

```
客户端 → LiMe → 302 → 115/123 CDN → 播放
```

LiMe 在服务端替客户端跟一跳 PanSave 的 302（`LIME_RESOLVE_REDIRECT`），
所以外网可以直接访问 LiMe，播放流量不必再绕经 PanSave 反代。

---

## 快速开始

预构建镜像由 GitHub Actions 发布，支持 `linux/amd64` 和 `linux/arm64`。

### 方式一：Docker Compose（推荐）

```bash
curl -fsSLO https://raw.githubusercontent.com/Anboyyt/LiMe/main/docker-compose.example.yml
mv docker-compose.example.yml docker-compose.yml
```

打开 `docker-compose.yml`，改两个地方：

```yaml
LIME_LIBRARIES: "电影|/media/电影|movies;剧集|/media/剧集|tvshows"
volumes:
  - /path/to/your/strm:/media:ro    # ← 你的 STRM 目录
```

然后启动：

```bash
docker compose up -d
```

### 方式二：docker run

```bash
docker run -d --name lime \
  --restart unless-stopped \
  -p 886:886 \
  -v /path/to/data:/data \
  -v /path/to/strm:/strm:ro \
  -e LIME_LIBRARIES="电影|/strm/电影|movies;剧集|/strm/剧集|tvshows" \
  -e LIME_ADMIN_USER=admin \
  -e LIME_ADMIN_PASSWORD=change-me \
  lixxh/lime:latest
```

### 方式三：从源码构建

```bash
git clone https://github.com/Anboyyt/LiMe.git
cd LiMe
LIME_VERSION=$(git describe --tags --always --dirty) \
  docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

本地构建必须显式传 `LIME_VERSION`（不传会报错），产物标签是
`lixxh/lime:local`，不会覆盖 `latest`；正式镜像由 CI 发布。

打开 `http://<服务器IP>:886` 就是媒体界面，用首次启动自动创建的账号登录；
运维面板在 `http://<服务器IP>:886/web/`。

> 首次启动会自动扫描 `LIME_LIBRARIES` 指定的目录。扫描大库时建议设置
> `LIME_SCAN_ON_START=false`，启动后在面板里手动触发。

## Web 界面

`/` 是自带的原生媒体界面，参考 Pelagica 的布局自己实现，**没有前端框架**：

- 首页：随机背景 + 继续观看 + 最近添加
- 媒体库：海报网格、按名称/年份/最近添加/最新一集排序、分页
- 详情：海报/背景、简介、类型、演员、季与单集（带剧照）、片源直链、外挂字幕、标记已看/收藏
- **相似推荐**：详情页底部按「类型交集 + 同演员 + 评分 + 年份」打分，电影只推电影、剧集只推剧集
- 搜索、登录、退出；播放时自动上报进度，能接上续播
- 亮/暗主题可切换，配色取自 LiMe logo；字体和图标全部本地，不依赖外网 CDN
- 整个前端约 60KB（未压缩，JS + CSS），不会出现完整 SPA 那种浏览器内存暴涨

> 浏览器仍然播不了 mkv / H.265（服务端不转码）；mp4/webm 可以直接播，
> 其他容器会复制直链，交给 VLC / 官方客户端。
>
> `/socket` WebSocket 握手保留，官方客户端会连。

## 用户与 API Key

- 首次启动自动创建管理员（`LIME_ADMIN_USER` / `LIME_ADMIN_PASSWORD`）
- 管理员可在面板「用户」页新建/删除用户、改名、重置密码、禁用账号
- 普通用户能浏览和播放，不能改配置、管理用户或生成 API Key
- 管理类接口要求管理员：扫描/刮削、刷新、建库删库；普通用户只能看到自己和自己的会话
- 改密码会吊销其他设备的 token，当前会话保留
- API Key 在面板左侧「API Key」页生成，可直接当 `X-Emby-Token` 使用

## 媒体库管理

`/web/` → **媒体库管理** 可以直接添加、改名、改路径、改类型、单独扫描和删除：

- 路径直接从 `/strm` 下的目录下拉选择；列表里没有的深层路径可选「手动输入路径」
- 新目录要先在 `docker-compose.yml` 的 `volumes` 里挂载，再回来添加
- 新增库只扫描这个库，不会全量重扫
- 删除库会级联删除库内条目和播放记录，面板会二次确认
- 库封面自动生成，见下节；还没刮削时生成渐变 + LiMe logo 封面，刮出真实海报后自动替换
- 首次启动时用 `LIME_LIBRARIES` 建库；之后以数据库为准，环境变量不会覆盖面板里的改名/删除

### 库封面

默认样式是「斜角海报墙」：取库内最新 9 张海报排成三列、整体旋转，
左侧是库名 + 英文副标题，背景用该库配置的颜色。样式可切到
「简约模糊」（最新一张海报模糊压暗）或「库内最新海报」（原图）。

- 标题 / 副标题 / 背景色内置了 lime-cover 的 `title_config`：电影类 `MOVIES`、
  剧集 `TV SHOWS`、动漫 `ANIME`、综艺 `VARIETY`、纪录片 `DOCUMENTARY` 等；
  可用 `settings.library_cover_titles` 覆盖（JSON，键是库名，
  值形如 `{"subtitle":"MOVIES","color":"#2E344A"}`）
- 有自定义封面（例如 lime-cover 上传的 GIF）时优先用自定义封面；
  面板点「改用自动封面」会把 `*-custom.*` 改名成 `*.bak` 备份（不删除），
  再回落到自动生成
- 生成结果缓存在 `data/library-images/<库ID>-collage-<签名>.jpg`，
  条目或标题配置变化会换签名重建

### 快速入库（增量扫描）

扫描器默认全量遍历。传 `mode=fast` 或走 PanSave 入库通知时，会先读
`media_sources.strm_mtime/strm_size`，遍历中 `lstat` 比对，未变化的 `.strm`
直接跳过，只解析和写库新增/修改的文件。

- `POST /Library/Refresh?mode=fast`：全库增量
- `POST /Library/Media/Updated`：接收 Emby/Jellyfin 格式的
  `{"Updates":[{"Path":"...","UpdateType":"Created"}]}`，按路径匹配到库后只增量扫该库
- 匹配不到库时退化为全库增量
- 快速模式不清理已删除文件，删除交给全量扫描，避免误删
- 老库第一次快速扫描会把 `strm_mtime` 补齐（此时看起来是全量重建），之后才生效
- `LIME_REALTIME_MONITOR=true` 时还会递归监听库目录：PanSave 写完新 `.strm`
  就立即增量解析，解析完自动补一轮刮削。这条链路不依赖 PanSave 的入库通知，
  相当于 Emby 媒体库的「实时监控」
- 新入库条目会走精准刮削（只刮受影响的电影/季/单集），不用等整轮批量刮削结束
- 实时监控也会同步删除/改名：文件或整个目录消失后清理对应片源，空季/空剧集
  一并移除；扫描进行中的事件会攒到扫描结束后再处理，不会丢
- **启动对账**：容器停机期间收不到 inotify 事件，启动后会对每个库做一次
  「磁盘 `.strm` ↔ 数据库片源」比对，把停机期间的增删补齐。库根目录不存在时
  （媒体盘没挂上）直接跳过，不会误清库；`LIME_REALTIME_RECONCILE=false` 可关闭
- 库路径的父目录也会被监听：库目录在运行时被整体删除、改名或重建都能感知
- 只有**已配置库路径内部**的目录会被自动发现。在库路径的上一级（例如库是
  `/strm/电影/欧美电影`，而在 `/strm/电影` 下新建 `新分类`）不会自动入库，
  需要把该目录加进媒体库路径，或把库路径改成上一级
- 扫描进度分母改用库内已有片源数，不再为了显示百分比先遍历一遍整个库

## 运维面板

`/web/` 是一个内嵌的轻量运维页（纯静态，无前端框架）：

- 概览：库数量、条目/片源/播放记录统计、扫描与刮削状态
- 媒体库：浏览、按类型筛选、按标题搜索、分页加载
- 条目详情：元数据、播放状态、片源列表、**复制直链**、标记已看/收藏
- 会话：当前播放设备与进度
- 播放记录：最近 7 天的播放历史（`GET /web/api/playback-history`）
- 用户：新建/删除、改名、重置密码、禁用、按库授权
- 刮削失败：按接口错误 / 404 / 海报 / 背景图分类，支持 CSV 导出
- 通知：Telegram 播放通知配置 + 测试消息
- 系统：服务器信息、当前配置、扫描记录、实时监控开关、每日任务、立即备份
- 一键触发全量扫描 / 增量扫描 / 刮削 / 海报与背景图回填

> 面板**不承担播放**：服务端不转码，浏览器打不开 mkv / H.265。
> 「复制直链」拿到的 302 地址交给 VLC / PotPlayer / 官方客户端用。
> 页面上的「浏览器播放」只对 mp4/webm 这类容器有意义。

### 修改配置

编辑 `docker-compose.yml` 里的 `environment`，重点：

| 变量 | 说明 | 默认 |
|---|---|---|
| `LIME_SERVER_NAME` | 客户端里显示的服务器名 | `LiMe` |
| `LIME_ADMIN_USER` | 初始管理员用户名 | `admin` |
| `LIME_ADMIN_PASSWORD` | 初始管理员密码（仅首次生效） | `password` |
| `LIME_HTTP_ADDR` | 监听地址 | `:886` |
| `LIME_DATA_DIR` | 数据目录（SQLite、缓存） | `./data` |
| `LIME_MAX_ITEMS` | 单次列表查询最大返回条数，防大库一次性返回 | `20000` |
| `LIME_LIBRARIES` | 首次启动时的媒体库种子配置，见下 | 自动探测 `/strm` |
| `LIME_LIBRARY_ROOTS` | 面板里新建库允许的容器内根路径，默认 `/strm` | `/strm` |
| `LIME_SCAN_ON_START` | 启动时是否全量扫描 | `true` |
| `LIME_SCAN_INTERVAL` | 定时重扫间隔，如 `6h`；留空关闭 | 空 |
| `LIME_SCAN_DAILY_AT` | 每天固定时间做增量扫描，格式 `HH:MM`；留空关闭 | 空 |
| `LIME_REALTIME_MONITOR` | 实时监控媒体库目录（类似 Emby 实时监控），新 `.strm` 出现即增量解析并补刮削 | `true` |
| `LIME_REALTIME_RECONCILE` | 启动时做一次磁盘与片源表的对账，补齐停机期间的增删 | `true` |
| `LIME_REWRITE_ENABLED` | 是否改写 STRM 里的地址 | `false` |
| `LIME_REWRITE_FROM` | STRM 里的原始 host | — |
| `LIME_REWRITE_LOCAL` | 局域网客户端使用的地址 | — |
| `LIME_REWRITE_PUBLIC` | 外网客户端使用的地址 | — |
| `LIME_STREAM_REQUIRE_AUTH` | 播放端点是否强制校验 token | `false` |
| `LIME_INTERNAL_URL` | PanSave 反代探测用的 LiMe 上游地址，如 `http://192.168.1.100:886`。只在请求 Host 是内网地址时生效，公网域名访问会自动改用请求地址 | 空 |
| `LIME_RESOLVE_REDIRECT` | 302 前先跟一跳 PanSave，把最终 CDN 地址直接给客户端；公网直连建议开 | `false`（compose 模板里为 `true`） |
| `LIME_TMDB_API_KEY` | TMDB v3 API Key，留空则关闭刮削 | 空 |
| `LIME_TMDB_LANGUAGE` | 刮削语言 | `zh-CN` |
| `LIME_TMDB_PROXY` | 访问 TMDB 的 HTTP 代理（国内需要） | 空 |
| `LIME_SCRAPE_TIMEOUT` | 单次刮削的时长上限，到点正常暂停，下次继续；`0` 不限 | `6h` |
| `LIME_SCRAPE_WORKERS` | 刮削并发数（1-8） | `3` |
| `LIME_SCRAPE_RATE_MS` | 每次 TMDB 请求之间的间隔（毫秒） | `50` |
| `LIME_TG_BOT_TOKEN` | Telegram Bot Token（也可在面板填写，面板优先） | 空 |
| `LIME_TG_CHAT_ID` | 接收通知的 Chat ID，群组为负数 | 空 |
| `LIME_TG_PROXY` | 访问 `api.telegram.org` 的 HTTP 代理 | 空 |
| `LIME_TG_NOTIFY` | 通知总开关；留空时 token + chat id 都齐就自动开启 | 自动 |
| `LIME_TG_EVENTS` | 推送事件，逗号分隔：`start,pause,resume,stop` | 全部 |
| 面板「定时任务」 | 每日扫描 / 每日刮削时间，互不联动 | 扫描 03:30、刮削关闭 |
| 面板「Telegram 播放通知」 | 填 Token / Chat ID / 代理，勾选事件，可发测试消息 | 关闭 |

> `LIME_REWRITE_LOCAL` / `LIME_REWRITE_PUBLIC` 可以只写 `host:port`，也可以带 scheme（如 `https://pan.example.com`）。
> 客户端来自私有网段时用 LOCAL，公网时用 PUBLIC。只改写 `LIME_REWRITE_FROM` 指定的 host，其他地址原样透传。

### 挂载 STRM 目录并配置媒体库

```yaml
volumes:
  - ./data:/data
  - /你的/PanSave输出目录:/strm:ro
environment:
  # 名称|容器内路径|类型
  # 类型可选：movies / tvshows / mixed / auto（默认 auto，按内容判断）
  LIME_LIBRARIES: "电影|/strm/Movies|movies;剧集|/strm/TV|tvshows"
```

---

## 当前进度

**M0–M6 已完成**：服务骨架、认证、STRM 扫描、媒体库浏览、播放 302、续播、
TMDB 刮削、用户与 API Key、运维面板。

**M7 元数据增强**：TMDB 类型/演员写入 `Genres`/`People`，新增 `/Genres`、
`/Items/Filters`；单集时长和剧照自动补全；原生详情页展示类型、演员和单集缩略图；
外挂字幕（srt/ass/ssa/vtt）按需发现并提供 Jellyfin/Emby 字幕流。

**M8 稳定性与观感**：

| 版本 | 内容 |
|---|---|
| v1.0.5 | 相似推荐打分（类型/演员/评分/年份）、海报回填修复、同目录 STRM 归并、时长回填 |
| v1.0.6 | 新 logo 与青柠绿主题、去掉装饰性渐变、公网直连不再需要 PanSave 中转、修复 logo 路由 404 |

已实现的接口：

```
GET  /health
GET  /System/Info/Public | /System/Info
GET  /System/Configuration | /System/Configuration/public
GET  /Branding/Configuration
GET  /Branding/Splashscreen | /Branding/logo.png   ← LiMe logo
POST /Users/AuthenticateByName
GET  /Users/Public | /Users | /Users/Me | /Users/{userId}
GET  /Users/{userId}/Views
GET  /Users/{userId}/Items/Latest | /Items/Resume
GET  /Items                       ← 主查询接口
GET  /Items/Counts | /Items/Filters
GET  /Genres
GET  /Items/{itemId}
GET  /Items/{itemId}/Similar
GET  /Shows/{seriesId}/Seasons | /Episodes
GET  /Shows/NextUp                  ← 「接下来看」
POST /Library/Refresh[?mode=fast]    ← 默认全量，fast 为增量
POST /Library/Media/Updated          ← PanSave 入库通知，按路径匹配库后增量扫描
POST /Items/{itemId}/Refresh          ← 只重解析该条目（剧集含其单集）
GET  /Library/VirtualFolders | /VirtualFolders/Query | /SelectableMediaFolders
GET  /Library/MediaFolders
GET  /QuickConnect/Enabled

# M3 播放
GET|POST /Items/{itemId}/PlaybackInfo
GET  /Videos/{itemId}/stream        ← 302 到 PanSave/网盘
GET  /Videos/{itemId}/{sourceId}/Subtitles/{index}/Stream.{format}
GET  /Videos/{itemId}/Subtitles/{index}/Stream.{format}
GET  /Audio/{itemId}/stream
POST /Sessions/Playing | /Progress | /Stopped
GET  /Sessions

# M5 续播 / 观看状态
GET  /Users/{userId}/Items/Resume   ← 真实续播列表
POST|DELETE /Users/{userId}/PlayedItems/{itemId}
POST|DELETE /Users/{userId}/FavoriteItems/{itemId}

# M6 用户与凭据
POST   /Users/New                        ← 新建用户（管理员）
POST   /Users/{userId}                   ← 改名（本人或管理员）
DELETE /Users/{userId}                   ← 删除用户（管理员）
POST   /Users/{userId}/Password          ← 改密码 / 管理员重置
POST   /Users/{userId}/Policy            ← 管理员/禁用状态
POST   /Sessions/Logout                  ← 吊销当前 token
GET|POST /Auth/Keys                      ← 列出 / 生成 API Key（管理员）
DELETE /Auth/Keys/{key}                  ← 删除 API Key（管理员）

# M4 刮削
GET  /Items/{itemId}/Images/{imageType}   ← 海报/背景图（本地文件）
GET|POST /web/api/scrape                  查询 / 触发刮削

# 运维面板
GET  /                 → 302 /web/
GET  /web/             内嵌静态页
GET  /web/api/stats    统计 + 扫描/刮削/图片回填状态
GET|POST /web/api/scan 查询 / 触发扫描
GET|POST /web/api/libraries             列出 / 新建媒体库
PUT|DELETE /web/api/libraries/{id}      修改 / 删除媒体库
POST /web/api/libraries/{id}/scan       只扫描指定库
GET  /web/api/browse?path=/strm         列出可选的子目录
GET|PUT  /web/api/settings              读写面板设置（含 Telegram、实时监控）
GET|POST /web/api/scrape                查询 / 触发刮削
GET  /web/api/scrape/failures           刮削失败列表（?format=csv 导出）
GET|POST /web/api/images/posters        海报回填
GET|POST /web/api/images/backdrops      背景图回填
GET  /web/api/playback-history          播放历史（保留 7 天）
POST /web/api/notify/test               发送 Telegram 测试消息
POST /web/api/backup                    立即备份数据库
```

`/Items`、`/Items/{id}`、`/Items/Resume` 返回的 `UserData` 现在带真实数据：
`PlaybackPositionTicks`、`Played`、`PlayCount`、`IsFavorite`、`PlayedPercentage`，
剧集/季额外带 `UnplayedItemCount`（未看集数角标）。

### Emby 兼容

所有接口都同时接受 `/emby/...` 前缀（去掉前缀后走同一套路由），该前缀下
`ProductName` 返回 `Emby Server`。PanSave 的 Emby 配置校验依赖：

- `GET /emby/System/Info`
- `GET /emby/Library/VirtualFolders/Query`
- `GET /emby/Library/SelectableMediaFolders`
- `GET /emby/Items`、`GET /emby/Users`

### 部署模式

两种都能用，**推荐直连**：少一跳反代，客户端直接吃 LiMe 的 302。

#### 模式 A：客户端直连 LiMe（推荐）

```
客户端 → https://lime.example.com（CF Tunnel / 端口映射）
      → LiMe 886 → 302 → 115/123 CDN → 播放
```

- `LIME_RESOLVE_REDIRECT: "true"`：LiMe 在返回 302 前先跟一跳 PanSave，
  把最终 CDN 地址交给客户端。PanSave 的 16868 不需要暴露到公网
- `LIME_INTERNAL_URL` 留空即可：PlaybackInfo 里的 `Path` 直接用客户端访问的域名。
  就算填了也不影响公网客户端，见下面「自动判断」
- CDN 直链的签名与请求 UA 绑定，LiMe 会透传客户端 UA，否则会 403
  `invalid signature`

#### 模式 B：PanSave 反代（只有反代端口对外）

```
客户端 → https://lime.example.com（CF Tunnel）
      → PanSave 反代 1886 → LiMe 886
      → LiMe 302 → 最终 CDN
```

PanSave 反代会拿 `MediaSources[].Path` 去探测 302，这个地址必须是
**PanSave 能直接访问的上游地址**，不能是公网域名，否则会打回反代自身形成回环
（日志：`302 探测失败，透明回退`）。所以：

```yaml
LIME_INTERNAL_URL: "http://192.168.1.100:886"
LIME_RESOLVE_REDIRECT: "true"
```

#### `LIME_INTERNAL_URL` 是自动判断的

同一个实例可以同时服务两种客户端，不需要二选一：

- 请求的 `Host` 是内网地址（`192.168.x.x`、`localhost`、裸主机名）→ 用
  `LIME_INTERNAL_URL`
- 请求来自公网域名（经 CF Tunnel / 端口映射）→ 用请求本身的
  `scheme://host`，并优先采信 `X-Forwarded-Proto` / `X-Forwarded-Host`

所以把 `LIME_INTERNAL_URL` 配好，公网客户端走域名访问照样能播；
只想纯直连的话留空即可。

> 局域网内可以把 `LIME_RESOLVE_REDIRECT` 关掉，让客户端沿 PanSave 自己的
> 302 链走；公网建议保持开启，省掉一次客户端往返。

### 客户端兼容

针对 SenPlayer / VidHub / Filmly（爆米花）实际请求补过的兼容点：

- API 路径首段大小写不敏感：`/emby/videos/{id}/stream.mkv` 和 `/Videos/...` 等价
- `/Items/Counts`、`/Items/Filters`、`/Items/{id}/Similar`、`/Persons`、`/Studios`
- `/MediaSegments/{id}`、`/Items/{id}/ThemeMedia|ThemeSongs|ThemeVideos|LocalTrailers|SpecialFeatures`
- `/DisplayPreferences/{id}`、`/System/Ping`、`/Plugins`、`/Devices`
- `/api/danmu/{id}/raw` 返回空数组，避免弹幕插件报错
- 用户头像 `Images/Primary` 不强制 token（返回 LiMe logo）
- `POST /Items/{id}` 返回 204，避免客户端因 405 反复重试
- `/Videos/{id}/{sourceId}/Subtitles/{index}/Stream.{format}` 外挂字幕

### 外挂字幕

播放时按需扫描 STRM 同目录下的同名字幕，不需要重新扫描媒体库：

```
movie.strm + movie.ass
movie.strm + movie.chs.srt
movie.strm + movie.zh-CN.vtt
```

- 支持 `srt / ass / ssa / vtt`
- 自动识别 `chs / cht / eng / jpn / kor` 等语言标记，也认「中文字幕 / 简体 / 繁体」
- 在 `MediaSources[].MediaStreams` 里返回 `Type: Subtitle`、`IsExternal: true`、
  `DeliveryUrl`，客户端可以直接选字幕轨
- 只做外挂字幕；内封字幕需要读取视频字节探测，按当前「不碰视频」架构不处理

### 多版本合并

归并规则：

- 电影：按 TMDB ID 合并，同一部片的 1080p / 2160p / HDR / DoVi 是一个条目
- 电影**没有 TMDB ID** 时：按**所在目录**合并。同一目录里的多个 `.strm`
  （`-C` 后缀、`cd1`/`cd2`、中日文标题不同）是一个条目的多个片源，
  不会再出现两张一模一样的海报卡片
- 单集：按「剧 + 季 + 集」合并，同一集的不同版本是一个条目
- **不同集不会合并**（S01E01 和 S01E02 永远是两个条目）

> 归并键变化会让条目 ID 跟着变。改版后第一次全量扫描时，旧条目会因为
> `media_sources(drive_id, cloud_file_id)` 的唯一索引挡住新片源，日志里是
> `UNIQUE constraint failed`。这种情况需要清库重扫（备份 → 删 `items`
> → `POST /Library/Refresh`）。

`PlaybackInfo` 和条目详情会返回多个 `MediaSources`，名称形如：

```
SDR · 1080p · 5.1 Mbps
SDR · 2160p · 14.1 Mbps
HDR10 · 2160p · 2.7 Mbps
HDR · 2160p · 14.4 Mbps
DV · 2160p · 14.8 Mbps
```

- 动态范围保留 `DV / HDR10+ / HDR10 / HDR / SDR`，不再把 DoVi 笼统归成 HDR
- 码率用「文件大小 ÷ 时长」估算；时长优先用片源自身，未探测时用 TMDB 时长
- 没有时长时退回显示文件大小
- `MediaSources[].Bitrate` 和视频流的 `BitRate` 同时返回给客户端

默认按**画质**排序：`4K DV > 4K HDR10+ > HDR10 > HDR > 4K SDR > 1080p`，
同一档位按文件大小（近似码率）倒序。服务端不解码、客户端直连 302，
所以客户端不指定 `MediaSourceId` 时默认播最高画质；设备播不动时可在
VidHub / SenPlayer 的版本列表里手动切低版本。媒体源 ID 不变，重扫不会影响
已有播放进度。

### 时长

TMDB 刮削会写回电影和单集的 `RunTimeTicks`；对升级前已刮削、时长为 0 的条目，
启动刮削时会自动补一轮（`run_time_ticks = 0` 的电影与单集）。有真实时长后，客户端进度条和
95% 自动判定已看才能正常工作。

> 时长未知（`RunTimeTicks = 0`）时，服务端无法按 95% 自动判定「看完」，
> 需要客户端显式调用 `PlayedItems` 标记；进度条百分比也要有真实时长才会返回。

**文件名解析**支持常见命名：

```
剧名.2026.S01E01.2160p.25fps.WEB-DL.SDR.H.265.8bit.AAC2.0-HHWEB.mp4
  → 剧名 / 2026 / S01E01 / 2160p / hevc / aac 2ch / mp4

电影名.2024.1080p.WEB-DL.H.264.DDP5.1.mkv
  → 电影名 / 2024 / 1080p / h264 / eac3 6ch / mkv
```

播放链路：

```
客户端 → GET /Items/{id}/PlaybackInfo   （拿到 MediaSource，Path 指向本服务）
      → GET /Videos/{id}/stream?static=true&MediaSourceId=...
      → 302（LIME_RESOLVE_REDIRECT=true 时直接给 CDN 地址）
      → 115/123 CDN（支持 Range，206）
```

关闭 `LIME_RESOLVE_REDIRECT` 时，LiMe 只回 STRM 里的 PanSave 地址，
由客户端再跟一跳 PanSave 的 302。

`SupportsTranscoding: false`，没有任何转码路径；客户端不支持直连编码时会直接报错，这是设计边界。

### 刮削（M4）

服务端用目录名里的 `{tmdb-ID}` 精确请求 TMDB，不做标题搜索，避免刮错片：

- 电影 → `/movie/{id}`，剧集 → `/tv/{id}`，单集 → `/tv/{id}/season/{n}`
- 写回简介、原名、首播日期、评分、类型、演员；海报/背景图/ClearLogo 下载到
  `data/images/`，由 `/Items/{id}/Images/{type}` 本地提供，不依赖外网始终可用
- 只处理 `scraped_at = 0` 的条目，重复扫描/重启不会重复请求
- **单次运行有软性时长上限**（`LIME_SCRAPE_TIMEOUT`，默认 `6h`，`0` 表示不限）。
  到点后本次正常结束，已处理的条目全部落库，面板显示「本次已达到单次运行上限，
  剩余条目下次继续」；这不是错误，不会标红，也不会把余下条目标成 TMDB 失败
- 单个条目偶发失败（网络抖动、单请求 25s 超时）只进「刮削失败」列表；
  只有连续 20 条都失败才认为 TMDB/代理整体不可用并在面板报错
- 面板里有「刮削失败」页，按接口错误 / 404 / 海报 / 背景图分类，可导出 CSV
- **海报回填**：海报为空的条目也会进回填队列并排在最前。早先只回填
  `image_path <> ''` 的条目，「刮过但当时 TMDB 还没图」的片会永远缺海报
- 头像回填并发执行，共用 50ms 全局限速；头像按需下载

> 国内直连 `api.themoviedb.org` 不通时，设置 `LIME_TMDB_PROXY`
> （NAS 上 mihomo 监听 `7890`，容器里要用宿主机 IP，不能写 `127.0.0.1`）。
> API Key 建议放在 compose 同目录的 `.env`，该文件已在 `.gitignore` /
> `.dockerignore` 中排除，同步源码时也不要带过去。

### 本地元数据（NFO / 本地图片）

媒体目录里自带 `.nfo` 和图片时，**本地元数据优先**，定时 TMDB 刮削不会覆盖：

```
电影名 (2024)/movie.nfo
电影名 (2024)/poster.jpg | folder.jpg | cover.jpg | thumb.jpg
电影名 (2024)/fanart.jpg | backdrop.jpg
```

- 支持 Kodi / Emby 风格的 `movie` / `episodedetails` / `tvshow`
- 识别 title / originaltitle / plot / year / premiered / runtime / rating /
  mpaa / genre / actor / director / studio / uniqueid(tmdb)
- 图片**不复制**，数据库存 `local:/绝对路径`，图片接口限制在媒体库根目录内（防穿越）
- 扫描时应用 NFO 并写入 `scraped_at`，本地元数据是权威来源
- 只改 NFO 不会触发实时监控，需要 `POST /Library/Refresh`

> NFO 标题变化会改变条目 ID，可能撞 `media_sources(drive_id, cloud_file_id)`
> 唯一索引，需要清库重扫（见上面「多版本合并」的说明）。

### 搜索

`items_fts` 用 SQLite FTS5 trigram 索引，增删改由触发器同步：

- ≥3 个字符走 FTS：实测「流浪地球」1.2ms、「蜘蛛侠」0.5ms
- 2 个字符的中文（如「三体」）仍走 `LIKE`，trigram 的固有限制

---

## 自测脚本

- `tools/verify_m5.py`：登录 → 上报进度 → 续播列表 → 标记已看 → 收藏
- `tools/verify_m4.py`：刮削状态 → 元数据/海报 → 图片端点 → 重扫不丢数据

```bash
ADMIN_PASS=你的密码 python tools/verify_m5.py
ADMIN_PASS=你的密码 python tools/verify_m4.py
```

---

## 本地开发

需要 Go 1.24+。

```bash
go build -o lime ./cmd/lime
LIME_DATA_DIR=./data LIME_HTTP_ADDR=:886 ./lime
```

测试登录：

```bash
curl -s -X POST http://127.0.0.1:886/Users/AuthenticateByName \
  -H 'Content-Type: application/json' \
  -H 'Authorization: MediaBrowser Client="Test", Device="PC", DeviceId="dev1", Version="1.0"' \
  -d '{"Username":"admin","Pw":"password"}'
```

---

## 目录结构

```
cmd/lime/                 入口、启动扫描、优雅退出
internal/
  api/                    HTTP 路由、中间件、Jellyfin 兼容 handler
    assets/logo.png       go:embed 的 logo（/Branding/Splashscreen）
  config/                 环境变量配置
  dto/                    Jellyfin 兼容 JSON 结构
  library/                STRM 扫描器、实时监控（fsnotify）、条目归并
  strm/                   STRM 解析 + 文件名/目录名解析
  nfo/                    本地 .nfo / poster.jpg 解析
  probe/                  ffprobe 探测（时长、音轨、分辨率）
  scrape/                 TMDB 刮削、海报/背景图/logo 回填
  tmdb/                   TMDB API 客户端 + 图片下载
  notify/                 Telegram 播放通知
  pinyin/                 拼音与首字母排序
  webui/                  内嵌媒体界面（app/）与运维面板
  store/                  SQLite 访问 + 迁移
    migrations/           SQL 迁移，按文件名顺序执行
```

---

## 社区

- **频道** <https://t.me/Lime_see>：版本发布、本次更新内容、已知问题
- **群组** <https://t.me/Lime_seee>：使用讨论、问题反馈

打 `v*` tag 后，CI 会把版本号和更新列表自动推送到频道。

## 许可证

MIT。本项目按 Jellyfin 的公开 API 契约独立实现，与 Jellyfin 项目无隶属关系。

内置的封面字体 `NotoSansSC-Black-gb.ttf` 来自 Noto Sans SC，按 SIL Open Font
License 1.1 分发，许可证见 `internal/api/assets/fonts/OFL.txt`。
