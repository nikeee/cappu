package format

// Mirrors src/format/import-order.test.ts case for case.

import (
	"strings"
	"testing"
)

type testImport struct {
	name     string
	isStatic bool
}

func (t testImport) ImportName() string   { return t.name }
func (t testImport) ImportIsStatic() bool { return t.isStatic }

func imp(name string) testImport       { return testImport{name: name} }
func staticImp(name string) testImport { return testImport{name: name, isStatic: true} }

// blockNames renders the blocks the way they print, so an expectation reads
// like the output: names joined by ", ", blocks by " | ".
func blockNames(blocks [][]testImport) string {
	parts := make([]string, len(blocks))
	for i, b := range blocks {
		names := make([]string, len(b))
		for j, e := range b {
			names[j] = e.name
		}
		parts[i] = strings.Join(names, ", ")
	}
	return strings.Join(parts, " | ")
}

func TestOrderImports(t *testing.T) {
	tests := []struct {
		name    string
		imports []testImport
		options ImportOrderOptions
		want    string
	}{
		{
			name:    "google style is one lexicographic block",
			imports: []testImport{imp("org.junit.Test"), imp("java.io.File")},
			options: ImportOrderOptions{Style: "google"},
			want:    "java.io.File, org.junit.Test",
		},
		{
			name:    "static imports are their own first block",
			imports: []testImport{imp("java.io.File"), staticImp("org.junit.Assert.assertTrue"), staticImp("a.B.c")},
			options: ImportOrderOptions{Style: "google"},
			want:    "a.B.c, org.junit.Assert.assertTrue | java.io.File",
		},
		{
			name:    "a pattern groups by prefix and an empty entry breaks the block",
			imports: []testImport{imp("java.io.File"), imp("org.junit.Test"), imp("javax.inject.Inject")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"org.*", "", "java.*", "javax.*"}},
			want:    "org.junit.Test | java.io.File, javax.inject.Inject",
		},
		{
			// Under a first-match rule the second block could never fill.
			name:    "the longest matching prefix wins, whatever the list order",
			imports: []testImport{imp("com.acme.Widget"), imp("com.other.Thing")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"com.*", "", "com.acme.*"}},
			want:    "com.other.Thing | com.acme.Widget",
		},
		{
			name:    "equally specific patterns fall back to list order",
			imports: []testImport{imp("com.acme.Widget")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"com.acme.*", "", "com.acme.*"}},
			want:    "com.acme.Widget",
		},
		{
			name:    "`*` is the catch-all wherever it sits",
			imports: []testImport{imp("zzz.Other"), imp("java.io.File"), imp("com.acme.Widget")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"*", "", "java.*"}},
			want:    "com.acme.Widget, zzz.Other | java.io.File",
		},
		{
			name:    "imports matching nothing form a last block of their own",
			imports: []testImport{imp("java.io.File"), imp("com.acme.Widget")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"java.*"}},
			want:    "java.io.File | com.acme.Widget",
		},
		{
			// `com.` must not swallow `common.`.
			name:    "a prefix does not match a longer top-level name",
			imports: []testImport{imp("common.Thing"), imp("com.acme.Widget")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"com.*"}},
			want:    "com.acme.Widget | common.Thing",
		},
		{
			name:    "an empty block is dropped rather than printed as a stray blank line",
			imports: []testImport{imp("java.io.File")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"org.*", "", "java.*"}},
			want:    "java.io.File",
		},
		{
			name: "aosp style groups android, third party and java",
			imports: []testImport{
				imp("java.io.File"), imp("org.junit.Test"), imp("android.view.View"),
				imp("javax.inject.Inject"), imp("com.acme.Widget"),
			},
			options: ImportOrderOptions{Style: "aosp"},
			want:    "android.view.View | com.acme.Widget | org.junit.Test | java.io.File | javax.inject.Inject",
		},
		{
			name:    "aosp splits an android import from a non-android one sharing its top level",
			imports: []testImport{imp("com.android.Foo"), imp("com.acme.Widget")},
			options: ImportOrderOptions{Style: "aosp"},
			want:    "com.android.Foo | com.acme.Widget",
		},
		{
			name:    "aosp keeps one top-level package in one block",
			imports: []testImport{imp("org.junit.Test"), imp("org.mockito.Mockito")},
			options: ImportOrderOptions{Style: "aosp"},
			want:    "org.junit.Test, org.mockito.Mockito",
		},
		// --- degenerate inputs ---
		{
			name:    "no imports produce no blocks at all",
			imports: nil,
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"java.*", "", "*"}},
			want:    "",
		},
		{
			name:    "no imports produce no blocks in aosp style either",
			imports: nil,
			options: ImportOrderOptions{Style: "aosp"},
			want:    "",
		},
		{
			name:    "a file of only static imports is one block",
			imports: []testImport{staticImp("b.C.d"), staticImp("a.B.c")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"java.*", "", "*"}},
			want:    "a.B.c, b.C.d",
		},
		{
			name:    "an empty importOrder puts everything in the unmatched block",
			imports: []testImport{imp("org.junit.Test"), imp("java.io.File")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{}},
			want:    "java.io.File, org.junit.Test",
		},
		{
			name:    "an importOrder of only blank-line entries still groups everything once",
			imports: []testImport{imp("org.junit.Test"), imp("java.io.File")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"", ""}},
			want:    "java.io.File, org.junit.Test",
		},
		// A blank-line entry with no group on one side of it must not print as a
		// stray blank line: the empty block is dropped, wherever it sits.
		{
			name:    "a leading blank-line entry collapses",
			imports: []testImport{imp("java.io.File"), imp("org.junit.Test")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"", "java.*", "", "org.*"}},
			want:    "java.io.File | org.junit.Test",
		},
		{
			name:    "a trailing blank-line entry collapses",
			imports: []testImport{imp("java.io.File"), imp("org.junit.Test")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"java.*", "", "org.*", ""}},
			want:    "java.io.File | org.junit.Test",
		},
		{
			name:    "doubled blank-line entries collapse",
			imports: []testImport{imp("java.io.File"), imp("org.junit.Test")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"java.*", "", "", "org.*"}},
			want:    "java.io.File | org.junit.Test",
		},
		{
			name:    "a group whose pattern matches nothing is dropped",
			imports: []testImport{imp("java.io.File")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"org.*", "", "java.*", "", "com.*"}},
			want:    "java.io.File",
		},
		// --- what a prefix selects ---
		{
			// `import java.util.*;` has the name `java.util`, so a pattern must
			// select its own package as well as everything under it.
			name:    "an on-demand import matches the pattern for its own package",
			imports: []testImport{imp("java.util"), imp("java.util.List"), imp("java.io.File")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"java.util.*", "", "java.*"}},
			want:    "java.util, java.util.List | java.io.File",
		},
		{
			name:    "prefix matching is case-sensitive",
			imports: []testImport{imp("Java.io.File"), imp("java.io.File")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"java.*"}},
			want:    "java.io.File | Java.io.File",
		},
		{
			name:    "the deepest of several nested prefixes wins",
			imports: []testImport{imp("com.acme.internal.Impl"), imp("com.acme.Widget"), imp("com.other.Thing")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"com.*", "", "com.acme.*", "", "com.acme.internal.*"}},
			want:    "com.other.Thing | com.acme.Widget | com.acme.internal.Impl",
		},
		{
			name:    "a static import is never routed by a pattern",
			imports: []testImport{imp("java.io.File"), staticImp("java.util.Arrays.asList")},
			options: ImportOrderOptions{Style: "google", ImportOrder: []string{"java.*"}},
			want:    "java.util.Arrays.asList | java.io.File",
		},
		// --- aosp built-in ---
		{
			// Each is its own top-level package, so each gets a block, android first.
			name:    "aosp counts androidx, dalvik and libcore as android",
			imports: []testImport{imp("libcore.io.Streams"), imp("androidx.core.App"), imp("dalvik.system.VMStack")},
			options: ImportOrderOptions{Style: "aosp"},
			want:    "androidx.core.App | dalvik.system.VMStack | libcore.io.Streams",
		},
		{
			name:    "aosp keeps static imports first, before the android block",
			imports: []testImport{imp("android.view.View"), staticImp("org.junit.Assert.fail")},
			options: ImportOrderOptions{Style: "aosp"},
			want:    "org.junit.Assert.fail | android.view.View",
		},
		{
			name:    "importOrder overrides the style's built-in order",
			imports: []testImport{imp("android.view.View"), imp("java.io.File")},
			options: ImportOrderOptions{Style: "aosp", ImportOrder: []string{"java.*", "", "*"}},
			want:    "java.io.File | android.view.View",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := blockNames(OrderImports(tt.imports, tt.options)); got != tt.want {
				t.Errorf("OrderImports:\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}

// The result depends on the imports, not on the order they arrive in - the
// printer hands them over in source order, the code action in its own.
func TestOrderImportsIgnoresInputOrder(t *testing.T) {
	imports := []testImport{
		imp("org.junit.Test"), imp("java.io.File"), imp("com.acme.Widget"), staticImp("a.B.c"),
	}
	options := ImportOrderOptions{Style: "google", ImportOrder: []string{"com.*", "", "java.*", "", "*"}}
	want := blockNames(OrderImports(imports, options))

	reversed := make([]testImport, len(imports))
	for i, e := range imports {
		reversed[len(imports)-1-i] = e
	}
	shuffled := []testImport{imports[2], imports[0], imports[3], imports[1]}
	for _, in := range [][]testImport{reversed, shuffled} {
		if got := blockNames(OrderImports(in, options)); got != want {
			t.Errorf("OrderImports(%v):\n got: %s\nwant: %s", in, got, want)
		}
	}
}
