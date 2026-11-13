package airplay

import (
	"testing"

	"howett.net/plist"
)

func TestParsePlayText(t *testing.T) {
	loc, sec, frac := parsePlay("text/parameters", []byte("Content-Location: http://h/v.mp4\nStart-Position: 0.25\n"))
	if loc != "http://h/v.mp4" || sec != 0 || frac != 0.25 {
		t.Fatalf("got %q %v %v", loc, sec, frac)
	}
}

func TestParsePlayBinaryPlist(t *testing.T) {
	body, err := plist.Marshal(map[string]interface{}{
		"Content-Location":       "http://h/master.m3u8",
		"Start-Position-Seconds": 12.5,
	}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}
	loc, sec, frac := parsePlay("application/x-apple-binary-plist", body)
	if loc != "http://h/master.m3u8" || sec != 12.5 || frac != 0 {
		t.Fatalf("got %q %v %v", loc, sec, frac)
	}
}
