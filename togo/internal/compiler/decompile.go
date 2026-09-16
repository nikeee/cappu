package compiler

// Port of src/compiler/decompile.ts.
//
// `cappu decompile`, phases 1.3 to 1.10 (nikeee/cappu#43): reconstruct Java
// source from bytecode. A symbolic stack interpreter walks a method's basic
// blocks and turns them back into expressions and statements, with the control
// flow structured into `if`/`else`, `&&`/`||`, `?:`, the loop forms, method
// calls, `try`/`catch`, array initializers, string concatenation and `switch`;
// anything that needs a `finally` or an invokedynamic that is not a
// concatenation (later phases) renders as its disassembly plus a
// `throw new UnsupportedOperationException(...)`, so the output is always
// compilable Java.
//
// The text is deliberately rough - callers run it through the formatter
// (internal/cli/decompile.go), which is why this file stays free of a
// dependency on internal/format.
//
// Only the class shape is reconstructed, not the declaration forms that carry
// generated members: an enum keeps its keyword but loses its constants (they
// live in <clinit>) and an obfuscated or non-javac class file can still produce
// something javac would reject. Those are later phases; the bail-out body keeps
// the *method* level honest, not the type level.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// notDecompilable is reported per method when the body is beyond this phase; it
// never escapes DecompileClass.
type notDecompilable struct{ reason string }

func (e *notDecompilable) Error() string { return e.reason }

func bail(format string, a ...any) error { return &notDecompilable{fmt.Sprintf(format, a...)} }

// --- expressions ---------------------------------------------------------------------

// Java operator precedence, high binds tighter. Only the levels straight-line
// code can produce are listed.
const (
	precTernary = 2
	// precAssign: an assignment used as a value binds looser than everything else.
	precAssign  = 1
	precLor     = 3
	precLand    = 4
	precOr      = 5
	precXor     = 6
	precAnd     = 7
	precEq      = 8
	precRel     = 9
	precShift   = 10
	precAdd     = 11
	precMul     = 12
	precUnary   = 14
	precPrimary = 15
)

// logicKind names the structured form of a boolean expression, kept alongside
// its text so negate can flip the operator instead of wrapping everything in a
// `!`: the bytecode branches on the *inverse* of what the source said, so every
// condition is negated exactly once on the way back.
type logicKind int

const (
	logicCompare logicKind = iota
	logicAnd
	logicOr
	logicNot
)

type logicNode struct {
	Kind  logicKind
	Left  *expr
	Right *expr
	Op    string
}

// comparedPair are the operands of an lcmp/fcmp/dcmp, which has no source form
// of its own: the comparison it feeds is what source wrote.
type comparedPair struct {
	Left  expr
	Right expr
}

// expr is a reconstructed value: its source text, how tightly it binds, and its
// Java type (used to declare locals).
type expr struct {
	Text  string
	Prec  int
	Type  string
	Logic *logicNode
	// AsInt is the int form of a value javac materialized as `1`/`0`: written
	// back as the condition itself, which is a boolean, so a use that wants a
	// number has to get the ternary again (`array[c ? 1 : 0]`).
	AsInt    string
	Compared *comparedPair
	// Lambda is set on a lambda: it has no type of its own, so it needs the
	// interface named wherever the context does not say it (a return type the
	// interface erased to `Object`, for one).
	Lambda bool
	// Effects marks a value that does something when it runs - a call. Dropping
	// it has to keep it as a statement, and nothing may write it twice.
	Effects bool
	// Pending is the id of the object `new` left on the stack, which is not a
	// value until its constructor has run. Every copy carries the same id, so
	// the call can put `new C(...)` in all of their places at once. Zero means
	// the value is not one.
	Pending int
	// Shared marks the copies a `dup` made of a receiver (or of an array and
	// its index) that cannot be written twice: only the read-modify-write of
	// a compound assignment may consume them, where the text is written once.
	Shared int
	// ReadOf is the Shared id of the receiver a field or element read came from.
	ReadOf int
	// Bin is the operation a binary expression was built from, kept for a
	// compound assignment to recognise `x op rhs`.
	Bin *binaryNode
	// Inner is the value a narrowing conversion wraps.
	Inner *expr
	// Arms are a conditional's two values: a variable with an open type read
	// in one is typed by what the conditional is asked for.
	Arms *[2]expr
	// Cast is set on a checkcast: assigned to a variable, the cast's type is
	// the declaration javac matched it to.
	Cast bool
	// Untyped is set on a conditional whose reference arms are of different
	// types: the type is their least upper bound, which needs a class hierarchy
	// this has not got. Where the value is target-typed (a return, an argument,
	// a typed variable) that does not matter; a variable typed from it cannot be.
	Untyped bool
	// Init is the array creation javac may still be filling in: `{1, 2, 3}` is a
	// `new int[3]` that is duplicated once per element and written through.
	// Every copy carries the same id, and the elements written so far are what
	// the literal gets rendered from once it is full.
	Init *arrayInit
}

type arrayInit struct {
	ID int
	// Prefix is `new int[]` for an `int[3]`, so the literal is Prefix + `{...}`.
	Prefix   string
	Element  string
	Length   int
	Elements []string
}

func primary(text, typ string) expr { return expr{Text: text, Prec: precPrimary, Type: typ} }

// at renders e parenthesized when it binds looser than the context needs.
func at(e expr, minimum int) string {
	if e.Prec < minimum {
		return "(" + e.Text + ")"
	}
	return e.Text
}

type binaryNode struct {
	Left, Right expr
	Op          string
}

func binaryExpr(left expr, operator string, right expr, prec int, typ string) expr {
	// Every operator here is left-associative, so the right operand needs one
	// more level to keep `a - (b - c)` from losing its parentheses.
	return expr{
		Text: at(left, prec) + " " + operator + " " + at(right, prec+1), Prec: prec, Type: typ,
		Bin: &binaryNode{Left: left, Right: right, Op: operator},
	}
}

// comparePrec reports where an operator binds: `==` and `!=` sit one level
// below the relational operators.
func comparePrec(op string) int {
	if op == "==" || op == "!=" {
		return precEq
	}
	return precRel
}

// foldComparison evaluates two integer literals compared, which only a constant
// condition produces.
func foldComparison(left expr, op string, right expr) (bool, bool) {
	a, err := strconv.Atoi(left.Text)
	if err != nil {
		return false, false
	}
	b, err := strconv.Atoi(right.Text)
	if err != nil {
		return false, false
	}
	switch op {
	case "==":
		return a == b, true
	case "!=":
		return a != b, true
	case "<":
		return a < b, true
	case "<=":
		return a <= b, true
	case ">":
		return a > b, true
	default:
		return a >= b, true
	}
}

func compareExpr(left expr, op string, right expr) expr {
	// `while (true)` is a test against a constant to a compiler that does not
	// fold it, and `1 != 0` is not what anyone wrote.
	if folded, ok := foldComparison(left, op, right); ok {
		if folded {
			return primary("true", "boolean")
		}
		return primary("false", "boolean")
	}
	prec := comparePrec(op)
	return expr{
		Text:  at(left, prec+1) + " " + op + " " + at(right, prec+1),
		Prec:  prec,
		Type:  "boolean",
		Logic: &logicNode{Kind: logicCompare, Left: &left, Right: &right, Op: op},
	}
}

func logicalExpr(kind logicKind, left, right expr) expr {
	prec, operator := precLor, "||"
	if kind == logicAnd {
		prec, operator = precLand, "&&"
	}
	return expr{
		Text:  at(left, prec) + " " + operator + " " + at(right, prec+1),
		Prec:  prec,
		Type:  "boolean",
		Logic: &logicNode{Kind: kind, Left: &left, Right: &right},
	}
}

func notExpr(value expr) expr {
	if value.Text == "true" {
		return primary("false", "boolean")
	}
	if value.Text == "false" {
		return primary("true", "boolean")
	}
	return expr{
		Text:  "!" + at(value, precUnary),
		Prec:  precUnary,
		Type:  "boolean",
		Logic: &logicNode{Kind: logicNot, Left: &value},
	}
}

var flippedComparison = map[string]string{
	"==": "!=", "!=": "==", "<": ">=", ">=": "<", ">": "<=", "<=": ">",
}

// negate renders `!e` the way source would have: a comparison flips its
// operator and a `&&`/`||` goes through De Morgan, because the bytecode always
// carries the negated form of what was written.
func negate(e expr) expr {
	if e.Logic == nil {
		return notExpr(e)
	}
	switch e.Logic.Kind {
	case logicCompare:
		return compareExpr(*e.Logic.Left, flippedComparison[e.Logic.Op], *e.Logic.Right)
	case logicAnd:
		return logicalExpr(logicOr, negate(*e.Logic.Left), negate(*e.Logic.Right))
	case logicOr:
		return logicalExpr(logicAnd, negate(*e.Logic.Left), negate(*e.Logic.Right))
	default:
		return *e.Logic.Left
	}
}

// numericWidth orders the types binary numeric promotion picks between.
var numericWidth = []string{"int", "long", "float", "double"}

func widthOf(typ string) int {
	for i, name := range numericWidth {
		if name == typ {
			return i
		}
	}
	return -1
}

// numeric renders a value in a position that wants a number. A condition javac
// materialized as `1`/`0` reads as a boolean everywhere the type is known (a
// store, a return), but arithmetic and comparisons splice the text in as it
// stands, so it has to become the ternary again.
func numeric(e expr) expr {
	if e.AsInt == "" {
		return e
	}
	return expr{Text: e.AsInt, Prec: precTernary, Type: "int"}
}

// asBoolean rewrites `1` and `0` in a position where the other arm proves a
// boolean was meant.
func asBoolean(e expr) expr {
	if e.Text == "1" {
		return primary("true", "boolean")
	}
	if e.Text == "0" {
		return primary("false", "boolean")
	}
	return e
}

// booleanOperands returns both operands as booleans, when the operation is one:
// a boolean on either side makes the other one a boolean too - `1` and `0` are
// `true` and `false` there. An int that is not one of those (a variable, a `2`)
// makes it an int operation instead, and a boolean javac materialized as `1`/`0`
// on the other side is then a number, which numeric writes.
func booleanOperands(left, right expr) (expr, expr, bool) {
	if left.Type != "boolean" && right.Type != "boolean" {
		return expr{}, expr{}, false
	}
	l, r := asBoolean(left), asBoolean(right)
	if l.Type != "boolean" || r.Type != "boolean" {
		return expr{}, expr{}, false
	}
	return l, r, true
}

// erasedBoolean reports whether a value is the `1`/`0` a boolean was erased to,
// in either form.
func erasedBoolean(e expr) bool {
	return e.AsInt != "" || e.Text == "1" || e.Text == "0"
}

// withoutLiterals is a value's text with its string and character literals
// removed, so a name or an operator inside one is not read as code.
func withoutLiterals(text string) string {
	return literalText.ReplaceAllString(text, "")
}

// embedsAssignment reports whether a value has an assignment inside it - the
// one operator this writes with a lone `=`, which no comparison or compound
// assignment does.
func embedsAssignment(text string) bool {
	return strings.Contains(withoutLiterals(text), " = ")
}

// increments reports whether a value carries an increment. `Effects` says the
// same for a call, but it is a property of the value, and an operand keeps none
// of it when it is nested into a larger expression - the text does.
func increments(text string) bool {
	stripped := withoutLiterals(text)
	return strings.Contains(stripped, "++") || strings.Contains(stripped, "--")
}

// cannotThrow reports whether a statement can be moved across a `super()` -
// which only one that cannot throw can be. A field of `this` assigned a local,
// `this` or a literal is the shape javac writes an inner class's captured values
// in; anything that calls, indexes or reads through another reference can throw,
// and where it lands relative to the `super()` is then observable.
func cannotThrow(statement stmt) bool {
	if statement.Nested != nil {
		return false
	}
	match := plainFieldAssignment.FindStringSubmatch(statement.Text)
	return match != nil && !strings.ContainsAny(match[1], ".([")
}

var plainFieldAssignment = regexp.MustCompile(`^this\.[A-Za-z_$][\w$]* = ([^;]+);$`)

// observesWrites reports whether a value already on the stack could *see* a
// write to a field or an array element - which decides whether the write may be
// written out as a statement in front of it. A local cannot be aliased and a
// literal is a value, so only a field, an array element or a call can. A value
// that increments is the other way round: the write would run before an
// increment that has already happened.
func observesWrites(value expr, locals map[string]bool) bool {
	if value.Effects || increments(value.Text) {
		return true
	}
	withoutLiterals := literalText.ReplaceAllString(value.Text, "")
	// A field access or an array element can be the one being written, under this
	// name or another.
	if strings.ContainsAny(withoutLiterals, ".[") {
		return true
	}
	// A bare name that is not a local is a field of this class, which the store
	// may be to. `this` and the primitive type names of a cast are neither.
	for _, name := range identifierText.FindAllString(withoutLiterals, -1) {
		if !locals[name] && !notAName[name] {
			return true
		}
	}
	return false
}

var (
	literalText    = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'`)
	identifierText = regexp.MustCompile(`[A-Za-z_$][\w$]*`)
	notAName       = map[string]bool{
		"this": true, "true": true, "false": true, "null": true,
		"boolean": true, "byte": true, "char": true, "short": true,
		"int": true, "long": true, "float": true, "double": true,
	}
)

// isConstantText reports whether a value's text is the same every time it is read.
func isConstantText(text string) bool {
	switch text {
	case "null", "true", "false":
		return true
	}
	return numericText.MatchString(text) || stringText.MatchString(text) || charText.MatchString(text)
}

var (
	numericText = regexp.MustCompile(`^-?\d[\w.]*$`)
	stringText  = regexp.MustCompile(`^"(?:[^"\\]|\\.)*"$`)
	charText    = regexp.MustCompile(`^'(?:[^'\\]|\\.)*'$`)
)

// namedLambda is a lambda with its interface named, for a place that does not
// say it.
func namedLambda(value expr) expr {
	if !value.Lambda {
		return value
	}
	return expr{Text: "(" + value.Type + ") " + value.Text, Prec: 0, Type: value.Type}
}

func ternaryExpr(condition, thenValue, elseValue expr) (expr, bool) {
	// A lambda has no type of its own, and a conditional gives it none: the arm
	// has to name the interface, which is what source wrote.
	whenTrue, whenFalse := namedLambda(thenValue), namedLambda(elseValue)
	if (whenTrue.Type == "boolean") != (whenFalse.Type == "boolean") {
		// One arm is a boolean and the other an int: either that int is the
		// `1`/`0` a boolean was erased to, or the boolean is a condition javac
		// materialized and the value really is a number, which AsInt is for.
		boolArm, other := whenTrue, whenFalse
		if whenFalse.Type == "boolean" {
			boolArm, other = whenFalse, whenTrue
		}
		asBool := asBoolean(other)
		var replacement expr
		replacesOther := true
		found := false
		if asBool.Text != other.Text {
			replacement, found = asBool, true
		} else if boolArm.AsInt != "" {
			replacement = expr{Text: boolArm.AsInt, Prec: precTernary, Type: "int"}
			replacesOther, found = false, true
		}
		if !found {
			return expr{}, false // a mix nothing can write
		}
		if (whenTrue.Type == "boolean") == replacesOther {
			whenFalse = replacement
		} else {
			whenTrue = replacement
		}
	}
	// `c ? true : x` and `c ? x : false` are how a short-circuit reads once its
	// value is materialized; writing them back as `||`/`&&` is both shorter and
	// what the source said. Where both arms are booleans javac erased, the int
	// form is the conditional itself, the way it is for any two such arms below.
	shortCircuit := func(value expr) expr {
		if erasedBoolean(thenValue) && erasedBoolean(elseValue) {
			value.AsInt = conditionalOverInts(condition, thenValue, elseValue)
		}
		return value
	}
	switch {
	case whenTrue.Text == "true":
		return shortCircuit(logicalExpr(logicOr, condition, whenFalse)), true
	case whenFalse.Text == "false":
		return shortCircuit(logicalExpr(logicAnd, condition, whenTrue)), true
	case whenTrue.Text == "false":
		return shortCircuit(logicalExpr(logicAnd, negate(condition), whenFalse)), true
	case whenFalse.Text == "true":
		return shortCircuit(logicalExpr(logicOr, negate(condition), whenTrue)), true
	}
	typ := whenTrue.Type
	// An arm that is a conditional of differing arms has no type of its own,
	// and neither has the whole then.
	untyped := whenTrue.Untyped || whenFalse.Untyped
	if whenTrue.Type != whenFalse.Type {
		left, right := widthOf(whenTrue.Type), widthOf(whenFalse.Type)
		switch {
		case left >= 0 && right >= 0 && right > left:
			typ = whenFalse.Type
		case whenTrue.Text == "null":
			// `null` is of the other arm's type.
			typ = whenFalse.Type
		case whenFalse.Text == "null":
		case whenTrue.Type == "java.lang.Object" || whenFalse.Type == "java.lang.Object":
			// An Object arm is the bound, whatever the other is.
			typ = "java.lang.Object"
			untyped = false
		case !primitiveTypeNames[whenTrue.Type] && !primitiveTypeNames[whenFalse.Type]:
			untyped = true
		}
	}
	out := expr{
		Text: at(condition, precTernary+1) + " ? " + at(whenTrue, precTernary) +
			" : " + at(whenFalse, precTernary),
		Prec:    precTernary,
		Type:    typ,
		Untyped: untyped,
		Arms:    &[2]expr{whenTrue, whenFalse},
	}
	// Two arms that are each a boolean javac erased make a value that is one
	// too, and the int form is the conditional over their int forms - which is
	// what source wrote where the result is a number.
	if erasedBoolean(whenTrue) && erasedBoolean(whenFalse) {
		out.AsInt = conditionalOverInts(condition, whenTrue, whenFalse)
	}
	return out, true
}

// conditionalOverInts is the conditional over the int forms of two erased
// booleans.
func conditionalOverInts(condition, whenTrue, whenFalse expr) string {
	return at(condition, precTernary+1) + " ? " + at(numeric(whenTrue), precTernary) +
		" : " + at(numeric(whenFalse), precTernary)
}

// materializedBoolean is the value of a branch whose arms are `1` and `0`: that
// is a boolean, and value is how the condition reads as one - but the int form
// has to keep the *branch's* own arms (`c ? 0 : 1`, not `!c ? 1 : 0`), which is
// the form source wrote and the one that recompiles to the same branch.
func materializedBoolean(value, condition expr, whenTrue string) expr {
	whenFalse := "0"
	if whenTrue == "0" {
		whenFalse = "1"
	}
	out := value
	out.AsInt = at(condition, precTernary+1) + " ? " + whenTrue + " : " + whenFalse
	return out
}

// comparisons are the source operators a branch tests, keyed by the mnemonic's
// suffix.
var comparisons = map[string]string{
	"eq": "==", "ne": "!=", "lt": "<", "ge": ">=", "gt": ">", "le": "<=",
}

type binaryOp struct {
	operator string
	prec     int
}

var binaryOps = map[string]binaryOp{
	"add":  {"+", precAdd},
	"sub":  {"-", precAdd},
	"mul":  {"*", precMul},
	"div":  {"/", precMul},
	"rem":  {"%", precMul},
	"shl":  {"<<", precShift},
	"shr":  {">>", precShift},
	"ushr": {">>>", precShift},
	"and":  {"&", precAnd},
	"or":   {"|", precOr},
	"xor":  {"^", precXor},
}

var primitiveOfPrefix = map[byte]string{
	'i': "int", 'l': "long", 'f': "float", 'd': "double",
	'a': "java.lang.Object", 'b': "byte", 'c': "char", 's': "short",
}

var conversions = map[string]string{
	"i2l": "long", "i2f": "float", "i2d": "double",
	"l2i": "int", "l2f": "float", "l2d": "double",
	"f2i": "int", "f2l": "long", "f2d": "double",
	"d2i": "int", "d2l": "long", "d2f": "float",
	"i2b": "byte", "i2c": "char", "i2s": "short",
}

// --- constants -----------------------------------------------------------------------

// sourceTypeText renders a binary type name as a source *reference*:
// `java.util.Map$Entry` is written `java.util.Map.Entry`, because `Map$Entry`
// resolves to nothing. It stops at the first `$` segment that starts with a
// digit - an anonymous or local class has no source name at all, so its binary
// one is the only thing left to print.
//
// self is the class being declared. This file declares it under its binary name
// (restoring the nesting needs the enclosing file, a later phase), so references
// to it keep the `$` and still resolve.
func sourceTypeText(text, self string) string {
	// Only the class itself keeps the binary name - a *sibling* nested class is
	// a different type, and `Outer$Other` resolves to nothing.
	if !strings.Contains(text, "$") || (self != "" && strings.ReplaceAll(text, "[]", "") == self) {
		return text
	}
	parts := strings.Split(text, "$")
	out := parts[0]
	for _, part := range parts[1:] {
		anonymous := part == "" || (part[0] >= '0' && part[0] <= '9')
		if anonymous || strings.Contains(out, "$") {
			out += "$" + part
		} else {
			out += "." + part
		}
	}
	return out
}

// typeName renders a `Foo$Bar` binary name as a source type reference.
func typeName(internal, self string) string {
	text := strings.ReplaceAll(internal, "/", ".")
	if strings.HasPrefix(internal, "[") {
		text, _ = DescriptorType(internal, 0)
	}
	return sourceTypeText(text, self)
}

// descriptorSourceType renders a descriptor as a source type reference.
func descriptorSourceType(descriptor, self string) string {
	text, _ := DescriptorType(descriptor, 0)
	return sourceTypeText(text, self)
}

// selfOf is the class being decompiled, as its own references have to spell it.
func selfOf(classFile *ClassFile) string {
	return strings.ReplaceAll(classFile.ThisClass, "/", ".")
}

func intLiteral(value int) expr {
	// A negative literal is a unary minus, not part of the token.
	prec := precPrimary
	if value < 0 {
		prec = precUnary
	}
	return expr{Text: strconv.Itoa(value), Prec: prec, Type: "int"}
}

// nonFinite maps NaN and the infinities to the wrapper constants javac inlined
// them from: Java has no literal for them, and javap's `NaNf`/`Infinity` is not
// source.
func nonFinite(value float64, wrapper string) (string, bool) {
	switch {
	case math.IsNaN(value):
		return wrapper + ".NaN", true
	case math.IsInf(value, 1):
		return wrapper + ".POSITIVE_INFINITY", true
	case math.IsInf(value, -1):
		return wrapper + ".NEGATIVE_INFINITY", true
	}
	return "", false
}

func negatablePrec(text string) int {
	if strings.HasPrefix(text, "-") {
		return precUnary
	}
	return precPrimary
}

func constantExpr(pool []*Constant, index uint16, self string) (expr, error) {
	entry := PoolAt(pool, index)
	if entry == nil {
		return expr{}, bail("unsupported constant #%d", index)
	}
	switch entry.Tag {
	case TagInt:
		return intLiteral(int(entry.Int)), nil
	case TagLong:
		text := fmt.Sprintf("%dL", entry.Long)
		return expr{Text: text, Prec: negatablePrec(text), Type: "long"}, nil
	case TagFloat:
		if wrapper, ok := nonFinite(float64(entry.Float), "java.lang.Float"); ok {
			return primary(wrapper, "float"), nil
		}
		text := JavaFloatText(entry.Float) + "f"
		return expr{Text: text, Prec: negatablePrec(text), Type: "float"}, nil
	case TagDouble:
		if wrapper, ok := nonFinite(entry.Double, "java.lang.Double"); ok {
			return primary(wrapper, "double"), nil
		}
		text := JavaDoubleText(entry.Double)
		return expr{Text: text, Prec: negatablePrec(text), Type: "double"}, nil
	case TagString:
		return primary(`"`+escapeString(PoolUtf8(pool, entry.Index))+`"`, "java.lang.String"), nil
	case TagClass:
		return primary(typeName(PoolUtf8(pool, entry.Index), self)+".class", "java.lang.Class"), nil
	}
	// A method handle/type or a dynamic constant: only reachable through the
	// features later phases add.
	return expr{}, bail("unsupported constant #%d", index)
}

func isIntegerText(text string) bool {
	rest := strings.TrimPrefix(text, "-")
	if rest == "" {
		return false
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return false
		}
	}
	return true
}

// coerce renders e as it must be written to land in a target-typed slot. javac
// erases boolean and char to int constants, so the literal has to be written back.
func coerce(e expr, target string) string {
	// A condition javac materialized as `1`/`0` reads as a boolean everywhere
	// but where a number is what belongs - and a conditional is no constant, so
	// the narrower targets need the cast an int would.
	narrow := target == "byte" || target == "short" || target == "char"
	if e.AsInt != "" && target != "boolean" {
		if narrow {
			return "(" + target + ") (" + e.AsInt + ")"
		}
		return e.AsInt
	}
	if e.Type != "int" || !isIntegerText(e.Text) {
		// An int expression javac let into a narrower slot was a constant one
		// (`f ? 'a' : 'b'` with the arms erased); written back, it needs the cast.
		if narrow && e.Type == "int" && e.Prec < precPrimary {
			return "(" + target + ") (" + e.Text + ")"
		}
		return e.Text
	}
	value, err := strconv.Atoi(e.Text)
	if err != nil {
		return e.Text
	}
	if target == "boolean" && (value == 0 || value == 1) {
		if value == 1 {
			return "true"
		}
		return "false"
	}
	if target == "char" {
		if value >= 0x20 && value < 0x7f {
			return "'" + escapeString(string(rune(value))) + "'"
		}
		return "(char) " + e.Text
	}
	return e.Text
}

// --- locals --------------------------------------------------------------------------

type localEntry struct {
	StartPc int
	EndPc   int
	Slot    int
	Name    string
	Type    string
}

// readLocalVariables decodes the LocalVariableTable (JVMS 4.7.13), present only
// for classes built with -g.
func readLocalVariables(code *Code, pool []*Constant) []localEntry {
	attribute, ok := FindAttribute(code.Attributes, "LocalVariableTable")
	if !ok || len(attribute.Bytes) < 2 {
		return nil
	}
	b := attribute.Bytes
	count := int(binary.BigEndian.Uint16(b))
	out := make([]localEntry, 0, count)
	for i := 0; i < count; i++ {
		start := 2 + i*10
		if start+10 > len(b) {
			break
		}
		startPc := int(binary.BigEndian.Uint16(b[start:]))
		descriptor := PoolUtf8(pool, binary.BigEndian.Uint16(b[start+6:]))
		typ := ""
		if descriptor != "" {
			typ, _ = DescriptorType(descriptor, 0)
		}
		out = append(out, localEntry{
			StartPc: startPc,
			EndPc:   startPc + int(binary.BigEndian.Uint16(b[start+2:])),
			Name:    PoolUtf8(pool, binary.BigEndian.Uint16(b[start+4:])),
			Slot:    int(binary.BigEndian.Uint16(b[start+8:])),
			Type:    typ,
		})
	}
	return out
}

// erasedToInt are the types javac erases to int in the bytecode, leaving only
// the use to say so.
var erasedToInt = map[string]bool{"boolean": true, "char": true, "byte": true, "short": true}

// numericTargets are the targets only a number can be coerced into.
var numericTargets = map[string]bool{
	"int": true, "long": true, "float": true, "double": true, "char": true, "byte": true, "short": true,
}

// stmt is a statement, or the body of a nested block. The tree is flattened
// only once the method is done, so a retype can still reach a statement that
// has already been placed inside an `if`.
type stmt struct {
	Text   string
	Nested *[]stmt
}

// withHoisted puts the hoisted declarations in front of the body - except in a
// constructor that chains, where nothing may come before the
// `super(...)`/`this(...)` call (chained, as construct wrote it; a qualified
// `outer.super(...)` can open with anything), so they follow it. That call is
// always the first statement when it is there.
func withHoisted(hoisted, statements []string, chained string) []string {
	if len(statements) == 0 || chained == "" || statements[0] != chained {
		return append(append([]string{}, hoisted...), statements...)
	}
	// One the call's own arguments assign has to be declared before it - which
	// only Java 25 accepts, and is then exactly what that source wrote.
	first := statements[0]
	var before, after []string
	for _, one := range hoisted {
		name := one[strings.LastIndex(one, " ")+1 : len(one)-1]
		if reads(withoutLiterals(first), name) {
			before = append(before, one)
		} else {
			after = append(after, one)
		}
	}
	out := make([]string, 0, len(hoisted)+len(statements))
	out = append(out, before...)
	out = append(out, first)
	out = append(out, after...)
	return append(out, statements[1:]...)
}

func flattenStatements(statements []stmt) []string {
	out := []string{}
	for _, statement := range statements {
		if statement.Nested != nil {
			out = append(out, flattenStatements(*statement.Nested)...)
			continue
		}
		out = append(out, statement.Text)
	}
	return out
}

type localWrite struct {
	List  *[]stmt
	Index int
	Value expr
	// InValue: the assignment is inside an expression, not a statement of its
	// own - it counts as a write, but there is no line at Index to rewrite.
	InValue bool
}

// localDeclaration is where a local was declared; Inline when it carries the
// first value.
type localDeclaration struct {
	List   *[]stmt
	Index  int
	Inline bool
}

type local struct {
	Name     string
	Type     string
	Declared bool
	// Origin is the debug-table row this name came from, when there is one.
	Origin *localEntry
	// Authoritative is set when the type came from a parameter descriptor or the
	// debug table, so a store of a differently-typed value is an assignment to
	// *this* variable (`boolean b` taking `iconst_0`), not a second variable in
	// the same slot.
	Authoritative bool
	// Numeric: the variable was used where only a number can be - incremented,
	// ordered, an index - so a later use that would make it a boolean is a slot
	// reused for another variable, not this one changing its mind.
	Numeric bool
	// Proven: the variable was stored a value that is a boolean and nothing
	// else - a call that returns one, a parameter, a field - so where it stands
	// beside an int-typed variable in `&`, `|`, `^` or `==`, that one is a
	// boolean too.
	Proven bool
	// Writes records where every assignment landed, so a retype can rewrite them.
	Writes []localWrite
	// Declaration is where the declaration landed.
	Declaration *localDeclaration
	// StoreBlocks are the blocks that store to it, which is what says whether a
	// read is unambiguous.
	StoreBlocks map[int]bool
	// Reads counts the loads of it: a read rendered against the type it had
	// then is text, and a later narrowing cannot reach it.
	Reads int
	// Open says the type is not known yet - two arms stored values of
	// different reference types, whose bound this cannot compute - and the
	// first use that asks for one gives it: an argument, a return, a field, or
	// the class a method called on it belongs to. Until then it is an Object.
	Open bool
	// Tentative says the type came from an argument or a return, which take a
	// supertype too: the class a method or field is reached through is the
	// exact one and replaces it, another argument asking for something else
	// is a conflict this cannot resolve.
	Tentative bool
	// Also are the variables this one was assigned to, or from, while its type
	// was open: whatever types one types the others.
	Also []*local
}

