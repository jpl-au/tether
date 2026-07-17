module github.com/jpl-au/tether/tetheredis

go 1.25.0

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	// Pinned to v9.20.0. The fix for #3839 (RESP3 pub/sub message loss),
	// shipped in v9.20.1 and carried into v9.21.0, reworked
	// PeekPushNotificationName to grow its peek window with a blocking
	// r.rd.Peek(36). Subscribe confirmations for short channel names produce
	// a push frame shorter than 36 bytes, so under Receive()'s zero read
	// deadline that Peek blocks forever and deadlocks Subscribe. Verified:
	// TestPublishSubscribe hangs on v9.20.1 and v9.21.0. Every release from
	// v9.20.1 up is affected, so a patch bump does not help; lift only once
	// upstream bounds the peek to the frame length (or the caller's deadline).
	github.com/redis/go-redis/v9 v9.20.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/yuin/gopher-lua v1.1.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
)
