package bind

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// This file compiles infix expressions ("cart.qty * cart.price") into a
// compact postfix program that the browser runtime executes with a tiny
// stack VM. The compile happens once, in Go, at construction time; the
// client stays a fixed interpreter over a closed opcode set and never
// evals a string. That is what lets a computed signal ship under a
// strict script-src 'self' CSP.
//
// The wire value is "name|<json>", where <json> is an array of typed
// cells. Each cell is a two-element array [opcode, arg]:
//
//	[0, "signal"]  push the current value of a signal
//	[1, literal]   push a number or boolean literal
//	[2, "str"]     push a string literal
//	[3, "op"]      pop two, apply a binary operator, push the result
//	[4, "op"]      pop one, apply a unary operator, push the result
//
// Dependencies are derived on the client by scanning for opcode-0 cells,
// so there is no separate dependency list to keep in sync.
const (
	opSignal = 0
	opNumber = 1 // a number or boolean literal (the client pushes arg as-is)
	opString = 2
	opBinary = 3
	opUnary  = 4
)

// maxComputeDepth caps parenthesis nesting so a pathological expression
// cannot blow the parser stack. It is deliberately generous - real
// expressions nest a handful of levels at most.
const maxComputeDepth = 32

// binaryPrecedence maps every binary operator (and its alias) to a
// binding power. Higher binds tighter. Aliases are normalised to their
// canonical spelling before they reach the wire so the client VM only
// ever sees one form per operator.
var binaryPrecedence = map[string]int{
	"||": 1, "or": 1,
	"&&": 2, "and": 2,
	"==": 3, "!=": 3, ">": 3, ">=": 3, "<": 3, "<=": 3,
	"+": 4, "-": 4,
	"*": 5, "/": 5, "%": 5,
}

// canonicalBinaryOp folds the C-style aliases onto the word the VM
// understands. Everything else passes through unchanged.
func canonicalBinaryOp(op string) string {
	switch op {
	case "&&":
		return "and"
	case "||":
		return "or"
	default:
		return op
	}
}

// Computed declares a client-side computed signal: whenever any signal
// the expression reads changes, the browser re-evaluates expr and
// publishes the result under name, driving every binding on name (Text,
// Show, Class, ...) without a server round-trip. The server pushes the
// inputs; it never pushes the computed output.
//
//	// A live cart total from two server-pushed signals.
//	bind.Apply(span.New(),
//	    bind.Computed("cart.total", "cart.qty * cart.price"),
//	    bind.Text("cart.total"),
//	)
//
// The expression is compiled and validated here, at construction time.
// A malformed expression - unknown operator, unbalanced parenthesis,
// nesting past 32 levels, or an invalid identifier - panics with a
// positioned message, mirroring the fail-fast contract of [ShowWhen] and
// friends. See the package docs for the full operator set.
func Computed(name, expr string) Option {
	if !validSignalName(name) {
		panic("bind: Computed name " + strconv.Quote(name) + " is not a valid signal name")
	}
	return Option{"tether-computed", name + "|" + compileExpr(expr)}
}

// compileExpr parses expr and returns the JSON program. It panics on any
// error - callers are constructing UI, not handling user input, so a
// mistake should surface loudly at build time.
func compileExpr(expr string) string {
	c := &compiler{expr: expr, tokens: tokenise(expr)}
	if c.peek().kind == tokEOF {
		c.fail(0, "empty expression")
	}
	c.parseExpr(1)
	if tok := c.peek(); tok.kind != tokEOF {
		c.fail(tok.pos, "unexpected "+strconv.Quote(tok.text))
	}
	return marshalCells(c.cells)
}

// marshalCells serialises the program to JSON with HTML escaping off: the
// renderer already escapes the attribute value, so letting json escape
// <, >, and & as well only produces noisier \u00xx sequences on the wire.
func marshalCells(cells []any) string {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cells); err != nil {
		panic("bind: compute: " + err.Error())
	}
	// Encoder appends a trailing newline; trim it so the attribute is tidy.
	return strings.TrimRight(buf.String(), "\n")
}

