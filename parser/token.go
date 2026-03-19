package parser

import "strings"

// TokenType identifies the kind of lexical token.
type TokenType int

const (
	// Literals
	IDENT      TokenType = iota // variable or label name
	INT_LIT                     // 123
	FLOAT_LIT                   // 3.14
	STRING_LIT                  // "hello" or 'hello'
	PARAM                       // $name

	// Keywords
	MATCH
	CREATE
	RETURN
	WHERE
	SET
	DELETE
	AND
	OR
	NOT
	TRUE_KW
	FALSE_KW
	NULL_KW
	AS

	// Symbols
	LPAREN      // (
	RPAREN      // )
	LBRACKET    // [
	RBRACKET    // ]
	LBRACE      // {
	RBRACE      // }
	COLON       // :
	DOT         // .
	COMMA       // ,
	STAR        // *
	EQ          // =
	NEQ         // <>
	LT          // <
	GT          // >
	LTE         // <=
	GTE         // >=
	ARROW_RIGHT // ->
	ARROW_LEFT  // <-
	DASH        // -

	EOF
)

var tokenNames = map[TokenType]string{
	IDENT: "IDENT", INT_LIT: "INT", FLOAT_LIT: "FLOAT", STRING_LIT: "STRING", PARAM: "PARAM",
	MATCH: "MATCH", CREATE: "CREATE", RETURN: "RETURN", WHERE: "WHERE", SET: "SET", DELETE: "DELETE",
	AND: "AND", OR: "OR", NOT: "NOT", TRUE_KW: "TRUE", FALSE_KW: "FALSE", NULL_KW: "NULL", AS: "AS",
	LPAREN: "(", RPAREN: ")", LBRACKET: "[", RBRACKET: "]", LBRACE: "{", RBRACE: "}",
	COLON: ":", DOT: ".", COMMA: ",", STAR: "*",
	EQ: "=", NEQ: "<>", LT: "<", GT: ">", LTE: "<=", GTE: ">=",
	ARROW_RIGHT: "->", ARROW_LEFT: "<-", DASH: "-",
	EOF: "EOF",
}

func (t TokenType) String() string {
	if s, ok := tokenNames[t]; ok {
		return s
	}
	return "UNKNOWN"
}

// Token is a single lexical unit produced by the Lexer.
type Token struct {
	Type    TokenType
	Literal string
	Pos     int
}

// keywords maps upper-cased identifier strings to keyword token types.
var keywords = map[string]TokenType{
	"MATCH":  MATCH,
	"CREATE": CREATE,
	"RETURN": RETURN,
	"WHERE":  WHERE,
	"SET":    SET,
	"DELETE": DELETE,
	"AND":    AND,
	"OR":     OR,
	"NOT":    NOT,
	"TRUE":   TRUE_KW,
	"FALSE":  FALSE_KW,
	"NULL":   NULL_KW,
	"AS":     AS,
}

// LookupKeyword returns the keyword TokenType if word is a keyword,
// otherwise returns IDENT. Comparison is case-insensitive.
func LookupKeyword(word string) TokenType {
	if tt, ok := keywords[strings.ToUpper(word)]; ok {
		return tt
	}
	return IDENT
}
