package dto

type PublicSystemInfo struct {
	LocalAddress           string `json:"LocalAddress"`
	ServerName             string `json:"ServerName"`
	Version                string `json:"Version"`
	ProductName            string `json:"ProductName"`
	OperatingSystem        string `json:"OperatingSystem"`
	Id                     string `json:"Id"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
}

type SystemInfo struct {
	PublicSystemInfo
	OperatingSystemDisplayName string `json:"OperatingSystemDisplayName"`
	HasPendingRestart          bool   `json:"HasPendingRestart"`
	IsShuttingDown             bool   `json:"IsShuttingDown"`
	SupportsLibraryMonitor     bool   `json:"SupportsLibraryMonitor"`
	WebSocketPortNumber        int    `json:"WebSocketPortNumber"`
	CompletedInstallations     []any  `json:"CompletedInstallations"`
	CanSelfRestart             bool   `json:"CanSelfRestart"`
	CanLaunchWebBrowser        bool   `json:"CanLaunchWebBrowser"`
	ProgramDataPath            string `json:"ProgramDataPath"`
	WebPath                    string `json:"WebPath"`
	ItemsByNamePath            string `json:"ItemsByNamePath"`
	CachePath                  string `json:"CachePath"`
	LogPath                    string `json:"LogPath"`
	InternalMetadataPath       string `json:"InternalMetadataPath"`
	TranscodingTempPath        string `json:"TranscodingTempPath"`
	CastReceiverApplications   []any  `json:"CastReceiverApplications"`
	EncoderLocation            string `json:"EncoderLocation"`
	SystemArchitecture         string `json:"SystemArchitecture"`
}

type BrandingOptionsDto struct {
	LoginDisclaimer     string `json:"LoginDisclaimer"`
	CustomCss           string `json:"CustomCss"`
	SplashscreenEnabled bool   `json:"SplashscreenEnabled"`
}

type SystemConfigurationDto struct {
	IsStartupWizardCompleted      bool     `json:"IsStartupWizardCompleted"`
	CachePath                     string   `json:"CachePath"`
	PreviousVersion               string   `json:"PreviousVersion"`
	EnableMetrics                 bool     `json:"EnableMetrics"`
	EnableNormalizedItemByNameIds bool     `json:"EnableNormalizedItemByNameIds"`
	HidePlayedInLatest            bool     `json:"HidePlayedInLatest"`
	PlayableMediaTypes            []string `json:"PlayableMediaTypes"`
	SupportedCommands             []string `json:"SupportedCommands"`
	QuickConnectAvailable         bool     `json:"QuickConnectAvailable"`
	EnableCaseSensitiveItemIds    bool     `json:"EnableCaseSensitiveItemIds"`
}
