package ast

import (
	"reflect"
	"testing"
)

func TestSplitArgsRespectsStringLiterals(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`a, b`, []string{"a", " b"}},
		{`f(a, b), c`, []string{"f(a, b)", " c"}},
		// The motivating bug: commas inside a SQL literal sheared the
		// argument into broken halves.
		{`ctx, "SELECT id, total FROM orders", id`, []string{"ctx", ` "SELECT id, total FROM orders"`, " id"}},
		{`'a,b', c`, []string{"'a,b'", " c"}},
		{"`tpl, raw`, x", []string{"`tpl, raw`", " x"}},
		{`"escaped \" quote, here", y`, []string{`"escaped \" quote, here"`, " y"}},
		{`"unterminated, literal`, []string{`"unterminated, literal`}},
	}
	for _, c := range cases {
		if got := splitArgs(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitArgs(%q): want %q, got %q", c.in, c.want, got)
		}
	}
}
