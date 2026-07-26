package tether

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/wire"
)

// memoiseNoopWarnOnce guards the one-time warning for a Memoise-enabled
// handler whose render carries no jit.Memoise regions.
var memoiseNoopWarnOnce sync.Once

// maxHeldKeys caps the distinct Flash and Signals keys held across one
// disconnect window, counted over both maps together. Effects merge by
// key, so a page with a fixed set of signals never approaches it however
// long the client stays away or however hard the application
// broadcasts. Only a handler minting a fresh key per event does, and
// that would otherwise grow unchecked for the whole reconnect window.
const maxHeldKeys = 256

// deferRender reports whether the render-diff-send pipeline must be
// skipped because the client's transport is gone, holding fx for the
// reattach catch-up when it is.
//
// Diffing costs nothing to skip and everything to get wrong here. The
// engine's stored snapshots describe the DOM the browser is currently
// showing, and Diff advances them to the tree it was handed. With no
// transport the patches are discarded, so diffing anyway would move the
// baseline past bytes the browser never received - and the reattach
// catch-up in [Handler.reattach] diffs against exactly that baseline,
// so it would then find nothing to send and the client would stay stale
// for the rest of the session. Leaving the baseline where the client's
// DOM actually is makes the catch-up produce precisely the patches the
// client missed.
//
// Deciding and holding are one call on purpose. Split apart, a caller
// that skipped the render but forgot to hold would drop the cycle's
// effects with nothing to show for it - the silent loss this whole
// mechanism exists to end. Pass nil when the cycle has no effects
// (Patch renders a subtree and raises none).
//
// Runs on the loop goroutine, which owns both transport and heldFx.
func (s *StatefulSession[S]) deferRender(fx *Effects) bool {
	if s.transport != nil {
		return false
	}
	dev.Debug("render deferred, client disconnected",
		"session", s.id,
		"endpoint", s.endpoint,
	)
	if fx != nil {
		s.holdFx(fx)
	}
	return true
}

// holdFx keeps the effects raised while the client is away so the
// reattach catch-up can deliver them, and drops the ones that would
// arrive as a surprise.
//
// The cut is between what describes the page and what commands the
// browser. A toast, a flash, a signal, an announcement and the document
// title all still describe the page when the client returns, so they
// are held and merged by the ordinary [Effects] coalescing rules - the
// reconnect window behaves like any other coalesced batch. A scroll, a
// download and a prefetch hint name a moment that has passed: firing
// them on a user who has just come back is worse than not firing them
// at all, and the download in particular is a synthesised click on a
// URL that is often signed and by then expired. Those are dropped and
// named in a [CommandDiscarded] diagnostic.
//
// The URL and title are also mirrored onto lastURL and lastTitle, which
// [Handler.Shutdown] persists to the [SessionStore]. Without that a
// session that navigated while away would be restored on the page it
// left, not the one it was sent to.
func (s *StatefulSession[S]) holdFx(fx *Effects) {
	if !fx.Any() {
		return
	}

	var dropped []string
	if fx.ScrollTo != "" {
		fx.ScrollTo = ""
		dropped = append(dropped, "ScrollTo")
	}
	if fx.Download != "" {
		fx.Download = ""
		dropped = append(dropped, "Download")
	}
	if fx.Prefetch != nil {
		fx.Prefetch = nil
		dropped = append(dropped, "Prefetch")
	}
	if len(dropped) > 0 {
		s.emitDiagnostic(Diagnostic{
			Kind:      CommandDiscarded,
			SessionID: s.id,
			Detail:    "disconnected: " + strings.Join(dropped, ", "),
		})
	}

	// Mirror the navigation state the session store reads on shutdown.
	// send() does this for delivered updates; a held one must too.
	if fx.URL != "" {
		s.lastURL = fx.URL
	}
	if fx.Title != "" {
		s.lastTitle = fx.Title
	}

	if !fx.Any() {
		return
	}
	if s.heldFx == nil {
		s.heldFx = &Effects{}
	}
	if refused := s.heldFx.mergeBounded(fx, maxHeldKeys); refused > 0 {
		s.emitDiagnostic(Diagnostic{
			Kind:      CommandDiscarded,
			SessionID: s.id,
			Detail:    fmt.Sprintf("disconnected: %d new Flash/Signals keys over the %d held-key limit", refused, maxHeldKeys),
		})
	}
}

