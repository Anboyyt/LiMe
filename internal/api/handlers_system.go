package api

import (
	"net/http"
	"strings"

	"lime/internal/dto"
)

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"name":    "LiMe",
		"version": h.cfg.Version,
	})
}

func (h *Handler) publicSystemInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, dto.PublicSystemInfo{
		LocalAddress:           h.requestBaseURL(r),
		ServerName:             h.cfg.ServerName,
		Version:                h.cfg.Version,
		ProductName:            productName(r),
		OperatingSystem:        "",
		Id:                     h.serverID,
		StartupWizardCompleted: true,
	})
}

func (h *Handler) systemInfo(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}

	writeJSON(w, http.StatusOK, dto.SystemInfo{
		PublicSystemInfo: dto.PublicSystemInfo{
			LocalAddress:           h.requestBaseURL(r),
			ServerName:             h.cfg.ServerName,
			Version:                h.cfg.Version,
			ProductName:            productName(r),
			OperatingSystem:        "",
			Id:                     h.serverID,
			StartupWizardCompleted: true,
		},
		OperatingSystemDisplayName: "",
		HasPendingRestart:          false,
		IsShuttingDown:             false,
		SupportsLibraryMonitor:     true,
		WebSocketPortNumber:        0,
		CompletedInstallations:     []any{},
		CanSelfRestart:             false,
		CanLaunchWebBrowser:        false,
		ProgramDataPath:            h.cfg.DataDir,
		WebPath:                    "",
		ItemsByNamePath:            "",
		CachePath:                  h.cfg.DataDir,
		LogPath:                    "",
		InternalMetadataPath:       "",
		TranscodingTempPath:        "",
		CastReceiverApplications:   []any{},
		EncoderLocation:            "",
		SystemArchitecture:         "X64",
	})
}

func (h *Handler) systemConfiguration(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.systemConfig())
}

func (h *Handler) systemConfigurationPublic(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.systemConfig())
}

// systemConfigurationByKey 兜底：部分客户端会请求任意配置键。
func (h *Handler) systemConfigurationByKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) systemConfig() dto.SystemConfigurationDto {
	return dto.SystemConfigurationDto{
		IsStartupWizardCompleted:      true,
		CachePath:                     h.cfg.DataDir,
		PreviousVersion:               "",
		EnableMetrics:                 false,
		EnableNormalizedItemByNameIds: true,
		HidePlayedInLatest:            false,
		PlayableMediaTypes:            []string{"Audio", "Video"},
		SupportedCommands:             []string{},
		QuickConnectAvailable:         false,
		EnableCaseSensitiveItemIds:    false,
	}
}

func (h *Handler) brandingConfiguration(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, dto.BrandingOptionsDto{
		LoginDisclaimer:     "",
		CustomCss:           "",
		SplashscreenEnabled: true,
	})
}

func (h *Handler) quickConnectEnabled(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, false)
}

// requestBaseURL 返回服务端对客户端公布的基础地址。
// 配置了 LIME_INTERNAL_URL 时优先用它：反代（Cloudflare Tunnel / PanSave）会
// 透传 X-Forwarded-Proto=https，但 886 实际只监听 http，直接拼出来会得到
// https://<内网IP>:886 这种不可用地址。
func (h *Handler) requestBaseURL(r *http.Request) string {
	if h.cfg.InternalURL != "" {
		return h.cfg.InternalURL
	}
	return externalBase(r)
}

// productName 让 Emby 客户端看到 "Emby Server"：
// /emby/ 前缀直接按 Emby 应答；根路径则根据 UA / Authorization 识别
// PanSave、SenPlayer 这类按 Emby 协议实现的服务，避免它们把本服务
// 当成 Jellyfin 而走错接口。官方 Jellyfin 客户端不受影响。
func productName(r *http.Request) string {
	if isEmbyCompat(r) || isEmbyClient(r) {
		return "Emby Server"
	}
	return "Jellyfin Server"
}

func isEmbyClient(r *http.Request) bool {
	ua := strings.ToLower(r.UserAgent())
	if strings.Contains(ua, "emby") || strings.Contains(ua, "pansave") {
		return true
	}
	auth := strings.ToLower(r.Header.Get("Authorization"))
	return strings.Contains(auth, `client="emby"`)
}
