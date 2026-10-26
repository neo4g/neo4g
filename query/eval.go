package query

import (
	"fmt"
	"strings"

	"github.com/neo4g/neo4g/graph"
	"github.com/neo4g/neo4g/parser"
)

// evalExpr evaluates an expression AST node against a row of variable
// bindings and a parameter map. Returns a Go primitive (int64, float64,
// string, bool, nil) or a *graph.Node / *graph.Edge for ident lookups.
func evalExpr(e parser.Expr, row Row, params map[string]interface{}) (interface{}, error) {
	switch ex := e.(type) {
	case *parser.LiteralExpr:
		return ex.Value, nil

	case *parser.IdentExpr:
		v, ok := row[ex.Name]
		if !ok {
			return nil, fmt.Errorf("unbound variable %q", ex.Name)
		}
		return v, nil

	case *parser.PropExpr:
		return evalProp(ex, row)

	case *parser.ParamExpr:
		v, ok := params[ex.Name]
		if !ok {
			return nil, fmt.Errorf("unbound parameter $%s", ex.Name)
		}
		return v, nil

	case *parser.FuncExpr:
		return evalFunc(ex, row, params)

	case *parser.BinaryExpr:
		return evalBinary(ex, row, params)

	case *parser.UnaryExpr:
		return evalUnary(ex, row, params)

	default:
		return nil, fmt.Errorf("unknown expression type %T", e)
	}
}

// evalProp extracts a property from a node or edge bound in the row.
func evalProp(ex *parser.PropExpr, row Row) (interface{}, error) {
	v, ok := row[ex.Variable]
	if !ok {
		return nil, fmt.Errorf("unbound variable %q in property access", ex.Variable)
	}

	switch obj := v.(type) {
	case *graph.Node:
		prop, exists := obj.Properties[ex.Key]
		if !exists {
			return nil, nil
		}
		return ValueToGo(prop), nil
	case *graph.Edge:
		prop, exists := obj.Properties[ex.Key]
		if !exists {
			return nil, nil
		}
		return ValueToGo(prop), nil
	default:
		return nil, fmt.Errorf("variable %q is not a node or edge", ex.Variable)
	}
}

// evalFunc evaluates built-in functions. Currently only id(variable).
func evalFunc(ex *parser.FuncExpr, row Row, params map[string]interface{}) (interface{}, error) {
	name := strings.ToLower(ex.Name)
	switch name {
	case "id":
		if len(ex.Args) != 1 {
			return nil, fmt.Errorf("id() requires exactly 1 argument, got %d", len(ex.Args))
		}
		arg, err := evalExpr(ex.Args[0], row, params)
		if err != nil {
			return nil, err
		}
		switch obj := arg.(type) {
		case *graph.Node:
			return int64(obj.ID), nil
		case *graph.Edge:
			return int64(obj.ID), nil
		default:
			return nil, fmt.Errorf("id() argument must be a node or edge, got %T", arg)
		}
	default:
		return nil, fmt.Errorf("unknown function %q", ex.Name)
	}
}

// evalBinary evaluates a binary expression (comparison, AND, OR).
func evalBinary(ex *parser.BinaryExpr, row Row, params map[string]interface{}) (interface{}, error) {
	// Short-circuit for AND / OR
	if ex.Op == parser.AND {
		lv, err := evalExpr(ex.Left, row, params)
		if err != nil {
			return nil, err
		}
		lb, ok := toBool(lv)
		if !ok {
			return nil, fmt.Errorf("AND: left operand is not boolean")
		}
		if !lb {
			return false, nil
		}
		rv, err := evalExpr(ex.Right, row, params)
		if err != nil {
			return nil, err
		}
		rb, ok := toBool(rv)
		if !ok {
			return nil, fmt.Errorf("AND: right operand is not boolean")
		}
		return rb, nil
	}

	if ex.Op == parser.OR {
		lv, err := evalExpr(ex.Left, row, params)
		if err != nil {
			return nil, err
		}
		lb, ok := toBool(lv)
		if !ok {
			return nil, fmt.Errorf("OR: left operand is not boolean")
		}
		if lb {
			return true, nil
		}
		rv, err := evalExpr(ex.Right, row, params)
		if err != nil {
			return nil, err
		}
		rb, ok := toBool(rv)
		if !ok {
			return nil, fmt.Errorf("OR: right operand is not boolean")
		}
		return rb, nil
	}

	left, err := evalExpr(ex.Left, row, params)
	if err != nil {
		return nil, err
	}
	right, err := evalExpr(ex.Right, row, params)
	if err != nil {
		return nil, err
	}

	return compare(ex.Op, left, right)
}

// evalUnary evaluates a unary expression (NOT).
func evalUnary(ex *parser.UnaryExpr, row Row, params map[string]interface{}) (interface{}, error) {
	if ex.Op != parser.NOT {
		return nil, fmt.Errorf("unsupported unary op %s", ex.Op)
	}
	val, err := evalExpr(ex.Operand, row, params)
	if err != nil {
		return nil, err
	}
	b, ok := toBool(val)
	if !ok {
		return nil, fmt.Errorf("NOT: operand is not boolean")
	}
	return !b, nil
}

// compare performs a comparison between two Go values.
func compare(op parser.TokenType, left, right interface{}) (interface{}, error) {
	// nil handling
	if left == nil || right == nil {
		switch op {
		case parser.EQ:
			return left == nil && right == nil, nil
		case parser.NEQ:
			return left != nil || right != nil, nil
		default:
			return nil, nil
		}
	}

	// Promote to common numeric type for comparison
	ln, lok := toFloat64(left)
	rn, rok := toFloat64(right)

	if lok && rok {
		switch op {
		case parser.EQ:
			return ln == rn, nil
		case parser.NEQ:
			return ln != rn, nil
		case parser.LT:
			return ln < rn, nil
		case parser.GT:
			return ln > rn, nil
		case parser.LTE:
			return ln <= rn, nil
		case parser.GTE:
			return ln >= rn, nil
		}
	}

	// String comparison
	ls, lsok := left.(string)
	rs, rsok := right.(string)
	if lsok && rsok {
		switch op {
		case parser.EQ:
			return ls == rs, nil
		case parser.NEQ:
			return ls != rs, nil
		case parser.LT:
			return ls < rs, nil
		case parser.GT:
			return ls > rs, nil
		case parser.LTE:
			return ls <= rs, nil
		case parser.GTE:
			return ls >= rs, nil
		}
	}

	// Bool equality
	lb, lbok := left.(bool)
	rb, rbok := right.(bool)
	if lbok && rbok {
		switch op {
		case parser.EQ:
			return lb == rb, nil
		case parser.NEQ:
			return lb != rb, nil
		}
	}

	return nil, fmt.Errorf("cannot compare %T and %T with %s", left, right, op)
}

func toBool(v interface{}) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}
