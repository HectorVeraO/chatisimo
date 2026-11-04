package capitime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultFetchTimeout = 5 * time.Second
	maxImageRedirects   = 3
)

// DiscordImageHosts lists the hosts the default image fetcher and the message filter accept:
// the Discord CDN and its media proxy.
func DiscordImageHosts() []string {
	return []string{"cdn.discordapp.com", "media.discordapp.net"}
}

// HTTPImageFetcherConfig configures an HTTPImageFetcher.
type HTTPImageFetcherConfig struct {
	// Client performs the requests. When nil, a client with a five second timeout is used. Its
	// redirect policy is always replaced by one that stays on AllowedHosts over HTTPS.
	Client *http.Client
	// AllowedHosts lists the hosts (with the port, when not the default one) the fetcher may
	// contact. When empty, DiscordImageHosts is used.
	AllowedHosts []string
}

// HTTPImageFetcher downloads images over HTTPS from an allowlist of hosts. Errors never include
// the URL, because Discord CDN URLs carry signatures.
type HTTPImageFetcher struct {
	client  *http.Client
	allowed map[string]struct{}
}

// NewHTTPImageFetcher builds an HTTPImageFetcher from cfg.
func NewHTTPImageFetcher(cfg HTTPImageFetcherConfig) *HTTPImageFetcher {
	hosts := cfg.AllowedHosts
	if len(hosts) == 0 {
		hosts = DiscordImageHosts()
	}

	allowed := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		allowed[strings.ToLower(strings.TrimSpace(host))] = struct{}{}
	}

	fetcher := &HTTPImageFetcher{allowed: allowed}

	var client http.Client
	if cfg.Client != nil {
		client = *cfg.Client
	} else {
		client = http.Client{Timeout: defaultFetchTimeout}
	}
	client.CheckRedirect = fetcher.checkRedirect
	fetcher.client = &client

	return fetcher
}

// Fetch downloads rawURL and returns its body. It fails when the URL is not HTTPS on an allowed
// host, a redirect leaves the allowed hosts, the status is not 200, or the body is empty or larger
// than maxBytes.
func (f *HTTPImageFetcher) Fetch(ctx context.Context, rawURL string, maxBytes int) ([]byte, error) {
	if f == nil || f.client == nil {
		return nil, errors.New("capitime: image fetcher is not initialized")
	}
	if ctx == nil {
		return nil, errors.New("capitime: image fetch context is nil")
	}
	if maxBytes <= 0 {
		return nil, errors.New("capitime: image fetch size limit must be positive")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("capitime: image URL is invalid")
	}
	err = f.checkURL(parsed)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, errors.New("capitime: build image request failed")
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, errors.New("capitime: image request failed")
	}
	defer func() {
		// The body is fully read or abandoned by now; a close failure changes nothing for the caller.
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("capitime: image request returned a non-200 status")
	}
	if resp.ContentLength > int64(maxBytes) {
		return nil, errors.New("capitime: image is larger than the limit")
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return nil, errors.New("capitime: read image failed")
	}
	if len(data) > maxBytes {
		return nil, errors.New("capitime: image is larger than the limit")
	}
	if len(data) == 0 {
		return nil, errors.New("capitime: image is empty")
	}

	return data, nil
}

func (f *HTTPImageFetcher) checkURL(u *url.URL) error {
	isHTTPS := u.Scheme == "https"
	hasUserInfo := u.User != nil
	_, isAllowedHost := f.allowed[strings.ToLower(u.Host)]

	if !isHTTPS || hasUserInfo || !isAllowedHost {
		return errors.New("capitime: image URL is not an allowed HTTPS address")
	}

	return nil
}

func (f *HTTPImageFetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxImageRedirects {
		return errors.New("capitime: image request redirected too many times")
	}

	return f.checkURL(req.URL)
}

// isDiscordImageURL reports whether raw is an HTTPS URL without credentials on a Discord CDN host.
func isDiscordImageURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}

	isHTTPS := parsed.Scheme == "https"
	host := strings.ToLower(parsed.Host)
	isAllowedHost := false
	for _, allowed := range DiscordImageHosts() {
		if host == allowed {
			isAllowedHost = true
		}
	}

	hasNoUserInfo := parsed.User == nil
	isAllowedURL := isHTTPS && hasNoUserInfo && isAllowedHost

	return isAllowedURL
}