type paramSlot struct {
	Slot int
	Type string
}

// parameterSlots reports the slot each declared parameter occupies; long and
// double take two.
func parameterSlots(descriptor string, isStatic bool) []paramSlot {
	var out []paramSlot
	slot := 1
	if isStatic {
		slot = 0
	}
	index := 1 // past '('
	for index < len(descriptor) && descriptor[index] != ')' {
		text, next := DescriptorType(descriptor, index)
		out = append(out, paramSlot{Slot: slot, Type: text})
		if text == "long" || text == "double" {
			slot += 2
		} else {
			slot++
		}
		index = next
	}
	return out
}

func methodReturnType(descriptor string) string {
	text, _ := DescriptorType(descriptor, strings.LastIndex(descriptor, ")")+1)
	return text
}

// --- the method body -----------------------------------------------------------------

// bodyDecompiler turns one method's straight-line bytecode into Java statements.
// --- the control-flow graph ----------------------------------------------------------

// A back edge is a loop, so pc order is not a topological order and the
// dominator analyses below are fixpoints rather than a single pass.

// exitBlock is the virtual block every `return`/`athrow` falls into, so a merge
// always exists.
const exitBlock = -1

var conditionalBranches = map[string]bool{
	"ifeq": true, "ifne": true, "iflt": true, "ifge": true, "ifgt": true, "ifle": true,
	"if_icmpeq": true, "if_icmpne": true, "if_icmplt": true,
	"if_icmpge": true, "if_icmpgt": true, "if_icmple": true,
	"if_acmpeq": true, "if_acmpne": true, "ifnull": true, "ifnonnull": true,
}

var subroutineOpcodes = map[string]bool{"jsr": true, "jsr_w": true, "ret": true, "ret_w": true}

// dups are the stack-copying opcodes, which is how javac writes a compound
// assignment.
var dups = map[string]bool{
	"dup": true, "dup_x1": true, "dup_x2": true,
	"dup2": true, "dup2_x1": true, "dup2_x2": true,
}

var invokes = map[string]bool{
	"invokestatic": true, "invokevirtual": true, "invokeinterface": true, "invokespecial": true,
}

func isGotoMnemonic(mnemonic string) bool {
	return mnemonic == "goto" || mnemonic == "goto_w"
}

func isSwitchMnemonic(mnemonic string) bool {
	return mnemonic == "tableswitch" || mnemonic == "lookupswitch"
}

func isBlockEndMnemonic(mnemonic string) bool {
	if mnemonic == "athrow" || mnemonic == "return" {
		return true
	}
	return len(mnemonic) == len("ireturn") && strings.HasSuffix(mnemonic, "return") &&
		strings.IndexByte("ilfda", mnemonic[0]) >= 0
}

type blockKind int

const (
	blockFall blockKind = iota
	blockConditional
	blockGoto
	blockSwitch
	blockEnd
)

type block struct {
	Start        int
	Instructions []Instruction
	Kind         blockKind
	// Successors of a conditional are [fallthrough, target], in that order.
	Successors []int
}

func buildBlocks(instructions []Instruction, exceptions []ExceptionEntry, splits ...int) (map[int]*block, error) {
	entry := 0
	if len(instructions) > 0 {
		entry = instructions[0].Pc
	}
	leaders := map[int]bool{entry: true}
	// Where a `finally`'s copy begins, which nothing else would split at.
	for _, split := range splits {
		leaders[split] = true
	}
	// A protected range and a handler both begin a statement of their own, and
	// neither is a branch target, so nothing else would split the block there.
	for _, entry := range exceptions {
		leaders[int(entry.StartPc)] = true
		leaders[int(entry.HandlerPc)] = true
	}
	for i, instruction := range instructions {
		mnemonic := instruction.Mnemonic
		// The subroutine opcodes: gone since Java 6, and their control flow is
		// not expressible as a branch.
		if subroutineOpcodes[mnemonic] {
			return nil, bail("unsupported instruction %s", mnemonic)
		}
		branch := conditionalBranches[mnemonic] || isGotoMnemonic(mnemonic)
		if branch {
			leaders[instruction.Arg] = true
		}
		if isSwitchMnemonic(mnemonic) {
			// Arg is the default target, and every case target begins a statement.
			leaders[instruction.Arg] = true
			for _, entry := range instruction.SwitchCases {
				leaders[entry.Target] = true
			}
		}
		if branch || isSwitchMnemonic(mnemonic) || isBlockEndMnemonic(mnemonic) {
			if i+1 < len(instructions) {
				leaders[instructions[i+1].Pc] = true
			}
		}
	}
	blocks := map[int]*block{}
	var current []Instruction
	start := entry
	flush := func(next int, hasNext bool) error {
		if len(current) == 0 {
			return nil
		}
		last := current[len(current)-1]
		kind := blockFall
		var successors []int
		if hasNext {
			successors = []int{next}
		}
		switch {
		case conditionalBranches[last.Mnemonic]:
			if !hasNext {
				return bail("a branch runs off the end")
			}
			kind = blockConditional
			successors = []int{next, last.Arg}
		case isGotoMnemonic(last.Mnemonic):
			kind = blockGoto
			successors = []int{last.Arg}
		case isSwitchMnemonic(last.Mnemonic):
			kind = blockSwitch
			// The default first, then each distinct case target once: the
			// analyses below walk this list, and a repeated edge would be
			// walked twice.
			successors = []int{last.Arg}
			seen := map[int]bool{last.Arg: true}
			for _, entry := range last.SwitchCases {
				if !seen[entry.Target] {
					seen[entry.Target] = true
					successors = append(successors, entry.Target)
				}
			}
		case isBlockEndMnemonic(last.Mnemonic):
			kind = blockEnd
			successors = nil
		case !hasNext:
			return bail("the code runs off the end of the method")
		}
		blocks[start] = &block{Start: start, Instructions: current, Kind: kind, Successors: successors}
		return nil
	}
	for _, instruction := range instructions {
		if leaders[instruction.Pc] && len(current) > 0 {
			if err := flush(instruction.Pc, true); err != nil {
				return nil, err
			}
			current = nil
			start = instruction.Pc
		}
		current = append(current, instruction)
	}
	if err := flush(0, false); err != nil {
		return nil, err
	}
	for _, b := range blocks {
		for _, successor := range b.Successors {
			if blocks[successor] == nil {
				return nil, bail("a branch lands mid-instruction")
			}
		}
	}
	return blocks, nil
}

func containsRange(values [][2]int, wanted [2]int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// clause is one `catch`: the types that reach the handler, and where it begins.
type clause struct {
	Types     []string
	HandlerPc int
}

// monitorRegion is one `synchronized` statement: javac holds the monitor in a
// synthetic local and guards the body with a catch-all that releases it and
// rethrows.
type monitorRegion struct {
	StartPc int
	// EndPc is the end of the last range the handler guards, where the body leaves.
	EndPc     int
	HandlerPc int
	// Slot is the synthetic local the monitor was copied into.
	Slot int
}

// finallyRegion is one `finally`: javac writes the body twice - once on the way
// out, once in a catch-all that rethrows - and the copies are what says where it
// is.
type finallyRegion struct {
	StartPc   int
	EndPc     int
	HandlerPc int
	// Body is the handler's copy, without the store and the rethrow.
	Body []Instruction
}

// finallyBody is the body of a `finally`, when b is the catch-all that rethrows:
// `astore e; <body>; aload e; athrow`, with the same slot at both ends.
func finallyBody(b *block) ([]Instruction, bool) {
	if b == nil || len(b.Instructions) < 4 || b.Kind != blockEnd {
		return nil, false
	}
	kept := b.Instructions
	store, reload, throwing := kept[0], kept[len(kept)-2], kept[len(kept)-1]
	if !strings.HasPrefix(store.Mnemonic, "astore") || !strings.HasPrefix(reload.Mnemonic, "aload") ||
		throwing.Mnemonic != "athrow" || slotOf(store) != slotOf(reload) {
		return nil, false
	}
	return kept[1 : len(kept)-2], true
}

// sameInstructions reports whether two runs are the same code, which a copy has
// to be.
func sameInstructions(left, right []Instruction) bool {
	if len(left) != len(right) {
		return false
	}
	for i, one := range left {
		if one.Mnemonic != right[i].Mnemonic || one.Arg != right[i].Arg || one.Arg2 != right[i].Arg2 {
			return false
		}
	}
	return true
}

// isMonitorHandler reports the monitor slot when b is the release-and-rethrow a
// `synchronized` is guarded by.
func isMonitorHandler(b *block) (int, bool) {
	if b == nil || len(b.Instructions) != 5 {
		return 0, false
	}
	store, load, exit := b.Instructions[0], b.Instructions[1], b.Instructions[2]
	reload, throwing := b.Instructions[3], b.Instructions[4]
	if !strings.HasPrefix(store.Mnemonic, "astore") || !strings.HasPrefix(load.Mnemonic, "aload") ||
		exit.Mnemonic != "monitorexit" || !strings.HasPrefix(reload.Mnemonic, "aload") ||
		throwing.Mnemonic != "athrow" || slotOf(store) != slotOf(reload) {
		return 0, false
	}
	return slotOf(load), true
}

// monitorRegions returns the `synchronized` statements among the catch-all
// ranges, and the exception entries left for tryRegions. A `finally` and a
// try-with-resources are catch-alls too, and those javac writes as duplicated
// code: not this phase.
func monitorRegions(exceptions []ExceptionEntry, blocks map[int]*block, instructions []Instruction) ([]monitorRegion, []finallyRegion, []ExceptionEntry, error) {
	enters := map[int]bool{}
	for _, one := range instructions {
		if one.Mnemonic == "monitorenter" {
			enters[one.Pc+1] = true
		}
	}
	var rest []ExceptionEntry
	byHandler := map[int][]ExceptionEntry{}
	var order []int
	for _, entry := range exceptions {
		if entry.CatchType != "" {
			rest = append(rest, entry)
			continue
		}
		handlerPc := int(entry.HandlerPc)
		if _, seen := byHandler[handlerPc]; !seen {
			order = append(order, handlerPc)
		}
		byHandler[handlerPc] = append(byHandler[handlerPc], entry)
	}
	var monitors []monitorRegion
	var finallys []finallyRegion
	for _, handlerPc := range order {
		slot, ok := isMonitorHandler(blocks[handlerPc])
		// The handler guards itself as well, so a throw out of the release runs
		// it again; that entry says nothing about where the statement is.
		start, end, ranges := 0, 0, 0
		for _, entry := range byHandler[handlerPc] {
			if int(entry.StartPc) == handlerPc {
				continue
			}
			if ranges == 0 || int(entry.StartPc) < start {
				start = int(entry.StartPc)
			}
			if int(entry.EndPc) > end {
				end = int(entry.EndPc)
			}
			ranges++
		}
		if ok && ranges > 0 && enters[start] {
			monitors = append(monitors, monitorRegion{StartPc: start, EndPc: end, HandlerPc: handlerPc, Slot: slot})
			continue
		}
		// A `finally` javac wrote once per way out: this takes the shape with one
		// way out, where the range is not split and the copy sits right after it.
		body, isFinally := finallyBody(blocks[handlerPc])
		if !isFinally || ranges != 1 || len(body) == 0 {
			return nil, nil, nil, bail("a finally or synchronized block")
		}
		copyBlock := blocks[end]
		// Everything that reaches the copy has to come out of the protected range:
		// a jump into it from elsewhere would lose the body this drops.
		fromOutside := false
		for _, b := range blocks {
			if (b.Start < start || b.Start >= end) && containsInt(b.Successors, end) {
				fromOutside = true
			}
		}
		// Nothing may leave the range other than into the copy: javac wrote
		// another copy of the body on any such path, and structuring the range
		// would pull that one in as a statement on top of the `finally` this
		// writes.
		escapes := false
		for _, b := range blocks {
			if b.Start < start || b.Start >= end {
				continue
			}
			for _, target := range b.Successors {
				if target < start || target > end {
					escapes = true
				}
			}
		}
		// The copy is followed by what the body was doing when it left: the jump
		// over the handler, or the `return` javac protected the *value* of.
		leaves := copyBlock != nil &&
			len(copyBlock.Instructions) > len(body) &&
			(isGotoMnemonic(copyBlock.Instructions[len(copyBlock.Instructions)-1].Mnemonic) ||
				copyBlock.Kind == blockEnd)
		if copyBlock == nil || fromOutside || escapes || !leaves ||
			len(copyBlock.Instructions) < len(body) ||
			!sameInstructions(body, copyBlock.Instructions[:len(body)]) {
			return nil, nil, nil, bail("a finally with more than one way out")
		}
		finallys = append(finallys, finallyRegion{StartPc: start, EndPc: end, HandlerPc: handlerPc, Body: body})
	}
	return monitors, finallys, rest, nil
}

// tryRegion is one `try` statement: the range it protects, and the clauses
// guarding it.
type tryRegion struct {
	StartPc int
	EndPc   int
	Clauses []*clause
}

// tryRegions are the `try` statements of one method, from its exception table. A
// clause is one handler with the types that reach it (a multi-catch reaches the
// table as one entry per type); the clauses guarding the same range are one
// statement, in source order.
func tryRegions(exceptions []ExceptionEntry, blocks map[int]*block, self string) ([]*tryRegion, error) {
	type clauseRanges struct {
		types  []string
		ranges [][2]int
	}
	byHandler := map[int]*clauseRanges{}
	var order []int
	for _, entry := range exceptions {
		startPc, endPc, handlerPc := int(entry.StartPc), int(entry.EndPc), int(entry.HandlerPc)
		// monitorRegions has taken the catch-alls it knows, and rejected the rest.
		if entry.CatchType == "" {
			return nil, bail("a finally block")
		}
		if blocks[startPc] == nil || blocks[handlerPc] == nil {
			return nil, bail("a try range that starts mid-instruction")
		}
		if handlerPc >= startPc && handlerPc < endPc {
			return nil, bail("a handler inside its own try")
		}
		found := byHandler[handlerPc]
		if found == nil {
			found = &clauseRanges{}
			byHandler[handlerPc] = found
			order = append(order, handlerPc)
		}
		caught := typeName(entry.CatchType, self)
		if !containsString(found.types, caught) {
			found.types = append(found.types, caught)
		}
		// A multi-catch is one entry per type over the same range: one clause,
		// one range.
		if !containsRange(found.ranges, [2]int{startPc, endPc}) {
			found.ranges = append(found.ranges, [2]int{startPc, endPc})
		}
	}

	byRange := map[[2]int]*tryRegion{}
	guards := map[*tryRegion]string{}
	var regions []*tryRegion
	for _, handlerPc := range order {
		found := byHandler[handlerPc]
		sort.Slice(found.ranges, func(i, j int) bool { return found.ranges[i][0] < found.ranges[j][0] })
		merged := found.ranges[0]
		for _, next := range found.ranges[1:] {
			// javac splits the range of a `try` around what it does not protect:
			// the return or jump that ends a nested `catch`, or the `return`,
			// `break` or `continue` that leaves the body. Both are body in
			// source, so the gap may only hold blocks that jump or end, and none
			// may run past it.
			joined := next[0] <= merged[1]
			if !joined {
				joined = true
				for start, b := range blocks {
					if start < merged[1] || start >= next[0] {
						continue
					}
					last := b.Instructions[len(b.Instructions)-1]
					if (b.Kind != blockGoto && b.Kind != blockEnd) || last.Pc >= next[0] {
						joined = false
						break
					}
				}
			}
			if !joined {
				return nil, bail("a try with a split range")
			}
			merged = [2]int{merged[0], max(merged[1], next[1])}
		}
		guarded := ""
		for _, one := range found.ranges {
			guarded += strconv.Itoa(one[0]) + ":" + strconv.Itoa(one[1]) + ","
		}
		region := byRange[merged]
		if region == nil {
			region = &tryRegion{StartPc: merged[0], EndPc: merged[1]}
			byRange[merged] = region
			guards[region] = guarded
			regions = append(regions, region)
		} else if guards[region] != guarded {
			// Two clauses over the same span that do not guard the same ranges
			// are not the same `try`; which handler catches what is no longer
			// the clause order.
			return nil, bail("clauses that guard different ranges")
		}
		region.Clauses = append(region.Clauses, &clause{Types: found.types, HandlerPc: handlerPc})
	}

	for _, region := range regions {
		for _, other := range regions {
			disjoint := other.EndPc <= region.StartPc || other.StartPc >= region.EndPc
			nested := (other.StartPc >= region.StartPc && other.EndPc <= region.EndPc) ||
				(region.StartPc >= other.StartPc && region.EndPc <= other.EndPc)
			if !disjoint && !nested {
				return nil, bail("overlapping try ranges")
			}
		}
	}
	return regions, nil
}

// reachableBlocks are the blocks reachable from the roots, which is all the
// structuring may cover.
func reachableBlocks(blocks map[int]*block, roots ...int) map[int]bool {
	seen := map[int]bool{}
	queue := append([]int{}, roots...)
	for len(queue) > 0 {
		at := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[at] || blocks[at] == nil {
			continue
		}
		seen[at] = true
		queue = append(queue, blocks[at].Successors...)
	}
	return seen
}

// postDominators reports the immediate post-dominator of every block: the point
// where the two arms of a branch come back together, and so where the `if` it
// was written as ends. Blocks whose paths all leave the method map to exitBlock.
//
// Inside a loop this is computed over the loop's blocks alone (within), with the
// edges that `break` and `continue` take cut: they leave the statement they sit
// in exactly the way a `return` does, and counting them would put the merge of
// every `if` in the body at the loop's own test.
func postDominators(blocks map[int]*block, within, cut map[int]bool) map[int]int {
	starts := make([]int, 0, len(blocks))
	for start := range blocks {
		if within == nil || within[start] {
			starts = append(starts, start)
		}
	}
	sort.Ints(starts)
	leaves := func(successor int) bool {
		return successor == exitBlock || cut[successor] || (within != nil && !within[successor])
	}
	// A back edge makes reverse pc order stop being a topological one, so this
	// is a fixpoint: every set starts full and shrinks until nothing moves.
	all := map[int]bool{exitBlock: true}
	for _, start := range starts {
		all[start] = true
	}
	sets := map[int]map[int]bool{}
	for _, start := range starts {
		sets[start] = copySet(all)
	}
	for changed := true; changed; {
		changed = false
		for i := len(starts) - 1; i >= 0; i-- {
			start := starts[i]
			successors := blocks[start].Successors
			if len(successors) == 0 {
				successors = []int{exitBlock}
			}
			var shared map[int]bool
			for _, successor := range successors {
				of := map[int]bool{exitBlock: true}
				if !leaves(successor) && sets[successor] != nil {
					of = sets[successor]
				}
				if shared == nil {
					shared = copySet(of)
					continue
				}
				for at := range shared {
					if !of[at] {
						delete(shared, at)
					}
				}
			}
			shared[start] = true
			if !sameSet(shared, sets[start]) {
				sets[start] = shared
				changed = true
			}
		}
	}
	immediate := map[int]int{}
	for _, start := range starts {
		immediate[start] = exitBlock
		candidates := make([]int, 0, len(sets[start]))
		for at := range sets[start] {
			if at != start && at != exitBlock {
				candidates = append(candidates, at)
			}
		}
		sort.Ints(candidates)
		// The nearest one: the one every other candidate post-dominates too.
		for _, at := range candidates {
			nearest := true
			for _, other := range candidates {
				if !sets[at][other] {
					nearest = false
					break
				}
			}
			if nearest {
				immediate[start] = at
				break
			}
		}
	}
	return immediate
}

func copySet(of map[int]bool) map[int]bool {
	out := make(map[int]bool, len(of))
	for at := range of {
		out[at] = true
	}
	return out
}

func sameSet(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for at := range a {
		if !b[at] {
			return false
		}
	}
	return true
}

// dominators reports which blocks every path from the entry to a block has to
// pass through.
func dominators(blocks map[int]*block, entry int) map[int]map[int]bool {
	starts := make([]int, 0, len(blocks))
	for start := range blocks {
		starts = append(starts, start)
	}
	sort.Ints(starts)
	predecessors := map[int][]int{}
	for _, start := range starts {
		for _, successor := range blocks[start].Successors {
			if blocks[successor] != nil {
				predecessors[successor] = append(predecessors[successor], start)
			}
		}
	}
	all := map[int]bool{}
	for _, start := range starts {
		all[start] = true
	}
	sets := map[int]map[int]bool{}
	for _, start := range starts {
		if start == entry {
			sets[start] = map[int]bool{entry: true}
			continue
		}
		sets[start] = copySet(all)
	}
	for changed := true; changed; {
		changed = false
		for _, start := range starts {
			if start == entry {
				continue
			}
			var shared map[int]bool
			for _, predecessor := range predecessors[start] {
				if shared == nil {
					shared = copySet(sets[predecessor])
					continue
				}
				for at := range shared {
					if !sets[predecessor][at] {
						delete(shared, at)
					}
				}
			}
			if shared == nil {
				shared = map[int]bool{}
			}
			shared[start] = true
			if !sameSet(shared, sets[start]) {
				sets[start] = shared
				changed = true
			}
		}
	}
	return sets
}

// loop is a loop, as the blocks it is made of and the two places control leaves
// it.
type loop struct {
	Header int
	// Body holds every block inside the loop, the header included.
	Body map[int]bool
	// Latches are the blocks whose branch closes the loop.
	Latches []int
	// Follow is where the code after the loop begins, or exitBlock when nothing
	// leaves it.
	Follow int
}

// retreatingEdges are the edges that close a cycle, found by a depth-first walk:
// an edge to a block the walk is still inside. pc order does not say this -
// javac lays a `while (a && b)` out with the second test jumping *backwards*
// into the body.
func retreatingEdges(blocks map[int]*block, entry int) [][2]int {
	type frame struct{ at, next int }
	var edges [][2]int
	open := map[int]bool{entry: true}
	done := map[int]bool{}
	stack := []frame{{at: entry}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		var successors []int
		if blocks[top.at] != nil {
			successors = blocks[top.at].Successors
		}
		if top.next >= len(successors) {
			delete(open, top.at)
			done[top.at] = true
			stack = stack[:len(stack)-1]
			continue
		}
		successor := successors[top.next]
		top.next++
		if blocks[successor] == nil || done[successor] {
			continue
		}
		if open[successor] {
			edges = append(edges, [2]int{top.at, successor})
			continue
		}
		open[successor] = true
		stack = append(stack, frame{at: successor})
	}
	return edges
}

// loopFollow reports where the code after the loop begins. The test decides it -
// the one at the head of a `while`, the one at the foot of a `do` - because a
// `break` leaves from a block that no longer reaches the latch, so it is not in
// the loop's body and its own target would otherwise look like a second way out.
func loopFollow(blocks map[int]*block, header int, latches []int, body map[int]bool, monitors []monitorRegion) (int, error) {
	// A `synchronized` *inside* the loop keeps its own blocks: the `return` javac
	// writes in there leaves the loop, but it is part of that statement and the
	// statement is what writes it. A loop inside a `synchronized` is the other way
	// round - then the range holds the whole body, and its blocks are the loop's.
	var held []monitorRegion
	for _, region := range monitors {
		inside, outsideRange := false, false
		for start := range body {
			if start >= region.StartPc && start < region.EndPc {
				inside = true
			} else {
				outsideRange = true
			}
		}
		if inside && outsideRange {
			held = append(held, region)
		}
	}
	inStatement := func(start int) bool {
		for _, region := range held {
			if start >= region.StartPc && start < region.EndPc {
				return true
			}
		}
		return false
	}
	outside := func(start int) []int {
		var out []int
		if blocks[start] == nil {
			return out
		}
		for _, successor := range blocks[start].Successors {
			if !body[successor] && !inStatement(successor) {
				out = append(out, successor)
			}
		}
		return out
	}
	// Only a header that is the test itself: one that carries statements - a call
	// among them - is the start of a `do`'s body, and what leaves it is a
	// `break`, not the loop's end.
	if fromHeader := outside(header); blocks[header].Kind == blockConditional &&
		isPureBlock(blocks[header]) && len(fromHeader) == 1 {
		return fromHeader[0], nil
	}
	if len(latches) == 1 {
		latch := blocks[latches[0]]
		if fromLatch := outside(latch.Start); latch.Kind == blockConditional && len(fromLatch) == 1 {
			return fromLatch[0], nil
		}
	}
	exits := map[int]bool{}
	for start := range body {
		for _, successor := range outside(start) {
			exits[successor] = true
		}
	}
	if len(exits) == 0 {
		return exitBlock, nil
	}
	if len(exits) == 1 {
		for exit := range exits {
			return exit, nil
		}
	}
	// Several ways out, none of them a test: the one they all reach ends the loop.
	candidates := make([]int, 0, len(exits))
	for exit := range exits {
		candidates = append(candidates, exit)
	}
	sort.Ints(candidates)
	for _, candidate := range candidates {
		merged := true
		for _, other := range candidates {
			if other != candidate && !reachableBlocks(blocks, other)[candidate] {
				merged = false
				break
			}
		}
		if merged {
			return candidate, nil
		}
	}
	// None of them reaches the others, so they are `return`s and the loop's own
	// end. Where the test is the header - which a single unconditional latch
	// says, a conditional one being the test of a `do` - what the header leaves
	// to is that end, and the rest are `return`s the body writes.
	if fromHeader := outside(header); len(latches) == 1 &&
		blocks[header].Kind == blockConditional && len(fromHeader) == 1 {
		if single, ok := blocks[latches[0]]; ok && single.Kind != blockConditional {
			return fromHeader[0], nil
		}
	}
	return 0, bail("a loop with more than one exit")
}

// findLoops reports the natural loops of the method, keyed by header. Every
// cycle has to be one: an edge that closes a cycle without its target dominating
// its source means two ways into the same loop, which is not something Java
// source can say.
// withExceptionEdges is the graph with an edge from every protected block to the
// handlers guarding it. A handler is only reachable by throwing, so without those
// edges it has no predecessor at all: its dominator set collapses to itself and
// poisons every block it flows into, which makes a loop holding a `try` look
// irreducible. Only the loop analysis wants them - a merge point does not, since
// an `if` inside a `try` still ends where its own arms come back together.
func withExceptionEdges(blocks map[int]*block, regions []*tryRegion, monitors []monitorRegion) map[int]*block {
	if len(regions) == 0 && len(monitors) == 0 {
		return blocks
	}
	augmented := make(map[int]*block, len(blocks))
	for start, b := range blocks {
		var handlers []int
		for _, region := range regions {
			if start < region.StartPc || start >= region.EndPc {
				continue
			}
			for _, c := range region.Clauses {
				if !containsInt(b.Successors, c.HandlerPc) && !containsInt(handlers, c.HandlerPc) {
					handlers = append(handlers, c.HandlerPc)
				}
			}
		}
		// A `synchronized` is guarded the same way, and a handler nothing reaches
		// has no dominators of its own - which poisons every block it flows into.
		for _, region := range monitors {
			if start < region.StartPc || start >= region.EndPc {
				continue
			}
			if !containsInt(b.Successors, region.HandlerPc) && !containsInt(handlers, region.HandlerPc) {
				handlers = append(handlers, region.HandlerPc)
			}
		}
		if len(handlers) == 0 {
			augmented[start] = b
			continue
		}
		copied := *b
		copied.Successors = append(append([]int{}, b.Successors...), handlers...)
		augmented[start] = &copied
	}
	return augmented
}

func findLoops(blocks map[int]*block, entry int, regions []*tryRegion, monitors []monitorRegion) (map[int]*loop, error) {
	// Everything that asks which blocks a loop is made of runs over the graph
	// with the throwing edges in it; where the loop *ends* is read off the real
	// one, where entering a handler is not a way out of the body.
	flow := withExceptionEdges(blocks, regions, monitors)
	retreating := retreatingEdges(flow, entry)
	loops := map[int]*loop{}
	if len(retreating) == 0 {
		return loops, nil
	}
	doms := dominators(flow, entry)
	headers := []int{}
	latchesOf := map[int][]int{}
	for _, edge := range retreating {
		from, to := edge[0], edge[1]
		if !doms[from][to] {
			return nil, bail("irreducible control flow")
		}
		if latchesOf[to] == nil {
			headers = append(headers, to)
		}
		latchesOf[to] = append(latchesOf[to], from)
	}
	sort.Ints(headers)
	predecessors := map[int][]int{}
	for start, b := range flow {
		for _, successor := range b.Successors {
			predecessors[successor] = append(predecessors[successor], start)
		}
	}
	for _, header := range headers {
		latches := latchesOf[header]
		// Everything that reaches a latch without leaving through the header.
		body := map[int]bool{header: true}
		queue := append([]int{}, latches...)
		for len(queue) > 0 {
			at := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			if body[at] {
				continue
			}
			body[at] = true
			queue = append(queue, predecessors[at]...)
		}
		follow, err := loopFollow(blocks, header, latches, body, monitors)
		if err != nil {
			return nil, err
		}
		loops[header] = &loop{Header: header, Body: body, Latches: latches, Follow: follow}
	}
	// Two loops are either nested or disjoint; anything else is one loop entered
	// at two places, which no `while` describes.
	for _, outer := range loops {
		for _, inner := range loops {
			if outer == inner {
				continue
			}
			shared := 0
			for start := range inner.Body {
				if outer.Body[start] {
					shared++
				}
			}
			if shared == 0 || shared == len(inner.Body) || shared == len(outer.Body) {
				continue
			}
			return nil, bail("overlapping loops")
		}
	}
	return loops, nil
}

// headerExits reports whether the test at the head of the loop is what leaves
// it - the `while (c)` shape, which javac writes with the test at the bottom and
// a `goto` into it. Only condition-only blocks count on the way there:
// `while (a && b)` is two of them, while a loop that leaves from inside its body
// is a `do` or a `for (;;)`.
func headerExits(blocks map[int]*block, l *loop) bool {
	seen := map[int]bool{}
	queue := []int{l.Header}
	for len(queue) > 0 {
		at := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[at] {
			continue
		}
		seen[at] = true
		b := blocks[at]
		if b == nil || b.Kind != blockConditional || !isConditionBlock(b) {
			continue
		}
		for _, successor := range b.Successors {
			if successor == l.Follow {
				return true
			}
		}
		for _, successor := range b.Successors {
			if l.Body[successor] {
				queue = append(queue, successor)
			}
		}
	}
	return false
}

// activeLoop is a loop being written right now.
type activeLoop struct {
	Loop *loop
	// ContinueTarget is where `continue;` goes: the test, which a `do` keeps at
	// the bottom.
	ContinueTarget int
	// Continues says whether jumping to ContinueTarget really is `continue;`. A
	// `do`'s latch is the test *and* the tail of the body when nothing else jumps
	// to it - and `continue;` skips that tail, so a jump there is not one.
	Continues bool
	// Label names the loop once a jump from a loop inside it leaves or
	// continues it: `break label;` needs a `label:` on the header, which is
	// written after the body and so can still take it.
	Label string
}

// activeSwitch is a `switch` statement being written right now.
type activeSwitch struct {
	// Follow is where `break;` goes: the end of the statement.
	Follow int
	// LoopDepth is how many loops were being written when this `switch` was
	// entered. It is the innermost breakable statement only while that is still
	// true - a loop opened inside it takes every unlabeled `break` for itself.
	LoopDepth int
}

// pureMnemonics are the instructions a *condition* may be built from: no store,
// no call, nothing that is a statement. A block made only of these can be folded
// into the condition of the branch before it (`a && b`) or into a ternary
// without changing what runs.
// literalFits reports whether an int literal is a value of a boolean, char,
// byte or short.
func literalFits(text, typ string) bool {
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return false
	}
	switch typ {
	case "boolean":
		return n == 0 || n == 1
	case "char":
		return n >= 0 && n <= 0xFFFF
	case "byte":
		return n >= -128 && n <= 127
	case "short":
		return n >= -32768 && n <= 32767
	}
	return false
}

