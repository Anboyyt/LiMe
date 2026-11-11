package strm

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  Metadata
	}{
		{
			name:  "123 网盘样本",
			input: "恶作剧之吻.2005.S01E01.1080p.30fps.WEB-DL.SDR.H.264.8bit.DDP2.0.mkv",
			want: Metadata{
				Title: "恶作剧之吻", Year: 2005, Season: 1, Episode: 1, IsEpisode: true,
				Resolution: "1080p", Width: 1920, Height: 1080,
				VideoCodec: "h264", AudioCodec: "eac3", Channels: 2, BitDepth: 8,
				FrameRate: 30, VideoRange: "SDR", Source: "WEB-DL", Container: "mkv",
			},
		},
		{
			name:  "115 网盘样本",
			input: "21天重养自己.2026.S01E01.2160p.25fps.WEB-DL.SDR.H.265.8bit.AAC2.0-HHWEB.mp4",
			want: Metadata{
				Title: "21天重养自己", Year: 2026, Season: 1, Episode: 1, IsEpisode: true,
				Resolution: "2160p", Width: 3840, Height: 2160,
				VideoCodec: "hevc", AudioCodec: "aac", Channels: 2, BitDepth: 8,
				FrameRate: 25, VideoRange: "SDR", Source: "WEB-DL", Container: "mp4",
				ReleaseGroup: "HHWEB",
			},
		},
		{
			name:  "电影 + 5.1",
			input: "沙丘2.2024.2160p.WEB-DL.H265.DDP5.1.mkv",
			want: Metadata{
				Title: "沙丘2", Year: 2024, Resolution: "2160p", Width: 3840, Height: 2160,
				VideoCodec: "hevc", AudioCodec: "eac3", Channels: 6, Container: "mkv",
				Source: "WEB-DL",
			},
		},
		{
			name:  "标题带点 + x264",
			input: "Mr.Robot.2015.S02E03.1080p.BluRay.x264.AC3.5.1.mkv",
			want: Metadata{
				Title: "Mr Robot", Year: 2015, Season: 2, Episode: 3, IsEpisode: true,
				Resolution: "1080p", Width: 1920, Height: 1080,
				VideoCodec: "h264", AudioCodec: "ac3", Channels: 6,
				Container: "mkv", Source: "BLURAY",
			},
		},
		{
			name:  "标题带连字符",
			input: "Spider-Man.No.Way.Home.2021.1080p.WEB-DL.AAC2.0.mp4",
			want: Metadata{
				Title: "Spider-Man No Way Home", Year: 2021, Resolution: "1080p",
				Width: 1920, Height: 1080,
				AudioCodec: "aac", Channels: 2, Container: "mp4", Source: "WEB-DL",
			},
		},
		{
			name:  "DD5.1 多音轨",
			input: "侧耳倾听.耳をすませば.1995.1080p.24fps.SDR.H.264.8bit.DD5.1.3Audios.内封字幕.中文字幕.mkv",
			want: Metadata{
				Title: "侧耳倾听 耳をすませば", Year: 1995, Resolution: "1080p",
				Width: 1920, Height: 1080, VideoCodec: "h264",
				AudioCodec: "ac3", Channels: 6, BitDepth: 8, FrameRate: 24,
				VideoRange: "SDR", Container: "mkv",
			},
		},
		{
			name:  "DD+ 5.1",
			input: "沙丘2.2024.2160p.WEB-DL.H265.DD+5.1.mkv",
			want: Metadata{
				Title: "沙丘2", Year: 2024, Resolution: "2160p", Width: 3840, Height: 2160,
				VideoCodec: "hevc", AudioCodec: "eac3", Channels: 6, Container: "mkv",
				Source: "WEB-DL",
			},
		},
		{
			name:  "DTS-HD MA 7.1",
			input: "电影.2024.1080p.BluRay.DTS-HD.MA.7.1.mkv",
			want: Metadata{
				Title: "电影", Year: 2024, Resolution: "1080p", Width: 1920, Height: 1080,
				AudioCodec: "dts", Channels: 8, Container: "mkv", Source: "BLURAY",
			},
		},
		{
			name:  "TrueHD Atmos",
			input: "电影.2024.2160p.BluRay.TrueHD.7.1.Atmos.mkv",
			want: Metadata{
				Title: "电影", Year: 2024, Resolution: "2160p", Width: 3840, Height: 2160,
				AudioCodec: "truehd", Channels: 8, Container: "mkv", Source: "BLURAY",
			},
		},
		{
			name:  "OPUS 5.1",
			input: "动画.2024.1080p.WEB-DL.OPUS5.1.mkv",
			want: Metadata{
				Title: "动画", Year: 2024, Resolution: "1080p", Width: 1920, Height: 1080,
				AudioCodec: "opus", Channels: 6, Container: "mkv", Source: "WEB-DL",
			},
		},
		{
			name:  "MP3 2.0",
			input: "老片.1999.DVD.MP3.2.0.avi",
			want: Metadata{
				Title: "老片", Year: 1999,
				AudioCodec: "mp3", Channels: 2, Container: "avi",
			},
		},
		{
			name:  "AAC2.0 带发布组",
			input: "一斩苍穹.2026.S01E12.2160p.25fps.HQ.WEB-DL.Dovi.P5.H.265.10bit.AAC2.0-Ocat.mkv",
			want: Metadata{
				Title: "一斩苍穹", Year: 2026, Season: 1, Episode: 12, IsEpisode: true,
				Resolution: "2160p", Width: 3840, Height: 2160,
				VideoCodec: "hevc", AudioCodec: "aac", Channels: 2, BitDepth: 10,
				FrameRate: 25, VideoRange: "DoVi", Source: "WEB-DL", Container: "mkv",
				ReleaseGroup: "Ocat",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.input)

			checkStr(t, "Title", got.Title, tc.want.Title)
			checkInt(t, "Year", got.Year, tc.want.Year)
			checkInt(t, "Season", got.Season, tc.want.Season)
			checkInt(t, "Episode", got.Episode, tc.want.Episode)
			checkBool(t, "IsEpisode", got.IsEpisode, tc.want.IsEpisode)
			checkStr(t, "Resolution", got.Resolution, tc.want.Resolution)
			checkInt(t, "Width", got.Width, tc.want.Width)
			checkInt(t, "Height", got.Height, tc.want.Height)
			checkStr(t, "VideoCodec", got.VideoCodec, tc.want.VideoCodec)
			checkStr(t, "AudioCodec", got.AudioCodec, tc.want.AudioCodec)
			checkInt(t, "Channels", got.Channels, tc.want.Channels)
			checkInt(t, "BitDepth", got.BitDepth, tc.want.BitDepth)
			checkStr(t, "VideoRange", got.VideoRange, tc.want.VideoRange)
			checkStr(t, "Source", got.Source, tc.want.Source)
			checkStr(t, "Container", got.Container, tc.want.Container)
			if tc.want.FrameRate > 0 && got.FrameRate != tc.want.FrameRate {
				t.Errorf("FrameRate = %v, want %v", got.FrameRate, tc.want.FrameRate)
			}
			if tc.want.ReleaseGroup != "" {
				checkStr(t, "ReleaseGroup", got.ReleaseGroup, tc.want.ReleaseGroup)
			}
		})
	}
}

func checkStr(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func checkInt(t *testing.T, field string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %d, want %d", field, got, want)
	}
}

func checkBool(t *testing.T, field string, got, want bool) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}
