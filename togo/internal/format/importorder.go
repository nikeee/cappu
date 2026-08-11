package format

// Port of src/format/import-order.ts: how the import block is grouped and
// ordered. Shared by the formatter, the source.organizeImports code action and
// the organize_imports MCP tool, so the three can never disagree.
//
// An unset ImportOrder reproduces google-java-format: one lexicographic block in
// google style, and gjf's AOSP order (ImportOrderer.AOSP_IMPORT_COMPARATOR plus
// shouldInsertBlankLineAosp) in aosp style. A configured ImportOrder replaces
// whichever built-in applies.

import (
	"sort"
	"strings"

	"github.com/nikeee/cappu/internal/compiler"
)

// ImportLike is the minimum an import must expose to be ordered: Name is the
// dotted name without `import`/`static`/`.*`/`;` (`java.io.File`).
type ImportLike interface {
	ImportName() string
	ImportIsStatic() bool
}

// importEntry adapts a parsed import declaration to ImportLike: the printer and
// the code action both key on the dotted name, which they compute once.
type importEntry struct {
	name     string
	isStatic bool
	node     *compiler.Node
}

func (e importEntry) ImportName() string   { return e.name }
func (e importEntry) ImportIsStatic() bool { return e.isStatic }

// ImportOrderOptions mirrors the formatter's importOrder configuration. A nil
// ImportOrder means "use the style's built-in order"; an empty (but non-nil)
// one means every import lands in the unmatched block.
type ImportOrderOptions struct {
	Style       string
	ImportOrder []string
}

// androidPrefixes is gjf's AOSP android group (ImportOrderer.Import#isAndroid).
var androidPrefixes = []string{"android.", "androidx.", "dalvik.", "libcore.", "com.android."}

// OrderImports groups and orders imports into the blocks to print, in order.
// Blocks are separated by one blank line; the caller renders each block's lines.
//
// Static imports always form the first block (gjf's rule, and every other Java
// tool's default), so ImportOrder applies to the non-static imports only.
func OrderImports[T ImportLike](imports []T, options ImportOrderOptions) [][]T {
	var statics, rest []T
	for _, i := range imports {
		if i.ImportIsStatic() {
			statics = append(statics, i)
		} else {
			rest = append(rest, i)
		}
	}
	var blocks [][]T
	switch {
	case options.ImportOrder != nil:
		blocks = configuredBlocks(rest, options.ImportOrder)
	case options.Style == "aosp":
		blocks = aospBlocks(rest)
	default:
		blocks = [][]T{sortByName(rest)}
	}
	out := make([][]T, 0, len(blocks)+1)
	for _, b := range append([][]T{sortByName(statics)}, blocks...) {
		if len(b) > 0 {
			out = append(out, b)
		}
	}
	return out
}

func sortByName[T ImportLike](imports []T) []T {
	out := append([]T(nil), imports...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ImportName() < out[j].ImportName() })
	return out
}

// PatternPrefix is the prefix a pattern selects: everything before its trailing
// `*`. The config schema rejects a pattern that does not end in `*`, so this is
// total.
func PatternPrefix(pattern string) string {
	return strings.TrimSuffix(pattern, "*")
}

// configuredBlocks splits ImportOrder into blocks at each "". An import joins
// the block of the pattern with the LONGEST matching prefix - so precedence does
// not depend on list position, and a specific group may sit after a general one.
// Equal prefixes fall back to list order. Imports matching nothing form a last
// block.
func configuredBlocks[T ImportLike](imports []T, importOrder []string) [][]T {
	type pattern struct {
		prefix string
		block  int
	}
	var patterns []pattern
	block := 0
	for _, entry := range importOrder {
		if entry == "" {
			block++
			continue
		}
		patterns = append(patterns, pattern{PatternPrefix(entry), block})
	}
	blockCount := block + 1
	blocks := make([][]T, blockCount+1)
	unmatched := blockCount
	for _, imp := range imports {
		best, bestLen := unmatched, -1
		for _, p := range patterns {
			if !strings.HasPrefix(imp.ImportName(), p.prefix) {
				continue
			}
			if len(p.prefix) > bestLen {
				best, bestLen = p.block, len(p.prefix)
			}
		}
		blocks[best] = append(blocks[best], imp)
	}
	for i := range blocks {
		blocks[i] = sortByName(blocks[i])
	}
	return blocks
}

// aospBlocks is gjf's AOSP order: android imports, then third party, then
// java/javax, lexicographic within each, with a blank line wherever the
// top-level package changes (so `com.foo` and `io.bar` split even though both
// are third party) or an android import is followed by a non-android one.
func aospBlocks[T ImportLike](imports []T) [][]T {
	sorted := append([]T(nil), imports...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := aospRank(sorted[i].ImportName()), aospRank(sorted[j].ImportName())
		if ri != rj {
			return ri < rj
		}
		return sorted[i].ImportName() < sorted[j].ImportName()
	})
	var blocks [][]T
	var current []T
	for _, imp := range sorted {
		if len(current) > 0 && aospSplits(current[len(current)-1].ImportName(), imp.ImportName()) {
			blocks = append(blocks, current)
			current = nil
		}
		current = append(current, imp)
	}
	if len(current) > 0 {
		blocks = append(blocks, current)
	}
	return blocks
}

func isAndroidImport(name string) bool {
	for _, p := range androidPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func importTopLevel(name string) string {
	if dot := strings.Index(name, "."); dot >= 0 {
		return name[:dot]
	}
	return name
}

func aospRank(name string) int {
	if isAndroidImport(name) {
		return 0
	}
	if top := importTopLevel(name); top == "java" || top == "javax" {
		return 2
	}
	return 1
}

// aospSplits is gjf's shouldInsertBlankLineAosp, minus the static case.
func aospSplits(prev, curr string) bool {
	if isAndroidImport(prev) && !isAndroidImport(curr) {
		return true
	}
	return importTopLevel(prev) != importTopLevel(curr)
}
