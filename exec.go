package tether

import (
	"fmt"
	"net/url"
	"time"

	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/event"
	"github.com/jpl-au/tether/wire"
)

// runHandle invokes the composed Handle function with the handling
// flag raised so State() can warn about stale reads. The flag is
// lowered via defer so a panic recovered by OnPanic cannot leave it
// stuck and turn every later State() call into a bogus warning.
func (s *StatefulSession[S]) runHandle(state S, ev Event) S {
	s.handling.Store(true)
	defer s.handling.Store(false)
	return s.handle(s, state, ev)
}

// resolveHeldNavigate runs the application's Handle for a navigation
// raised while the client was disconnected, so the session's state
// catches up with the URL before the reattach diff is taken.
//
// Without this the client would be told to push a URL for a page
// OnNavigate never saw: it would echo a navigate event straight back
// and render the previous page's DOM under the new address in the
// meantime. Resolving here mirrors the redirect loop in exec - the
// framework settles navigation on the server rather than round-tripping
// - and lets the catch-up sync the address bar with Replace instead of
// pushing a history entry the client would have to answer.
//
// The URL stays in lastURL either way, so the caller's replay covers it.
// Runs on the loop goroutine, from reattach's command.
func (s *StatefulSession[S]) resolveHeldNavigate() {
	if s.heldFx == nil || s.heldFx.URL == "" {
		return
	}
	target := s.heldFx.URL
	// Consumed here: the catch-up replays it from lastURL as a
	// Replace, so leaving it would push a duplicate history entry.
	s.heldFx.URL = ""
	s.heldFx.Replace = false

	u, err := url.Parse(target)
	if err != nil {
		dev.Warn("malformed held navigate URL",
			"session", s.id, "url", target, "error", err)
		return
	}

	fx := &Effects{}
	defer func() {
		if r := recover(); r != nil {
			err := panicErr(r)
			dev.Log().Error("panic resolving held navigate", "session", s.id, "url", target, "panic", r)
			s.emitDiagnostic(Diagnostic{
				Kind:      HandlerPanic,
				SessionID: s.id,
				Err:       err,
				Detail:    "held navigate:" + target,
			})
		}
	}()

	s.state = s.runHandle(s.state, Event{
		Type: event.Navigate,
		Data: map[string]string{"path": u.Path, "search": u.RawQuery},
	})
	s.stateSnap.Store(s.state)

	// Effects the navigate handler raised join the held batch. A
	// further Navigate from it is a redirect chain; lastURL follows it
	// so the client lands on the final URL.
	s.drainFx(fx)
	s.holdFx(fx)
}

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
	newState := s.runHandle(s.state, ev)

	s.drainFx(fx)

	// Resolve navigate redirects inline. When OnNavigate calls
	// Navigate(), re-process the redirect target server-side rather
	// than round-tripping to the client. Effects from intermediate
	// steps are preserved unless the redirect target overwrites them.
	if ev.Type == event.Navigate && fx.URL != "" {
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
				Type: event.Navigate,
				Data: map[string]string{"path": u.Path, "search": u.RawQuery},
			}
			newState = s.runHandle(newState, redirectEv)
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

	// The transport dropped between the client sending this event and
	// the loop reaching it. Commit the state - the event was still
	// valid - but leave the engine baseline alone so the reattach
	// catch-up can send what the client missed.
	if s.deferRender(fx) {
		s.state = newState
		s.stateSnap.Store(s.state)
		return
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
	// Nil patches with no structural change means the engine is
	// unseeded (stale client) - sendDiff answers with a full morph,
	// so it is not a "no patch" outcome and is excluded here.
	if patches != nil && len(patches) == 0 && change == nil {
		source := string(ev.Type)
		switch {
		case s.onNoPatch != nil:
			s.onNoPatch(s, NoPatch{Source: source, Action: ev.Action})
		case ev.Type == event.Navigate:
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
