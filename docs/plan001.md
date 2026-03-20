# Plan 001: Standalone MCP Web Server (HTTP / SSE Transport)

## Goal

Enable `omni-code mcp` to run as a **long-lived HTTP server**, not just a
single-session stdio pipe.  This makes it possible to connect any MCP-capable
GUI, web dashboard, or remote client to a single running daemon instead of
spawning a fresh process per IDE session.

The MCP go-sdk (`github.com/modelcontextprotocol/go-sdk v0.8.0`) already ships
two HTTP-capable transports:

| Transport | SDK type | MCP spec | Best for |
|---|---|---|---|
| SSE (legacy) | `mcp.SSEHandler` | 2024-11-05 | Wide client compat |
| Streamable HTTP | `mcp.StreamableHTTPHandler` | 2025-03-26 | Modern clients, stateless mode |

No new dependencies are required.

---

## Phase 1 — CLI Flags for Transport Selection `[ ]`

**Files:** `cmd/omni-code/main.go`

Add three new flags to the `mcp` sub-command:

| Flag | Type | Default | Description |
|---|---|---|---|
| `--transport` | string | `stdio` | Transport mode: `stdio`, `sse`, `streamable` |
| `--addr` | string | `:8090` | `host:port` to listen on (HTTP modes only) |
| `--stateless` | bool | `false` | Run `streamable` handler in stateless mode (no session ID) |

The existing `runMCP` function routes to `internalmcp.ServeStdio` when
`--transport stdio` (or no flag given).  New branches call `ServeSSE` and
`ServeStreamable` respectively.

```go
// Pseudocode — actual diff in Phase 3
switch *transport {
case "stdio", "":
    err = internalmcp.ServeStdio(ctx, client)
case "sse":
    err = internalmcp.ServeSSE(ctx, client, *addr)
case "streamable":
    err = internalmcp.ServeStreamable(ctx, client, *addr, *stateless)
default:
    log.Fatalf("[mcp] unknown transport %q", *transport)
}
```

- [ ] Add `--transport`, `--addr`, `--stateless` flags to `runMCP`'s `flag.FlagSet`
- [ ] Update `printUsage` to list the new flags and give example invocations.

---

## Phase 2 — New Server Functions in `internal/mcp` `[ ]`

**Files:** `internal/mcp/server.go`

- [ ] Extract tool registration into a private `buildServer(client)` helper
- [ ] Refactor `ServeStdio` to call `buildServer()`
- [ ] Implement `ServeSSE(ctx, client, addr)`
- [ ] Implement `ServeStreamable(ctx, client, addr, stateless)`
- [ ] Add `/health` endpoint to the streamable mux

Extract the tool-registration step into a private helper so all three transport
modes share identical tool definitions without duplication:

```go
func buildServer() *mcp.Server {
    s := mcp.NewServer(&mcp.Implementation{Name: "omni-code", Version: "1.0.0"}, nil)
    // … AddTool calls (moved from ServeStdio) …
    return s
}
```

Then refactor `ServeStdio` to call `buildServer()` and add two new exported
functions:

### `ServeSSE(ctx, client, addr)`

```go
func ServeSSE(ctx context.Context, client *db.ChromaClient, addr string) error {
    s := buildServer(client)
    handler := mcp.NewSSEHandler(func(_ *http.Request) *mcp.Server { return s }, nil)
    srv := &http.Server{Addr: addr, Handler: handler}
    log.Printf("[mcp] SSE server listening on %s", addr)
    // Shutdown on context cancellation
    go func() { <-ctx.Done(); srv.Shutdown(context.Background()) }()
    return srv.ListenAndServe()
}
```

### `ServeStreamable(ctx, client, addr string, stateless bool)`

```go
func ServeStreamable(ctx context.Context, client *db.ChromaClient, addr string, stateless bool) error {
    s := buildServer(client)
    opts := &mcp.StreamableHTTPOptions{Stateless: stateless}
    handler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server { return s }, opts)
    mux := http.NewServeMux()
    mux.Handle("/mcp", handler)
    mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(`{"status":"ok"}`))
    })
    srv := &http.Server{Addr: addr, Handler: mux}
    log.Printf("[mcp] streamable HTTP server listening on %s", addr)
    go func() { <-ctx.Done(); srv.Shutdown(context.Background()) }()
    return srv.ListenAndServe()
}
```

The `/health` endpoint lets operators (and GUI dashboards) probe liveness
without opening an MCP session.

