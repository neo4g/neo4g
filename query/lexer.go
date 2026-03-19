package query

import (
	"fmt"
	"unicode"
)

// Lexer tokenizes a Cypher-lite query string.
type Lexer struct {
	input []rune
	pos   int
}

// NewLexer creates a Lexer for the given input string.
func NewLexer(input string) *Lexer {
	return &Lexer{input: []rune(input)}
}

// Tokenize returns all tokens from the input, ending with EOF.
func (l *Lexer) Tokenize() ([]Token, error) {
	var tokens []Token
	for {
		tok, err := l.NextToken()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
		if tok.Type == EOF {
			break
		}
	}
	return tokens, nil
}

// NextToken returns the next token from the input.
func (l *Lexer) NextToken() (Token, error) {
	l.skipWhitespace()

	if l.pos >= len(l.input) {
		return Token{Type: EOF, Pos: l.pos}, nil
	}

	ch := l.input[l.pos]
	start := l.pos

	switch {
	case ch == '(':
		l.pos++
		return Token{Type: LPAREN, Literal: "(", Pos: start}, nil
	case ch == ')':
		l.pos++
		return Token{Type: RPAREN, Literal: ")", Pos: start}, nil
	case ch == '[':
		l.pos++
		return Token{Type: LBRACKET, Literal: "[", Pos: start}, nil
	case ch == ']':
		l.pos++
		return Token{Type: RBRACKET, Literal: "]", Pos: start}, nil
	case ch == '{':
		l.pos++
		return Token{Type: LBRACE, Literal: "{", Pos: start}, nil
	case ch == '}':
		l.pos++
		return Token{Type: RBRACE, Literal: "}", Pos: start}, nil
	case ch == ':':
		l.pos++
		return Token{Type: COLON, Literal: ":", Pos: start}, nil
	case ch == '.':
		l.pos++
		return Token{Type: DOT, Literal: ".", Pos: start}, nil
	case ch == ',':
		l.pos++
		return Token{Type: COMMA, Literal: ",", Pos: start}, nil
	case ch == '*':
		l.pos++
		return Token{Type: STAR, Literal: "*", Pos: start}, nil
	case ch == '=':
		l.pos++
		return Token{Type: EQ, Literal: "=", Pos: start}, nil

	case ch == '<':
		l.pos++
		if l.pos < len(l.input) {
			switch l.input[l.pos] {
			case '>':
				l.pos++
				return Token{Type: NEQ, Literal: "<>", Pos: start}, nil
			case '=':
				l.pos++
				return Token{Type: LTE, Literal: "<=", Pos: start}, nil
			case '-':
				l.pos++
				return Token{Type: ARROW_LEFT, Literal: "<-", Pos: start}, nil
			}
		}
		return Token{Type: LT, Literal: "<", Pos: start}, nil

	case ch == '>':
		l.pos++
		if l.pos < len(l.input) && l.input[l.pos] == '=' {
			l.pos++
			return Token{Type: GTE, Literal: ">=", Pos: start}, nil
		}
		return Token{Type: GT, Literal: ">", Pos: start}, nil

	case ch == '-':
		l.pos++
		if l.pos < len(l.input) && l.input[l.pos] == '>' {
			l.pos++
			return Token{Type: ARROW_RIGHT, Literal: "->", Pos: start}, nil
		}
		return Token{Type: DASH, Literal: "-", Pos: start}, nil

	case ch == '$':
		return l.readParam()

	case ch == '"' || ch == '\'':
		return l.readString(ch)

	case unicode.IsDigit(ch):
		return l.readNumber()

	case isIdentStart(ch):
		return l.readIdentOrKeyword()
	}

	return Token{}, fmt.Errorf("unexpected character %q at position %d", ch, start)
}

func (l *Lexer) skipWhitespace() {
	for l.pos < len(l.input) && unicode.IsSpace(l.input[l.pos]) {
		l.pos++
	}
}

func (l *Lexer) readParam() (Token, error) {
	start := l.pos
	l.pos++ // skip $

	if l.pos >= len(l.input) || !isIdentStart(l.input[l.pos]) {
		return Token{}, fmt.Errorf("expected parameter name after '$' at position %d", start)
	}

	nameStart := l.pos
	for l.pos < len(l.input) && isIdentPart(l.input[l.pos]) {
		l.pos++
	}
	return Token{Type: PARAM, Literal: string(l.input[nameStart:l.pos]), Pos: start}, nil
}

func (l *Lexer) readString(quote rune) (Token, error) {
	start := l.pos
	l.pos++ // skip opening quote

	var buf []rune
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '\\' && l.pos+1 < len(l.input) {
			l.pos++
			next := l.input[l.pos]
			switch next {
			case 'n':
				buf = append(buf, '\n')
			case 't':
				buf = append(buf, '\t')
			case '\\':
				buf = append(buf, '\\')
			default:
				buf = append(buf, next)
			}
			l.pos++
			continue
		}
		if ch == quote {
			l.pos++ // skip closing quote
			return Token{Type: STRING_LIT, Literal: string(buf), Pos: start}, nil
		}
		buf = append(buf, ch)
		l.pos++
	}
	return Token{}, fmt.Errorf("unterminated string starting at position %d", start)
}

func (l *Lexer) readNumber() (Token, error) {
	start := l.pos
	isFloat := false

	for l.pos < len(l.input) && unicode.IsDigit(l.input[l.pos]) {
		l.pos++
	}

	if l.pos < len(l.input) && l.input[l.pos] == '.' {
		if l.pos+1 < len(l.input) && unicode.IsDigit(l.input[l.pos+1]) {
			isFloat = true
			l.pos++ // skip dot
			for l.pos < len(l.input) && unicode.IsDigit(l.input[l.pos]) {
				l.pos++
			}
		}
	}

	lit := string(l.input[start:l.pos])
	if isFloat {
		return Token{Type: FLOAT_LIT, Literal: lit, Pos: start}, nil
	}
	return Token{Type: INT_LIT, Literal: lit, Pos: start}, nil
}

func (l *Lexer) readIdentOrKeyword() (Token, error) {
	start := l.pos
	for l.pos < len(l.input) && isIdentPart(l.input[l.pos]) {
		l.pos++
	}
	word := string(l.input[start:l.pos])
	tt := LookupKeyword(word)
	return Token{Type: tt, Literal: word, Pos: start}, nil
}

func isIdentStart(ch rune) bool {
	return unicode.IsLetter(ch) || ch == '_'
}

func isIdentPart(ch rune) bool {
	return unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_'
}
