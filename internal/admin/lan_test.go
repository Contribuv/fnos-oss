package admin

import (
	"net"
	"testing"
)

func TestUsableLANIPv4(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"192.168.1.10", true},
		{"10.0.0.5", true},
		{"172.16.3.4", true},
		{"8.8.8.8", true},
		{"127.0.0.1", false},
		{"169.254.10.1", false},
		{"0.0.0.0", false},
		{"::1", false},
		{"fe80::1", false},
	}
	for _, c := range cases {
		got := usableLANIPv4(net.ParseIP(c.ip))
		if got != c.want {
			t.Errorf("usableLANIPv4(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestIsVirtualInterface(t *testing.T) {
	cases := map[string]bool{
		"eth0":             false,
		"enp1s0":           false,
		"bond0":            false,
		"br0":              false,
		"lo":               false,
		"docker0":          true,
		"veth1a2b3c":       true,
		"virbr0":           true,
		"tailscale0":       true,
		"wg0":              true,
		"br-0123456789ab":  true,
		"br-0123456789abc": false,
		"ztabcdef":         true,
		"vnet0":            true,
	}
	for name, want := range cases {
		if got := isVirtualInterface(name); got != want {
			t.Errorf("isVirtualInterface(%s) = %v, want %v", name, got, want)
		}
	}
}

func TestLanIPv4sDeduplicates(t *testing.T) {
	ips := lanIPv4s()
	seen := map[string]bool{}
	for _, ip := range ips {
		if seen[ip] {
			t.Fatalf("duplicate address %s in %v", ip, ips)
		}
		seen[ip] = true
		if net.ParseIP(ip) == nil {
			t.Fatalf("invalid address %q", ip)
		}
	}
}
