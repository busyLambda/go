// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noder

import (
	"strings"
	"testing"

	"cmd/compile/internal/syntax"
)

func parseMacroTestFile(t *testing.T, name, src string) *noder {
	t.Helper()
	f, err := syntax.Parse(syntax.NewFileBase(name), strings.NewReader(src), nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return &noder{file: f}
}

func TestCollectMacros(t *testing.T) {
	p := parseMacroTestFile(t, "macros.go", `package p

#defmacro Exported(stream []Token) []Node { return nil }
#defmacro hidden(stream []Token) []Node { return nil }
`)
	macros := collectMacros([]*noder{p})
	if len(macros) != 2 {
		t.Fatalf("got %d macros; want 2", len(macros))
	}
	if macros[0].Name != "Exported" || !macros[0].Exported {
		t.Errorf("macros[0] = %+v; want exported Exported", macros[0])
	}
	if macros[1].Name != "hidden" || macros[1].Exported {
		t.Errorf("macros[1] = %+v; want unexported hidden", macros[1])
	}
}

func TestClassifyMacroPackage(t *testing.T) {
	marked := func(name string) *noder {
		p := parseMacroTestFile(t, name, "package p\n")
		p.file.Pragma = &pragmas{Macro: true}
		return p
	}
	plain := func(name string) *noder {
		return parseMacroTestFile(t, name, "package p\n")
	}
	decl := parseMacroTestFile(t, "decl.go", "package p\n#defmacro m(stream []Token) []Node { return nil }\n")

	tests := []struct {
		name       string
		files      []*noder
		wantMarked int
		wantPlain  int
		wantBad    int
	}{
		{"all marked", []*noder{marked("a.go"), marked("b.go")}, 2, 0, 0},
		{"mixed", []*noder{marked("a.go"), plain("b.go")}, 1, 1, 0},
		{"unmarked clean", []*noder{plain("a.go")}, 0, 1, 0},
		{"unmarked with macro", []*noder{decl}, 0, 1, 1},
		{"cgo generated exempt", []*noder{marked("a.go"), plain("_cgo_gotypes.go")}, 1, 0, 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := classifyMacroPackage(test.files)
			if got := len(info.marked); got != test.wantMarked {
				t.Errorf("marked = %d; want %d", got, test.wantMarked)
			}
			if got := len(info.plain); got != test.wantPlain {
				t.Errorf("plain = %d; want %d", got, test.wantPlain)
			}
			if got := len(info.badDecls); got != test.wantBad {
				t.Errorf("badDecls = %d; want %d", got, test.wantBad)
			}
		})
	}
}
