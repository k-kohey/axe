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

## Run as a standalone daemon

```sh
cd cmd
go run ./simulator-server
```

Or build and run a binary:

```sh
cd cmd
go build -o /tmp/simulator-server ./simulator-server
/tmp/simulator-server
```

Defaults:

- HTTP: `127.0.0.1:3977`
- gRPC: `127.0.0.1:3978`

Custom addresses:

```sh
go run ./simulator-server \
  --http 127.0.0.1:3977 \
  --grpc 127.0.0.1:3978
```

Use a custom CoreSimulator device set:

```sh
go run ./simulator-server --device-set /path/to/device-set
```

Enable verbose logging:

```sh
go run ./simulator-server --verbose
```

## HTTP usage

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

Health check:

```sh
curl http://127.0.0.1:3977/v1/health
```

List available simulator device types and runtimes:

```sh
curl http://127.0.0.1:3977/v1/devices
```

Create a session and install/launch an `.app` bundle:

```sh
curl -X POST http://127.0.0.1:3977/v1/sessions \
  -H 'content-type: application/json' \
  -d '{
    "deviceType": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro",
    "runtime": "com.apple.CoreSimulator.SimRuntime.iOS-18-2",
    "appBundle": {
      "path": "/path/to/My.app"
    }
  }'
```

If `appBundle.bundleId` is omitted, the daemon reads it from
`/path/to/My.app/Info.plist`.

Launch an already installed app:

```sh
curl -X POST http://127.0.0.1:3977/v1/sessions \
  -H 'content-type: application/json' \
  -d '{
    "deviceType": "com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro",
    "runtime": "com.apple.CoreSimulator.SimRuntime.iOS-18-2",
    "installedApp": {
      "bundleId": "com.example.MyApp"
    }
  }'
```

Install, launch, or terminate an app in an existing session:

```sh
curl -X POST http://127.0.0.1:3977/v1/sessions/<session_id>/install \
  -H 'content-type: application/json' \
  -d '{"appPath":"/path/to/My.app"}'

curl -X POST http://127.0.0.1:3977/v1/sessions/<session_id>/launch \
  -H 'content-type: application/json' \
  -d '{
    "bundleId": "com.example.MyApp",
    "env": {
      "SIMCTL_CHILD_EXAMPLE": "1"
    },
    "args": ["--example"]
  }'

curl -X POST http://127.0.0.1:3977/v1/sessions/<session_id>/terminate \
  -H 'content-type: application/json' \
  -d '{"bundleId":"com.example.MyApp"}'
```

Send input. Coordinates are normalized from `0.0` to `1.0`:

```sh
curl -X POST http://127.0.0.1:3977/v1/sessions/<session_id>/input \
  -H 'content-type: application/json' \
  -d '{"input":{"tap":{"x":0.5,"y":0.5}}}'

curl -X POST http://127.0.0.1:3977/v1/sessions/<session_id>/input \
  -H 'content-type: application/json' \
  -d '{"input":{"text":{"value":"hello"}}}'
```

Read event stream as Server-Sent Events:

```sh
curl http://127.0.0.1:3977/v1/sessions/<session_id>/events
```

Open the video stream as multipart JPEG:

```sh
open http://127.0.0.1:3977/v1/sessions/<session_id>/video
```

Stop a session:

```sh
curl -X DELETE http://127.0.0.1:3977/v1/sessions/<session_id>
```

## gRPC / Go client usage

The gRPC service is defined in `proto/simulator/v1/simulator.proto`.

Go clients can use `pkg/simulatorclient`:

```go
client, err := simulatorclient.Dial(ctx, "127.0.0.1:3978")
if err != nil {
    return err
}
defer client.Close()

health, err := client.Health(ctx)
```

## API schema

The source of truth is the protobuf schema:

```text
proto/simulator/v1/simulator.proto
```

Generated Go types live under:

```text
pkg/simulatorapi/v1
```

There is currently no OpenAPI schema. The HTTP API uses the protobuf JSON
mapping for request and response bodies, but the routes are implemented with
hand-written HTTP handlers rather than generated from OpenAPI.

If browser/client tooling needs an OpenAPI document, the next step should be to
either generate OpenAPI from the protobuf service or add an explicit
`openapi.yaml` and keep it checked against `simulator.proto`.
