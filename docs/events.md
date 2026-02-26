# Events

## Event binding

Convenience helpers in the `bind` package wrap `SetData` so you don't need to remember the `poly-*` convention strings:

```go
bind.Click(button.Text("Save"), "save")       // data-poly-click="save"
bind.Submit(form.New(children...), "create")   // data-poly-submit="create"
bind.Input(input.Text("q", ""), "search")      // data-poly-input="search"
bind.Change(dropdown, "filter")                // data-poly-change="filter"
bind.KeyDown(input.Text("cmd", ""), "exec")    // data-poly-keydown="exec"
bind.Focus(el, "focus-name")                   // data-poly-focus="focus-name"
bind.Blur(el, "blur-name")                     // data-poly-blur="blur-name"
```

These return the same element type, so chaining continues:

```go
bind.Click(button.Text("+"), "increment").Style("cursor: pointer").Class("btn")
```

Keydown events include modifier keys (`ctrl`, `shift`, `alt`, `meta`) in `Event.Data` when held. Filter to a specific key with `bind.FilterKey`:

```go
bind.FilterKey(bind.KeyDown(input.Text("cmd", ""), "exec"), "Enter")
```

## Timing control

Input events are debounced at `DefaultDebounce` (default 300ms). Override per element:

```go
bind.Debounce(bind.Input(input.Text("q", ""), "search"), 150*time.Millisecond)
```

Throttle any event type:

```go
bind.Throttle(bind.Click(button.Text("Fire"), "fire"), time.Second)
```

## Loading states

Disable an element while its event is in flight to prevent double-clicks and give visual feedback:

```go
bind.Disable(bind.Click(button.Text("Save"), "save"), "Saving...")
```

The element is re-enabled when the next server update arrives. If the text argument is non-empty, the element's text content is temporarily replaced.

Point to a separate loading indicator element:

```go
bind.Indicator(bind.Click(button.Text("Save"), "save"), "#spinner")
```

The JS runtime adds the `poly-pending` class to the matched element while the event is in flight.

## Confirmation dialogs

Show `window.confirm` before sending an event:

```go
bind.Confirm(bind.Click(button.Text("Delete"), "delete"), "Are you sure?")
```

## Focus management

Direct focus to a specific element after a server update:

```go
bind.AutoFocus(input.Text("name", ""))
```

The JS runtime calls `focus()` on the first `[data-poly-autofocus]` element after applying patches and morphs.

Constrain Tab key navigation to within an element (useful for modals):

```go
bind.FocusTrap(div.New(modalContent...))
```

## Viewport events

Fire a server event when an element scrolls into view (useful for infinite scroll):

```go
bind.Viewport(div.New(), "load-more")
```

## Form validation

Validation is handled server-side in the `Handle` function. The key patterns:

- Wrap form + error in a `Dynamic` key so the server controls field values
- Use `bind.Preserve` to prevent JS form reset after submit
- Use `bind.Input` with a validation action for live feedback
- Keep error spans always in the tree (empty when no error) to avoid structural changes

```go
div.New(
    bind.Preserve(form.New(
        bind.Input(input.Text("text", s.TodoText), "validate-todo"),
        button.Submit("Add"),
    ).SetData("poly-submit", "add")),
    span.Text(s.TodoError).Style("color: #c33"),
).Dynamic("todo-form")
```
