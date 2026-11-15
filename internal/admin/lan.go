package admin

import (
	"net"
	"strings"
)

// lanIPv4 returns the address most likely reachable from another device on the
// same network. The S3 listener binds 0.0.0.0, so the console should show a
// concrete interface address rather than the browser's host name (which is the
// NAS domain and may not answer on the S3 port).
func lanIPv4() string {
	if ips := lanIPv4s(); len(ips) > 0 {
		return ips[0]
	}
	return ""
}

// lanIPv4s lists candidate addresses, best first.
func lanIPv4s() []string {
	var out []string
	add := func(ip string) {
		if ip == "" {
			return
		}
		for _, seen := range out {
			if seen == ip {
				return
			}
		}
		out = append(out, ip)
	}

	// The outbound route usually points at the interface users reach the NAS
	// from. Dial does not send packets, so this works even without internet.
	add(outboundIPv4())
	for _, ip := range interfaceIPv4s() {
		add(ip)
	}
	return out
}

func outboundIPv4() string {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return ""
	}
	ip := addr.IP.To4()
	if ip == nil || !usableLANIPv4(ip) {
		return ""
	}
	return ip.String()
}

func interfaceIPv4s() []string {
	var private, other []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isVirtualInterface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil || !usableLANIPv4(ip4) {
				continue
			}
			if ip4.IsPrivate() {
				private = append(private, ip4.String())
			} else {
				other = append(other, ip4.String())
			}
		}
	}
	return append(private, other...)
}

// usableLANIPv4 rejects loopback, link-local and unspecified addresses.
func usableLANIPv4(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	return ip.To4() != nil
}

// isVirtualInterface filters container, VM and tunnel bridges. Real LAN bridges
// such as br0 or bond0 are kept.
func isVirtualInterface(name string) bool {
	name = strings.ToLower(name)
	for _, prefix := range []string{
		"docker", "veth", "virbr", "vnet", "vmnet", "tailscale", "zt",
		"wg", "tun", "tap", "cni", "flannel", "kube", "cali", "lxcbr", "lxdbr",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	// Docker user-defined bridges are named br-<12 hex chars>.
	if strings.HasPrefix(name, "br-") && len(name) == 15 {
		return true
	}
	return false
}
