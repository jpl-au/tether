package bind

// Signal bindings connect elements to server-pushed reactive values.
// When the server calls Session.Signal, the client updates all bound
// elements instantly — no render, no diff, no HTML. Bindings survive
// morphs: the client reapplies current signal values after idiomorph
// reconciliation.

// BindText binds an element's text content to a named signal. When
// the server pushes a new value, the client sets textContent directly.
//
//	bind.BindText(span.New(), "count")
func BindText[E Settable[E]](el E, signal string) E {
	return el.SetData("poly-bind-text", signal)
}

// BindShow binds an element's visibility to a named signal. The
// element is shown (display restored) when the value is truthy and
// hidden (display:none) when falsy.
//
//	bind.BindShow(div.New(children...), "isOpen")
func BindShow[E Settable[E]](el E, signal string) E {
	return el.SetData("poly-bind-show", signal)
}

// BindHide is the inverse of [BindShow]. The element is hidden when
// the value is truthy and shown when falsy.
func BindHide[E Settable[E]](el E, signal string) E {
	return el.SetData("poly-bind-hide", signal)
}

// BindClass binds a CSS class to a named signal. The class is added
// when the signal value is truthy and removed when falsy. The data
// attribute stores "className signalName" as a space-separated pair.
//
//	bind.BindClass(span.New(), "active", "isSelected")
func BindClass[E Settable[E]](el E, class, signal string) E {
	return el.SetData("poly-bind-class", class+" "+signal)
}

// BindAttr binds an HTML attribute to a named signal. When the signal
// value is falsy (null, false, undefined) the attribute is removed;
// otherwise it is set to the string representation of the value.
//
//	bind.BindAttr(button.New(), "disabled", "isLoading")
func BindAttr[E Settable[E]](el E, attr, signal string) E {
	return el.SetData("poly-bind-attr", attr+" "+signal)
}
