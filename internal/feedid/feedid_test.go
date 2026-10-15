package feedid

import "testing"

func TestFeedIDs(t *testing.T) {
	if Season("hele_historien", "watergate") != "hele_historien/sesong/watergate" {
		t.Error("season id must mirror NRK's URL structure")
	}
	if r := Parse("a/sesong/b"); r.SeriesID != "a" || r.SeasonID != "b" {
		t.Errorf("parse season: %+v", r)
	}
	if r := Parse("plain"); r.SeriesID != "plain" || r.SeasonID != "" {
		t.Errorf("parse plain: %+v", r)
	}
	for id, want := range map[string]bool{
		"trygdekontoret": true, "l_abc-123": true, "a/sesong/b": true,
		"": false, "../etc": false, "a/sesong/": false, "a b": false, "a/b": false, "a/sesong/../x": false,
	} {
		if Valid(id) != want {
			t.Errorf("Valid(%q) != %v", id, want)
		}
	}
}
