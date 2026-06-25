// Port of src/format/index.ts.
//
// Public entry point for the Java source formatter. Parses with cappu's own
// parser and regenerates layout via the Doc IR (printer.go / doc.go), targeting
// google-java-format compatibility. Default style is "google" (2-space indent);
// "aosp" is the 4-space variant.

package format

import (
	"github.com/nikeee/cappu/internal/compiler"
)

// FormatSource formats Java source text. fileName is only used for parser
// diagnostics. It returns ErrUnsupportedSyntax when the input cannot be
// reformatted without losing information - a syntax error, or a comment in a
// position the formatter does not yet handle - so callers can leave such files
// untouched.
func FormatSource(text string, options FormatOptions, fileName string) (string, error) {
	if fileName == "" {
		fileName = "input.java"
	}
	if options.Style == "" {
		options.Style = "google"
	}
	sf := compiler.ParseSourceFile(fileName, text)
	if len(sf.AsSourceFile().ParseDiagnostics) > 0 {
		return "", ErrUnsupportedSyntax
	}
	return formatSourceFile(sf, options)
}
