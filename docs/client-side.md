# Client-side features

## Directives

Toggle CSS classes or attributes without a server round-trip:

```go
// Toggle a CSS class on the element itself
bind.Apply(button.Text("Menu"), bind.ToggleClass("is-open"))

// Toggle a CSS class on a different element
bind.Apply(button.Text("Menu"), bind.ToggleTarget("#nav"), bind.ToggleClass("is-open"))

// Toggle visibility via the hidden attribute
bind.Apply(button.Text("Show Help"), bind.ToggleTarget("#help"), bind.ToggleAttr("hidden"))
```

Client-managed state survives server morphs automatically. The morph engine preserves client-toggled classes and attributes so they are not lost when the server re-renders.

Prevent the morph engine from touching an element entirely (useful for video players, iframes, and third-party widgets):

```go
bind.Apply(div.New(children...), bind.Permanent())
```

Hide an element until the tether runtime initialises (prevents flash of unstyled content):

```go
bind.Apply(div.New(children...), bind.Cloak())
```

## Scroll management

Preserve the scroll position of a container across morphs:

```go
bind.Apply(div.New(messages...).Class("chat-feed"), bind.PreserveScroll())
```

Without this, the morph engine may reset the scroll position when the container's content changes. Use this on columns, chat feeds, and any scrollable region where the user has scrolled to a specific position.

Auto-scroll a container to the bottom after each morph:

```go
bind.Apply(pre.New(text.Text(logOutput)).Class("log-viewer"), bind.AutoScroll())
```

Use this on log viewers, streaming output, and terminal-style displays where new content appears at the bottom. After every morph that updates the container's content, the client scrolls to the latest entry automatically.

Scroll a specific element into view on click (client-side, no server round-trip):

```go
bind.Apply(button.Text("Jump to bottom"), bind.ScrollTo("#latest"))
```

## Transitions

CSS transitions coordinated with the morph lifecycle:

```go
bind.Apply(div.New(children...), bind.Transition("fade"))
```

```css
.item { opacity: 1; transition: opacity 0.3s; }
.tether-fade-enter { opacity: 0; }
.tether-fade-leave { opacity: 0; }
```

Enter: `tether-{name}-enter` is added before insertion and removed next frame. Leave: `tether-{name}-leave` is added and the node waits for `transitionend` before removal (`TransitionTimeout` fallback, default 5s).

## JS hooks

Integrate third-party JavaScript libraries (charts, maps, rich text editors) via lifecycle hooks:

```go
bind.Apply(div.New(), bind.Hook("chart"))
```

```js
Tether.hooks.chart = {
    mounted: function(el) { /* initialise chart library */ },
    updated: function(el) { /* refresh with new data */ },
    destroyed: function(el) { /* teardown */ }
};
```

The JS runtime calls `mounted` when the element is added to the DOM, `updated` when it is morphed in place, and `destroyed` when it is about to be removed.

---

[← Back to documentation](../README.md#documentation)
