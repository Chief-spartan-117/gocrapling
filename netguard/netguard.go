// Package netguard keeps fetchers away from non-public addresses
// (loopback, RFC 1918, link-local and cloud metadata, CGNAT, multicast,
// reserved). Without it, any caller that controls a URL, such as an AI agent
// using the MCP server, can make the machine fetch internal services.
//
// Two layers work together: CheckURL refuses a target before any request,
// and Control (used by Dialer) refuses the address at connect time, which
// also covers redirects and DNS rebinding for direct connections.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// ErrBlocked is returned for targets in non-public address space.
var ErrBlocked = errors.New("netguard: address is not public")

var denied = mustPrefixes(
	"0.0.0.0/8",       // "this" network
	"10.0.0.0/8",      // RFC 1918
	"100.64.0.0/10",   // CGNAT
	"127.0.0.0/8",     // loopback
	"169.254.0.0/16",  // link-local, cloud metadata
	"172.16.0.0/12",   // RFC 1918
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"192.88.99.0/24",  // 6to4 relay
	"192.168.0.0/16",  // RFC 1918
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved, incl. broadcast
	"::/128",
	"::1/128",
	"fc00::/7",  // unique local
	"fe80::/10", // link-local
	"ff00::/8",  // multicast
	"2001:db8::/32",
	"64:ff9b::/96", // NAT64 (embedded v4 checked separately)
	"2002::/16",    // 6to4 (embedded v4 checked separately)
)

func mustPrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(cidrs))
	for i, c := range cidrs {
		out[i] = netip.MustParsePrefix(c)
	}
	return out
}

// IsPublic reports whether ip is a routable public address.
func IsPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() {
		return false
	}
	for _, p := range denied {
		if p.Contains(ip) {
			// NAT64 and 6to4 are only a problem when they embed a
			// non-public IPv4 address.
			if inner, ok := embeddedV4(ip); ok {
				return IsPublic(inner)
			}
			return false
		}
	}
	return true
}

func embeddedV4(ip netip.Addr) (netip.Addr, bool) {
	b := ip.As16()
	switch {
	case netip.MustParsePrefix("64:ff9b::/96").Contains(ip):
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	case netip.MustParsePrefix("2002::/16").Contains(ip):
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	}
	return netip.Addr{}, false
}

// CheckURL allows only http and https URLs whose host resolves exclusively
// to public addresses.
func CheckURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("netguard: bad url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("netguard: scheme %q not allowed (http and https only)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("netguard: url has no host")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !IsPublic(ip) {
			return fmt.Errorf("%w: %s", ErrBlocked, host)
		}
		return nil
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(rctx, "ip", host)
	if err != nil {
		return fmt.Errorf("netguard: resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("netguard: %s has no addresses", host)
	}
	for _, a := range addrs {
		if !IsPublic(a) {
			return fmt.Errorf("%w: %s resolves to %s", ErrBlocked, host, a)
		}
	}
	return nil
}

// Control is a net.Dialer.Control hook that refuses non-public peers after
// DNS resolution, so a name that flips to an internal address between the
// check and the connection (DNS rebinding) is still stopped.
func Control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("netguard: bad peer %q: %w", address, err)
	}
	if !IsPublic(ap.Addr()) {
		return fmt.Errorf("%w: %s", ErrBlocked, ap.Addr())
	}
	return nil
}

// Dialer returns a net.Dialer that only connects to public addresses.
func Dialer() *net.Dialer {
	return &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: Control}
}

// DialContext dials through Dialer; it matches the signature fetchers expect.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return Dialer().DialContext(ctx, network, addr)
}
