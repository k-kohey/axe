# simulator-server

`simulator-server` is a standalone daemon for headless iOS Simulator sessions.
It lives in the axe repository for now, but its package boundaries are shaped so
the daemon can move to a separate module later.

## Boundaries

- `proto/simulator/v1` is the external contract.
- `pkg/simulatorapi/v1` contains generated protobuf and gRPC types intended for
  external Go clients.
- `internal/simruntime` owns simulator lifecycle, idb companion processes,
  video frames, and HID input.
- `internal/simulatorserver` adapts the runtime to gRPC and HTTP.
- `simulator-server` is the daemon binary.

axe code can import `internal/simruntime` directly while this is a monorepo.
Other applications should use the daemon over gRPC or HTTP.

## Run

```sh
go run ./simulator-server
```

Defaults:

- HTTP: `127.0.0.1:3977`
- gRPC: `127.0.0.1:3978`

Useful endpoints:

- `GET /v1/health`
- `GET /v1/devices`
- `POST /v1/sessions`
- `GET /v1/sessions/{session_id}`
- `DELETE /v1/sessions/{session_id}`
- `POST /v1/sessions/{session_id}/install`
- `POST /v1/sessions/{session_id}/launch`
- `POST /v1/sessions/{session_id}/terminate`
- `POST /v1/sessions/{session_id}/input`
- `GET /v1/sessions/{session_id}/events`
- `GET /v1/sessions/{session_id}/video`
