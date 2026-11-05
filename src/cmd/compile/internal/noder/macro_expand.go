// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package noder

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"go/macro"
	"internal/macro/wire"
	"internal/pkgbits"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"cmd/compile/internal/base"
	"cmd/compile/internal/syntax"
	"cmd/compile/internal/types"
	"cmd/internal/src"
)

// expandMacros finds every #pkg.Name call in the package, validates it against
// the imported macro package's export data, runs the generated macro runner,
// and replaces the calls with the returned nodes.
func expandMacros(m posMap, noders []*noder) {
	exp := &macroExpander{
		m:       m,
		files:   make(map[*syntax.File]*macroFile),
		exports: make(map[string][]types.Macro),
		calls:   make(map[*syntax.MacroCall]*macroCallInfo),
	}

	var files []*syntax.File
	for _, p := range noders {
		if p.file != nil {
			files = append(files, p.file)
		}
	}

	for _, f := range files {
		exp.scanFile(f)
	}
	if len(exp.calls) == 0 {
		return
	}

	runner, err := startMacroRunner(exp)
	if err != nil {
		base.ErrorfAt(src.NoXPos, 0, "macro: %v", err)
		return
	}
	exp.runner = runner
	defer runner.close()

	for _, f := range files {
		exp.expandFile(f)
	}
}

type macroExpander struct {
	m       posMap
	files   map[*syntax.File]*macroFile
	exports map[string][]types.Macro
	calls   map[*syntax.MacroCall]*macroCallInfo
	used    map[string]map[string]bool
	runner  *macroRunner
}

type macroFile struct {
	imports map[string]string
	decls   map[string]*syntax.ImportDecl
}

type macroCallInfo struct {
	file    *syntax.File
	call    *syntax.MacroCall
	path    string
	name    string
	context uint8
}

func (e *macroExpander) fileInfo(f *syntax.File) *macroFile {
	if fi := e.files[f]; fi != nil {
		return fi
	}
	fi := &macroFile{
		imports: make(map[string]string),
		decls:   make(map[string]*syntax.ImportDecl),
	}
	for _, d := range f.DeclList {
		imp, ok := d.(*syntax.ImportDecl)
		if !ok || imp.Path == nil || imp.Path.Bad {
			continue
		}
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(p)
		if imp.LocalPkgName != nil {
			name = imp.LocalPkgName.Value
		}
		if name == "_" || name == "." {
			continue
		}
		fi.imports[name] = p
		fi.decls[name] = imp
	}
	e.files[f] = fi
	return fi
}

func (e *macroExpander) scanFile(f *syntax.File) {
	syntax.Inspect(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.File:
			for _, d := range n.DeclList {
				if call, ok := d.(*syntax.MacroCall); ok {
					e.addCall(f, call, wire.ContextDecl)
				}
			}
		case *syntax.BlockStmt:
			e.scanStmts(f, n.List)
		case *syntax.CaseClause:
			e.scanStmts(f, n.Body)
		case *syntax.CommClause:
			e.scanStmts(f, n.Body)
		}
		return true
	})
}

func (e *macroExpander) scanStmts(f *syntax.File, list []syntax.Stmt) {
	for _, s := range list {
		if call, ok := s.(*syntax.MacroCall); ok {
			e.addCall(f, call, wire.ContextStmt)
		}
	}
}

func (e *macroExpander) addCall(f *syntax.File, call *syntax.MacroCall, context uint8) {
	if e.calls[call] != nil {
		return
	}
	fi := e.fileInfo(f)

	sel, ok := call.Name.(*syntax.SelectorExpr)
	if !ok {
		base.ErrorfAt(e.m.makeXPos(call.Pos()), 0, "macro: unqualified macro call; use #pkg.Name")
		return
	}
	pkg, ok := sel.X.(*syntax.Name)
	if !ok {
		base.ErrorfAt(e.m.makeXPos(call.Pos()), 0, "macro: invalid macro qualifier")
		return
	}
	importPath, ok := fi.imports[pkg.Value]
	if !ok {
		base.ErrorfAt(e.m.makeXPos(call.Pos()), 0, "macro: unknown package %q", pkg.Value)
		return
	}
	name := sel.Sel.Value

	macros, ok := e.readMacros(importPath)
	if !ok {
		return
	}
	found := false
	for _, m := range macros {
		if m.Name != name {
			continue
		}
		if !m.Exported {
			base.ErrorfAt(e.m.makeXPos(call.Pos()), 0, "macro: %s.%s is not exported", importPath, name)
			return
		}
		found = true
		break
	}
	if !found {
		base.ErrorfAt(e.m.makeXPos(call.Pos()), 0, "macro: undefined macro %s.%s", importPath, name)
		return
	}

	if imp := fi.decls[pkg.Value]; imp != nil {
		imp.MacroUsed = true
	}
	if e.used == nil {
		e.used = make(map[string]map[string]bool)
	}
	if e.used[importPath] == nil {
		e.used[importPath] = make(map[string]bool)
	}
	e.used[importPath][name] = true

	e.calls[call] = &macroCallInfo{
		file:    f,
		call:    call,
		path:    importPath,
		name:    name,
		context: context,
	}
}

