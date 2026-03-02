package bind

// Disable marks an element for automatic disabling while an event is
// in flight. The JS runtime sets the disabled attribute when the event
// is sent and clears it when the next server update arrives. If text
// is non-empty, the element's text content is temporarily replaced
// (e.g. "Saving..." while a form submits).
func Disable[E Settable[E]](el E, text string) E {
	return el.SetData("tether-disable", text)
}

// Confirm attaches a confirmation prompt to an event-bound element.
// The JS runtime shows window.confirm with the given message before
// sending the event. If the user cancels, the event is dropped.
func Confirm[E Settable[E]](el E, message string) E {
	return el.SetData("tether-confirm", message)
}

// Reset tells the JS runtime to reset a form's fields after submit.
// Without this, the server controls field values via the re-render.
// Use this for chat inputs, search bars, or any form where the fields
// should clear after submission.
func Reset[E Settable[E]](el E) E {
	return el.SetData("tether-reset", "")
}

// AutoFocus marks an element to receive focus after the next server
// update. The JS runtime calls focus() on the first element with
// data-tether-autofocus after applying patches and morphs. Only one
// element should have this attribute at a time.
func AutoFocus[E Settable[E]](el E) E {
	return el.SetData("tether-autofocus", "")
}

// Indicator points to an element that shows loading state while an
// event is in flight. The value is a CSS selector. The JS runtime
// adds tether-pending to the matched element when the event is sent
// and removes it when the server responds.
//
//	bind.Indicator(bind.Click(button.Text("Save"), "save"), "#spinner")
func Indicator[E Settable[E]](el E, selector string) E {
	return el.SetData("tether-indicator", selector)
}

// FocusTrap constrains Tab key navigation to the element's
// descendants. Useful for modals and dropdown menus.
func FocusTrap[E Settable[E]](el E) E {
	return el.SetData("tether-focus-trap", "")
}
