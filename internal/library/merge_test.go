package library

import (
	"testing"

	"lime/internal/strm"
)

func TestMergeMetaAudioChannels(t *testing.T) {
	// .strm 名字只有 AAC，真实文件名有 AAC2.0：声道要补上
	got := mergeMeta(
		strm.Metadata{AudioCodec: "aac"},
		strm.Metadata{AudioCodec: "aac", Channels: 2},
	)
	if got.AudioCodec != "aac" || got.Channels != 2 {
		t.Fatalf("got %s/%d, want aac/2", got.AudioCodec, got.Channels)
	}

	// 编码不同时保留先解析到的，不拿另一个覆盖
	got = mergeMeta(
		strm.Metadata{AudioCodec: "ac3", Channels: 6},
		strm.Metadata{AudioCodec: "aac", Channels: 2},
	)
	if got.AudioCodec != "ac3" || got.Channels != 6 {
		t.Fatalf("got %s/%d, want ac3/6", got.AudioCodec, got.Channels)
	}

	// 一边完全没解析出音频时取另一边
	got = mergeMeta(
		strm.Metadata{},
		strm.Metadata{AudioCodec: "eac3", Channels: 6},
	)
	if got.AudioCodec != "eac3" || got.Channels != 6 {
		t.Fatalf("got %s/%d, want eac3/6", got.AudioCodec, got.Channels)
	}
}
