# LiMe

[![docker](https://github.com/Anboyyt/LiMe/actions/workflows/docker.yml/badge.svg)](https://github.com/Anboyyt/LiMe/actions/workflows/docker.yml)
[![GitHub Release](https://img.shields.io/github/v/release/Anboyyt/LiMe?sort=semver)](https://github.com/Anboyyt/LiMe/releases)
[![Docker Pulls](https://img.shields.io/docker/pulls/lixxh/lime)](https://hub.docker.com/r/lixxh/lime)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Telegram Channel](https://img.shields.io/badge/Telegram-Channel-26A5E4?logo=telegram&logoColor=white)](https://t.me/Lime_see)
[![Telegram Chat](https://img.shields.io/badge/Telegram-Chat-229ED9?logo=telegram&logoColor=white)](https://t.me/Lime_seee)

**轻量 Jellyfin 兼容媒体服务器 · 只适配 PanSave**

兼容 Jellyfin 客户端、爆米花、SenPlayer、VidHub —— **SenPlayer 体验最佳**。

最新版本 **v1.1.2** · [更新记录](#更新记录) · [Docker Hub](https://hub.docker.com/r/lixxh/lime)

服务端代客户端跟一跳 PanSave 的 302；**外网直接访问 LiMe 就能播放**，
视频流不再绕经 PanSave 反向代理。

```
客户端 → LiMe → 302 → 115/123 CDN → 播放
```

默认自动开启：LiMe 在服务端把 PanSave 那一跳跟掉，直接把最终 CDN 地址交给客户端
—— PanSave 的 16868 端口完全不用暴露到公网。PanSave 慢或连不上时会自动回退原 302，
不会把起播卡死。

> 服务端不接触视频字节、不转码、不缓存 URL。播放大库时 LiMe 几乎不占上传带宽。

---

## 目录

- [为什么是 LiMe](#为什么是-lime)
- [功能特性](#功能特性)
- [快速开始](#快速开始)
- [客户端兼容](#客户端兼容)
- [Web 界面](#web-界面)
- [片头片尾](#片头片尾)
- [媒体库管理](#媒体库管理)
- [运维面板](#运维面板)
- [本地播放器唤起](#本地播放器唤起)
- [配置项](#配置项)
- [播放链路](#播放链路)
- [刮削](#刮削)
- [本地元数据](#本地元数据-nfo--本地图片)
- [外挂字幕](#外挂字幕)
- [多版本合并](#多版本合并)
- [搜索](#搜索)
- [部署模式](#部署模式)
- [更新记录](#更新记录)
- [已实现接口](#已实现接口)
- [本地开发](#本地开发)
- [目录结构](#目录结构)
- [社区与许可证](#社区与许可证)

---

## 为什么是 LiMe

- **只为「网盘 STRM + 302」这一件事**。不接云盘 API、不做转码、不缓存直链，
  云盘那层全部交给 PanSave，LiMe 只负责索引和协议。
- **29 万条 STRM 能扫**。流式 `WalkDir` + 12 个解析 worker + 批量事务，
  实测 29 万条约 2 秒完成启动对账，扫描内存不随文件数线性增长。
- **出网带宽几乎为零**。播放时只回一个 302，视频字节从 CDN 直进客户端，
  NAS 不承担中转。
- **复用官方客户端生态**。实现 Jellyfin 兼容 API，Android / Android TV /
  Infuse / VidHub / SenPlayer / 爆米花都能直接连。
- **单二进制 + SQLite**。Go 静态编译，无 CGO、无外部数据库、无前端构建链。

## 功能特性

| 能力 | 说明 |
|---|---|
| Jellyfin / Emby 兼容 | 同时接受 `/emby/...` 前缀，PanSave 的 Emby 配置校验可直接通过 |
| 302 直连 | 服务端只发重定向，不碰视频字节；默认 auto 在服务端跟掉 PanSave 一跳，失败/超时回退原 302 |
| 多人同播 | 同一视频第二个设备由 PanSave 分配 115 网盘副本，避开同一直链并发 403；自动识别反代入口，也可在面板手动指定 |
| 播放设置面板 | 跟跳模式、DirectStreamUrl、播放鉴权、多人同播全部在 `/web/` 里调，保存即时生效，不用改 compose |
| 健康检查 | `/health` 进程级；`/health/deep` 带数据库探测、uptime、goroutine 和内存快照，供容器 healthcheck 使用 |
| 下载 / 离线缓存 | 客户端通过 `/Items/{id}/Download`、`/Items/{id}/File` 下载原片；默认 302 到 CDN，不占 NAS 带宽，`proxy` 模式可兜底不跟 302 的下载器 |
| STRM 索引 | 扫描 `.strm`，自动识别电影 / 剧集 / 季 / 单集，支持多版本归并 |
| TMDB 刮削 | 按目录里的 `{tmdb-ID}` 精确刮削，海报 / 背景 / Logo / 简介 / 演员 |
| 手动识别 | 详情页按标题搜索 TMDB，选中结果即可改绑并重新刮削；目录名缺 `{tmdb-ID}` 也能救回来 |
| 图片尺寸 | 海报可选 w342 / w500 / w780 / original，剧照可选 w92 / w185 / w300 / original；单集封面可继承剧集海报，也可单独下载剧照 |
| 待处理 | 没 TMDB ID、刮削失败、缺图、重复条目集中展示，支持识别、单条/批量重试 |
| 运维维护 | 图片孤儿清理、扫描/刮削/备份任务历史、卡顿阈值面板可调、运行诊断 |
| 本地元数据 | 识别同目录 `.nfo` 与 `poster.jpg` / `fanart.jpg`，本地优先 |
| 实时监控 | 新 `.strm` 落盘即增量解析并精准刮削，删除/改名同步清理 |
| 片头片尾 | 章节 + 音频指纹两种来源，返回 `MediaSegments`，支持手动标记 |
| 入库通知 | 刮削完成后推送 Telegram，同剧同季多集合并，电影逐部通知；可选独立 Bot |
| 多用户 | 独立进度、收藏、已看状态，支持按库授权与查看权限 |
| 相似推荐 | 按类型 / 演员 / 评分 / 年份打分，电影只推电影、剧集只推剧集 |
| 本地播放器 | 唤起 PotPlayer / VLC / MPV / IINA / Infuse，也可导出 m3u |
| 自带 Web UI | `/` 原生媒体界面，`/web/` 运维面板，均无前端框架 |
| 单二进制 | Go 静态编译 + SQLite，镜像约 58MB（含 ffmpeg） |

---

## 快速开始

预构建镜像由 GitHub Actions 发布，支持 `linux/amd64` 和 `linux/arm64`。

### 方式一：Docker Compose（推荐）

新建 `docker-compose.yml`，粘贴下面这段，只改 `volumes` 里的 STRM 路径：

```yaml
services:
  lime:
    image: lixxh/lime:latest
    container_name: lime
    restart: unless-stopped
    ports:
      - "886:886"
    environment:
      TZ: Asia/Shanghai
      LIME_ADMIN_USER: "admin"
      LIME_ADMIN_PASSWORD: "change-me"
      LIME_LIBRARIES: "电影|/media/电影|movies;剧集|/media/剧集|tvshows"
      LIME_LIBRARY_ROOTS: "/media"
      # 起播加速默认 auto，不用写；需要强制/关闭时再设 true/false
      # LIME_RESOLVE_REDIRECT: "auto"
      # LIME_PLAYBACK_DIRECT_URL: "auto"
      LIME_BACKUP_DIR: "/backup"        # 数据库每日备份目录
    volumes:
      - ./data:/data
      - ./backup:/backup
      - /path/to/your/strm:/media:ro    # ← 你的 PanSave STRM 目录
```

```bash
docker compose up -d
```

也可以直接下载模板：

```bash
curl -fsSLO https://raw.githubusercontent.com/Anboyyt/LiMe/main/docker-compose.example.yml
mv docker-compose.example.yml docker-compose.yml
```

### 方式二：docker run

```bash
docker run -d --name lime \
  --restart unless-stopped \
  -p 886:886 \
  -v /path/to/data:/data \
  -v /path/to/backup:/backup \
  -v /path/to/strm:/media:ro \
  -e LIME_ADMIN_USER=admin \
  -e LIME_ADMIN_PASSWORD=change-me \
  -e LIME_LIBRARIES="电影|/media/电影|movies;剧集|/media/剧集|tvshows" \
  -e LIME_LIBRARY_ROOTS=/media \
  -e LIME_BACKUP_DIR=/backup \
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

## 客户端兼容

| 客户端 | 状态 | 说明 |
|---|---|---|
| **SenPlayer** | ✅ 最佳 | 实际按它的请求行为做过针对性适配，片源切换 / 续播 / 字幕最完整 |
| VidHub | ✅ | 多版本列表、进度上报正常 |
| 爆米花（Filmly） | ✅ | 登录 / 浏览 / 播放正常 |
| Infuse | ✅ | 兼容性好，可作对照组 |
| Jellyfin 官方 Android / Android TV | ⚠️ 基础可用 | 浏览 / 播放 / 续播正常；服务端报 10.10.7，片头跳过按钮可显示 |
| jellyfin-web | ❌ | 浏览器播不了 mkv / H.265，服务端不转码，属架构固有边界 |

已针对性补齐的兼容点：

- API 路径首段大小写不敏感：`/emby/videos/{id}/stream.mkv` 与 `/Videos/...` 等价
- `/Items/Counts`、`/Items/Filters`、`/Items/{id}/Similar`、`/Studios`
- 演员：`/Persons`、`/Persons/{name|id}`、`/Items?PersonIds=` 反查；
  `/Items/{personId}` 也能返回 Person 条目，点演员可直接跳详情
- `/MediaSegments/{id}`、`/Items/{id}/ThemeMedia|ThemeSongs|ThemeVideos|LocalTrailers|SpecialFeatures`
- `/DisplayPreferences/{id}`、`/System/Ping`、`/Plugins`、`/Devices`
- `/api/danmu/{id}/raw` 返回空数组，避免弹幕插件报错
- 用户头像 `Images/Primary` 不强制 token（返回 LiMe logo）
- `POST /Items/{id}` 返回 204，避免客户端因 405 反复重试
- 外挂字幕走 `/Videos/{id}/{sourceId}/Subtitles/{index}/Stream.{format}`

## Web 界面

`/` 是自带的原生媒体界面，参考 Pelagica 的布局自己实现，**没有前端框架**：

- 首页：随机背景 + 继续观看 + 最近添加
- 媒体库：海报网格、按名称/年份/最近添加/最新一集排序、分页
- 详情：海报/背景、简介、类型、演员、季与单集（单集封面继承剧集海报）、片源直链、外挂字幕、标记已看/收藏
- 管理员可在详情页「手动识别」：搜索 TMDB、选结果或直接填 ID，自动改绑并重新刮削
- **相似推荐**：按「类型交集 + 同演员 + 评分 + 年份」打分，电影只推电影、剧集只推剧集
- 搜索、登录、退出；播放时自动上报进度，能接上续播
- 亮/暗主题可切换，配色取自 LiMe logo；字体和图标全部本地，不依赖外网 CDN
- 详情页可直接唤起本地播放器（PotPlayer 等），见[本地播放器唤起](#本地播放器唤起)
- 整个前端约 60KB（未压缩，JS + CSS），不会出现完整 SPA 那种浏览器内存暴涨

> 浏览器仍然播不了 mkv / H.265（服务端不转码）；mp4/webm 可以直接播，
> 其他容器会复制直链，交给 VLC / 官方客户端。
>
> `/socket` WebSocket 握手保留，官方客户端会连。

## 片头片尾

`GET /MediaSegments/{itemId}` 返回 Jellyfin 10.10 的
`QueryResult<MediaSegmentDto>`（支持 `includeSegmentTypes` 过滤），
片段类型为 `Intro / Outro / Recap / Preview / Commercial`。
数据按**单集**存储，同一部剧不同集各有各的起点和终点。

两种识别方式，可以叠加使用：

**1. 章节（默认开启）**

`ffprobe -show_chapters` 和轨道探测走同一次请求，**没有额外流量**，
实测 1–2 秒/集。按章节名匹配：

- 片头：`Intro` / `Opening Credits` / `片头`
- 片尾：`Credits` / `End Credits` / `片尾`
- 其他：`Recap` / `Preview`
- 普通 `Chapter 01` / `Scene 1` 不做猜测

时长做了上下限约束（Intro 5 秒–15 分钟、Outro 5 秒–20 分钟），
避免把几分钟的正片误判成片头。章节大概覆盖 60% 的片尾、20% 的片头，
其余靠音频指纹。

**2. 音频指纹（无章节的剧）**

把每集开头/结尾的音频转成 0.5 秒 RMS 包络，同季多集两两做互相关，
找出重复出现的连续片段。不依赖章节名，也不需要人工规则。
同一时间只跑一季，避免打满 NAS 的 CPU 和网盘带宽。

**触发时机**

- 新入库条目立即探测并提取（不依赖后续整轮扫描）
- 单集播放进度达到 `LIME_SEGMENT_AUTO_AFTER`（默认 **10 分钟**，`0` 关闭）时
  自动补齐整部剧；10 / 20 分钟各检查一次。
  同一部剧 12 小时内只预热一次
- 新集章节为空时，自动补跑本季音频指纹；只采样 6 集没找到时会自动用整季重试
- 最近 14 天看到 10 分钟以上的剧，整季都没有片头片尾时会在预热时间窗内
  自动跑指纹回填；一次只跑一季，失败的季 7 天内不重复扫
- 老剧不会主动回填，等单集看到 `LIME_SEGMENT_AUTO_AFTER` 后再按剧补齐
- 季中换 OP/ED：新片段只要有两集共享即可识别；只有单伙伴时，
  匹配长度 ≥60 秒且相关度足够高也接受
- 已经识别出片尾的集，后续还会补上缺的片头，不会因为「已有片段」整集跳过
- 手动标记过空结果的条目不会再自动分析，可用下面的 reset 接口重跑
- 手动触发（管理员）：
  - `POST /web/api/segments/scan?seriesId=&limit=` 按剧补齐
  - `POST /web/api/segments/scan?itemIds=a,b` 定点补条目
  - `POST /web/api/segments/fingerprint?seriesId=&season=` 跑本季指纹
  - `POST /web/api/segments` 手动写入片段（`ItemId` + `Segments[]`，会标记为手动来源）
  - `POST /web/api/segments/reset?itemId=` 清掉片段和分析状态后重新排队

> 官方客户端播放器里的「跳过片头」按钮依赖服务端版本号 ≥ 10.10。
> LiMe 上报 10.10.7，接口返回 `MediaSegments` 后按钮即可显示。

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

## 健康检查

- `GET /health`：进程级，返回版本，只要 HTTP 端口活着就 200
- `GET /health/deep`：查一次数据库（`SELECT 1`，2 秒上限），数据库卡住时返回 503；
  同时带 uptime、goroutine 数、内存（alloc/sys，5 秒内复用缓存，不重复触发 STW）
  和数据库延迟，排查时直接看这一条

容器 healthcheck 建议用 `/health/deep`：

```yaml
healthcheck:
  test: ["CMD", "wget", "-qO-", "http://127.0.0.1:886/health/deep"]
  interval: 30s
  timeout: 5s
  retries: 3
  start_period: 120s   # 大库首次启动留足时间
```

## 运维面板

`/web/` 是一个内嵌的轻量运维页（纯静态，无前端框架），**仅管理员可进**：

- 概览：库数量、条目/片源/播放记录统计、扫描与刮削状态
- **待处理**：没 TMDB ID / 刮削失败 / 缺图 / 重复条目，支持打开详情识别、单条或批量重试
- 媒体库：浏览、按类型筛选、按刮削状态筛选、按标题搜索、分页加载
- 条目详情：元数据、播放状态、片源列表、**复制直链**、标记已看/收藏
- 会话：当前播放设备与进度（30 分钟不上屏、24 小时清理）
- 播放记录：最近 7 天的播放历史（`GET /web/api/playback-history`）
- 用户：新建/删除、改名、重置密码、禁用、按库授权
- 刮削失败：按接口错误 / 404 / 海报 / 背景图分类，支持 CSV 导出
- 通知：Telegram 播放通知配置 + 测试消息
- 系统：按「常规 / 刮削与图片 / 播放 / 高级」分组，包含服务器信息、扫描与实时监控、
  定时任务、最近任务历史、TMDB 与图片尺寸、单集封面、卡顿阈值、图片清理、运行诊断
- **播放设置**：起播加速（跟跳 / DirectStreamUrl 三态）、播放端点鉴权、多人同播
  （模式 + 反代入口 + 反代域名），保存即时生效，不用改 compose 或重启
- **运行日志**：进程内环形缓存最近 1000 条，可按级别筛选、一键导出
  `.txt`（`GET /web/api/logs/export`）；日志不落盘，`docker logs lime` 才是完整来源
- 片头片尾：展示每集片段、触发提取、跑本季音频指纹
- 一键触发全量扫描 / 增量扫描 / 刮削 / 海报与背景图回填

> 面板**不承担播放**：服务端不转码，浏览器打不开 mkv / H.265。
> 「复制直链」拿到的 302 地址交给 VLC / PotPlayer / 官方客户端用。
> 页面上的「浏览器播放」只对 mp4/webm 这类容器有意义。

## 播放通知与卡顿监控

Telegram 播放通知（开始 / 暂停 / 继续 / 停止 / 卡顿）支持配图：
优先用 **16:9 背景图**，没有背景图时退回竖版海报；图片在后台缩放到最长边
1280 再上传，不阻塞客户端的进度上报。

卡顿判定只看客户端自己的进度心跳：服务端只发 302 看不到 CDN 字节，
但真正卡住时上报位置会停住。未暂停且位置超过阈值（默认 2 分钟）没有前进
就推一条告警，包含用户、设备、片名、进度和当前片源；暂停、拖动、正常前进
都会重置计时，同一会话默认 10 分钟内不重复告警。阈值和冷却时间可在
「系统 → 播放 → 卡顿告警」里调整，不用改环境变量重启。

暂停、切后台或系统挂起时客户端会停止上报，超过 60 秒的静默会按暂停处理；
之前明确上报过 `IsPaused=true` 的，后续心跳即使没带暂停标记也不会误报。

每次卡顿会落到 `playback_stalls` 表（用户、设备、条目、片源、位置、时长），
方便后面按片子/片源统计。管理员接口：

```
GET /web/api/stalls?days=7&limit=200
```

> 只覆盖「位置完全不动」的硬卡顿；网络抖动导致进度变慢但仍在前进的情况
> 目前不告警，避免误报。

## 入库通知

新 `.strm` 写入后不会立刻发消息，而是等刮削补上海报 / 演员 / TMDB 后再推送：

- 同一部剧同一季的多集合并成一条，例如 `S02 E01-E10`
- 电影逐部单独通知，方便确认哪一部已经整理入库
- 优先用 16:9 背景图，没有则退回竖版海报
- 大量入库（默认超过 200 条）只发一条汇总，避免初始扫描刷屏
- 通知失败不会丢：发送成功后才标记 `notified_at`，下次继续重试
- Bot 可以复用播放通知，也可以单独配置 Token / Chat ID / 代理

面板路径：`/web/` → 通知 → Telegram 入库通知。

## 本地播放器唤起

媒体界面和运维面板的播放按钮可以直接拉起本机播放器，不用先复制直链：

- 片源区顶部有「本地播放器」下拉，默认 **PotPlayer**，选择存 localStorage
- 支持：PotPlayer `potplayer://`、VLC `vlc://`、MPV `mpv://`、
  IINA（macOS）`iina://weblink?url=`、Infuse `infuse://x-callback-url/play?url=`
- 后端 `GET /web/api/play-target?itemId=&sourceId=&player=` 返回
  `HttpUrl / ProtocolUrl / M3uUrl / Player / Players`；sourceId 可省略取第一个源
- `POST /web/api/play-target` 保存管理员默认播放器
- `GET /web/api/playlist.m3u?itemId=&sourceId=` 输出 m3u 作为回退

> 浏览器首次会弹「是否打开 PotPlayer」，无法绕过；协议没注册时会静默失败，
> 这时用「播放列表」或「复制直链」回退。

## 配置项

播放相关的开关（起播加速、播放鉴权、多人同播）**都可以在运维面板 `/web/` →
播放设置里改**，保存即时生效，不用动 compose；环境变量只作为初始值。
下面列出这些变量是为了说明语义和兼容旧部署。

编辑 `docker-compose.yml` 里的 `environment`，重点：

| 变量 | 说明 | 默认 |
|---|---|---|
| `LIME_SERVER_NAME` | 客户端里显示的服务器名 | `LiMe` |
| `LIME_ADMIN_USER` | 初始管理员用户名 | `admin` |
| `LIME_ADMIN_PASSWORD` | 初始管理员密码（仅首次生效） | `password` |
| `LIME_HTTP_ADDR` | 监听地址 | `:886` |
| `LIME_DATA_DIR` | 数据目录（SQLite、缓存） | `./data` |
| `LIME_BACKUP_DIR` | 数据库每日快照输出目录，留空写数据目录；可指到独立挂载点 | 空 |
| `LIME_BACKUP_KEEP` | 数据库备份保留份数（1-30） | `3` |
| `LIME_MAX_ITEMS` | 单次列表查询最大返回条数，防大库一次性返回；客户端传 `EnableTotalRecordCount=false` 时跳过 COUNT | `20000` |
| `LIME_LIBRARIES` | 首次启动时的媒体库种子配置，见下 | 自动探测 `/strm` |
| `LIME_LIBRARY_ROOTS` | 面板里新建库允许的容器内根路径 | `/strm` |
| `LIME_SCAN_ON_START` | 启动时是否全量扫描 | `true` |
| `LIME_SCAN_INTERVAL` | 定时重扫间隔，如 `6h`；留空关闭 | 空 |
| `LIME_SCAN_DAILY_AT` | 每天固定时间做增量扫描，格式 `HH:MM`；留空关闭 | 空 |
| `LIME_REALTIME_MONITOR` | 实时监控媒体库目录，新 `.strm` 出现即增量解析并补刮削 | `true` |
| `LIME_REALTIME_RECONCILE` | 启动时做一次磁盘与片源表的对账，补齐停机期间的增删 | `true` |
| `LIME_REWRITE_ENABLED` | 是否改写 STRM 里的地址 | `false` |
| `LIME_REWRITE_FROM` | STRM 里的原始 host | — |
| `LIME_REWRITE_LOCAL` | 局域网客户端使用的地址 | — |
| `LIME_REWRITE_PUBLIC` | 外网客户端使用的地址 | — |
| `LIME_STREAM_REQUIRE_AUTH` | 播放端点是否强制校验 token。也可在运维面板「播放设置」里改，面板值优先 | `false` |
| `LIME_INTERNAL_URL` | PanSave 反代探测用的 LiMe 上游地址。只在请求 Host 是内网地址时生效，公网域名访问会自动改用请求地址 | 空 |
| `LIME_PROXY_HOSTS` | PanSave Emby 反代的公网域名，逗号分隔（如 `ps.example.com`）。命中的请求按反代场景处理，参与 115 复制同播；内网地址不需要列 | 空 |
| `LIME_RESOLVE_REDIRECT` | 302 前先跟一跳 PanSave，把最终 CDN 地址直接给客户端。`auto`：最多等 500ms，失败/超时回退原 302，连续失败 3 次熔断 5 分钟；`true`：强制解析，失败可能返回 502；`false`：关闭。也可在运维面板「播放设置」里改，面板值优先 | `auto` |
| `LIME_MEDIA_PATH_STRM` | `MediaSources[].Path` 返回 STRM 里的原始地址（仍按 rewrite 规则改写 host），`DirectStreamUrl` 保持本服务地址。PanSave 的 Emby 反代只对 `/strm` 形状的片源路径启用 **115 复制同播**。`auto`（默认）：请求带 `X-Forwarded-Host` 且是内网地址（即从 PanSave 反代端口进来）时启用，Cloudflare 隧道等公网域名访问保持原行为；`true` / `false` 强制开关。Yamby / 爆米花仍返回相对路径 | `auto` |
| `LIME_PLAYBACK_DIRECT_URL` | PlaybackInfo / 剧集列表的 `DirectStreamUrl` 直接返回 CDN 直链。`auto`：只对 VidHub / SenPlayer / Yamby / Infuse 等会用该字段的客户端预解析；`true`：对所有客户端；`false`：关闭。需 `LIME_RESOLVE_REDIRECT` 不为 false。也可在运维面板「播放设置」里改 | `auto` |
| `LIME_YAMBY_PROXY` | Yamby 跟 302 不稳时，把它的视频流改成服务端中转；会占用 NAS 上行 | `false` |
| `LIME_STREAM_CACHE_MB` | 仅代理模式：每个片源预取的头部长度的 MB 数，`0` 关闭（尾部固定 2MB） | `0` |
| `LIME_STREAM_CACHE_MAX_MB` | 头/尾缓存总上限 | `4096` |
| `LIME_DOWNLOAD_MODE` | 客户端下载：`redirect` 直接 302 到 CDN；`proxy` 由 LiMe 中转（占 NAS 上行）；`off` 关闭下载入口 | `redirect` |
| `LIME_SQLITE_CACHE_MB` | SQLite 每个连接的页缓存（MB），8 个连接合计上限约为该值的 8 倍 | `32` |
| `LIME_SQLITE_MMAP_MB` | SQLite mmap 大小（MB），`0` 关闭 | `256` |
| `LIME_TMDB_API_KEY` | TMDB v3 API Key，留空则关闭刮削 | 空 |
| `LIME_TMDB_LANGUAGE` | 刮削语言 | `zh-CN` |
| `LIME_TMDB_PROXY` | 访问 TMDB 的 HTTP 代理（国内需要） | 空 |
| `LIME_SCRAPE_TIMEOUT` | 单次刮削的时长上限，到点正常暂停，下次继续；`0` 不限 | `6h` |
| `LIME_SCRAPE_WORKERS` | 刮削并发数（1-8） | `3` |
| `LIME_SCRAPE_RATE_MS` | 每次 TMDB 请求之间的间隔（毫秒） | `50` |
| `LIME_SEGMENT_EXTRACT` | 是否提取片头片尾 | `true` |
| `LIME_SEGMENT_SCOPE` | 存量回填范围：`active` / `favorites` / `watching` / `recent` / `all` | `active` |
| `LIME_SEGMENT_AUTO_AFTER` | 单集观看多久后自动补齐整部剧；`0` 关闭 | `10m` |
| `LIME_TG_BOT_TOKEN` | Telegram Bot Token（也可在面板填写，面板优先） | 空 |
| `LIME_TG_CHAT_ID` | 接收通知的 Chat ID，群组为负数 | 空 |
| `LIME_TG_PROXY` | 访问 `api.telegram.org` 的 HTTP 代理 | 空 |
| `LIME_TG_NOTIFY` | 通知总开关；留空时 token + chat id 都齐就自动开启 | 自动 |
| `LIME_TG_EVENTS` | 推送事件，逗号分隔：`start,pause,resume,stop,stall,health` | 全部 |
| `LIME_STALL_ALERT_AFTER` | 未暂停时进度多久不前进就发卡顿告警；`0` 关闭；面板设置优先 | `2m` |
| `LIME_STALL_ALERT_COOLDOWN` | 同一播放会话两次卡顿告警的最小间隔；面板设置优先 | `10m` |

> `LIME_REWRITE_LOCAL` / `LIME_REWRITE_PUBLIC` 可以只写 `host:port`，也可以带 scheme
> （如 `https://pan.example.com`）。客户端来自私有网段时用 LOCAL，公网时用 PUBLIC。
> 只改写 `LIME_REWRITE_FROM` 指定的 host，其他地址原样透传。

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

## 播放链路

```
客户端 → GET /Items/{id}/PlaybackInfo   （拿到 MediaSource，Path 指向本服务）
      → GET /Videos/{id}/stream?static=true&MediaSourceId=...
      → 302（auto/true 时直接给 CDN 地址；false 时给 PanSave 地址）
      → 115/123 CDN（支持 Range，206）
```

`LIME_RESOLVE_REDIRECT` 设为 `false` 时，LiMe 只回 STRM 里的 PanSave 地址，
由客户端再跟一跳 PanSave 的 302。默认的 `auto` 会自动尝试服务端跟跳，
失败或超时就回退到这条路径。

- `SupportsTranscoding: false`，没有任何转码路径；客户端不支持直连编码时会直接报错，这是设计边界
- CDN 直链的签名与请求 UA 绑定，LiMe 会透传客户端 UA，否则会 403 `invalid signature`
- `RunTimeTicks = 0` 时部分客户端进度条异常，属已知限制；有 TMDB 时长后会自动回填
- 多版本默认按画质排序，客户端不指定 `MediaSourceId` 时播最高画质

### PanSave 115 复制同播（多人同时看同一部）

115 的同一条直链并发播放会 403。PanSave 的 Emby 反代支持按 `device_id` 给
第二个设备分配一份网盘副本，但它的前提是**片源的 `MediaSources[].Path` 必须是
它自己生成的 `/strm` 地址**——它会观察 Item 详情里的 `media_source_path`，
不是这个形状就当成普通外部 HTTP 源，不做副本分配。

**最省事的方式是运维面板**：`/web/` → 播放设置 →「多人同播」，选自动/强制/关闭，
反代入口和反代域名可以点最近检测到的记录直接填。保存即时生效，不用重启。

**也可以完全不管**：`LIME_MEDIA_PATH_STRM` 默认 `auto`，LiMe 检测到请求来自
内网反代（`X-Forwarded-Host` 是私有地址，即从 PanSave 的 1886/1888 端口进来）
就自动启用；客户端直连 LiMe 或走 Cloudflare 隧道（公网域名）时保持原行为。

需要固定行为时再显式写环境变量：

需要固定行为时再显式写：

```yaml
environment:
  LIME_MEDIA_PATH_STRM: "true"     # 强制开启；"false" 强制关闭
  # LIME_RESOLVE_REDIRECT: "false"  # 走反代时 LiMe 的 stream 不被调用，auto 也不影响
```

如果客户端是**直连 LiMe 端口**（比如内网用 `886`，没经过 PanSave 反代），
再补一条反代入口地址，LiMe 会把播放地址引导过去，同样能分配副本：

```yaml
environment:
  LIME_INTERNAL_URL: "http://192.168.1.10:1886"   # PanSave Emby 反代端口
```

这种情况下只对私网请求生效，公网域名（Cloudflare 隧道）访问不受影响。

如果客户端是通过 **PanSave 反代的公网域名**访问（例如 `ps.example.com` 指向
1886、`part.example.com` 指向 1888），把域名列进去，公网入口也能分配副本：

```yaml
environment:
  LIME_PROXY_HOSTS: "ps.example.com"
```

内网地址靠私有网段自动识别，不用列；没列进去的公网域名（如直连 LiMe 的
`lime.example.com`）保持原来的 CDN 直连行为。

字段分工：

```
MediaSources[].Path            = http://.../strm/115/drive?...   ← 只给 PanSave 观察
MediaSources[].DirectStreamUrl = http://...:1888/Videos/.../stream ← 客户端实际播放，走反代拿副本
```

**不要把两个字段都设成 `/strm` 地址**：部分客户端（如 SenPlayer）会优先用
`Path` 播放，直接打到 PanSave 的 `/strm` 通道，那条通道不识别设备，不会分配副本，
多人播放照样 403。

局限：第二个设备首次播放时 PanSave 要真正复制一份文件，约 1–1.5 秒；
副本刚创建时 115 CDN 可能还没索引到，客户端立刻播放会收到 403，过几秒重进即可。
这段等待发生在 PanSave/115 侧，LiMe 无法干预。Yamby / 爆米花仍返回相对路径，
不参与该模式。

### 起播加速：默认 auto，不需要写环境变量

两个开关都支持三态，**不写就是 auto**：

| 值 | 行为 |
|---|---|
| `auto`（默认） | 服务端先尝试跟掉 PanSave 一跳，最多等 500ms；成功就把最终 CDN 地址交给客户端，失败/超时回退原 302。连续失败 3 次后熔断 5 分钟，全部直接走回退 |
| `true` | 强制解析。保持原有严格行为：上游 4xx/5xx 可能返回 502 |
| `false` | 关闭，客户端自己跟 PanSave 的 302 |

> 老 compose 里已经写了 `true` / `false` 的，会继续按写的值执行，不会因为升级
> 改变行为。想用 auto 的超时回退/熔断，把对应变量改成 `"auto"` 或直接删掉那行；
> 什么都不写就是 `auto`。

`LIME_PLAYBACK_DIRECT_URL` 在 auto 模式下只对会优先读 `DirectStreamUrl` 的客户端
（VidHub、SenPlayer、Yamby、Infuse 等）预解析，其它客户端保持 LiMe 地址，不会
额外等解析。显式写 `true` 才会对所有客户端生效。

```
客户端 → PlaybackInfo（DirectStreamUrl = https://xxx.115cdn.net/...mp4）
      → 直接连 CDN，跳过 LiMe 的 302
```

- 显式 `true` 时列表接口（`/Items`、`/Shows/{id}/Episodes`）也会预解析第一片源，
  最多等 600ms；auto 模式只对上述客户端做。超时保持 LiMe 地址，不影响列表可用性
- 外部访问走 Cloudflare / 反向代理时收益更明显，省掉一次公网往返
- 实现在 `internal/api/resolve_mode.go`：短超时、负缓存、熔断都在这里，任何
  一步失败都不会阻断播放

### 时长

TMDB 刮削会写回电影和单集的 `RunTimeTicks`；对升级前已刮削、时长为 0 的条目，
启动刮削时会自动补一轮（`run_time_ticks = 0` 的电影与单集）。有真实时长后，
客户端进度条和 90% 自动判定已看才能正常工作。

> 时长未知（`RunTimeTicks = 0`）时，服务端无法自动判定「看完」，
> 需要客户端显式调用 `PlayedItems` 标记；进度条百分比也要有真实时长才会返回。

**文件名解析**支持常见命名：

```
剧名.2026.S01E01.2160p.25fps.WEB-DL.SDR.H.265.8bit.AAC2.0-HHWEB.mp4
  → 剧名 / 2026 / S01E01 / 2160p / hevc / aac 2ch / mp4

电影名.2024.1080p.WEB-DL.H.264.DDP5.1.mkv
  → 电影名 / 2024 / 1080p / h264 / eac3 6ch / mkv
```

## 刮削

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
- **海报回填**：海报为空的条目也会进回填队列并排在最前，避免「刮过但当时 TMDB 还没图」的片永远缺海报
- 头像回填并发执行，共用 50ms 全局限速；头像按需下载
- **手动识别**：目录名里的 TMDB ID 错了或没写，管理员可在详情页搜索 TMDB 改绑，
  电影重刮单条，剧集连同所有季一起重刮
- **尺寸可选**：海报和剧照的下载尺寸都能在运维面板切换；改尺寸后点「重新下载」
  按新尺寸回填，旧尺寸文件会自动清理
- **单集封面策略**：默认继承剧集大海报（省空间、省请求）；也可切回 TMDB 16:9 剧照，
  支持按尺寸重下、清理旧剧照，每个单集只保留一个版本
- 年份排序以 TMDB 首映/上映日期为准，不再被目录名里的年份带偏

> 国内直连 `api.themoviedb.org` 不通时，设置 `LIME_TMDB_PROXY`
> （NAS 上 mihomo 监听 `7890`，容器里要用宿主机 IP，不能写 `127.0.0.1`）。
> API Key 建议放在 compose 同目录的 `.env`，该文件已在 `.gitignore` /
> `.dockerignore` 中排除，同步源码时也不要带过去。

## 本地元数据（NFO / 本地图片）

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
> 唯一索引，需要清库重扫（见下面「多版本合并」的说明）。

## 外挂字幕

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

## 多版本合并

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

## 搜索

`items_fts` 用 SQLite FTS5 trigram 索引，增删改由触发器同步：

- ≥3 个字符走 FTS：实测「流浪地球」1.2ms、「蜘蛛侠」0.5ms
- 2 个字符的中文（如「三体」）仍走 `LIKE`，trigram 的固有限制

## 部署模式

两种都能用，**推荐直连**：少一跳反代，客户端直接吃 LiMe 的 302。

### 模式 A：客户端直连 LiMe（推荐）

```
客户端 → https://lime.example.com（CF Tunnel / 端口映射）
      → LiMe 886 → 302 → 115/123 CDN → 播放
```

- `LIME_RESOLVE_REDIRECT` 默认 `auto`：LiMe 在返回 302 前先跟一跳 PanSave，
  把最终 CDN 地址交给客户端；失败自动回退。PanSave 的 16868 不需要暴露到公网
- `LIME_INTERNAL_URL` 留空即可：PlaybackInfo 里的 `Path` 直接用客户端访问的域名。
  就算填了也不影响公网客户端，见下面「自动判断」
- CDN 直链的签名与请求 UA 绑定，LiMe 会透传客户端 UA，否则会 403
  `invalid signature`

### 模式 B：PanSave 反代（只有反代端口对外）

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
# LIME_RESOLVE_REDIRECT 默认 auto，不用写；要强制严格解析再设 true
```

### `LIME_INTERNAL_URL` 是自动判断的

同一个实例可以同时服务两种客户端，不需要二选一：

- 请求的 `Host` 是内网地址（`192.168.x.x`、`localhost`、裸主机名）→ 用
  `LIME_INTERNAL_URL`
- 请求来自公网域名（经 CF Tunnel / 端口映射）→ 用请求本身的
  `scheme://host`，并优先采信 `X-Forwarded-Proto` / `X-Forwarded-Host`

所以把 `LIME_INTERNAL_URL` 配好，公网客户端走域名访问照样能播；
只想纯直连的话留空即可。

> 局域网内可以把 `LIME_RESOLVE_REDIRECT` 设为 `false`，让客户端沿 PanSave
> 自己的 302 链走；公网保持默认 `auto` 即可，失败会自动回退。

> 要启用 PanSave 的 **115 复制同播**（多人同时看同一部）时，反代入口和域名
> 直接在运维面板「播放设置 → 多人同播」里填，不需要写 `LIME_INTERNAL_URL` /
> `LIME_PROXY_HOSTS`；环境变量仍然作为兜底。详见「播放链路 → PanSave 115 复制同播」。

### Emby 兼容

所有接口都同时接受 `/emby/...` 前缀（去掉前缀后走同一套路由），该前缀下
`ProductName` 返回 `Emby Server`。PanSave 的 Emby 配置校验依赖：

- `GET /emby/System/Info`
- `GET /emby/Library/VirtualFolders/Query`
- `GET /emby/Library/SelectableMediaFolders`
- `GET /emby/Items`、`GET /emby/Users`

## 更新记录

当前版本 **v1.3.6**。

| 版本 | 内容 |
|---|---|
| v1.3.6 | `PlaybackInfo` 返回标准字段 `StartPositionTicks`：已看完的集为 0，未看完为上次位置。修正 VidHub 切集时进度条停在上集时间的问题 |
| v1.3.5 | `PlaybackInfo` 加 30 秒响应缓存（按 设备 + 条目 + 片源 + UA），客户端起播前的连续探测不再重复查库 |
| v1.3.4 | 同一设备 10 分钟内重进同一视频复用 `PlaySessionId`，避免 PanSave 每换会话就复制一份新副本 |
| v1.3.3 | 待处理页默认每类 50 条（原 200），响应从 75KB 降到 25KB；实测并行查询在同一进程内反而更慢，保持串行 |
| v1.3.1 | 待处理页优化：启动后后台预热失败计数缓存、前端 60 秒内复用结果、支持 `?limit=` 显式调整条数 |
| v1.3.0 | 新增 `/health/deep`：查一次数据库（2 秒上限），返回 uptime、goroutine、内存和数据库延迟；容器 healthcheck 已切换 |
| v1.2.9 | 起播加速（跟跳 / DirectStreamUrl）与播放鉴权搬到面板，运行时 `atomic` 读取，保存即时生效 |
| v1.2.8 | 运维面板新增「多人同播」：模式、反代入口、反代域名；自动记录最近见过的 Host 供直接勾选 |
| v1.2.7 | 设备 ID 缺失时用 token 反查登录记录，保证 PanSave 能区分第二台设备 |
| v1.2.6 | 播放 URL 附带 `DeviceId` 参数：部分客户端请求 stream 时不带设备头 |
| v1.2.5 | 支持 PanSave 公网反代域名（`LIME_PROXY_HOSTS`），公网入口也能分配副本 |
| v1.2.4 | 内网直连 LiMe 端口时引导到反代入口，886 等入口同样能触发同播 |
| v1.2.3 | 同播模式下不再预解析 CDN 直链，避免客户端绕过反代 |
| v1.2.2 | 修正 `LIME_INTERNAL_URL` 覆盖反代地址的问题 |
| v1.2.1 | `LIME_MEDIA_PATH_STRM` 默认 auto：按请求来源自动判断，无需配置 |
| v1.2.0 | 支持 PanSave 115 复制同播：`MediaSources[].Path` 返回 STRM 原文供反代识别，播放地址仍走反代 |
| v1.1.3–v1.1.10 | 片源名补帧率与大小、播放日期与最后一集日期排序、CD 分盘按番号合并、清理空壳条目前先迁移播放记录，以及刮削、扫描的多项修复 |
| v1.1.2 | 补齐客户端下载 / 离线缓存契约：新增 `/Items/{id}/Download`、`/Items/{id}/File`，默认 302 到 CDN，不占服务端带宽；条目补 `CanDownload`，用户策略的 `EnableContentDownloading` 与下载端点统一开关；Web UI 片源行新增「下载」，Android App 内走原生下载桥。`LIME_DOWNLOAD_MODE=proxy` 可兜底不跟 302 的下载器，`off` 关闭下载 |
| v1.1.1 | 修正 SQLite 页缓存默认值：此前 config 一直传 64MB，覆盖了 store 层 32MB 的默认值，8 个连接上限 512MB；现在默认 32MB（合计上限 256MB），大库启动内存峰值明显下降。启动时的全库统计、图片统计、片头片尾待分析计数改为错峰执行，避免多个全表扫描同时填满页缓存 |
| v1.1.0 | 起播加速改为默认 `auto`：不写环境变量也会自动跟掉 PanSave 一跳并给 CDN 直链；500ms 超时、失败回退、连续失败 3 次熔断 5 分钟。`LIME_PLAYBACK_DIRECT_URL` 的 auto 只对 VidHub / SenPlayer / Yamby / Infuse 预解析；显式 `true`/`false` 保持原行为，老 compose 无需修改。系统 → 播放新增当前模式展示 |
| v1.0.60 | 修复「继续观看」：同一部剧多个单集都有进度时，只显示最近看的一集，不再整季铺满同一张海报；TotalRecordCount 和分页同步按合并结果计算 |
| v1.0.59 | 运维面板性能优化：全库统计、刮削失败汇总、图片目录统计加缓存与启动预热；系统页图片统计改为异步加载，不再阻塞首屏；带版本号的 JS/CSS 改为长缓存，入口 HTML 继续回源校验；概览与系统页打开明显更快 |
| v1.0.58 | 新增「待处理」页：没 TMDB ID / 刮削失败 / 缺图 / 重复条目集中展示，支持单条与批量重试；新增图片孤儿清理、扫描/刮削/备份任务历史、卡顿阈值面板可调、媒体库刮削状态筛选；系统页按常规/刮削与图片/播放/高级分组；修复暂停、切后台时误报播放卡顿 |
| v1.0.57 | 详情页新增「手动识别」：管理员可搜索 TMDB、选结果或直接填 ID，自动改绑并重新刮削；完善 README 与仓库介绍 |
| v1.0.56 | 海报尺寸可选 w342 / w500 / w780 / original；单集封面默认继承剧集大海报，可切回 TMDB 剧照；剧照尺寸可选 w92 / w185 / w300 / original，重下时只保留一个版本；新增单集封面清理 |
| v1.0.55 | 片头片尾支持按剧名精准提取；指纹并发与低码率片源可调 |
| v1.0.54 | Web UI 启用新 logo，重排登录页与侧栏品牌区 |
| v1.0.53 | 数据库备份目录可配置 |
| v1.0.52 | 播放前读取磁盘上的 `.strm` 校验地址；PanSave 批量重生成 pickcode 后自动修复数据库旧地址，长剧不再沿用旧链接 |
| v1.0.51 | PanSave 直链解析改用独立短连接（禁用 keep-alive），避免复用被上游静默关闭的空闲连接导致 8 秒超时；超时负缓存从 30 秒降到 5 秒，客户端取消不再缓存 |
| v1.0.50 | 播放起播只预热 2 集，避免一次性把整部未探测剧压给 PanSave 导致直链解析超时；单集看满 10 分钟后的整剧补探测限制为 50 集/次 |
| v1.0.49 | 片头片尾回填：整季缺失自动跑音频指纹、新集指纹采样失败自动整季重试、失败记录 7 天内不重复扫；默认只回填最近 14 天看到 ≥10 分钟的剧，老剧等观看到 10 分钟后再补；整季采样上限 16 集；运维面板新增「补齐缺失指纹」 |
| v1.0.43 | Infuse 8.x 兼容：补齐 `/UserViews`、`/UserViews/GroupingOptions`、`/Users/{id}/GroupingOptions`、`/UserItems/Resume`、`/Items/Latest`、`/UserItems/{id}/UserData`、`/UserFavoriteItems` 等接口；登录支持 query/form；Views 补 `DateCreated`；`DisplayPreferences` 字段类型修正；支持 camelCase 查询参数、`/Users/{id}/Items/` 尾斜杠、库 ID 当 item 查询；新增 `/Packages`；通知页待分析计数改后台缓存 |
| v1.0.42 | 新增 Telegram 入库通知：刮削完成后推送、同剧同季多集合并、电影逐部通知、16:9 海报降级、独立/共用 Bot、批量抑制与失败重试 |
| v1.0.41 | Web UI：继续观看中的单集统一用剧集海报；新增 `SeriesPrimaryImageTag`；手机端重做顶栏/底部导航、详情页、筛选、播放器与安全区适配 |
| v1.0.40 | 类型筛选/相似推荐改走 `item_genres`；筛选缓存过期后先返回旧值再后台刷新；新增磁盘/DB 延迟指标与 TG 健康告警；Random 排序改用随机窗口 |
| v1.0.39 | 快扫快照改用 64 位路径哈希 + 流式修剪，整库路径 map 内存约降到 1/3–1/4 |
| v1.0.38 | 启动时清理没有片源的电影/单集及变空的季、剧，修复历史遗留空壳条目 |
| v1.0.37 | 增量扫描的 changedIDs 每 2 万条分批展开演员，避免首次快扫时内存无限累积 |
| v1.0.36 | 演员筛选改走 `IN (SELECT item_id ...)`，让 SQLite 从 `item_persons` 索引驱动（55 条结果从 545ms 降到 1ms） |
| v1.0.35 | 演员筛选改 `item_persons` 关联表，不再全表解析 people JSON；扫描器单次扫描内每部剧/每季只写一次 |
| v1.0.34 | 大库优化：筛选面板 5 分钟缓存、补充索引、SQLite 参数调优、支持 `EnableTotalRecordCount=false`、启动补数据后台化、watch 目录数上报、卡顿记录保留 90 天 |
| v1.0.33 | Telegram 播放通知带 16:9 背景图（退回竖版海报）；新增播放卡顿监控、落库与告警 |
| v1.0.32 | 匹配段中心重新校准 lag，修正粗匹配半秒偏差导致的片尾起点偏早 |
| v1.0.31 | 边界回收阈值提到 0.7、最多回收 16 秒，修正片尾仍偏早 |
| v1.0.30 | 片头片尾边界用 4 秒窗收紧，避免片尾在正片结束前就跳 |
| v1.0.29 | 片头片尾 lag 搜索改 0.5 秒步长，修复半秒偏差导致整段识别不到 |
| v1.0.28 | 匹配窗从 3 秒放宽到 8 秒，避免片尾被截短后过不了长度门槛 |
| v1.0.27 | 季中换 OP/ED 支持单伙伴长匹配；只补缺失类型；解除 30 分钟去重误锁；章节名支持 OP2/ED2/NCOP/NCED；新增重分析接口 |
| v1.0.26 | 新集章节为空时自动补跑本季音频指纹 |
| v1.0.25 | 新入库条目立即探测并提取片头片尾，不等整轮扫描 |
| v1.0.24 | 年份排序以 TMDB 首播/上映日期为准 |
| v1.0.23 | 音频指纹识别片头片尾（无章节的剧可用），Emby 章节标记 |
| v1.0.22 | 手动标记片头片尾接口 `POST /web/api/segments` |
| v1.0.21 | 片段回填按观看状态排序，播放 10 / 20 分钟二次触发 |
| v1.0.20 | 片头片尾提取（章节）+ 预热与在看自动补齐 |
| v1.0.19 | 运行日志导出、运维面板限管理员、图标改内联 SVG |
| v1.0.18 | 已播判定改为 90%，进度心跳也参与；Stopped 位置为 0 时回退最后进度 |
| v1.0.16 | 会话 30 分钟不上屏、24 小时清理；会话并入概览；面板新增运行日志页 |
| v1.0.15 | 演员筛选：persons 索引、`PersonIds` 反查、本地 `tvshow.nfo` 演员挂到 Series |
| v1.0.6 | 新 logo 与青柠绿主题、公网直连不再需要 PanSave 中转 |
| v1.0.5 | 相似推荐打分、海报回填修复、同目录 STRM 归并、时长回填 |

**已完成**：服务骨架、认证、STRM 扫描、媒体库浏览、302 播放、续播、
TMDB 刮削、用户与 API Key、运维面板、实时监控、片头片尾、PanSave 115
复制同播、播放设置面板、深度健康检查。

**待做**：官方客户端跳过按钮（需要把伪装版本号提到 10.10）、
无章节剧的音频指纹覆盖率继续提升、原生 Web UI 播放器加跳过按钮。

## 已实现接口

```
GET  /health | /health/deep       ← 后者查库 + 运行时快照，数据库异常返回 503
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
GET  /Genres | /Persons | /Studios
GET  /Items/{itemId}
GET  /Items/{itemId}/Similar
GET  /Shows/{seriesId}/Seasons | /Episodes
GET  /Shows/NextUp                  ← 「接下来看」
GET  /MediaSegments/{itemId}        ← 片头片尾
POST /Library/Refresh[?mode=fast]    ← 默认全量，fast 为增量
POST /Library/Media/Updated          ← PanSave 入库通知，按路径匹配库后增量扫描
POST /Items/{itemId}/Refresh          ← 只重解析该条目（剧集含其单集）
GET  /Library/VirtualFolders | /VirtualFolders/Query | /SelectableMediaFolders
GET  /Library/MediaFolders
GET  /QuickConnect/Enabled

# 播放
GET|POST /Items/{itemId}/PlaybackInfo
GET  /Videos/{itemId}/stream        ← 302 到 PanSave/网盘
GET  /Videos/{itemId}/{sourceId}/Subtitles/{index}/Stream.{format}
GET  /Videos/{itemId}/Subtitles/{index}/Stream.{format}
GET  /Audio/{itemId}/stream
POST /Sessions/Playing | /Progress | /Stopped
GET  /Sessions

# 续播 / 观看状态
GET  /Users/{userId}/Items/Resume   ← 真实续播列表
GET  /UserItems/Resume              ← Infuse 8.x 新接口
GET  /Items/Latest                  ← Infuse 8.x 新接口
GET  /UserViews                     ← Infuse 8.x 新接口
GET  /UserItems/{itemId}/UserData
POST|DELETE /Users/{userId}/PlayedItems/{itemId}
POST|DELETE /Users/{userId}/FavoriteItems/{itemId}
POST|DELETE /UserPlayedItems/{itemId}
POST|DELETE /UserFavoriteItems/{itemId}

# 用户与凭据
POST   /Users/New                        ← 新建用户（管理员）
POST   /Users/{userId}                   ← 改名（本人或管理员）
DELETE /Users/{userId}                   ← 删除用户（管理员）
POST   /Users/{userId}/Password          ← 改密码 / 管理员重置
POST   /Users/{userId}/Policy            ← 管理员/禁用状态
POST   /Sessions/Logout                  ← 吊销当前 token
GET|POST /Auth/Keys                      ← 列出 / 生成 API Key（管理员）
DELETE /Auth/Keys/{key}                  ← 删除 API Key（管理员）

# 刮削
GET  /Items/{itemId}/Images/{imageType}   ← 海报/背景图（本地文件）
GET|POST /web/api/scrape                  查询 / 触发刮削

# 运维面板
GET  /                 → 媒体界面
GET  /web/             运维面板（管理员）
GET  /web/api/stats    统计 + 扫描/刮削状态 + 磁盘/DB 延迟/WatchDirs
GET  /web/api/logs     运行日志（环形缓存 1000 条）
GET  /web/api/logs/export  导出日志 .txt
GET|POST /web/api/scan 查询 / 触发扫描
GET|POST /web/api/libraries             列出 / 新建媒体库
PUT|DELETE /web/api/libraries/{id}      修改 / 删除媒体库
POST /web/api/libraries/{id}/scan       只扫描指定库
GET  /web/api/browse?path=/strm         列出可选的子目录
GET|PUT  /web/api/settings              读写面板设置（含 Telegram、实时监控、片头片尾）
GET|POST /web/api/scrape                查询 / 触发刮削
GET  /web/api/scrape/failures           刮削失败列表（?format=csv 导出）
GET|POST /web/api/images/posters        海报回填
GET|POST /web/api/images/backdrops      背景图回填
GET  /web/api/playback-history          播放历史（保留 7 天）
GET  /web/api/stalls                    播放卡顿记录（?days=&limit=）
POST /web/api/segments/scan             批量补齐片头片尾
POST /web/api/segments/fingerprint/backfill  补齐整季缺失的音频指纹
POST /web/api/segments                  手动写入片头片尾
POST /web/api/segments/reset            清除片段与分析状态，重新分析
POST /web/api/segments/fingerprint      跑指定季的音频指纹
GET  /web/api/play-target               获取播放器协议链接
GET  /web/api/playlist.m3u              导出 m3u
POST /web/api/notify/test               发送播放通知测试消息
POST /web/api/notify/ingest-test        发送入库通知测试消息
POST /web/api/backup                    立即备份数据库
```

`/Items`、`/Items/{id}`、`/Items/Resume` 返回的 `UserData` 带真实数据：
`PlaybackPositionTicks`、`Played`、`PlayCount`、`IsFavorite`、`PlayedPercentage`，
剧集/季额外带 `UnplayedItemCount`（未看集数角标）。

## 自测脚本

- `tools/verify_m5.py`：登录 → 上报进度 → 续播列表 → 标记已看 → 收藏
- `tools/verify_m4.py`：刮削状态 → 元数据/海报 → 图片端点 → 重扫不丢数据

```bash
ADMIN_PASS=你的密码 python tools/verify_m5.py
ADMIN_PASS=你的密码 python tools/verify_m4.py
```

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
  probe/                  ffprobe 探测（轨道、时长、章节、音频指纹）
  scrape/                 TMDB 刮削、海报/背景图/logo 回填
  tmdb/                   TMDB API 客户端 + 图片下载
  notify/                 Telegram 播放通知
  pinyin/                 拼音与首字母排序
  webui/                  内嵌媒体界面（app/）与运维面板
  store/                  SQLite 访问 + 迁移
    migrations/           SQL 迁移，按文件名顺序执行
```

## 社区与许可证

- **频道** <https://t.me/Lime_see>：版本发布、本次更新内容、已知问题
- **群组** <https://t.me/Lime_seee>：使用讨论、问题反馈
- **Docker Hub** <https://hub.docker.com/r/lixxh/lime>

打 `v*` tag 后，CI 会把版本号和更新列表自动推送到频道。

MIT。本项目按 Jellyfin 的公开 API 契约独立实现，与 Jellyfin 项目无隶属关系。

内置的封面字体 `NotoSansSC-Black-gb.ttf` 来自 Noto Sans SC，按 SIL Open Font
License 1.1 分发，许可证见 `internal/api/assets/fonts/OFL.txt`。
