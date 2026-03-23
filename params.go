package tether

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/jpl-au/tether/dev"
)

// Params carries URL information from a navigation event. The handler
// passes this to [StatefulConfig].OnNavigate on the initial page load (so the
// application can derive state from the URL) and whenever the browser
// navigates via a tether link click or the back/forward buttons.
//
// Params lives in its own file (separate from [Event]) because it
// represents a different data source - URL navigation context rather
// than DOM interaction - even though both types share a similar
// extraction API. Keeping them apart makes the conceptual boundary
// clear: Event is wire protocol from the client JS, Params is parsed
// from the browser's address bar.
//
// The extraction helpers are organised into three tiers:
//
// Single-value with error - [Params.Get], [Params.Int], [Params.Bool],
// [Params.Float64]. These mirror [Event]'s API so developers learn one
// extraction pattern for the whole framework. Use these when a missing
// or malformed value is a hard error (e.g. a required resource ID).
//
// Soft getters with default - [Params.IntDefault], [Params.BoolDefault],
// [Params.Float64Default]. These exist because URL parameters are
// fundamentally different from event data: event data is developer-
// controlled wire protocol where a missing field signals a bug, but
// URL parameters are user-supplied and routinely absent. Soft getters
// eliminate the if-err-else boilerplate that would otherwise dominate
// every OnNavigate handler.
//
// Multi-value - [Params.Strings], [Params.Ints], [Params.Float64s].
// These exist because [url.Values] is map[string][]string - query keys
// can repeat (e.g. ?tag=go&tag=web). Without these, developers would
// have to bypass the helper API and access p.Query directly, defeating
// the purpose of the abstraction.
type Params struct {
	// Path is the URL path component (e.g. "/settings"). The router
	// uses this to determine which page to render. Always present.
	Path string

	// Query holds the parsed URL query parameters. It is nil when the
	// URL has no query string. All extraction methods are nil-safe  -
	// calling Get, IntDefault, etc. on a nil Query returns zero values or
	// defaults without panicking, because url.Values is a map type and
	// nil map reads return zero values in Go.
	Query url.Values
}

// paramsFromEvent builds Params from a navigate event's data map.
// The client JS sends "path" and "search" keys; search is the query
// string without the leading "?". The prefix is trimmed defensively
// in case a future client change includes it.
func paramsFromEvent(ev Event) Params {
	p := Params{Path: ev.Data["path"]}
	if search := ev.Data["search"]; search != "" {
		var err error
		p.Query, err = url.ParseQuery(strings.TrimPrefix(search, "?"))
		if err != nil {
			dev.Log().Warn("malformed query string in navigate event", "search", search, "err", err)
		}
	}
	return p
}

// --- Single-value helpers ---
//
// These mirror Event's API (Get, Int, Float64, Bool) so developers
// learn one extraction pattern for the entire framework. Use these
// when a missing or malformed value is a hard error - e.g. a required
// resource ID in a URL like /items?id=42.

// Get returns the first query value for key. If the key is not present,
// it returns an empty string. url.Values.Get always returns the first
// value when a key appears multiple times; for all values use
// [Params.Strings].
func (p Params) Get(key string) string {
	return p.Query.Get(key)
}

// Int returns the first query value for key parsed as an integer. If
// the key is missing or the value is not a valid integer, it returns 0
// and an error. Most navigation handlers should prefer [Params.IntDefault]
// because URL parameters are typically optional - Int is here for the
// rare case where absence genuinely means something is wrong.
func (p Params) Int(key string) (int, error) {
	return strconv.Atoi(p.Query.Get(key))
}

// Float64 returns the first query value for key parsed as a float. If
// the key is missing or the value is not a valid number, it returns 0
// and an error. For optional parameters prefer [Params.Float64Default].
func (p Params) Float64(key string) (float64, error) {
	return strconv.ParseFloat(p.Query.Get(key), 64)
}

// Bool returns true when the first query value for key is the string
// "true". All other values - including "false", "0", empty, and
// missing keys - return false. This matches [Event.Bool]'s semantics
// so the same truthiness rules apply everywhere in the framework.
func (p Params) Bool(key string) bool {
	return p.Query.Get(key) == "true"
}

// --- Soft getters ---
//
// URL parameters are user-supplied and routinely absent - a user
// visiting /items without ?page= is not an error, they just want
// page 1. Soft getters return a caller-supplied default when the key
// is missing or the value is malformed, eliminating the if-err-else
// boilerplate that would otherwise dominate every OnNavigate handler.
// Event does not have these because event data is developer-controlled
// wire protocol where a missing field signals a bug.

// IntDefault returns the first query value for key parsed as an integer. If
// the key is missing or the value is not a valid integer, it returns
// the provided default instead of an error. This is the idiomatic way
// to read optional numeric URL parameters:
//
//	s.PageNum = p.IntDefault("page", 1)
//	s.Limit   = p.IntDefault("limit", 20)
func (p Params) IntDefault(key string, def int) int {
	n, err := strconv.Atoi(p.Query.Get(key))
	if err != nil {
		return def
	}
	return n
}

// Float64Default returns the first query value for key parsed as a float. If
// the key is missing or the value is not a valid number, it returns the
// provided default instead of an error.
//
//	s.MinPrice = p.Float64Default("min", 0.0)
func (p Params) Float64Default(key string, def float64) float64 {
	n, err := strconv.ParseFloat(p.Query.Get(key), 64)
	if err != nil {
		return def
	}
	return n
}

// BoolDefault returns the first query value for key parsed as a boolean. If
// the key is missing (empty string from url.Values.Get), it returns
// the provided default - this distinguishes "user didn't specify" from
// "user explicitly set to false". When the key is present, any value
// other than the literal string "true" evaluates to false, matching
// [Params.Bool] and [Event.Bool] semantics.
//
//	s.ShowDrafts = p.BoolDefault("drafts", false)
func (p Params) BoolDefault(key string, def bool) bool {
	v := p.Query.Get(key)
	// Empty means the key was absent - return the caller's default so
	// they can distinguish "not specified" from "explicitly false".
	if v == "" {
		return def
	}
	return v == "true"
}

// --- Multi-value helpers ---
//
// url.Values is map[string][]string because query keys can repeat
// (e.g. ?tag=go&tag=web). The single-value helpers above silently
// collapse lists to the first element via url.Values.Get. These
// methods expose the full slice so developers don't have to bypass
// the helper API and access p.Query directly when they need all values.

// Strings returns all query values for key as a string slice. If the
// key is not present, it returns nil. Use this for query parameters
// that may appear more than once (e.g. ?tag=go&tag=web).
func (p Params) Strings(key string) []string {
	return p.Query[key]
}

// Ints returns all query values for key parsed as integers. If any
// value cannot be parsed, it returns the values successfully parsed
// before the error and the error itself - callers can choose to use
// the partial result or treat it as a failure. If the key is not
// present, it returns nil and no error.
func (p Params) Ints(key string) ([]int, error) {
	raw := p.Query[key]
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]int, len(raw))
	for i, s := range raw {
		n, err := strconv.Atoi(s)
		if err != nil {
			return out[:i], err
		}
		out[i] = n
	}
	return out, nil
}

// Float64s returns all query values for key parsed as floats. If any
// value cannot be parsed, it returns the values successfully parsed
// before the error and the error itself. If the key is not present,
// it returns nil and no error.
func (p Params) Float64s(key string) ([]float64, error) {
	raw := p.Query[key]
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]float64, len(raw))
	for i, s := range raw {
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return out[:i], err
		}
		out[i] = n
	}
	return out, nil
}