---

## Phase 3 — Wire It All Together `[ ]`

**Files:** `cmd/omni-code/main.go` (full diff)

- [ ] Add the three flags to the `flag.FlagSet` inside `runMCP`
- [ ] Route to the correct `internalmcp.ServeXxx` function via `switch *transport`
- [ ] Update the usage string
- [ ] Update the top-level `printUsage` description to say "HTTP/stdio MCP server"

---

## Phase 4 — Signal Handling & Graceful Shutdown `[ ]`

- [ ] Replace `context.Background()` in `runMCP` with `signal.NotifyContext` for `os.Interrupt` / `syscall.SIGTERM`
- [ ] Verify graceful drain on `Ctrl-C` manually

The `watch` command already handles `os.Interrupt` / `syscall.SIGTERM`.  Extend
the same pattern inside `runMCP` for HTTP modes:

```go
ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer cancel()
```

This lets `Ctrl-C` drain active sessions cleanly before the process exits.

---

## Phase 5 — CORS for Browser / GUI Clients `[ ]`

- [ ] Add `--cors` flag to the `mcp` sub-command
- [ ] Implement `corsMiddleware(http.Handler) http.Handler` in `internal/mcp/server.go`
- [ ] Wrap handler with CORS middleware only when `--cors` is set
- [ ] Document security implications in flag help text

If the user plans to connect a browser-based GUI (e.g. MCP Inspector web app),
the HTTP handler needs permissive CORS headers.  Wrap the `http.Handler` with a
thin CORS middleware only when `--cors` flag is set (opt-in, not default):

```go
// --cors flag: add Access-Control-Allow-* headers
if *cors {
    handler = corsMiddleware(handler)
}
```

`corsMiddleware` allows `*` origin, `GET`, `POST`, `OPTIONS` methods, and the
`Content-Type` + `Mcp-*` headers required by the spec.  It is ~15 lines and
lives in `internal/mcp/server.go`.

**Security note:** `--cors` must not be enabled by default.  When omitted, the
server binds to localhost only and no cross-origin requests are permitted.
Document this clearly in the flag help text.

---

## Phase 6 — README & `.vscode/mcp.json` Updates `[ ]`

- [ ] Add HTTP/SSE mode subsection to README under **MCP Server**
- [ ] Add note that `--transport stdio` remains unchanged for VS Code / Copilot CLI
- [ ] Note in `.vscode/mcp.json` that HTTP modes are for external GUIs only

### README.md

Add a new subsection under **MCP Server**:

```
#### HTTP / SSE mode

# SSE mode (legacy, broadest client support)
./bin/omni-code mcp --transport sse --addr :8090

# Streamable HTTP (modern spec, recommended for new GUIs)
./bin/omni-code mcp --transport streamable --addr :8090

# Health probe
curl http://localhost:8090/health
```

Include a note that `--transport stdio` (the default) is unchanged and still
required for VS Code / Copilot CLI integration.

### `.vscode/mcp.json`

No change needed — VS Code always uses stdio.  Document that the HTTP modes are
for external GUIs only.

---

## Phase 7 — Tests `[ ]`

**Files:** `internal/mcp/server_test.go`

| Test | Description |
|---|---|
| `TestServeSSE_health` | Start SSE server on a random port, hit `/sse`, check HTTP 200. |
| `TestServeStreamable_health` | Start streamable server, hit `/health`, check `{"status":"ok"}`. |
| `TestBuildServer_tools` | Assert all 8 expected tool names are registered. |
| `TestCORSMiddleware` | Confirm CORS headers are absent by default, present when enabled. |

- [ ] `TestServeSSE_health`
- [ ] `TestServeStreamable_health`
- [ ] `TestBuildServer_tools`
- [ ] `TestCORSMiddleware`
- [ ] `go test ./...` passes with no regressions

Use `httptest.NewServer` for isolation; no external dependencies required.

---

## Acceptance Criteria

- [ ] `omni-code mcp` (no flags) behaves identically to today — no regression.
- [ ] `omni-code mcp --transport sse --addr :8090` starts an HTTP server and logs the address.
- [ ] `omni-code mcp --transport streamable --addr :8090` starts and `/health` returns 200.
- [ ] `Ctrl-C` / `SIGTERM` shuts the server down gracefully within 5 seconds.
- [ ] All existing tests pass (`go test ./...`).
- [ ] New unit tests pass.
- [ ] README documents the new modes.