// comparisonProgram builds the postfix program for a single signal-op-value
// comparison. ShowWhen, HideWhen, and ClassWhen all funnel through here so
// the conditional bindings compile to exactly the same programs the
// expression parser emits - the client has one evaluator, not two.
func comparisonProgram(signal, op string, value any) string {
	cells := []any{
		[]any{opSignal, signal},
		literalCell(value),
		[]any{opBinary, op},
	}
	return marshalCells(cells)
}

// literalCell turns a Go value from the ShowWhen/HideWhen/ClassWhen API
// into the right push cell: strings become string literals, numbers and
// booleans become number-slot literals (the client pushes them as-is).
func literalCell(value any) []any {
	switch v := value.(type) {
	case string:
		return []any{opString, v}
	case bool:
		return []any{opNumber, v}
	case int:
		return []any{opNumber, v}
	case int64:
		return []any{opNumber, v}
	case float64:
		return []any{opNumber, v}
	default:
		// Fall back to the string form so exotic types still compare.
		return []any{opString, fmt.Sprint(v)}
	}
}

// --- Tokeniser ---

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNumber
	tokString
	tokBool
	tokIdent
	tokOp
	tokLParen
	tokRParen
)

type token struct {
	kind tokenKind
	text string // operator text, identifier, decoded string value, or number text
	pos  int    // byte offset in the source expression, for error messages
}

// keywords are bare words with a fixed meaning: the boolean literals and
// the word-spelled operators. Everything else that lexes as a word is a
// signal name.
var keywordOps = map[string]bool{"and": true, "or": true, "not": true, "len": true}

func tokenise(expr string) []token {
	var toks []token
	i := 0
	for i < len(expr) {
		ch := expr[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			i++
		case ch == '(':
			toks = append(toks, token{tokLParen, "(", i})
			i++
		case ch == ')':
			toks = append(toks, token{tokRParen, ")", i})
			i++
		case ch == '\'' || ch == '"':
			tok, next := scanString(expr, i)
			toks = append(toks, tok)
			i = next
		case ch >= '0' && ch <= '9':
			tok, next := scanNumber(expr, i)
			toks = append(toks, tok)
			i = next
		case isIdentStart(ch):
			tok, next := scanWord(expr, i)
			toks = append(toks, tok)
			i = next
		default:
			tok, next := scanOp(expr, i)
			toks = append(toks, tok)
			i = next
		}
	}
	toks = append(toks, token{tokEOF, "", len(expr)})
	return toks
}

func scanString(expr string, start int) (token, int) {
	quote := expr[start]
	var b strings.Builder
	i := start + 1
	for i < len(expr) {
		ch := expr[i]
		if ch == '\\' && i+1 < len(expr) {
			b.WriteByte(expr[i+1])
			i += 2
			continue
		}
		if ch == quote {
			return token{tokString, b.String(), start}, i + 1
		}
		b.WriteByte(ch)
		i++
	}
	// Unterminated string: report at the opening quote.
	panicCompile(expr, start, "unterminated string literal")
	return token{}, 0 // unreachable
}

func scanNumber(expr string, start int) (token, int) {
	i := start
	seenDot := false
	for i < len(expr) {
		ch := expr[i]
		if ch >= '0' && ch <= '9' {
			i++
			continue
		}
		if ch == '.' && !seenDot {
			seenDot = true
			i++
			continue
		}
		break
	}
	return token{tokNumber, expr[start:i], start}, i
}

func scanWord(expr string, start int) (token, int) {
	i := start
	for i < len(expr) && isIdentPart(expr[i]) {
		i++
	}
	word := expr[start:i]
	switch {
	case word == "true" || word == "false":
		return token{tokBool, word, start}, i
	case keywordOps[word]:
		return token{tokOp, word, start}, i
	default:
		return token{tokIdent, word, start}, i
	}
}

func scanOp(expr string, start int) (token, int) {
	// Try the two-character operators first so ">=" does not lex as ">".
	if start+1 < len(expr) {
		two := expr[start : start+2]
		switch two {
		case ">=", "<=", "==", "!=", "&&", "||":
			return token{tokOp, two, start}, start + 2
		}
	}
	switch expr[start] {
	case '+', '-', '*', '/', '%', '>', '<', '!':
		return token{tokOp, expr[start : start+1], start}, start + 1
	}
	panicCompile(expr, start, "unexpected character "+strconv.Quote(string(expr[start])))
	return token{}, 0 // unreachable
}

