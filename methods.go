package poly

import (
	"errors"
	"maps"
	"time"

	"github.com/jpl-au/fluent-poly/push"
)

// State returns the current session state. When called from inside
// Handle it returns directly (no channel hop). When called from
// outside, it performs a synchronous read through the command channel
// so the value reflects any prior queued updates.
func (s *Session[S]) State() S {
	if s.handling {
		return s.state
	}
	ch := make(chan S, 1)
	select {
	case s.cmds <- func() { ch <- s.state }:
		return <-ch
	case <-s.ctx.Done():
		// Session destroyed — return last known state rather than
		// blocking forever on a channel nobody will drain.
		return s.state
	}
}

// Update applies a state change and pushes the resulting diff to the
// client. This is the primary way to push server-initiated updates —
// call it from timers, database change listeners, message queue
// consumers, or [Group.Broadcast].
//
// When called inside Handle, the state change is applied directly and
// will be rendered as part of the current exec cycle.
//
// When called from outside, a full render-diff-send cycle is queued
// as a command. Non-blocking — returns immediately after queuing.
//
// Safe to call from any goroutine, including from within Handle.
func (s *Session[S]) Update(fn func(S) S) {
	if s.handling {
		s.state = fn(s.state)
		return
	}
	s.enqueue(func() {
		s.handling = true
		s.fx = &effects{}
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("panic in Update", "panic", r)
				s.emitted = s.emitted[:0]
			}
			s.handling = false
			s.fx = nil
		}()

		s.lastActivity.Store(time.Now().UnixNano())
		if s.idleTimer != nil {
			s.idleTimer.Reset(s.idleTimeout)
		}
		s.state = fn(s.state)

		tree := s.render(s.state)
		patches, change := s.differ.Diff(tree)
		s.sendDiff("", patches, change, tree)
		s.flushEmissions()
	})
}

// Close terminates the session by closing its transport. The reader
// goroutine exits, which closes the events channel, which the loop
// handles via onTransportClose. Safe to call from any goroutine;
// safe to call more than once.
func (s *Session[S]) Close() {
	s.enqueue(func() {
		if s.transport != nil {
			s.transport.Close()
		}
	})
}

// Toast sends a global notification to the client. Inside Handle the
// toast is buffered and sent atomically with the state diff. Outside
// Handle it is sent immediately.
func (s *Session[S]) Toast(text string) {
	if s.handling {
		s.fx.toast = text
		return
	}
	s.enqueue(func() {
		s.send(update{Toast: text})
	})
}

// Navigate pushes a URL change to the client (history.pushState).
// Inside Handle the URL is buffered; outside it is sent immediately.
func (s *Session[S]) Navigate(rawURL string) {
	if s.handling {
		s.fx.url = rawURL
		s.fx.replace = false
		return
	}
	s.enqueue(func() {
		s.send(update{URL: rawURL})
	})
}

// ReplaceURL updates the browser URL without a history entry
// (history.replaceState). Inside Handle the URL is buffered; outside
// it is sent immediately.
func (s *Session[S]) ReplaceURL(rawURL string) {
	if s.handling {
		s.fx.url = rawURL
		s.fx.replace = true
		return
	}
	s.enqueue(func() {
		s.send(update{URL: rawURL, Replace: true})
	})
}

// SetTitle updates the browser's document title. Inside Handle the
// title is buffered; outside it is sent immediately.
func (s *Session[S]) SetTitle(title string) {
	if s.handling {
		s.fx.title = title
		return
	}
	s.enqueue(func() {
		s.send(update{Title: title})
	})
}

// Announce sends text to a screen-reader-accessible live region on
// the client. Inside Handle the text is buffered; outside it is sent
// immediately.
func (s *Session[S]) Announce(text string) {
	if s.handling {
		s.fx.announce = text
		return
	}
	s.enqueue(func() {
		s.send(update{Announce: text})
	})
}

// Flash sends a one-time notification to the client. The selector is
// a CSS selector for the target element; the text is displayed for 5
// seconds. Inside Handle the flash is buffered; outside it is sent
// immediately.
func (s *Session[S]) Flash(selector, text string) {
	if s.handling {
		if s.fx.flash == nil {
			s.fx.flash = make(map[string]string)
		}
		s.fx.flash[selector] = text
		return
	}
	s.enqueue(func() {
		s.send(update{Flash: map[string]string{selector: text}})
	})
}

// Signal pushes a reactive value to the client. Elements bound to the
// signal name via [BindText], [BindShow], [BindClass], or [BindAttr]
// update instantly — no render cycle, no diff, no HTML. Inside Handle
// the signal is buffered and sent atomically with the state diff.
// Outside Handle it is sent immediately.
//
// Signals are ideal for high-frequency updates (counters, status
// indicators, progress bars) where the full render/diff pipeline
// is unnecessary overhead.
//
//	s.Signal("count", 42)
//	s.Signal("status", "online")
func (s *Session[S]) Signal(key string, value any) {
	if s.handling {
		if s.fx.signals == nil {
			s.fx.signals = make(map[string]any)
		}
		s.fx.signals[key] = value
		return
	}
	s.enqueue(func() {
		s.send(update{Signals: map[string]any{key: value}})
	})
}

// Signals pushes multiple reactive values to the client in a single
// update. This is a batch variant of [Signal] — use it when setting
// several signals at once to avoid sending one message per key.
// Inside Handle all keys are merged into the buffered effects.
// Outside Handle a single update is sent with all keys.
//
//	s.Signals(map[string]any{"count": 42, "status": "online"})
func (s *Session[S]) Signals(signals map[string]any) {
	if s.handling {
		if s.fx.signals == nil {
			s.fx.signals = make(map[string]any, len(signals))
		}
		maps.Copy(s.fx.signals, signals)
		return
	}
	s.enqueue(func() {
		s.send(update{Signals: signals})
	})
}

// Push sends a Web Push notification to the browser. Only works when
// the session has an active push subscription and a [push.Sender] is
// configured. Returns an error if either is missing. Safe to call
// from any goroutine.
func (s *Session[S]) Push(n push.Notification) error {
	// Push subscription is only written from within the loop, so
	// reading it here is safe when called from within Handle.
	// When called from outside, we read through the command channel.
	if s.handling {
		return s.sendPush(n)
	}

	ch := make(chan error, 1)
	select {
	case s.cmds <- func() { ch <- s.sendPush(n) }:
		return <-ch
	case <-s.ctx.Done():
		return errors.New("poly: session closed")
	}
}

func (s *Session[S]) sendPush(n push.Notification) error {
	if s.pushSender == nil {
		return errors.New("poly: push not configured")
	}
	if s.pushSub == nil {
		return errors.New("poly: no push subscription for session")
	}
	return s.pushSender.Send(*s.pushSub, n)
}
