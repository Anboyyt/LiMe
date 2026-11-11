package pinyin

import "testing"

func TestKey(t *testing.T) {
	cases := []struct {
		in                 string
		wantFull, wantAbbr string
	}{
		{"三体", "santi", "st"},
		{"流浪地球2", "liulangdiqiu2", "lldq2"},
		{"蜘蛛侠：英雄无归", "zhizhuxiayingxiongwugui", "zzxyxwg"},
		{"Spider-Man", "", ""},
		{"1917", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		full, abbr := Key(c.in)
		if full != c.wantFull || abbr != c.wantAbbr {
			t.Errorf("Key(%q) = (%q, %q), want (%q, %q)", c.in, full, abbr, c.wantFull, c.wantAbbr)
		}
	}
}

func TestPrefixRange(t *testing.T) {
	lo, hi := PrefixRange(" San ")
	if lo != "san" {
		t.Fatalf("lo = %q", lo)
	}
	if !(lo <= "santi" && "santi" < hi) {
		t.Fatalf("range [%q, %q) should cover santi", lo, hi)
	}
	if !(hi <= "sb" || "sb" >= hi) {
		t.Fatalf("range upper bound should exclude sb")
	}
}
