// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package syntax

import (
	"fmt"
	"strings"
	"testing"
)

// TestMacrospace verifies that '#' is scanned as a single _Macrospace token
// instead of being rejected as an invalid character.
func TestMacrospace(t *testing.T) {
	var s scanner
	var errs []string
	s.init(strings.NewReader("#mymacro"), func(line, col uint, msg string) {
		errs = append(errs, fmt.Sprintf("%d:%d: %s", line, col, msg))
	}, 0)

	s.next()
	if s.tok != _Macrospace {
		t.Fatalf("tok = %v; want _Macrospace", s.tok)
	}
	if got := s.tok.String(); got != "#" {
		t.Fatalf("String() = %q; want %q (regenerate token_string.go)", got, "#")
	}

	s.next()
	if s.tok != _Name || s.lit != "mymacro" {
		t.Fatalf("tok = %v, lit = %q; want _Name %q", s.tok, s.lit, "mymacro")
	}
	if len(errs) != 0 {
		t.Fatalf("scanner errors: %v", errs)
	}
}

func parseMacro(t *testing.T, src string) (*File, []error) {
	t.Helper()
	var errs []error
	f, _ := Parse(NewFileBase("macro.go"), strings.NewReader(src), func(err error) {
		errs = append(errs, err)
	}, nil, 0)
	return f, errs
}

func wantError(t *testing.T, errs []error, substr string) {
	t.Helper()
	for _, err := range errs {
		if strings.Contains(err.Error(), substr) {
			return
		}
	}
	t.Fatalf("missing error containing %q; got %v", substr, errs)
}

func TestMacroDecl(t *testing.T) {
	const src = "package p\n#defmacro m(stream []Token) []Node { return nil }\n"
	f, errs := parseMacro(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(f.DeclList) != 1 {
		t.Fatalf("got %d declarations; want 1", len(f.DeclList))
	}
	fn, ok := f.DeclList[0].(*FuncDecl)
	if !ok {
		t.Fatalf("got %T; want *FuncDecl", f.DeclList[0])
	}
	if !fn.Macro {
		t.Fatal("FuncDecl.Macro = false; want true")
	}
	if fn.Body == nil {
		t.Fatal("macro has no body")
	}
	if fn.Name.Value != "m" {
		t.Fatalf("macro name = %q; want %q", fn.Name.Value, "m")
	}
	if fn.Pos().Line() != 2 || fn.Pos().Col() != 1 {
		t.Fatalf("macro position = %d:%d; want 2:1", fn.Pos().Line(), fn.Pos().Col())
	}
}

func TestMacroCallDecl(t *testing.T) {
	const src = "package p\n#mymacro(a, b(c))\n"
	f, errs := parseMacro(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	mc, ok := f.DeclList[0].(*MacroCall)
	if !ok {
		t.Fatalf("got %T; want *MacroCall", f.DeclList[0])
	}
	if mc.Name.Value != "mymacro" {
		t.Fatalf("macro call name = %q; want %q", mc.Name.Value, "mymacro")
	}
	want := []struct {
		tok token
		lit string
	}{
		{_Name, "a"},
		{_Comma, ""},
		{_Name, "b"},
		{_Lparen, ""},
		{_Name, "c"},
		{_Rparen, ""},
	}
	if len(mc.Args) != len(want) {
		t.Fatalf("got %d args; want %d: %v", len(mc.Args), len(want), mc.Args)
	}
	for i, w := range want {
		if mc.Args[i].Tok != w.tok || (w.lit != "" && mc.Args[i].Lit != w.lit) {
			t.Fatalf("arg %d = %v %q; want %v %q", i, mc.Args[i].Tok, mc.Args[i].Lit, w.tok, w.lit)
		}
	}
}

func TestMacroCallStmt(t *testing.T) {
	const src = "package p\nfunc f() { #mymacro() }\n"
	f, errs := parseMacro(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	fn := f.DeclList[0].(*FuncDecl)
	if len(fn.Body.List) != 1 {
		t.Fatalf("got %d statements; want 1", len(fn.Body.List))
	}
	if _, ok := fn.Body.List[0].(*MacroCall); !ok {
		t.Fatalf("got %T; want *MacroCall", fn.Body.List[0])
	}
}

func TestMacroDefBodyRequired(t *testing.T) {
	const src = "package p\n#defmacro m(stream []Token) []Node\n"
	_, errs := parseMacro(t, src)
	wantError(t, errs, "must have a body")
}

func TestMacroDefInFuncRejected(t *testing.T) {
	const src = "package p\nfunc f() { #defmacro m(stream []Token) []Node { return nil } }\n"
	_, errs := parseMacro(t, src)
	wantError(t, errs, "#defmacro is only allowed at package level")
}

func TestMacroBadParameter(t *testing.T) {
	const src = "package p\n#defmacro m(x int) []Node { return nil }\n"
	_, errs := parseMacro(t, src)
	wantError(t, errs, "macro parameter must be []Token")
}

func TestMacroVariadicRejected(t *testing.T) {
	const src = "package p\n#defmacro m(stream ...Token) []Node { return nil }\n"
	_, errs := parseMacro(t, src)
	wantError(t, errs, "macro parameter must be []Token")
}

func TestMacroBadResult(t *testing.T) {
	const src = "package p\n#defmacro m(stream []Token) int { return 0 }\n"
	_, errs := parseMacro(t, src)
	wantError(t, errs, "macro result must be []Node")
}

// Expression-position macro calls are not supported in v1: only declaration
// and statement positions are parsed.
func TestMacroCallExprRejected(t *testing.T) {
	const src = "package p\nvar _ = #mymacro()\n"
	_, errs := parseMacro(t, src)
	if len(errs) == 0 {
		t.Fatal("parser accepted a macro call in expression position")
	}
	for _, err := range errs {
		if strings.Contains(err.Error(), "invalid character U+0023") {
			t.Fatalf("scanner rejected '#': %v", err)
		}
	}
}

func TestMacroUnterminated(t *testing.T) {
	const src = "package p\n#mymacro(a\n"
	_, errs := parseMacro(t, src)
	wantError(t, errs, "unterminated macro call")
}
