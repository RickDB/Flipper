// Package spotweb knows how to pull a message-id out of a Spotweb spot URL
// (including the classic Angular-style "#/?page=getspot&messageid=..."
// hash-fragment links Spotweb generates) and fetch the corresponding NZB via
// Spotweb's built-in Newznab-compatible API ({base}/api?t=get&id=...), the
// same mechanism tools like Sonarr/NZBHydra use — no session login required,
// just a per-user API key from the user's Spotweb profile.
package spotweb

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	BaseURL     string
	Username    string
	Password    string
	SkipVerify  bool
	NZBTemplate string // e.g. "{base}/index.php?page=getnzb&messageid={messageid}"
	httpClient  *http.Client
}

func New(baseURL, username, password string, skipVerify bool, nzbTemplate string) *Client {
	if nzbTemplate == "" {
		nzbTemplate = "{base}/api?t=get&id={messageid}&apikey={apikey}"
	}
	return &Client{
		BaseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Username:    username,
		Password:    password,
		SkipVerify:  skipVerify,
		NZBTemplate: nzbTemplate,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify}, //nolint:gosec // explicit opt-in for self-signed setups
			},
		},
	}
}

var messageIDPattern = regexp.MustCompile(`messageid=([^&#]+)`)

// ExtractMessageID pulls the messageid out of a full Spotweb spot URL, e.g.
// https://spotweb.example.com/#/?page=getspot&messageid=QJ5L5tFQ1xspEpQagoyor%40spot.net
// It looks in both the query string and the URL fragment, since Spotweb's
// SPA frontend puts its own query string after the "#".
func ExtractMessageID(spotURL string) (string, error) {
	spotURL = strings.TrimSpace(spotURL)
	if spotURL == "" {
		return "", fmt.Errorf("please paste a Spotweb spot URL")
	}

	m := messageIDPattern.FindStringSubmatch(spotURL)
	if m == nil {
		return "", fmt.Errorf("could not find a messageid in that URL")
	}
	id, err := url.QueryUnescape(m[1])
	if err != nil {
		return "", fmt.Errorf("could not decode messageid: %w", err)
	}
	if id == "" {
		return "", fmt.Errorf("could not find a messageid in that URL")
	}
	return id, nil
}

func (c *Client) setAuth(req *http.Request) {
	if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
}

// TestConnection checks that Spotweb's Newznab-compatible API is reachable
// by hitting its unauthenticated capabilities action ({base}/api?t=caps).
// This confirms the actual subsystem Flipper depends on, not just that the
// web UI loads.
func (c *Client) TestConnection() error {
	if c.BaseURL == "" {
		return fmt.Errorf("Spotweb URL is not configured")
	}
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/api?t=caps", nil)
	if err != nil {
		return err
	}
	c.setAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Spotweb: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("Spotweb rejected the credentials (HTTP 401)")
	case resp.StatusCode >= 400:
		return fmt.Errorf("Spotweb returned HTTP %d at %s/api?t=caps", resp.StatusCode, c.BaseURL)
	}
	if !strings.Contains(string(body), "<caps") {
		return fmt.Errorf("reached %s but it didn't return the expected Spotweb API response — is the URL correct?", c.BaseURL)
	}
	return nil
}

// FetchNZB builds the NZB download URL for the given messageid and fetches
// the raw NZB bytes. apiKey is substituted for {apikey} in the NZB URL
// template — the default template uses Spotweb's Newznab-style API, which
// requires it. Callers resolve which key to pass (the submitting user's own,
// falling back to an admin-configured shared key).
func (c *Client) FetchNZB(messageID, apiKey string) ([]byte, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("Spotweb URL is not configured")
	}
	nzbURL := strings.NewReplacer(
		"{base}", c.BaseURL,
		"{messageid}", url.QueryEscape(messageID),
		"{apikey}", url.QueryEscape(apiKey),
	).Replace(c.NZBTemplate)

	req, err := http.NewRequest(http.MethodGet, nzbURL, nil)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach Spotweb: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // 64MB cap
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Spotweb returned HTTP %d fetching the NZB", resp.StatusCode)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("Spotweb returned an empty NZB — check the messageid and NZB URL template")
	}
	// Sanity check: a genuine NZB (or a gzip of one) should not look like an
	// HTML error page.
	trimmed := strings.TrimSpace(string(body[:min(len(body), 256)]))
	if strings.HasPrefix(strings.ToLower(trimmed), "<html") || strings.HasPrefix(strings.ToLower(trimmed), "<!doctype html") {
		return nil, fmt.Errorf("Spotweb returned an HTML page instead of an NZB (usually a missing or invalid Spotweb API key) — set your personal API key on the Account page, found in Spotweb under your own profile settings")
	}
	return body, nil
}
