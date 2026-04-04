package fetcher

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const MaxBodySize = 10 << 20 // 10 MB

var privateRanges []*net.IPNet

func init() {
	cidrs := []string{
		"127.0.0.0/8",    // loopback
		"10.0.0.0/8",     // private class A
		"172.16.0.0/12",  // private class B
		"192.168.0.0/16", // private class C
		"169.254.0.0/16", // link-local (AWS metadata)
		"0.0.0.0/8",      // current network
		"::1/128",        // IPv6 loopback
		"fc00::/7",       // IPv6 unique local
		"fe80::/10",      // IPv6 link-local
	}
	for _, cidr := range cidrs {
		_, network, _ := net.ParseCIDR(cidr)
		privateRanges = append(privateRanges, network)
	}
}

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Fetcher struct {
	client    HTTPClient
	userAgent string
}

func NewFetcher(client HTTPClient, userAgent string) *Fetcher {
	return &Fetcher{
		client:    client,
		userAgent: userAgent,
	}
}

func (f *Fetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	if err := CheckSSRF(rawURL); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", f.userAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfterStr := resp.Header.Get("Retry-After")
		retryAfter, err := strconv.Atoi(retryAfterStr)
		if err != nil {
			return nil, &RateLimitedError{RetryAfter: 60 * time.Second}
		}

		return nil, &RateLimitedError{RetryAfter: time.Duration(retryAfter) * time.Second}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &RequestError{StatusCode: resp.StatusCode}
	}

	if ct := resp.Header.Get("Content-Type"); !IsAllowedContentType(ct) {
		return nil, fmt.Errorf("unsupported content type: %s", ct)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodySize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxBodySize {
		return nil, fmt.Errorf("response body exceeds %d bytes", MaxBodySize)
	}

	return body, nil
}

// CheckSSRF resolves the hostname and blocks requests to private/reserved IP ranges.
func CheckSSRF(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("missing hostname in URL")
	}

	// If the hostname is already an IP address, check it directly.
	if ip := net.ParseIP(hostname); ip != nil {
		if IsPrivateIP(ip) {
			return fmt.Errorf("blocked request to private IP %s", hostname)
		}
		return nil
	}

	// Resolve hostname and check all returned IPs.
	ips, err := net.LookupHost(hostname)
	if err != nil {
		return fmt.Errorf("DNS lookup failed for %s: %w", hostname, err)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip != nil && IsPrivateIP(ip) {
			return fmt.Errorf("blocked request to private IP %s (%s)", ipStr, hostname)
		}
	}

	return nil
}

// IsPrivateIP returns true if the IP falls within a private/reserved range.
func IsPrivateIP(ip net.IP) bool {
	for _, r := range privateRanges {
		if r.Contains(ip) {
			return true
		}
	}
	return false
}

// IsAllowedContentType returns true if the Content-Type is HTML or unset.
func IsAllowedContentType(ct string) bool {
	if ct == "" {
		return true
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	return strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "application/xhtml+xml")
}
