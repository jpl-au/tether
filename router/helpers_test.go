package router

// Shared test types and helpers for router tests.

type state struct {
	Page  string
	Count int
}

func selector(s state) string { return s.Page }
