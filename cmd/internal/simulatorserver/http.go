package simulatorserver

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/k-kohey/axe/internal/simruntime"
	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"nhooyr.io/websocket"
)

//go:embed web/*
var webAssets embed.FS

type HTTPServer struct {
	runtime simruntime.Manager
	version string
	mux     *http.ServeMux
}

func NewHTTPServer(runtime simruntime.Manager, version string) *HTTPServer {
	s := &HTTPServer{
		runtime: runtime,
		version: version,
		mux:     http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *HTTPServer) Handler() http.Handler {
	return s.mux
}

func (s *HTTPServer) routes() {
	web, err := fs.Sub(webAssets, "web")
	if err != nil {
		panic(err)
	}
	s.mux.HandleFunc("/", s.handleRoot)
	s.mux.HandleFunc("/ui", s.handleUIRedirect)
	s.mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(web))))
	s.mux.HandleFunc("/v1/health", s.handleHealth)
	s.mux.HandleFunc("/v1/devices", s.handleDevices)
	s.mux.HandleFunc("/v1/managed-devices", s.handleManagedDevices)
	s.mux.HandleFunc("/v1/managed-devices/", s.handleManagedDeviceResource)
	s.mux.HandleFunc("/v1/sessions", s.handleSessions)
	s.mux.HandleFunc("/v1/sessions/", s.handleSessionResource)
}

func (s *HTTPServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

func (s *HTTPServer) handleUIRedirect(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ui" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeProto(w, &simulatorv1.HealthResponse{Status: "ok", Version: s.version})
}

func (s *HTTPServer) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	devices, err := s.runtime.ListDevices(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeProto(w, &simulatorv1.ListDevicesResponse{DeviceTypes: deviceTypesToProto(devices)})
}

func (s *HTTPServer) handleManagedDevices(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/managed-devices" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		devices, err := s.runtime.ListManagedDevices(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeProto(w, &simulatorv1.ListManagedDevicesResponse{Devices: managedDevicesToProto(devices)})
	case http.MethodPost:
		req := &simulatorv1.AddManagedDeviceRequest{}
		if err := readProto(r, req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		device, err := s.runtime.AddManagedDevice(r.Context(), addManagedDeviceFromProto(req))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeProto(w, &simulatorv1.AddManagedDeviceResponse{Device: managedDeviceToProto(*device)})
	default:
		methodNotAllowed(w)
	}
}

func (s *HTTPServer) handleManagedDeviceResource(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/managed-devices/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	udid := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodDelete {
			methodNotAllowed(w)
			return
		}
		if err := s.runtime.RemoveManagedDevice(r.Context(), udid); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeProto(w, &simulatorv1.RemoveManagedDeviceResponse{})
		return
	}
	if len(parts) == 2 && parts[1] == "default" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		if err := s.runtime.SetDefaultManagedDevice(r.Context(), udid); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeProto(w, &simulatorv1.SetDefaultManagedDeviceResponse{})
		return
	}
	http.NotFound(w, r)
}

func (s *HTTPServer) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/sessions" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		writeProto(w, &simulatorv1.ListSessionsResponse{Sessions: sessionsToProto(s.runtime.ListSessions())})
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	req := &simulatorv1.CreateSessionRequest{}
	if err := readProto(r, req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	session, err := s.runtime.CreateSession(r.Context(), createSessionFromProto(req))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeProto(w, &simulatorv1.CreateSessionResponse{Session: sessionToProto(session)})
}

func (s *HTTPServer) handleSessionResource(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	sessionID := parts[0]
	if len(parts) == 1 {
		s.handleSession(w, r, sessionID)
		return
	}
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "input":
		s.handleInput(w, r, sessionID)
	case "input-stream":
		s.handleInputStream(w, r, sessionID)
	case "install":
		s.handleInstall(w, r, sessionID)
	case "launch":
		s.handleLaunch(w, r, sessionID)
	case "terminate":
		s.handleTerminate(w, r, sessionID)
	case "events":
		s.handleEvents(w, r, sessionID)
	case "video":
		s.handleVideo(w, r, sessionID)
	default:
		http.NotFound(w, r)
	}
}

func (s *HTTPServer) handleSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	switch r.Method {
	case http.MethodGet:
		session, err := s.runtime.GetSession(sessionID)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeProto(w, sessionToProto(session))
	case http.MethodDelete:
		if err := s.runtime.StopSession(r.Context(), sessionID); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeProto(w, &simulatorv1.StopSessionResponse{})
	default:
		methodNotAllowed(w)
	}
}

