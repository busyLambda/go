// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package runner implements the entry point for a macro runner process. A
// generated runner main imports the macro packages for side effects and calls
// Main.
package runner

import (
	"fmt"
	"io"
	"os"

	"go/macro"
	"internal/macro/wire"
)

// Main reads requests from stdin and writes responses to stdout until EOF.
// lookup is the generated dispatch table mapping (package path, macro name)
// to the macro implementation.
func Main(lookup func(pkg, name string) (func(macro.Stream) macro.Nodes, bool)) {
	dec := wire.NewDecoder(os.Stdin)
	enc := wire.NewEncoder(os.Stdout)
	for {
		var req wire.Request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "macro runner: %v\n", err)
			return
		}
		if err := enc.Encode(Run(req, lookup)); err != nil {
			fmt.Fprintf(os.Stderr, "macro runner: %v\n", err)
			return
		}
	}
}

// Run expands one macro call.
func Run(req wire.Request, lookup func(pkg, name string) (func(macro.Stream) macro.Nodes, bool)) wire.Response {
	if req.Version != wire.Version {
		return wire.Response{Err: fmt.Sprintf("macro runner: protocol version %d, want %d", req.Version, wire.Version)}
	}
	fn, ok := lookup(req.Pkg, req.Macro)
	if !ok {
		return wire.Response{Err: fmt.Sprintf("macro runner: unknown macro %s.%s", req.Pkg, req.Macro)}
	}
	ops, roots, err := macro.Invoke(fn, req.Tokens)
	if err != nil {
		return wire.Response{Err: err.Error()}
	}
	return wire.Response{OK: true, Ops: ops, Roots: roots}
}
