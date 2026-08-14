package secureproxy

import "testing"

func TestParseAndValidateURLAcceptsPublicHTTPURLs(t *testing.T) {
	for _, raw := range []string{
		"https://example.com/video.mkv?token=abc",
		"http://8.8.8.8/media.mp4",
		"https://example.com:8080/live",
	} {
		if _, err := ParseAndValidateURL(raw); err != nil {
			t.Fatalf("expected %q to be accepted: %v", raw, err)
		}
	}
}

func TestParseAndValidateURLRejectsUnsafeDestinations(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"http://localhost/video",
		"http://127.0.0.1/video",
		"http://10.0.0.1/video",
		"http://169.254.169.254/latest/meta-data",
		"http://192.168.1.10/video",
		"http://[::1]/video",
		"https://user:password@example.com/video",
		"https://example.com:9000/video",
	} {
		if _, err := ParseAndValidateURL(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

func TestDisplayNameDoesNotExposeQuerySecrets(t *testing.T) {
	parsed, err := ParseAndValidateURL("https://media.example.com/path/Movie.S01E02.mkv?token=private")
	if err != nil {
		t.Fatal(err)
	}
	if got := DisplayName(parsed); got != "Movie.S01E02.mkv · media.example.com" {
		t.Fatalf("unexpected display name: %q", got)
	}
}