func isIdentStart(ch byte) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isIdentPart(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9') || ch == '.'
}

// validSignalName accepts the same shape a signal name may take: a
// dot-separated path of identifier segments, each starting with a letter
// or underscore. Empty names and empty segments are rejected.
func validSignalName(name string) bool {
	if name == "" {
		return false
	}
	for seg := range strings.SplitSeq(name, ".") {
		if seg == "" || !isIdentStart(seg[0]) {
			return false
		}
		for i := 1; i < len(seg); i++ {
			if !isIdentPart(seg[i]) || seg[i] == '.' {
				return false
			}
		}
	}
	return true
}

// --- Pratt parser ---
//
// The parser emits postfix cells directly as it climbs precedence: each
// operand is emitted before its operator, so the output is already a
// ready-to-run program with no separate AST or codegen pass.

type compiler struct {
	expr   string
	tokens []token
	pos    int
	cells  []any
	depth  int
}

func (c *compiler) peek() token { return c.tokens[c.pos] }

func (c *compiler) next() token {
	t := c.tokens[c.pos]
	c.pos++
	return t
}

func (c *compiler) emit(op int, arg any) {
	c.cells = append(c.cells, []any{op, arg})
}

func (c *compiler) fail(pos int, msg string) {
	panicCompile(c.expr, pos, msg)
}

// parseExpr is precedence climbing: parse a unary operand, then fold in
// any binary operator whose precedence is at least minPrec, recursing at
// prec+1 so operators are left-associative.
func (c *compiler) parseExpr(minPrec int) {
	c.parseUnary()
	for {
		t := c.peek()
		if t.kind != tokOp {
			break
		}
		prec, ok := binaryPrecedence[t.text]
		if !ok || prec < minPrec {
			break
		}
		c.next()
		c.parseExpr(prec + 1)
		c.emit(opBinary, canonicalBinaryOp(t.text))
	}
}

// parseUnary handles the three prefix operators - negation, boolean not,
// and len - before falling through to a primary. len has no call syntax:
// it is a plain prefix operator, so "len items" and "len(items)" both
// work but "len(a, b)" cannot be written.
func (c *compiler) parseUnary() {
	t := c.peek()
	if t.kind == tokOp {
		switch t.text {
		case "-":
			c.next()
			c.parseUnary()
			c.emit(opUnary, "neg")
			return
		case "!", "not":
			c.next()
			c.parseUnary()
			c.emit(opUnary, "not")
			return
		case "len":
			c.next()
			c.parseUnary()
			c.emit(opUnary, "len")
			return
		}
	}
	c.parsePrimary()
}

func (c *compiler) parsePrimary() {
	t := c.peek()
	switch t.kind {
	case tokNumber:
		c.next()
		n, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			c.fail(t.pos, "invalid number "+strconv.Quote(t.text))
		}
		c.emit(opNumber, n)
	case tokString:
		c.next()
		c.emit(opString, t.text)
	case tokBool:
		c.next()
		c.emit(opNumber, t.text == "true")
	case tokIdent:
		c.next()
		if !validSignalName(t.text) {
			c.fail(t.pos, "invalid identifier "+strconv.Quote(t.text))
		}
		c.emit(opSignal, t.text)
	case tokLParen:
		c.depth++
		if c.depth > maxComputeDepth {
			c.fail(t.pos, "expression nested deeper than "+strconv.Itoa(maxComputeDepth)+" levels")
		}
		c.next()
		c.parseExpr(1)
		if got := c.peek(); got.kind != tokRParen {
			c.fail(got.pos, "expected ')'")
		}
		c.next()
		c.depth--
	case tokOp:
		c.fail(t.pos, "expected a value but found operator "+strconv.Quote(t.text))
	case tokRParen:
		c.fail(t.pos, "unbalanced ')'")
	default:
		c.fail(t.pos, "unexpected end of expression")
	}
}

// panicCompile raises a positioned compile error. The caret points at the
// offending byte so a developer reading the panic sees exactly where the
// expression went wrong.
func panicCompile(expr string, pos int, msg string) {
	panic("bind: compute: " + msg + " at position " + strconv.Itoa(pos) + " in " + strconv.Quote(expr))
}