// allLiteralsFit reports whether every value stored into the variable so far
// was a literal of typ - or, for a boolean, a condition javac materialized.
func allLiteralsFit(entry *local, typ string) bool {
	for _, write := range entry.Writes {
		if typ == "boolean" && write.Value.AsInt != "" {
			continue
		}
		if !literalFits(write.Value.Text, typ) {
			return false
		}
	}
	return true
}

// onlyNull reports whether every value stored into the variable so far was
// `null` - and there was one: a parameter, never stored, is not.
func onlyNull(entry *local) bool {
	for _, write := range entry.Writes {
		if write.Value.Text != "null" {
			return false
		}
	}
	return len(entry.Writes) > 0
}

var primitiveTypeNames = map[string]bool{
	"boolean": true, "byte": true, "char": true, "short": true,
	"int": true, "long": true, "float": true, "double": true,
}

// allocations are the instructions that make an object or an array: an effect,
// but one a block may carry into a condition or a ternary arm.
var allocations = map[string]bool{"new": true, "newarray": true, "anewarray": true, "multianewarray": true}

var pureMnemonics = regexp.MustCompile(`^(?:nop|aconst_null|[ilfd]const_\w+|bipush|sipush|ldc\w*|` +
	`[ilfda]load(?:_\d|_w)?|arraylength|[ilfdabcs]aload|` +
	`[ilfd](?:add|sub|mul|div|rem|neg|shl|shr|ushr|and|or|xor)|[ilfd]2[ilfdbcs]|` +
	`lcmp|[fd]cmp[lg]|getstatic|getfield|checkcast|instanceof|dup)$`)

// isConditionBlock reports whether a condition may be folded from a block. Like
// isPureBlock, but a call or an allocation is allowed: folding runs it exactly
// once and in the same place, which is not true of the ternary arms isPureBlock
// guards - those can be evaluated twice.
func isConditionBlock(b *block) bool {
	for i, instruction := range b.Instructions {
		if pureMnemonics.MatchString(instruction.Mnemonic) || invokes[instruction.Mnemonic] ||
			instruction.Mnemonic == "invokedynamic" || allocations[instruction.Mnemonic] {
			continue
		}
		last := i == len(b.Instructions)-1
		if last && (conditionalBranches[instruction.Mnemonic] || isGotoMnemonic(instruction.Mnemonic)) {
			continue
		}
		return false
	}
	return true
}

func isPureBlock(b *block) bool {
	for i, instruction := range b.Instructions {
		if pureMnemonics.MatchString(instruction.Mnemonic) {
			continue
		}
		last := i == len(b.Instructions)-1
		if last && (conditionalBranches[instruction.Mnemonic] || isGotoMnemonic(instruction.Mnemonic)) {
			continue
		}
		return false
	}
	return true
}

// endOf reports where the code after a block's body begins - the pc a store at
// the end of it has to look its variable's scope up at. A branch ends at its
// terminator; a block that falls through ends where the next one starts.
func endOf(b *block) int {
	if b.Kind == blockFall {
		return b.Successors[0]
	}
	return b.Instructions[len(b.Instructions)-1].Pc
}

type bodyDecompiler struct {
	classFile  *ClassFile
	locals     map[int]*local
	localTable []localEntry
	returnType string
	isStatic   bool
	stack      []expr
	statements []stmt
	// sharedIDs numbers the copies a compound assignment's dup made.
	sharedIDs int
	// labels numbers the loop labels handed out.
	labels int
	// assignFieldAsValue is set by a `dup_x1` into a `putfield` (or a `dup`
	// into a `putstatic`): the field assignment is the value, not a statement.
	assignFieldAsValue bool
	// assignAsValue is set by a `dup` into a store: the store is the value, not
	// a statement.
	assignAsValue bool
	// hoisted are declarations of locals first stored inside a branch. Java
	// scopes them to that branch, the bytecode does not, so they are declared up
	// front and the store becomes an assignment; methodSource puts these first.
	//
	// ponytail: hoisting to the top of the method, not to the innermost block
	// that encloses every use - that is the upgrade path if the output reads
	// badly.
	hoisted []stmt
	// current is where statements are being appended right now: a branch's arm,
	// or the body.
	current *[]stmt
	depth   int
	// chained is the `super(...)`/`this(...)` statement a constructor opened
	// with, as written - the one thing hoisting keeps in front.
	chained string
	// arms is every statement block a branch or loop has captured: it may not
	// be in statements yet (the then-arm while the else-arm runs), and once it
	// is, it is there twice, which a scan does not mind.
	arms []*[]stmt
	// names is every local name handed out so far, so a reused slot cannot
	// shadow one.
	names    map[string]bool
	byName   map[string]*local
	blocks   map[int]*block
	followOf map[int]int
	loops    map[int]*loop
	regions  []*tryRegion
	// activeTries are the `try` statements being written right now.
	activeTries map[*tryRegion]bool
	// skip counts instructions to drop from the front of a block: a handler
	// starts with the store of the exception, which source writes as the catch
	// parameter.
	skip map[int]int
	// methodFollowOf is followOf over the whole method, which followOf itself is
	// not inside a loop.
	methodFollowOf map[int]int
	// dominators over the graph the throwing edges are in, for a `try`'s own end.
	dominators map[int]map[int]bool
	// active are the loops being written right now, innermost last.
	active []activeLoop
	// switches are the `switch` statements being written right now, innermost last.
	switches []activeSwitch
	// monitors are the `synchronized` statements of this method, and the slots
	// they hold; instructionAt is every instruction by pc, for the ones a
	// statement has to look up.
	monitors []monitorRegion
	// finallys are the `finally` statements of this method, and activeFinallys
	// the ones being written right now.
	finallys       []finallyRegion
	activeFinallys map[*finallyRegion]bool
	// monitorSlots are the slots held by the `synchronized` statements being
	// written right now. javac frees the monitor's local when the statement ends
	// and reuses the slot for the next variable, so this may not outlive the body
	// it belongs to.
	monitorSlots  []int
	instructionAt map[int]Instruction
	// captured are the locals a lambda captured. Java takes only effectively
	// final ones, and a variable this hoisted to the top of the method may be
	// written more than once - which is only known when the whole body is out.
	captured []*local
	// inlining are the lambda bodies being inlined right now, so one cannot
	// inline itself.
	inlining map[string]bool
	visited  map[int]bool
	// pendingCount hands out the ids that tell the copies of one `new` apart.
	pendingCount int
	// initCount does the same for the copies of one array literal.
	initCount int
	// innerFlags are the access flags of the nested classes this file names.
	innerFlags map[string]uint16
	// bootstraps is the BootstrapMethods table, which every invokedynamic indexes into.
	bootstraps []BootstrapMethod
	// conditions are every `if (...)` line emitted, with the condition it came
	// from: a local's type can still narrow after the line is written (the use
	// that proves it is a boolean may come later), and the text has to follow.
	conditions   []emittedCondition
	entryPc      int
	currentBlock int
}

// self is the class being decompiled, as its references have to spell it.
func (d *bodyDecompiler) self() string { return selfOf(d.classFile) }

// coerceInto renders value as it has to read in a target-typed position. A local
// the bytecode only says is an int, used where a boolean/char/byte/short
// belongs, *is* one - the store opcode is the same for all of them - so its
// declaration and every assignment to it are rewritten to that type.
func (d *bodyDecompiler) coerceInto(value expr, target string) (string, error) {
	// A lambda takes its type from where it is written; when that is not the
	// interface itself, source had to say which one it is.
	if value.Lambda && target != value.Type {
		return "(" + value.Type + ") " + value.Text, nil
	}
	// A bare variable, or an assignment to one used as a value - `return b =
	// true` - where the variable's type was only inferred and this use narrows
	// it. For the assignment that can only be a refusal: its text is frozen.
	name := value.Text
	if m := assignedName.FindStringSubmatch(value.Text); m != nil {
		name = m[1]
	}
	if entry, ok := d.byName[name]; ok && !entry.Authoritative &&
		entry.Type == "int" && erasedToInt[target] {
		if err := d.retype(entry, target); err != nil {
			return "", err
		}
	}
	// A variable that has only held `null` is of the type its first use asks
	// for - javac chose that overload, return or field by the declared type.
	// A read before this one was rendered against `Object` and is text; the
	// variable stays an Object then.
	// An open type is asked for by any use: a read before that was a bare name,
	// which reads the same whatever the type turns out to be.
	if entry, ok := d.byName[name]; ok && !entry.Authoritative && name == value.Text &&
		entry.Type == "java.lang.Object" && target != "java.lang.Object" && !primitiveTypeNames[target] &&
		(onlyNull(entry) && entry.Reads <= 1 || entry.Open) {
		if err := d.settle(entry, target, entry.Open); err != nil {
			return "", err
		}
		value = primary(entry.Name, entry.Type)
	} else if ok && entry.Tentative && name == value.Text && target != entry.Type && target != "java.lang.Object" &&
		!primitiveTypeNames[target] {
		return "", bail("a variable whose uses ask for different types")
	} else if ok && entry.Open && name == value.Text && target == "java.lang.Object" {
		// An Object is asked for: an Object it is, until a member says better.
		if err := d.settle(entry, "", true); err != nil {
			return "", err
		}
	}
	// A conditional's arm that is such a variable is asked for the same.
	if value.Arms != nil && !primitiveTypeNames[target] {
		for _, arm := range value.Arms {
			if entry, ok := d.byName[arm.Text]; ok && entry.Open {
				typ := ""
				if target != "java.lang.Object" {
					typ = target
					value.Type = target
				}
				if err := d.settle(entry, typ, true); err != nil {
					return "", err
				}
			}
		}
	}
	// Where a number belongs, the value is used as one.
	if numericTargets[target] {
		if err := d.usedAsNumber(value); err != nil {
			return "", err
		}
	}
	return coerce(value, target), nil
}

// retype gives a local a narrower type, rewriting its declaration and assignments.
func (d *bodyDecompiler) retype(entry *local, target string) error {
	// An assignment written inside an expression was coerced to the type the
	// variable had then, and its text is already part of a bigger one: nothing
	// here can reach in and change it. That holds for the variable it assigns,
	// and for any variable whose own value has such an assignment inside it.
	for _, write := range entry.Writes {
		if write.InValue || embedsAssignment(write.Value.Text) {
			return bail("a retyped assignment used as a value")
		}
	}
	// A boolean cannot be incremented, ordered or used as an index: a variable
	// that was is an int whose slot a boolean took over afterwards, which one
	// name cannot carry.
	if target == "boolean" && entry.Numeric {
		return bail("a variable used as both a number and a boolean")
	}
	// A value that is an int and nothing else - a call's result, arithmetic -
	// cannot be a char's, byte's or boolean's: a variable that holds one and is
	// used as the narrower type is two variables in one slot, which one name
	// cannot carry.
	if erasedToInt[target] && entry.Type == "int" {
		for _, write := range entry.Writes {
			value := write.Value
			_, bareLocal := d.byName[value.Text]
			if value.Type == target || literalFits(value.Text, target) || value.AsInt != "" || bareLocal {
				continue
			}
			if value.Type == "int" {
				return bail("a variable used as both an int and a %s", target)
			}
		}
	}
	entry.Type = target
	declaration := entry.Declaration
	if declaration != nil && !declaration.Inline {
		(*declaration.List)[declaration.Index] = stmt{Text: target + " " + entry.Name + ";"}
	}
	for i, write := range entry.Writes {
		// A condition stored is rendered against the types its locals have now,
		// like one that was branched on: `w = !w` reads `w` on both sides. Only
		// for a boolean: a char or byte keeps the int form the value carries.
		value := write.Value
		if target == "boolean" && value.Logic != nil {
			value = d.renderCondition(value)
		}
		assigned := coerce(value, target)
		text := entry.Name + " = " + assigned + ";"
		if i == 0 && declaration != nil && declaration.Inline {
			text = target + " " + entry.Name + " = " + assigned + ";"
		}
		(*write.List)[write.Index] = stmt{Text: text}
	}
	for _, emitted := range d.conditions {
		(*emitted.List)[emitted.Index] = stmt{Text: emitted.Wrap(d.renderCondition(emitted.Condition).Text)}
	}
	// A condition written into anything else - an argument, an operand, a
	// field - was rendered against the old type and is text now, `x == 0` or
	// `x != 0 ? 1 : 0` for what has become a boolean: there is nothing left that
	// can reach in and rewrite it.
	if target == "boolean" && d.frozenCondition(entry.Name) {
		return bail("a retyped variable in a condition already written")
	}
	return nil
}

// frozenCondition reports whether name is still compared with a number
// somewhere the rewrite cannot reach.
func (d *bodyDecompiler) frozenCondition(name string) bool {
	quoted := regexp.QuoteMeta(name)
	pattern := regexp.MustCompile(`(^|[^\w$.])` + quoted + ` [!=]= \d|\d [!=]= ` + quoted + `($|[^\w$])`)
	texts := flattenStatements(d.statements)
	for _, arm := range d.arms {
		texts = append(texts, flattenStatements(*arm)...)
	}
	for _, value := range d.stack {
		texts = append(texts, value.Text, value.AsInt)
	}
	for _, text := range texts {
		if pattern.MatchString(withoutLiterals(text)) {
			return true
		}
	}
	return false
}

// emittedCondition is an `if (...)` line and the condition it was rendered from.
type emittedCondition struct {
	List      *[]stmt
	Index     int
	Condition expr
	Wrap      func(text string) string
}

// emitCondition writes a condition as a line, kept re-renderable for as long as
// a local can retype.
func (d *bodyDecompiler) emitCondition(condition expr, wrap func(text string) string) {
	d.conditions = append(d.conditions,
		emittedCondition{List: d.current, Index: len(*d.current), Condition: condition, Wrap: wrap})
	*d.current = append(*d.current, stmt{Text: wrap(d.renderCondition(condition).Text)})
}

func ifWrap(text string) string { return "if (" + text + ") {" }

func whileWrap(text string) string { return "while (" + text + ") {" }

func doWhileWrap(text string) string { return "} while (" + text + ");" }

// renderCondition renders a condition against the types its locals are known to
// have *now*. `ifeq` on a local is how both `if (!b)` and `if (x == 0)` are
// compiled, so the comparison is written against an int until something proves
// the variable is a boolean - and then this rewrites it.
func (d *bodyDecompiler) renderCondition(condition expr) expr {
	logic := condition.Logic
	if logic == nil {
		return condition
	}
	switch logic.Kind {
	case logicAnd, logicOr:
		return logicalExpr(logic.Kind, d.renderCondition(*logic.Left), d.renderCondition(*logic.Right))
	case logicNot:
		return notExpr(d.renderCondition(*logic.Left))
	default:
		// The local may sit on either side (`true == w` puts it right).
		left, right := *logic.Left, *logic.Right
		changed := false
		if entry, ok := d.byName[left.Text]; ok && entry.Type != left.Type {
			left, changed = primary(entry.Name, entry.Type), true
		}
		if entry, ok := d.byName[right.Text]; ok && entry.Type != right.Type {
			right, changed = primary(entry.Name, entry.Type), true
		}
		if !changed {
			return condition
		}
		// A boolean tested against `0` is the boolean (or its negation); against
		// any other literal, the literal is written as the type it now has.
		if left.Type == "boolean" && right.Text == "0" || right.Type == "boolean" && left.Text == "0" {
			value := left
			if right.Type == "boolean" {
				value = right
			}
			if logic.Op == "!=" {
				return value
			}
			if logic.Op == "==" {
				return notExpr(value)
			}
		}
		if isIntegerText(right.Text) && right.Type == "int" {
			right = primary(coerce(right, left.Type), left.Type)
		} else if isIntegerText(left.Text) && left.Type == "int" {
			left = primary(coerce(left, right.Type), right.Type)
		}
		return compareExpr(left, logic.Op, right)
	}
}

// reads reports whether text reads name - a variable or a field reference,
// matched whole so a longer name that contains it does not count.
func reads(text, name string) bool {
	if !strings.Contains(text, name) {
		return false
	}
	// Not `\b`: a name may end in a bracket (`a[i]`), where there is no word
	// boundary at all. A leading `.` is excluded so a field does not match a local
	// of the same name.
	return regexp.MustCompile(`(^|[^\w$.])` + regexp.QuoteMeta(name) + `($|[^\w$])`).MatchString(text)
}

// emit appends one statement where statements are currently going.
func (d *bodyDecompiler) emit(text string) {
	*d.current = append(*d.current, stmt{Text: text})
}

// capture collects the statements run appends as a nested block rather than
// into the body.
func (d *bodyDecompiler) capture(run func() error) ([]stmt, error) {
	outer := d.current
	captured := []stmt{}
	d.current = &captured
	d.arms = append(d.arms, &captured)
	d.depth++
	err := run()
	d.current = outer
	d.depth--
	return captured, err
}

// staticRef writes a static field of this class with its simple name: that is
// what source used, and a blank `static final` can only be *assigned* that way.
// A local of the same name (declared before this point) shadows it, so then the
// owner has to stay.
func (d *bodyDecompiler) staticRef(owner, name string) string {
	if owner == d.classFile.ThisClass && !d.names[name] {
		return name
	}
	return typeName(owner, d.self()) + "." + name
}

func (d *bodyDecompiler) push(e expr) { d.stack = append(d.stack, e) }

func (d *bodyDecompiler) popRaw() (expr, error) {
	if len(d.stack) == 0 {
		return expr{}, bail("stack underflow")
	}
	top := d.stack[len(d.stack)-1]
	d.stack = d.stack[:len(d.stack)-1]
	return top, nil
}

func (d *bodyDecompiler) pop() (expr, error) {
	top, err := d.popRaw()
	if err != nil {
		return expr{}, err
	}
	// An `lcmp` result has no source form of its own (`Long.compare` is a call,
	// which is a later phase), so only the branch that follows may consume it.
	if top.Compared != nil {
		return expr{}, bail("a comparison outside a branch")
	}
	// A half-written array literal has no source form: the elements already
	// consumed are gone from the statement list.
	if top.Init != nil && len(top.Init.Elements) > 0 {
		return expr{}, bail("incomplete array initializer")
	}
	// A copy that may only be written once is consumed by the read and the
	// write of a compound assignment, which take it raw.
	if top.Shared != 0 {
		return expr{}, bail("dup of a non-trivial value")
	}
	return top, nil
}

// arrayInitOf reports the literal a fresh array may still turn into. Only a
// constant length can: `{1, 2, 3}` is the only source that duplicates a new
// array and writes through the copies, and its length is the element count.
func (d *bodyDecompiler) arrayInitOf(prefix, element string, length expr) *arrayInit {
	size, err := strconv.Atoi(length.Text)
	if err != nil || size < 0 {
		return nil
	}
	d.initCount++
	return &arrayInit{ID: d.initCount, Prefix: prefix, Element: element, Length: size}
}

// fillArray takes one `dup; index; value; Xastore` of an array initializer. The
// store consumed one copy of the array; the copy left below it is the same
// array, so it is replaced by the literal grown by this element - and by the
// finished literal once the last one lands.
func (d *bodyDecompiler) fillArray(init *arrayInit, index, value expr) error {
	// javac writes an initializer front to back, one element per index.
	if index.Text != strconv.Itoa(len(init.Elements)) || len(init.Elements) >= init.Length {
		return bail("array initializer out of order")
	}
	below, err := d.popRaw()
	if err != nil {
		return err
	}
	if below.Init == nil || below.Init.ID != init.ID {
		return bail("array initializer copy lost")
	}
	element, err := d.coerceInto(value, init.Element)
	if err != nil {
		return err
	}
	elements := append(append([]string{}, init.Elements...), element)
	if len(elements) == init.Length {
		d.push(primary(init.Prefix+"{"+strings.Join(elements, ", ")+"}", below.Type))
		return nil
	}
	grown := *init
	grown.Elements = elements
	below.Init = &grown
	d.push(below)
	return nil
}

// local reports the variable living in slot at pc. javac reuses a slot for the
// next variable once the previous one goes out of scope, so a slot is not a
// variable: a new debug-table scope - or, with no debug table, a store of a
// different type - starts a new one, which has to be declared under its own name.
func (d *bodyDecompiler) local(slot, pc int, fallbackType string, isStore bool) (*local, error) {
	var scoped *localEntry
	for i := range d.localTable {
		if e := &d.localTable[i]; e.Slot == slot && pc >= e.StartPc && pc < e.EndPc {
			scoped = e
			break
		}
	}
	if existing, ok := d.locals[slot]; ok {
		switch {
		case scoped != nil:
			// javac writes one row per scope range, so the same variable can
			// appear twice for one slot (once per arm of an `if`); the name and
			// type are what say it is the same one.
			origin := existing.Origin
			if origin == scoped ||
				(origin != nil && origin.Name == scoped.Name && origin.Type == scoped.Type) {
				return existing, nil
			}
		case isStore && existing.Origin != nil:
			// The debug table scoped the variable in this slot, and scopes
			// nothing here: its range is over, and this store begins a variable
			// source never named - a for-each's array copy or index, say - or is
			// a dead one past the variable's last use, which javac keeps no row
			// for. Either way it is not the old one.
		case !isStore || existing.Authoritative || existing.Type == fallbackType:
			// Without a debug table a slot is only a variable as long as one
			// definition explains every path to here: two arms that stored
			// differently-typed values were split into two variables, and which
			// one this reads is not something the bytecode still says.
			if !isStore && len(existing.StoreBlocks) > 0 &&
				d.reachesAvoiding(d.currentBlock, existing.StoreBlocks) {
				return nil, bail("local %d is written in more than one branch", slot)
			}
			return existing, nil
		}
	}
	// Reaching a slot that was never stored means the local is read
	// uninitialized - javac cannot produce that, so the input is doing something
	// this phase does not model.
	if !isStore {
		return nil, bail("local %d is read before it is written", slot)
	}
	wanted := "var" + strconv.Itoa(slot)
	if scoped != nil && scoped.Name != "" {
		wanted = scoped.Name
	}
	declared := fallbackType
	authoritative := false
	if scoped != nil && scoped.Type != "" {
		declared = scoped.Type
		authoritative = true
	} else if declared == "" {
		return nil, bail("a conditional whose arms differ in type")
	}
	created := &local{
		Name:          d.freshName(wanted),
		Type:          sourceTypeText(declared, d.self()),
		Origin:        scoped,
		Authoritative: authoritative,
		StoreBlocks:   map[int]bool{},
	}
	d.locals[slot] = created
	d.byName[created.Name] = created
	return created, nil
}

// callArguments pops a call's arguments, coerced to the parameter types of
// its descriptor. A literal `null` where `Object` is declared is cast, except
// on a call whose receiver is itself a call: that receiver has the
// parameterized type the real class gives it, and a generic `T` there erases
// to `Object` in the descriptor but is not one to javac.
func (d *bodyDecompiler) callArguments(descriptor string) ([]string, error) {
	params := parameterSlots(descriptor, true)
	receiverIsCall := false
	if at := len(d.stack) - len(params) - 1; at >= 0 {
		receiver := d.stack[at]
		receiverIsCall = receiver.Effects && receiver.Pending == 0
	}
	args := make([]string, len(params))
	for i := len(params) - 1; i >= 0; i-- {
		value, err := d.pop()
		if err != nil {
			return nil, err
		}
		target := params[i].Type
		text, err := d.coerceInto(value, target)
		if err != nil {
			return nil, err
		}
		// Java narrows an int constant implicitly when it is assigned, but never
		// when it is passed: `f((byte) 3)` is the only way to write the call.
		narrows := (target == "byte" || target == "short") && intLiteralText.MatchString(text)
		// A bare `null` where an `Object` is declared re-resolves the overload -
		// `String.valueOf(null)` binds `char[]` and throws - so the type the call
		// was compiled against is written back. Only `Object` gets it: a cast to
		// any other reference type is a `checkcast` the original did not have.
		//
		// ponytail: that leaves a narrower hole - a parameter typed `CharSequence`
		// with a `String` overload alongside it also re-resolves. Closing it needs
		// the callee's other overloads, which live outside this class file.
		if narrows || (text == "null" && target == "java.lang.Object" && !receiverIsCall) {
			text = "(" + sourceTypeText(target, d.self()) + ") " + text
		}
		args[i] = text
	}
	return args, nil
}

// intLiteralText matches a rendered int constant, which is what may need a cast.
var intLiteralText = regexp.MustCompile(`^-?\d+$`)

// noSpaceText matches a rendered value that is a single token: a name or a literal.
var noSpaceText = regexp.MustCompile(`^\S+$`)

// coercedExpr is value where a target-typed value belongs, kept an expression so
// the caller can still parenthesize it.
func (d *bodyDecompiler) coercedExpr(value expr, target string) (expr, error) {
	text, err := d.coerceInto(value, target)
	if err != nil {
		return expr{}, err
	}
	if text == value.Text {
		return value, nil
	}
	// What the rewrite produces is a literal (`true`, `'a'`) or something with an
	// operator in it (`(char) 200`, a materialized boolean's own ternary); the
	// latter gets the lowest precedence, so it is parenthesized wherever it lands
	// rather than needing its real level worked out.
	prec := 0
	if noSpaceText.MatchString(text) {
		prec = precPrimary
	}
	return expr{Text: text, Prec: prec, Type: target}, nil
}

// concat reconstructs a string concatenation, which javac compiles to an
// invokedynamic whose bootstrap is StringConcatFactory.makeConcatWithConstants:
// the recipe (its first bootstrap argument) is the result text with \u0001 where
// a stack argument goes and \u0002 where one of the remaining bootstrap
// constants does. makeConcat is the same call with no literal parts at all.
// dynamic writes one invokedynamic. javac writes two of them: a string
// concatenation, and the lambda or method reference LambdaMetafactory builds.
func (d *bodyDecompiler) dynamic(index uint16) error {
	pool := d.classFile.Pool
	entry := PoolAt(pool, index)
	if entry == nil || entry.Tag != TagInvokeDynamic {
		return bail("bad invokedynamic reference")
	}
	_, siteDescriptor, haveSite := PoolNameAndType(pool, entry.NameAndType)
	var factory MemberRef
	haveFactory := false
	var bootstrap BootstrapMethod
	haveBootstrap := int(entry.Bootstrap) < len(d.bootstraps)
	if haveBootstrap {
		bootstrap = d.bootstraps[entry.Bootstrap]
		if handle := PoolAt(pool, bootstrap.HandleIndex); handle != nil && handle.Tag == TagMethodHandle {
			factory, haveFactory = PoolMemberRef(pool, handle.RefIndex)
		}
	}
	if !haveSite || !haveBootstrap || !haveFactory {
		return bail("bad invokedynamic reference")
	}
	if factory.Owner == "java/lang/invoke/StringConcatFactory" {
		return d.concat(siteDescriptor, bootstrap, factory)
	}
	// `altMetafactory` carries flags of its own - a serializable lambda, extra
	// interfaces, extra bridges - and dropping them would change what the class
	// implements.
	if factory.Owner == "java/lang/invoke/LambdaMetafactory" && factory.Name == "metafactory" {
		return d.lambda(siteDescriptor, bootstrap)
	}
	return bail("an invokedynamic that is neither a lambda nor a concatenation")
}

