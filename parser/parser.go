package parser

import (
	"fmt"
	"strconv"
)

// Parse lexes and parses a Cypher-lite query string into an AST.
func Parse(input string) (*Statement, error) {
	tokens, err := NewLexer(input).Tokenize()
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	return p.parseStatement()
}

type parser struct {
	tokens []Token
	pos    int
}

// --- helpers ---

func (p *parser) peek() Token {
	if p.pos >= len(p.tokens) {
		return Token{Type: EOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) advance() Token {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *parser) expect(tt TokenType) (Token, error) {
	tok := p.advance()
	if tok.Type != tt {
		return tok, fmt.Errorf("expected %s, got %s (%q) at position %d", tt, tok.Type, tok.Literal, tok.Pos)
	}
	return tok, nil
}

func (p *parser) at(tt TokenType) bool {
	return p.peek().Type == tt
}

// --- statement ---

func (p *parser) parseStatement() (*Statement, error) {
	stmt := &Statement{}

	if p.at(EOF) {
		return nil, fmt.Errorf("empty query")
	}

	if p.at(MATCH) {
		mc, err := p.parseMatch()
		if err != nil {
			return nil, err
		}
		stmt.Match = mc
	}

	switch p.peek().Type {
	case CREATE:
		cc, err := p.parseCreate()
		if err != nil {
			return nil, err
		}
		stmt.Create = cc
	case RETURN:
		rc, err := p.parseReturn()
		if err != nil {
			return nil, err
		}
		stmt.Return = rc
	case SET:
		sc, err := p.parseSet()
		if err != nil {
			return nil, err
		}
		stmt.Set = sc
	case DELETE:
		dc, err := p.parseDelete()
		if err != nil {
			return nil, err
		}
		stmt.Delete = dc
	case EOF:
		if stmt.Match != nil && stmt.Match.Patterns != nil {
			return nil, fmt.Errorf("MATCH clause requires RETURN, CREATE, SET, or DELETE")
		}
		return nil, fmt.Errorf("empty query")
	default:
		return nil, fmt.Errorf("unexpected %s (%q) at position %d", p.peek().Type, p.peek().Literal, p.peek().Pos)
	}

	if !p.at(EOF) {
		tok := p.peek()
		return nil, fmt.Errorf("unexpected %s (%q) at position %d after statement", tok.Type, tok.Literal, tok.Pos)
	}

	return stmt, nil
}

// --- MATCH ---

func (p *parser) parseMatch() (*MatchClause, error) {
	if _, err := p.expect(MATCH); err != nil {
		return nil, err
	}

	mc := &MatchClause{}

	pat, err := p.parsePattern()
	if err != nil {
		return nil, err
	}
	mc.Patterns = append(mc.Patterns, pat)

	for p.at(COMMA) {
		p.advance()
		pat, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		mc.Patterns = append(mc.Patterns, pat)
	}

	if p.at(WHERE) {
		p.advance()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		mc.Where = expr
	}

	return mc, nil
}

// --- CREATE ---

func (p *parser) parseCreate() (*CreateClause, error) {
	if _, err := p.expect(CREATE); err != nil {
		return nil, err
	}

	cc := &CreateClause{}

	pat, err := p.parsePattern()
	if err != nil {
		return nil, err
	}
	cc.Patterns = append(cc.Patterns, pat)

	for p.at(COMMA) {
		p.advance()
		pat, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		cc.Patterns = append(cc.Patterns, pat)
	}

	return cc, nil
}

// --- RETURN ---

func (p *parser) parseReturn() (*ReturnClause, error) {
	if _, err := p.expect(RETURN); err != nil {
		return nil, err
	}

	rc := &ReturnClause{}

	if p.at(STAR) {
		p.advance()
		rc.Star = true
		return rc, nil
	}

	item, err := p.parseReturnItem()
	if err != nil {
		return nil, err
	}
	rc.Items = append(rc.Items, item)

	for p.at(COMMA) {
		p.advance()
		item, err := p.parseReturnItem()
		if err != nil {
			return nil, err
		}
		rc.Items = append(rc.Items, item)
	}

	return rc, nil
}

func (p *parser) parseReturnItem() (*ReturnItem, error) {
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	item := &ReturnItem{Expr: expr}

	if p.at(AS) {
		p.advance()
		tok, err := p.expect(IDENT)
		if err != nil {
			return nil, err
		}
		item.Alias = tok.Literal
	}

	return item, nil
}

// --- SET ---

func (p *parser) parseSet() (*SetClause, error) {
	if _, err := p.expect(SET); err != nil {
		return nil, err
	}

	sc := &SetClause{}

	item, err := p.parseSetItem()
	if err != nil {
		return nil, err
	}
	sc.Items = append(sc.Items, item)

	for p.at(COMMA) {
		p.advance()
		item, err := p.parseSetItem()
		if err != nil {
			return nil, err
		}
		sc.Items = append(sc.Items, item)
	}

	return sc, nil
}

func (p *parser) parseSetItem() (*SetItem, error) {
	varTok, err := p.expect(IDENT)
	if err != nil {
		return nil, fmt.Errorf("SET expected variable: %w", err)
	}
	if _, err := p.expect(DOT); err != nil {
		return nil, err
	}
	propTok, err := p.expect(IDENT)
	if err != nil {
		return nil, fmt.Errorf("SET expected property name: %w", err)
	}
	if _, err := p.expect(EQ); err != nil {
		return nil, err
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	return &SetItem{Variable: varTok.Literal, Property: propTok.Literal, Value: val}, nil
}

// --- DELETE ---

func (p *parser) parseDelete() (*DeleteClause, error) {
	if _, err := p.expect(DELETE); err != nil {
		return nil, err
	}

	dc := &DeleteClause{}

	tok, err := p.expect(IDENT)
	if err != nil {
		return nil, fmt.Errorf("DELETE expected variable: %w", err)
	}
	dc.Variables = append(dc.Variables, tok.Literal)

	for p.at(COMMA) {
		p.advance()
		tok, err := p.expect(IDENT)
		if err != nil {
			return nil, err
		}
		dc.Variables = append(dc.Variables, tok.Literal)
	}

	return dc, nil
}

// --- Patterns ---

func (p *parser) parsePattern() (*Pattern, error) {
	pat := &Pattern{}

	node, err := p.parseNodePattern()
	if err != nil {
		return nil, err
	}
	pat.Elements = append(pat.Elements, node)

	for p.at(DASH) || p.at(ARROW_LEFT) {
		edge, err := p.parseEdgePattern()
		if err != nil {
			return nil, err
		}
		pat.Elements = append(pat.Elements, edge)

		node, err := p.parseNodePattern()
		if err != nil {
			return nil, err
		}
		pat.Elements = append(pat.Elements, node)
	}

	return pat, nil
}

func (p *parser) parseNodePattern() (*NodePattern, error) {
	if _, err := p.expect(LPAREN); err != nil {
		return nil, fmt.Errorf("expected '(' for node pattern: %w", err)
	}

	np := &NodePattern{}

	if p.at(IDENT) {
		np.Variable = p.advance().Literal
	}

	for p.at(COLON) {
		p.advance()
		tok, err := p.expect(IDENT)
		if err != nil {
			return nil, fmt.Errorf("expected label after ':': %w", err)
		}
		np.Labels = append(np.Labels, tok.Literal)
	}

	if p.at(LBRACE) {
		props, err := p.parsePropMap()
		if err != nil {
			return nil, err
		}
		np.Properties = props
	}

	if _, err := p.expect(RPAREN); err != nil {
		return nil, fmt.Errorf("expected ')' to close node pattern: %w", err)
	}

	return np, nil
}

func (p *parser) parseEdgePattern() (*EdgePattern, error) {
	ep := &EdgePattern{}

	// Determine direction from leading token
	// -[...]-> or -[...]- (right or undirected)
	// <-[...]- (left)
	if p.at(ARROW_LEFT) {
		p.advance() // consume <-
		ep.Dir = DirLeft
	} else if p.at(DASH) {
		p.advance()       // consume -
		ep.Dir = DirRight // tentatively right, confirmed by trailing ->
	} else {
		return nil, fmt.Errorf("expected '-' or '<-' at position %d", p.peek().Pos)
	}

	if p.at(LBRACKET) {
		p.advance()

		if p.at(IDENT) {
			ep.Variable = p.advance().Literal
		}

		for p.at(COLON) {
			p.advance()
			tok, err := p.expect(IDENT)
			if err != nil {
				return nil, fmt.Errorf("expected type after ':': %w", err)
			}
			ep.Types = append(ep.Types, tok.Literal)
		}

		if p.at(LBRACE) {
			props, err := p.parsePropMap()
			if err != nil {
				return nil, err
			}
			ep.Properties = props
		}

		if _, err := p.expect(RBRACKET); err != nil {
			return nil, fmt.Errorf("expected ']' to close edge pattern: %w", err)
		}
	}

	// Trailing direction token
	if ep.Dir == DirLeft {
		// <-[...]- : expect trailing DASH
		if _, err := p.expect(DASH); err != nil {
			return nil, fmt.Errorf("expected '-' after ']' for left-arrow edge: %w", err)
		}
	} else {
		// Started with -, check for -> or just -
		if p.at(ARROW_RIGHT) {
			p.advance()
			ep.Dir = DirRight
		} else if p.at(DASH) {
			p.advance()
			ep.Dir = DirNone
		} else {
			return nil, fmt.Errorf("expected '->' or '-' after ']' at position %d", p.peek().Pos)
		}
	}

	return ep, nil
}

func (p *parser) parsePropMap() (map[string]Expr, error) {
	if _, err := p.expect(LBRACE); err != nil {
		return nil, err
	}

	props := make(map[string]Expr)

	if p.at(RBRACE) {
		p.advance()
		return props, nil
	}

	for {
		key, err := p.expect(IDENT)
		if err != nil {
			return nil, fmt.Errorf("expected property name: %w", err)
		}
		if _, err := p.expect(COLON); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		props[key.Literal] = val

		if !p.at(COMMA) {
			break
		}
		p.advance()
	}

	if _, err := p.expect(RBRACE); err != nil {
		return nil, fmt.Errorf("expected '}' to close property map: %w", err)
	}

	return props, nil
}

// --- Expressions ---

func (p *parser) parseExpr() (Expr, error) {
	return p.parseOr()
}

func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.at(OR) {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: OR, Left: left, Right: right}
	}

	return left, nil
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}

	for p.at(AND) {
		p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: AND, Left: left, Right: right}
	}

	return left, nil
}

