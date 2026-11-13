package dial

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type fakeApp struct {
	mode   string
	params url.Values
	data   url.Values
}

func (f *fakeApp) Mode() string               { return f.mode }
func (f *fakeApp) LaunchYouTube(p url.Values) { f.mode, f.params = "youtube", p }
func (f *fakeApp) GoHome()                    { f.mode = "home" }
func (f *fakeApp) DialData() url.Values       { return f.data }
func (f *fakeApp) SetDialData(v url.Values)   { f.data = v }

func TestLaunchStatusStop(t *testing.T) {
	app := &fakeApp{mode: "home"}
	mux := http.NewServeMux()
	(&Server{Name: "Test", UUID: "u", App: app}).Register(mux)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	if rec := do("GET", "/apps/YouTube", ""); !strings.Contains(rec.Body.String(), "<state>stopped</state>") {
		t.Fatalf("expected stopped: %s", rec.Body)
	}
	if rec := do("POST", "/apps/YouTube", "pairingCode=abc&v=xyz&additionalDataUrl=http://evil/"); rec.Code != 201 {
		t.Fatalf("launch: %d", rec.Code)
	}
	if app.params.Get("pairingCode") != "abc" || app.params.Get("additionalDataUrl") != "" {
		t.Fatalf("bad params: %v", app.params)
	}
	do("POST", "/apps/YouTube/dial_data", "screenId=s1&bad<tag>=x")
	body := do("GET", "/apps/YouTube", "").Body.String()
	if !strings.Contains(body, "<state>running</state>") || !strings.Contains(body, "<screenId>s1</screenId>") || strings.Contains(body, "bad<") {
		t.Fatalf("status: %s", body)
	}
	if rec := do("DELETE", "/apps/YouTube/run", ""); rec.Code != 200 || app.mode != "home" {
		t.Fatalf("stop: %d %s", rec.Code, app.mode)
	}
	if rec := do("GET", "/apps/Netflix", ""); rec.Code != 404 {
		t.Fatalf("unknown app: %d", rec.Code)
	}
}
