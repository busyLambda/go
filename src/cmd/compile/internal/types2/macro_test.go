// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types2_test

import (
	"strings"
	"testing"

	"cmd/compile/internal/syntax"

	. "cmd/compile/internal/types2"
)

func TestMacroUsedImport(t *testing.T) {
	for _, macroUsed := range []bool{false, true} {
		const src = "package p\nimport \"fmt\"\n"
		f, err := syntax.Parse(syntax.NewFileBase("macro.go"), strings.NewReader(src), nil, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		imp := f.DeclList[0].(*syntax.ImportDecl)
		imp.MacroUsed = macroUsed

		var errs []error
		conf := Config{
			Importer: defaultImporter(),
			Error: func(err error) {
				errs = append(errs, err)
			},
		}
		conf.Check("p", []*syntax.File{f}, nil)

		found := false
		for _, err := range errs {
			if strings.Contains(err.Error(), "imported and not used") {
				found = true
			}
		}
		if found != !macroUsed {
			t.Fatalf("macroUsed=%v: unused-import error = %v; errors: %v", macroUsed, found, errs)
		}
	}
}
