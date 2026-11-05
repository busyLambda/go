// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package macro

import "testing"

func TestBuilder(t *testing.T) {
	var b Builder
	x := b.Name(Position{Line: 1, Col: 1}, "x")
	f := b.Name(Position{Line: 1, Col: 5}, "f")
	call := b.Call(Position{Line: 1, Col: 4}, f, x)
	b.ExprStmt(Position{Line: 1, Col: 1}, call)

	ops := b.Ops()
	if len(ops) != 4 {
		t.Fatalf("got %d ops; want 4", len(ops))
	}
	if ops[0].Kind != OpName || ops[0].Str != "x" {
		t.Fatalf("op 0 = %+v; want name x", ops[0])
	}
	if ops[2].Kind != OpCall || len(ops[2].Args) != 2 || ops[2].Args[0] != f || ops[2].Args[1] != x {
		t.Fatalf("op 2 = %+v; want call f(x)", ops[2])
	}
	if ops[3].Kind != OpExprStmt || len(ops[3].Args) != 1 || ops[3].Args[0] != call {
		t.Fatalf("op 3 = %+v; want expr stmt", ops[3])
	}
}

func testMacro(s Stream) Nodes {
	return Nodes{Name(Position{}, "x")}
}

func panicMacro(Stream) Nodes {
	panic("boom")
}

func TestInvoke(t *testing.T) {
	ops, roots, err := Invoke(testMacro, Stream{{Kind: KindName, Lit: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Kind != OpName || ops[0].Str != "x" {
		t.Fatalf("ops = %+v; want one name x", ops)
	}
	if len(roots) != 1 || roots[0] != 0 {
		t.Fatalf("roots = %v; want [0]", roots)
	}
}

func TestInvokePanic(t *testing.T) {
	if _, _, err := Invoke(panicMacro, nil); err == nil {
		t.Fatal("Invoke did not report panic")
	}
}
