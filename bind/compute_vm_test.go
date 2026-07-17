package bind

import (
	"encoding/json"
	"math"
	"testing"
)

// This file proves the compiler↔VM contract end-to-end without a browser:
// it decodes the JSON program the compiler emits and runs it through a Go
// port of the client's stack VM (client/tether.js runProgram/binaryOp/
// unaryOp). If the wire format or the evaluation semantics drift, these
// assertions fail. Keep the three helpers below in lock-step with the JS.

// vmTruthy mirrors isTruthy in tether.js: null/undefined/false/0/"" are
// falsy, everything else truthy.
func vmTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	default:
		return true
	}
}

// vmNumber mirrors JS Number(): booleans coerce to 1/0, numeric strings
// parse, non-numeric strings become NaN.
func vmNumber(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	case nil:
		return 0
	case string:
		var f float64
		if err := json.Unmarshal([]byte(x), &f); err == nil {
			return f
		}
		return math.NaN()
	default:
		return math.NaN()
	}
}

func vmString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		b, _ := json.Marshal(x)
		return string(b)
	default:
		return ""
	}
}

// looseEqual approximates JS == closely enough for the operands these
// programs produce (number/string/bool). Numbers and numeric strings
// compare by value; a bool compares to its numeric coercion.
func looseEqual(a, b any) bool {
	if _, ok := a.(string); ok {
		if _, ok := b.(string); ok {
			return a == b
		}
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return vmNumber(a) == vmNumber(b)
}

func vmBinary(op string, a, b any) any {
	switch op {
	case "+":
		if _, sa := a.(string); sa {
			return vmString(a) + vmString(b)
		}
		if _, sb := b.(string); sb {
			return vmString(a) + vmString(b)
		}
		return vmNumber(a) + vmNumber(b)
	case "-":
		return vmNumber(a) - vmNumber(b)
	case "*":
		return vmNumber(a) * vmNumber(b)
	case "/":
		return vmNumber(a) / vmNumber(b)
	case "%":
		return math.Mod(vmNumber(a), vmNumber(b))
	case "==":
		return looseEqual(a, b)
	case "!=":
		return !looseEqual(a, b)
	case "and":
		return vmTruthy(a) && vmTruthy(b)
	case "or":
		return vmTruthy(a) || vmTruthy(b)
	}
	x, y := vmNumber(a), vmNumber(b)
	if math.IsNaN(x) || math.IsNaN(y) {
		return false
	}
	switch op {
	case ">":
		return x > y
	case ">=":
		return x >= y
	case "<":
		return x < y
	default: // "<="
		return x <= y
	}
}

func vmUnary(op string, v any) any {
	switch op {
	case "not":
		return !vmTruthy(v)
	case "neg":
		return -vmNumber(v)
	default: // "len"
		if s, ok := v.(string); ok {
			return float64(len(s))
		}
		if a, ok := v.([]any); ok {
			return float64(len(a))
		}
		return float64(0)
	}
}

// runVM decodes a compiled program and evaluates it against signals,
// mirroring client/tether.js runProgram exactly.
func runVM(t *testing.T, program string, signals map[string]any) any {
	t.Helper()
	var cells [][]any
	if err := json.Unmarshal([]byte(program), &cells); err != nil {
		t.Fatalf("program is not valid JSON: %v\n%s", err, program)
	}
	var stack []any
	push := func(v any) { stack = append(stack, v) }
	pop := func() any {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return v
	}
	for _, cell := range cells {
		op := int(cell[0].(float64))
		arg := cell[1]
		switch op {
		case 0:
			push(signals[arg.(string)])
		case 1, 2:
			push(arg)
		case 3:
			b := pop()
			a := pop()
			push(vmBinary(arg.(string), a, b))
		default:
			push(vmUnary(arg.(string), pop()))
		}
	}
	return pop()
}

func TestVMEvaluatesCompiledPrograms(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		signals map[string]any
		want    any
	}{
		{"cart total", "cart.qty * cart.price", map[string]any{"cart.qty": 3.0, "cart.price": 10.0}, 30.0},
		{"char counter", "280 - len(draft)", map[string]any{"draft": "hello"}, 275.0},
		{"n selected concat", "selected + ' selected'", map[string]any{"selected": 4.0}, "4 selected"},
		{"enable submit", "len(email) > 3 and agreed", map[string]any{"email": "a@b.co", "agreed": true}, true},
		{"enable submit short email", "len(email) > 3 and agreed", map[string]any{"email": "ab", "agreed": true}, false},
		{"precedence", "a + b * c", map[string]any{"a": 2.0, "b": 3.0, "c": 4.0}, 14.0},
		{"parens override", "(a + b) * c", map[string]any{"a": 2.0, "b": 3.0, "c": 4.0}, 20.0},
		{"string equals", `status == "done"`, map[string]any{"status": "done"}, true},
		{"string not equals", `status != "done"`, map[string]any{"status": "busy"}, true},
		{"boolean or with not", "not a or b", map[string]any{"a": false, "b": false}, true},
		{"unary minus", "-x + 5", map[string]any{"x": 2.0}, 3.0},
		{"modulo", "n % 3", map[string]any{"n": 7.0}, 1.0},
		{"len of array", "len items", map[string]any{"items": []any{1.0, 2.0, 3.0}}, 3.0},
		{"comparison of subexprs", "a * 2 >= b + 1", map[string]any{"a": 3.0, "b": 4.0}, true},
		{"true literal", "flag == true", map[string]any{"flag": true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runVM(t, compileExpr(tt.expr), tt.signals)
			if got != tt.want {
				t.Errorf("%q with %v = %v (%T), want %v (%T)", tt.expr, tt.signals, got, got, tt.want, tt.want)
			}
		})
	}
}

// TestVMRunsComparisonPrograms confirms ShowWhen/HideWhen/ClassWhen's
// compiled comparison programs evaluate as the conditional bindings expect.
func TestVMRunsComparisonPrograms(t *testing.T) {
	if got := runVM(t, comparisonProgram("count", ">", 5), map[string]any{"count": 6.0}); got != true {
		t.Errorf("count>5 at 6 = %v, want true", got)
	}
	if got := runVM(t, comparisonProgram("count", ">", 5), map[string]any{"count": 3.0}); got != false {
		t.Errorf("count>5 at 3 = %v, want false", got)
	}
	if got := runVM(t, comparisonProgram("status", "==", "done"), map[string]any{"status": "done"}); got != true {
		t.Errorf("status==done = %v, want true", got)
	}
}
