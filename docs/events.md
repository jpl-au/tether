# Events

## Event binding

Convenience helpers wrap `SetData` so you don't need to remember the `poly-*` convention strings:

```go
poly.Click(button.Text("Save"), "save")       // data-poly-click="save"
poly.Submit(form.New(children...), "create")   // data-poly-submit="create"
poly.Input(input.Text("q", ""), "search")      // data-poly-input="search"
poly.Change(dropdown, "filter")                // data-poly-change="filter"
poly.KeyDown(input.Text("cmd", ""), "exec")    // data-poly-keydown="exec"
poly.Focus(el, "focus-name")                   // data-poly-focus="focus-name"
poly.Blur(el, "blur-name")                     // data-poly-blur="blur-name"
```

These return the same element type, so chaining continues:

```go
poly.Click(button.Text("+"), "increment").Style("cursor: pointer").Class("btn")
```

Keydown events include modifier keys (`ctrl`, `shift`, `alt`, `meta`) in `Event.Data` when held.

## Timing control

Input events are debounced at `DefaultDebounce` (default 300ms). Override with `poly.Debounce`:

```go
poly.Debounce(poly.Input(input.Text("q", ""), "search"), 150)
```

Throttle any event type with `poly.Throttle`:

```go
poly.Throttle(poly.Click(button.Text("Fire"), "fire"), 1000)
```

## Loading states

Disable an element while its event is in flight to prevent double-clicks and give visual feedback:

```go
poly.Disable(poly.Click(button.Text("Save"), "save"), "Saving...")
```

The element is re-enabled when the next server update arrives. If the text argument is non-empty, the element's text content is temporarily replaced.

## Confirmation dialogs

Show `window.confirm` before sending an event:

```go
poly.Confirm(poly.Click(button.Text("Delete"), "delete"), "Are you sure?")
```

## Focus management

Direct focus to a specific element after a server update:

```go
poly.AutoFocus(input.Text("name", ""))
```

The JS runtime calls `focus()` on the first `[data-poly-autofocus]` element after applying patches and morphs.

## Form validation

Validation is handled server-side in the `Handle` function. The key patterns:

- Wrap form + error in a `Dynamic` key so the server controls field values
- Use `poly.Preserve` to prevent JS form reset after submit
- Use `poly.Input` with a validation action for live feedback
- Keep error spans always in the tree (empty when no error) to avoid structural changes

```go
div.New(
    poly.Preserve(form.New(
        poly.Input(input.Text("text", s.TodoText), "validate-todo"),
        button.Submit("Add"),
    ).SetData("poly-submit", "add")),
    span.Text(s.TodoError).Style("color: #c33"),
).Dynamic("todo-form")
```
