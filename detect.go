package tether

import (
	"log/slog"
	"net/http"

	"github.com/jpl-au/tether/protocol"
)

// detectProtocol returns the wire protocol for a request. If r.TLS is
// nil, the request arrived over plain HTTP — assumed HTTP/1.1 because
// browsers require TLS for HTTP/2. Otherwise, r.ProtoMajor
// distinguishes HTTP/1.1 from HTTP/2.
func detectProtocol(r *http.Request) protocol.Protocol {
	if r.TLS == nil {
		return protocol.HTTP1
	}
	if r.ProtoMajor >= 2 {
		return protocol.HTTP2
	}
	return protocol.HTTP1
}

// resolveProtocol returns the effective protocol for a request. When
// the configured protocol is [protocol.Auto], it detects from the
// request. When set explicitly, it trusts the configuration and emits
// a warning if the wire protocol doesn't match.
func resolveProtocol(cfg protocol.Protocol, r *http.Request, logger *slog.Logger) protocol.Protocol {
	detected := detectProtocol(r)
	if cfg == protocol.Auto {
		return detected
	}
	if cfg != detected {
		logger.Warn("tether: protocol mismatch",
			"configured", cfg.String(),
			"detected", detected.String())
	}
	return cfg
}