// lambda writes a lambda or a method reference: `LambdaMetafactory.metafactory`
// is handed the interface method's type, the method javac compiled the body
// into, and the type it is instantiated at; the call site takes the captured
// values and returns the interface. A body javac generated is inlined, which is
// the lambda source wrote; a method reference points at a method that was
// already there.
func (d *bodyDecompiler) lambda(siteDescriptor string, bootstrap BootstrapMethod) error {
	pool := d.classFile.Pool
	if len(bootstrap.ArgumentIndexes) < 3 {
		return bail("a lambda without an implementation")
	}
	samType := PoolAt(pool, bootstrap.ArgumentIndexes[0])
	handle := PoolAt(pool, bootstrap.ArgumentIndexes[1])
	instantiated := PoolAt(pool, bootstrap.ArgumentIndexes[2])
	if samType == nil || samType.Tag != TagMethodType || handle == nil ||
		handle.Tag != TagMethodHandle || instantiated == nil || instantiated.Tag != TagMethodType {
		return bail("a lambda without an implementation")
	}
	sam := PoolUtf8(pool, samType.Index)
	exact := PoolUtf8(pool, instantiated.Index)
	target, okTarget := PoolMemberRef(pool, handle.RefIndex)
	if sam == "" || exact == "" || !okTarget {
		return bail("a lambda without an implementation")
	}
	// The captured values are the call site's arguments; the interface method's
	// own parameters are what the lambda has to name.
	captureTypes := parameterSlots(siteDescriptor, true)
	captures := make([]string, len(captureTypes))
	for i := len(captureTypes) - 1; i >= 0; i-- {
		value, err := d.pop()
		if err != nil {
			return err
		}
		capture, err := d.coerceInto(value, captureTypes[i].Type)
		if err != nil {
			return err
		}
		captures[i] = capture
		entry, isLocal := d.byName[captures[i]]
		if isLocal {
			d.captured = append(d.captured, entry)
		}
		// javac evaluates a captured value *here* and hands it over; the lambda
		// this writes reads the text again every time it runs. That is the same
		// value only for a variable - a field or an array element can change, and
		// `name::toUpperCase` would then upper-case whatever the field holds later.
		if !isLocal && captures[i] != "this" && !isConstantText(captures[i]) {
			return bail("a lambda that captures more than a variable")
		}
	}
	// A parameter the interface passes as its erased type is cast at the use,
	// which is what the body javac generated does.
	erased := parameterSlots(sam, true)
	wanted := parameterSlots(exact, true)
	if len(erased) != len(wanted) {
		return bail("a lambda that binds differently")
	}
	parameters := make([]string, len(erased))
	passed := append([]string{}, captures...)
	for i := range erased {
		parameters[i] = d.freshName("p")
		if erased[i].Type == wanted[i].Type {
			passed = append(passed, parameters[i])
		} else {
			passed = append(passed, "("+wanted[i].Type+") "+parameters[i])
		}
	}
	// What kind of call the handle is decides both forms below; an unknown one is
	// not a call at all.
	switch handle.RefKind {
	case 5, 6, 7, 8, 9:
	default:
		return bail("a lambda that is not a call")
	}
	sourceType := methodReturnType(siteDescriptor)
	// A body javac generated is the lambda source wrote: inlining it is what
	// brings that source back, and javac generates the same method from it.
	if target.Owner == d.classFile.ThisClass {
		for _, method := range d.classFile.Methods {
			if method.Name != target.Name || method.Descriptor != target.Descriptor ||
				method.Flags&accSynthetic == 0 {
				continue
			}
			text, err := d.inlineLambda(method, captures, passed, methodReturnType(sam))
			if err != nil {
				return err
			}
			d.push(expr{
				Text:   "(" + strings.Join(parameters, ", ") + ") -> " + text,
				Prec:   0,
				Type:   sourceType,
				Lambda: true,
			})
			return nil
		}
	}
	implParams := len(parameterSlots(target.Descriptor, true))
	var call string
	switch handle.RefKind {
	case 6: // REF_invokeStatic
		if len(passed) != implParams {
			return bail("a lambda that binds differently")
		}
		call = d.staticCallee(target.Owner, target.Name) + "(" + strings.Join(passed, ", ") + ")"
	case 8: // REF_newInvokeSpecial
		if len(passed) != implParams {
			return bail("a lambda that binds differently")
		}
		call = "new " + typeName(target.Owner, d.self()) + "(" + strings.Join(passed, ", ") + ")"
	case 5, 7, 9:
		// The receiver is the captured value, or - for an unbound reference like
		// `String::length` - the first parameter.
		if len(passed) != implParams+1 {
			return bail("a lambda that binds differently")
		}
		receiver := passed[0]
		if strings.HasPrefix(receiver, "(") {
			receiver = "(" + receiver + ")"
		}
		call = receiver + "." + target.Name + "(" + strings.Join(passed[1:], ", ") + ")"
	default:
		return bail("a lambda that is not a call")
	}
	// With nothing captured and nothing to cast, source's own form is a method
	// reference - and that is what javac compiles back to this same call site.
	reference := len(captures) == 0
	for i := range parameters {
		if passed[i] != parameters[i] {
			reference = false
		}
	}
	if reference {
		written := typeName(target.Owner, d.self()) + "::" + target.Name
		if handle.RefKind == 8 {
			written = typeName(target.Owner, d.self()) + "::new"
		}
		// A method reference takes its type from where it is written, exactly as
		// a lambda does, so it needs the interface named in the same places.
		d.push(expr{Text: written, Prec: precPrimary, Type: sourceType, Lambda: true})
		return nil
	}
	d.push(expr{
		Text:   "(" + strings.Join(parameters, ", ") + ") -> " + call,
		Prec:   0,
		Type:   sourceType,
		Lambda: true,
	})
	return nil
}

// inlineLambda is the body of a lambda, as the expression or block source wrote:
// the method javac generated is decompiled with its parameters bound to what the
// call site captured and to the lambda's own parameters.
func (d *bodyDecompiler) inlineLambda(body Member, captures, passed []string, yields string) (string, error) {
	key := body.Name + body.Descriptor
	if d.inlining[key] {
		return "", bail("a lambda that inlines itself")
	}
	isStatic := body.Flags&accStatic != 0
	code, err := ReadCode(body, d.classFile.Pool)
	if err != nil || code == nil {
		return "", bail("a lambda without a body")
	}
	// A cast binds looser than a member access, so a bound value that is not a
	// plain name is parenthesized where the body reads it.
	bound := make([]string, 0, len(passed))
	for _, text := range passed {
		if plainName.MatchString(text) {
			bound = append(bound, text)
		} else {
			bound = append(bound, "("+text+")")
		}
	}
	if !isStatic {
		// An instance body reads the enclosing object as `this`, which is what
		// the call site captured first.
		if len(captures) == 0 || captures[0] != "this" {
			return "", bail("a lambda on another object")
		}
		bound = bound[1:]
	}
	instructions, err := DecodeInstructions(d.classFile, code.Code)
	if err != nil {
		return "", bail("a lambda without a body")
	}
	localTable := readLocalVariables(code, d.classFile.Pool)
	locals := buildLocals(body, localTable, isStatic, d.self())
	slots := parameterSlots(body.Descriptor, isStatic)
	if len(slots) != len(bound) {
		return "", bail("a lambda that binds differently")
	}
	for i, slot := range slots {
		if entry, ok := locals[slot.Slot]; ok {
			entry.Name = bound[i]
		}
	}
	nested := &bodyDecompiler{
		classFile:   d.classFile,
		locals:      locals,
		localTable:  localTable,
		returnType:  yields,
		isStatic:    isStatic,
		names:       d.names,
		byName:      map[string]*local{},
		visited:     map[int]bool{},
		activeTries: map[*tryRegion]bool{},
		skip:        map[int]int{},
		innerFlags:  d.innerFlags,
		bootstraps:  d.bootstraps,
		inlining:    d.inlining,
	}
	nested.current = &nested.statements
	for _, parameter := range locals {
		nested.names[parameter.Name] = true
		nested.byName[parameter.Name] = parameter
	}
	d.inlining[key] = true
	err = nested.run(instructions, code.Exceptions)
	delete(d.inlining, key)
	if err != nil {
		return "", err
	}
	// A body that assigns to one of its parameters would be assigning to what
	// the call site handed it, which source cannot write.
	for _, slot := range slots {
		if entry, ok := locals[slot.Slot]; ok && len(entry.Writes) > 0 {
			return "", bail("a lambda that assigns to its parameter")
		}
	}
	lines := withHoisted(flattenStatements(nested.hoisted), flattenStatements(nested.statements), "")
	if len(lines) > 0 && lines[len(lines)-1] == "return;" {
		lines = lines[:len(lines)-1]
	}
	// One expression is the form source wrote; anything else needs the block.
	if len(lines) == 1 {
		only := lines[0]
		if strings.HasPrefix(only, "return ") && strings.HasSuffix(only, ";") {
			return strings.TrimSuffix(strings.TrimPrefix(only, "return "), ";"), nil
		}
		if isStatementExpression(only) {
			return strings.TrimSuffix(only, ";"), nil
		}
	}
	return "{ " + strings.Join(lines, " ") + " }", nil
}

var (
	plainName        = regexp.MustCompile(`^[\w$.]+$`)
	statementKeyword = regexp.MustCompile(`^(?:throw|if|while|for|do|switch|try|synchronized|assert|break|continue|return|else)\b`)
	declarationStart = regexp.MustCompile(`^[A-Za-z_$][\w.$]*(?:\[\])*(?:<[^;]*>)?\s+[A-Za-z_$][\w$]*\s*[=;]`)
	expressionStart2 = regexp.MustCompile(`^[A-Za-z_$(]`)
)

// isStatementExpression reports whether one line is a statement *expression* -
// the only thing a lambda may carry without a block. A `throw` and a declaration
// are statements and neither is one, however much they look like a call.
func isStatementExpression(line string) bool {
	if !strings.HasSuffix(line, ";") || strings.Contains(line, "{") {
		return false
	}
	if statementKeyword.MatchString(line) || declarationStart.MatchString(line) {
		return false
	}
	return expressionStart2.MatchString(line)
}

func (d *bodyDecompiler) concat(siteDescriptor string, bootstrap BootstrapMethod, factory MemberRef) error {
	pool := d.classFile.Pool
	if (factory.Name != "makeConcatWithConstants" && factory.Name != "makeConcat") ||
		methodReturnType(siteDescriptor) != "java.lang.String" {
		return bail("an invokedynamic that is not a string concatenation")
	}

	params := parameterSlots(siteDescriptor, true)
	args := make([]expr, len(params))
	for i := len(params) - 1; i >= 0; i-- {
		value, err := d.pop()
		if err != nil {
			return err
		}
		arg, err := d.coercedExpr(value, params[i].Type)
		if err != nil {
			return err
		}
		args[i] = arg
	}

	recipe := strings.Repeat("\u0001", len(args))
	if factory.Name == "makeConcatWithConstants" {
		if len(bootstrap.ArgumentIndexes) == 0 {
			return bail("a concatenation without a recipe")
		}
		first := PoolAt(pool, bootstrap.ArgumentIndexes[0])
		if first == nil || first.Tag != TagString {
			return bail("a concatenation without a recipe")
		}
		recipe = PoolUtf8(pool, first.Index)
	}

	type concatPart struct {
		Value    expr
		IsString bool
	}
	var parts []concatPart
	literal := ""
	flush := func() {
		if literal == "" {
			return
		}
		parts = append(parts, concatPart{primary(`"`+escapeString(literal)+`"`, "java.lang.String"), true})
		literal = ""
	}
	taken := 0
	constant := 1
	// Byte by byte, not rune by rune: an unpaired surrogate reaches here as the
	// bytes PoolUtf8 kept, and re-encoding a decoded rune would turn them into
	// replacement characters instead of the `?` escapeString writes.
	for i := 0; i < len(recipe); i++ {
		switch recipe[i] {
		case '\u0001':
			if taken >= len(args) {
				return bail("a recipe that wants more arguments")
			}
			flush()
			parts = append(parts, concatPart{args[taken], params[taken].Type == "java.lang.String"})
			taken++
		case '\u0002':
			// A literal part javac could not put in the recipe itself, because it
			// contains one of the two tag characters: it is spliced back in as text.
			if constant >= len(bootstrap.ArgumentIndexes) {
				return bail("a recipe that wants more constants")
			}
			at := PoolAt(pool, bootstrap.ArgumentIndexes[constant])
			constant++
			if at == nil || at.Tag != TagString {
				return bail("a concatenation constant that is not a string")
			}
			literal += PoolUtf8(pool, at.Index)
		default:
			// The byte itself, not `string(recipe[i])` - that would read it as a
			// code point and re-encode a WTF-8 byte into something else.
			literal += recipe[i : i+1]
		}
	}
	flush()
	if taken != len(args) {
		return bail("a recipe that leaves arguments unused")
	}
	// With every part in the recipe the result is a constant expression, and javac
	// folds one of those to an `ldc` instead of the call it came from.
	if len(args) == 0 {
		return bail("a concatenation of constants")
	}

	// The result is a String, so one of the first two operands has to be one:
	// `"" + i + j` and `i + j` compile to the same arguments but are not the same
	// expression. A single operand needs the empty literal too - `s + ""` is a
	// concatenation, and `null + ""` is `"null"` where `s` alone is null.
	if len(parts) < 2 || (!parts[0].IsString && !parts[1].IsString) {
		parts = append([]concatPart{{primary(`""`, "java.lang.String"), true}}, parts...)
	}
	result := parts[0].Value
	for _, part := range parts[1:] {
		result = binaryExpr(result, "+", part.Value, precAdd, "java.lang.String")
	}
	// A part that calls something makes the concatenation itself a value that does
	// something: dropping it has to keep the call, not delete it.
	for _, arg := range args {
		if arg.Effects {
			result.Effects = true
			break
		}
	}
	d.push(result)
	return nil
}

// staticCallee names a static call: unqualified when it is this class's own
// method.
func (d *bodyDecompiler) staticCallee(owner, name string) string {
	if owner == d.classFile.ThisClass {
		return name
	}
	return typeName(owner, d.self()) + "." + name
}

// receiverCallee names an instance call, with the receiver the bytecode pushed
// before the arguments.
func (d *bodyDecompiler) receiverCallee(mnemonic, owner, name string, iface bool) (string, error) {
	receiver, err := d.pop()
	if err != nil {
		return "", err
	}
	// A variable whose type is still open is of the class the method called on
	// it belongs to: javac wrote the receiver's static type there.
	if entry, ok := d.byName[receiver.Text]; ok && (entry.Open || entry.Tentative) && mnemonic != "invokespecial" {
		if err := d.settle(entry, typeName(owner, d.self()), false); err != nil {
			return "", err
		}
		receiver = primary(entry.Name, entry.Type)
	}
	// A lambda has no type of its own, so calling a method on one needs the
	// interface named - source could not have written it any other way.
	if receiver.Lambda {
		return "(" + namedLambda(receiver).Text + ")." + name, nil
	}
	if mnemonic != "invokespecial" || owner == d.classFile.ThisClass {
		return at(receiver, precPrimary) + "." + name, nil
	}
	// The only other invokespecial source writes is `super.m()` - on `this`, of
	// a method of a superclass (javac names the one that declares it, which may
	// be further up) or of a direct superinterface, `Iface.super.m()`. The JVM
	// allows nothing else here.
	if receiver.Text != "this" {
		return "", bail("unsupported instruction invokespecial")
	}
	if iface {
		return typeName(owner, d.self()) + ".super." + name, nil
	}
	return "super." + name, nil
}

// construct writes a constructor call: either `new C(...)`, whose object is
// already on the stack, or the `super(...)`/`this(...)` that opens a
// constructor - which is not a call in source but the shape of one, and without
// which no constructor decompiles at all.
func (d *bodyDecompiler) construct(target MemberRef) error {
	// An inner class's constructor takes the enclosing instance as its first
	// argument, which source passes another way: implicitly, from a method of
	// the enclosing class, or as the `outer.new Inner(...)` qualifier. The
	// InnerClasses attribute of *this* file says which of the nested classes it
	// names are `static`; without an entry, the shape of the descriptor is all
	// there is to go on. An anonymous or local class is named for the method it
	// lives in, which is a declaration this phase does not restore.
	enclosing := ""
	if cut := strings.LastIndexByte(target.Owner, '$'); cut > 0 {
		enclosing = target.Owner[:cut]
	}
	params := parameterSlots(target.Descriptor, true)
	var outer *expr
	if enclosing != "" {
		// A static nested class may take the outer type first too, which is why
		// the flag decides where there is one; a class the emitter wrote may be
		// inner without taking the instance at all, and then there is none to
		// pass on.
		takesOuter := len(params) > 0 && params[0].Type == strings.ReplaceAll(enclosing, "/", ".")
		// An anonymous or local class - static or not - is named for the
		// method it lives in, a declaration this phase does not restore.
		tail := target.Owner[len(enclosing)+1:]
		if tail != "" && tail[0] >= '0' && tail[0] <= '9' {
			if strings.Trim(tail, "0123456789") == "" {
				return bail("an anonymous class")
			}
			return bail("a local class")
		}
		access, ok := d.innerFlags[target.Owner]
		inner := ok && access&accStatic == 0
		if !ok && takesOuter {
			// javac lists every nested class a file refers to; without the entry
			// the shape of the descriptor is all there is, and a static nested
			// class may take the outer type first too.
			return bail("an inner class constructor")
		}
		if inner && takesOuter {
			if len(d.stack) < len(params) {
				return bail("stack underflow")
			}
			value := d.stack[len(d.stack)-len(params)]
			outer = &value
		}
	}
	args, err := d.callArguments(target.Descriptor)
	if err != nil {
		return err
	}
	// The enclosing instance is implicit where source is a method of the
	// enclosing class and passed `this`; anywhere else it qualifies the call.
	// A `this(...)` keeps it: this file declares the class on its own, with the
	// parameter its constructors take, until the nesting is restored.
	qualifier := ""
	if outer != nil && target.Owner != d.classFile.ThisClass {
		args = args[1:]
		if outer.Text != "this" || d.classFile.ThisClass != enclosing {
			qualifier = at(*outer, precPrimary)
		}
	}
	receiver, err := d.pop()
	if err != nil {
		return err
	}
	if receiver.Pending == 0 {
		superClass := d.classFile.SuperClass
		if superClass == "" {
			superClass = "java/lang/Object"
		}
		isSuper := target.Owner == superClass
		if !isSuper && target.Owner != d.classFile.ThisClass {
			return bail("constructor call to an unrelated class")
		}
		if receiver.Text != "this" {
			return bail("constructor call on another object")
		}
		// Statements in front of the call are ones source could not have
		// written: the synthetic field an inner class assigns before its
		// `super()`, for one. Written back they follow the `super()` instead,
		// and only a statement that cannot throw survives that move -
		// `Object`'s constructor is where the object is registered for
		// finalization, so one that throws in front of it leaves an object
		// that is never registered, and after it one that is.
		trivialSuper := isSuper && len(args) == 0 && target.Owner == "java/lang/Object"
		for _, one := range d.statements {
			if !cannotThrow(one) {
				trivialSuper = false
			}
		}
		if (len(d.statements) > 0 && !trivialSuper) || d.depth > 0 {
			return bail("constructor call is not first")
		}
		// javac writes the implicit `super()` into every constructor; source
		// does not, and re-emitting puts it back. An enum constructor's
		// `super(name, ordinal)` is generated too - and writing it is a compile
		// error.
		if isSuper && qualifier == "" && (len(args) == 0 || isEnumDeclaration(d.classFile)) {
			return nil
		}
		keyword := "this"
		if isSuper {
			keyword = "super"
			if qualifier != "" {
				keyword = qualifier + ".super"
			}
		}
		d.chained = keyword + "(" + strings.Join(args, ", ") + ");"
		d.emit(d.chained)
		return nil
	}
	text := "new " + receiver.Type + "(" + strings.Join(args, ", ") + ")"
	if qualifier != "" {
		text = qualifier + ".new " + target.Owner[len(enclosing)+1:] + "(" + strings.Join(args, ", ") + ")"
	}
	value := expr{
		Text:    text,
		Prec:    precPrimary,
		Type:    receiver.Type,
		Effects: true,
	}
	// The `dup` in front of the call left one other copy of the same object. Two
	// would mean the object is used twice, and writing `new C(...)` in both
	// places would make two of them.
	kept := 0
	for i := range d.stack {
		if d.stack[i].Pending != receiver.Pending {
			continue
		}
		d.stack[i] = value
		kept++
	}
	if kept > 1 {
		return bail("one object used twice")
	}
	if kept == 0 {
		d.emit(value.Text + ";")
	}
	return nil
}

// freshName is wanted, kept distinct from the names already handed out. Two
// sibling scopes can declare the same name over the same slot; the body they
// decompile to is flat, so the second one has to be renamed.
func (d *bodyDecompiler) freshName(wanted string) string {
	name := wanted
	for n := 2; d.names[name]; n++ {
		name = wanted + "_" + strconv.Itoa(n)
	}
	d.names[name] = true
	return name
}

// slotOf reports the local slot of `iload_1`-style mnemonics, or the decoded operand.
func slotOf(instruction Instruction) int {
	m := instruction.Mnemonic
	if len(m) > 2 && m[len(m)-2] == '_' && m[len(m)-1] >= '0' && m[len(m)-1] <= '9' {
		return int(m[len(m)-1] - '0')
	}
	return instruction.Arg
}

// opBase strips the `_0`..`_3` and `_w` suffixes off a load/store mnemonic.
func opBase(m string) string {
	if len(m) > 2 && m[len(m)-2] == '_' {
		last := m[len(m)-1]
		if last == 'w' || (last >= '0' && last <= '9') {
			return m[:len(m)-2]
		}
	}
	return m
}

var singleSlotStore = regexp.MustCompile(`^[ifa]store(_[0-3])?$`)

var wideStore = regexp.MustCompile(`^[ld]store(_[0-3])?$`)

// assignedName is the variable an assignment-as-value assigns.
var assignedName = regexp.MustCompile(`^([A-Za-z_$][\w$]*) = `)

// usedAsNumber notes a use of value where only a number can go, when it is a
// variable. A variable this took for a boolean cannot be there: it is an int
// whose slot a dead boolean had, merged into one name that cannot carry both.
func (d *bodyDecompiler) usedAsNumber(value expr) error {
	// A materialized boolean reads as its condition - which may be a bare
	// variable - but is a number already, written as the ternary it carries.
	if value.AsInt != "" {
		return nil
	}
	// javac never puts a boolean bare where a number belongs; it materializes
	// one. So a value that reads as a boolean here has an int in it that this
	// took for a boolean - a variable whose slot a dead boolean had.
	if value.Type == "boolean" {
		return bail("a variable used as both a number and a boolean")
	}
	if entry, ok := d.byName[value.Text]; ok {
		entry.Numeric = true
	}
	return nil
}

// asNumber is numeric, with the use noted.
func (d *bodyDecompiler) asNumber(value expr) (expr, error) {
	if err := d.usedAsNumber(value); err != nil {
		return expr{}, err
	}
	return numeric(value), nil
}

// provenBoolean is value where its partner in a boolean operation is a boolean:
// Java has no `int & boolean`, so an int-typed local there is a boolean whose
// type was only inferred, and this is the use that proves it.
func (d *bodyDecompiler) provenBoolean(value, partner expr) (expr, error) {
	// A boolean javac erased to `1`/`0` proves nothing: `buf | (c ? 1 : 0)` is
	// an int operation. Only one that could never have been a number does.
	if partner.Type != "boolean" || erasedBoolean(partner) || value.Type != "int" {
		return value, nil
	}
	// Nor does a variable whose boolean type was only inferred from such a
	// value; one that was stored a genuine boolean does.
	if other, ok := d.byName[partner.Text]; ok && !other.Authoritative && !other.Proven {
		return value, nil
	}
	// The variable itself, or the one an assignment used as a value assigns.
	name := value.Text
	if m := assignedName.FindStringSubmatch(value.Text); m != nil {
		name = m[1]
	}
	entry, ok := d.byName[name]
	if !ok || entry.Authoritative {
		return value, nil
	}
	if _, err := d.coerceInto(value, "boolean"); err != nil {
		return expr{}, err
	}
	value.Type = "boolean"
	return value, nil
}

// checkDuplicable says what a `dup` may copy: only a value that reads the same
// thing every time may be written twice; an expression would be *computed*
// twice.
func checkDuplicable(value expr) error {
	if value.Effects {
		return bail("dup of a call")
	}
	// The text is written once per copy, so an increment inside it would run
	// once per copy too.
	if increments(value.Text) {
		return bail("dup of an increment")
	}
	if value.Compared != nil {
		return bail("dup of a comparison")
	}
	if value.Pending == 0 && value.Init == nil &&
		(value.Prec != precPrimary || strings.HasPrefix(value.Text, "new ")) {
		return bail("dup of a non-trivial value")
	}
	return nil
}

// settle gives an open variable its type - and the variables typed with it.
// An empty type closes it as the Object it is.
func (d *bodyDecompiler) settle(entry *local, typ string, tentative bool) error {
	entry.Open, entry.Tentative = false, tentative
	if typ != "" && typ != entry.Type {
		if err := d.retype(entry, typ); err != nil {
			return err
		}
	}
	for _, other := range entry.Also {
		if other.Open {
			if err := d.settle(other, typ, tentative); err != nil {
				return err
			}
		}
	}
	return nil
}

// popShared pops a value that may be one of a compound assignment's copies.
func (d *bodyDecompiler) popShared() (expr, error) {
	top, err := d.popRaw()
	if err != nil {
		return expr{}, err
	}
	if top.Shared != 0 {
		return top, nil
	}
	d.push(top)
	return d.pop()
}

// compoundOps are the operators with a compound assignment form.
var compoundOps = map[string]bool{
	"+": true, "-": true, "*": true, "/": true, "%": true,
	"<<": true, ">>": true, ">>>": true, "&": true, "|": true, "^": true,
}

// compoundAssign writes `target op= rhs` for a value that is the read of the
// same target (through the copies marked shared) combined with one operand -
// the only shape a receiver written once can carry. A narrowing conversion
// javac put on the result is the compound assignment's own.
func (d *bodyDecompiler) compoundAssign(target string, shared int, value expr, targetType string) error {
	if value.Inner != nil && value.Type == targetType {
		value = *value.Inner
	}
	if value.Bin == nil || value.Bin.Left.ReadOf != shared || !compoundOps[value.Bin.Op] ||
		value.Bin.Left.Text != target {
		return bail("dup of a non-trivial value")
	}
	text := target + " " + value.Bin.Op + "= " + at(value.Bin.Right, precAssign+1)
	// One the stack still wants is the expression it is, in place.
	if d.assignFieldAsValue {
		d.assignFieldAsValue = false
		d.push(expr{Text: text, Prec: precAssign, Type: targetType, Effects: true})
		return nil
	}
	for _, stacked := range d.stack {
		if observesWrites(stacked, d.names) {
			return bail("an assignment with a value that could see it on the stack")
		}
	}
	d.emit(text + ";")
	return nil
}

// storeAsValue writes a store whose value the stack still wants as the
// assignment it is, in the place the value was. The variable cannot be declared
// here - an expression is no place for a declaration - so it is hoisted.
func (d *bodyDecompiler) storeAsValue(slot, scopePc int, value expr, declaredType string) error {
	target, err := d.local(slot, scopePc, declaredType, true)
	if err != nil {
		return err
	}
	target.StoreBlocks[d.currentBlock] = true
	text, err := d.coerceInto(value, target.Type)
	if err != nil {
		return err
	}
	if !target.Declared {
		target.Declared = true
		target.Declaration = &localDeclaration{List: &d.hoisted, Index: len(d.hoisted)}
		d.hoisted = append(d.hoisted, stmt{Text: target.Type + " " + target.Name + ";"})
	}
	target.Writes = append(target.Writes,
		localWrite{List: d.current, Index: len(*d.current), Value: value, InValue: true})
	// The value is the assignment, whose text is coerced to the variable's type
	// as it stands and can never be rewritten. Where that type was only inferred
	// it is not what the next variable in a chain should learn from -
	// `int i = (c = s.charAt(0))` is an int, whatever `c` turned out to be - so
	// an inferred int-family type is handed on as the `int` it was erased to.
	// A boolean is not erased: a `Z`-typed value stored with `istore` is a
	// boolean in source, and so is the assignment.
	typ := target.Type
	if !target.Authoritative && typ != "boolean" && (typ == "int" || erasedToInt[typ]) {
		typ = "int"
	}
	d.push(expr{
		Text:    target.Name + " = " + text,
		Prec:    precAssign,
		Type:    typ,
		Effects: true,
	})
	return nil
}

func (d *bodyDecompiler) store(slot, scopePc int, value expr, declaredType string) error {
	target, err := d.local(slot, scopePc, declaredType, true)
	if err != nil {
		return err
	}
	// A variable assigned another whose type is open shares that: the two are
	// typed together, by whichever use asks first - this store asks nothing.
	linked := false
	if source, ok := d.byName[value.Text]; ok && source.Open && !target.Authoritative &&
		target.Type == "java.lang.Object" && len(target.Writes) == 0 {
		target.Open = true
		source.Also = append(source.Also, target)
		target.Also = append(target.Also, source)
		linked = true
	}
	// The assignment is a statement here, so it runs before everything the stack
	// already holds - and those read the variable as it is *after* it. A `char`,
	// `byte` or `short` post-increment is written this way rather than with an
	// `iinc`, and so is any other assignment used as a value. A value that
	// increments is wrong the other way round: this would run before an
	// increment that has already happened.
	for _, one := range d.stack {
		if increments(one.Text) || reads(withoutLiterals(one.Text), target.Name) {
			return bail("an assignment to a variable that is already on the stack")
		}
	}
	target.StoreBlocks[d.currentBlock] = true
	text := value.Text
	if !linked {
		text, err = d.coerceInto(value, target.Type)
	}
	if err != nil {
		return err
	}
	if !target.Declared {
		target.Declared = true
		if d.depth == 0 {
			target.Declaration = &localDeclaration{List: d.current, Index: len(*d.current), Inline: true}
			target.Writes = append(target.Writes,
				localWrite{List: d.current, Index: len(*d.current), Value: value})
			d.emit(target.Type + " " + target.Name + " = " + text + ";")
			return nil
		}
		target.Declaration = &localDeclaration{List: &d.hoisted, Index: len(d.hoisted)}
		d.hoisted = append(d.hoisted, stmt{Text: target.Type + " " + target.Name + ";"})
	}
	target.Writes = append(target.Writes,
		localWrite{List: d.current, Index: len(*d.current), Value: value})
	d.emit(target.Name + " = " + text + ";")
	return nil
}

