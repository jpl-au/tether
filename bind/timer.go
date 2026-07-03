package bind

import (
	"strconv"
	"time"
)

// Timer attaches a client-side timer to the element. The timer ticks
// entirely in the browser - no server goroutine, no WebSocket messages
// per tick. The server controls the timer by pushing signals:
//
//   - sess.Signal("name.running", true)  starts the timer
//   - sess.Signal("name.running", false) pauses it
//   - sess.Signal("name", 0)             resets the value
//
// The element's text content is automatically bound to the formatted
// timer value - no separate [Text] call is needed.
//
// By default the timer counts up from zero at one-second precision
// with an auto-detected display format (ss, mm:ss, or hh:mm:ss).
// Combine with [Countdown], [TimerPrecision], [TimerFormat], and
// [TimerOnComplete] to configure behaviour:
//
//	bind.Apply(el,
//	    bind.Timer("quiz"),
//	    bind.Countdown(30*time.Second),
//	    bind.TimerOnComplete("quiz.expired"),
//	)
func Timer(name string) Option { return Option{"tether-timer", name} }

// Countdown configures a timer to count down from the given duration
// instead of counting up from zero. When the timer reaches zero it
// stops automatically. Combine with [TimerOnComplete] to fire a
// server event when the countdown finishes.
func Countdown(d time.Duration) Option {
	if d <= 0 {
		panic("bind: Countdown duration must be positive")
	}
	return Option{"tether-timer-countdown", strconv.FormatFloat(d.Seconds(), 'f', -1, 64)}
}

// TimerPrecision sets the tick interval for the timer. The default is
// one second. Use shorter intervals for stopwatch-style displays:
//
//	bind.TimerPrecision(100 * time.Millisecond) // tenths of a second
func TimerPrecision(d time.Duration) Option {
	if d <= 0 {
		panic("bind: TimerPrecision duration must be positive")
	}
	return Option{"tether-timer-precision", strconv.Itoa(int(d.Milliseconds()))}
}

// TimerFormat sets an explicit display format for the timer value.
// The default is "auto", which picks the shortest readable
// representation based on the current value (ss under a minute,
// mm:ss under an hour, hh:mm:ss beyond that).
//
// Supported format tokens:
//
//   - hh:mm:ss    hours, minutes, seconds
//   - mm:ss       minutes, seconds
//   - ss          seconds only
//   - mm:ss.S     minutes, seconds, tenths
//   - mm:ss.SS    minutes, seconds, hundredths
func TimerFormat(pattern string) Option {
	return Option{"tether-timer-format", pattern}
}

// TimerOnComplete sets the event action fired back to the server when
// a countdown timer reaches zero. The event is sent as a standard
// tether event and appears in Handle with the given action name. Has
// no effect on count-up timers.
func TimerOnComplete(action string) Option {
	return Option{"tether-timer-complete", action}
}
