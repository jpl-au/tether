module github.com/jpl-au/tether/tetheredis

go 1.25.0

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	// Pinned to v9.20.0: v9.21.0's PeekPushNotificationName does a blocking
	// Peek(36) that deadlocks Subscribe on short channel names (go-redis #3839).
	// Lift the pin once upstream ships a fix.
	github.com/redis/go-redis/v9 v9.20.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/yuin/gopher-lua v1.1.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
)
