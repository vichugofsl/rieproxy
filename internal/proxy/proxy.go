// Package proxy is the HTTP server that fronts the Lambda RIE: it translates
// each request into an API Gateway event, invokes the function, and writes the
// response back. Invocations are serialized because the RIE is single-threaded.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/vichugofsl/rieproxy/internal/ansi"
	"github.com/vichugofsl/rieproxy/internal/logtail"
	"github.com/vichugofsl/rieproxy/internal/rie"
	"github.com/vichugofsl/rieproxy/internal/translate"
)

// Config holds the runtime options for a proxy.
type Config struct {
	Port             string        // local HTTP listen port
	Target           string        // RIE endpoint URL, e.g. http://127.0.0.1:8080
	Function         string        // RIE function name in the invoke path
	PayloadVersion   string        // "1.0" or "2.0"
	Timeout          time.Duration // per-invocation timeout
	CORS             bool          // send permissive CORS headers
	RestartContainer string        // docker container to restart on invoke failure (optional)
	TailContainers   []string      // docker containers to tail logs from (optional)
}

// Run starts the proxy and blocks until the process receives SIGINT/SIGTERM or
// the server fails.
func Run(cfg Config) error {
	translator, err := translate.New(cfg.PayloadVersion)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, c := range cfg.TailContainers {
		go logtail.Tail(ctx, c)
	}

	srv := &server{
		cfg:        cfg,
		translator: translator,
		client:     rie.New(cfg.Target, cfg.Function, cfg.Timeout),
	}

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           http.HandlerFunc(srv.handle),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown: give in-flight invocations time to finish.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Timeout+10*time.Second)
		defer shutdownCancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	logInfo("listening on :%s -> %s (function=%s, payload=%s)", cfg.Port, cfg.Target, cfg.Function, cfg.PayloadVersion)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

type server struct {
	cfg        Config
	translator translate.Translator
	client     *rie.Client

	// mu serializes invocations: the RIE is single-threaded and panics on
	// concurrent requests. This is fine for local dev.
	mu sync.Mutex

	restartMu   sync.Mutex
	lastRestart time.Time
}

func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if s.cfg.CORS {
		setCORS(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			logRequest(r.Method, r.URL.Path, http.StatusOK, time.Since(start), nil)
			return
		}
	}

	payload, err := s.translator.BuildPayload(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to build lambda request: %v", err), http.StatusInternalServerError)
		logRequest(r.Method, r.URL.Path, http.StatusInternalServerError, time.Since(start), err)
		return
	}

	s.mu.Lock()
	// Use context.Background() (not r.Context()) so a client disconnect does
	// not cancel the in-flight invocation, which would leave the RIE in a
	// broken state (AlreadyReserved -> nil pointer panic) for all later requests.
	raw, err := s.client.Invoke(context.Background(), payload)
	s.mu.Unlock()
	if err != nil {
		s.maybeRestart()
		http.Error(w, fmt.Sprintf("lambda invocation failed: %v", err), http.StatusBadGateway)
		logRequest(r.Method, r.URL.Path, http.StatusBadGateway, time.Since(start), err)
		return
	}

	if s.cfg.CORS {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}

	status, err := s.translator.WriteResponse(w, raw)
	if err != nil {
		logRequest(r.Method, r.URL.Path, http.StatusBadGateway, time.Since(start), err)
		return
	}
	logRequest(r.Method, r.URL.Path, status, time.Since(start), nil)
}

// maybeRestart restarts the configured container after an invocation failure to
// recover from RIE crashes. A 5s cooldown prevents restart storms.
func (s *server) maybeRestart() {
	if s.cfg.RestartContainer == "" {
		return
	}
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	if time.Since(s.lastRestart) < 5*time.Second {
		return
	}
	s.lastRestart = time.Now()

	logInfo("restarting container %q after invocation failure...", s.cfg.RestartContainer)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "restart", s.cfg.RestartContainer).Run(); err != nil {
		logError("failed to restart container: %v", err)
		return
	}
	time.Sleep(2 * time.Second) // give the container time to initialize
	logInfo("container restarted")
}

func setCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Amz-Date, X-Api-Key, X-Amz-Security-Token")
	w.Header().Set("Access-Control-Max-Age", "86400")
}

// --- logging ---

func logInfo(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s[rieproxy]%s %s\n", ansi.Blue+ansi.Bold, ansi.Reset, fmt.Sprintf(format, args...))
}

func logError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s[rieproxy] ERROR%s %s\n", ansi.Red+ansi.Bold, ansi.Reset, fmt.Sprintf(format, args...))
}

func logRequest(method, path string, status int, d time.Duration, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s[rieproxy]%s %-7s %s -> %s%d%s (%s) %serror: %v%s\n",
			ansi.Blue+ansi.Bold, ansi.Reset, method, path, ansi.Red, status, ansi.Reset,
			d.Round(time.Millisecond), ansi.Red, err, ansi.Reset)
		return
	}
	statusColor := ansi.Green
	switch {
	case status >= 500:
		statusColor = ansi.Red
	case status >= 400:
		statusColor = ansi.Yellow
	}
	fmt.Fprintf(os.Stderr, "%s[rieproxy]%s %-7s %s -> %s%d%s (%s)\n",
		ansi.Blue+ansi.Bold, ansi.Reset, method, path, statusColor, status, ansi.Reset, d.Round(time.Millisecond))
}
