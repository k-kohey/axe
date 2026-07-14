package simulatorserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"nhooyr.io/websocket"
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

func TestHTTPServerServesUI(t *testing.T) {
	server := NewHTTPServer(newFakeManager(), "test-version")
	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "simulator-server") {
		t.Fatalf("unexpected UI body: %s", rec.Body.String())
	}
}

func TestHTTPServerCreateSession(t *testing.T) {
	manager := newFakeManager()
	server := NewHTTPServer(manager, "test")
	body := `{
		"deviceType": "device",
		"runtime": "runtime",
		"deviceUdid": "UDID-1",
		"noHeadless": true,
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
	if got.DeviceUDID != "UDID-1" || !got.NoHeadless {
		t.Fatalf("unexpected session options: %+v", got)
	}
	resp := &simulatorv1.CreateSessionResponse{}
	if err := protojson.Unmarshal(rec.Body.Bytes(), resp); err != nil {
		t.Fatal(err)
	}
	if resp.GetSession().GetSessionId() != "session-1" {
		t.Fatalf("unexpected session response: %+v", resp.GetSession())
	}
}

func TestHTTPServerListSessions(t *testing.T) {
	manager := newFakeManager()
	server := NewHTTPServer(manager, "test")
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	resp := &simulatorv1.ListSessionsResponse{}
	if err := protojson.Unmarshal(rec.Body.Bytes(), resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.GetSessions()) != 1 || resp.GetSessions()[0].GetSessionId() != "session-1" {
		t.Fatalf("sessions = %+v", resp.GetSessions())
	}
}

func TestHTTPServerManagedDevices(t *testing.T) {
	manager := newFakeManager()
	server := NewHTTPServer(manager, "test")

	req := httptest.NewRequest(http.MethodGet, "/v1/managed-devices", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	listResp := &simulatorv1.ListManagedDevicesResponse{}
	if err := protojson.Unmarshal(rec.Body.Bytes(), listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.GetDevices()) != 1 || !listResp.GetDevices()[0].GetIsDefault() {
		t.Fatalf("managed devices = %+v", listResp.GetDevices())
	}

	body := `{"deviceType":"device","runtime":"runtime","setDefault":true}`
	req = httptest.NewRequest(http.MethodPost, "/v1/managed-devices", strings.NewReader(body))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(manager.addReqs) != 1 || manager.addReqs[0].DeviceType != "device" || !manager.addReqs[0].SetDefault {
		t.Fatalf("add requests = %+v", manager.addReqs)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/managed-devices/UDID-2/default", strings.NewReader("{}"))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("default status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(manager.defaults) != 1 || manager.defaults[0] != "UDID-2" {
		t.Fatalf("defaults = %+v", manager.defaults)
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/managed-devices/UDID-2", nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(manager.removed) != 1 || manager.removed[0] != "UDID-2" {
		t.Fatalf("removed = %+v", manager.removed)
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

func TestHTTPServerInputStream(t *testing.T) {
	manager := newFakeManager()
	server := httptest.NewServer(NewHTTPServer(manager, "test").Handler())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/sessions/session-1/input-stream"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"touchDown":{"x":0.25,"y":0.75}}`)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		inputCount := len(manager.inputs)
		manager.mu.Unlock()
		if inputCount > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.inputs) != 1 {
		t.Fatalf("input calls = %d", len(manager.inputs))
	}
	if manager.inputs[0].TouchDown == nil || manager.inputs[0].TouchDown.X != 0.25 || manager.inputs[0].TouchDown.Y != 0.75 {
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
