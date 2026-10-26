package mediajobs

import "testing"

func TestDetectSeasonEpisode(t *testing.T) {
	tests := []struct {
		name, season, episode string
	}{
		{"Example.Show.S03E07.1080p.mkv", "3", "7"},
		{"Example Show 2x11 WEB-DL.mp4", "2", "11"},
		{"Movie.2026.1080p.mkv", "", ""},
	}
	for _, test := range tests {
		season, episode := detectSeasonEpisode(test.name)
		if season != test.season || episode != test.episode {
			t.Fatalf("%q: got S%sE%s, want S%sE%s", test.name, season, episode, test.season, test.episode)
		}
	}
}
