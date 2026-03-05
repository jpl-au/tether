package tether

// Watcher subscribes a session to a reactive source when it connects.
// Create watchers with [WatchValue] and [WatchBus].
//
// Watchers listed in [Config.Watchers] are subscribed automatically
// before [Config.OnConnect] runs, so the session receives updates from
// the moment it connects. The subscriptions are cleaned up when the
// session is destroyed.
type Watcher[S any] interface {
	subscribe(sess *LiveSession[S])
}

// WatchValue creates a [Watcher] that observes a [Value]. The current
// value is delivered immediately on connect; future changes are mapped
// into the session state via the mapper function.
//
//	tether.WatchValue(onlineCount, func(n int, s State) State {
//	    s.OnlineCount = n
//	    return s
//	})
func WatchValue[S any, V any](val *Value[V], mapper func(V, S) S) Watcher[S] {
	return &valueWatcher[S, V]{val: val, mapper: mapper}
}

// WatchBus creates a [Watcher] that subscribes a session to a [Bus].
// Published events from other sessions are folded into the subscriber's
// state via the mapper function. Sender filtering is automatic — events
// emitted by this session via [Bus.Emit] are skipped.
//
//	tether.WatchBus(messages, func(msg Message, s State) State {
//	    s.Messages = append(s.Messages, msg)
//	    return s
//	})
func WatchBus[S any, E any](bus *Bus[E], mapper func(E, S) S) Watcher[S] {
	return &busWatcher[S, E]{bus: bus, mapper: mapper}
}

type valueWatcher[S any, V any] struct {
	val    *Value[V]
	mapper func(V, S) S
}

func (w *valueWatcher[S, V]) subscribe(sess *LiveSession[S]) {
	Observe(sess, w.val, w.mapper)
}

type busWatcher[S any, E any] struct {
	bus    *Bus[E]
	mapper func(E, S) S
}

func (w *busWatcher[S, E]) subscribe(sess *LiveSession[S]) {
	On(sess, w.bus, w.mapper)
}
