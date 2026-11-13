package player

import "testing"

func TestKindOf(t *testing.T) {
	for in, want := range map[string]string{
		"http://h/a.mp4": "video", "http://h/a.MP3?x=1": "audio", "http://h/p.jpg#f": "image",
		"http://h/stream.m3u8": "video", "http://h/noext": "video",
	} {
		if got := KindOf(in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
}

func TestReportIgnoresStaleMedia(t *testing.T) {
	h := NewHub()
	old := h.Play(Media{URL: "a"})
	cur := h.Play(Media{URL: "b"})
	h.Report(old, 10, 20, true, true) // a stale "ended" must not stop the new media
	if s := h.Snapshot(); s.Media == nil || s.Media.ID != cur {
		t.Fatalf("stale report affected current media: %+v", s.Media)
	}
	h.Report(cur, 5, 20, true, false)
	if s := h.Snapshot(); s.Position != 5 || s.Duration != 20 {
		t.Fatalf("report not applied: %+v", s)
	}
	h.Report(cur, 0, 0, false, true)
	if h.Snapshot().Media != nil {
		t.Fatal("ended report should clear media")
	}
}

func TestWatching(t *testing.T) {
	cases := []struct {
		s    State
		want bool
	}{
		{State{Mode: "home"}, false},
		{State{Mode: "youtube"}, true},
		{State{Mode: "home", Media: &Media{Kind: "video"}}, true},
		{State{Mode: "home", Media: &Media{Kind: "video"}, Paused: true}, false},
		{State{Mode: "home", Media: &Media{Kind: "image"}, Paused: true}, true},
	}
	for i, c := range cases {
		if got := c.s.Watching(); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}
