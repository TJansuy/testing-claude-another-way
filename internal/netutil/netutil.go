// Package netutil contains small helpers for finding local addresses.
package netutil

import (
	"net"
	"net/http"
)

// LocalIPv4s returns the non-loopback IPv4 addresses of interfaces that are up.
func LocalIPv4s() []net.IP {
	var out []net.IP
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip4 := ipn.IP.To4(); ip4 != nil && !ip4.IsLinkLocalUnicast() {
					out = append(out, ip4)
				}
			}
		}
	}
	return out
}

// MulticastInterfaces returns up, multicast-capable, non-loopback interfaces
// that have an IPv4 address.
func MulticastInterfaces() []net.Interface {
	var out []net.Interface
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				out = append(out, ifi)
				break
			}
		}
	}
	return out
}

// LocalIPFor returns the local address the kernel would use to reach remote.
// No packets are sent.
func LocalIPFor(remote net.IP) net.IP {
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: remote, Port: 9})
	if err != nil {
		return PrimaryIP()
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP
}

// PrimaryIP returns the address of the default-route interface, falling back
// to the first local address, then loopback.
func PrimaryIP() net.IP {
	if c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 9}); err == nil {
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).IP
	}
	if ips := LocalIPv4s(); len(ips) > 0 {
		return ips[0]
	}
	return net.IPv4(127, 0, 0, 1)
}

// RequestLocalHost returns the local IP an HTTP request arrived on, so URLs
// handed back to a client use an address that client can reach.
func RequestLocalHost(r *http.Request) string {
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if ta, ok := a.(*net.TCPAddr); ok {
			if ip4 := ta.IP.To4(); ip4 != nil {
				return ip4.String()
			}
			return ta.IP.String()
		}
	}
	return PrimaryIP().String()
}
