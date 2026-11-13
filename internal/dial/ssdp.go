package dial

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/ipv4"

	"castdeck/internal/netutil"
)

var ssdpGroup = &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}

// SSDP answers discovery requests so phones on the network see the device.
type SSDP struct {
	UUID     string
	HTTPPort int
}

func (s *SSDP) usn(st string) string {
	if st == "upnp:rootdevice" || strings.HasPrefix(st, "urn:") {
		return "uuid:" + s.UUID + "::" + st
	}
	return "uuid:" + s.UUID
}

func (s *SSDP) location(ip net.IP) string {
	return fmt.Sprintf("http://%s:%d/dd.xml", ip, s.HTTPPort)
}

// Run listens until ctx is cancelled.
func (s *SSDP) Run(ctx context.Context) error {
	lc := net.ListenConfig{Control: reuseAddr}
	conn, err := lc.ListenPacket(ctx, "udp4", "0.0.0.0:1900")
	if err != nil {
		return fmt.Errorf("ssdp listen: %w", err)
	}
	defer conn.Close()
	pc := ipv4.NewPacketConn(conn)
	joined := 0
	for _, ifi := range netutil.MulticastInterfaces() {
		ifi := ifi
		if err := pc.JoinGroup(&ifi, ssdpGroup); err == nil {
			joined++
		}
	}
	if joined == 0 {
		if err := pc.JoinGroup(nil, ssdpGroup); err != nil {
			return fmt.Errorf("ssdp join multicast group: %w", err)
		}
	}
	_ = pc.SetMulticastLoopback(true)

	go s.notifyLoop(ctx)
	go func() { <-ctx.Done(); conn.Close() }()

	buf := make([]byte, 2048)
	for {
		n, src, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		udp, ok := src.(*net.UDPAddr)
		if !ok {
			continue
		}
		s.handle(conn, udp, buf[:n])
	}
}

func (s *SSDP) handle(conn net.PacketConn, src *net.UDPAddr, msg []byte) {
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(msg)))
	if err != nil || req.Method != "M-SEARCH" {
		return
	}
	if !strings.EqualFold(strings.Trim(req.Header.Get("MAN"), `"`), "ssdp:discover") {
		return
	}
	st := strings.TrimSpace(req.Header.Get("ST"))
	var targets []string
	switch st {
	case "ssdp:all":
		targets = []string{"upnp:rootdevice", "urn:dial-multiscreen-org:device:dial:1", ServiceType}
	case "upnp:rootdevice", "urn:dial-multiscreen-org:device:dial:1", ServiceType, "uuid:" + s.UUID:
		targets = []string{st}
	default:
		return
	}
	loc := s.location(netutil.LocalIPFor(src.IP))
	go func() {
		time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
		for _, t := range targets {
			resp := "HTTP/1.1 200 OK\r\n" +
				"CACHE-CONTROL: max-age=1800\r\n" +
				"DATE: " + time.Now().UTC().Format(http.TimeFormat) + "\r\n" +
				"EXT:\r\n" +
				"LOCATION: " + loc + "\r\n" +
				"SERVER: Linux/1.0 UPnP/1.1 CastDeck/1.0\r\n" +
				"ST: " + t + "\r\n" +
				"USN: " + s.usn(t) + "\r\n" +
				"BOOTID.UPNP.ORG: 1\r\n" +
				"CONFIGID.UPNP.ORG: 1\r\n\r\n"
			if _, err := conn.WriteTo([]byte(resp), src); err != nil {
				log.Printf("ssdp: reply to %s: %v", src, err)
			}
		}
	}()
}

// notifyLoop periodically announces the device (and says goodbye on exit).
func (s *SSDP) notifyLoop(ctx context.Context) {
	send := func(nts string) {
		c, err := net.DialUDP("udp4", nil, ssdpGroup)
		if err != nil {
			return
		}
		defer c.Close()
		loc := s.location(c.LocalAddr().(*net.UDPAddr).IP)
		for _, nt := range []string{"upnp:rootdevice", ServiceType} {
			msg := "NOTIFY * HTTP/1.1\r\n" +
				"HOST: 239.255.255.250:1900\r\n" +
				"CACHE-CONTROL: max-age=1800\r\n" +
				"LOCATION: " + loc + "\r\n" +
				"NT: " + nt + "\r\n" +
				"NTS: " + nts + "\r\n" +
				"SERVER: Linux/1.0 UPnP/1.1 CastDeck/1.0\r\n" +
				"USN: " + s.usn(nt) + "\r\n" +
				"BOOTID.UPNP.ORG: 1\r\n" +
				"CONFIGID.UPNP.ORG: 1\r\n\r\n"
			_, _ = c.Write([]byte(msg))
		}
	}
	send("ssdp:alive")
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			send("ssdp:byebye")
			return
		case <-t.C:
			send("ssdp:alive")
		}
	}
}
