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
