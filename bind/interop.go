package bind

// Hook annotates an element with a named JS hook. The developer
// registers callbacks on the global Tether.hooks object in JavaScript:
//
//	Tether.hooks.chart = {
//	    mounted: function(el) { /* init */ },
//	    updated: function(el) { /* refresh */ },
//	    destroyed: function(el) { /* teardown */ }
//	};
//
// The JS runtime calls mounted when the element is added to the DOM,
// updated when it is morphed in place, and destroyed when it is about
// to be removed.
func Hook[E Settable[E]](el E, name string) E {
	return el.SetData("tether-hook", name)
}

// Transition annotates an element with a CSS transition name. When
// the element is added to the DOM during a morph, the JS runtime
// applies tether-{name}-enter and removes it next frame. When removed,
// it applies tether-{name}-leave and waits for transitionend before
// removing the node.
func Transition[E Settable[E]](el E, name string) E {
	return el.SetData("tether-transition", name)
}
