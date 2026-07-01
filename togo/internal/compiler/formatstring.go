// Port of src/compiler/formatString.ts
//
// Parser for java.util.Formatter conversion strings (String.format / printf /
// formatted / Formatter.format). Pure and AST-free. A specifier is
// %[argument_index$][flags][width][.precision]conversion . We parse enough to
// count referenced arguments and classify each consuming conversion for a
// conservative type check; anything malformed returns ok=false so the caller
// stays silent.

package compiler

import (
	"regexp"
	"strconv"
	"strings"
)

// FormatConsumer is one conversion that consumes an argument. ArgIndex is 1-based.
type FormatConsumer struct {
	ArgIndex   int
	Conversion string // conversion letter; "t"/"T" for the whole date/time family
}

// FormatParse is the result of parsing a format string.
type FormatParse struct {
	Consumers []FormatConsumer
	MaxIndex  int // highest argument index referenced (0 when nothing is consumed)
}

var consuming = map[rune]bool{}

func init() {
	for _, r := range "bBhHsScCdoxXeEfgGaAtT" {
		consuming[r] = true
	}
}

// %[index$ or <][flags][width][.precision]conv , anchored so it matches at the '%'.
var specRe = regexp.MustCompile(`^%(\d+\$|<)?([-#+ 0,(]*)(\d+)?(?:\.(\d+))?([a-zA-Z%])`)
var letterRe = regexp.MustCompile(`^[a-zA-Z]$`)

// ParseFormatString parses a Formatter string. ok is false on any malformed or
// unrecognized specifier (caller should then emit no diagnostics).
func ParseFormatString(format string) (FormatParse, bool) {
	consumers := []FormatConsumer{}
	auto := 1      // next ordinary (auto-incrementing) index
	lastIndex := 0 // last index used, for the '<' relative flag
	maxIndex := 0
	i := 0
	for i < len(format) {
		if format[i] != '%' {
			i++
			continue
		}
		loc := specRe.FindStringSubmatch(format[i:])
		if loc == nil {
			return FormatParse{}, false // a lone or malformed '%'
		}
		conversion := loc[5]
		i += len(loc[0])

		// Date/time conversions take one trailing suffix letter (e.g. %tY).
		if conversion == "t" || conversion == "T" {
			if i >= len(format) || !letterRe.MatchString(string(format[i])) {
				return FormatParse{}, false
			}
			i++
		}

		if conversion == "%" || conversion == "n" {
			continue // consume no argument
		}
		if !consuming[rune(conversion[0])] {
			return FormatParse{}, false
		}

		indexTok := loc[1]
		var idx int
		switch indexTok {
		case "":
			idx = auto
			auto++
		case "<":
			if lastIndex == 0 {
				return FormatParse{}, false // '<' with no previous specifier
			}
			idx = lastIndex
		default:
			idx, _ = strconv.Atoi(strings.TrimSuffix(indexTok, "$"))
			if idx == 0 {
				return FormatParse{}, false
			}
		}
		lastIndex = idx
		if idx > maxIndex {
			maxIndex = idx
		}
		consumers = append(consumers, FormatConsumer{ArgIndex: idx, Conversion: conversion})
	}
	return FormatParse{Consumers: consumers, MaxIndex: maxIndex}, true
}

// --- conservative type check for a single conversion -----------------------
// The argument descriptor is a primitive name ("int", ...) or a class FQN. We
// judge a mismatch as definite only when the type is provably incompatible; a
// supertype or user type is Unknown so the runtime type could still satisfy it.

// ArgTypeDescriptor is either a primitive name or a class FQN (the other empty).
type ArgTypeDescriptor struct {
	Primitive string
	Fqn       string
}

// Accepts is the three-valued result of conversionAccepts.
type Accepts int

const (
	AcceptsUnknown Accepts = iota
	AcceptsYes
	AcceptsNo
)

// Leaf set: final/effectively-final classes that are not a supertype of any
// accepted boxed type, so a reference type is decidable only against these.
var knownLeaf = toSet([]string{
	"java.lang.String", "java.lang.Boolean", "java.lang.Character",
	"java.lang.Byte", "java.lang.Short", "java.lang.Integer", "java.lang.Long",
	"java.lang.Float", "java.lang.Double", "java.math.BigInteger",
	"java.math.BigDecimal", "java.lang.StringBuilder", "java.lang.StringBuffer",
})

type fmtCategory struct {
	prims map[string]bool
	fqns  map[string]bool
}

var integralCat = fmtCategory{
	prims: toSet([]string{"byte", "short", "int", "long"}),
	fqns: toSet([]string{
		"java.lang.Byte", "java.lang.Short", "java.lang.Integer",
		"java.lang.Long", "java.math.BigInteger",
	}),
}
var floatCat = fmtCategory{
	prims: toSet([]string{"float", "double"}),
	fqns:  toSet([]string{"java.lang.Float", "java.lang.Double", "java.math.BigDecimal"}),
}
var charCat = fmtCategory{
	prims: toSet([]string{"byte", "short", "char", "int"}),
	fqns: toSet([]string{
		"java.lang.Character", "java.lang.Byte", "java.lang.Short", "java.lang.Integer",
	}),
}

func fmtCategoryOf(conversion string) (fmtCategory, bool) {
	switch conversion {
	case "d", "o", "x", "X":
		return integralCat, true
	case "e", "E", "f", "g", "G", "a", "A":
		return floatCat, true
	case "c", "C":
		return charCat, true
	default:
		return fmtCategory{}, false // general (s/b/h) and date/time: never a definite "no"
	}
}

// ConversionAccepts judges whether conversion can accept an argument of the
// given static type: Yes, No (provably incompatible), or Unknown.
func ConversionAccepts(conversion string, arg ArgTypeDescriptor) Accepts {
	cat, ok := fmtCategoryOf(conversion)
	if !ok {
		return AcceptsUnknown // s/S/b/B/h/H accept anything; t/T too intricate
	}
	if arg.Primitive != "" {
		if cat.prims[arg.Primitive] {
			return AcceptsYes
		}
		return AcceptsNo // primitives fully decidable
	}
	if cat.fqns[arg.Fqn] {
		return AcceptsYes
	}
	if knownLeaf[arg.Fqn] {
		return AcceptsNo
	}
	return AcceptsUnknown
}

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}
