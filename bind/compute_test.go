package bind

import (
	"strings"
	"testing"
)

// The compiler is unexported, so these tests live in package bind (not
// bind_test) to reach compileExpr and comparisonProgram directly and
// assert on the raw postfix program rather than the escaped HTML.

func TestCompileLiteralsAndSignals(t *testing.T) {
	tests := []struct {
		expr string
		want string
	}{
		{"count", `[[0,"count"]]`},
		{"cart.total", `[[0,"cart.total"]]`},
		{"42", `[[1,42]]`},
		{"3.5", `[[1,3.5]]`},
		{"true", `[[1,true]]`},
		{"false", `[[1,false]]`},
		{`"hi"`, `[[2,"hi"]]`},
		{`'hi'`, `[[2,"hi"]]`},
	}
	for _, tt := range tests {
		if got := compileExpr(tt.expr); got != tt.want {
			t.Errorf("compileExpr(%q) = %s, want %s", tt.expr, got, tt.want)
		}
	}
}

func TestCompilePrecedence(t *testing.T) {
	tests := []struct {
		expr string
		want string
	}{
		// * binds tighter than +: a + b * c => a, (b c *), +
		{"a + b * c", `[[0,"a"],[0,"b"],[0,"c"],[3,"*"],[3,"+"]]`},
		// Parens override precedence: (a + b) * c
		{"(a + b) * c", `[[0,"a"],[0,"b"],[3,"+"],[0,"c"],[3,"*"]]`},
		// Comparison binds looser than arithmetic: a + b > c
		{"a + b > c", `[[0,"a"],[0,"b"],[3,"+"],[0,"c"],[3,">"]]`},
		// and binds looser than comparison; or looser than and.
		{"a > 1 and b < 2 or c", `[[0,"a"],[1,1],[3,">"],[0,"b"],[1,2],[3,"<"],[3,"and"],[0,"c"],[3,"or"]]`},
		// Left-associative subtraction: a - b - c => ((a-b)-c)
		{"a - b - c", `[[0,"a"],[0,"b"],[3,"-"],[0,"c"],[3,"-"]]`},
	}
	for _, tt := range tests {
		if got := compileExpr(tt.expr); got != tt.want {
			t.Errorf("compileExpr(%q) =\n  %s\nwant\n  %s", tt.expr, got, tt.want)
		}
	}
}

func TestCompileUnaryOperators(t *testing.T) {
	tests := []struct {
		expr string
		want string
	}{
		{"-a", `[[0,"a"],[4,"neg"]]`},
		{"not a", `[[0,"a"],[4,"not"]]`},
		{"!a", `[[0,"a"],[4,"not"]]`},
		{"len items", `[[0,"items"],[4,"len"]]`},
		{"len(name)", `[[0,"name"],[4,"len"]]`},
		{"-a + b", `[[0,"a"],[4,"neg"],[0,"b"],[3,"+"]]`},
	}
	for _, tt := range tests {
		if got := compileExpr(tt.expr); got != tt.want {
			t.Errorf("compileExpr(%q) = %s, want %s", tt.expr, got, tt.want)
		}
	}
}

func TestCompileAliases(t *testing.T) {
	// && || ! normalise to and / or / not so the client VM sees one form.
	if got := compileExpr("a && b"); got != `[[0,"a"],[0,"b"],[3,"and"]]` {
		t.Errorf("&& alias: got %s", got)
	}
	if got := compileExpr("a || b"); got != `[[0,"a"],[0,"b"],[3,"or"]]` {
		t.Errorf("|| alias: got %s", got)
	}
}

func TestCompileStringConcatAndEscaping(t *testing.T) {
	// A quote and backslash inside a literal must round-trip through JSON.
	got := compileExpr(`'a"b\\c' + name`)
	want := `[[2,"a\"b\\c"],[0,"name"],[3,"+"]]`
	if got != want {
		t.Errorf("string escaping: got %s, want %s", got, want)
	}
}

func TestCompilePanics(t *testing.T) {
	tests := []struct {
		name string
		expr string
		frag string // substring the panic message must contain
	}{
		{"unknown operator", "a ^ b", "unexpected character"},
		{"unbalanced open", "(a + b", "expected ')'"},
		{"unbalanced close", "a)", "unexpected"},
		{"bad identifier", "a..b", "invalid identifier"},
		{"empty", "", "empty expression"},
		{"operator without operand", "a +", "unexpected end"},
		{"leading operator", "* a", "expected a value"},
		{"no call grammar", "foo(a, b)", "unexpected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic for %q", tt.expr)
				}
				if msg, ok := r.(string); !ok || !strings.Contains(msg, tt.frag) {
					t.Fatalf("panic %v does not contain %q", r, tt.frag)
				}
			}()
			compileExpr(tt.expr)
		})
	}
}

func TestCompileDepthCap(t *testing.T) {
	// 33 nested parens exceeds the depth cap of 32.
	deep := strings.Repeat("(", 33) + "a" + strings.Repeat(")", 33)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on excessive nesting")
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, "nested deeper") {
			t.Fatalf("panic %v is not a depth error", r)
		}
	}()
	compileExpr(deep)
}

func TestCompilePanicIsPositioned(t *testing.T) {
	defer func() {
		r := recover()
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "position") {
			t.Fatalf("panic %v is not positioned", r)
		}
	}()
	compileExpr("a + ^")
}

// TestShowWhenCompilesToProgram proves the conditional-binding sugar
// compiles to exactly the postfix program the expression parser would
// emit for the equivalent infix comparison - there is one evaluator, not
// two. comparisonProgram is what ShowWhen/HideWhen/ClassWhen emit.
func TestShowWhenCompilesToProgram(t *testing.T) {
	cases := []struct {
		signal string
		op     string
		value  any
		expr   string
	}{
		{"count", ">", 5, "count > 5"},
		{"seconds", "<", 10, "seconds < 10"},
		{"status", "==", "done", `status == "done"`},
	}
	for _, tc := range cases {
		fromSugar := comparisonProgram(tc.signal, tc.op, tc.value)
		fromExpr := compileExpr(tc.expr)
		if fromSugar != fromExpr {
			t.Errorf("comparisonProgram(%q,%q,%v) = %s, but compileExpr(%q) = %s",
				tc.signal, tc.op, tc.value, fromSugar, tc.expr, fromExpr)
		}
	}
}

func TestComputedRendersWireFormat(t *testing.T) {
	opt := Computed("cart.total", "cart.qty * cart.price")
	if opt.key != "tether-computed" {
		t.Fatalf("key = %q, want tether-computed", opt.key)
	}
	want := `cart.total|[[0,"cart.qty"],[0,"cart.price"],[3,"*"]]`
	if opt.value != want {
		t.Errorf("value = %s, want %s", opt.value, want)
	}
}

func TestComputedInvalidNamePanics(t *testing.T) {
	for _, name := range []string{"", "1bad", "has space", "trailing.", ".leading"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("expected panic for name %q", name)
				}
			}()
			Computed(name, "a + b")
		}()
	}
}