func (d *bodyDecompiler) run(instructions []Instruction, exceptions []ExceptionEntry) error {
	blocks, err := buildBlocks(instructions, exceptions)
	if err != nil {
		return err
	}
	d.blocks = blocks
	d.instructionAt = map[int]Instruction{}
	for _, one := range instructions {
		d.instructionAt[one.Pc] = one
	}
	// A `finally` writes its body twice, and the copy begins where the protected
	// range ends - in the middle of a block. Only that shape wants the split: a
	// monitor's range ends inside the expression it releases.
	var splits []int
	for _, entry := range exceptions {
		if entry.CatchType != "" || int(entry.StartPc) == int(entry.HandlerPc) {
			continue
		}
		if _, isMonitor := isMonitorHandler(blocks[int(entry.HandlerPc)]); isMonitor {
			continue
		}
		if _, isFinally := finallyBody(blocks[int(entry.HandlerPc)]); isFinally {
			splits = append(splits, int(entry.EndPc))
		}
	}
	if len(splits) > 0 {
		rebuilt, err := buildBlocks(instructions, exceptions, splits...)
		if err != nil {
			return err
		}
		blocks = rebuilt
		d.blocks = blocks
	}
	monitors, finallys, rest, err := monitorRegions(exceptions, blocks, instructions)
	if err != nil {
		return err
	}
	d.monitors = monitors
	d.finallys = finallys
	d.activeFinallys = map[*finallyRegion]bool{}
	regions, err := tryRegions(rest, blocks, d.self())
	if err != nil {
		return err
	}
	d.regions = regions
	d.followOf = postDominators(blocks, nil, nil)
	d.methodFollowOf = d.followOf
	entry := 0
	if len(instructions) > 0 {
		entry = instructions[0].Pc
	}
	if len(d.regions) > 0 || len(d.monitors) > 0 {
		d.dominators = dominators(withExceptionEdges(blocks, d.regions, d.monitors), entry)
	}
	loops, err := findLoops(blocks, entry, d.regions, d.monitors)
	if err != nil {
		return err
	}
	d.loops = loops
	d.entryPc = entry
	d.currentBlock = entry
	// A parameter is defined on entry, on every path: a read after a branch
	// that reassigned it is as unambiguous as any other.
	for _, parameter := range d.locals {
		parameter.StoreBlocks[entry] = true
	}
	if err := d.structure(entry, exitBlock); err != nil {
		return err
	}
	if len(d.stack) > 0 {
		return bail("values left on the stack")
	}
	// Only now is it known how often a captured variable is written: Java takes
	// an effectively final one, and hoisting can leave a loop variable assigned
	// once per turn. A parameter is written nowhere; anything else has to carry
	// its one value at the declaration.
	for _, entry := range d.captured {
		settled := len(entry.Writes) == 0 ||
			(len(entry.Writes) == 1 && entry.Declaration != nil && entry.Declaration.Inline)
		if !settled {
			return bail("a lambda that captures a variable that is not final")
		}
	}
	// A block that was never entered would silently drop its statements, and one
	// entered twice would duplicate them: either means the layout is not the nest
	// of `if`s this phase reconstructs.
	roots := []int{entry}
	for _, region := range d.regions {
		for _, c := range region.Clauses {
			roots = append(roots, c.HandlerPc)
		}
	}
	for start := range reachableBlocks(d.blocks, roots...) {
		if !d.visited[start] {
			return bail("unstructured control flow")
		}
	}
	return nil
}

// structure appends the statements for the blocks from entry up to (not
// including) stop.
func (d *bodyDecompiler) structure(entry, stop int) error {
	return d.structureFrom(entry, stop, false)
}

// structureFrom appends the statements for the blocks from entry up to (not
// including) stop. The block a `while (true)` opens with *is* its continue
// target: arriving there later is a `continue`, but entering it is the body
// starting, which `opening` says.
func (d *bodyDecompiler) structureFrom(entry, stop int, opening bool) error {
	at := entry
	entering := opening
	for at != stop && at != exitBlock {
		jump, jumped := "", false
		if !entering {
			var err error
			jump, jumped, err = d.loopJump(at)
			if err != nil {
				return err
			}
		}
		entering = false
		if jumped {
			d.emit(jump)
			return nil
		}
		if next, handled, err := d.finallyAt(at); err != nil {
			return err
		} else if handled {
			at = next
			continue
		}
		if region := d.regionAt(at); region != nil {
			next, err := d.tryStatement(region)
			if err != nil {
				return err
			}
			at = next
			continue
		}
		if l := d.loops[at]; l != nil && !d.inActive(l) {
			next, err := d.loop(l)
			if err != nil {
				return err
			}
			at = next
			continue
		}
		b := d.blocks[at]
		if b == nil {
			return bail("a branch lands outside the method")
		}
		if d.visited[at] {
			// The `continue` of a `for` jumps to its update, not to the test -
			// so the update is entered twice, once from the jump and once from
			// the body running off its end. Writing it needs the `for` form.
			if len(d.active) > 0 {
				inner := d.active[len(d.active)-1]
				if inner.Loop.Body[at] && at != inner.ContinueTarget {
					return bail("a jump into the middle of a loop")
				}
			}
			return bail("unstructured control flow")
		}
		d.visited[at] = true
		body := b.Instructions[d.skip[at]:]
		if len(body) > 0 && body[len(body)-1].Mnemonic == "monitorenter" {
			next, err := d.synchronizedStatement(b, body)
			if err != nil {
				return err
			}
			at = next
			continue
		}
		if b.Kind == blockConditional || b.Kind == blockGoto || b.Kind == blockSwitch {
			body = body[:len(body)-1]
		}
		if err := d.runInstructions(body, endOf(b), b.Start); err != nil {
			return err
		}
		if b.Kind == blockEnd {
			return nil
		}
		if b.Kind == blockSwitch {
			next, err := d.switchStatement(b, stop)
			if err != nil {
				return err
			}
			at = next
			continue
		}
		if b.Kind != blockConditional {
			at = b.Successors[0]
			continue
		}
		next, err := d.conditional(b, stop)
		if err != nil {
			return err
		}
		at = next
	}
	return nil
}

// synchronizedStatement writes one `synchronized`, from the `monitorenter` that
// ends b, and reports where the statement after it begins.
func (d *bodyDecompiler) synchronizedStatement(b *block, kept []Instruction) (int, error) {
	var region *monitorRegion
	for i := range d.monitors {
		if len(b.Successors) > 0 && d.monitors[i].StartPc == b.Successors[0] {
			region = &d.monitors[i]
			break
		}
	}
	if region == nil {
		return 0, bail("unsupported instruction monitorenter")
	}
	// javac evaluates the monitor, copies it into a synthetic local and enters:
	// the copy is what the handler releases, and source wrote only the expression.
	if len(kept) < 3 {
		return 0, bail("a monitor that is not held in a local")
	}
	head := kept[:len(kept)-3]
	copyInstruction, store := kept[len(kept)-3], kept[len(kept)-2]
	if copyInstruction.Mnemonic != "dup" || !strings.HasPrefix(store.Mnemonic, "astore") ||
		slotOf(store) != region.Slot {
		return 0, bail("a monitor that is not held in a local")
	}
	if err := d.runInstructions(head, copyInstruction.Pc, b.Start); err != nil {
		return 0, err
	}
	monitor, err := d.pop()
	if err != nil {
		return 0, err
	}
	if len(d.stack) > 0 {
		return 0, bail("values left on the stack")
	}
	// The body leaves through the jump over the handler; with no jump there,
	// every path out of it returns or throws.
	follow := exitBlock
	if exit, ok := d.instructionAt[region.EndPc]; ok && isGotoMnemonic(exit.Mnemonic) && d.blocks[exit.Arg] != nil {
		follow = exit.Arg
	}
	// The release and the rethrow are the statement's own, not the body's.
	d.visited[region.HandlerPc] = true
	// Inside the body, a merge is a merge of the body's own paths: what leaves
	// the statement leaves it the way a `return` does, and counting it would put
	// the merge of an `if` in here past the end of the `synchronized`.
	within := map[int]bool{}
	queue := []int{region.StartPc}
	for len(queue) > 0 {
		at := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if at == follow || within[at] || d.blocks[at] == nil {
			continue
		}
		within[at] = true
		queue = append(queue, d.blocks[at].Successors...)
	}
	outer := d.followOf
	d.followOf = postDominators(d.blocks, within, map[int]bool{follow: true})
	d.monitorSlots = append(d.monitorSlots, region.Slot)
	statements, err := d.capture(func() error { return d.structure(region.StartPc, follow) })
	d.followOf = outer
	d.monitorSlots = d.monitorSlots[:len(d.monitorSlots)-1]
	if err != nil {
		return 0, err
	}
	if len(d.stack) > 0 {
		return 0, bail("values left on the stack")
	}
	d.emit("synchronized (" + monitor.Text + ") {")
	*d.current = append(*d.current, stmt{Nested: &statements})
	d.emit("}")
	return follow, nil
}

// finallyAt writes the `try`/`finally` that begins at at, if one does.
func (d *bodyDecompiler) finallyAt(at int) (int, bool, error) {
	for i := range d.finallys {
		guarded := &d.finallys[i]
		if guarded.StartPc != at || d.visited[at] || d.activeFinallys[guarded] {
			continue
		}
		next, err := d.finallyStatement(guarded)
		return next, true, err
	}
	return 0, false, nil
}

// finallyStatement writes one `try`/`finally`. javac writes the body of the
// `finally` twice - once on the way out of the protected range, once in the
// catch-all that rethrows - and only the second one is written back; the copy on
// the way out is dropped, because source wrote it once.
func (d *bodyDecompiler) finallyStatement(region *finallyRegion) (int, error) {
	if len(d.stack) > 0 {
		return 0, bail("values left on the stack")
	}
	copyBlock := d.blocks[region.EndPc]
	jump := copyBlock.Instructions[len(copyBlock.Instructions)-1]
	// What is left of the copy block is the jump over the handler, or the `return`
	// javac protected the value of - and a jump to nowhere is not a statement.
	if copyBlock.Kind != blockEnd && d.blocks[jump.Arg] == nil {
		return 0, bail("a finally that leaves the method")
	}
	// The copy on the way out is not a statement: what is left of that block is
	// the jump over the handler.
	d.skip[copyBlock.Start] = len(region.Body)
	d.visited[region.HandlerPc] = true
	d.activeFinallys[region] = true
	body, err := d.capture(func() error { return d.structure(region.StartPc, copyBlock.Start) })
	delete(d.activeFinallys, region)
	if err != nil {
		return 0, err
	}
	if len(d.stack) > 0 {
		return 0, bail("values left on the stack")
	}
	handler := d.blocks[region.HandlerPc]
	cleanup, err := d.capture(func() error {
		return d.runInstructions(region.Body, endOf(handler), region.HandlerPc)
	})
	if err != nil {
		return 0, err
	}
	if len(d.stack) > 0 {
		return 0, bail("values left on the stack")
	}
	d.emit("try {")
	*d.current = append(*d.current, stmt{Nested: &body})
	d.emit("} finally {")
	*d.current = append(*d.current, stmt{Nested: &cleanup})
	d.emit("}")
	return copyBlock.Start, nil
}

// regionAt is the `try` statement that begins at at, if one does. The outermost
// comes first: an inner `try` sharing the start is written when the body reaches
// it again. A loop whose header is the same block is the outer statement, unless
// the protected range covers the whole loop.
func (d *bodyDecompiler) regionAt(at int) *tryRegion {
	if d.visited[at] {
		return nil
	}
	var region *tryRegion
	for _, candidate := range d.regions {
		if candidate.StartPc != at || d.activeTries[candidate] {
			continue
		}
		if region == nil || candidate.EndPc > region.EndPc {
			region = candidate
		}
	}
	if region == nil {
		return nil
	}
	if l := d.loops[at]; l != nil && !d.inActive(l) {
		for start := range l.Body {
			if start < region.StartPc || start >= region.EndPc {
				return nil
			}
		}
	}
	return region
}

// tryStatement writes one `try` with its clauses. It returns where the statement
// after it begins.
func (d *bodyDecompiler) tryStatement(region *tryRegion) (int, error) {
	if len(d.stack) > 0 {
		return 0, bail("values left on the stack")
	}
	bodyStop, err := d.tryFollow(region)
	if err != nil {
		return 0, err
	}
	// Where the body leaves off is not always where the statement ends: when the
	// exit is also a branch target, javac's jump over the handlers stands in a
	// block of its own. That block is the end of the `try`, not a statement.
	follow := bodyStop
	for follow != exitBlock {
		b := d.blocks[follow]
		if b.Kind != blockGoto || len(b.Instructions) != 1 || d.visited[follow] {
			break
		}
		d.visited[follow] = true
		follow = b.Successors[0]
	}
	d.activeTries[region] = true
	defer delete(d.activeTries, region)
	body, err := d.capture(func() error { return d.structure(region.StartPc, bodyStop) })
	if err != nil {
		return 0, err
	}
	if len(d.stack) > 0 {
		return 0, bail("values left on the stack")
	}
	type written struct {
		head       string
		statements []stmt
	}
	var clauses []written
	for _, c := range region.Clauses {
		// The parameter has to be named before the handler runs: its store is
		// what the name comes from, and that store is not a statement.
		name, restore, err := d.catchName(c)
		if err != nil {
			return 0, err
		}
		statements, err := d.capture(func() error { return d.structure(c.HandlerPc, follow) })
		if err != nil {
			return 0, err
		}
		// The parameter's scope is its clause; the slot goes back to whatever
		// variable it held before, which javac reuses it for afterwards.
		restore()
		if len(d.stack) > 0 {
			return 0, bail("values left on the stack")
		}
		clauses = append(clauses, written{
			head:       "} catch (" + strings.Join(c.Types, " | ") + " " + name + ") {",
			statements: statements,
		})
	}
	d.emit("try {")
	*d.current = append(*d.current, stmt{Nested: &body})
	for i := range clauses {
		d.emit(clauses[i].head)
		*d.current = append(*d.current, stmt{Nested: &clauses[i].statements})
	}
	d.emit("}")
	return follow, nil
}

// tryFollow is where a `try` statement ends. What leaves the protected range
// normally is the statement's own follow; a `break`, a `continue` or a `return`
// leaves the statement the way it leaves an `if`, and says nothing about where
// it ends. When nothing leaves the body at all, the code after the statement is
// whatever the handlers fall into.
func (d *bodyDecompiler) tryFollow(region *tryRegion) (int, error) {
	inside := map[int]bool{}
	for start := range d.blocks {
		if start >= region.StartPc && start < region.EndPc {
			inside[start] = true
		}
	}
	jumps := map[int]bool{}
	for _, entered := range d.active {
		jumps[entered.Loop.Follow] = true
		// A `do`'s continue target is its latch, and javac puts the tail of the
		// body in there: falling into it is the body running on, not a `continue`.
		latch := d.blocks[entered.ContinueTarget]
		if entered.ContinueTarget == entered.Loop.Header || (latch != nil && isPureBlock(latch)) {
			jumps[entered.ContinueTarget] = true
		}
	}
	exits := map[int]bool{}
	for start := range inside {
		for _, successor := range d.blocks[start].Successors {
			if !inside[successor] && !jumps[successor] {
				exits[successor] = true
			}
		}
	}
	// A `return` or a `throw` javac kept out of the protected range shows up as
	// an edge leaving it, but it is body, not the end of the statement: it only
	// counts when nothing else leaves.
	var leaving []int
	for exit := range exits {
		if b := d.blocks[exit]; b == nil || b.Kind != blockEnd {
			leaving = append(leaving, exit)
		}
	}
	if len(leaving) == 1 {
		return leaving[0], nil
	}
	if len(leaving) > 1 || len(exits) > 1 {
		return 0, bail("unstructured control flow")
	}
	for exit := range exits {
		return exit, nil
	}
	follows := map[int]bool{}
	for _, c := range region.Clauses {
		follow, ok := d.methodFollowOf[c.HandlerPc]
		if !ok {
			follow = exitBlock
		}
		// The post-dominator of a handler that branches is a merge *inside* it,
		// and so is anything only the handler reaches: taking either would end
		// the statement in the middle of its own `catch`.
		if follow != exitBlock && !jumps[follow] && !d.dominators[follow][c.HandlerPc] {
			follows[follow] = true
		}
	}
	if len(follows) > 1 {
		return 0, bail("unstructured control flow")
	}
	for follow := range follows {
		return follow, nil
	}
	return exitBlock, nil
}

// catchName is the catch parameter of one clause. A handler is entered with the
// exception on the stack, so it starts by storing it - or dropping it, when
// source never named it.
func (d *bodyDecompiler) catchName(c *clause) (string, func(), error) {
	b := d.blocks[c.HandlerPc]
	if len(b.Instructions) == 0 {
		return "", nil, bail("an exception handler that keeps the exception")
	}
	first := b.Instructions[0]
	if first.Mnemonic != "pop" && !strings.HasPrefix(first.Mnemonic, "astore") {
		return "", nil, bail("an exception handler that keeps the exception")
	}
	d.skip[c.HandlerPc] = 1
	if first.Mnemonic == "pop" {
		return d.freshName("e"), func() {}, nil
	}
	slot := slotOf(first)
	// The debug table scopes the parameter from *after* its store, which is
	// where the handler's own code begins - and when the store is all there is,
	// that is where the block ends.
	scopePc := endOf(b)
	if len(b.Instructions) > 1 {
		scopePc = b.Instructions[1].Pc
	}
	var scoped *localEntry
	for i := range d.localTable {
		entry := &d.localTable[i]
		if entry.Slot == slot && scopePc >= entry.StartPc && scopePc < entry.EndPc {
			scoped = entry
			break
		}
	}
	// ponytail: a multi-catch with no debug table is declared as its first type -
	// the common supertype source used is not written to the class file.
	declared, name := c.Types[0], "e"
	if scoped != nil {
		if scoped.Type != "" {
			declared = scoped.Type
		}
		if scoped.Name != "" {
			name = scoped.Name
		}
	}
	entry := &local{
		Name:          d.freshName(name),
		Type:          sourceTypeText(declared, d.self()),
		Declared:      true,
		Origin:        scoped,
		Authoritative: true, // the clause says what the parameter's type is
		StoreBlocks:   map[int]bool{c.HandlerPc: true},
	}
	shadowed, wasSet := d.locals[slot]
	d.byName[entry.Name] = entry
	d.locals[slot] = entry
	restore := func() {
		if wasSet {
			d.locals[slot] = shadowed
		} else {
			delete(d.locals, slot)
		}
	}
	return entry.Name, restore, nil
}

// loopJump reports `break;` or `continue;` when at is where the innermost
// loop's next iteration, or the code after it, begins - and `break label;` or
// `continue label;` for an enclosing loop, which the label then names.
func (d *bodyDecompiler) loopJump(at int) (string, bool, error) {
	// Only the innermost breakable statement can be left without a label, and a
	// loop opened inside a `switch` is the innermost one.
	switchIsInner := len(d.switches) > 0 && d.switches[len(d.switches)-1].LoopDepth == len(d.active)
	// The end of the `switch` comes first, even when it is also the loop's
	// continue target: a `do`'s continue target is its latch, and javac puts the
	// tail of the body in there - `continue;` would skip it.
	if switchIsInner && at == d.switches[len(d.switches)-1].Follow {
		return "break;", true, nil
	}
	// A `switch` catches `break` but not `continue`, so the innermost loop still
	// owns its own continue target even from inside one.
	if len(d.active) > 0 && at == d.active[len(d.active)-1].ContinueTarget {
		// Unless the jump lands in the tail of a `do`'s body, which a `continue;`
		// would skip: nothing in this phase can write that jump.
		if !d.active[len(d.active)-1].Continues {
			return "", false, bail("a jump into the tail of a do-while")
		}
		return "continue;", true, nil
	}
	if !switchIsInner && len(d.active) > 0 && at == d.active[len(d.active)-1].Loop.Follow {
		return "break;", true, nil
	}
	outerLoops := d.active
	if !switchIsInner && len(d.active) > 0 {
		outerLoops = d.active[:len(d.active)-1]
	}
	// Innermost first: the nearest loop the jump fits is the one source named.
	for i := len(outerLoops) - 1; i >= 0; i-- {
		outer := &outerLoops[i]
		if at != outer.ContinueTarget && at != outer.Loop.Follow {
			continue
		}
		if at == outer.ContinueTarget && !outer.Continues {
			return "", false, bail("a jump into the tail of a do-while")
		}
		if outer.Label == "" {
			d.labels++
			outer.Label = d.freshName("label" + strconv.Itoa(d.labels))
		}
		if at == outer.ContinueTarget {
			return "continue " + outer.Label + ";", true, nil
		}
		return "break " + outer.Label + ";", true, nil
	}
	outerSwitches := d.switches
	if switchIsInner {
		outerSwitches = d.switches[:len(d.switches)-1]
	}
	for _, outer := range outerSwitches {
		if at == outer.Follow {
			return "", false, bail("a labeled break or continue")
		}
	}
	return "", false, nil
}

func (d *bodyDecompiler) inActive(l *loop) bool {
	for _, entered := range d.active {
		if entered.Loop == l {
			return true
		}
	}
	return false
}

// isLoopEdge reports whether start is a loop's own edge, which no expression may
// fold away.
func (d *bodyDecompiler) isLoopEdge(start int) bool {
	if d.loops[start] != nil {
		return true
	}
	for _, entered := range d.active {
		if entered.ContinueTarget == start || entered.Loop.Follow == start {
			return true
		}
	}
	return false
}

// trimTail drops a trailing `continue;` a loop's own fallthrough already says.
func trimTail(statements []stmt, text string) []stmt {
	if len(statements) > 0 {
		last := statements[len(statements)-1]
		if last.Nested == nil && last.Text == text {
			return statements[:len(statements)-1]
		}
	}
	return statements
}

// loop writes one loop, from its header, and reports where the statement after
// it begins.
func (d *bodyDecompiler) loop(l *loop) (int, error) {
	if len(d.stack) > 0 {
		return 0, bail("values left on the stack")
	}
	header := d.blocks[l.Header]
	var latch *block
	if len(l.Latches) == 1 {
		latch = d.blocks[l.Latches[0]]
	}
	isWhile := l.Follow != exitBlock && header.Kind == blockConditional &&
		isConditionBlock(header) && headerExits(d.blocks, l)
	isDoWhile := !isWhile && l.Follow != exitBlock && latch != nil &&
		latch.Kind == blockConditional &&
		containsInt(latch.Successors, l.Header) && containsInt(latch.Successors, l.Follow)
	// Inside the body, the merge of an `if` is a merge of the body's own paths:
	// the edges a `continue` and a `break` take are exits, not joins. A `do`'s
	// latch only counts when it is the test alone - javac puts the tail of the
	// body in the same block when nothing jumps to the test, and that tail is a
	// join like any other. A call in there is body, not test, so this is the
	// strict predicate: cutting a join would drop the statements after it.
	cut := map[int]bool{l.Header: true}
	if isDoWhile && isPureBlock(latch) {
		cut[latch.Start] = true
	}
	outer := d.followOf
	d.followOf = postDominators(d.blocks, l.Body, cut)
	defer func() { d.followOf = outer }()
	if isWhile {
		return d.whileLoop(l, header)
	}
	if isDoWhile {
		return d.doWhileLoop(l, latch)
	}
	return d.foreverLoop(l)
}

