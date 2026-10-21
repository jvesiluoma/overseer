package netinfo

import "testing"

func TestHotspotDetectionAndOrder(t *testing.T) {
	addrs := []InterfaceAddress{
		{"Ethernet", "169.254.3.4"},
		{"Wi-Fi", "192.168.1.20"},
		{"Public", "203.0.113.5"},
		{"Local Area Connection* 10", "192.168.137.1"},
	}
	sorted := Sorted(addrs)
	if sorted[0].Address != WindowsHotspotAddress || sorted[1].Address != "192.168.1.20" || sorted[3].Address != "169.254.3.4" {
		t.Fatalf("order: %+v", sorted)
	}
	if h := HotspotIn(addrs); h == nil || h.Kind != WindowsHotspot {
		t.Fatalf("hotspot: %+v", h)
	}
	if h := HotspotIn([]InterfaceAddress{{"Wi-Fi", "172.20.10.3"}}); h == nil || h.Kind != IPhoneHotspot {
		t.Fatalf("iPhone hotspot: %+v", h)
	}
	if h := HotspotIn([]InterfaceAddress{{"Wi-Fi", "172.20.10.20"}}); h != nil {
		t.Fatalf("outside the iPhone range: %+v", h)
	}
}

func TestHostAllowed(t *testing.T) {
	for _, h := range []string{"192.168.1.2", "192.168.1.2:8443", "localhost", "pc.local:443", "[::1]:443", "go.ss"} {
		if !HostAllowed(h, []string{"go.ss"}) {
			t.Errorf("rejected %s", h)
		}
	}
	for _, h := range []string{"", "evil.example", "192.168.1", "999.1.1.1", "rebind.example:443"} {
		if HostAllowed(h, []string{"go.ss"}) {
			t.Errorf("allowed %s", h)
		}
	}
}

func TestOriginMatchesHost(t *testing.T) {
	cases := []struct {
		origin, host string
		has, want    bool
	}{
		{"", "127.0.0.1:8080", false, true},
		{"http://127.0.0.1:8080", "127.0.0.1:8080", true, true},
		{"https://192.168.1.2", "192.168.1.2:443", true, true},
		{"https://192.168.1.2:443", "192.168.1.2", true, true},
		{"http://evil.example", "127.0.0.1:8080", true, false},
		{"null", "127.0.0.1", true, false},
		{"file://x", "x", true, false},
	}
	for _, c := range cases {
		if got := OriginMatchesHost(c.origin, c.host, c.has); got != c.want {
			t.Errorf("OriginMatchesHost(%q, %q) = %v", c.origin, c.host, got)
		}
	}
}
