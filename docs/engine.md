# Engine

Tether's render pipeline converts state changes into targeted DOM
patches. The engine is the component that compares consecutive
renders and decides what to send to the client. Two engines are
available, plus a targeted update mechanism that works with either.

## The pipeline

Every state change flows through the same pipeline:

```
state change → render(state) → node tree → engine.Diff(tree) → patches → wire → client
```

The engine's job is step 4: compare the new tree against the
previous render and produce patches for Dynamic regions that
changed.

## Differ (default)

The Differ is the default engine. It renders every Dynamic region
on every cycle, compares the output against stored snapshots, and
produces patches for regions that changed. No configuration, no
developer effort, no correctness obligations.

```go
tether.Stateful(app, tether.StatefulConfig[State]{
    // No Memoise field - Differ is used automatically.
    Render: render,
    Handle: handle,
})
```

**Cost**: O(tree size) per cycle. Every Dynamic region is rendered
and compared, even if only one changed.

**When to use**: always start here. The Differ is correct by
construction. Move to Memoiser or Patch only when profiling shows
the render+diff cost is a bottleneck.

## Memoiser (opt-in)

The Memoiser is an alternative engine that skips unchanged subtrees.
Each Dynamic region wraps its content in `jit.Memoise` with a cache
key. When the key matches the previous render, the closure never
runs and no HTML is produced for that region.

```go
tether.Stateful(app, tether.StatefulConfig[State]{
    Memoise: true,
    Render: func(s State) node.Node {
        return div.New(
            div.New(
                jit.Memoise(s.Items.Version(), func() node.Node {
                    return renderTable(s.Items.Val)
                }),
            ).Dynamic("items"),
            span.Text(strconv.Itoa(s.Count)).Dynamic("count"),
        )
    },
})
```

**Cost**: O(changed subtrees) per cycle. Unchanged regions are
skipped at the cost of one key comparison each.

**When to use**: pages with expensive render functions (large
tables, complex charts) where most regions stay unchanged on most
events.

### Versioned helper

`tether.Versioned[T]` ties the cache key to data changes
automatically. Update data via `With`, read via `Val`, get the key
via `Version`:

```go
type State struct {
    Items tether.Versioned[[]Item]  // expensive, memoised
    Count int                        // cheap, plain Dynamic
}

// Handle:
s.Items = s.Items.With(append(s.Items.Val, newItem))

// Render:
jit.Memoise(s.Items.Version(), func() node.Node { ... })
```

The version increments on every `With` call. No manual bookkeeping.

## Patch (targeted updates)

`sess.Patch` bypasses the full render pipeline entirely. Instead of
rendering the whole tree and diffing every Dynamic key, Patch
re-renders a single key and diffs only that key against the stored
snapshot.

**Patch works with either engine.** It does not require `Memoise: true`.
Any handler with Dynamic keys can use it.

```go
sess.Patch("row-47", func(s State) (State, node.Node) {
    s.Items[47].Count++
    return s, renderRow(s.Items[47])
})
```

The closure returns both the new state and the rendered subtree for
the targeted key. The framework updates state, diffs the targeted
key, and sends the patch. Everything else is untouched.

**Cost**: O(targeted subtree) per call. For a page with 50 Dynamic
regions, Patch is over 1,000x faster than a full render.

**When to use**: timers, broadcast callbacks, and `Go` goroutines
where you know exactly which Dynamic region changed. Inside Handle,
return the new state directly and let the full render run.

**Consistency**: the targeted render must produce the same output
that the full render would for that key. If they diverge, the
client has a brief inconsistency until the next full render
corrects it. This is the explicit tradeoff for the performance gain.

## Coalescing

When multiple `Update` calls arrive in quick succession (broadcasts,
Value changes, watcher callbacks), the command loop drains all
pending mutations before rendering. Each mutation runs, but only one
render+diff cycle executes for the batch.

Coalescing is automatic. No configuration needed. It reduces
redundant renders under broadcast load from O(N) to O(1) per loop
iteration.

Coalescing applies to `Update` calls only. `Patch` calls send their
own targeted patches immediately - they do not trigger or participate
in the coalesced render.

Client events from the transport are not coalesced. Each event gets
its own render cycle because events carry client correlation IDs.

## Composing strategies

The strategies are complementary. Use them independently or together:

### Differ + Patch

The simplest combination. Start with the default Differ for all
events, add Patch for specific hot paths:

```go
// Handle processes client events (full render via Differ):
func handle(sess tether.Session, s State, ev tether.Event) State {
    s.Items[0].Count++
    return s
}

// Timer uses Patch (targeted, no full render):
sess.Go(func(ctx context.Context) {
    for range ticker.C {
        sess.Patch("status", func(s State) (State, node.Node) {
            s.LastPing = time.Now()
            return s, renderStatus(s)
        })
    }
})
```

### Memoiser + Patch

Maximum performance. Memoisation skips unchanged regions during full
renders (page load, reconnect, client events). Patch skips the full
render entirely for targeted server-push updates:

```go
tether.StatefulConfig[State]{
    Memoise: true,
    Render: render,  // uses jit.Memoise for expensive regions
}

// Timer updates one chart via Patch:
sess.Patch("chart-cpu", func(s State) (State, node.Node) {
    s.CPU = append(s.CPU, readCPU())
    return s, renderCPUChart(s)
})
```

On page load, Memoisation skips unchanged charts. On each tick, Patch
updates one chart at ~5-10µs instead of ~5-12ms for a full render.

## Developer journey

1. **Start with Differ.** Write a render function, add Dynamic keys.
   Everything works. No optimisation needed.

2. **Add Patch for hot paths.** When a timer, broadcast, or
   goroutine updates a known key, use `sess.Patch` instead of
   `sess.Update`. Immediate 1,000x improvement for that path.

3. **Switch to Memoiser for expensive pages.** When profiling shows
   the full render is slow, set `Memoise: true` and wrap expensive
   regions in `jit.Memoise` with Versioned keys.

4. **Combine them.** Memoisation handles full renders efficiently. Patch
   handles targeted updates efficiently. Both on the same handler.

Each step is independent. You do not need to use all of them. Most
pages only need step 1. Step 2 covers most performance-sensitive
cases. Steps 3 and 4 are for dashboards and data-heavy pages.

## Choosing an engine

| | Differ | Memoiser |
|---|---|---|
| Config | Default (no config) | `Memoise: true` |
| Developer effort | Zero | Must provide cache keys |
| Full render cost | O(tree size) | O(changed subtrees) |
| Correctness | Automatic | Developer maintains key accuracy |
| Patch support | Yes | Yes |

---

[← Back to documentation](../README.md#documentation)
