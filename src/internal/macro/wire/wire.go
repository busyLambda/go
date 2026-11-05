// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package wire defines the gob protocol between the compiler and a macro
// runner process.
package wire

import (
	"encoding/gob"
	"io"

	"go/macro"
)

// Version is the protocol version. Bump it when Request or Response changes
// incompatibly.
const Version = 2

// Context says where a macro call appeared.
const (
	ContextDecl = iota
	ContextStmt
)

// Request asks a runner to expand one macro call.
type Request struct {
	Version int
	Pkg     string
	Macro   string
	Context uint8
	Pos     macro.Position
	Tokens  []macro.Token
}

// Response is the runner's answer.
type Response struct {
	OK    bool
	Err   string
	Ops   []macro.Op
	Roots []macro.Node
}

// NewEncoder returns a gob encoder for w.
func NewEncoder(w io.Writer) *gob.Encoder {
	return gob.NewEncoder(w)
}

// NewDecoder returns a gob decoder for r.
func NewDecoder(r io.Reader) *gob.Decoder {
	return gob.NewDecoder(r)
}
