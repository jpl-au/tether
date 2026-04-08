# tetheredis

Redis Pub/Sub backend for [Tether](https://github.com/jpl-au/tether)'s
Cluster interface. Enables cross-node communication for Bus and Value
primitives.

## Install

```bash
go get github.com/jpl-au/tether/tetheredis
```

## Usage

```go
import (
    "github.com/redis/go-redis/v9"
    "github.com/jpl-au/tether"
    "github.com/jpl-au/tether/tetheredis"
)

func main() {
    rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
    defer rdb.Close()

    app := tether.App{
        Cluster: tetheredis.New(rdb),
    }

    // Buses and Values with topic names now publish/subscribe
    // across all nodes connected to the same Redis.
}
```

## How it works

- `Publish` delegates to Redis `PUBLISH`
- `Subscribe` creates a Redis Pub/Sub subscription, confirms it
  with `Receive` before returning (so the caller can publish
  immediately without a race), and runs a goroutine to read
  messages from the channel
- The returned unsubscribe function closes the Redis subscription

The caller owns the `*redis.Client` lifetime. Create it before
the Cluster, close it after the tether application shuts down.

## Testing

Tests use [miniredis](https://github.com/alicebob/miniredis) - a
pure Go in-process Redis server. No external Redis required.

```bash
go test ./...
```

## Licence

MIT
