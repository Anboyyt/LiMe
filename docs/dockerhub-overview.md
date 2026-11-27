# LiMe

**轻量 Jellyfin 兼容媒体服务器 · 只适配 PanSave**

兼容 Jellyfin 客户端、爆米花、SenPlayer、VidHub —— **SenPlayer 体验最佳**。

服务端代客户端跟一跳 PanSave 的 302；**外网直接访问 LiMe 就能播放**，
视频流不再绕经 PanSave 反向代理。

```
客户端 → LiMe → 302 → 115/123 CDN → 播放
```

默认自动开启：LiMe 在服务端把 PanSave 那一跳跟掉，直接把最终 CDN 地址交给
客户端 —— PanSave 的反代端口完全不用暴露到公网。PlaybackInfo 的
`DirectStreamUrl` 也会按客户端类型自动给 CDN 直链，起播再少一次 302 往返。
PanSave 慢或连不上时会自动回退原 302，不会把起播卡死。

两个开关都支持 `auto` / `true` / `false` 三态，不写就是 `auto`，不需要额外配置。

> 服务端不接触视频字节、不转码、不缓存 URL。播放大库时 LiMe 几乎不占上传带宽。

## 特性

- **Jellyfin / Emby 兼容**：同时接受 `/emby/...` 前缀，PanSave 的 Emby 校验可直接通过
- **302 直连**：服务端只发重定向，不吃带宽、不做转码
- **多人同播**：同一视频第二台设备由 PanSave 自动分配网盘副本，避开同一直链并发 403（`LIME_MEDIA_PATH_STRM=true`）
- **下载 / 离线缓存**：兼容 `Items/{id}/Download`、`Items/{id}/File`，默认 302 到 CDN；`LIME_DOWNLOAD_MODE=proxy` 可兜底不跟 302 的下载器
- **STRM 索引**：扫描 `.strm`，自动识别电影 / 剧集 / 季 / 单集，同一部片多版本归并
- **TMDB 刮削**：按目录里的 `{tmdb-ID}` 精确刮削，海报、背景、Logo、简介、演员、评分
- **手动识别**：详情页按标题搜索 TMDB，选中结果或直接填 ID，自动改绑并重新刮削
- **待处理**：没 TMDB ID / 刮削失败 / 缺图 / 重复条目集中展示，支持单条与批量重试
- **图片尺寸**：海报 w342–original、剧照 w92–original 可选；单集封面可继承剧集大海报，也可单独用剧照
- **运维维护**：图片孤儿清理、任务历史、卡顿阈值面板可调、运行诊断
- **本地元数据**：识别同目录 `.nfo` 和 `poster.jpg` / `fanart.jpg`，本地优先
- **实时监控**：新 `.strm` 落盘后自动入库并精准刮削，删除/改名同步清理
- **片头片尾**：章节 + 音频指纹两种来源，返回 `MediaSegments`，支持手动标记
- **多用户**：独立播放进度、收藏、已看状态，支持按库授权
- **相似推荐**：按类型 / 演员 / 评分 / 年份打分，电影推电影、剧集推剧集
- **本地播放器**：可唤起 PotPlayer / VLC / MPV / IINA / Infuse
- **播放通知与卡顿监控**：Telegram 推送播放事件，带 16:9 背景图；卡顿自动告警并落库
- **入库通知**：刮削后推送 Telegram，同剧同季多集合并、电影逐部通知，支持独立 Bot
- **自带 Web UI**：`/` 原生媒体界面，`/web/` 运维面板，无前端框架；详情页支持手动识别
- **单二进制**：Go 静态编译，SQLite 存储，无外部数据库依赖

## 客户端兼容

| 客户端 | 状态 |
|---|---|
| **SenPlayer** | ✅ 最佳（针对性适配过） |
| VidHub | ✅ |
| 爆米花（Filmly） | ✅ |
| Infuse | ✅ |
| Jellyfin 官方 Android / Android TV | ⚠️ 基础可用（片头跳过按钮需服务端 ≥ 10.10，暂不显示） |
| jellyfin-web | ❌ 浏览器播不了 mkv / H.265（服务端不转码） |

