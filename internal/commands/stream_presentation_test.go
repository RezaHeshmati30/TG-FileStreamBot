package commands

import "testing"

func TestFileLinkPresentation(t *testing.T) {
	tests := []struct {
		name     string
		fileName string
		mimeType string
		icon     string
		label    string
	}{
		{name: "mkv video", fileName: "movie.mkv", mimeType: "application/octet-stream", icon: "🎬", label: "Video Link"},
		{name: "mp4 video", fileName: "movie.bin", mimeType: "video/mp4", icon: "🎬", label: "Video Link"},
		{name: "subtitle", fileName: "movie.srt", mimeType: "text/plain", icon: "💬", label: "Subtitle Link"},
		{name: "archive", fileName: "files.zip", mimeType: "application/zip", icon: "📎", label: "File Link"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			icon, _, label := fileLinkPresentation(test.fileName, test.mimeType)
			if icon != test.icon || label != test.label {
				t.Fatalf("got icon %q and label %q", icon, label)
			}
		})
	}
}
