package dto

// PlaybackInfoResponse 对应 Jellyfin 的 PlaybackInfoDto。
// 我们不转码，因此永远只返回可直连的 MediaSource。
type PlaybackInfoResponse struct {
	MediaSources  []MediaSourceDto `json:"MediaSources"`
	PlaySessionId string           `json:"PlaySessionId"`
}

// PlaybackProgressInfo 是客户端上报播放状态时的请求体（字段可缺省）。
type PlaybackProgressInfo struct {
	CanSeek             bool   `json:"CanSeek"`
	ItemId              string `json:"ItemId"`
	MediaSourceId       string `json:"MediaSourceId"`
	PlaySessionId       string `json:"PlaySessionId"`
	PositionTicks       int64  `json:"PositionTicks"`
	IsPaused            bool   `json:"IsPaused"`
	IsMuted             bool   `json:"IsMuted"`
	PlayMethod          string `json:"PlayMethod"`
	RepeatMode          string `json:"RepeatMode"`
	SessionId           string `json:"SessionId"`
	DeviceId            string `json:"DeviceId"`
	VolumeLevel         int    `json:"VolumeLevel"`
	AudioStreamIndex    *int   `json:"AudioStreamIndex"`
	SubtitleStreamIndex *int   `json:"SubtitleStreamIndex"`
}

// PlaybackStopInfo 是停止播放上报的请求体。
type PlaybackStopInfo struct {
	ItemId        string `json:"ItemId"`
	MediaSourceId string `json:"MediaSourceId"`
	PlaySessionId string `json:"PlaySessionId"`
	PositionTicks int64  `json:"PositionTicks"`
	SessionId     string `json:"SessionId"`
	DeviceId      string `json:"DeviceId"`
	Failed        bool   `json:"Failed"`
}

// SessionInfoDto 见 dto/user.go（登录响应也在用），这里只补播放相关字段。

type PlayerStateInfoDto struct {
	PositionTicks       int64  `json:"PositionTicks"`
	CanSeek             bool   `json:"CanSeek"`
	IsPaused            bool   `json:"IsPaused"`
	IsMuted             bool   `json:"IsMuted"`
	VolumeLevel         int    `json:"VolumeLevel"`
	AudioStreamIndex    *int   `json:"AudioStreamIndex,omitempty"`
	SubtitleStreamIndex *int   `json:"SubtitleStreamIndex,omitempty"`
	PlayMethod          string `json:"PlayMethod,omitempty"`
	RepeatMode          string `json:"RepeatMode"`
}