func (e *macroExpander) readMacros(importPath string) ([]types.Macro, bool) {
	if ms, ok := e.exports[importPath]; ok {
		return ms, true
	}

	f, err := openPackage(importPath)
	if err != nil {
		base.ErrorfAt(src.NoXPos, 0, "macro: reading %s: %v", importPath, err)
		return nil, false
	}
	defer f.Close()

	data, err := readExportData(f)
	if err != nil {
		base.ErrorfAt(src.NoXPos, 0, "macro: reading %s: %v", importPath, err)
		return nil, false
	}

	pr := pkgbits.NewPkgDecoder(importPath, data)
	r := pr.NewDecoder(pkgbits.SectionMeta, pkgbits.PublicRootIdx, pkgbits.SyncPublic)
	r.Sync(pkgbits.SyncPkg)
	r.Reloc(pkgbits.SectionPkg)

	var macros []types.Macro
	if r.Version().Has(pkgbits.MacroDecls) && r.Bool() {
		for i, n := 0, r.Len(); i < n; i++ {
			macros = append(macros, types.Macro{Name: r.String(), Exported: r.Bool()})
		}
	}
	e.exports[importPath] = macros
	return macros, true
}

func (e *macroExpander) expandFile(f *syntax.File) {
	syntax.Inspect(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.File:
			n.DeclList = e.expandDecls(n.DeclList)
		case *syntax.BlockStmt:
			n.List = e.expandStmts(n.List)
		case *syntax.CaseClause:
			n.Body = e.expandStmts(n.Body)
		case *syntax.CommClause:
			n.Body = e.expandStmts(n.Body)
		}
		return true
	})
}

func (e *macroExpander) expandDecls(list []syntax.Decl) []syntax.Decl {
	out := make([]syntax.Decl, 0, len(list))
	for _, d := range list {
		call, ok := d.(*syntax.MacroCall)
		if !ok {
			out = append(out, d)
			continue
		}
		info := e.calls[call]
		if info == nil {
			continue
		}
		for _, n := range e.eval(info) {
			decl, ok := n.(syntax.Decl)
			if !ok {
				base.ErrorfAt(e.m.makeXPos(call.Pos()), 0, "macro: %s.%s returned %T in declaration position", info.path, info.name, n)
				continue
			}
			out = append(out, decl)
		}
	}
	return out
}

func (e *macroExpander) expandStmts(list []syntax.Stmt) []syntax.Stmt {
	out := make([]syntax.Stmt, 0, len(list))
	for _, s := range list {
		call, ok := s.(*syntax.MacroCall)
		if !ok {
			out = append(out, s)
			continue
		}
		info := e.calls[call]
		if info == nil {
			continue
		}
		for _, n := range e.eval(info) {
			switch n := n.(type) {
			case syntax.Stmt:
				out = append(out, n)
			case syntax.Decl:
				ds := &syntax.DeclStmt{DeclList: []syntax.Decl{n}}
				ds.SetPos(call.Pos())
				out = append(out, ds)
			default:
				base.ErrorfAt(e.m.makeXPos(call.Pos()), 0, "macro: %s.%s returned %T in statement position", info.path, info.name, n)
			}
		}
	}
	return out
}

func (e *macroExpander) eval(info *macroCallInfo) []syntax.Node {
	if e.runner == nil {
		return nil
	}
	req := wire.Request{
		Version: wire.Version,
		Pkg:     info.path,
		Macro:   info.name,
		Context: info.context,
		Pos:     macroPosition(info.call.Pos()),
		Tokens:  macroTokens(info.call.Args),
	}
	resp, err := e.runner.do(req)
	if err != nil {
		base.ErrorfAt(e.m.makeXPos(info.call.Pos()), 0, "macro %s.%s: %v", info.path, info.name, err)
		return nil
	}
	if !resp.OK {
		base.ErrorfAt(e.m.makeXPos(info.call.Pos()), 0, "macro %s.%s: %s", info.path, info.name, resp.Err)
		return nil
	}
	nodes, err := replayOps(resp.Ops, resp.Roots, info.call.Pos())
	if err != nil {
		base.ErrorfAt(e.m.makeXPos(info.call.Pos()), 0, "macro %s.%s: %v", info.path, info.name, err)
		return nil
	}
	return nodes
}