// reachesArm reports whether one arm of a branch runs into the other, which
// makes the second one the code after the `if` rather than its `else`. A `break`
// or a `continue` ends the walk: it leaves the statement, like a `return`.
func (d *bodyDecompiler) reachesArm(from, to int) bool {
	stop := map[int]bool{}
	if len(d.active) > 0 {
		inner := d.active[len(d.active)-1]
		stop[inner.ContinueTarget] = true
		stop[inner.Loop.Follow] = true
	}
	for _, entered := range d.switches {
		stop[entered.Follow] = true
	}
	seen := map[int]bool{}
	queue := []int{from}
	for len(queue) > 0 {
		at := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if at == to {
			return true
		}
		if seen[at] || stop[at] || d.blocks[at] == nil {
			continue
		}
		seen[at] = true
		queue = append(queue, d.blocks[at].Successors...)
	}
	return false
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// whileLoop writes `while (c) { ... }`: the header is the test, and the loop
// runs while it holds.
func (d *bodyDecompiler) whileLoop(l *loop, header *block) (int, error) {
	update := d.forUpdate(l)
	continueTarget := l.Header
	if update != nil {
		continueTarget = update.Start
	}
	d.active = append(d.active, activeLoop{Loop: l, ContinueTarget: continueTarget, Continues: true})
	defer func() { d.active = d.active[:len(d.active)-1] }()
	d.visited[l.Header] = true
	last := header.Instructions[len(header.Instructions)-1]
	statementsBefore := len(*d.current)
	if err := d.runInstructions(header.Instructions[:len(header.Instructions)-1], last.Pc, header.Start); err != nil {
		return 0, err
	}
	// The test runs once per iteration, and what it computes goes into the
	// `while (...)` line - a statement in there would run once, ahead of the
	// loop, which is not what the bytecode says.
	if len(*d.current) != statementsBefore {
		return 0, bail("a loop test that is a statement")
	}
	var taken []int
	jump, err := d.jumpConditionOf(header, &taken, nil)
	if err != nil {
		return 0, err
	}
	for _, start := range taken {
		d.visited[start] = true
	}
	if jump.Target != l.Follow && jump.Fallthrough != l.Follow {
		return 0, bail("an unstructured loop")
	}
	// The branch that leaves the loop is the negation of what source wrote.
	condition, body := jump.Condition, jump.Target
	if jump.Target == l.Follow {
		condition, body = negate(jump.Condition), jump.Fallthrough
	}
	statements, err := d.capture(func() error { return d.structure(body, continueTarget) })
	if err != nil {
		return 0, err
	}
	statements = trimTail(statements, "continue;")
	// `for (; c; update)` is the same bytecode as the `while` whose last
	// statement is the update - but it is where a `continue` goes, so it is the
	// only form that can write one.
	clause := ""
	if update != nil {
		clause, err = d.updateClause(update)
		if err != nil {
			return 0, err
		}
	}
	label := labelPrefix(d.active[len(d.active)-1].Label)
	if clause == "" {
		// A `continue` target with nothing in it: the jump back to the test, on
		// its own. There is no update to write, so this stays a `while`.
		d.emitCondition(condition, func(text string) string { return label + whileWrap(text) })
	} else {
		d.emitCondition(condition, func(text string) string {
			return label + "for (; " + text + "; " + clause + ") {"
		})
	}
	*d.current = append(*d.current, stmt{Nested: &statements})
	d.emit("}")
	return l.Follow, nil
}

// labelPrefix is `label: ` for a loop a jump named, and nothing otherwise.
func labelPrefix(label string) string {
	if label == "" {
		return ""
	}
	return label + ": "
}

// forUpdate is the update of a `for`, which javac lays out at the bottom of the
// body with the test at the top. It is only worth naming when something *jumps*
// to it - a body that simply runs into it is a `while` whose last statement is
// the update, which is what this wrote before there was a `for` form.
func (d *bodyDecompiler) forUpdate(l *loop) *block {
	if len(l.Latches) != 1 {
		return nil
	}
	latch := d.blocks[l.Latches[0]]
	// The update runs before the test, so it ends in the jump back to it, and it
	// has to hold something besides that jump.
	if latch == nil || latch.Start == l.Header || latch.Kind != blockGoto {
		return nil
	}
	if d.visited[latch.Start] {
		return nil
	}
	// Only an increment can be written as the update clause, and this has to be
	// decided before the body is - `continue;` goes here only if it does. Any
	// other tail stays a statement of the body, where the `while` form has always
	// put it: a local stored in here could still be *retyped*, and a clause
	// frozen into the `for` line would not carry the rewrite.
	for _, instruction := range latch.Instructions[:len(latch.Instructions)-1] {
		if strings.TrimSuffix(instruction.Mnemonic, "_w") != "iinc" {
			return nil
		}
	}
	predecessors := 0
	for _, b := range d.blocks {
		for _, successor := range b.Successors {
			if successor == latch.Start {
				predecessors++
				break
			}
		}
	}
	if predecessors > 1 {
		return latch
	}
	return nil
}

// updateClause is the `for`'s update clause: the update block's statements, which
// are increments, so every one of them is an expression statement. It reports ""
// for a block that holds only the jump back to the test.
func (d *bodyDecompiler) updateClause(update *block) (string, error) {
	d.visited[update.Start] = true
	statements, err := d.capture(func() error {
		return d.runInstructions(update.Instructions[:len(update.Instructions)-1], endOf(update), update.Start)
	})
	if err != nil {
		return "", err
	}
	var written []string
	for _, text := range flattenStatements(statements) {
		written = append(written, strings.TrimSuffix(text, ";"))
	}
	return strings.Join(written, ", "), nil
}

// doWhileLoop writes `do { ... } while (c);`: the test is the latch, and the
// body runs first.
func (d *bodyDecompiler) doWhileLoop(l *loop, latch *block) (int, error) {
	d.active = append(d.active, activeLoop{Loop: l, ContinueTarget: latch.Start, Continues: isPureBlock(latch)})
	defer func() { d.active = d.active[:len(d.active)-1] }()
	var condition expr
	statements, err := d.capture(func() error {
		if err := d.structure(l.Header, latch.Start); err != nil {
			return err
		}
		if d.visited[latch.Start] {
			return bail("unstructured control flow")
		}
		d.visited[latch.Start] = true
		// The latch holds the last of the body and then the test, which is what
		// the instructions before its branch leave on the stack.
		last := latch.Instructions[len(latch.Instructions)-1]
		if err := d.runInstructions(latch.Instructions[:len(latch.Instructions)-1], last.Pc, latch.Start); err != nil {
			return err
		}
		var taken []int
		jump, err := d.jumpConditionOf(latch, &taken, nil)
		if err != nil {
			return err
		}
		for _, start := range taken {
			d.visited[start] = true
		}
		if jump.Target != l.Header || jump.Fallthrough != l.Follow {
			return bail("an unstructured loop")
		}
		condition = jump.Condition
		return nil
	})
	if err != nil {
		return 0, err
	}
	d.emit(labelPrefix(d.active[len(d.active)-1].Label) + "do {")
	*d.current = append(*d.current, stmt{Nested: &statements})
	d.emitCondition(condition, doWhileWrap)
	return l.Follow, nil
}

// foreverLoop writes `while (true) { ... }`: nothing at the head decides whether
// to go round again.
func (d *bodyDecompiler) foreverLoop(l *loop) (int, error) {
	d.active = append(d.active, activeLoop{Loop: l, ContinueTarget: l.Header, Continues: true})
	defer func() { d.active = d.active[:len(d.active)-1] }()
	statements, err := d.capture(func() error { return d.structureFrom(l.Header, exitBlock, true) })
	if err != nil {
		return 0, err
	}
	statements = trimTail(statements, "continue;")
	d.emit(labelPrefix(d.active[len(d.active)-1].Label) + "while (true) {")
	*d.current = append(*d.current, stmt{Nested: &statements})
	d.emit("}")
	return l.Follow, nil
}

// switchFollowOf is where a `switch` ends when its cases do not all come back
// together: a case that returns leaves the post-dominator at exitBlock, and what
// follows the statement is then the first block the switch as a whole leads to
// that no single case owns.
func (d *bodyDecompiler) switchFollowOf(b *block, cases []int, defaultTarget int, orDefaultBody bool) int {
	// A jump that leaves an enclosing loop is that loop's, not this statement's:
	// taking it for the follow would write a `break` that breaks the wrong one.
	barriers := map[int]bool{}
	for _, entered := range d.active {
		barriers[entered.ContinueTarget] = true
		barriers[entered.Loop.Follow] = true
	}
	// Only a `try` needs the dominators up front; a `switch` this deep into the
	// rules is rare enough to pay for them here instead of in every method.
	if len(d.dominators) == 0 {
		d.dominators = dominators(withExceptionEdges(d.blocks, d.regions, d.monitors), d.entryPc)
	}
	reachable := reachableBlocks(d.blocks, append(append([]int{}, cases...), defaultTarget)...)
	leadsTo := func(owners []int) int {
		follow := exitBlock
		for start := range reachable {
			above := d.dominators[start]
			if start <= b.Start || barriers[start] || above == nil || !above[b.Start] {
				continue
			}
			owned := false
			for _, owner := range owners {
				if above[owner] {
					owned = true
					break
				}
			}
			if owned || (follow != exitBlock && start >= follow) {
				continue
			}
			follow = start
		}
		return follow
	}
	merged := leadsTo(append(append([]int{}, cases...), defaultTarget))
	if merged != exitBlock || !orDefaultBody {
		return merged
	}
	// A `switch` with no `default` of its own ends where the table's default
	// sends it - which the pass above ruled out as the default's own body. A
	// default that a case falls into is still ruled out, by that case.
	return leadsTo(cases)
}

// switchStatement writes one `switch`, from the table that ends b, and reports
// where the statement after it begins.
func (d *bodyDecompiler) switchStatement(b *block, stop int) (int, error) {
	table := b.Instructions[len(b.Instructions)-1]
	selector, err := d.pop()
	if err != nil {
		return 0, err
	}
	// The selector is an int, so a condition javac materialized as `1`/`0` has
	// to become the ternary again - `switch (flag)` is not Java.
	selector, err = d.asNumber(selector)
	if err != nil {
		return 0, err
	}
	if len(d.stack) > 0 {
		return 0, bail("values left on the stack")
	}
	// javac compiles a `switch` over an enum into a lookup through a synthetic
	// `$SwitchMap$` array held by an *anonymous* class - which has no name source
	// can write, so the reconstruction would not compile. Restoring the
	// `case CONSTANT:` form needs that holder's initializer, in another file.
	if strings.Contains(selector.Text, "$SwitchMap$") {
		return 0, bail("an enum switch")
	}
	defaultTarget := table.Arg
	// Every key that lands on the same block is one list of labels. A key that
	// lands on the default is dropped: a tableswitch pads its gaps that way, and
	// a case that shares the default's body says nothing a `default:` does not.
	keysOf := map[int][]int{}
	seen := map[int]bool{}
	var cases []int
	for _, entry := range table.SwitchCases {
		// A repeated key is not a table javac wrote, and source cannot say it
		// twice, so the reconstruction would not compile.
		if seen[entry.Key] {
			return 0, bail("a switch table with a repeated key")
		}
		seen[entry.Key] = true
		if entry.Target == defaultTarget {
			continue
		}
		if _, seen := keysOf[entry.Target]; !seen {
			cases = append(cases, entry.Target)
		}
		keysOf[entry.Target] = append(keysOf[entry.Target], entry.Key)
	}
	targets := append(append([]int{}, cases...), defaultTarget)
	sort.Ints(targets)
	if targets[0] <= b.Start {
		return 0, bail("a switch that jumps backwards")
	}

	follow, ok := d.followOf[b.Start]
	if !ok {
		follow = exitBlock
	}
	// The merge can sit outside this statement: a case that returns makes the
	// post-dominator exitBlock, while the rest still comes back together where
	// the statement around this one ends. That only counts when no case begins
	// past it: a loop's `stop` is its header, behind them. A case *on* it is the
	// end of this statement, which is `case k: break;`.
	if follow == exitBlock && targets[len(targets)-1] <= stop {
		for _, target := range targets {
			if d.reachesArm(target, stop) {
				follow = stop
				break
			}
		}
	}
	if follow == exitBlock {
		follow = d.switchFollowOf(b, cases, defaultTarget, true)
	}
	// A `continue` in one case skips the end of the statement, which leaves both
	// the post-dominator and the end of the statement around this one on the
	// loop's own edge instead of on it. Where it really ends is then the block
	// every case leads to - which is never that edge. (Only that strict pass: the
	// `default`'s own body ends a `switch` that has none, and inside a loop that
	// is not this.)
	if follow != exitBlock {
		onLoopEdge := false
		for _, entered := range d.active {
			if entered.ContinueTarget == follow || entered.Loop.Follow == follow {
				onLoopEdge = true
				break
			}
		}
		if onLoopEdge {
			if earlier := d.switchFollowOf(b, cases, defaultTarget, false); earlier != exitBlock && earlier < follow {
				follow = earlier
			}
		}
	}
	// javac lays the case bodies out in one run before the end of the statement,
	// so a candidate that sits between them ends nothing: the statement has no
	// follow at all then, and every case runs into the next or leaves on its own.
	if follow != exitBlock && targets[len(targets)-1] > follow {
		follow = exitBlock
	}
	// A case that lands on the end of the statement has no body: it is
	// `case k: break;`, written last so nothing can fall into it.
	var bodies []int
	for _, target := range targets {
		if target != follow {
			bodies = append(bodies, target)
		}
	}

	// A `switch` whose every case leaves one value where they all come back
	// together - or throws - is a switch expression, and the value is what the
	// code after the merge picks up.
	if follow != exitBlock && len(bodies) == len(targets) {
		value, found, err := d.trySwitchExpression(selector, bodies, keysOf, defaultTarget, follow)
		if err != nil {
			return 0, err
		}
		if found {
			d.push(value)
			return follow, nil
		}
	}

	d.switches = append(d.switches, activeSwitch{Follow: follow, LoopDepth: len(d.active)})
	// The bodyless cases come first: nothing can fall into them there, and their
	// own `break;` keeps them from falling into the first body.
	clauses := []stmt{}
	for _, target := range targets {
		if target != follow {
			continue
		}
		for _, key := range keysOf[target] {
			clauses = append(clauses, stmt{Text: fmt.Sprintf("case %d:", key)})
		}
		if len(keysOf[target]) > 0 {
			broke := []stmt{{Text: "break;"}}
			clauses = append(clauses, stmt{Nested: &broke})
		}
	}
	err = func() error {
		defer func() { d.switches = d.switches[:len(d.switches)-1] }()
		for i, target := range bodies {
			// Running off the end of one case is a fallthrough into the next, so
			// a body stops where the next one begins.
			end := follow
			if i+1 < len(bodies) {
				end = bodies[i+1]
			}
			for _, key := range keysOf[target] {
				clauses = append(clauses, stmt{Text: fmt.Sprintf("case %d:", key)})
			}
			if target == defaultTarget {
				clauses = append(clauses, stmt{Text: "default:"})
			}
			statements, err := d.capture(func() error { return d.structure(target, end) })
			if err != nil {
				return err
			}
			clauses = append(clauses, stmt{Nested: &statements})
			if len(d.stack) > 0 {
				return bail("values left on the stack")
			}
		}
		return nil
	}()
	if err != nil {
		return 0, err
	}
	d.emit("switch (" + selector.Text + ") {")
	*d.current = append(*d.current, stmt{Nested: &clauses})
	d.emit("}")
	return follow, nil
}

// trySwitchExpression writes the cases as `case k -> value;` arms when each is
// a single value left for the merge, or a `throw`. Nothing is kept of a try
// that does not fit.
func (d *bodyDecompiler) trySwitchExpression(selector expr, bodies []int, keysOf map[int][]int, defaultTarget, follow int) (expr, bool, error) {
	stackBefore := append([]expr(nil), d.stack...)
	statements := d.current
	statementsBefore := len(*statements)
	visitedBefore := map[int]bool{}
	for k, v := range d.visited {
		visitedBefore[k] = v
	}
	restore := func() {
		d.stack = stackBefore
		*statements = (*statements)[:statementsBefore]
		d.visited = visitedBefore
	}
	consumed := []int{}
	arms := []string{}
	values := []expr{}
	for _, target := range bodies {
		label := "default"
		if target != defaultTarget {
			keys := make([]string, len(keysOf[target]))
			for i, key := range keysOf[target] {
				keys[i] = strconv.Itoa(key)
			}
			label = "case " + strings.Join(keys, ", ")
		}
		b := d.blocks[target]
		if b != nil && len(b.Instructions) > 0 && b.Instructions[len(b.Instructions)-1].Mnemonic == "athrow" {
			captured, err := d.capture(func() error { return d.structure(target, follow) })
			if err != nil {
				restore()
				return expr{}, false, err
			}
			lines := flattenStatements(captured)
			if len(lines) != 1 || !strings.HasPrefix(lines[0], "throw ") {
				restore()
				return expr{}, false, nil
			}
			arms = append(arms, label+" -> "+lines[0])
			continue
		}
		value, found, err := d.valueOfRegion(target, follow, &consumed)
		if err != nil {
			restore()
			return expr{}, false, err
		}
		if !found || len(d.stack) != len(stackBefore) {
			restore()
			return expr{}, false, nil
		}
		values = append(values, value)
		arms = append(arms, label+" -> %s;")
	}
	if len(values) == 0 || len(*statements) != statementsBefore {
		restore()
		return expr{}, false, nil
	}
	for _, start := range consumed {
		d.visited[start] = true
	}
	// The value's type is the arms' common one, as for a conditional; arms that
	// are all a boolean javac erased make a boolean, with the int form kept.
	typ := values[0].Type
	untyped := false
	allErased, anyBoolean := true, false
	for _, value := range values {
		if !erasedBoolean(value) {
			allErased = false
			if value.Type == "boolean" {
				anyBoolean = true
			}
		}
		if value.Type == typ {
			continue
		}
		left, right := widthOf(typ), widthOf(value.Type)
		switch {
		case left >= 0 && right >= 0 && right > left:
			typ = value.Type
		case value.Text == "null":
		case typ == "java.lang.Object" || value.Type == "java.lang.Object":
			typ = "java.lang.Object"
		case !primitiveTypeNames[typ] && !primitiveTypeNames[value.Type]:
			untyped = true
		}
	}
	render := func(arm func(expr) string) string {
		texts := []string{}
		next := 0
		for _, one := range arms {
			if strings.HasSuffix(one, " -> %s;") {
				texts = append(texts, strings.TrimSuffix(one, "%s;")+arm(values[next])+";")
				next++
			} else {
				texts = append(texts, one)
			}
		}
		return "switch (" + selector.Text + ") { " + strings.Join(texts, " ") + " }"
	}
	if allErased {
		return expr{
			Text:  render(func(v expr) string { return asBoolean(v).Text }),
			Prec:  precPrimary,
			Type:  "boolean",
			AsInt: render(func(v expr) string { return numeric(v).Text }),
		}, true, nil
	}
	// An arm that is a boolean and nothing else makes the others' `1`/`0` the
	// `true`/`false` they were.
	if anyBoolean {
		for _, value := range values {
			if value.Type != "boolean" && !erasedBoolean(value) {
				restore()
				return expr{}, false, nil
			}
		}
		return expr{Text: render(func(v expr) string { return asBoolean(v).Text }), Prec: precPrimary, Type: "boolean", Effects: true}, true, nil
	}
	return expr{
		Text:    render(func(v expr) string { return v.Text }),
		Prec:    precPrimary,
		Type:    typ,
		Untyped: untyped,
		Effects: true,
	}, true, nil
}

// conditional writes one `if`, from the branch that ends b, and reports where
// the statement after it begins.
func (d *bodyDecompiler) conditional(b *block, stop int) (int, error) {
	var taken []int
	jump, err := d.jumpConditionOf(b, &taken, nil)
	if err != nil {
		return 0, err
	}
	for _, start := range taken {
		d.visited[start] = true
	}
	// javac emits the arms in source order, so the `then` is whichever comes
	// first; the branch is written to select it.
	condition := negate(jump.Condition)
	whenTrue, whenFalse := jump.Fallthrough, jump.Target
	if jump.Target < jump.Fallthrough {
		condition = jump.Condition
		whenTrue, whenFalse = jump.Target, jump.Fallthrough
	}
	merge, ok := d.followOf[b.Start]
	if !ok {
		merge = exitBlock
	}

	if merge != exitBlock {
		value, found, err := d.tryTernary(condition, whenTrue, whenFalse, merge)
		if err != nil {
			return 0, err
		}
		if found {
			d.push(value)
			return merge, nil
		}
	}
	// An arm that flows into the other one is an `if` without an `else`: the
	// second arm is what follows the statement, not a branch of it. The merge
	// point cannot say so when the first arm also ends in a `return`, a `break`
	// or a `continue` - those paths never reach it.
	follow := merge
	if d.reachesArm(whenTrue, whenFalse) {
		follow = whenFalse
	}
	// Inside a `try` the merge can sit outside the statement: a `return` on one
	// path makes the post-dominator exitBlock, but the arms still come back
	// together where the statement around this one ends.
	if follow == exitBlock && stop != exitBlock &&
		(d.reachesArm(whenTrue, stop) || d.reachesArm(whenFalse, stop)) {
		follow = stop
	}
	// `if (c) return x;` has no merge point. Both arms leave the method - every
	// path does, since a block that runs off the end is rejected - so the arm
	// that branches is the whole statement and the other one is what follows it,
	// at the same level rather than inside an `else`.
	if follow == exitBlock {
		// The arm still ends where the statement around it does: `stop` is the
		// end of the `try` or the loop body this `if` sits in, not always the
		// method.
		exiting, err := d.capture(func() error { return d.structure(whenTrue, stop) })
		if err != nil {
			return 0, err
		}
		d.pushIf(condition, exiting, nil)
		return whenFalse, nil
	}
	thenStatements, err := d.capture(func() error { return d.structure(whenTrue, follow) })
	if err != nil {
		return 0, err
	}
	elseStatements, err := d.capture(func() error { return d.structure(whenFalse, follow) })
	if err != nil {
		return 0, err
	}
	if len(thenStatements) == 0 && len(elseStatements) > 0 {
		d.pushIf(negate(condition), elseStatements, nil)
		return follow, nil
	}
	d.pushIf(condition, thenStatements, elseStatements)
	return follow, nil
}

func (d *bodyDecompiler) pushIf(condition expr, thenStatements, elseStatements []stmt) {
	d.emitCondition(condition, ifWrap)
	*d.current = append(*d.current, stmt{Nested: &thenStatements})
	if len(elseStatements) > 0 {
		d.emit("} else {")
		*d.current = append(*d.current, stmt{Nested: &elseStatements})
	}
	d.emit("}")
}

// jump is a conditional branch, once the tests that belong with it are folded in.
type jump struct {
	Condition   expr
	Target      int
	Fallthrough int
}

// jumpConditionOf reports the condition under which b's branch is taken, with
// any further tests that belong to the same source condition folded in: javac
// lays a short-circuit out as a chain of branches that share their outcomes. The
// blocks folded away are appended to taken, for the caller to account for.
func (d *bodyDecompiler) jumpConditionOf(b *block, taken *[]int, folded map[int]bool) (jump, error) {
	inChain := folded
	if inChain == nil {
		inChain = map[int]bool{b.Start: true}
	}
	condition, err := d.branchExpr(b.Instructions[len(b.Instructions)-1])
	if err != nil {
		return jump{}, err
	}
	target, fallthrough_ := b.Successors[1], b.Successors[0]
	for {
		merged := false
		// The shortest fold first: a test that carries its own chain may not line
		// up with this one, while the single branch at its head does.
		for _, deep := range []bool{false, true} {
			// A test on the *fallthrough* path shares an outcome with this one:
			// `a || b` when both jump to the same place, `a || !b` when the
			// second falls into where the first jumped.
			onFall, found, undo, err := d.chainFrom(fallthrough_, taken, inChain, deep)
			if err != nil {
				return jump{}, err
			}
			if found {
				if onFall.Target == target || onFall.Fallthrough == target {
					second := onFall.Condition
					if onFall.Target != target {
						second = negate(second)
					}
					condition = logicalExpr(logicOr, condition, second)
					if onFall.Target == target {
						fallthrough_ = onFall.Fallthrough
					} else {
						fallthrough_ = onFall.Target
					}
					merged = true
					break
				}
				undo()
			}
			// A test on the *target* path: landing on the fallthrough now means
			// either this branch was not taken, or the second one sent us there.
			onTarget, found, undo, err := d.chainFrom(target, taken, inChain, deep)
			if err != nil {
				return jump{}, err
			}
			if found {
				if onTarget.Target == fallthrough_ || onTarget.Fallthrough == fallthrough_ {
					second := onTarget.Condition
					jumpsBack := onTarget.Target == fallthrough_
					if !jumpsBack {
						second = negate(second)
					}
					condition = logicalExpr(logicOr, negate(condition), second)
					target = fallthrough_
					if jumpsBack {
						fallthrough_ = onTarget.Fallthrough
					} else {
						fallthrough_ = onTarget.Target
					}
					merged = true
					break
				}
				undo()
			}
		}
		if !merged {
			return jump{Condition: condition, Target: target, Fallthrough: fallthrough_}, nil
		}
	}
}

// chainFrom reports the branch start amounts to once its own chain is folded, or
// nothing when it is not a test that belongs to this condition. It is
// speculative: undo puts everything back when the outcomes do not line up.
func (d *bodyDecompiler) chainFrom(
	start int,
	taken *[]int,
	folded map[int]bool,
	deep bool,
) (jump, bool, func(), error) {
	next := d.blocks[start]
	if next == nil || next.Kind != blockConditional || !isConditionBlock(next) {
		return jump{}, false, nil, nil
	}
	if folded[start] || d.visited[start] {
		return jump{}, false, nil, nil
	}
	// A loop's own test is a statement, not a term of the condition in front of it.
	if d.isLoopEdge(start) {
		return jump{}, false, nil, nil
	}
	// Nothing outside the chain may reach it, or folding would skip a path in.
	if d.predecessorsOf(start, folded) != 0 {
		return jump{}, false, nil, nil
	}
	stackBefore := append([]expr(nil), d.stack...)
	statementsBefore := len(*d.current)
	takenCount := len(*taken)
	foldedBefore := make([]int, 0, len(folded))
	for at := range folded {
		foldedBefore = append(foldedBefore, at)
	}
	folded[start] = true
	*taken = append(*taken, start)
	last := next.Instructions[len(next.Instructions)-1]
	if err := d.runInstructions(next.Instructions[:len(next.Instructions)-1], last.Pc, start); err != nil {
		return jump{}, false, nil, err
	}
	var folded_ jump
	var err error
	if deep {
		folded_, err = d.jumpConditionOf(next, taken, folded)
	} else {
		var condition expr
		condition, err = d.branchExpr(last)
		folded_ = jump{Condition: condition, Target: next.Successors[1], Fallthrough: next.Successors[0]}
	}
	if err != nil {
		return jump{}, false, nil, err
	}
	undo := func() {
		d.stack = stackBefore
		*d.current = (*d.current)[:statementsBefore]
		*taken = (*taken)[:takenCount]
		for at := range folded {
			delete(folded, at)
		}
		for _, at := range foldedBefore {
			folded[at] = true
		}
	}
	// A statement means the block did more than compute a value - a call whose
	// result is dropped, say - and folding it into a condition would move it.
	if len(*d.current) != statementsBefore {
		undo()
		return jump{}, false, nil, nil
	}
	return folded_, true, undo, nil
}

func (d *bodyDecompiler) predecessorsOf(start int, ignore map[int]bool) int {
	count := 0
	for _, b := range d.blocks {
		if ignore[b.Start] {
			continue
		}
		for _, successor := range b.Successors {
			if successor == start {
				count++
				break
			}
		}
	}
	return count
}

// branchExpr reports the source condition a branch instruction tests, with its
// operands popped.
func (d *bodyDecompiler) branchExpr(instruction Instruction) (expr, error) {
	mnemonic := instruction.Mnemonic
	if mnemonic == "ifnull" || mnemonic == "ifnonnull" {
		value, err := d.pop()
		if err != nil {
			return expr{}, err
		}
		op := "!="
		if mnemonic == "ifnull" {
			op = "=="
		}
		return compareExpr(value, op, primary("null", "java.lang.Object")), nil
	}
	if mnemonic == "if_acmpeq" || mnemonic == "if_acmpne" {
		right, err := d.pop()
		if err != nil {
			return expr{}, err
		}
		left, err := d.pop()
		if err != nil {
			return expr{}, err
		}
		op := "!="
		if mnemonic == "if_acmpeq" {
			op = "=="
		}
		return compareExpr(left, op, right), nil
	}
	suffix := strings.TrimPrefix(strings.Replace(mnemonic, "if_icmp", "if", 1), "if")
	op, ok := comparisons[suffix]
	if !ok {
		return expr{}, bail("unsupported branch %s", mnemonic)
	}
	if strings.HasPrefix(mnemonic, "if_icmp") {
		right, err := d.pop()
		if err != nil {
			return expr{}, err
		}
		left, err := d.pop()
		if err != nil {
			return expr{}, err
		}
		// `==` and `!=` are the only comparisons a boolean takes; on one, both
		// sides are booleans and a materialized one keeps its own text.
		if op == "==" || op == "!=" {
			if left, err = d.provenBoolean(left, right); err != nil {
				return expr{}, err
			}
			if right, err = d.provenBoolean(right, left); err != nil {
				return expr{}, err
			}
			if l, r, ok := booleanOperands(left, right); ok {
				return compareExpr(l, op, r), nil
			}
		}
		// A variable whose type is still open beside a materialized condition
		// is `b == (x > 3)` as much as `n == (x > 3 ? 1 : 0)`, and the text has
		// to be one of them now.
		if op == "==" || op == "!=" {
			for _, pair := range [2][2]expr{{left, right}, {right, left}} {
				if entry, ok := d.byName[pair[0].Text]; ok && !entry.Authoritative && pair[1].AsInt != "" {
					return expr{}, bail("a variable compared with a materialized boolean")
				}
			}
		}
		// Not a comparison of two booleans, then: both sides are numbers -
		// unless one side is the `0`/`1` a boolean may still turn out to be
		// compared with.
		if (op != "==" && op != "!=") || (!erasedBoolean(left) && !erasedBoolean(right)) {
			if err := d.usedAsNumber(left); err != nil {
				return expr{}, err
			}
			if err := d.usedAsNumber(right); err != nil {
				return expr{}, err
			}
		}
		return compareExpr(numeric(left), op, numeric(right)), nil
	}
	value, err := d.popRaw()
	if err != nil {
		return expr{}, err
	}
	// `lcmp`/`fcmpl`/`dcmpg` only exist to feed one of these: what source wrote
	// is the comparison of their two operands.
	if value.Compared != nil {
		return compareExpr(value.Compared.Left, op, value.Compared.Right), nil
	}
	if value.Type == "boolean" && (op == "==" || op == "!=") {
		if op == "!=" {
			return value, nil
		}
		return negate(value), nil
	}
	// `ifeq` is how a boolean is tested too; an ordering is a number's alone.
	if op != "==" && op != "!=" {
		if err := d.usedAsNumber(value); err != nil {
			return expr{}, err
		}
	}
	return compareExpr(numeric(value), op, intLiteral(0)), nil
}

// tryTernary writes the two arms of a branch as `condition ? a : b`, when both
// are side-effect-free and leave one value behind - which is how javac writes a
// conditional expression, and how a boolean ends up in a variable.
func (d *bodyDecompiler) tryTernary(condition expr, whenTrue, whenFalse, follow int) (expr, bool, error) {
	consumed := []int{}
	before := append([]expr(nil), d.stack...)
	statements := d.current
	statementsBefore := len(*statements)
	value, found, err := d.armValues(condition, whenTrue, whenFalse, follow, &consumed)
	if err != nil {
		return expr{}, false, err
	}
	// A statement means an arm did more than compute a value - a call whose
	// result is dropped, say - and it would end up in front of the `?:`.
	if !found || len(*statements) != statementsBefore {
		// An arm may have consumed values that were already on the stack, so the
		// depth alone does not put it back.
		d.stack = before
		*statements = (*statements)[:statementsBefore]
		return expr{}, false, nil
	}
	for _, start := range consumed {
		d.visited[start] = true
	}
	return value, true, nil
}

func (d *bodyDecompiler) armValues(
	condition expr,
	whenTrue, whenFalse, follow int,
	consumed *[]int,
) (expr, bool, error) {
	thenValue, found, err := d.valueOfRegion(whenTrue, follow, consumed)
	if err != nil || !found {
		return expr{}, false, err
	}
	elseValue, found, err := d.valueOfRegion(whenFalse, follow, consumed)
	if err != nil || !found {
		return expr{}, false, err
	}
	// `c ? 1 : 0` is a boolean that javac erased to an int; source wrote the
	// condition itself.
	if thenValue.Text == "1" && elseValue.Text == "0" {
		return materializedBoolean(condition, condition, "1"), true, nil
	}
	if thenValue.Text == "0" && elseValue.Text == "1" {
		return materializedBoolean(negate(condition), condition, "0"), true, nil
	}
	value, ok := ternaryExpr(condition, thenValue, elseValue)
	return value, ok, nil
}

// valueOfRegion reports the blocks from start to follow as a single value:
// either one side-effect-free block that leaves it on the stack, or - because a
// short-circuit nests them - another branch whose arms are values themselves.
func (d *bodyDecompiler) valueOfRegion(start, follow int, consumed *[]int) (expr, bool, error) {
	b := d.blocks[start]
	if b == nil {
		return expr{}, false, nil
	}
	// A block the two arms share (the merge of a `||`) is taken twice, so it may
	// only be one that has no side effects - then evaluating it twice is the
	// same value twice. An arm of its own may call something.
	if !isPureBlock(b) && (containsInt(*consumed, start) || !isConditionBlock(b)) {
		return expr{}, false, nil
	}
	if d.isLoopEdge(start) {
		return expr{}, false, nil
	}
	// A block already emitted as a statement cannot also be a value; one that two
	// arms of the same expression share (the merge of a `||`) is fine - it has no
	// side effects, so evaluating it twice is the same value twice.
	if d.visited[start] {
		return expr{}, false, nil
	}
	terminator := b.Instructions[len(b.Instructions)-1]
	if b.Kind == blockConditional {
		if follow_, ok := d.followOf[start]; !ok || follow_ != follow {
			return expr{}, false, nil
		}
		if err := d.runInstructions(b.Instructions[:len(b.Instructions)-1], terminator.Pc, start); err != nil {
			return expr{}, false, err
		}
		inner, err := d.jumpConditionOf(b, consumed, nil)
		if err != nil {
			return expr{}, false, err
		}
		*consumed = append(*consumed, start)
		condition := negate(inner.Condition)
		armTrue, armFalse := inner.Fallthrough, inner.Target
		if inner.Target < inner.Fallthrough {
			condition = inner.Condition
			armTrue, armFalse = inner.Target, inner.Fallthrough
		}
		return d.armValues(condition, armTrue, armFalse, follow, consumed)
	}
	if len(b.Successors) != 1 || b.Successors[0] != follow {
		return expr{}, false, nil
	}
	before := len(d.stack)
	body := b.Instructions
	if b.Kind == blockGoto {
		body = body[:len(body)-1]
	}
	if err := d.runInstructions(body, endOf(b), start); err != nil {
		return expr{}, false, err
	}
	if len(d.stack) != before+1 {
		return expr{}, false, nil
	}
	*consumed = append(*consumed, start)
	value, err := d.pop()
	if err != nil {
		return expr{}, false, err
	}
	return value, true, nil
}

// reachesAvoiding reports whether a path from the entry reaches target without
// passing a store.
func (d *bodyDecompiler) reachesAvoiding(target int, stores map[int]bool) bool {
	// A store in the reading block itself comes first: the read is what follows
	// it, so that path is covered.
	if stores[target] {
		return false
	}
	seen := map[int]bool{}
	queue := []int{d.entryPc}
	for len(queue) > 0 {
		at := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[at] || stores[at] || d.blocks[at] == nil {
			continue
		}
		if at == target {
			return true
		}
		seen[at] = true
		queue = append(queue, d.blocks[at].Successors...)
	}
	return false
}

// sharesRead reports whether the variable's stores and a store to its slot at
// pc flow to one read of the slot - a read reachable from both before another
// store. Two arms of one `if` that assign one variable read it after the join;
// two variables of their own in the slot - one per arm, or a dead one before a
// new one - never reach one read together: javac keeps a variable assigned on
// every path to where it is read. That is the one sign, without a debug table,
// that two stores are one variable.
func (d *bodyDecompiler) sharesRead(entry *local, slot, pc int) bool {
	if len(entry.StoreBlocks) == 0 {
		return false
	}
	theirs := map[int]bool{}
	for start := range entry.StoreBlocks {
		if start == d.currentBlock {
			continue
		}
		for read := range d.readsBeforeStore(d.blocks[start].Successors, slot) {
			theirs[read] = true
		}
	}
	if len(theirs) == 0 {
		return false
	}
	// This block's own tail after the store, then what follows it.
	block := d.blocks[d.currentBlock]
	if block == nil {
		return false
	}
	for _, instruction := range block.Instructions {
		if instruction.Pc <= pc {
			continue
		}
		base := opBase(instruction.Mnemonic)
		if (isOneOf(base, "ilfda", "load") || base == "iinc") && slotOf(instruction) == slot && theirs[instruction.Pc] {
			return true
		}
		if isOneOf(base, "ilfda", "store") && slotOf(instruction) == slot {
			return false
		}
	}
	for read := range d.readsBeforeStore(block.Successors, slot) {
		if theirs[read] {
			return true
		}
	}
	return false
}

// readsBeforeStore is the pcs of the loads of the slot some path from the
// starts reaches before storing to it.
func (d *bodyDecompiler) readsBeforeStore(starts []int, slot int) map[int]bool {
	reads := map[int]bool{}
	seen := map[int]bool{}
	queue := append([]int(nil), starts...)
	for len(queue) > 0 {
		at := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[at] || d.blocks[at] == nil {
			continue
		}
		seen[at] = true
		stored := false
		for _, instruction := range d.blocks[at].Instructions {
			base := opBase(instruction.Mnemonic)
			if (isOneOf(base, "ilfda", "load") || base == "iinc") && slotOf(instruction) == slot {
				reads[instruction.Pc] = true
			}
			if isOneOf(base, "ilfda", "store") && slotOf(instruction) == slot {
				stored = true
				break
			}
		}
		if !stored {
			queue = append(queue, d.blocks[at].Successors...)
		}
	}
	return reads
}

func (d *bodyDecompiler) runInstructions(instructions []Instruction, endPc, blockStart int) error {
	outer := d.currentBlock
	d.currentBlock = blockStart
	err := d.runSteps(instructions, endPc)
	d.currentBlock = outer
	return err
}

func (d *bodyDecompiler) runSteps(steps []Instruction, endPc int) error {
	instructions := withoutNullChecks(steps, d.classFile.Pool)
	for i, instruction := range instructions {
		// A store's variable comes into scope after the store, so the debug table
		// is searched at the next instruction's pc, not the store's own.
		nextPc := endPc
		if i+1 < len(instructions) {
			nextPc = instructions[i+1].Pc
		}
		var previous, next *Instruction
		if i > 0 {
			previous = &instructions[i-1]
		}
		if i+1 < len(instructions) {
			next = &instructions[i+1]
		}
		if err := d.step(instruction, nextPc, previous, next); err != nil {
			return err
		}
	}
	return nil
}

// withoutNullChecks drops the null check javac writes for a qualifying
// instance - `outer.new Inner()`, `outer.super()`, the enclosing instance an
// inner class stores, `x::m` - as `dup; requireNonNull; pop` (`getClass` before
// Java 9). Source has no such statement: the construct it belongs to brings it
// back when compiled.
func withoutNullChecks(instructions []Instruction, pool []*Constant) []Instruction {
	out := make([]Instruction, 0, len(instructions))
	for i := 0; i < len(instructions); i++ {
		// A bound method reference `x::m` checks its receiver the same way, but is
		// written back as a lambda, which checks nothing: that one stays a
		// statement.
		if instructions[i].Mnemonic == "dup" && i+2 < len(instructions) && instructions[i+2].Mnemonic == "pop" &&
			(i+3 >= len(instructions) || instructions[i+3].Mnemonic != "invokedynamic") {
			call := instructions[i+1]
			if call.Mnemonic == "invokestatic" || call.Mnemonic == "invokevirtual" {
				target, ok := PoolMemberRef(pool, uint16(call.Arg))
				check := ok && ((target.Owner == "java/util/Objects" && target.Name == "requireNonNull" &&
					target.Descriptor == "(Ljava/lang/Object;)Ljava/lang/Object;") ||
					(target.Owner == "java/lang/Object" && target.Name == "getClass" &&
						target.Descriptor == "()Ljava/lang/Class;"))
				if check {
					i += 2
					continue
				}
			}
		}
		out = append(out, instructions[i])
	}
	return out
}

func isOneOf(base string, prefixes string, suffix string) bool {
	return len(base) == len(suffix)+1 && strings.HasSuffix(base, suffix) &&
		strings.IndexByte(prefixes, base[0]) >= 0
}

func (d *bodyDecompiler) step(
	instruction Instruction, nextPc int, previous, next *Instruction,
) error {
	mnemonic, pc := instruction.Mnemonic, instruction.Pc
	pool := d.classFile.Pool

	// Constants.
	switch {
	case mnemonic == "nop":
		return nil
	case mnemonic == "aconst_null":
		d.push(primary("null", "java.lang.Object"))
		return nil
	case strings.HasPrefix(mnemonic, "iconst_"):
		value := 0
		if mnemonic == "iconst_m1" {
			value = -1
		} else {
			value, _ = strconv.Atoi(mnemonic[7:])
		}
		d.push(intLiteral(value))
		return nil
	case strings.HasPrefix(mnemonic, "lconst_"):
		d.push(primary(mnemonic[7:]+"L", "long"))
		return nil
	case strings.HasPrefix(mnemonic, "fconst_"):
		value, _ := strconv.ParseFloat(mnemonic[7:], 32)
		d.push(primary(JavaFloatText(float32(value))+"f", "float"))
		return nil
	case strings.HasPrefix(mnemonic, "dconst_"):
		value, _ := strconv.ParseFloat(mnemonic[7:], 64)
		d.push(primary(JavaDoubleText(value), "double"))
		return nil
	case mnemonic == "bipush" || mnemonic == "sipush":
		d.push(intLiteral(instruction.Arg))
		return nil
	case mnemonic == "ldc" || mnemonic == "ldc_w" || mnemonic == "ldc2_w":
		constant, err := constantExpr(pool, uint16(instruction.Arg), d.self())
		if err != nil {
			return err
		}
		d.push(constant)
		return nil
	}

	// Loads and stores.
	base := opBase(mnemonic)
	// The monitor of a `synchronized` lives in a synthetic local: source never
	// named it, and reading it back is part of the release, not a statement.
	if mnemonic == "monitorexit" {
		_, err := d.pop()
		return err
	}
	if isOneOf(base, "ilfda", "load") {
		slot := slotOf(instruction)
		if base == "aload" && slices.Contains(d.monitorSlots, slot) {
			d.push(primary("null", "java.lang.Object"))
			return nil
		}
		if base == "aload" && slot == 0 && !d.isStatic {
			d.push(primary("this", d.self()))
			return nil
		}
		target, err := d.local(slot, pc, primitiveOfPrefix[base[0]], false)
		if err != nil {
			return err
		}
		target.Reads++
		d.push(primary(target.Name, target.Type))
		return nil
	}
	if isOneOf(base, "ilfda", "store") {
		// `this` is final in source; a class file may still store over slot 0.
		if !d.isStatic && slotOf(instruction) == 0 {
			return bail("the method assigns to `this`")
		}
		value, err := d.pop()
		if err != nil {
			return err
		}
		// `istore` is what a boolean, char, byte and short are stored with too;
		// when the value knows which one it is, that is the variable's type. A
		// condition javac materialized as `1`/`0` does *not* know: `int x = c ? 1
		// : 0` and `boolean b = c` compile to the same store, so it starts as an
		// int and a use that needs a boolean narrows it (as for a literal).
		fallback := primitiveOfPrefix[base[0]]
		if base == "astore" || (erasedToInt[value.Type] && value.AsInt == "") {
			fallback = value.Type
		}
		// A conditional whose arms differ in type has none this could declare
		// a variable with; only a debug table can say what source wrote.
		if value.Untyped {
			fallback = ""
		}
		// `null` is of every reference type: stored into a variable whose scope
		// this is inside of, it does not begin another one - at the same depth
		// the slot may be a dead variable's, reused for a new one. And a
		// variable that has only ever held `null`, and was never read, is of
		// the type the first real value gives it; one that was read is text
		// already, rendered against `Object`, and stays one.
		if base == "astore" {
			if existing, ok := d.locals[slotOf(instruction)]; ok && !existing.Authoritative && !primitiveTypeNames[existing.Type] {
				// The same variable, when this store and the variable's flow to a
				// read together; otherwise the slot may be a dead variable's, or
				// another arm's own.
				same := d.sharesRead(existing, slotOf(instruction), pc)
				switch {
				case value.Text == "null" && same:
					fallback = existing.Type
				// `Object` is every reference type's bound: a variable that holds
				// one and takes another is an Object, and source declared it so
				// (or the Object it held would not have gone in). The other way
				// round, a variable typed by one arm's value is an Object once the
				// other arm stores one - if nothing has read it as the narrower
				// type yet.
				case existing.Type == "java.lang.Object" && fallback != "" && !onlyNull(existing) && same:
					fallback = existing.Type
				case fallback == "java.lang.Object" && value.Text != "null" && existing.Reads == 0 && same:
					if err := d.retype(existing, fallback); err != nil {
						return err
					}
				case value.Untyped && existing.Type == "java.lang.Object" && (same || onlyNull(existing)):
					// Whatever the arms' bound is, an Object holds it.
					fallback = existing.Type
				case existing.Type == "java.lang.Object" && fallback != "java.lang.Object" && fallback != "" &&
					onlyNull(existing) && existing.Reads == 0:
					if err := d.retype(existing, fallback); err != nil {
						return err
					}
				// Two reference types with a bound this cannot compute: one
				// variable, its type open until a use says.
				case fallback != "" && fallback != existing.Type && existing.Type != "java.lang.Object" && value.Text != "null" &&
					!primitiveTypeNames[fallback] && !strings.HasSuffix(fallback, "]") && !strings.HasSuffix(existing.Type, "]") &&
					existing.Reads == 0 && same:
					// A checkcast on either value is the declaration javac matched it
					// to; otherwise the type stays open until a use says.
					bound := "java.lang.Object"
					if value.Cast {
						bound = fallback
					} else if len(existing.Writes) > 0 && existing.Writes[len(existing.Writes)-1].Value.Cast {
						bound = existing.Type
					}
					if err := d.retype(existing, bound); err != nil {
						return err
					}
					existing.Open = bound == "java.lang.Object"
					existing.Tentative = !existing.Open
					fallback = existing.Type
				}
			}
		}
		// A `1`/`0` - or a condition javac materialized as one - stored into a
		// variable that is a boolean is a boolean: `w = !w` in a loop is the
		// same variable, and splitting it would leave every earlier read on a
		// stale one. Where the slot really was reused for an int, the int's
		// first use as a number says so. A variable the debug table typed needs
		// none of this: in its range the table's type wins, and past it the
		// slot is free.
		if fallback == "int" && erasedBoolean(value) {
			if existing, ok := d.locals[slotOf(instruction)]; ok && existing.Type == "boolean" && !existing.Authoritative {
				fallback = "boolean"
			}
		}
		// The same for a char, byte or short: a literal that fits is one of
		// theirs. And the other way round - a variable that has only held such
		// literals, and was never read, takes the type of the first value that
		// knows its own: `c = 'a'` in one arm and `c = s.charAt(0)` in the other
		// are one variable, whichever arm comes first.
		if fallback == "int" && isIntegerText(value.Text) {
			if existing, ok := d.locals[slotOf(instruction)]; ok && !existing.Authoritative &&
				literalFits(value.Text, existing.Type) {
				fallback = existing.Type
			}
		}
		if erasedToInt[fallback] && !erasedBoolean(value) {
			if existing, ok := d.locals[slotOf(instruction)]; ok && !existing.Authoritative &&
				existing.Type == "int" && existing.Reads == 0 && len(existing.Writes) > 0 && allLiteralsFit(existing, fallback) {
				if err := d.retype(existing, fallback); err != nil {
					return err
				}
			}
		}
		// A value that is an int and nothing else - a call that returns one, an
		// arithmetic result, a parameter - makes the variable one: not a `1`/`0`
		// a boolean was erased to, and not another variable whose own type is
		// still open.
		source, isLocal := d.byName[value.Text]
		if base == "istore" && value.Type == "int" && !erasedBoolean(value) &&
			(!isLocal || source.Authoritative) {
			target, err := d.local(slotOf(instruction), nextPc, fallback, true)
			if err != nil {
				return err
			}
			target.Numeric = true
		}
		// And one that is a boolean and nothing else makes it one.
		if base == "istore" && value.Type == "boolean" && !erasedBoolean(value) {
			target, err := d.local(slotOf(instruction), nextPc, fallback, true)
			if err != nil {
				return err
			}
			target.Proven = true
		}
		if d.assignAsValue {
			d.assignAsValue = false
			return d.storeAsValue(slotOf(instruction), nextPc, value, fallback)
		}
		return d.store(slotOf(instruction), nextPc, value, fallback)
	}
	if base == "iinc" {
		target, err := d.local(instruction.Arg, pc, "int", false)
		if err != nil {
			return err
		}
		if err := d.usedAsNumber(primary(target.Name, target.Type)); err != nil {
			return err
		}
		delta := instruction.Arg2
		// The old value being on the stack is what `i++` leaves: javac pushes the
		// variable and increments it behind the value. That is the top of the
		// stack and nothing else - a value under it was computed before the
		// increment, and in `f(i++, i)` it is written to its left, so it still
		// reads the old one. Any other shape needs the increment somewhere source
		// could not have put it.
		onStack := false
		for _, value := range d.stack {
			if reads(withoutLiterals(value.Text), target.Name) {
				onStack = true
			}
		}
		if onStack {
			// The load right before the increment is what put that value there; a
			// `dup` of the same read would leave two of it, and only one of them
			// can carry the `++`.
			loaded := previous != nil && strings.HasPrefix(previous.Mnemonic, "iload") &&
				slotOf(*previous) == instruction.Arg
			top := len(d.stack) - 1
			if (delta != 1 && delta != -1) || top < 0 || d.stack[top].Text != target.Name || !loaded {
				return bail("an increment of a variable that is already on the stack")
			}
			suffix := "++"
			if delta == -1 {
				suffix = "--"
			}
			d.stack[top] = expr{
				Text:    target.Name + suffix,
				Prec:    precPrimary,
				Type:    d.stack[top].Type,
				Effects: true,
			}
			return nil
		}
		switch {
		case delta == 1:
			d.emit(target.Name + "++;")
		case delta == -1:
			d.emit(target.Name + "--;")
		case delta < 0:
			d.emit(fmt.Sprintf("%s -= %d;", target.Name, -delta))
		default:
			d.emit(fmt.Sprintf("%s += %d;", target.Name, delta))
		}
		return nil
	}

	// Arithmetic, bitwise and conversions.
	if operator, ok := binaryOps[mnemonic[1:]]; ok && strings.IndexByte("ilfd", mnemonic[0]) >= 0 {
		right, err := d.pop()
		if err != nil {
			return err
		}
		left, err := d.pop()
		if err != nil {
			return err
		}
		// `|`, `&` and `^` are the only ones a boolean takes, and there the `1`
		// and `0` javac wrote are `true` and `false` - `b | 1` is not Java. But
		// `(a > b) ^ true` and `((a > b) ? 1 : 0) ^ 1` compile to the same
		// thing, and only what consumes the result knows which one source
		// wrote: where every operand is a boolean javac erased, the value
		// carries the int form as well, the way a materialized boolean does.
		if len(operator.operator) == 1 && strings.ContainsAny(operator.operator, "|&^") {
			var err error
			if left, err = d.provenBoolean(left, right); err != nil {
				return err
			}
			if right, err = d.provenBoolean(right, left); err != nil {
				return err
			}
			if l, r, ok := booleanOperands(left, right); ok {
				asBool := binaryExpr(l, operator.operator, r, operator.prec, "boolean")
				if erasedBoolean(left) && erasedBoolean(right) {
					asBool.AsInt = binaryExpr(numeric(left), operator.operator, numeric(right),
						operator.prec, "int").Text
				}
				d.push(asBool)
				return nil
			}
		}
		if left, err = d.asNumber(left); err != nil {
			return err
		}
		if right, err = d.asNumber(right); err != nil {
			return err
		}
		d.push(binaryExpr(left, operator.operator, right,
			operator.prec, primitiveOfPrefix[mnemonic[0]]))
		return nil
	}
	if isOneOf(mnemonic, "ilfd", "neg") {
		value, err := d.pop()
		if err != nil {
			return err
		}
		if value, err = d.asNumber(value); err != nil {
			return err
		}
		// A unary operand needs the parens too: `-(-a)` is not `--a`.
		d.push(expr{Text: "-" + at(value, precUnary+1), Prec: precUnary, Type: primitiveOfPrefix[mnemonic[0]]})
		return nil
	}
	if conversion, ok := conversions[mnemonic]; ok {
		value, err := d.pop()
		if err != nil {
			return err
		}
		if value, err = d.asNumber(value); err != nil {
			return err
		}
		inner := value
		d.push(expr{Text: "(" + conversion + ") " + at(value, precUnary), Prec: precUnary, Type: conversion, Inner: &inner})
		return nil
	}

	// Fields.
	if mnemonic == "getstatic" || mnemonic == "getfield" {
		field, ok := PoolMemberRef(pool, uint16(instruction.Arg))
		if !ok {
			return bail("bad field reference")
		}
		fieldType := descriptorSourceType(field.Descriptor, d.self())
		if mnemonic == "getstatic" {
			d.push(primary(d.staticRef(field.Owner, field.Name), fieldType))
			return nil
		}
		target, err := d.popShared()
		if err != nil {
			return err
		}
		// A variable whose type is still open is of the class the field read
		// from it belongs to.
		if entry, ok := d.byName[target.Text]; ok && (entry.Open || entry.Tentative) {
			if err := d.settle(entry, typeName(field.Owner, d.self()), false); err != nil {
				return err
			}
			target = primary(entry.Name, entry.Type)
		}
		read := primary(at(target, precPrimary)+"."+field.Name, fieldType)
		read.ReadOf = target.Shared
		d.push(read)
		return nil
	}
	if mnemonic == "putstatic" || mnemonic == "putfield" {
		field, ok := PoolMemberRef(pool, uint16(instruction.Arg))
		if !ok {
			return bail("bad field reference")
		}
		value, err := d.pop()
		if err != nil {
			return err
		}
		fieldType := descriptorSourceType(field.Descriptor, d.self())
		target := d.staticRef(field.Owner, field.Name)
		if mnemonic == "putfield" {
			receiver, err := d.popShared()
			if err != nil {
				return err
			}
			target = at(receiver, precPrimary) + "." + field.Name
			if receiver.Shared != 0 {
				return d.compoundAssign(target, receiver.Shared, value, fieldType)
			}
		}
		assigned, err := d.coerceInto(value, fieldType)
		if err != nil {
			return err
		}
		// An assignment the stack still wants stays where the value was, as the
		// expression it is.
		if d.assignFieldAsValue {
			d.assignFieldAsValue = false
			d.push(expr{Text: target + " = " + assigned, Prec: precAssign, Type: fieldType, Effects: true})
			return nil
		}
		// The assignment is a statement here, so it runs *before* everything the
		// stack already holds - and any of those that reads a field, an array or a
		// call could see it. `arr[idx++]`, `p.x + (q.x = 1)` and `f() + (n = 1)`
		// all need the assignment to stay an expression, which this phase does not
		// write. Textual identity is not enough: `q` may be `p`.
		for _, stacked := range d.stack {
			if observesWrites(stacked, d.names) {
				return bail("an assignment with a value that could see it on the stack")
			}
		}
		d.emit(target + " = " + assigned + ";")
		return nil
	}

	// Arrays.
	if mnemonic == "arraylength" {
		array, err := d.pop()
		if err != nil {
			return err
		}
		d.push(primary(at(array, precPrimary)+".length", "int"))
		return nil
	}
	if isOneOf(mnemonic, "ilfdabcs", "aload") {
		index, err := d.popShared()
		if err != nil {
			return err
		}
		array, err := d.popShared()
		if err != nil {
			return err
		}
		if err := d.usedAsNumber(index); err != nil {
			return err
		}
		read := primary(at(array, precPrimary)+"["+coerce(index, "int")+"]", elementType(array.Type, mnemonic[0]))
		if array.Shared != 0 && array.Shared == index.Shared {
			read.ReadOf = array.Shared
		}
		d.push(read)
		return nil
	}
	if isOneOf(mnemonic, "ilfdabcs", "astore") {
		value, err := d.pop()
		if err != nil {
			return err
		}
		index, err := d.popShared()
		if err != nil {
			return err
		}
		array, err := d.popRaw()
		if err != nil {
			return err
		}
		if array.Init != nil {
			return d.fillArray(array.Init, index, value)
		}
		element := elementType(array.Type, mnemonic[0])
		if err := d.usedAsNumber(index); err != nil {
			return err
		}
		target := at(array, precPrimary) + "[" + coerce(index, "int") + "]"
		if array.Shared != 0 || index.Shared != 0 {
			if array.Shared != index.Shared {
				return bail("dup of a non-trivial value")
			}
			return d.compoundAssign(target, array.Shared, value, element)
		}
		assigned, err := d.coerceInto(value, element)
		if err != nil {
			return err
		}
		// An assignment the stack still wants stays where the value was.
		if d.assignFieldAsValue {
			d.assignFieldAsValue = false
			d.push(expr{Text: target + " = " + assigned, Prec: precAssign, Type: element, Effects: true})
			return nil
		}
		// The same as a field: the store runs before what the stack holds, and an
		// array read on it may be this element under another name.
		for _, stacked := range d.stack {
			if observesWrites(stacked, d.names) {
				return bail("an assignment with a value that could see it on the stack")
			}
		}
		d.emit(target + " = " + assigned + ";")
		return nil
	}
	if mnemonic == "newarray" {
		length, err := d.pop()
		if err != nil {
			return err
		}
		if err := d.usedAsNumber(length); err != nil {
			return err
		}
		element := instruction.Operand
		d.push(expr{
			Text: "new " + element + "[" + numeric(length).Text + "]",
			Prec: precPrimary,
			Type: element + "[]",
			Init: d.arrayInitOf("new "+element+"[]", element, numeric(length)),
		})
		return nil
	}
	if mnemonic == "anewarray" {
		element := typeName(orDefault(PoolClassName(pool, uint16(instruction.Arg))), d.self())
		length, err := d.pop()
		if err != nil {
			return err
		}
		if err := d.usedAsNumber(length); err != nil {
			return err
		}
		// The element type may itself be an array: the new dimension goes first,
		// so `new String[n][]`, never `new String[][n]`.
		base := strings.ReplaceAll(element, "[]", "")
		rest := strings.Repeat("[]", (len(element)-len(base))/2)
		d.push(expr{
			Text: "new " + base + "[" + numeric(length).Text + "]" + rest,
			Prec: precPrimary,
			Type: element + "[]",
			Init: d.arrayInitOf("new "+base+"[]"+rest, element, numeric(length)),
		})
		return nil
	}
	if mnemonic == "multianewarray" {
		typ := typeName(PoolClassName(pool, uint16(instruction.Arg)), d.self())
		rank := strings.Count(typ, "[]")
		sizes := make([]string, instruction.Arg2)
		for i := instruction.Arg2 - 1; i >= 0; i-- {
			size, err := d.pop()
			if err != nil {
				return err
			}
			if err := d.usedAsNumber(size); err != nil {
				return err
			}
			sizes[i] = numeric(size).Text
		}
		if len(sizes) > rank {
			return bail("multianewarray rank mismatch")
		}
		element := typ[:len(typ)-rank*2]
		dimensions := ""
		for _, size := range sizes {
			dimensions += "[" + size + "]"
		}
		d.push(primary("new "+element+dimensions+strings.Repeat("[]", rank-len(sizes)), typ))
		return nil
	}

	// Casts.
	if mnemonic == "checkcast" {
		typ := typeName(orDefault(PoolClassName(pool, uint16(instruction.Arg))), d.self())
		value, err := d.pop()
		if err != nil {
			return err
		}
		d.push(expr{Text: "(" + typ + ") " + at(value, precUnary), Prec: precUnary, Type: typ, Cast: true})
		return nil
	}
	if mnemonic == "instanceof" {
		typ := typeName(orDefault(PoolClassName(pool, uint16(instruction.Arg))), d.self())
		value, err := d.pop()
		if err != nil {
			return err
		}
		d.push(expr{Text: at(value, precRel+1) + " instanceof " + typ, Prec: precRel, Type: "boolean"})
		return nil
	}

	// Object creation. `new` leaves a reference that is not a value yet: only
	// the constructor call makes one, and javac dups it first so the call can
	// consume a copy and leave the object behind.
	if mnemonic == "new" {
		name := PoolClassName(pool, uint16(instruction.Arg))
		if name == "" {
			name = "java/lang/Object"
		}
		d.pendingCount++
		d.push(expr{
			Text:    "",
			Prec:    precPrimary,
			Type:    typeName(name, d.self()),
			Pending: d.pendingCount,
		})
		return nil
	}
	if invokes[mnemonic] {
		target, ok := PoolMemberRef(pool, uint16(instruction.Arg))
		if !ok {
			return bail("bad method reference")
		}
		if target.Name == "<init>" {
			return d.construct(target)
		}
		args, err := d.callArguments(target.Descriptor)
		if err != nil {
			return err
		}
		callee := ""
		if mnemonic == "invokestatic" {
			callee = d.staticCallee(target.Owner, target.Name)
		} else if callee, err = d.receiverCallee(mnemonic, target.Owner, target.Name, target.Interface); err != nil {
			return err
		}
		text := callee + "(" + strings.Join(args, ", ") + ")"
		typ := sourceTypeText(methodReturnType(target.Descriptor), d.self())
		if typ == "void" {
			d.emit(text + ";")
			return nil
		}
		d.push(expr{Text: text, Prec: precPrimary, Type: typ, Effects: true})
		return nil
	}

	if mnemonic == "invokedynamic" {
		return d.dynamic(uint16(instruction.Arg))
	}

	// Stack shuffling. A dup of anything but a name or an array being filled in
	// would duplicate the expression itself (`new int[2][0] = 1;`), so only
	// those cases are taken; the rest waits for a later phase.
	if dups[mnemonic] {
		// How many values the copy is of, and how far down it goes: `dup2` is one
		// long or double, or two of anything else, and the `_xN` says how many
		// values it is pushed under.
		wide := func(value expr) bool { return value.Type == "long" || value.Type == "double" }
		// A `dup` straight into a store is an assignment source used as a value:
		// `while ((line = read()) != null)`. Nothing is written twice - the store
		// stays where it is and becomes the expression - so the guards below,
		// which are about writing a value's text once per copy, do not apply.
		if next != nil && (mnemonic == "dup" && singleSlotStore.MatchString(next.Mnemonic) ||
			mnemonic == "dup2" && wideStore.MatchString(next.Mnemonic)) {
			d.assignAsValue = true
			return nil
		}
		// A receiver copied for a read-modify-write - `dup; getfield` - or an
		// array and index copied for one - `dup2; iaload` - that cannot be
		// written twice is not written twice: both copies carry one mark, and
		// only the compound assignment's read and write may take them.
		if next != nil && (mnemonic == "dup" && next.Mnemonic == "getfield" ||
			mnemonic == "dup2" && isOneOf(next.Mnemonic, "ilfdabcs", "aload")) {
			count := 1
			if mnemonic == "dup2" {
				count = 2
			}
			if len(d.stack) < count {
				return bail("stack underflow")
			}
			copies := append([]expr(nil), d.stack[len(d.stack)-count:]...)
			shareable := count == 1 || !wide(copies[len(copies)-1])
			needed := false
			for _, value := range copies {
				needed = needed || checkDuplicable(value) != nil
			}
			if shareable && needed {
				d.sharedIDs++
				for i := range copies {
					copies[i].Shared = d.sharedIDs
				}
				d.stack = append(d.stack[:len(d.stack)-count], copies...)
				d.stack = append(d.stack, copies...)
				return nil
			}
		}
		// The same for a field: `dup_x1; putfield` keeps the value under the
		// receiver, `dup; putstatic` keeps it in place (`dup2` for a long).
		if next != nil && ((mnemonic == "dup_x1" || mnemonic == "dup2_x1") && next.Mnemonic == "putfield" ||
			(mnemonic == "dup" || mnemonic == "dup2") && next.Mnemonic == "putstatic" ||
			(mnemonic == "dup_x2" || mnemonic == "dup2_x2") && isOneOf(next.Mnemonic, "ilfdabcs", "astore")) {
			d.assignFieldAsValue = true
			return nil
		}
		// popRaw, because the copy being duplicated is the array literal that is
		// still being filled in, which pop rejects as incomplete.
		top, err := d.popRaw()
		if err != nil {
			return err
		}
		taken := []expr{top}
		if strings.HasPrefix(mnemonic, "dup2") && !wide(top) {
			second, err := d.popRaw()
			if err != nil {
				return err
			}
			taken = append([]expr{second}, taken...)
		}
		depth := 0
		if strings.HasSuffix(mnemonic, "_x1") {
			depth = 1
		} else if strings.HasSuffix(mnemonic, "_x2") {
			depth = 2
		}
		var under []expr
		for left := depth; left > 0; {
			value, err := d.popRaw()
			if err != nil {
				return err
			}
			under = append([]expr{value}, under...)
			if wide(value) {
				left -= 2
			} else {
				left--
			}
		}
		// Only what is *copied* has to be re-readable; a value the copy is pushed
		// under is popped and pushed back untouched.
		for _, value := range taken {
			if err := checkDuplicable(value); err != nil {
				return err
			}
		}
		for _, value := range taken {
			d.push(value)
		}
		for _, value := range under {
			d.push(value)
		}
		for _, value := range taken {
			d.push(value)
		}
		return nil
	}
	if mnemonic == "pop" || mnemonic == "pop2" {
		value, err := d.pop()
		if err != nil {
			return err
		}
		// `pop2` drops one long or double, or two of anything else - and two of
		// anything else is a shape this phase does not produce.
		if mnemonic == "pop2" && value.Type != "long" && value.Type != "double" {
			return bail("pop2 of two values")
		}
		// The value of a call is what is being dropped, not the call itself. Only a
		// call is a statement in Java, though: a dropped concatenation would have to
		// keep the calls inside it, and `"a" + f();` is not something to write.
		if value.Effects {
			if value.Prec != precPrimary {
				return bail("a dropped value that is not a statement")
			}
			d.emit(value.Text + ";")
		}
		return nil
	}

	// Comparisons. `lcmp` and the float ones have no source form: they only exist
	// to feed the branch that follows, which is what was written.
	if mnemonic == "lcmp" || (len(mnemonic) == 5 && strings.HasPrefix(mnemonic[1:], "cmp") &&
		(mnemonic[0] == 'f' || mnemonic[0] == 'd') && (mnemonic[4] == 'l' || mnemonic[4] == 'g')) {
		right, err := d.pop()
		if err != nil {
			return err
		}
		left, err := d.pop()
		if err != nil {
			return err
		}
		d.push(expr{Prec: precPrimary, Type: "int", Compared: &comparedPair{Left: left, Right: right}})
		return nil
	}

	// Returns.
	if mnemonic == "athrow" {
		value, err := d.pop()
		if err != nil {
			return err
		}
		d.emit("throw " + value.Text + ";")
		return nil
	}
	if mnemonic == "return" {
		d.emit("return;")
		return nil
	}
	if isOneOf(mnemonic, "ilfda", "return") {
		value, err := d.pop()
		if err != nil {
			return err
		}
		returned, err := d.coerceInto(value, d.returnType)
		if err != nil {
			return err
		}
		d.emit("return " + returned + ";")
		return nil
	}

	return bail("unsupported instruction %s", mnemonic)
}

func elementType(arrayType string, prefix byte) string {
	if strings.HasSuffix(arrayType, "[]") {
		return arrayType[:len(arrayType)-2]
	}
	return primitiveOfPrefix[prefix]
}

func orDefault(name string) string {
	if name == "" {
		return "java/lang/Object"
	}
	return name
}

// --- declarations --------------------------------------------------------------------

func accessModifiers(flags uint16) []string {
	switch {
	case flags&accPublic != 0:
		return []string{"public"}
	case flags&accProtected != 0:
		return []string{"protected"}
	case flags&accPrivate != 0:
		return []string{"private"}
	}
	return nil
}

func simpleClassName(internal string) string {
	// A nested class keeps its `$` name: it is a legal Java identifier, and
	// restoring the nesting needs the whole enclosing file (a later phase).
	if slash := strings.LastIndex(internal, "/"); slash >= 0 {
		return internal[slash+1:]
	}
	return internal
}

// constantValue reads the ConstantValue (JVMS 4.7.2) a `static final` field must
// be initialized to. On an instance field the JVM ignores the attribute and
// javac assigns the value in the constructor instead, so writing it back would
// be the assignment twice - and on a `final` field the second one does not
// compile.
func constantValue(field Member, classFile *ClassFile) (expr, bool) {
	if field.Flags&accStatic == 0 {
		return expr{}, false
	}
	attribute, ok := FindAttribute(field.Attributes, "ConstantValue")
	if !ok || len(attribute.Bytes) < 2 {
		return expr{}, false
	}
	value, err := constantExpr(classFile.Pool, binary.BigEndian.Uint16(attribute.Bytes), selfOf(classFile))
	if err != nil {
		return expr{}, false
	}
	return value, true
}

func fieldSource(field Member, classFile *ClassFile, keepFinal bool) string {
	fieldType := descriptorSourceType(field.Descriptor, selfOf(classFile))
	modifiers := accessModifiers(field.Flags)
	if field.Flags&accStatic != 0 {
		modifiers = append(modifiers, "static")
	}
	if field.Flags&accFinal != 0 {
		modifiers = append(modifiers, "final")
	}
	if field.Flags&accTransient != 0 {
		modifiers = append(modifiers, "transient")
	}
	if field.Flags&accVolatile != 0 {
		modifiers = append(modifiers, "volatile")
	}
	initializer := ""
	hasValue := false
	if value, ok := constantValue(field, classFile); ok {
		initializer = " = " + coerce(value, fieldType)
		hasValue = true
	}
	// A blank `static final` is only legal when something assigns it; when the
	// static initializer could not be reconstructed, nothing does.
	shown := modifiers
	if !keepFinal && !hasValue {
		shown = nil
		for _, m := range modifiers {
			if m != "final" {
				shown = append(shown, m)
			}
		}
	}
	return strings.Join(append(shown, fieldType), " ") + " " + field.Name + initializer + ";"
}

func methodModifiers(method Member, classFile *ClassFile) []string {
	isInterface := classFile.Flags&accInterface != 0
	isStatic := method.Flags&accStatic != 0
	isAbstract := method.Flags&accAbstract != 0
	modifiers := accessModifiers(method.Flags)
	if isAbstract {
		modifiers = append(modifiers, "abstract")
	}
	if isStatic {
		modifiers = append(modifiers, "static")
	}
	if method.Flags&accFinal != 0 {
		modifiers = append(modifiers, "final")
	}
	if method.Flags&accSynchronized != 0 {
		modifiers = append(modifiers, "synchronized")
	}
	if method.Flags&accNative != 0 {
		modifiers = append(modifiers, "native")
	}
	if isInterface && !isStatic && !isAbstract && method.Flags&accPrivate == 0 {
		modifiers = append(modifiers, "default")
	}
	return modifiers
}

// buildLocals seeds the parameter (and `this`) slots, named from the debug table
// when there is one.
func buildLocals(method Member, localTable []localEntry, isStatic bool, self string) map[int]*local {
	locals := map[int]*local{}
	for index, parameter := range parameterSlots(method.Descriptor, isStatic) {
		// The descriptor says what a parameter's type is.
		entry := &local{
			Name: "arg" + strconv.Itoa(index), Type: parameter.Type,
			Declared: true, Authoritative: true, StoreBlocks: map[int]bool{},
		}
		for i := range localTable {
			if scoped := &localTable[i]; scoped.Slot == parameter.Slot && scoped.StartPc == 0 {
				entry.Name = scoped.Name
				entry.Origin = scoped
				if scoped.Type != "" {
					entry.Type = scoped.Type
				}
				break
			}
		}
		entry.Type = sourceTypeText(entry.Type, self)
		locals[parameter.Slot] = entry
	}
	return locals
}

func parameterList(method Member, locals map[int]*local, isStatic bool, dropLeading int) string {
	slots := parameterSlots(method.Descriptor, isStatic)
	if dropLeading > 0 && dropLeading <= len(slots) {
		slots = slots[dropLeading:]
	}
	parameters := make([]string, 0, len(slots))
	for offset, parameter := range slots {
		index := offset + dropLeading
		name := "arg" + strconv.Itoa(index)
		typ := parameter.Type
		if entry, ok := locals[parameter.Slot]; ok {
			name, typ = entry.Name, entry.Type
		}
		if method.Flags&accVarargs != 0 && offset == len(slots)-1 && strings.HasSuffix(typ, "[]") {
			typ = typ[:len(typ)-2] + "..."
		}
		parameters = append(parameters, typ+" "+name)
	}
	return strings.Join(parameters, ", ")
}

// bailComment renders the disassembly of a body this phase cannot reconstruct.
func bailComment(instructions []Instruction, reason string) []string {
	lines := []string{"/* cappu: " + reason + "; the bytecode is:"}
	for _, instruction := range instructions {
		operand := ""
		if instruction.Operand != "" {
			operand = " " + instruction.Operand
		}
		texts := []string{fmt.Sprintf("%d: %s%s", instruction.Pc, instruction.Mnemonic, operand)}
		for _, extra := range instruction.ExtraLines {
			texts = append(texts, strings.TrimSpace(extra))
		}
		for _, text := range texts {
			// A string constant may contain the comment terminator.
			lines = append(lines, " * "+strings.ReplaceAll(text, "*/", "* /"))
		}
	}
	return append(lines, " */")
}

// generatedConstructor reports the `<init>()` javac writes when a class declares
// no constructor: the sole constructor, carrying the class' own access, whose
// body is nothing but the implicit `super()` call. Java puts exactly that back,
// so it is not source - but a declared no-arg constructor that merely looks like
// it (one of several, or `private` on a package-private class) has to stay, or
// the class' API changes.
func generatedConstructor(classFile *ClassFile) *Member {
	var only *Member
	count := 0
	for i := range classFile.Methods {
		if classFile.Methods[i].Name == "<init>" {
			count++
			only = &classFile.Methods[i]
		}
	}
	if count != 1 || only.Descriptor != "()V" {
		return nil
	}
	const access = accPublic | accProtected | accPrivate
	if only.Flags&access != classFile.Flags&access {
		return nil
	}
	code, err := ReadCode(*only, classFile.Pool)
	if err != nil || code == nil || len(code.Exceptions) > 0 {
		return nil
	}
	instructions, err := DecodeInstructions(classFile, code.Code)
	if err != nil || len(instructions) != 3 {
		return nil
	}
	if instructions[0].Mnemonic != "aload_0" || instructions[1].Mnemonic != "invokespecial" ||
		instructions[2].Mnemonic != "return" {
		return nil
	}
	target, ok := PoolMemberRef(classFile.Pool, uint16(instructions[1].Arg))
	superClass := classFile.SuperClass
	if superClass == "" {
		superClass = "java/lang/Object"
	}
	if ok && target.Name == "<init>" && target.Descriptor == "()V" && target.Owner == superClass {
		return only
	}
	return nil
}

// defaultValue is a value of type that compiles, for a chain call this phase
// cannot rebuild.
func defaultValue(typ string) string {
	switch typ {
	case "boolean":
		return "false"
	case "int", "long", "float", "double", "byte", "char", "short":
		return "(" + typ + ") 0"
	}
	return "(" + typ + ") null"
}

// chainCallStub is the `super(...)`/`this(...)` a constructor that gave up still
// has to make: without it the class does not compile when the superclass has no
// no-arg constructor. The arguments are placeholders - the body throws before
// anything can observe them - but their types come from the real descriptor.
func chainCallStub(instructions []Instruction, classFile *ClassFile) string {
	if isEnumDeclaration(classFile) {
		return "" // generated, never source
	}
	superClass := classFile.SuperClass
	if superClass == "" {
		superClass = "java/lang/Object"
	}
	for _, instruction := range instructions {
		if instruction.Mnemonic != "invokespecial" {
			continue
		}
		target, ok := PoolMemberRef(classFile.Pool, uint16(instruction.Arg))
		if !ok || target.Name != "<init>" {
			continue
		}
		isSuper := target.Owner == superClass
		// `new Foo()` in an argument is an invokespecial too; the chain call is
		// the one on this class or its superclass.
		if !isSuper && target.Owner != classFile.ThisClass {
			continue
		}
		params := parameterSlots(target.Descriptor, true)
		// An inner superclass's constructor takes its enclosing instance first,
		// which only a qualifier can pass: `((Outer) null).super(...)`. A
		// `this(...)` keeps it, as construct does.
		qualifier := ""
		if cut := strings.LastIndexByte(target.Owner, '$'); isSuper && cut > 0 && len(params) > 0 &&
			sourceTypeText(params[0].Type, selfOf(classFile)) == typeName(target.Owner[:cut], selfOf(classFile)) {
			if access, ok := InnerClassFlags(classFile)[target.Owner]; ok && access&accStatic == 0 {
				qualifier = "((" + sourceTypeText(params[0].Type, selfOf(classFile)) + ") null)."
				params = params[1:]
			}
		}
		if len(params) == 0 && qualifier == "" {
			return "" // the implicit super(), regenerated
		}
		args := make([]string, len(params))
		for i, parameter := range params {
			args[i] = defaultValue(sourceTypeText(parameter.Type, selfOf(classFile)))
		}
		keyword := "this"
		if isSuper {
			keyword = qualifier + "super"
		}
		return keyword + "(" + strings.Join(args, ", ") + ");"
	}
	return ""
}

// methodSource renders one member; reconstructed is false when the body is the
// bail-out rendering rather than reconstructed code.
func methodSource(method Member, classFile *ClassFile) (lines []string, reconstructed bool, err error) {
	isStatic := method.Flags&accStatic != 0
	self := selfOf(classFile)
	code, err := ReadCode(method, classFile.Pool)
	if err != nil {
		return nil, false, err
	}
	var localTable []localEntry
	if code != nil {
		localTable = readLocalVariables(code, classFile.Pool)
	}
	locals := buildLocals(method, localTable, isStatic, self)
	// A record's canonical constructor has to name its parameters after the
	// components - Java checks that, and a class file without a debug table does
	// not carry the names.
	if components := recordComponents(classFile); components != nil && method.Name == "<init>" &&
		method.Descriptor == canonicalDescriptor(components) {
		for index, slot := range parameterSlots(method.Descriptor, isStatic) {
			if entry, ok := locals[slot.Slot]; ok && index < len(components) {
				entry.Name = components[index].Name
			}
		}
	}

	head := "static"
	if method.Name != "<clinit>" {
		parts := methodModifiers(method, classFile)
		if method.Name == "<init>" {
			// Every enum constructor starts with the generated name and
			// ordinal; source declares neither.
			dropLeading := 0
			if isEnumDeclaration(classFile) {
				dropLeading = 2
			}
			parts = append(parts, simpleClassName(classFile.ThisClass)+
				"("+parameterList(method, locals, isStatic, dropLeading)+")")
		} else {
			parts = append(parts, sourceTypeText(methodReturnType(method.Descriptor), self),
				method.Name+"("+parameterList(method, locals, isStatic, 0)+")")
		}
		head = strings.Join(parts, " ")
		var thrown []string
		for _, name := range ReadThrownExceptions(method, classFile.Pool) {
			thrown = append(thrown, typeName(name, self))
		}
		if len(thrown) > 0 {
			head += " throws " + strings.Join(thrown, ", ")
		}
	}

	if code == nil {
		return []string{head + ";"}, true, nil
	}

	instructions, err := DecodeInstructions(classFile, code.Code)
	if err != nil {
		return nil, false, err
	}
	body, reached, chainCall, err := decompileBody(classFile, code, instructions, locals, localTable, method, isStatic)
	reconstructed = true
	if err != nil {
		var reason *notDecompilable
		if !errors.As(err, &reason) {
			return nil, false, err
		}
		reconstructed = false
		// A constructor that gave up keeps its chain call: without it the class
		// does not compile when the superclass has no no-arg constructor.
		chained := ""
		if len(reached) > 0 && chainCall != "" && reached[0] == chainCall {
			chained = reached[0]
		} else if method.Name == "<init>" {
			chained = chainCallStub(instructions, classFile)
		}
		body = nil
		if chained != "" {
			body = append(body, chained)
		}
		body = append(body, bailComment(instructions, reason.reason)...)
		// A static initializer has to be able to complete normally, so the throw
		// that marks every other unreconstructed body would not compile here.
		if method.Name != "<clinit>" {
			body = append(body, `throw new UnsupportedOperationException("cappu: not decompiled");`)
		}
	}
	return append(append([]string{head + " {"}, body...), "}"), reconstructed, nil
}

func decompileBody(
	classFile *ClassFile,
	code *Code,
	instructions []Instruction,
	locals map[int]*local,
	localTable []localEntry,
	method Member,
	isStatic bool,
) (body []string, reached []string, chained string, err error) {
	d := &bodyDecompiler{
		classFile:   classFile,
		locals:      locals,
		localTable:  localTable,
		returnType:  methodReturnType(method.Descriptor),
		isStatic:    isStatic,
		names:       map[string]bool{},
		byName:      map[string]*local{},
		visited:     map[int]bool{},
		activeTries: map[*tryRegion]bool{},
		skip:        map[int]int{},
		innerFlags:  InnerClassFlags(classFile),
		bootstraps:  ReadBootstrapMethods(classFile),
		inlining:    map[string]bool{},
	}
	d.current = &d.statements
	for _, parameter := range locals {
		d.names[parameter.Name] = true
		d.byName[parameter.Name] = parameter
	}
	if err := d.run(instructions, code.Exceptions); err != nil {
		return nil, flattenStatements(d.statements), d.chained, err
	}
	// A variable whose type stayed open was read only where any type reads the
	// same - in a conditional's arm, say - and that is where an Object is not
	// what source declared.
	for _, entry := range d.byName {
		if entry.Open {
			return nil, flattenStatements(d.statements), d.chained, bail("a variable whose type no use says")
		}
	}
	body = withHoisted(flattenStatements(d.hoisted), flattenStatements(d.statements), d.chained)
	// Every void method ends in a `return` javac inserted; source does not.
	if len(body) > 0 && body[len(body)-1] == "return;" {
		body = body[:len(body)-1]
	}
	return body, flattenStatements(d.statements), d.chained, nil
}

// generatedFields are the fields javac writes for itself, and regenerates from
// source.
var generatedFields = map[string]bool{"$VALUES": true, "$assertionsDisabled": true}

// recordComponent is one component of a record: its name and its type, in
// declaration order.
type decompiledRecordComponent struct {
	Name string
	Type string
	// Descriptor is the raw one, which is what a generated member is recognised by.
	Descriptor string
}

// recordComponents are the components of a record (JVMS 4.7.30), which is what
// its header declares - and what says which of its members javac generated.
func recordComponents(classFile *ClassFile) []decompiledRecordComponent {
	if classFile.SuperClass != "java/lang/Record" {
		return nil
	}
	attribute, ok := FindAttribute(classFile.Attributes, "Record")
	if !ok || len(attribute.Bytes) < 2 {
		return nil
	}
	b := attribute.Bytes
	be := binary.BigEndian
	count := int(be.Uint16(b))
	at := 2
	components := make([]decompiledRecordComponent, 0, count)
	for i := 0; i < count; i++ {
		if at+6 > len(b) {
			return nil
		}
		name := PoolUtf8(classFile.Pool, be.Uint16(b[at:]))
		descriptor := PoolUtf8(classFile.Pool, be.Uint16(b[at+2:]))
		attributes := int(be.Uint16(b[at+4:]))
		at += 6
		// Each component carries attributes of its own (a signature,
		// annotations), which are laid out like any other and skipped the same way.
		for j := 0; j < attributes; j++ {
			if at+6 > len(b) {
				return nil
			}
			at += 6 + int(be.Uint32(b[at+2:]))
		}
		if name == "" || descriptor == "" {
			return nil
		}
		components = append(components, decompiledRecordComponent{
			Name:       name,
			Type:       descriptorSourceType(descriptor, selfOf(classFile)),
			Descriptor: descriptor,
		})
	}
	return components
}

// canonicalDescriptor is the descriptor of a record's canonical constructor.
func canonicalDescriptor(components []decompiledRecordComponent) string {
	descriptor := "("
	for _, one := range components {
		descriptor += one.Descriptor
	}
	return descriptor + ")V"
}

// isGeneratedRecordMember reports whether method is one javac writes for a
// record: the accessor of a component, the canonical constructor that only
// stores them, or one of the three `ObjectMethods` members. A record may declare
// any of those itself, and then the body is not what javac generates.
func isGeneratedRecordMember(method Member, classFile *ClassFile, components []decompiledRecordComponent) bool {
	if method.Name == "equals" || method.Name == "hashCode" || method.Name == "toString" {
		code, err := ReadCode(method, classFile.Pool)
		if err != nil || code == nil {
			return false
		}
		instructions, err := DecodeInstructions(classFile, code.Code)
		if err != nil {
			return false
		}
		for _, one := range instructions {
			if one.Mnemonic != "invokedynamic" {
				continue
			}
			entry := PoolAt(classFile.Pool, uint16(one.Arg))
			if entry == nil || entry.Tag != TagInvokeDynamic {
				return false
			}
			bootstraps := ReadBootstrapMethods(classFile)
			if int(entry.Bootstrap) >= len(bootstraps) {
				return false
			}
			handle := PoolAt(classFile.Pool, bootstraps[entry.Bootstrap].HandleIndex)
			if handle == nil || handle.Tag != TagMethodHandle {
				return false
			}
			factory, ok := PoolMemberRef(classFile.Pool, handle.RefIndex)
			return ok && factory.Owner == "java/lang/runtime/ObjectMethods"
		}
		return false
	}
	body := func() []Instruction {
		code, err := ReadCode(method, classFile.Pool)
		if err != nil || code == nil {
			return nil
		}
		instructions, err := DecodeInstructions(classFile, code.Code)
		if err != nil {
			return nil
		}
		return instructions
	}
	// fieldOf is the field a `getfield`/`putfield` names, when it is this class'.
	fieldOf := func(instruction Instruction) string {
		field, ok := PoolMemberRef(classFile.Pool, uint16(instruction.Arg))
		if !ok || field.Owner != classFile.ThisClass {
			return ""
		}
		return field.Name + field.Descriptor
	}
	for _, component := range components {
		if component.Name != method.Name || method.Descriptor != "()"+component.Descriptor {
			continue
		}
		// An accessor javac wrote reads *that* component and returns it, and
		// nothing else: one that reads another one, or does anything more, is the
		// source's.
		kept := body()
		return len(kept) == 3 && kept[0].Mnemonic == "aload_0" && kept[1].Mnemonic == "getfield" &&
			fieldOf(kept[1]) == component.Name+component.Descriptor &&
			returnMnemonic.MatchString(kept[2].Mnemonic)
	}
	if method.Name == "<init>" && method.Descriptor == canonicalDescriptor(components) {
		// The canonical constructor javac writes chains to `Record` and stores
		// each component into its own field, in order, from its own parameter -
		// one that source wrote does more, or does it differently, and has to stay.
		kept := body()
		if len(kept) != 2+len(components)*3+1 {
			return false
		}
		if kept[0].Mnemonic != "aload_0" || kept[1].Mnemonic != "invokespecial" {
			return false
		}
		chained, ok := PoolMemberRef(classFile.Pool, uint16(kept[1].Arg))
		if !ok || chained.Owner != "java/lang/Record" || chained.Name != "<init>" {
			return false
		}
		slot := 1
		for index, one := range components {
			load, read, store := kept[2+index*3], kept[3+index*3], kept[4+index*3]
			if load.Mnemonic != "aload_0" || !loadMnemonic.MatchString(read.Mnemonic) ||
				slotOf(read) != slot || store.Mnemonic != "putfield" ||
				fieldOf(store) != one.Name+one.Descriptor {
				return false
			}
			if one.Descriptor == "J" || one.Descriptor == "D" {
				slot += 2
			} else {
				slot++
			}
		}
		return kept[len(kept)-1].Mnemonic == "return"
	}
	return false
}

var (
	returnMnemonic = regexp.MustCompile(`^[ilfda]?return$`)
	loadMnemonic   = regexp.MustCompile(`^[ilfda]load(?:_\d)?$`)
)

// isEnumDeclaration reports an enum type. ACC_ENUM is also set on the anonymous
// subclass javac writes for an enum constant with a body - which is a plain
// class, not an enum declaration: it extends the enum type, and `enum X extends
// Y` is not Java.
func isEnumDeclaration(classFile *ClassFile) bool {
	return classFile.Flags&accEnum != 0 && classFile.SuperClass == "java/lang/Enum"
}

// enumConstants are the constants of an enum, in declaration order, as the body
// must open.
func enumConstants(classFile *ClassFile) []string {
	self := "L" + classFile.ThisClass + ";"
	var out []string
	for _, field := range classFile.Fields {
		if field.Flags&accEnum != 0 && field.Descriptor == self {
			out = append(out, field.Name)
		}
	}
	return out
}

// isGeneratedEnumMember reports `values` and `valueOf`, which javac generates for
// every enum; writing them out is a compile error ("already defined"), so they
// are not source. The two leading constructor parameters are dropped for the
// same reason (see parameterList).
func isGeneratedEnumMember(method Member, classFile *ClassFile) bool {
	if !isEnumDeclaration(classFile) {
		return false
	}
	self := "L" + classFile.ThisClass + ";"
	return (method.Name == "values" && method.Descriptor == "()["+self) ||
		(method.Name == "valueOf" && method.Descriptor == "(Ljava/lang/String;)"+self)
}

func classHead(classFile *ClassFile, components []decompiledRecordComponent) string {
	isInterface := classFile.Flags&accInterface != 0
	isAnnotation := classFile.Flags&accAnnotation != 0
	isEnum := isEnumDeclaration(classFile)
	self := selfOf(classFile)
	keyword := "class"
	switch {
	case isAnnotation:
		keyword = "@interface"
	case isInterface:
		keyword = "interface"
	case isEnum:
		keyword = "enum"
	}
	var head []string
	if classFile.Flags&accPublic != 0 {
		head = append(head, "public")
	}
	if !isInterface && !isEnum && classFile.Flags&accFinal != 0 {
		head = append(head, "final")
	}
	// An enum carrying constant bodies is ACC_ABSTRACT, but `abstract enum` is
	// not something Java lets you write.
	if !isInterface && !isEnum && classFile.Flags&accAbstract != 0 {
		head = append(head, "abstract")
	}
	head = append(head, keyword, simpleClassName(classFile.ThisClass))
	if components != nil {
		// A record declares its state in the header, and `final` is implicit.
		head = head[:0]
		head = append(head, accessModifiers(classFile.Flags)...)
		var declared []string
		for _, one := range components {
			declared = append(declared, sourceTypeText(one.Type, selfOf(classFile))+" "+one.Name)
		}
		head = append(head, "record",
			simpleClassName(classFile.ThisClass)+"("+strings.Join(declared, ", ")+")")
	}
	// The implicit supertypes are not written in source.
	implicit := map[string]bool{"java/lang/Object": true, "java/lang/Enum": true, "java/lang/Record": true}
	if !isInterface && classFile.SuperClass != "" && !implicit[classFile.SuperClass] {
		head = append(head, "extends", typeName(classFile.SuperClass, self))
	}
	var interfaces []string
	for _, name := range classFile.Interfaces {
		if name != "java/lang/annotation/Annotation" {
			interfaces = append(interfaces, typeName(name, self))
		}
	}
	if len(interfaces) > 0 {
		if isInterface {
			head = append(head, "extends")
		} else {
			head = append(head, "implements")
		}
		head = append(head, strings.Join(interfaces, ", "))
	}
	return strings.Join(head, " ") + " {"
}

// --- entry points --------------------------------------------------------------------

// DecompileClass renders one class as (unformatted) Java source.
func DecompileClass(classFile *ClassFile) (string, error) {
	var lines []string
	packageName := ""
	if slash := strings.LastIndex(classFile.ThisClass, "/"); slash > 0 {
		packageName = strings.ReplaceAll(classFile.ThisClass[:slash], "/", ".")
	}
	// A package-info.class only carries the package's annotations, and its name
	// is not an identifier - the package declaration is the whole source.
	if simpleClassName(classFile.ThisClass) == "package-info" {
		if packageName == "" {
			return "", nil
		}
		return "package " + packageName + ";\n", nil
	}
	if packageName != "" {
		lines = append(lines, "package "+packageName+";", "")
	}
	// Methods first: whether the static initializer came back decides how the
	// static fields have to be declared.
	generated := generatedConstructor(classFile)
	components := recordComponents(classFile)
	var bodies [][]string
	staticInitializerLost := false
	for i := range classFile.Methods {
		method := classFile.Methods[i]
		if method.Flags&(accSynthetic|accBridge) != 0 || isGeneratedEnumMember(method, classFile) {
			continue
		}
		if generated != nil && &classFile.Methods[i] == generated {
			continue
		}
		// A record's accessors, its canonical constructor and the three members
		// `ObjectMethods` builds are the header, written out.
		if components != nil && isGeneratedRecordMember(method, classFile, components) {
			continue
		}
		body, reconstructed, err := methodSource(method, classFile)
		if err != nil {
			return "", err
		}
		if !reconstructed && method.Name == "<clinit>" {
			staticInitializerLost = true
		}
		bodies = append(bodies, body)
	}

	lines = append(lines, classHead(classFile, components))
	if isEnumDeclaration(classFile) {
		// An enum body opens with its constant list; even an empty one needs
		// the `;` before any member. How the constants are constructed lives in
		// <clinit>, which this phase does not reconstruct.
		lines = append(lines, strings.Join(enumConstants(classFile), ", ")+";")
	}
	for _, field := range classFile.Fields {
		// Enum constants are the constant list above, not fields. A synthetic
		// field is kept - the captured outer instance (`this$0`) and captured
		// locals (`val$x`) are referenced by real method bodies - except the two
		// javac generates on its own, which would clash with the ones it
		// regenerates.
		if field.Flags&accEnum != 0 || (field.Flags&accSynthetic != 0 && generatedFields[field.Name]) {
			continue
		}
		// A record's components are its state: declaring the fields again is not
		// something Java lets you write.
		isComponent := false
		for _, one := range components {
			if one.Name == field.Name {
				isComponent = true
			}
		}
		if isComponent {
			continue
		}
		keepFinal := !staticInitializerLost || field.Flags&accStatic == 0
		lines = append(lines, fieldSource(field, classFile, keepFinal))
	}
	for _, body := range bodies {
		lines = append(append(lines, ""), body...)
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n") + "\n", nil
}

// Decompile renders one class file's bytes as Java source. The text is
// unformatted: callers pass it through the formatter.
func Decompile(b []byte) (string, error) {
	classFile, err := ReadClassFile(b)
	if err != nil {
		return "", err
	}
	// Same reasoning as Disassemble: a module descriptor carries no members, so
	// rendering it as a class would print a plausible-looking empty type.
	if classFile.Flags&accModule != 0 {
		return "", errors.New("module descriptors are not supported yet")
	}
	return DecompileClass(classFile)
}
