package simulatorserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	"github.com/k-kohey/axe/internal/simruntime"
	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

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
	s.mux.HandleFunc("/v1/health", s.handleHealth)
	s.mux.HandleFunc("/v1/devices", s.handleDevices)
	s.mux.HandleFunc("/v1/sessions", s.handleSessions)
	s.mux.HandleFunc("/v1/sessions/", s.handleSessionResource)
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

func (s *HTTPServer) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/sessions" {
		http.NotFound(w, r)
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
	defer mw.Close()
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
	defer r.Body.Close()
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
	_, _ = w.Write(data)
}

func writeError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
}