func (p *parser) parseNot() (Expr, error) {
	if p.at(NOT) {
		p.advance()
		operand, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: NOT, Operand: operand}, nil
	}
	return p.parseComparison()
}

func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	switch p.peek().Type {
	case EQ, NEQ, LT, GT, LTE, GTE:
		op := p.advance().Type
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Op: op, Left: left, Right: right}, nil
	}

	return left, nil
}

func (p *parser) parsePrimary() (Expr, error) {
	tok := p.peek()

	switch tok.Type {
	case INT_LIT:
		p.advance()
		v, err := strconv.ParseInt(tok.Literal, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid integer %q: %w", tok.Literal, err)
		}
		return &LiteralExpr{Value: v}, nil

	case FLOAT_LIT:
		p.advance()
		v, err := strconv.ParseFloat(tok.Literal, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid float %q: %w", tok.Literal, err)
		}
		return &LiteralExpr{Value: v}, nil

	case STRING_LIT:
		p.advance()
		return &LiteralExpr{Value: tok.Literal}, nil

	case TRUE_KW:
		p.advance()
		return &LiteralExpr{Value: true}, nil

	case FALSE_KW:
		p.advance()
		return &LiteralExpr{Value: false}, nil

	case NULL_KW:
		p.advance()
		return &LiteralExpr{Value: nil}, nil

	case PARAM:
		p.advance()
		return &ParamExpr{Name: tok.Literal}, nil

	case IDENT:
		p.advance()
		name := tok.Literal

		// property access: ident.ident
		if p.at(DOT) {
			p.advance()
			prop, err := p.expect(IDENT)
			if err != nil {
				return nil, fmt.Errorf("expected property name after '.': %w", err)
			}
			return &PropExpr{Variable: name, Key: prop.Literal}, nil
		}

		// function call: ident(args)
		if p.at(LPAREN) {
			p.advance()
			var args []Expr
			if !p.at(RPAREN) {
				arg, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				args = append(args, arg)
				for p.at(COMMA) {
					p.advance()
					arg, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					args = append(args, arg)
				}
			}
			if _, err := p.expect(RPAREN); err != nil {
				return nil, fmt.Errorf("expected ')' after function args: %w", err)
			}
			return &FuncExpr{Name: name, Args: args}, nil
		}

		return &IdentExpr{Name: name}, nil

	case LPAREN:
		p.advance()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(RPAREN); err != nil {
			return nil, fmt.Errorf("expected ')' after grouped expression: %w", err)
		}
		return expr, nil
	}

	return nil, fmt.Errorf("unexpected %s (%q) at position %d", tok.Type, tok.Literal, tok.Pos)
}
