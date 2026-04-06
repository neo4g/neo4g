package parser

// Statement is the top-level AST node for a Cypher-lite query.
// A statement is an optional MATCH clause followed by exactly one of
// CREATE, RETURN, SET, or DELETE.
type Statement struct {
	Match  *MatchClause
	Create *CreateClause
	Set    *SetClause
	Delete *DeleteClause
	Return *ReturnClause
}

// IsReadOnly reports whether the statement only reads data (RETURN-only).
func (s *Statement) IsReadOnly() bool {
	return s.Create == nil && s.Set == nil && s.Delete == nil
}

// MatchClause represents MATCH pattern(, pattern)* (WHERE expr)?.
type MatchClause struct {
	Patterns []*Pattern
	Where    Expr
}

// CreateClause represents CREATE pattern(, pattern)*.
type CreateClause struct {
	Patterns []*Pattern
}

// ReturnClause represents RETURN * | returnItem(, returnItem)*.
type ReturnClause struct {
	Star  bool
	Items []*ReturnItem
}

// ReturnItem is a single expression in a RETURN list, optionally aliased.
type ReturnItem struct {
	Expr  Expr
	Alias string
}

// SetClause represents SET setItem(, setItem)*.
type SetClause struct {
	Items []*SetItem
}

// SetItem is a single property assignment: variable.property = expr.
type SetItem struct {
	Variable string
	Property string
	Value    Expr
}

// DeleteClause represents DELETE variable(, variable)*.
type DeleteClause struct {
	Variables []string
}

// --- Patterns ---

// Pattern is a chain of alternating nodes and edges: (a)-[e]->(b).
type Pattern struct {
	Elements []PatternElement
}

// PatternElement is either a NodePattern or an EdgePattern.
type PatternElement interface {
	patternElement()
}

// Direction indicates the direction of an edge in a pattern.
type Direction int

const (
	DirRight Direction = iota // -[]->(
	DirLeft                   // <-[]-(
	DirNone                   // -[]-(
)

// NodePattern represents a node in a pattern: (variable:Label:Label {props}).
type NodePattern struct {
	Variable   string
	Labels     []string
	Properties map[string]Expr
}

func (*NodePattern) patternElement() {}

// EdgePattern represents a relationship in a pattern: -[variable:TYPE {props}]->.
type EdgePattern struct {
	Variable   string
	Types      []string
	Dir        Direction
	Properties map[string]Expr
}

func (*EdgePattern) patternElement() {}

// --- Expressions ---

// Expr is the interface for all expression AST nodes.
type Expr interface {
	expr()
}

// LiteralExpr holds a literal value: int64, float64, string, bool, or nil.
type LiteralExpr struct {
	Value interface{}
}

func (*LiteralExpr) expr() {}

// IdentExpr is a bare variable reference.
type IdentExpr struct {
	Name string
}

func (*IdentExpr) expr() {}

// PropExpr is a property access: variable.key.
type PropExpr struct {
	Variable string
	Key      string
}

func (*PropExpr) expr() {}

// ParamExpr is a query parameter reference: $name.
type ParamExpr struct {
	Name string
}

func (*ParamExpr) expr() {}

// FuncExpr is a function call: name(args...).
type FuncExpr struct {
	Name string
	Args []Expr
}

func (*FuncExpr) expr() {}

// BinaryExpr is a binary operation (comparison, AND, OR).
type BinaryExpr struct {
	Op    TokenType
	Left  Expr
	Right Expr
}

func (*BinaryExpr) expr() {}

// UnaryExpr is a unary operation (NOT).
type UnaryExpr struct {
	Op      TokenType
	Operand Expr
}

func (*UnaryExpr) expr() {}
