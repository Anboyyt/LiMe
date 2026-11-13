package dto

type QueryResult struct {
	Items            []*BaseItemDto `json:"Items"`
	TotalRecordCount int            `json:"TotalRecordCount"`
	StartIndex       int            `json:"StartIndex"`
}

type BaseItemDto struct {
	Name                    string            `json:"Name"`
	ServerId                string            `json:"ServerId"`
	Id                      string            `json:"Id"`
	DateCreated             string            `json:"DateCreated,omitempty"`
	Container               string            `json:"Container,omitempty"`
	SortName                string            `json:"SortName"`
	OriginalTitle           string            `json:"OriginalTitle,omitempty"`
	PremiereDate            string            `json:"PremiereDate,omitempty"`
	ProductionYear          int               `json:"ProductionYear,omitempty"`
	TmdbId                  string            `json:"TmdbId,omitempty"`
	ProviderIds             map[string]string `json:"ProviderIds,omitempty"`
	RunTimeTicks            int64             `json:"RunTimeTicks,omitempty"`
	PrimaryImageAspectRatio float64           `json:"PrimaryImageAspectRatio,omitempty"`
	Genres                  []string          `json:"Genres"`
	People                  []PersonDto       `json:"People"`
	ImageTags               map[string]string `json:"ImageTags"`
	PrimaryImageTag         string            `json:"PrimaryImageTag,omitempty"`
	// PrimaryImageItemId 是 Emby 客户端取库封面的入口；缺省时部分客户端（Sen）
	// 不会去拼接 /Items/{id}/Images/Primary，表现为封面空白。
	PrimaryImageItemId string         `json:"PrimaryImageItemId,omitempty"`
	BackdropImageTags  []string       `json:"BackdropImageTags"`
	ImageBlurHashes    map[string]any `json:"ImageBlurHashes"`
	LocationType       string         `json:"LocationType"`
	Type               string         `json:"Type"`
	MediaType          string         `json:"MediaType,omitempty"`
	IsFolder           bool           `json:"IsFolder"`
	// ChildCount 是直接子项数（剧→季数、季→集数）；
	// RecursiveItemCount 是剧的总集数。部分客户端靠它决定是否渲染集列表。
	ChildCount         *int `json:"ChildCount,omitempty"`
	RecursiveItemCount *int `json:"RecursiveItemCount,omitempty"`
	// MediaSourceCount 是片源数量；部分客户端用它判断条目是否可播放。
	MediaSourceCount  int              `json:"MediaSourceCount,omitempty"`
	ParentId          string           `json:"ParentId,omitempty"`
	SeriesId          string           `json:"SeriesId,omitempty"`
	SeasonId          string           `json:"SeasonId,omitempty"`
	SeriesName        string           `json:"SeriesName,omitempty"`
	SeasonName        string           `json:"SeasonName,omitempty"`
	IndexNumber       *int             `json:"IndexNumber,omitempty"`
	ParentIndexNumber *int             `json:"ParentIndexNumber,omitempty"`
	CollectionType    string           `json:"CollectionType,omitempty"`
	Overview          string           `json:"Overview,omitempty"`
	OfficialRating    string           `json:"OfficialRating,omitempty"`
	CommunityRating   *float64         `json:"CommunityRating,omitempty"`
	UserData          *UserItemDataDto `json:"UserData"`
	MediaSources      []MediaSourceDto `json:"MediaSources,omitempty"`
}

type PersonDto struct {
	Name            string            `json:"Name"`
	Id              string            `json:"Id"`
	Role            string            `json:"Role,omitempty"`
	Type            string            `json:"Type"`
	ImageTags       map[string]string `json:"ImageTags"`
	PrimaryImageTag string            `json:"PrimaryImageTag,omitempty"`
}

type UserItemDataDto struct {
	PlaybackPositionTicks int64    `json:"PlaybackPositionTicks"`
	PlayCount             int      `json:"PlayCount"`
	IsFavorite            bool     `json:"IsFavorite"`
	Played                bool     `json:"Played"`
	PlayedPercentage      *float64 `json:"PlayedPercentage,omitempty"`
	UnplayedItemCount     *int     `json:"UnplayedItemCount,omitempty"`
	LastPlayedDate        string   `json:"LastPlayedDate,omitempty"`
	Key                   string   `json:"Key"`
	ItemId                string   `json:"ItemId,omitempty"`
}

type MediaSourceDto struct {
	Protocol                   string           `json:"Protocol"`
	Id                         string           `json:"Id"`
	Path                       string           `json:"Path"`
	Type                       string           `json:"Type"`
	Name                       string           `json:"Name"`
	Container                  string           `json:"Container,omitempty"`
	Size                       int64            `json:"Size,omitempty"`
	Bitrate                    int64            `json:"Bitrate,omitempty"`
	IsRemote                   bool             `json:"IsRemote"`
	DirectStreamUrl            string           `json:"DirectStreamUrl,omitempty"`
	SupportsDirectPlay         bool             `json:"SupportsDirectPlay"`
	SupportsDirectStream       bool             `json:"SupportsDirectStream"`
	SupportsTranscoding        bool             `json:"SupportsTranscoding"`
	RunTimeTicks               int64            `json:"RunTimeTicks,omitempty"`
	MediaStreams               []MediaStreamDto `json:"MediaStreams"`
	DefaultAudioStreamIndex    *int             `json:"DefaultAudioStreamIndex,omitempty"`
	DefaultSubtitleStreamIndex *int             `json:"DefaultSubtitleStreamIndex,omitempty"`
}

type MediaStreamDto struct {
	Type                   string   `json:"Type"`
	Index                  int      `json:"Index"`
	Codec                  string   `json:"Codec,omitempty"`
	Width                  int      `json:"Width,omitempty"`
	Height                 int      `json:"Height,omitempty"`
	BitRate                int64    `json:"BitRate,omitempty"`
	BitDepth               int      `json:"BitDepth,omitempty"`
	Channels               int      `json:"Channels,omitempty"`
	RealFrameRate          *float64 `json:"RealFrameRate,omitempty"`
	AverageFrameRate       *float64 `json:"AverageFrameRate,omitempty"`
	VideoRange             string   `json:"VideoRange,omitempty"`
	VideoRangeType         string   `json:"VideoRangeType,omitempty"`
	Profile                string   `json:"Profile,omitempty"`
	Level                  *float64 `json:"Level,omitempty"`
	AspectRatio            string   `json:"AspectRatio,omitempty"`
	IsInterlaced           bool     `json:"IsInterlaced"`
	ColorPrimaries         string   `json:"ColorPrimaries,omitempty"`
	ColorTransfer          string   `json:"ColorTransfer,omitempty"`
	ColorSpace             string   `json:"ColorSpace,omitempty"`
	PixelFormat            string   `json:"PixelFormat,omitempty"`
	SampleRate             int      `json:"SampleRate,omitempty"`
	ChannelLayout          string   `json:"ChannelLayout,omitempty"`
	IsDefault              bool     `json:"IsDefault"`
	IsForced               bool     `json:"IsForced"`
	IsExternal             bool     `json:"IsExternal"`
	IsTextSubtitleStream   bool     `json:"IsTextSubtitleStream"`
	SupportsExternalStream bool     `json:"SupportsExternalStream"`
	DeliveryMethod         string   `json:"DeliveryMethod"`
	DeliveryUrl            string   `json:"DeliveryUrl,omitempty"`
	Language               string   `json:"Language,omitempty"`
	Title                  string   `json:"Title,omitempty"`
	DisplayTitle           string   `json:"DisplayTitle"`
}
