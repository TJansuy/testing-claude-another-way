package web

import (
	"net/url"
	"testing"
)

func TestYouTubeID(t *testing.T) {
	cases := []struct{ in, id, start string }{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ", ""},
		{"https://youtu.be/dQw4w9WgXcQ?t=42", "dQw4w9WgXcQ", "42"},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ&t=90s", "dQw4w9WgXcQ", "90"},
		{"https://youtube.com/shorts/abcdefghijk", "abcdefghijk", ""},
		{"https://music.youtube.com/watch?v=abcdefghijk", "abcdefghijk", ""},
		{"https://www.youtube.com/feed/subscriptions", "", ""},
		{"https://example.com/watch?v=dQw4w9WgXcQ", "", ""},
	}
	for _, c := range cases {
		u, _ := url.Parse(c.in)
		id, start := YouTubeID(u)
		if id != c.id || start != c.start {
			t.Errorf("%s: got (%q,%q) want (%q,%q)", c.in, id, start, c.id, c.start)
		}
	}
}