func macroPosition(p syntax.Pos) macro.Position {
	return macro.Position{
		File: p.RelFilename(),
		Line: uint32(p.Line()),
		Col:  uint32(p.Col()),
	}
}

func macroTokens(list []syntax.MacroToken) []macro.Token {
	out := make([]macro.Token, len(list))
	for i, t := range list {
		out[i] = macro.Token{
			Kind: macroKind(t.Tok),
			Lit:  t.Lit,
			Pos:  macroPosition(t.Pos),
		}
	}
	return out
}

func macroKind(t syntax.Token) macro.Kind {
	switch t {
	case syntax.TokenName:
		return macro.KindName
	case syntax.TokenLiteral:
		return macro.KindLiteral
	case syntax.TokenOperator, syntax.TokenAssignOp, syntax.TokenIncOp, syntax.TokenStar:
		return macro.KindOperator
	default:
		if syntax.Break <= t && t <= syntax.TokenVar {
			return macro.KindKeyword
		}
		return macro.KindPunct
	}
}

func replayOps(ops []macro.Op, roots []macro.Node, basePos syntax.Pos) ([]syntax.Node, error) {
	nodes := make([]syntax.Node, 0, len(ops))
	nodeAt := func(n macro.Node) syntax.Node {
		if int(n) >= len(nodes) {
			return nil
		}
		return nodes[n]
	}
	exprAt := func(n macro.Node) syntax.Expr {
		x, _ := nodeAt(n).(syntax.Expr)
		return x
	}
	pos := basePos

	for i, op := range ops {
		var n syntax.Node
		switch op.Kind {
		case macro.OpName:
			n = syntax.NewName(pos, op.Str)

		case macro.OpBasicLit:
			lit := &syntax.BasicLit{Value: op.Str, Kind: syntax.LitKind(op.Aux)}
			lit.SetPos(pos)
			n = lit

		case macro.OpCall:
			if len(op.Args) == 0 {
				return nil, fmt.Errorf("op %d: call without function", i)
			}
			fun := exprAt(op.Args[0])
			if fun == nil {
				return nil, fmt.Errorf("op %d: call has invalid function", i)
			}
			call := &syntax.CallExpr{Fun: fun}
			for _, a := range op.Args[1:] {
				arg := exprAt(a)
				if arg == nil {
					return nil, fmt.Errorf("op %d: call has invalid argument", i)
				}
				call.ArgList = append(call.ArgList, arg)
			}
			call.SetPos(pos)
			n = call

		case macro.OpSelector:
			if len(op.Args) != 1 {
				return nil, fmt.Errorf("op %d: selector needs one operand", i)
			}
			x := exprAt(op.Args[0])
			if x == nil {
				return nil, fmt.Errorf("op %d: selector has invalid operand", i)
			}
			sel := &syntax.SelectorExpr{X: x, Sel: syntax.NewName(pos, op.Str)}
			sel.SetPos(pos)
			n = sel

		case macro.OpExprStmt:
			if len(op.Args) != 1 {
				return nil, fmt.Errorf("op %d: expression statement needs one operand", i)
			}
			x := exprAt(op.Args[0])
			if x == nil {
				return nil, fmt.Errorf("op %d: expression statement has invalid operand", i)
			}
			stmt := &syntax.ExprStmt{X: x}
			stmt.SetPos(pos)
			n = stmt

		case macro.OpAssign:
			if len(op.Args) != 2 {
				return nil, fmt.Errorf("op %d: assignment needs two operands", i)
			}
			lhs, rhs := exprAt(op.Args[0]), exprAt(op.Args[1])
			if lhs == nil || rhs == nil {
				return nil, fmt.Errorf("op %d: assignment has invalid operands", i)
			}
			stmt := &syntax.AssignStmt{Op: syntax.Operator(op.Aux), Lhs: lhs, Rhs: rhs}
			stmt.SetPos(pos)
			n = stmt

		case macro.OpReturn:
			stmt := &syntax.ReturnStmt{}
			switch len(op.Args) {
			case 0:
			case 1:
				x := exprAt(op.Args[0])
				if x == nil {
					return nil, fmt.Errorf("op %d: return has invalid operand", i)
				}
				stmt.Results = x
			default:
				list := &syntax.ListExpr{}
				for _, a := range op.Args {
					x := exprAt(a)
					if x == nil {
						return nil, fmt.Errorf("op %d: return has invalid operand", i)
					}
					list.ElemList = append(list.ElemList, x)
				}
				list.SetPos(pos)
				stmt.Results = list
			}
			stmt.SetPos(pos)
			n = stmt

		case macro.OpBlock:
			block := &syntax.BlockStmt{}
			for _, a := range op.Args {
				stmt, ok := nodeAt(a).(syntax.Stmt)
				if !ok {
					return nil, fmt.Errorf("op %d: block has non-statement operand", i)
				}
				block.List = append(block.List, stmt)
			}
			block.SetPos(pos)
			n = block

		default:
			return nil, fmt.Errorf("op %d: unknown kind %d", i, op.Kind)
		}
		nodes = append(nodes, n)
	}

	out := make([]syntax.Node, 0, len(roots))
	for _, r := range roots {
		n := nodeAt(r)
		if n == nil {
			return nil, fmt.Errorf("root %d out of range", r)
		}
		out = append(out, n)
	}
	return out, nil
}

