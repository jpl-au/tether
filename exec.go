package tether

import (
	"fmt"
	"net/url"
	"time"

	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/wire"
)

// exec processes a single client event: handle it, re-render, diff,
// and send patches to the transport.
func (s *StatefulSession[S]) exec(ev Event) {
	now := time.Now()
	s.lastActivity.Store(now.UnixNano())
	if s.idleTimer != nil {
		s.idleTimer.Reset(s.idleTimeout)
	}

	s.stateSnap.Store(s.state)
	fx := &Effects{}
	defer func() {
		if r := recover(); r != nil {
			err := panicErr(r)
			dev.Log().Error("panic in handler", "session", s.id, "action", ev.Action, "panic", r)
			s.emitDiagnostic(Diagnostic{
				Kind:      HandlerPanic,
				SessionID: s.id,
				Err:       err,
				Detail:    ev.Action,
			})
			s.drainFx(nil)
			// State may contain partially mutated reference types
			// (maps, slices) that cannot be trusted. Destroy the
			// session unless the developer has opted into custom
			// recovery via OnPanic.
			if s.onPanic != nil {
				s.onPanic(s, err)
			} else {
				s.stop()
			}
		}
	}()

	dev.Debug("event received",
		"session", s.id,
		"endpoint", s.endpoint,
		"action", ev.Action,
		"type", ev.Type,
	)

	// All events flow through the composed Handle function, which
	// includes middleware, OnNavigate, and component routing.
	s.handling = true
	newState := s.handle(s, s.state, ev)
	s.handling = false

	s.drainFx(fx)

	// Resolve navigate redirects inline. When OnNavigate calls
	// Navigate(), re-process the redirect target server-side rather
	// than round-tripping to the client. Effects from intermediate
	// steps are preserved unless the redirect target overwrites them.
	if ev.Type == "navigate" && fx.URL != "" {
		for i := range s.maxNavigateRedirects {
			redirectURL := fx.URL
			u, err := url.Parse(redirectURL)
			if err != nil {
				dev.Warn("malformed navigate redirect URL",
					"session", s.id, "url", redirectURL, "error", err)
				break
			}

			fx.URL = ""
			fx.Replace = false
			redirectEv := Event{
				Type: "navigate",
				Data: map[string]string{"path": u.Path, "search": u.RawQuery},
			}
			newState = s.handle(s, newState, redirectEv)
			s.drainFx(fx)

			if fx.URL == "" {
				// Redirect resolved - send the target URL as a replace
				// so the client updates the address bar without a
				// history entry or a navigate event back.
				fx.URL = redirectURL
				fx.Replace = true
				break
			}
			if i == s.maxNavigateRedirects-1 {
				dev.Warn("navigate redirect limit reached",
					"session", s.id, "url", fx.URL)
				s.emitDiagnostic(Diagnostic{
					Kind:      NavigateRedirectLoop,
					SessionID: s.id,
					Err:       fmt.Errorf("redirect limit exceeded after %d redirects", s.maxNavigateRedirects),
					Detail:    fx.URL,
				})
				fx.Replace = true
			}
		}
	}

	if s.equal != nil && s.equal(s.state, newState) {
		dev.Debug("state unchanged, skipping render",
			"session", s.id,
			"action", ev.Action,
		)
		if fx.Any() || ev.EventID != "" {
			u := wire.Update{EventID: ev.EventID}
			fx.merge(&u)
			s.send(u)
		}
		return
	}
	s.state = newState
	s.stateSnap.Store(s.state)

	renderStart := time.Now()
	tree := s.render(s.state)
	patches, change := s.engine.Diff(tree)
	renderDuration := time.Since(renderStart)
	dev.Debug("render complete",
		"session", s.id,
		"patches", len(patches),
		"structural", change != nil,
		"duration", renderDuration,
	)
	if s.slowRender > 0 && renderDuration > s.slowRender {
		s.emitDiagnostic(Diagnostic{
			Kind:      SlowRender,
			SessionID: s.id,
			Detail:    renderDuration.String(),
		})
	}
	s.checkMemoiseStats()
	if len(patches) == 0 && change == nil {
		source := string(ev.Type)
		switch {
		case s.onNoPatch != nil:
			s.onNoPatch(s, NoPatch{Source: source, Action: ev.Action})
		case ev.Type == "navigate":
			dev.Debug("navigate produced no patches",
				"session", s.id,
				"endpoint", s.endpoint,
				"url", s.lastURL,
			)
		default:
			dev.Debug("event produced no patches",
				"session", s.id,
				"endpoint", s.endpoint,
				"action", ev.Action,
			)
		}
	}

	s.sendDiff(ev.EventID, patches, change, tree, fx)
}
