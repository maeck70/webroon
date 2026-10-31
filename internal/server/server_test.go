package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"webroon/internal/roon"
)

func TestServerRoutesRender(t *testing.T) {
	rc := roon.NewClient()
	srv := New(rc)
	handler := srv.Routes()

	endpoints := []struct {
		method string
		path   string
		expect string
	}{
		{http.MethodGet, "/", "WebRoon"},
		{http.MethodGet, "/ui/status-badge", "Scanning LAN"},
		{http.MethodGet, "/ui/now-playing", "No Active Playback"},
		{http.MethodGet, "/ui/queue", "Playback Queue"},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s %s returned status %d, expected 200", ep.method, ep.path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), ep.expect) {
			t.Errorf("%s %s response did not contain %q", ep.method, ep.path, ep.expect)
		}
	}
}
