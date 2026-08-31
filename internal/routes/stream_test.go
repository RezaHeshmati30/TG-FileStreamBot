package routes

import "testing"

func TestStreamResponseMIMETypeForSubtitleFormats(t *testing.T) {
	tests := map[string]string{
		"subtitle.srt": "application/x-subrip; charset=utf-8",
		"subtitle.vtt": "text/vtt; charset=utf-8",
		"subtitle.ass": "text/x-ssa; charset=utf-8",
		"subtitle.ssa": "text/x-ssa; charset=utf-8",
	}
	for name, expected := range tests {
		if got := streamResponseMIMEType(name, "application/octet-stream", "/subtitle/:messageID/:filename"); got != expected {
			t.Errorf("%s: expected %q, got %q", name, expected, got)
		}
	}
}

func TestStreamResponseMIMETypePreservesRegularFileType(t *testing.T) {
	if got := streamResponseMIMEType("video.mkv", "video/x-matroska", "/stream/:messageID"); got != "video/x-matroska" {
		t.Fatalf("expected stored video MIME type, got %q", got)
	}
}
