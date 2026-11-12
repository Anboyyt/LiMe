# LiMe

一个用 Go 写的轻量媒体服务器，**兼容 Jellyfin / Emby 客户端**，专为「网盘 STRM + 302 直连」场景设计。

服务端不接触视频字节、不转码、不缓存 URL —— 播放时只回一个 `302`，让客户端直连云盘 CDN。

```
客户端 → LiMe → 302 → 115/123 CDN → 播放
```

LiMe 在服务端替客户端跟一跳 PanSave 的 302，外网可以直接访问 LiMe，
播放流量不必绕经 PanSave 反代。

## 特性

- **Jellyfin 兼容**：官方 Android / Android TV / Infuse / VidHub 等客户端可直接连接
- **302 直连**：服务端只发重定向，不吃带宽、不做转码
- **STRM 索引**：扫描 `.strm` 文件，自动识别电影 / 剧集 / 季 / 单集
- **TMDB 刮削**：海报、背景、Logo、简介、演员、评分，支持中文
- **本地元数据**：识别同目录 `.nfo` 和 `poster.jpg` / `fanart.jpg`，本地优先
- **实时监控**：新 `.strm` 落盘后自动入库并精准刮削
- **多用户**：独立播放进度、收藏、已看状态，支持家长分级
- **相似推荐**：详情页按类型 / 演员 / 评分 / 年份打分，电影推电影、剧集推剧集
- **自带 Web UI**：`/` 原生媒体界面，`/web/` 运维面板，无前端框架
- **单二进制**：Go 静态编译，SQLite 存储，无外部依赖

## 快速开始

### Docker Compose（推荐）

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
      # 名称|容器内路径|类型，多个库用分号分隔
      LIME_LIBRARIES: "电影|/media/电影|movies;剧集|/media/剧集|tvshows"
      LIME_LIBRARY_ROOTS: "/media"
    volumes:
      - ./data:/data
      - /path/to/your/strm:/media:ro
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
  -v /path/to/strm:/strm:ro \
  -e LIME_LIBRARIES="电影|/strm/电影|movies;剧集|/strm/剧集|tvshows" \
  -e LIME_ADMIN_USER=admin \
  -e LIME_ADMIN_PASSWORD=change-me \
  lixxh/lime:latest
```

启动后打开 `http://<服务器IP>:886` 登录，运维面板在 `/web/`。

## 常用环境变量

| 变量 | 说明 | 默认 |
|---|---|---|
| `LIME_ADMIN_USER` | 初始管理员 | `admin` |
| `LIME_ADMIN_PASSWORD` | 初始密码（仅首次生效） | `change-me` |
| `LIME_LIBRARIES` | 媒体库定义 | — |
| `LIME_LIBRARY_ROOTS` | 面板新建库允许的根路径 | — |
| `LIME_SCAN_ON_START` | 启动时全量扫描 | `false` |
| `LIME_SCAN_DAILY_AT` | 每日增量扫描时间 `HH:MM` | `03:30` |
| `LIME_REALTIME_MONITOR` | 实时监控媒体目录 | `true` |
| `LIME_TMDB_API_KEY` | TMDB Key，留空关闭刮削 | 空 |
| `LIME_TMDB_PROXY` | TMDB 代理（国内需要） | 空 |
| `LIME_REWRITE_ENABLED` | 外网访问时改写 302 地址 | `false` |
| `LIME_REWRITE_FROM` / `LOCAL` / `PUBLIC` | 地址改写规则 | — |
| `LIME_STREAM_REQUIRE_AUTH` | 播放端点强制校验 token | `false` |
| `LIME_PROBE_ENABLED` | ffprobe 探测媒体信息 | `true` |

完整列表见 GitHub 仓库 README。

## 镜像

- 平台：`linux/amd64`、`linux/arm64`
- 标签：`latest`、`1.0`、`1.0.x`、`edge`（`main` 分支自动构建）
- 基础镜像：`alpine` + `ffmpeg`（用于 ffprobe）

## 链接

- GitHub：https://github.com/Anboyyt/LiMe
- Telegram 频道（版本更新通知）：https://t.me/Lime_see
- 许可证：MIT
- 注意：本项目按 Jellyfin 公开 API 契约独立实现，与 Jellyfin 项目无隶属关系
