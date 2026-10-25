package db

import (
	"strings"
	"testing"
)

// TestParseDSLWhitespaceAndFuzzySynonyms locks in the compatibility grammar:
// spaces around operators, `contains` and `=~` must all parse into the same
// fuzzy `=` leaf instead of producing "意外的内容 'BARE'".
func TestParseDSLWhitespaceAndFuzzySynonyms(t *testing.T) {
	cases := []struct {
		query string
		field string
		op    string
		value string
	}{
		{`domain==example.com`, "domain", "==", "example.com"},
		{`domain == "example.com"`, "domain", "==", "example.com"},
		{`domain = "example.com"`, "domain", "=", "example.com"},
		{`domain=example.com`, "domain", "=", "example.com"},
		{`url contains "userList"`, "url", "=", "userList"},
		{`url contains userList`, "url", "=", "userList"},
		{`url =~ 'userList'`, "url", "=", "userList"},
		{`url =~ userList`, "url", "=", "userList"},
		{`port >= 443`, "port", ">=", "443"},
		{`domain != "localhost"`, "domain", "!=", "localhost"},
	}
	for _, tc := range cases {
		leaf := mustSingleLeaf(t, tc.query)
		if leaf.Field != tc.field || leaf.Op != tc.op || leaf.Value != tc.value {
			t.Fatalf("%q => %+v, want %s %s %s", tc.query, leaf, tc.field, tc.op, tc.value)
		}
	}
}

func TestParseDSLCombinedExpressionKeepsTree(t *testing.T) {
	node, err := ParseDSL(`(domain = "example.com" OR url contains "api") AND port == 443`)
	if err != nil {
		t.Fatalf("ParseDSL: %v", err)
	}
	where, args, err := buildDSLWhere(node)
	if err != nil {
		t.Fatalf("buildDSLWhere: %v", err)
	}
	if !strings.Contains(where, "domain ILIKE") || !strings.Contains(where, "url ILIKE") ||
		!strings.Contains(where, "port =") {
		t.Fatalf("unexpected where: %s", where)
	}
	if len(args) != 3 || args[0] != "%example.com%" || args[1] != "%api%" || args[2] != 443 {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestParseDSLRejectsMalformedCompatibilityForms(t *testing.T) {
	for _, query := range []string{
		`url =~`,
		`domain foo bar`,
		`url containsX value`,
		`port === 443`,
	} {
		if _, err := ParseDSL(query); err == nil {
			t.Fatalf("%q unexpectedly parsed", query)
		}
	}
}

func TestParseDSLErrorMentionsExample(t *testing.T) {
	_, err := ParseDSL(`domain foo bar`)
	if err == nil || !strings.Contains(err.Error(), "domain==example.com") {
		t.Fatalf("error should point at a correct example, got: %v", err)
	}
}

func mustSingleLeaf(t *testing.T, query string) Expr {
	t.Helper()
	node, err := ParseDSL(query)
	if err != nil {
		t.Fatalf("ParseDSL(%q): %v", query, err)
	}
	if node == nil || node.kind != "leaf" || node.expr == nil {
		t.Fatalf("ParseDSL(%q) did not produce a single leaf: %+v", query, node)
	}
	return *node.expr
}
