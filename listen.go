package poly

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

// ListenAndServe starts an HTTP server with graceful shutdown. It
// handles SIGINT and SIGTERM, drains sessions, shuts down the HTTP
// listener, then force-closes any remaining sessions.
//
// The addr parameter follows [net.Listen] conventions (e.g. ":8080",
// "127.0.0.1:3000"). When empty, the PORT environment variable is
// checked (standard for cloud platforms such as Cloud Run, Fly.io,
// and Railway); if that is also empty, ":8080" is used.
//
// The optional handler parameter sets which [http.Handler] the HTTP
// server routes requests through. When omitted, the poly handler
// serves all requests directly. Pass a custom mux to add routes or
// HTTP-level middleware alongside poly:
//
//	mux := http.NewServeMux()
//	mux.HandleFunc("GET /health", healthCheck)
//	mux.Handle("/{path...}", h)
//	h.ListenAndServe("", mux)
//
// ListenAndServe returns nil on clean shutdown. It only returns an
// error for startup failures such as a port already in use. A second
// signal during shutdown forces an immediate exit.
func (h *Handler[S]) ListenAndServe(addr string, handler ...http.Handler) error {
	addr = resolveAddr(addr)
	srv := h.newServer(addr, handler)

	return h.serve(srv, func() error {
		return srv.ListenAndServe()
	}, displayURL(addr))
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

	return h.serve(srv, func() error {
		return srv.ListenAndServeTLS(certFile, keyFile)
	}, displayTLSURL(addr))
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
// graceful shutdown. The start function is called in a goroutine to
// begin accepting connections. The url string is logged on startup.
func (h *Handler[S]) serve(srv *http.Server, start func() error, url string) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- start()
	}()

	h.cfg.Logger.Info("listening", "url", url)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		signal.Stop(sigCh)
		return err
	case <-sigCh:
	}

	h.cfg.Logger.Info("shutting down")

	// A second signal during shutdown forces an immediate exit.
	go func() {
		<-sigCh
		h.cfg.Logger.Warn("forced exit")
		os.Exit(1)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.Timeouts.ShutdownGrace)
	defer cancel()

	if err := h.Drain(ctx); err != nil {
		h.cfg.Logger.Warn("drain timed out, forcing shutdown")
	}

	srv.Shutdown(ctx)
	h.Shutdown(ctx)

	h.cfg.Logger.Info("shutdown complete")
	signal.Stop(sigCh)
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
