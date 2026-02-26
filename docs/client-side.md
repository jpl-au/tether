# Client-side features

## Directives

Toggle CSS classes or attributes without a server round-trip:

```go
// Toggle a CSS class on the element itself
bind.ToggleClass(button.Text("Menu"), "is-open")

// Toggle a CSS class on a different element
bind.ToggleClass(bind.ToggleTarget(button.Text("Menu"), "#nav"), "is-open")

// Toggle visibility via the hidden attribute
bind.ToggleAttr(bind.ToggleTarget(button.Text("Show Help"), "#help"), "hidden")
```

Client-managed state survives server morphs automatically. The morph engine preserves client-toggled classes and attributes so they are not lost when the server re-renders.

Prevent the morph engine from touching an element entirely (useful for video players, iframes, and third-party widgets):

```go
bind.Permanent(div.New(children...))
```

Hide an element until the poly runtime initialises (prevents flash of unstyled content):

```go
bind.Cloak(div.New(children...))
```

## Transitions

CSS transitions coordinated with the morph lifecycle:

```go
bind.Transition(div.New(children...), "fade")
```

```css
.item { opacity: 1; transition: opacity 0.3s; }
.poly-fade-enter { opacity: 0; }
.poly-fade-leave { opacity: 0; }
```

Enter: `poly-{name}-enter` is added before insertion and removed next frame. Leave: `poly-{name}-leave` is added and the node waits for `transitionend` before removal (`TransitionTimeout` fallback, default 5s).

## JS hooks

Integrate third-party JavaScript libraries (charts, maps, rich text editors) via lifecycle hooks:

```go
bind.Hook(div.New(), "chart")
```

```js
Poly.hooks.chart = {
    mounted: function(el) { /* initialise chart library */ },
    updated: function(el) { /* refresh with new data */ },
    destroyed: function(el) { /* teardown */ }
};
```

The JS runtime calls `mounted` when the element is added to the DOM, `updated` when it is morphed in place, and `destroyed` when it is about to be removed.
