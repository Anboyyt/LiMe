package dto

// 字段名必须 PascalCase，数组必须是非 nil 的空切片。

type UserDto struct {
	Name                      string         `json:"Name"`
	ServerId                  string         `json:"ServerId"`
	Id                        string         `json:"Id"`
	HasPassword               bool           `json:"HasPassword"`
	HasConfiguredPassword     bool           `json:"HasConfiguredPassword"`
	HasConfiguredEasyPassword bool           `json:"HasConfiguredEasyPassword"`
	EnableAutoLogin           bool           `json:"EnableAutoLogin"`
	LastLoginDate             string         `json:"LastLoginDate,omitempty"`
	LastActivityDate          string         `json:"LastActivityDate,omitempty"`
	Configuration             map[string]any `json:"Configuration"`
	Policy                    UserPolicy     `json:"Policy"`
	PrimaryImageTag           *string        `json:"PrimaryImageTag"`
}

type UserPolicy struct {
	IsAdministrator                  bool     `json:"IsAdministrator"`
	IsHidden                         bool     `json:"IsHidden"`
	IsDisabled                       bool     `json:"IsDisabled"`
	MaxParentalRating                *int     `json:"MaxParentalRating"`
	BlockedTags                      []string `json:"BlockedTags"`
	EnableUserPreferenceAccess       bool     `json:"EnableUserPreferenceAccess"`
	AccessSchedules                  []any    `json:"AccessSchedules"`
	BlockUnratedItems                []string `json:"BlockUnratedItems"`
	EnableRemoteControlOfOtherUsers  bool     `json:"EnableRemoteControlOfOtherUsers"`
	EnableSharedDeviceControl        bool     `json:"EnableSharedDeviceControl"`
	EnableRemoteAccess               bool     `json:"EnableRemoteAccess"`
	EnableLiveTvManagement           bool     `json:"EnableLiveTvManagement"`
	EnableLiveTvAccess               bool     `json:"EnableLiveTvAccess"`
	EnableMediaPlayback              bool     `json:"EnableMediaPlayback"`
	EnableAudioPlaybackTranscoding   bool     `json:"EnableAudioPlaybackTranscoding"`
	EnableVideoPlaybackTranscoding   bool     `json:"EnableVideoPlaybackTranscoding"`
	EnablePlaybackRemuxing           bool     `json:"EnablePlaybackRemuxing"`
	ForceRemoteSourceTranscoding     bool     `json:"ForceRemoteSourceTranscoding"`
	EnableContentDeletion            bool     `json:"EnableContentDeletion"`
	EnableContentDeletionFromFolders []string `json:"EnableContentDeletionFromFolders"`
	EnableContentDownloading         bool     `json:"EnableContentDownloading"`
	EnableSyncTranscoding            bool     `json:"EnableSyncTranscoding"`
	EnableMediaConversion            bool     `json:"EnableMediaConversion"`
	EnabledDevices                   []string `json:"EnabledDevices"`
	EnableAllDevices                 bool     `json:"EnableAllDevices"`
	EnabledChannels                  []string `json:"EnabledChannels"`
	EnableAllChannels                bool     `json:"EnableAllChannels"`
	EnabledFolders                   []string `json:"EnabledFolders"`
	EnableAllFolders                 bool     `json:"EnableAllFolders"`
	InvalidLoginAttemptCount         int      `json:"InvalidLoginAttemptCount"`
	LoginAttemptsBeforeLockout       int      `json:"LoginAttemptsBeforeLockout"`
	MaxActiveSessions                int      `json:"MaxActiveSessions"`
	EnablePublicSharing              bool     `json:"EnablePublicSharing"`
	BlockedMediaFolders              []string `json:"BlockedMediaFolders"`
	BlockedChannels                  []string `json:"BlockedChannels"`
	RemoteClientBitrateLimit         int      `json:"RemoteClientBitrateLimit"`
	AuthenticationProviderId         string   `json:"AuthenticationProviderId"`
	PasswordResetProviderId          string   `json:"PasswordResetProviderId"`
}

func DefaultPolicy(isAdmin bool) UserPolicy {
	return UserPolicy{
		IsAdministrator:                  isAdmin,
		IsHidden:                         false,
		IsDisabled:                       false,
		MaxParentalRating:                nil,
		BlockedTags:                      []string{},
		EnableUserPreferenceAccess:       true,
		AccessSchedules:                  []any{},
		BlockUnratedItems:                []string{},
		EnableRemoteControlOfOtherUsers:  false,
		EnableSharedDeviceControl:        true,
		EnableRemoteAccess:               true,
		EnableLiveTvManagement:           isAdmin,
		EnableLiveTvAccess:               true,
		EnableMediaPlayback:              true,
		EnableAudioPlaybackTranscoding:   false,
		EnableVideoPlaybackTranscoding:   false,
		EnablePlaybackRemuxing:           false,
		ForceRemoteSourceTranscoding:     false,
		EnableContentDeletion:            false,
		EnableContentDeletionFromFolders: []string{},
		EnableContentDownloading:         true,
		EnableSyncTranscoding:            false,
		EnableMediaConversion:            false,
		EnabledDevices:                   []string{},
		EnableAllDevices:                 true,
		EnabledChannels:                  []string{},
		EnableAllChannels:                true,
		EnabledFolders:                   []string{},
		EnableAllFolders:                 true,
		InvalidLoginAttemptCount:         0,
		LoginAttemptsBeforeLockout:       -1,
		MaxActiveSessions:                0,
		EnablePublicSharing:              false,
		BlockedMediaFolders:              []string{},
		BlockedChannels:                  []string{},
		RemoteClientBitrateLimit:         0,
		AuthenticationProviderId:         "Jellyfin.Server.Implementations.Users.DefaultAuthenticationProvider",
		PasswordResetProviderId:          "Jellyfin.Server.Implementations.Users.DefaultPasswordResetProvider",
	}
}

type SessionInfoDto struct {
	Id                    string   `json:"Id"`
	UserId                string   `json:"UserId"`
	UserName              string   `json:"UserName"`
	Client                string   `json:"Client"`
	DeviceName            string   `json:"DeviceName"`
	DeviceId              string   `json:"DeviceId"`
	ApplicationVersion    string   `json:"ApplicationVersion"`
	IsActive              bool     `json:"IsActive"`
	PlayableMediaTypes    []string `json:"PlayableMediaTypes"`
	SupportedCommands     []string `json:"SupportedCommands"`
	SupportsRemoteControl bool     `json:"SupportsRemoteControl"`
	LastActivityDate      string   `json:"LastActivityDate"`

	// 仅 GET /Sessions 填充。
	NowPlayingItem *BaseItemDto        `json:"NowPlayingItem,omitempty"`
	PlayState      *PlayerStateInfoDto `json:"PlayState,omitempty"`
}

type AuthenticationResult struct {
	User        *UserDto        `json:"User"`
	SessionInfo *SessionInfoDto `json:"SessionInfo"`
	AccessToken string          `json:"AccessToken"`
	ServerId    string          `json:"ServerId"`
	SessionId   string          `json:"SessionId"`
}
