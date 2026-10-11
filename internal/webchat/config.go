package webchat

import (
	"fmt"
	"net"
	"net/url"
	"time"
)

type Config struct {
	Enabled                      bool
	ListenAddress, PublicBaseURL string
	SessionTTL, LoginCodeTTL     time.Duration
	TrustedProxies               []string
}

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	u, e := url.Parse(c.PublicBaseURL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return fmt.Errorf("invalid WEB_PUBLIC_BASE_URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())) {
		return fmt.Errorf("WEB_PUBLIC_BASE_URL requires HTTPS (HTTP allowed only on loopback)")
	}
	host, _, e := net.SplitHostPort(c.ListenAddress)
	if e != nil {
		return fmt.Errorf("invalid WEB_LISTEN_ADDRESS")
	}
	if u.Scheme == "http" && host != "localhost" && !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("HTTP development listener must be loopback")
	}
	if c.SessionTTL < time.Minute || c.SessionTTL > 30*24*time.Hour || c.LoginCodeTTL < 10*time.Second || c.LoginCodeTTL > 10*time.Minute {
		return fmt.Errorf("invalid web TTL")
	}
	for _, cidr := range c.TrustedProxies {
		if _, _, e := net.ParseCIDR(cidr); e != nil {
			return fmt.Errorf("invalid WEB_TRUSTED_PROXIES CIDR")
		}
	}
	if u.Scheme == "https" && len(c.TrustedProxies) == 0 {
		return fmt.Errorf("HTTPS reverse proxy requires WEB_TRUSTED_PROXIES")
	}
	return nil
}
