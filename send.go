package tether

import (
	"fmt"
	"time"

	jit "github.com/jpl-au/fluent-jit"
	"github.com/jpl-au/fluent/node"
	"github.com/jpl-au/tether/dev"
	"github.com/jpl-au/tether/wire"
)

// sendDiff is the render-diff-send pipeline extracted from exec and
// Update. It handles patches, structural changes, and the no-diff
// case where only the eventID needs echoing. fx carries any buffered
// effects to merge into the update message.
func (s *StatefulSession[S]) sendDiff(eventID string, patches []jit.Patch, change *jit.StructuralChange, tree node.Node, fx *Effects) {
	if change != nil {
		html := s.engine.Render(tree)
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
		return
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

	// Unseeded engine (nil patches, nil change) means the client has
	// stale DOM from a previous server instance. Send a full morph so
	// the client's content is replaced with the current render. This
	// happens when a reconnecting client's session ID was not found
	// in any pool or store, so a fresh session was created without
	// seeding the engine.
	if patches == nil && change == nil {
		html := s.engine.Render(tree)
		dev.Debug("unseeded engine, sending full morph",
			"session", s.id,
			"endpoint", s.endpoint,
			"bytes", len(html),
		)
		u := wire.Update{
			Morphs: []wire.Morph{{Key: "", HTML: html}},
		}
		if fx != nil {
			fx.merge(&u)
		}
		s.send(u)
		return
	}

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
	if len(patches) == 0 && change == nil {
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
}
