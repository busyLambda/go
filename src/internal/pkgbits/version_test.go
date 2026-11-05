// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkgbits

import "testing"

func TestMacroDeclsVersion(t *testing.T) {
	if V5.Has(MacroDecls) {
		t.Fatal("V5 unexpectedly has MacroDecls")
	}
	if !V6.Has(MacroDecls) {
		t.Fatal("V6 does not have MacroDecls")
	}
}
