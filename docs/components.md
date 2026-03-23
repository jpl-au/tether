# Components

`tether.Component` is a self-contained rendering unit with its own state. Components know how to render themselves and handle their own events, without any knowledge of the parent's state type:

```go
type Counter struct {
    Count int
}

func (c Counter) Render() node.Node {
    return div.New(
        span.Textf("Count: %d", c.Count).Dynamic("count"),
        bind.Apply(button.Text("+1"), bind.OnClick("increment")),
        bind.Apply(button.Text("Reset"), bind.OnClick("reset")),
    )
}

func (c Counter) Handle(sess tether.Session, ev tether.Event) tether.Component {
    switch ev.Action {
    case "increment":
        c.Count++
    case "reset":
        c.Count = 0
        sess.Toast("Counter reset")
    }
    return c
}
```

Components are value types - `Handle` returns a new value, the receiver is never mutated. Side effects (`sess.Toast`, `sess.Signal`, etc.) work inside components just like they do in the page handler.

## Declarative mounting with StatefulConfig.Components

For components that are fully self-contained, mount them declaratively on StatefulConfig. The framework intercepts events matching the mount's prefix and dispatches them automatically - the page's `Handle` never sees these events:

```go
tether.StatefulConfig[State]{
    Components: []tether.ComponentMount[State]{
        tether.Mount("likes",
            func(s State) Counter { return s.Likes },
            func(s State, c Counter) State { s.Likes = c; return s },
        ),
    },
}
```

In Render, call the component's `Render` method:

```go
Render: func(s State) node.Node {
    return div.New(
        p.Text("Likes:"),
        div.New(s.Likes.Render()).Dynamic("likes-section"),
    )
},
```

## Manual routing with RouteTyped

When you need to coordinate component events with other state changes, or when using `tether.Stateless` (which does not support `StatefulConfig.Components`), route events manually in Handle:

```go
Handle: func(sess tether.Session, s State, ev tether.Event) State {
    s.Counter = tether.RouteTyped(s.Counter, "counter", sess, ev)
    return s
},
```

Events with actions like `"counter.increment"` are forwarded to the component with the prefix stripped - the component sees `"increment"`. Events without a matching prefix pass through unchanged.

## Initial setup with Mounter

Components that need one-time setup (firing a toast, pushing a signal, starting background work) can implement the optional `Mounter` interface:

```go
func (d Dashboard) Mount(sess tether.Session) tether.Component {
    sess.Toast("Dashboard ready")
    return d
}
```

The framework calls `Mount` once per component during session startup for components registered via `StatefulConfig.Components`. Components that don't need setup simply omit the method.

---

[← Back to documentation](../README.md#documentation)
