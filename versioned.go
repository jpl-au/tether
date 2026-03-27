package tether

// Versioned wraps a value with an automatic version counter for use
// with [node.Memo]. The version increments on every call to [With],
// ensuring the memo key changes when the data changes.
//
// Use Versioned for state fields that back memoised Dynamic regions.
// The version is the memo key - when it matches the previous render,
// the Memoiser skips the subtree entirely.
//
//	type State struct {
//	    Items tether.Versioned[[]Item]
//	    Count int
//	}
//
// Read the data directly via Val:
//
//	renderTable(s.Items.Val)
//
// Update the data via With (version increments automatically):
//
//	s.Items = s.Items.With(append(s.Items.Val, newItem))
//
// Use the version as the memo key in Render:
//
//	node.Memo(s.Items.Version(), func() node.Node {
//	    return renderTable(s.Items.Val)
//	})
//
// Versioned is a value type. It works naturally with tether's
// state model where Handle receives S by value and returns a new S.
// The zero value is valid - version starts at 0.
type Versioned[T any] struct {
	// Val is the wrapped data. Read it directly in render functions
	// and Handle. To update, use [With] which returns a new
	// Versioned with an incremented version.
	Val T

	version int
}

// NewVersioned creates a Versioned wrapping the given initial data.
// The version starts at 1 to distinguish initialised values from
// zero values.
func NewVersioned[T any](data T) Versioned[T] {
	return Versioned[T]{Val: data, version: 1}
}

// With returns a new Versioned with the given data and an
// incremented version. This is the only way to update the data
// and have the version track the change.
//
//	s.Items = s.Items.With(append(s.Items.Val, newItem))
func (v Versioned[T]) With(data T) Versioned[T] {
	return Versioned[T]{Val: data, version: v.version + 1}
}

// Version returns the current version counter. Use this as the
// key for [node.Memo]:
//
//	node.Memo(s.Items.Version(), func() node.Node { ... })
func (v Versioned[T]) Version() int {
	return v.version
}
