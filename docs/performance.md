# Performance

## Generic helpers vs SetData

The generic helpers are ~47% slower than calling `SetData` directly. For performance-sensitive render paths, use `SetData`:

```go
button.Text("+").SetData("tether-click", "increment")
```

In practice the difference is ~250ns per element - negligible unless you're rendering thousands of event-bound elements per frame.

## Profile-Guided Optimisation (PGO)

Applications using tether benefit from [Profile-Guided Optimisation](https://go.dev/doc/pgo) (Go 1.21+). Expect **10-20% speed improvements** with no code changes.

1. Collect a CPU profile under realistic load:
   ```bash
   curl -o default.pgo http://localhost:8080/debug/pprof/profile?seconds=30
   ```
2. Place `default.pgo` in your main package directory
3. `go build` - PGO is applied automatically

Both generic helpers and direct `SetData` paths benefit from PGO.

---

[← Back to documentation](../README.md#documentation)