type macroRunner struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	enc     *gob.Encoder
	dec     *gob.Decoder
	stderr  bytes.Buffer
	cleanup func()
}

func startMacroRunner(exp *macroExpander) (*macroRunner, error) {
	cmdPath := base.Flag.MacroCmd
	cleanup := func() {}
	if cmdPath == "" {
		p, c, err := buildMacroRunner(exp.used)
		if err != nil {
			return nil, err
		}
		cmdPath, cleanup = p, c
	}

	r := &macroRunner{cleanup: cleanup}
	r.cmd = exec.Command(cmdPath)
	stdin, err := r.cmd.StdinPipe()
	if err != nil {
		cleanup()
		return nil, err
	}
	stdout, err := r.cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return nil, err
	}
	r.cmd.Stderr = &r.stderr
	if err := r.cmd.Start(); err != nil {
		cleanup()
		return nil, err
	}
	r.stdin = stdin
	r.enc = gob.NewEncoder(stdin)
	r.dec = gob.NewDecoder(stdout)
	return r, nil
}

func (r *macroRunner) do(req wire.Request) (wire.Response, error) {
	var resp wire.Response
	if err := r.enc.Encode(req); err != nil {
		return resp, fmt.Errorf("%v: %s", err, r.stderr.String())
	}
	if err := r.dec.Decode(&resp); err != nil {
		return resp, fmt.Errorf("%v: %s", err, r.stderr.String())
	}
	return resp, nil
}

func (r *macroRunner) close() {
	if r.stdin != nil {
		r.stdin.Close()
	}
	if r.cmd != nil {
		r.cmd.Wait()
	}
	if r.cleanup != nil {
		r.cleanup()
	}
}

// hostEnv returns the process environment with the build target pinned to the
// host. The macro runner must execute on the host, even when the outer build
// cross-compiles.
func hostEnv() []string {
	env := os.Environ()
	out := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, "GOOS=") || strings.HasPrefix(e, "GOARCH=") || strings.HasPrefix(e, "GOTOOLCHAIN=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, "GOTOOLCHAIN=local", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
}

// buildMacroRunner generates a dispatch main from used and builds it outside
// the user's working tree. The binary is placed in a temporary directory.
func buildMacroRunner(used map[string]map[string]bool) (string, func(), error) {
	dir, err := os.MkdirTemp("", "gomacro-runner-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }

	var b strings.Builder
	b.WriteString("package main\n\nimport (\n\t\"go/macro\"\n\t\"go/macro/runner\"\n")

	paths := make([]string, 0, len(used))
	for p := range used {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	aliases := make(map[string]string, len(paths))
	for i, p := range paths {
		alias := fmt.Sprintf("macro%d", i)
		aliases[p] = alias
		fmt.Fprintf(&b, "\t%s %q\n", alias, p)
	}

	b.WriteString(")\n\nfunc lookup(pkg, name string) (func(macro.Stream) macro.Nodes, bool) {\n\tswitch pkg + \".\" + name {\n")
	for _, p := range paths {
		names := make([]string, 0, len(used[p]))
		for n := range used[p] {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&b, "\tcase %q: return func(s macro.Stream) macro.Nodes { return %s.%s([]macro.Token(s)) }, true\n", p+"."+n, aliases[p], n)
		}
	}
	b.WriteString("\t}\n\treturn nil, false\n}\n\nfunc main() { runner.Main(lookup) }\n")

	mainPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainPath, []byte(b.String()), 0666); err != nil {
		cleanup()
		return "", nil, err
	}

	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goTool); err != nil {
		goTool, err = exec.LookPath("go")
		if err != nil {
			cleanup()
			return "", nil, err
		}
	}
	outPath := filepath.Join(dir, "runner")
	cmd := exec.Command(goTool, "build", "-o", outPath, mainPath)
	cmd.Dir = "."
	cmd.Env = hostEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("building macro runner: %v\n%s", err, stderr.String())
	}
	return outPath, cleanup, nil
}
