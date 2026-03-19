package parser

import "testing"

func TestLexKeywords(t *testing.T) {
	input := "MATCH CREATE RETURN WHERE SET DELETE AND OR NOT TRUE FALSE NULL AS"
	tokens, err := NewLexer(input).Tokenize()
	if err != nil {
		t.Fatal(err)
	}

	expected := []TokenType{
		MATCH, CREATE, RETURN, WHERE, SET, DELETE,
		AND, OR, NOT, TRUE_KW, FALSE_KW, NULL_KW, AS,
		EOF,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, tt := range expected {
		if tokens[i].Type != tt {
			t.Errorf("token[%d]: expected %s, got %s (%q)", i, tt, tokens[i].Type, tokens[i].Literal)
		}
	}
}

func TestLexCaseInsensitive(t *testing.T) {
	input := "match Match MATCH"
	tokens, err := NewLexer(input).Tokenize()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if tokens[i].Type != MATCH {
			t.Errorf("token[%d]: expected MATCH, got %s", i, tokens[i].Type)
		}
	}
}

func TestLexNumbers(t *testing.T) {
	input := "42 3.14 0 100"
	tokens, err := NewLexer(input).Tokenize()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		typ TokenType
		lit string
	}{
		{INT_LIT, "42"},
		{FLOAT_LIT, "3.14"},
		{INT_LIT, "0"},
		{INT_LIT, "100"},
	}
	for i, c := range cases {
		if tokens[i].Type != c.typ || tokens[i].Literal != c.lit {
			t.Errorf("token[%d]: expected %s %q, got %s %q", i, c.typ, c.lit, tokens[i].Type, tokens[i].Literal)
		}
	}
}

func TestLexStrings(t *testing.T) {
	input := `"hello" 'world' "it\'s" "esc\"ape"`
	tokens, err := NewLexer(input).Tokenize()
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"hello", "world", "it's", `esc"ape`}
	for i, e := range expected {
		if tokens[i].Type != STRING_LIT {
			t.Errorf("token[%d]: expected STRING, got %s", i, tokens[i].Type)
		}
		if tokens[i].Literal != e {
			t.Errorf("token[%d]: expected %q, got %q", i, e, tokens[i].Literal)
		}
	}
}

func TestLexParams(t *testing.T) {
	input := "$name $age"
	tokens, err := NewLexer(input).Tokenize()
	if err != nil {
		t.Fatal(err)
	}
	if tokens[0].Type != PARAM || tokens[0].Literal != "name" {
		t.Errorf("expected PARAM 'name', got %s %q", tokens[0].Type, tokens[0].Literal)
	}
	if tokens[1].Type != PARAM || tokens[1].Literal != "age" {
		t.Errorf("expected PARAM 'age', got %s %q", tokens[1].Type, tokens[1].Literal)
	}
}

func TestLexSymbols(t *testing.T) {
	input := "( ) [ ] { } : . , * = <> < > <= >= -> <- -"
	tokens, err := NewLexer(input).Tokenize()
	if err != nil {
		t.Fatal(err)
	}

	expected := []TokenType{
		LPAREN, RPAREN, LBRACKET, RBRACKET, LBRACE, RBRACE,
		COLON, DOT, COMMA, STAR,
		EQ, NEQ, LT, GT, LTE, GTE,
		ARROW_RIGHT, ARROW_LEFT, DASH,
		EOF,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, tt := range expected {
		if tokens[i].Type != tt {
			t.Errorf("token[%d]: expected %s, got %s (%q)", i, tt, tokens[i].Type, tokens[i].Literal)
		}
	}
}

func TestLexFullQuery(t *testing.T) {
	input := `MATCH (n:Person) WHERE n.name = "Alice" RETURN n`
	tokens, err := NewLexer(input).Tokenize()
	if err != nil {
		t.Fatal(err)
	}

	expected := []TokenType{
		MATCH, LPAREN, IDENT, COLON, IDENT, RPAREN,
		WHERE, IDENT, DOT, IDENT, EQ, STRING_LIT,
		RETURN, IDENT,
		EOF,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, tt := range expected {
		if tokens[i].Type != tt {
			t.Errorf("token[%d]: expected %s, got %s (%q)", i, tt, tokens[i].Type, tokens[i].Literal)
		}
	}
}

func TestLexCreatePattern(t *testing.T) {
	input := `CREATE (n:Person {name: "Alice", age: 30})`
	tokens, err := NewLexer(input).Tokenize()
	if err != nil {
		t.Fatal(err)
	}

	expected := []TokenType{
		CREATE, LPAREN, IDENT, COLON, IDENT, LBRACE,
		IDENT, COLON, STRING_LIT, COMMA,
		IDENT, COLON, INT_LIT,
		RBRACE, RPAREN, EOF,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, tt := range expected {
		if tokens[i].Type != tt {
			t.Errorf("token[%d]: expected %s, got %s (%q)", i, tt, tokens[i].Type, tokens[i].Literal)
		}
	}
}

func TestLexEdgePattern(t *testing.T) {
	input := `(a)-[:KNOWS]->(b)`
	tokens, err := NewLexer(input).Tokenize()
	if err != nil {
		t.Fatal(err)
	}

	expected := []TokenType{
		LPAREN, IDENT, RPAREN,
		DASH, LBRACKET, COLON, IDENT, RBRACKET, ARROW_RIGHT,
		LPAREN, IDENT, RPAREN,
		EOF,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, tt := range expected {
		if tokens[i].Type != tt {
			t.Errorf("token[%d]: expected %s, got %s (%q)", i, tt, tokens[i].Type, tokens[i].Literal)
		}
	}
}

func TestLexLeftArrowEdge(t *testing.T) {
	input := `(a)<-[:KNOWS]-(b)`
	tokens, err := NewLexer(input).Tokenize()
	if err != nil {
		t.Fatal(err)
	}

	expected := []TokenType{
		LPAREN, IDENT, RPAREN,
		ARROW_LEFT, LBRACKET, COLON, IDENT, RBRACKET, DASH,
		LPAREN, IDENT, RPAREN,
		EOF,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, tt := range expected {
		if tokens[i].Type != tt {
			t.Errorf("token[%d]: expected %s, got %s (%q)", i, tt, tokens[i].Type, tokens[i].Literal)
		}
	}
}

func TestLexUnterminatedString(t *testing.T) {
	input := `"hello`
	_, err := NewLexer(input).Tokenize()
	if err == nil {
		t.Fatal("expected error for unterminated string")
	}
}

func TestLexUnexpectedChar(t *testing.T) {
	input := "MATCH @"
	_, err := NewLexer(input).Tokenize()
	if err == nil {
		t.Fatal("expected error for unexpected char")
	}
}