// sendDiff is the render-diff-send pipeline extracted from exec and
// Update. It handles patches, structural changes, the unseeded-engine
// case, and the no-diff case where only the eventID needs echoing.
// fx carries any buffered effects to merge into the update message.
func (s *StatefulSession[S]) sendDiff(eventID string, patches []jit.Patch, change *jit.StructuralChange, tree node.Node, fx *Effects) {
	// Unseeded engine (nil patches, nil change) means the client has
	// stale DOM from a previous server instance. Send a full morph so
	// the client's content is replaced with the current render. This
	// happens when a reconnecting client's session ID was not found
	// in any pool or store, so a fresh session was created without
	// seeding the engine. Handled here so the event path (exec) and
	// the update path (coalescedRender) recover identically.
	if patches == nil && change == nil {
		html := s.engine.RenderBytes(tree)
		dev.Debug("unseeded engine, sending full morph",
			"session", s.id,
			"endpoint", s.endpoint,
			"bytes", len(html),
		)
		u := wire.Update{
			Morphs:  []wire.Morph{{Key: "", HTML: html}},
			EventID: eventID,
		}
		if fx != nil {
			fx.merge(&u)
		}
		s.send(u)
		return
	}

	if change != nil {
		html := s.engine.RenderBytes(tree)
		sc := StructuralChange{
			Added:     change.Added,
			Removed:   change.Removed,
			Reordered: change.Reordered,
			Bytes:     len(html),
		}
		if s.onStructuralChange != nil {
			s.onStructuralChange(s, sc)
		} else {
			dev.Debug("structural change, sending root morph",
				"session", s.id,
				"endpoint", s.endpoint,
				"change", change.String(),
				"bytes", len(html),
			)
		}

		u := wire.Update{
			Morphs:  []wire.Morph{{Key: "", HTML: html}},
			EventID: eventID,
		}
		if fx != nil {
			fx.merge(&u)
		}
		s.send(u)
		return
	}

	if len(patches) > 0 || (fx != nil && fx.Any()) {
		wp := make([]wire.Patch, len(patches))
		for i, p := range patches {
			wp[i] = wire.Patch{Key: p.Key, HTML: p.HTML}
		}
		u := wire.Update{Patches: wp, EventID: eventID}
		if fx != nil {
			fx.merge(&u)
		}
		s.send(u)
		return
	}

	// No patches and no structural change. Still echo the eventID
	// so the client can restore any loading state.
	if eventID != "" {
		s.send(wire.Update{EventID: eventID})
	}
}

// send encodes an update and writes the bytes to the transport. URL
// and title are captured before the nil-transport guard so reattach
// can replay them after a disconnect.
func (s *StatefulSession[S]) send(u wire.Update) {
	if s.pendingSession != "" {
		u.Session = s.pendingSession
		s.pendingSession = ""
	}
	if u.URL != "" {
		s.lastURL = u.URL
	}
	if u.Title != "" {
		s.lastTitle = u.Title
	}
	if s.transport == nil {
		// Standalone effects raised outside a render cycle - a Toast
		// from a timer, a Signal from a watcher. The render paths hold
		// their effects via deferRender before reaching here, so this
		// is the effect-only route into the same buffer.
		s.holdFx(fxFrom(u))
		return
	}
	data, err := s.encoder.Encode(u)
	if err != nil {
		s.emitDiagnostic(Diagnostic{
			Kind:      EncodeError,
			SessionID: s.id,
			Err:       err,
			Detail:    s.endpoint,
		})
		// Signals carry arbitrary developer values (the usual encode
		// failure: a chan, func, or NaN slipped into a Signal). One
		// bad signal must not cost the client its patches - retry
		// without the signals and only drop the update if it still
		// will not encode.
		if u.Signals == nil {
			return
		}
		// One unencodable value takes every signal in the batch with
		// it, and a batch can be a whole reconnect window's worth
		// rather than one cycle. Name the loss: EncodeError alone says
		// something failed to encode, not that the client is now
		// missing state it will never be sent again.
		s.emitDiagnostic(Diagnostic{
			Kind:      CommandDiscarded,
			SessionID: s.id,
			Detail:    fmt.Sprintf("%d signal(s) dropped - the batch could not be encoded", len(u.Signals)),
		})
		u.Signals = nil
		data, err = s.encoder.Encode(u)
		if err != nil {
			return
		}
	}
	// Binary wire formats (CBOR) travel as native binary frames when
	// the transport supports them (WebSocket). Text-only transports
	// (SSE) get base64 instead, where raw bytes would corrupt the
	// event stream framing - at a +33% size cost.
	if s.wireFormat == wire.CBOR {
		if bs, ok := s.transport.(BinarySender); ok {
			if err := bs.SendBinary(data); err != nil {
				s.emitDiagnostic(Diagnostic{
					Kind:      TransportError,
					SessionID: s.id,
					Err:       err,
					Detail:    s.endpoint,
				})
			}
			return
		}
		data = []byte(base64.StdEncoding.EncodeToString(data))
	}
	if err := s.transport.Send(data); err != nil {
		s.emitDiagnostic(Diagnostic{
			Kind:      TransportError,
			SessionID: s.id,
			Err:       err,
			Detail:    s.endpoint,
		})
	}
}

