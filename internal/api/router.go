package api

import (
	"io/fs"
	"net/http"

	"lime/internal/webui"
)

func (h *Handler) routes(appFS fs.FS) http.Handler {
	mux := http.NewServeMux()

	// 媒体界面：/app/ 下是静态资源，/ 返回入口页
	mux.Handle("GET /app/", noCache(http.StripPrefix("/app/", http.FileServerFS(appFS))))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, appFS, "index.html")
	})

	// 健康检查
	mux.HandleFunc("GET /health", h.health)

	// 运维面板：内嵌静态页 + 几个只读接口。
	// 静态文件每次都要回源校验，否则更新后浏览器还在用旧 JS。
	mux.Handle("GET /web/", noCache(http.StripPrefix("/web/", http.FileServerFS(webui.OpsFS))))
	mux.HandleFunc("GET /web", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/web/", http.StatusFound)
	})
	mux.HandleFunc("GET /web/api/stats", h.webStatsHandler)
	mux.HandleFunc("GET /web/api/libraries", h.webLibraries)
	mux.HandleFunc("POST /web/api/libraries", h.webLibraries)
	mux.HandleFunc("GET /web/api/browse", h.webBrowseDirs)
	mux.HandleFunc("PUT /web/api/libraries/{id}", h.webLibraryByID)
	mux.HandleFunc("DELETE /web/api/libraries/{id}", h.webLibraryByID)
	mux.HandleFunc("POST /web/api/libraries/{id}/scan", h.webLibraryScan)
	mux.HandleFunc("POST /web/api/libraries/{id}/move", h.webLibraryMove)
	mux.HandleFunc("POST /web/api/libraries/reorder", h.webLibraryReorder)
	mux.HandleFunc("GET /web/api/users/{userId}/libraries", h.userLibraries)
	mux.HandleFunc("PUT /web/api/users/{userId}/libraries", h.userLibraries)
	mux.HandleFunc("GET /web/api/settings", h.webSettings)
	mux.HandleFunc("PUT /web/api/settings", h.webSettings)
	mux.HandleFunc("POST /web/api/backup", h.webBackup)
	mux.HandleFunc("GET /web/api/images/backdrops", h.webBackdrops)
	mux.HandleFunc("POST /web/api/images/backdrops", h.webBackdrops)
	mux.HandleFunc("GET /web/api/images/posters", h.webPosters)
	mux.HandleFunc("POST /web/api/images/posters", h.webPosters)
	mux.HandleFunc("GET /web/api/scrape/failures", h.webScrapeFailures)
	mux.HandleFunc("GET /web/api/scan", h.webScan)
	mux.HandleFunc("POST /web/api/scan", h.webScan)
	mux.HandleFunc("GET /web/api/scrape", h.webScrape)
	mux.HandleFunc("POST /web/api/scrape", h.webScrape)
	mux.HandleFunc("GET /web/api/playback-history", h.webPlaybackHistory)
	mux.HandleFunc("POST /web/api/notify/test", h.webNotifyTest)
	// Jellyfin 客户端的 WebSocket（官方客户端也会连），保持握手可用
	mux.HandleFunc("GET /socket", h.socket)

	// 系统信息
	mux.HandleFunc("GET /System/Info/Public", h.publicSystemInfo)
	mux.HandleFunc("GET /System/Info", h.systemInfo)
	mux.HandleFunc("GET /System/Configuration", h.systemConfiguration)
	mux.HandleFunc("GET /System/Configuration/public", h.systemConfigurationPublic)
	mux.HandleFunc("GET /System/Configuration/{key}", h.systemConfigurationByKey)
	mux.HandleFunc("GET /System/Ext/ServerDomains", h.serverDomains)
	mux.HandleFunc("GET /System/Ping", h.systemPing)
	mux.HandleFunc("GET /Plugins", h.plugins)
	mux.HandleFunc("GET /Devices", h.devices)
	mux.HandleFunc("GET /DisplayPreferences/{displayPreferencesId}", h.displayPreferences)
	mux.HandleFunc("POST /DisplayPreferences/{displayPreferencesId}", h.saveDisplayPreferences)

	// 品牌
	mux.HandleFunc("GET /Branding/Configuration", h.brandingConfiguration)
	mux.HandleFunc("GET /Branding/Splashscreen", h.brandingSplash)
	// 注意大小写：canonicalAPIPath 会把首段 branding 规范成 Branding，
	// 注册成小写永远匹配不到，表现为 404。
	mux.HandleFunc("GET /Branding/logo.png", h.brandingLogo)
	mux.HandleFunc("GET /api/danmu/{itemId}/raw", h.danmuRaw)

	// 认证
	mux.HandleFunc("POST /Users/AuthenticateByName", h.authenticateByName)
	mux.HandleFunc("GET /QuickConnect/Enabled", h.quickConnectEnabled)

	// 用户
	mux.HandleFunc("GET /Users/Public", h.publicUsers)
	mux.HandleFunc("GET /Users/Me", h.getCurrentUser)
	mux.HandleFunc("GET /Users", h.listUsers)
	mux.HandleFunc("GET /Users/{userId}", h.getUser)
	mux.HandleFunc("POST /Users/{userId}", h.updateUser)
	mux.HandleFunc("POST /Users/New", h.createUser)
	mux.HandleFunc("DELETE /Users/{userId}", h.deleteUser)
	mux.HandleFunc("POST /Users/{userId}/Password", h.updatePassword)
	mux.HandleFunc("POST /Users/{userId}/Policy", h.updateUserPolicy)
	mux.HandleFunc("GET /Users/{userId}/Images/{imageType}", h.userImage)
	mux.HandleFunc("GET /Users/{userId}/Images/{imageType}/{rest...}", h.userImage)
	mux.HandleFunc("POST /Sessions/Logout", h.logout)

	// API Key（Jellyfin 旧版接口，第三方客户端/脚本用）
	mux.HandleFunc("GET /Auth/Keys", h.apiKeys)
	mux.HandleFunc("POST /Auth/Keys", h.apiKeys)
	mux.HandleFunc("DELETE /Auth/Keys/{key}", h.apiKeys)

	// 观看状态：标记已看 / 取消已看 / 收藏
	mux.HandleFunc("POST /Users/{userId}/PlayedItems/{itemId}", h.setPlayed)
	mux.HandleFunc("DELETE /Users/{userId}/PlayedItems/{itemId}", h.setPlayed)
	mux.HandleFunc("POST /Users/{userId}/FavoriteItems/{itemId}", h.setFavorite)
	mux.HandleFunc("DELETE /Users/{userId}/FavoriteItems/{itemId}", h.setFavorite)

	// 媒体库视图
	mux.HandleFunc("GET /Users/{userId}/Views", h.userViews)
	mux.HandleFunc("GET /Users/{userId}/Items", h.queryItems)
	mux.HandleFunc("GET /Users/{userId}/Items/Latest", h.latestItems)
	mux.HandleFunc("GET /Users/{userId}/Items/Resume", h.resumeItems)
	mux.HandleFunc("GET /Users/{userId}/Items/{itemId}", h.getItem)
	mux.HandleFunc("POST /Library/Refresh", h.refreshLibrary)
	mux.HandleFunc("POST /Library/Media/Updated", h.mediaUpdated)
	mux.HandleFunc("POST /Items/{itemId}/Refresh", h.itemRefresh)
	mux.HandleFunc("GET /Library/VirtualFolders", h.virtualFolders)
	mux.HandleFunc("GET /Library/VirtualFolders/Query", h.virtualFoldersQuery)
	mux.HandleFunc("GET /Library/SelectableMediaFolders", h.selectableMediaFolders)
	mux.HandleFunc("GET /Library/MediaFolders", h.userViews)

	// 条目
	mux.HandleFunc("GET /Items", h.queryItems)
	mux.HandleFunc("GET /Items/Counts", h.itemCounts)
	mux.HandleFunc("GET /Items/Filters", h.itemFilters)
	mux.HandleFunc("GET /Genres", h.genres)
	mux.HandleFunc("GET /Persons", h.persons)
	mux.HandleFunc("GET /Persons/{name}", h.personByName)
	mux.HandleFunc("GET /Persons/{name}/Images/{imageType}", h.personImage)
	mux.HandleFunc("GET /Persons/{name}/Images/{imageType}/{rest...}", h.personImage)
	mux.HandleFunc("GET /Studios", h.studios)
	mux.HandleFunc("GET /MediaSegments/{itemId}", h.mediaSegments)
	mux.HandleFunc("GET /Items/{itemId}/Similar", h.similarItems)
	mux.HandleFunc("GET /Items/{itemId}/LocalTrailers", h.emptyItemList)
	mux.HandleFunc("GET /Items/{itemId}/SpecialFeatures", h.emptyItemList)
	mux.HandleFunc("GET /Items/{itemId}/Ancestors", h.emptyItemList)
	mux.HandleFunc("GET /Items/{itemId}/ThemeMedia", h.themeMedia)
	mux.HandleFunc("GET /Items/{itemId}/ThemeSongs", h.emptyQuery)
	mux.HandleFunc("GET /Items/{itemId}/ThemeVideos", h.emptyQuery)
	mux.HandleFunc("GET /Items/{itemId}/CriticReviews", h.emptyQuery)
	mux.HandleFunc("GET /Movies/{itemId}/Similar", h.emptyQuery)
	mux.HandleFunc("GET /Shows/{itemId}/Similar", h.emptyQuery)
	mux.HandleFunc("GET /Trailers/{itemId}/Similar", h.emptyQuery)
	mux.HandleFunc("POST /Items/{itemId}", h.updateItem)
	mux.HandleFunc("GET /Items/{itemId}", h.getItem)
	mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}", h.itemImage)
	// 客户端可能在后面带 index/tag/尺寸等任意段，整段吃掉
	mux.HandleFunc("GET /Items/{itemId}/Images/{imageType}/{rest...}", h.itemImage)
	// Emby 图片写回（封面生成器用 POST，部分工具用 PUT）
	mux.HandleFunc("POST /Items/{itemId}/Images/{imageType}", h.uploadItemImage)
	mux.HandleFunc("POST /Items/{itemId}/Images/{imageType}/{rest...}", h.uploadItemImage)
	mux.HandleFunc("PUT /Items/{itemId}/Images/{imageType}", h.uploadItemImage)
	mux.HandleFunc("PUT /Items/{itemId}/Images/{imageType}/{rest...}", h.uploadItemImage)
	mux.HandleFunc("DELETE /Items/{itemId}/Images/{imageType}", h.deleteItemImage)
	mux.HandleFunc("DELETE /Items/{itemId}/Images/{imageType}/{rest...}", h.deleteItemImage)
	mux.HandleFunc("GET /Shows/NextUp", h.nextUp)
	mux.HandleFunc("GET /Shows/{seriesId}/Seasons", h.showSeasons)
	mux.HandleFunc("GET /Shows/{seriesId}/Episodes", h.showEpisodes)

	// 播放（M3）：只回 302，不碰视频字节
	mux.HandleFunc("GET /Items/{itemId}/PlaybackInfo", h.playbackInfo)
	mux.HandleFunc("POST /Items/{itemId}/PlaybackInfo", h.playbackInfo)
	// 用整段通配再接 "stream"/"stream.mp4"，Go 的 mux 不允许段内混合字面量和通配。
	mux.HandleFunc("GET /Videos/{itemId}/{action}", h.videoStream)
	mux.HandleFunc("GET /Videos/{itemId}/{mediaSourceId}/Subtitles/{index}/{action}", h.subtitleStream)
	mux.HandleFunc("GET /Videos/{itemId}/Subtitles/{index}/{action}", h.subtitleStream)
	// Emby 客户端会在序号后面再带一段起始位置：/Subtitles/{index}/0/Stream.srt。
	// 只能注册带 mediaSourceId 的那种：不带的那条与上面 6 段路由路径重叠，
	// ServeMux 会判定两条互不更具体而 panic。
	mux.HandleFunc("GET /Videos/{itemId}/{mediaSourceId}/Subtitles/{index}/{positionTicks}/{action}", h.subtitleStream)
	mux.HandleFunc("GET /Audio/{itemId}/{action}", h.audioStream)

	// 播放状态上报（M5 续播的数据基础）
	mux.HandleFunc("POST /Sessions/Playing", h.sessionPlaying)
	mux.HandleFunc("POST /Sessions/Playing/Progress", h.sessionProgress)
	mux.HandleFunc("POST /Sessions/Playing/Stopped", h.sessionStopped)
	mux.HandleFunc("POST /Sessions/Capabilities", h.sessionCapabilities)
	mux.HandleFunc("POST /Sessions/Capabilities/Full", h.sessionCapabilities)
	mux.HandleFunc("POST /Sessions/Playing/Ping", h.sessionPing)
	mux.HandleFunc("GET /Sessions", h.listSessions)

	return withGzip(withCORS(withLogging(h.log, h.cfg.HTTPLog, stripEmbyPrefix(mux))))
}
