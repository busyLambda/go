// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package macro defines the interface between the Go compiler and macro
// implementations.
//
// A macro implementation is a normal Go function with the signature
//
//	func(Stream) Nodes
//
// registered with Register from the macro package's init function. The
// function runs in a separate process during compilation. It inspects the
// token stream and constructs a node program with the package-level builder
// functions. The compiler replays that program into its own syntax tree.
//
// Macros are invoked sequentially. A macro must not construct nodes from
// another goroutine after it returns.
package macro

import (
	"fmt"
)

// Kind classifies a token.
type Kind uint8

const (
	Invalid Kind = iota
	KindName
	KindLiteral
	KindOperator
	KindKeyword
	KindPunct
)

// Literal kinds. These match syntax.LitKind so the compiler can replay a
// BasicLit directly.
const (
	LitInt = iota
	LitFloat
	LitImag
	LitRune
	LitString
)

// Position is a source position. It deliberately contains no pointers so it
// can cross a process boundary.
type Position struct {
	File string
	Line uint32
	Col  uint32
}

// Token is one token from a macro call's argument stream.
type Token struct {
	Kind Kind
	Lit  string
	Pos  Position
}

// Stream is the token stream passed to a macro.
type Stream []Token

// Node is a handle to a node in the current builder.
type Node uint32

// Nodes is a list of node handles.
type Nodes []Node

// OpKind identifies a builder operation.
type OpKind uint8

const (
	OpName OpKind = iota
	OpBasicLit
	OpCall
	OpSelector
	OpExprStmt
	OpAssign
	OpReturn
	OpBlock
)

// Op is one builder operation. Args are node handles.
type Op struct {
	Kind OpKind
	Pos  Position
	Str  string
	Args []Node
	Aux  uint8
}

// Builder records node operations in order.
type Builder struct {
	ops []Op
}

// Ops returns the recorded operations.
func (b *Builder) Ops() []Op {
	return b.ops
}

func (b *Builder) node(op Op) Node {
	b.ops = append(b.ops, op)
	return Node(len(b.ops) - 1)
}

// Name records an identifier.
func (b *Builder) Name(pos Position, name string) Node {
	return b.node(Op{Kind: OpName, Pos: pos, Str: name})
}

// BasicLit records a literal. kind is the macro literal kind.
func (b *Builder) BasicLit(pos Position, kind uint8, lit string) Node {
	return b.node(Op{Kind: OpBasicLit, Pos: pos, Str: lit, Aux: kind})
}

// Call records a call. fun is Args[0].
func (b *Builder) Call(pos Position, fun Node, args ...Node) Node {
	all := make([]Node, 0, 1+len(args))
	all = append(all, fun)
	all = append(all, args...)
	return b.node(Op{Kind: OpCall, Pos: pos, Args: all})
}

// Selector records x.sel.
func (b *Builder) Selector(pos Position, x Node, sel string) Node {
	return b.node(Op{Kind: OpSelector, Pos: pos, Str: sel, Args: []Node{x}})
}

// ExprStmt records an expression statement.
func (b *Builder) ExprStmt(pos Position, x Node) Node {
	return b.node(Op{Kind: OpExprStmt, Pos: pos, Args: []Node{x}})
}

// Assign records an assignment. op is the assignment operator.
func (b *Builder) Assign(pos Position, op uint8, lhs, rhs Node) Node {
	return b.node(Op{Kind: OpAssign, Pos: pos, Args: []Node{lhs, rhs}, Aux: op})
}

// Return records a return statement.
func (b *Builder) Return(pos Position, results ...Node) Node {
	return b.node(Op{Kind: OpReturn, Pos: pos, Args: results})
}

// Block records a statement block.
func (b *Builder) Block(pos Position, stmts ...Node) Node {
	return b.node(Op{Kind: OpBlock, Pos: pos, Args: stmts})
}

var current *Builder

func builder() *Builder {
	if current == nil {
		panic("macro: no active builder")
	}
	return current
}

// The package-level functions construct nodes with the builder of the current
// macro invocation. They are the API a macro body normally uses.

// Name records an identifier.
func Name(pos Position, name string) Node { return builder().Name(pos, name) }

// BasicLit records a literal.
func BasicLit(pos Position, kind uint8, lit string) Node { return builder().BasicLit(pos, kind, lit) }

// Call records a call.
func Call(pos Position, fun Node, args ...Node) Node { return builder().Call(pos, fun, args...) }

// Selector records x.sel.
func Selector(pos Position, x Node, sel string) Node { return builder().Selector(pos, x, sel) }

// ExprStmt records an expression statement.
func ExprStmt(pos Position, x Node) Node { return builder().ExprStmt(pos, x) }

// Assign records an assignment.
func Assign(pos Position, op uint8, lhs, rhs Node) Node {
	return builder().Assign(pos, op, lhs, rhs)
}

// Return records a return statement.
func Return(pos Position, results ...Node) Node { return builder().Return(pos, results...) }

// Block records a statement block.
func Block(pos Position, stmts ...Node) Node { return builder().Block(pos, stmts...) }

// Invoke runs fn with a fresh builder and returns the recorded operations and
// the node handles fn returned. A panic in fn is converted into an error.
func Invoke(fn func(Stream) Nodes, s Stream) (ops []Op, roots Nodes, err error) {
	b := new(Builder)
	prev := current
	current = b
	defer func() {
		current = prev
		if r := recover(); r != nil {
			err = fmt.Errorf("macro panic: %v", r)
		}
	}()
	roots = fn(s)
	return b.ops, roots, nil
}