func (s *HTTPServer) handleInput(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	req := &simulatorv1.SendInputRequest{}
	if err := readProto(r, req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.SessionId == "" {
		req.SessionId = sessionID
	}
	if err := s.runtime.SendInput(r.Context(), req.GetSessionId(), inputFromProto(req.GetInput())); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeProto(w, &simulatorv1.SendInputResponse{})
}

func (s *HTTPServer) handleInputStream(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		slog.Debug("failed to accept input websocket", "session", sessionID, "err", err)
		return
	}
	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()

	ctx := r.Context()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
				websocket.CloseStatus(err) == websocket.StatusGoingAway {
				return
			}
			slog.Debug("input websocket read failed", "session", sessionID, "err", err)
			return
		}
		input := &simulatorv1.InputEvent{}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, input); err != nil {
			writeInputStreamError(ctx, conn, fmt.Errorf("invalid input event: %w", err))
			continue
		}
		if err := s.runtime.SendInput(ctx, sessionID, inputFromProto(input)); err != nil {
			writeInputStreamError(ctx, conn, err)
		}
	}
}

func writeInputStreamError(ctx context.Context, conn *websocket.Conn, err error) {
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	data, marshalErr := json.Marshal(map[string]string{"error": err.Error()})
	if marshalErr != nil {
		return
	}
	_ = conn.Write(writeCtx, websocket.MessageText, data)
}

func (s *HTTPServer) handleInstall(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	req := &simulatorv1.InstallAppRequest{}
	if err := readProto(r, req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.SessionId == "" {
		req.SessionId = sessionID
	}
	if err := s.runtime.InstallApp(r.Context(), req.GetSessionId(), req.GetAppPath()); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeProto(w, &simulatorv1.InstallAppResponse{})
}

func (s *HTTPServer) handleLaunch(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	req := &simulatorv1.LaunchAppRequest{}
	if err := readProto(r, req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.SessionId == "" {
		req.SessionId = sessionID
	}
	if err := s.runtime.LaunchApp(r.Context(), req.GetSessionId(), req.GetBundleId(), req.GetEnv(), req.GetArgs()); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeProto(w, &simulatorv1.LaunchAppResponse{})
}

func (s *HTTPServer) handleTerminate(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	req := &simulatorv1.TerminateAppRequest{}
	if err := readProto(r, req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.SessionId == "" {
		req.SessionId = sessionID
	}
	if err := s.runtime.TerminateApp(r.Context(), req.GetSessionId(), req.GetBundleId()); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeProto(w, &simulatorv1.TerminateAppResponse{})
}

func (s *HTTPServer) handleEvents(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming is not supported"))
		return
	}
	events, err := s.runtime.SubscribeEvents(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			data, err := protojson.Marshal(eventToProto(event))
			if err != nil {
				slog.Debug("failed to marshal event", "err", err)
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *HTTPServer) handleVideo(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming is not supported"))
		return
	}
	frames, err := s.runtime.WatchVideo(r.Context(), sessionID, 30)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	mw := multipart.NewWriter(w)
	defer func() {
		if err := mw.Close(); err != nil {
			slog.Debug("failed to close multipart writer", "err", err)
		}
	}()
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+mw.Boundary())
	for {
		select {
		case <-r.Context().Done():
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			part, err := mw.CreatePart(textproto.MIMEHeader{
				"Content-Type": {"image/jpeg"},
			})
			if err != nil {
				return
			}
			if _, err := part.Write(frame.JPEG); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func readProto(r *http.Request, msg proto.Message) error {
	defer func() {
		if err := r.Body.Close(); err != nil {
			slog.Debug("failed to close request body", "err", err)
		}
	}()
	data, err := readAll(r.Context(), r)
	if err != nil {
		return err
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(data, msg)
}

func readAll(ctx context.Context, r *http.Request) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return io.ReadAll(r.Body)
}

func writeProto(w http.ResponseWriter, msg proto.Message) {
	data, err := protojson.MarshalOptions{EmitDefaultValues: true}.Marshal(msg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	//nolint:gosec // data is produced by protojson marshaling, not user-provided HTML.
	if _, err := w.Write(data); err != nil {
		slog.Debug("failed to write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
}
