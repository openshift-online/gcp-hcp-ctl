package platformapi

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// CanonicalEndpoint binds operations to one API realm without persisting URL
// credentials, query strings or fragments. HTTP is loopback-test-only; the
// production NewClient constructor still requires HTTPS.
func CanonicalEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("a credential-free API endpoint identity is required")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", fmt.Errorf("API endpoint identity must use HTTPS")
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}
func (c *Client) EndpointIdentity() (string, error) { return CanonicalEndpoint(c.endpoint) }
