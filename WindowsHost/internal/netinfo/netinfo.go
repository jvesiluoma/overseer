// Package netinfo lists the host's addresses, recognizes hotspots and checks Host headers.
package netinfo

import (
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

type InterfaceAddress struct {
	Interface string
	Address   string
}

type HotspotKind int

const (
	// WindowsHotspot: the PC shares a (USB-tethered phone's) connection with Windows Mobile hotspot.
	WindowsHotspot HotspotKind = iota + 1
	// IPhoneHotspot: the PC has joined an iPhone's Personal Hotspot; the device must join it too.
	IPhoneHotspot
)

type Hotspot struct {
	Kind    HotspotKind
	Address InterfaceAddress
}

// WindowsHotspotAddress is what Windows Mobile hotspot (Internet Connection Sharing) gives the PC.
const WindowsHotspotAddress = "192.168.137.1"

// IPv4Addresses of interfaces that are up, excluding loopback: hotspot addresses first, then
// private, public and link-local ones.
func IPv4Addresses() []InterfaceAddress {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var result []InterfaceAddress
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if v4 := ipnet.IP.To4(); v4 != nil {
				result = append(result, InterfaceAddress{Interface: iface.Name, Address: v4.String()})
			}
		}
	}
	return Sorted(result)
}

// Sorted is a stable sort: hotspot addresses first, then private, public and link-local ones.
func Sorted(addresses []InterfaceAddress) []InterfaceAddress {
	out := slices.Clone(addresses)
	slices.SortStableFunc(out, func(a, b InterfaceAddress) int { return rank(a) - rank(b) })
	return out
}

func rank(a InterfaceAddress) int {
	switch {
	case hotspotKind(a) != 0:
		return 0
	case IsPrivateIPv4(a.Address):
		return 1
	case strings.HasPrefix(a.Address, "169.254."):
		return 3
	}
	return 2
}

// HotspotIn returns the hotspot the PC is part of, if any. The PC's own hotspot wins over a phone
// hotspot, because then the device joins the PC's network.
func HotspotIn(addresses []InterfaceAddress) *Hotspot {
	var found *Hotspot
	for _, a := range addresses {
		switch hotspotKind(a) {
		case WindowsHotspot:
			return &Hotspot{Kind: WindowsHotspot, Address: a}
		case IPhoneHotspot:
			if found == nil {
				found = &Hotspot{Kind: IPhoneHotspot, Address: a}
			}
		}
	}
	return found
}

func hotspotKind(a InterfaceAddress) HotspotKind {
	if a.Address == WindowsHotspotAddress {
		return WindowsHotspot
	}
	if isIPhoneHotspotAddress(a.Address) {
		return IPhoneHotspot
	}
	return 0
}

func octets(address string) []int {
	parts := strings.Split(address, ".")
	if len(parts) != 4 {
		return nil
	}
	out := make([]int, 4)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return nil
		}
		out[i] = n
	}
	return out
}

// iPhone Personal Hotspot hands out 172.20.10.2–14 (172.20.10.0/28, gateway .1).
func isIPhoneHotspotAddress(address string) bool {
	o := octets(address)
	return o != nil && o[0] == 172 && o[1] == 20 && o[2] == 10 && o[3] >= 2 && o[3] <= 14
}

func IsPrivateIPv4(address string) bool {
	o := octets(address)
	if o == nil {
		return false
	}
	return o[0] == 10 || (o[0] == 192 && o[1] == 168) || (o[0] == 172 && o[1] >= 16 && o[1] <= 31)
}

// HostAllowed defends the local server against DNS rebinding: a page on an attacker's domain that
// re-resolves to the host's LAN IP still sends its own domain in Host, which is rejected.
func HostAllowed(rawHost string, extraNames []string) bool {
	host := strings.ToLower(Hostname(rawHost))
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return true
	}
	if isIPv4Literal(host) || strings.Contains(host, ":") { // IPv6 literal (brackets removed)
		return true
	}
	for _, n := range extraNames {
		if strings.ToLower(n) == host {
			return true
		}
	}
	return false
}

// OriginMatchesHost: the browser must not open the WebSocket from another origin (WebSockets
// ignore CORS). A missing Origin is fine only when Host is missing too or for non-browser clients.
func OriginMatchesHost(origin, host string, hasOrigin bool) bool {
	if !hasOrigin {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return false
	}
	normalize := func(s string) string {
		s = strings.ToLower(s)
		s = strings.NewReplacer("[", "", "]", "").Replace(s)
		def := ":80"
		if u.Scheme == "https" {
			def = ":443"
		}
		return strings.TrimSuffix(s, def)
	}
	return normalize(u.Host) == normalize(host)
}

// Hostname strips the port (and IPv6 brackets) from a Host header value.
func Hostname(raw string) string {
	t := strings.TrimSpace(raw)
	if strings.HasPrefix(t, "[") {
		end := strings.Index(t, "]")
		if end < 0 {
			return ""
		}
		return t[1:end]
	}
	if i := strings.Index(t, ":"); i >= 0 {
		return t[:i]
	}
	return t
}

func isIPv4Literal(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 3 {
			return false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n > 255 || strings.ContainsAny(p, "+-") {
			return false
		}
	}
	return true
}
