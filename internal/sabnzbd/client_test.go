package sabnzbd

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGetDownloadStatusesCombinesQueueAndHistory(t *testing.T) {
	t.Parallel()

	client := New("http://sabnzbd.test", "key", false)
	client.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Query().Get("mode") {
		case "queue":
			body = `{"queue":{"slots":[{"nzo_id":"active","status":"Downloading","percentage":"42","sizeleft":"580 MB","timeleft":"0:01:23"}]}}`
		case "history":
			body = `{"history":{"slots":[{"nzo_id":"done","status":"Completed"},{"nzo_id":"bad","status":"Failed"}]}}`
		default:
			body = `{"error":"unexpected mode"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	statuses, err := client.GetDownloadStatuses([]string{"active", "done", "bad"})
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses["active"]; got.State != "Downloading" || got.Percentage != 42 || got.SizeLeft != "580 MB" || got.TimeLeft != "0:01:23" {
		t.Fatalf("active status = %+v", got)
	}
	if got := statuses["done"]; got.State != "Completed" || got.Percentage != 100 {
		t.Fatalf("completed status = %+v", got)
	}
	if got := statuses["bad"]; got.State != "Failed" || got.Percentage != -1 {
		t.Fatalf("failed status = %+v", got)
	}
}
