// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wire

import (
	"bytes"
	"reflect"
	"testing"

	"go/macro"
)

func TestRoundTrip(t *testing.T) {
	req := Request{
		Version: Version,
		Pkg:     "example.com/m",
		Macro:   "M",
		Context: ContextStmt,
		Pos:     macro.Position{File: "a.go", Line: 3, Col: 7},
		Tokens: []macro.Token{
			{Kind: macro.KindName, Lit: "x", Pos: macro.Position{File: "a.go", Line: 3, Col: 9}},
			{Kind: macro.KindLiteral, Lit: "1", Pos: macro.Position{File: "a.go", Line: 3, Col: 11}},
		},
	}
	resp := Response{
		OK: true,
		Ops: []macro.Op{
			{Kind: macro.OpName, Pos: macro.Position{File: "a.go", Line: 3, Col: 9}, Str: "y"},
			{Kind: macro.OpExprStmt, Args: []macro.Node{0}},
		},
		Roots: []macro.Node{1},
	}

	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.Encode(req); err != nil {
		t.Fatal(err)
	}
	if err := enc.Encode(resp); err != nil {
		t.Fatal(err)
	}

	dec := NewDecoder(&buf)
	var gotReq Request
	if err := dec.Decode(&gotReq); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotReq, req) {
		t.Fatalf("request = %+v; want %+v", gotReq, req)
	}
	var gotResp Response
	if err := dec.Decode(&gotResp); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotResp, resp) {
		t.Fatalf("response = %+v; want %+v", gotResp, resp)
	}
}
