package roon

import (
	"net"
	"testing"
)

func TestSoodEncodeAndParse(t *testing.T) {
	props := map[string]string{
		"query_service_id": RoonServiceID,
		"_tid":             "test-tid-1234",
		"service_id":       RoonServiceID,
		"unique_id":        "core-uuid-999",
		"http_port":        "9330",
		"display_name":     "Living Room Roon Server",
	}

	pkt := encodeSoodQuery(props)
	raddr := &net.UDPAddr{IP: net.ParseIP("192.168.1.50"), Port: 9003}
	parsed := parseSoodPacket(pkt, raddr)
	if parsed == nil {
		t.Fatalf("expected non-nil parsed SOOD message")
	}
	if parsed.Type != "Q" {
		t.Errorf("expected type Q, got %q", parsed.Type)
	}
	if parsed.Props["unique_id"] != "core-uuid-999" {
		t.Errorf("unexpected unique_id: %q", parsed.Props["unique_id"])
	}
	if parsed.Props["http_port"] != "9330" {
		t.Errorf("unexpected http_port: %q", parsed.Props["http_port"])
	}
	if parsed.Props["display_name"] != "Living Room Roon Server" {
		t.Errorf("unexpected display_name: %q", parsed.Props["display_name"])
	}
}

func TestMooFrameBuildAndParse(t *testing.T) {
	body := []byte(`{"hierarchy":"browse","pop_all":true}`)
	frame := buildMooFrame("REQUEST", "com.roonlabs.browse:1/browse", "42", "application/json", body)

	msg, err := parseMooFrame(frame)
	if err != nil {
		t.Fatalf("parseMooFrame failed: %v", err)
	}
	if msg.Verb != "REQUEST" {
		t.Errorf("expected Verb REQUEST, got %q", msg.Verb)
	}
	if msg.Service != "com.roonlabs.browse:1" {
		t.Errorf("expected Service com.roonlabs.browse:1, got %q", msg.Service)
	}
	if msg.Name != "browse" {
		t.Errorf("expected Name browse, got %q", msg.Name)
	}
	if msg.RequestID != "42" {
		t.Errorf("expected RequestID 42, got %q", msg.RequestID)
	}
	if string(msg.Body) != string(body) {
		t.Errorf("expected Body %q, got %q", string(body), string(msg.Body))
	}
}
