// Package util provides shared utility helpers for the agent and tool layers.
package util

import (
	"fmt"
	"net"
	"strings"
)

// CheckSSRFOptions configures the SSRF guard applied by CheckSSRFWithOptions.
type CheckSSRFOptions struct {
	// AllowLoopback permits requests to 127.0.0.0/8 and ::1 targets.
	AllowLoopback bool
}

// CheckSSRFWithOptions returns an error if the given URL target is blocked
// by SSRF policy. Private, loopback, link-local, and multicast addresses are
// rejected unless explicitly permitted by opts.
//
// DNS resolution errors are not treated as SSRF violations -- the request is
// allowed through and the HTTP client handles the failure.
func CheckSSRFWithOptions(rawURL string, opts CheckSSRFOptions) error {
	host := extractHost(rawURL)
	if host == "" {
		return nil
	}

	ips, err := net.LookupHost(host)
	if err != nil {
		return nil
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() {
			if !opts.AllowLoopback {
				return fmt.Errorf("SSRF guard: loopback address %s is not permitted", ipStr)
			}
			continue
		}
		if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("SSRF guard: private/internal address %s is not permitted", ipStr)
		}
		if ip.IsUnspecified() || ip.IsMulticast() {
			return fmt.Errorf("SSRF guard: reserved address %s is not permitted", ipStr)
		}
	}
	return nil
}

// extractHost strips the scheme and path from a URL to obtain the host portion.
func extractHost(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	host, _, err := net.SplitHostPort(s)
	if err != nil {
		return s
	}
	return host
}