// coalescedRender runs the render-diff-send pipeline once after one
// or more Update mutations have been drained from the command channel.
// Effects enqueued during the mutations are collected from fxCh and
// sent atomically with the diff.
func (s *StatefulSession[S]) coalescedRender() {
	fx := &Effects{}
	defer func() {
		if r := recover(); r != nil {
			err := panicErr(r)
			dev.Log().Error("panic in coalesced render", "session", s.id, "panic", r)
			s.emitDiagnostic(Diagnostic{
				Kind:      HandlerPanic,
				SessionID: s.id,
				Err:       err,
				Detail:    s.endpoint,
			})
			s.drainFx(nil)
			if s.onPanic != nil {
				s.onPanic(s, err)
			} else {
				s.stop()
			}
		}
	}()

	s.drainFx(fx)

	if s.deferRender(fx) {
		return
	}

	if s.coalescedCount > 1 {
		s.emitDiagnostic(Diagnostic{
			Kind:      RenderCoalesced,
			SessionID: s.id,
			Detail:    fmt.Sprintf("%d", s.coalescedCount),
		})
	}

	renderStart := time.Now()
	tree := s.render(s.state)
	patches, change := s.engine.Diff(tree)
	renderDuration := time.Since(renderStart)

	dev.Debug("render complete",
		"session", s.id,
		"patches", len(patches),
		"structural", change != nil,
		"duration", renderDuration,
		"coalesced", true,
	)
	if s.slowRender > 0 && renderDuration > s.slowRender {
		s.emitDiagnostic(Diagnostic{
			Kind:      SlowRender,
			SessionID: s.id,
			Detail:    renderDuration.String(),
		})
	}
	s.checkMemoiseStats()
	// Nil patches (unseeded engine) is answered with a full morph by
	// sendDiff, so only a seeded-but-unchanged diff counts as no-patch.
	if patches != nil && len(patches) == 0 && change == nil {
		if s.onNoPatch != nil {
			s.onNoPatch(s, NoPatch{Source: "update"})
		} else {
			dev.Debug("Update produced no patches",
				"session", s.id,
				"endpoint", s.endpoint,
				"url", s.lastURL,
			)
		}
	}
	s.sendDiff("", patches, change, tree, fx)
}

// checkMemoiseStats reads hit/miss counters from the Memoiser after a
// Diff call. In dev mode, per-node hit/miss detail is logged. In all
// modes, a HighMemoiseMissRate diagnostic is emitted when the miss ratio
// exceeds the configured threshold. Only applies when the engine is a
// Memoiser. Called on the loop goroutine after every Diff.
func (s *StatefulSession[S]) checkMemoiseStats() {
	ms, ok := s.engine.(*jit.Memoiser)
	if !ok {
		return
	}
	hits, misses := ms.Stats()
	total := hits + misses
	if total == 0 {
		return
	}
	dev.Debug("memoiser stats",
		"session", s.id,
		"hits", hits,
		"misses", misses,
	)

	// Memoise is enabled but the render carried no jit.Memoise regions,
	// so the Memoiser has nothing to skip and behaves like the plain
	// differ - the flag is a silent no-op. Warn once per process so the
	// misconfiguration surfaces without spamming every render.
	if ms.Memoised() == 0 {
		memoiseNoopWarnOnce.Do(func() {
			dev.Log().Warn("tether: Memoise is enabled but no jit.Memoise regions were found in the render - the flag has no effect; wrap the expensive Dynamic regions in jit.Memoise or disable Memoise")
		})
	}
	if s.memoiseMissThreshold > 0 {
		ratio := float64(misses) / float64(total)
		if ratio > s.memoiseMissThreshold {
			s.emitDiagnostic(Diagnostic{
				Kind:      HighMemoiseMissRate,
				SessionID: s.id,
				Detail:    fmt.Sprintf("%.0f%% miss rate (%d/%d)", ratio*100, misses, total),
			})
		}
	}

	// Shared-fragment cache activity, if this render used any
	// jit.Shared regions. A hit reused another session's bytes; a
	// miss rendered fresh for the whole process.
	if sharedHits, sharedMisses := ms.SharedStats(); sharedHits+sharedMisses > 0 {
		dev.Debug("shared cache stats",
			"session", s.id,
			"hits", sharedHits,
			"misses", sharedMisses,
		)
		s.emitDiagnostic(Diagnostic{
			Kind:      SharedCacheReuse,
			SessionID: s.id,
			Detail:    fmt.Sprintf("%d hits, %d misses", sharedHits, sharedMisses),
		})
	}
}
