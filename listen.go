package tether

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Drainable is satisfied by [*Handler]. It provides graceful shutdown
// in two phases: Drain stops accepting new sessions and waits for
// existing ones to finish; Shutdown force-closes any remaining
// sessions. Use with [ListenAndServe] when multiple handlers share
// a single HTTP server.
type Drainable interface {
	Drain(ctx context.Context) error
	Shutdown(ctx context.Context) error
}

// ListenAndServe starts an HTTP server at addr with the provided
// [http.Handler] and shuts down gracefully on SIGINT or SIGTERM. All
// drainers are drained concurrently, then the HTTP server is stopped,
// then remaining sessions are force-closed.
//
// Use this when multiple tether handlers share a single mux:
//
//	mux := http.NewServeMux()
//	mux.Handle("/ws/", wsHandler)
//	mux.Handle("/sse/", sseHandler)
//	tether.ListenAndServe("", mux, wsHandler, sseHandler)
//
// The addr parameter follows [net.Listen] conventions. When empty,
// the PORT environment variable is checked; if that is also empty,
// ":8080" is used.
//
// Returns nil on clean shutdown. Returns an error only for startup
// failures such as a port already in use. A second signal during
// shutdown forces an immediate exit.
//
// For single-handler apps, [Handler.ListenAndServe] is simpler — it
// uses the handler's configured ShutdownGrace timeout and serves
// itself without a separate mux.
func ListenAndServe(addr string, handler http.Handler, drainers ...Drainable) error {
	addr = resolveAddr(addr)
	srv := &http.Server{Addr: addr, Handler: handler}

	return serve(srv, func() error {
		return srv.ListenAndServe()
	}, displayURL(addr), defaultShutdownGrace, drainers)
}

// ListenAndServeTLS starts an HTTPS server with graceful shutdown.
// It behaves identically to [ListenAndServe] but accepts TLS
// certificate and key file paths. If addr is empty and the PORT
// environment variable is not set, ":443" is used.
func ListenAndServeTLS(addr, certFile, keyFile string, handler http.Handler, drainers ...Drainable) error {
	if addr == "" && os.Getenv("PORT") == "" {
		addr = ":443"
	}
	addr = resolveAddr(addr)
	srv := &http.Server{Addr: addr, Handler: handler}

	return serve(srv, func() error {
		return srv.ListenAndServeTLS(certFile, keyFile)
	}, displayTLSURL(addr), defaultShutdownGrace, drainers)
}

// Handler.ListenAndServe starts an HTTP server with graceful shutdown.
// It handles SIGINT and SIGTERM, drains sessions, shuts down the HTTP
// listener, then force-closes any remaining sessions.
//
// The addr parameter follows [net.Listen] conventions (e.g. ":8080",
// "127.0.0.1:3000"). When empty, the PORT environment variable is
// checked (standard for cloud platforms such as Cloud Run, Fly.io,
// and Railway); if that is also empty, ":8080" is used.
//
// The optional handler parameter sets which [http.Handler] the HTTP
// server routes requests through. When omitted, the tether handler
// serves all requests directly. Pass a custom mux to add routes or
// HTTP-level middleware alongside tether:
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("GET /health", healthCheck)
//	mux.Handle("/{path...}", h)
//	h.ListenAndServe("", mux)
//
// ListenAndServe returns nil on clean shutdown. It only returns an
// error for startup failures such as a port already in use. A second
// signal during shutdown forces an immediate exit.
//
// For multi-handler apps, use the package-level [ListenAndServe]
// which drains and shuts down all handlers.
func (h *Handler[S]) ListenAndServe(addr string, handler ...http.Handler) error {
	addr = resolveAddr(addr)
	srv := h.newServer(addr, handler)

	return serve(srv, func() error {
		return srv.ListenAndServe()
	}, displayURL(addr), h.cfg.Timeouts.ShutdownGrace, []Drainable{h})
}

// ListenAndServeTLS starts an HTTPS server with graceful shutdown.
// It behaves identically to [Handler.ListenAndServe] but accepts TLS
// certificate and key file paths. If addr is empty and the PORT
// environment variable is not set, ":443" is used.
func (h *Handler[S]) ListenAndServeTLS(addr, certFile, keyFile string, handler ...http.Handler) error {
	if addr == "" && os.Getenv("PORT") == "" {
		addr = ":443"
	}
	addr = resolveAddr(addr)
	srv := h.newServer(addr, handler)

	return serve(srv, func() error {
		return srv.ListenAndServeTLS(certFile, keyFile)
	}, displayTLSURL(addr), h.cfg.Timeouts.ShutdownGrace, []Drainable{h})
}

// newServer creates an [http.Server] with the resolved handler.
func (h *Handler[S]) newServer(addr string, handler []http.Handler) *http.Server {
	var root http.Handler = h
	if len(handler) > 0 && handler[0] != nil {
		root = handler[0]
	}
	return &http.Server{
		Addr:    addr,
		Handler: root,
	}
}

// serve runs the HTTP server, waits for a signal, and performs
// graceful shutdown. All drainers are drained concurrently, the HTTP
// server is stopped, then remaining sessions are force-closed.
func serve(srv *http.Server, start func() error, url string, grace time.Duration, drainers []Drainable) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- start()
	}()

	slog.Info("listening", "url", url)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		signal.Stop(sigCh)
		return err
	case <-sigCh:
	}

	slog.Info("shutting down")

	// A second signal during shutdown forces an immediate exit.
	// The channel is closed after clean shutdown to release this
	// goroutine — the ok check prevents a force-exit on close.
	go func() {
		_, ok := <-sigCh
		if ok {
			slog.Warn("forced exit", "reason", "second signal during shutdown")
			os.Exit(1)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	// Drain all handlers concurrently — let existing sessions
	// finish naturally before force-closing anything.
	var wg sync.WaitGroup
	for _, d := range drainers {
		wg.Go(func() {
			if err := d.Drain(ctx); err != nil {
				slog.Warn("drain timed out", "error", err)
			}
		})
	}
	wg.Wait()

	// Stop accepting new HTTP requests.
	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("tether: HTTP server shutdown error", "error", err)
	}

	// Force-close any sessions that didn't drain in time.
	for _, d := range drainers {
		d.Shutdown(ctx)
	}

	slog.Info("shutdown complete")
	signal.Stop(sigCh)
	close(sigCh) // unblock the second-signal goroutine
	return nil
}

// resolveAddr returns the listen address, falling back to the PORT
// environment variable, then to ":8080".
func resolveAddr(addr string) string {
	if addr != "" {
		return addr
	}
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}

// displayURL converts a listen address into a clickable HTTP URL for
// log output. Wildcard hosts (empty, "0.0.0.0", "::") are replaced
// with "localhost" because the wildcard is not useful in a terminal.
func displayURL(addr string) string {
	return formatURL("http", addr)
}

// displayTLSURL converts a listen address into a clickable HTTPS URL.
func displayTLSURL(addr string) string {
	return formatURL("https", addr)
}

func formatURL(scheme, addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return scheme + "://localhost" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}
