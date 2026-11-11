package api

import (
	"testing"
	"time"

	"lime/internal/store"
)

func TestDisplayName(t *testing.T) {
	cases := []struct{ name, original, want string }{
		{"借东西的小人阿莉埃蒂 借りぐらしのアリエッティ", "借りぐらしのアリエッティ", "借东西的小人阿莉埃蒂"},
		{"侧耳倾听 耳をすませば", "耳をすませば", "侧耳倾听"},
		{"沙丘2", "Dune: Part Two", "沙丘2"},
		{"Spider-Man No Way Home", "Spider-Man No Way Home", "Spider-Man No Way Home"},
		{"电影 - 原名", "原名", "电影"},
		{"", "原名", ""},
	}
	for _, c := range cases {
		if got := displayName(c.name, c.original); got != c.want {
			t.Errorf("displayName(%q, %q) = %q, want %q", c.name, c.original, got, c.want)
		}
	}
}

func TestImageTags(t *testing.T) {
	if len(imageTags("")) != 0 || len(backdropTags("")) != 0 {
		t.Fatal("empty path should produce no tags")
	}
	a := imageTags("images/a.jpg")["Primary"]
	if a == "" || a != imageTags("images/a.jpg")["Primary"] {
		t.Fatal("tag should be stable and non-empty")
	}
	if a == imageTags("images/b.jpg")["Primary"] {
		t.Fatal("different images should have different tags")
	}
	if len(backdropTags("images/a.jpg")) != 1 {
		t.Fatal("backdrop should have one tag")
	}
}

func TestBuildUserData(t *testing.T) {
	t.Run("no playstate", func(t *testing.T) {
		ud := buildUserData("i1", 0, nil)
		if ud.Played || ud.PlaybackPositionTicks != 0 || ud.PlayedPercentage != nil {
			t.Fatalf("unexpected user data: %+v", ud)
		}
		if ud.Key != "i1" || ud.ItemId != "i1" {
			t.Fatalf("key/itemid not set: %+v", ud)
		}
	})

	t.Run("partial progress with runtime", func(t *testing.T) {
		ud := buildUserData("i1", 1000, &store.Playstate{PositionTicks: 500})
		if ud.PlayedPercentage == nil || *ud.PlayedPercentage != 50 {
			t.Fatalf("percentage = %v, want 50", ud.PlayedPercentage)
		}
	})

	t.Run("partial progress without runtime", func(t *testing.T) {
		ud := buildUserData("i1", 0, &store.Playstate{PositionTicks: 500})
		if ud.PlayedPercentage != nil {
			t.Fatalf("percentage should be nil when runtime unknown, got %v", *ud.PlayedPercentage)
		}
	})

	t.Run("played", func(t *testing.T) {
		ps := &store.Playstate{Played: true, UpdatedAt: time.Unix(1700000000, 0)}
		ud := buildUserData("i1", 1000, ps)
		if !ud.Played || ud.PlayCount != 1 || ud.LastPlayedDate == "" {
			t.Fatalf("unexpected played user data: %+v", ud)
		}
		if ud.PlayedPercentage != nil {
			t.Fatal("played item should not have a percentage")
		}
	})
}

func TestVideoRangeType(t *testing.T) {
	cases := map[string]string{
		"DoVi": "DOVI", "HDR10": "HDR10", "HDR10+": "HDR10+",
		"HLG": "HLG", "SDR": "SDR", "HDR": "HDR10",
	}
	for in, want := range cases {
		if got := videoRangeType(in); got != want {
			t.Errorf("videoRangeType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStreamDisplayTitle(t *testing.T) {
	v := streamDisplayTitle(&store.MediaStream{Type: "Video", Height: 2160, Codec: "hevc", VideoRange: "HDR10"})
	if v != "4K HDR10 HEVC" {
		t.Fatalf("video title = %q", v)
	}
	a := streamDisplayTitle(&store.MediaStream{Type: "Audio", Title: "English", Codec: "eac3", ChannelLayout: "5.1"})
	if a != "English EAC3 5.1" {
		t.Fatalf("audio title = %q", a)
	}
	s := streamDisplayTitle(&store.MediaStream{Type: "Subtitle", Title: "chs&eng", Codec: "ass"})
	if s != "chs&eng ASS" {
		t.Fatalf("subtitle title = %q", s)
	}
}

func TestParsePeopleWithAvatar(t *testing.T) {
	raw := `[{"Name":"Tom Hanks","Role":"Forrest","Type":"Actor","ImageTag":"abc123"},` +
		`{"Name":"无头像","Role":"","Type":"Actor","ImageTag":""}]`
	people := parsePeople(raw)
	if len(people) != 2 {
		t.Fatalf("people = %d", len(people))
	}
	if people[0].Id != store.PersonID("Tom Hanks") || people[0].PrimaryImageTag != "abc123" {
		t.Fatalf("person 0 = %+v", people[0])
	}
	if len(people[0].ImageTags) != 1 || people[0].ImageTags["Primary"] != "abc123" {
		t.Fatalf("image tags = %+v", people[0].ImageTags)
	}
	if people[1].PrimaryImageTag != "" || len(people[1].ImageTags) != 0 {
		t.Fatalf("person without avatar = %+v", people[1])
	}
}
