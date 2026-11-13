// Package dial implements a DIAL (DIscovery And Launch) receiver.
//
// This is how the YouTube app casts to smart TVs and consoles: the phone finds
// the device with SSDP, asks it to launch "YouTube" with a pairing code, and
// the device opens the YouTube TV web app (youtube.com/tv), which pairs with
// the phone through YouTube's servers. From then on the phone is a remote for
// the TV app — exactly what "Cast to TV" looks like on a smart TV.
package dial

import (
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"castdeck/internal/netutil"
)

// ServiceType is the SSDP search target of DIAL devices.
const ServiceType = "urn:dial-multiscreen-org:service:dial:1"

// Launcher is implemented by the screen controller.
type Launcher interface {
	Mode() string
	LaunchYouTube(params url.Values)
	GoHome()
	DialData() url.Values
	SetDialData(url.Values)
}

// Server serves the DIAL HTTP endpoints.
type Server struct {
	Name string
	UUID string
	App  Launcher
}

// Register adds the DIAL routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("/dd.xml", s.deviceDescription)
	mux.HandleFunc("/apps/", s.apps)
}

func (s *Server) deviceDescription(w http.ResponseWriter, r *http.Request) {
	host := netutil.RequestLocalHost(r)
	port := portOf(r)
	w.Header().Set("Application-URL", fmt.Sprintf("http://%s:%s/apps/", host, port))
	w.Header().Set("Access-Control-Expose-Headers", "Application-URL")
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion><major>1</major><minor>0</minor></specVersion>
  <device>
    <deviceType>urn:dial-multiscreen-org:device:dial:1</deviceType>
    <friendlyName>%s</friendlyName>
    <manufacturer>CastDeck</manufacturer>
    <modelName>CastDeck</modelName>
    <UDN>uuid:%s</UDN>
    <serviceList>
      <service>
        <serviceType>%s</serviceType>
        <serviceId>urn:dial-multiscreen-org:serviceId:dial</serviceId>
        <controlURL>/ssdp/notfound</controlURL>
        <eventSubURL>/ssdp/notfound</eventSubURL>
        <SCPDURL>/ssdp/notfound</SCPDURL>
      </service>
    </serviceList>
  </device>
</root>
`, xmlEscape(s.Name), s.UUID, ServiceType)
}

func (s *Server) apps(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/apps/"), "/"), "/")
	if len(parts) == 0 || parts[0] != "YouTube" {
		http.NotFound(w, r)
		return
	}
	switch {
	case len(parts) == 1:
		s.youtube(w, r)
	case len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodDelete:
		if s.App.Mode() != "youtube" {
			http.NotFound(w, r)
			return
		}
		log.Printf("dial: stop YouTube requested by %s", r.RemoteAddr)
		s.App.GoHome()
		w.WriteHeader(http.StatusOK)
	case len(parts) == 2 && parts[1] == "dial_data":
		s.dialData(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) youtube(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		running := s.App.Mode() == "youtube"
		state := "stopped"
		link := ""
		if running {
			state, link = "running", `<link rel="run" href="run"/>`
		}
		var add strings.Builder
		for k, vs := range s.App.DialData() {
			if len(vs) == 0 || !validTag(k) {
				continue
			}
			fmt.Fprintf(&add, "<%s>%s</%s>", k, xmlEscape(vs[0]), k)
		}
		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<service xmlns="urn:dial-multiscreen-org:schemas:dial" dialVer="2.1">
  <name>YouTube</name>
  <options allowStop="true"/>
  <state>%s</state>
  %s
  <additionalData>%s</additionalData>
</service>
`, state, link, add.String())
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 8192))
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		params, err := url.ParseQuery(strings.TrimSpace(string(body)))
		if err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		params.Del("additionalDataUrl")
		log.Printf("dial: launch YouTube requested by %s", r.RemoteAddr)
		s.App.LaunchYouTube(params)
		w.Header().Set("Location", fmt.Sprintf("http://%s:%s/apps/YouTube/run", netutil.RequestLocalHost(r), portOf(r)))
		w.WriteHeader(http.StatusCreated)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// dialData receives the YouTube TV app's pairing info (it POSTs here because
// we gave it additionalDataUrl). The phone reads it back via GET /apps/YouTube.
func (s *Server) dialData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Private-Network", "true")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
		v, err := url.ParseQuery(string(body))
		if err != nil {
			http.Error(w, "bad data", http.StatusBadRequest)
			return
		}
		s.App.SetDialData(v)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		io.WriteString(w, s.App.DialData().Encode())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func portOf(r *http.Request) string {
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(interface{ String() string }); ok {
		if i := strings.LastIndexByte(a.String(), ':'); i >= 0 {
			return a.String()[i+1:]
		}
	}
	return "8090"
}

func validTag(k string) bool {
	if k == "" || len(k) > 64 {
		return false
	}
	for i, c := range k {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
