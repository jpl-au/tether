package bind

// Signal bindings connect elements to server-pushed reactive values.
// When the server calls Session.Signal, the client updates all bound
// elements in the document instantly — no render, no diff, no HTML.
// Bindings work document-wide, not just inside the tether root. This
// means elements in the [Config.Layout] shell (navigation highlights,
// status indicators, body classes) react to signal updates alongside
// elements in the morphed content area. Bindings inside the tether root
// survive morphs: the client reapplies current signal values after
// idiomorph reconciliation.

// BindText binds an element's text content to a named signal. When
// the server pushes a new value, the client sets textContent directly.
//
//	bind.BindText(span.New(), "count")
func BindText[E Settable[E]](el E, signal string) E {
	return el.SetData("tether-bind-text", signal)
}

// BindShow binds an element's visibility to a named signal. The
// element is shown (display restored) when the value is truthy and
// hidden (display:none) when falsy.
//
//	bind.BindShow(div.New(children...), "isOpen")
func BindShow[E Settable[E]](el E, signal string) E {
	return el.SetData("tether-bind-show", signal)
}

// BindHide is the inverse of [BindShow]. The element is hidden when
// the value is truthy and shown when falsy.
func BindHide[E Settable[E]](el E, signal string) E {
	return el.SetData("tether-bind-hide", signal)
}

// BindClass binds a CSS class to a named signal. The class is added
// when the signal value is truthy and removed when falsy. The data
// attribute stores "className signalName" as a space-separated pair.
//
//	bind.BindClass(span.New(), "active", "isSelected")
func BindClass[E Settable[E]](el E, class, signal string) E {
	return el.SetData("tether-bind-class", class+" "+signal)
}

// BindAttr binds an HTML attribute to a named signal. When the signal
// value is falsy (null, false, undefined) the attribute is removed;
// otherwise it is set to the string representation of the value.
//
//	bind.BindAttr(button.New(), "disabled", "isLoading")
func BindAttr[E Settable[E]](el E, attr, signal string) E {
	return el.SetData("tether-bind-attr", attr+" "+signal)
}

// BindValue binds a form element's value to a named signal. When the
// server pushes a new value, the client sets the element's value
// property directly — not the HTML attribute, the DOM property. This
// is the correct approach for input, select, and textarea elements
// where the user's interaction diverges the property from the attribute.
//
//	bind.BindValue(input.Text("email", ""), "email")
func BindValue[E Settable[E]](el E, signal string) E {
	return el.SetData("tether-bind-value", signal)
}