## 快速开始

### Docker Compose（推荐）

新建 `docker-compose.yml` 粘贴即可，只改 STRM 路径：

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

完整模板见 GitHub 仓库的 `docker-compose.example.yml`。

### docker run

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

启动后打开 `http://<服务器IP>:886` 登录，运维面板在 `/web/`（仅管理员可进）。

## 常用环境变量

| 变量 | 说明 | 默认 |
|---|---|---|
| `LIME_ADMIN_USER` | 初始管理员 | `admin` |
| `LIME_ADMIN_PASSWORD` | 初始密码（仅首次生效） | `password` |
| `LIME_LIBRARIES` | 媒体库定义：`名称\|容器内路径\|类型` | 自动探测 `/strm` |
| `LIME_BACKUP_DIR` | 数据库每日快照目录，留空写数据目录 | 空 |
| `LIME_BACKUP_KEEP` | 备份保留份数 | `3` |
| `LIME_LIBRARY_ROOTS` | 面板新建库允许的根路径 | `/strm` |
| `LIME_SCAN_ON_START` | 启动时全量扫描 | `true` |
| `LIME_SCAN_DAILY_AT` | 每日增量扫描时间 `HH:MM`，留空关闭 | 空（面板默认 03:30） |
| `LIME_REALTIME_MONITOR` | 实时监控媒体目录 | `true` |
| `LIME_REALTIME_RECONCILE` | 启动时磁盘与数据库对账 | `true` |
| `LIME_RESOLVE_REDIRECT` | 302 前先跟一跳 PanSave，直接给 CDN 地址；`auto` 500ms 超时并失败回退/熔断，`true` 严格，`false` 关闭 | `auto` |
| `LIME_PLAYBACK_DIRECT_URL` | `DirectStreamUrl` 直接给 CDN 直链；`auto` 只对会用该字段的客户端预解析，`true` 对所有客户端，`false` 关闭 | `auto` |
| `LIME_INTERNAL_URL` | PanSave 反代用的内网上游地址 | 空 |
| `LIME_MEDIA_PATH_STRM` | `MediaSources[].Path` 返回 STRM 原文，供 PanSave 启用 115 复制同播；`auto` 只在请求来自内网反代时启用 | `auto` |
| `LIME_REWRITE_ENABLED` | 改写 STRM 里的 302 地址 | `false` |
| `LIME_REWRITE_FROM` / `LOCAL` / `PUBLIC` | 地址改写规则 | — |
| `LIME_STREAM_REQUIRE_AUTH` | 播放端点强制校验 token | `false` |
| `LIME_TMDB_API_KEY` | TMDB Key，留空关闭刮削 | 空 |
| `LIME_TMDB_PROXY` | TMDB 代理（国内需要，写宿主机 IP） | 空 |
| `LIME_SCRAPE_TIMEOUT` | 单次刮削上限，到点正常暂停 | `6h` |
| `LIME_SEGMENT_EXTRACT` | 是否提取片头片尾 | `true` |
| `LIME_SEGMENT_SCOPE` | 存量回填范围：`favorites`/`watching`/`recent`/`all` | `watching` |
| `LIME_SEGMENT_AUTO_AFTER` | 观看多久后补齐整剧；`0` 关闭 | `10m` |

完整列表见 GitHub 仓库 README。

## 镜像

- 平台：`linux/amd64`、`linux/arm64`
- 标签：`latest`、`1.0`、`1.0.x`、`edge`（`main` 分支自动构建）
- 基础镜像：`alpine` + `ffmpeg`（用于 ffprobe 探测和音频指纹）

## 链接

- GitHub：https://github.com/Anboyyt/LiMe
- Telegram 频道：https://t.me/Lime_see
- Telegram 群组：https://t.me/Lime_seee
- 许可证：MIT
- 注意：本项目按 Jellyfin 公开 API 契约独立实现，与 Jellyfin 项目无隶属关系

> 本页由 GitHub Actions 在 `docs/dockerhub-overview.md` 变更时自动同步到 Docker Hub。
