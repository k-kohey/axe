package simulatorserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestHTTPServerHealth(t *testing.T) {
	server := NewHTTPServer(newFakeManager(), "test-version")
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := &simulatorv1.HealthResponse{}
	if err := protojson.Unmarshal(rec.Body.Bytes(), resp); err != nil {
		t.Fatal(err)
	}
	if resp.GetStatus() != "ok" || resp.GetVersion() != "test-version" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestHTTPServerCreateSession(t *testing.T) {
	manager := newFakeManager()
	server := NewHTTPServer(manager, "test")
	body := `{
		"deviceType": "device",
		"runtime": "runtime",
		"appBundle": {"path": "/tmp/App.app", "bundleId": "com.example.App"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(manager.createReqs) != 1 {
		t.Fatalf("create calls = %d", len(manager.createReqs))
	}
	got := manager.createReqs[0]
	if got.Target.AppBundle == nil || got.Target.AppBundle.Path != "/tmp/App.app" {
		t.Fatalf("unexpected create request: %+v", got)
	}
	resp := &simulatorv1.CreateSessionResponse{}
	if err := protojson.Unmarshal(rec.Body.Bytes(), resp); err != nil {
		t.Fatal(err)
	}
	if resp.GetSession().GetSessionId() != "session-1" {
		t.Fatalf("unexpected session response: %+v", resp.GetSession())
	}
}

func TestHTTPServerSendInputUsesPathSessionID(t *testing.T) {
	manager := newFakeManager()
	server := NewHTTPServer(manager, "test")
	body := `{"input":{"tap":{"x":0.25,"y":0.75}}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/session-1/input", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(manager.inputs) != 1 {
		t.Fatalf("input calls = %d", len(manager.inputs))
	}
	if manager.inputs[0].Tap == nil || manager.inputs[0].Tap.X != 0.25 || manager.inputs[0].Tap.Y != 0.75 {
		t.Fatalf("unexpected input: %+v", manager.inputs[0])
	}
}

func TestHTTPServerLaunchApp(t *testing.T) {
	manager := newFakeManager()
	server := NewHTTPServer(manager, "test")
	body := `{
		"bundleId": "com.example.App",
		"env": {"SIMCTL_CHILD_FOO": "bar"},
		"args": ["--flag"]
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/session-1/launch", strings.NewReader(body))
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(manager.launched) != 1 {
		t.Fatalf("launch calls = %d", len(manager.launched))
	}
	got := manager.launched[0]
	if got.sessionID != "session-1" || got.bundleID != "com.example.App" {
		t.Fatalf("launch = %+v", got)
	}
	if got.env["SIMCTL_CHILD_FOO"] != "bar" || len(got.args) != 1 || got.args[0] != "--flag" {
		t.Fatalf("launch details = %+v", got)
	}
}

func TestHTTPServerDeleteSession(t *testing.T) {
	manager := newFakeManager()
	server := NewHTTPServer(manager, "test")
	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/session-1", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(manager.stoppedIDs) != 1 || manager.stoppedIDs[0] != "session-1" {
		t.Fatalf("stopped IDs = %+v", manager.stoppedIDs)
	}
}
