// Package sabnzbd is a minimal client for the SABnzbd HTTP API:
// https://sabnzbd.org/wiki/advanced/api
package sabnzbd

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	APIKey     string
	SkipVerify bool
	httpClient *http.Client
}

func New(baseURL, apiKey string, skipVerify bool) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:     apiKey,
		SkipVerify: skipVerify,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify}, //nolint:gosec // explicit opt-in for self-signed setups
			},
		},
	}
}

func (c *Client) apiURL(params url.Values) string {
	params.Set("apikey", c.APIKey)
	params.Set("output", "json")
	return c.BaseURL + "/api?" + params.Encode()
}

type versionResp struct {
	Version string `json:"version"`
}

// TestConnection checks that the SABnzbd URL and API key are valid by
// calling mode=version.
func (c *Client) TestConnection() (version string, err error) {
	if c.BaseURL == "" {
		return "", fmt.Errorf("SABnzbd URL is not configured")
	}
	req, err := http.NewRequest(http.MethodGet, c.apiURL(url.Values{"mode": {"version"}}), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach SABnzbd: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SABnzbd returned HTTP %d: %s", resp.StatusCode, trim(body))
	}
	var v versionResp
	if err := json.Unmarshal(body, &v); err != nil || v.Version == "" {
		// SABnzbd responds with plain 401-ish JSON errors on bad api key
		if apiErr := parseAPIError(body); apiErr != "" {
			return "", fmt.Errorf("SABnzbd error: %s", apiErr)
		}
		return "", fmt.Errorf("unexpected response from SABnzbd (check the URL): %s", trim(body))
	}
	return v.Version, nil
}

type catsResp struct {
	Categories []string `json:"categories"`
}

// GetCategories fetches the live list of categories configured in SABnzbd.
func (c *Client) GetCategories() ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, c.apiURL(url.Values{"mode": {"get_cats"}}), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach SABnzbd: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SABnzbd returned HTTP %d", resp.StatusCode)
	}
	var c2 catsResp
	if err := json.Unmarshal(body, &c2); err != nil {
		if apiErr := parseAPIError(body); apiErr != "" {
			return nil, fmt.Errorf("SABnzbd error: %s", apiErr)
		}
		return nil, fmt.Errorf("could not parse categories from SABnzbd")
	}
	// Drop the "*" (Default) pseudo category from the raw list; callers can
	// add it back explicitly if desired.
	out := make([]string, 0, len(c2.Categories))
	for _, cat := range c2.Categories {
		if cat == "*" {
			continue
		}
		out = append(out, cat)
	}
	return out, nil
}

type addResp struct {
	Status bool     `json:"status"`
	NzoIDs []string `json:"nzo_ids"`
	Error  string   `json:"error"`
}

// DownloadStatus is the normalized subset of SABnzbd queue/history data
// shown by Flipper's live downloads card.
type DownloadStatus struct {
	NZOID      string `json:"nzoId"`
	State      string `json:"state"`
	Percentage int    `json:"percentage"`
	SizeLeft   string `json:"sizeLeft"`
	TimeLeft   string `json:"timeLeft"`
}

type statusSlot struct {
	NZOID      string `json:"nzo_id"`
	Status     string `json:"status"`
	Percentage any    `json:"percentage"`
	SizeLeft   string `json:"sizeleft"`
	TimeLeft   string `json:"timeleft"`
}

func percentageValue(v any) int {
	switch value := v.(type) {
	case string:
		n, err := strconv.Atoi(strings.TrimSuffix(value, "%"))
		if err == nil {
			return n
		}
	case float64:
		return int(value)
	}
	return -1
}

func (c *Client) getJSON(params url.Values, dst any) error {
	req, err := http.NewRequest(http.MethodGet, c.apiURL(params), nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach SABnzbd: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SABnzbd returned HTTP %d: %s", resp.StatusCode, trim(body))
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("could not parse SABnzbd response: %s", trim(body))
	}
	if apiErr := parseAPIError(body); apiErr != "" {
		return fmt.Errorf("SABnzbd error: %s", apiErr)
	}
	return nil
}

// GetDownloadStatuses looks up selected jobs in both the active queue and
// completed/failed history. SABnzbd supports filtering both calls by nzo_id.
func (c *Client) GetDownloadStatuses(nzoIDs []string) (map[string]DownloadStatus, error) {
	result := make(map[string]DownloadStatus, len(nzoIDs))
	if len(nzoIDs) == 0 {
		return result, nil
	}
	filter := strings.Join(nzoIDs, ",")
	var queue struct {
		Queue struct {
			Slots []statusSlot `json:"slots"`
		} `json:"queue"`
	}
	if err := c.getJSON(url.Values{"mode": {"queue"}, "nzo_ids": {filter}, "limit": {"100"}}, &queue); err != nil {
		return nil, err
	}
	for _, slot := range queue.Queue.Slots {
		result[slot.NZOID] = DownloadStatus{
			NZOID: slot.NZOID, State: slot.Status, Percentage: percentageValue(slot.Percentage),
			SizeLeft: slot.SizeLeft, TimeLeft: slot.TimeLeft,
		}
	}

	var history struct {
		History struct {
			Slots []statusSlot `json:"slots"`
		} `json:"history"`
	}
	if err := c.getJSON(url.Values{"mode": {"history"}, "nzo_ids": {filter}, "limit": {"100"}}, &history); err != nil {
		return nil, err
	}
	for _, slot := range history.History.Slots {
		percentage := -1
		if strings.EqualFold(slot.Status, "completed") {
			percentage = 100
		}
		result[slot.NZOID] = DownloadStatus{
			NZOID: slot.NZOID, State: slot.Status, Percentage: percentage,
			SizeLeft: "0 B", TimeLeft: "—",
		}
	}
	return result, nil
}

// AddNZB uploads raw NZB file bytes to SABnzbd under the given category.
func (c *Client) AddNZB(filename string, nzb []byte, category string) ([]string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	if category != "" {
		_ = mw.WriteField("cat", category)
	}
	_ = mw.WriteField("nzbname", filename)

	part, err := mw.CreateFormFile("name", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(nzb); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	q := url.Values{"mode": {"addfile"}}
	req, err := http.NewRequest(http.MethodPost, c.apiURL(q), &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach SABnzbd: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SABnzbd returned HTTP %d: %s", resp.StatusCode, trim(body))
	}

	var ar addResp
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("could not parse SABnzbd response: %s", trim(body))
	}
	if !ar.Status {
		msg := ar.Error
		if msg == "" {
			msg = "SABnzbd rejected the NZB"
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return ar.NzoIDs, nil
}

func parseAPIError(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil {
		return e.Error
	}
	return ""
}

func trim(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
