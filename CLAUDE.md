## Design Philosophy

**Developer Experience is the north star.** Every decision - API shape, naming,
defaults, error messages - must be evaluated through the lens of "how easy is
this for the developer?" Think Rails: convention over configuration, sensible
defaults, minimal boilerplate. If a developer has to read the source to
understand how to use something, we've failed.

**No backwards compatibility concerns.** We only move forward. We will break
APIs freely to arrive at the best possible design. Never preserve old
behaviour, add shims, or keep deprecated paths "just in case."

# Claude Code Instructions

- Do not add `Co-Authored-By` lines to commit messages
- Do not commit CLAUDE.md
- Do not commit `*-audit.md` files
- Do not push to remote unless explicitly asked
- Use British spelling in code comments and commit messages

## Code Principles

All code in this repository should follow these principles:

- Is easy to read from top to bottom
- Does not assume that you already know what it is doing
- Does not assume that you can memorise all of the preceding code
- Does not have unnecessary levels of abstraction
- Does not have names that call attention to something mundane
- Makes the propagation of values and decisions clear to the reader
- Has comments that explain why, not what, the code is doing to avoid future deviation
- Has documentation that stands on its own
- Has useful errors and useful test failures
- May often be mutually exclusive with "clever" code

## Build

- Use `go build ./...` to check compilation - never `go build .` which writes a binary
- When editing Go files, do not worry about precise whitespace or indentation - `gofmt` will normalise formatting automatically
