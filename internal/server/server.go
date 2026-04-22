// Package server exposes a haemil Runtime behind a minimal HTTP API so
// external clients can dispatch turns (POST /v1/turn) and subscribe to
// runtime events (GET /v1/events, Server-Sent Events).
//
// MVP scope (Phase 4 C16):
//   - Single default tenant, wired the same way as the CLI REPL.
//   - Localhost binding by default; non-loopback bind logs a warning.
//   - No auth / TLS / CORS / rate limiting — front this with a reverse
//     proxy or an admin gateway in production.
//   - Graceful shutdown on ctx cancellation: HTTP drains, Supervisor
//     closes (which closes the Session).
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ShinKwangsub/haemil/internal/cli"
	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// DefaultTenantID is the fallback tenant label used when cfg.TenantID is
// empty. Supervisor requires a non-empty ID as a routing key.
const DefaultTenantID = "default"

// DefaultAddr is the address used when cfg.Addr is empty. Loopback only
// — we never accidentally bind to a public interface.
const DefaultAddr = "127.0.0.1:8080"

// shutdownTimeout is how long we wait for in-flight requests and
// Supervisor drain on ctx cancellation before giving up.
const shutdownTimeout = 5 * time.Second

// Run starts the HTTP server and blocks until ctx is cancelled. It
// shares its wiring with the CLI REPL via cli.BuildRuntime — provider,
// session, tools, policy, hooks, MCP, memory are constructed identically.
// The session is handed to a Supervisor so tenant lifecycle is uniform
// with the multi-tenant world we'll grow into.
func Run(ctx context.Context, cfg cli.Config) error {
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}

	br, cleanup, err := cli.BuildRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	tenant := br.Tenant
	if tenant.ID == "" {
		tenant.ID = DefaultTenantID
	}

	bus := runtime.NewEventBus()
	defer bus.Close()

	sup := runtime.NewSupervisor()
	if err := sup.Register(tenant, br.Runtime, runtime.RegisterOpts{
		QueueSize: 4,
		EventBus:  bus,
	}); err != nil {
		return fmt.Errorf("server: supervisor register: %w", err)
	}
	// Supervisor.Close closes the tenant's Session — we do NOT close it
	// separately (that would be a double-close).

	addr := cfg.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	warnIfNonLoopback(addr, cfg.Stderr)

	handler := newMux(sup, bus, tenant.ID)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Startup banner.
	fmt.Fprintf(cfg.Stdout, "haemil — serve mode (tenant %s, mode %s) listening on %s\n",
		tenant.ID, br.Mode, addr)
	if len(br.MCPRegistry.Servers) > 0 {
		fmt.Fprintf(cfg.Stdout, "mcp: %d server(s), %d tool(s) registered\n",
			len(br.MCPRegistry.Servers), len(br.MCPRegistry.Tools))
	}
	if br.HooksRunner.Enabled() {
		fmt.Fprintf(cfg.Stdout, "hooks: %d pre + %d post loaded from %s\n",
			len(br.HooksConfig.PreToolUse), len(br.HooksConfig.PostToolUse), br.HooksPath)
	}

	// Start listener.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("server: listen %s: %w", addr, err)
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpSrv.Serve(ln)
	}()

	// Wait for ctx cancel or fatal serve error.
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server: serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		// Graceful shutdown path.
	}

	fmt.Fprintln(cfg.Stdout, "server: shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := httpSrv.Shutdown(shutCtx); err != nil {
		fmt.Fprintf(cfg.Stderr, "server: http shutdown: %v\n", err)
	}
	if err := sup.Close(shutCtx); err != nil {
		fmt.Fprintf(cfg.Stderr, "server: supervisor close: %v\n", err)
	}
	return nil
}

// warnIfNonLoopback logs a stderr warning when addr binds to a
// non-loopback interface. Auth is not implemented — callers who bind
// publicly are doing it on purpose, and should have a reverse proxy.
func warnIfNonLoopback(addr string, stderr io.Writer) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}
	if host == "" || host == "0.0.0.0" || host == "::" ||
		!(host == "127.0.0.1" || host == "::1" || host == "localhost") {
		fmt.Fprintf(stderr,
			"warning: binding %s — haemil serve mode has NO AUTH; front with a reverse proxy for non-local use\n",
			addr)
	}
}

// newMux builds the routing table. Exposed as a standalone func so
// tests can construct a handler without a live Listen.
func newMux(sup *runtime.Supervisor, bus *runtime.EventBus, tenantID string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/v1/turn", handleTurn(sup, tenantID))
	mux.HandleFunc("/v1/events", handleEvents(bus))
	return mux
}

// --- handlers ---------------------------------------------------------

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}

// TurnRequest is the POST /v1/turn body.
type TurnRequest struct {
	Input string `json:"input"`
}

// TurnResponse wraps a TurnSummary for serialisation. We inline instead
// of returning runtime.TurnSummary directly so we can add metadata later
// (latency, tokens cost, warnings) without a breaking change.
type TurnResponse struct {
	TenantID string               `json:"tenant_id"`
	Summary  *runtime.TurnSummary `json:"summary"`
}

func handleTurn(sup *runtime.Supervisor, tenantID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()

		var req TurnRequest
		dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Input) == "" {
			http.Error(w, "bad request: input is empty", http.StatusBadRequest)
			return
		}

		summary, err := sup.RunTurn(r.Context(), tenantID, req.Input)
		if err != nil {
			statusForError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(TurnResponse{
			TenantID: tenantID,
			Summary:  summary,
		})
	}
}

// statusForError maps Supervisor/Runtime failure modes to HTTP codes.
// 499 follows the nginx convention for client-aborted requests.
func statusForError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		http.Error(w, "client canceled", 499)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "deadline exceeded", http.StatusGatewayTimeout)
	case errors.Is(err, runtime.ErrSupervisorClosed):
		http.Error(w, "service unavailable: "+err.Error(), http.StatusServiceUnavailable)
	default:
		http.Error(w, "internal error: "+err.Error(), http.StatusInternalServerError)
	}
}

// handleEvents streams EventBus events as Server-Sent Events. The
// response stays open until the client disconnects or the bus closes.
// Each event is:
//
//	event: <Type>
//	data: <JSON of runtime.Event>
//	\n
func handleEvents(bus *runtime.EventBus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		sub := bus.Subscribe(128, nil)
		defer bus.Unsubscribe(sub)

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-sub.C:
				if !ok {
					return
				}
				body, err := json.Marshal(ev)
				if err != nil {
					// Skip un-marshallable events but don't break the stream.
					continue
				}
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, body)
				flusher.Flush()
			}
		}
	}
}
