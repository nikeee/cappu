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
	// Effects marks a value that does something when it runs - a call. Dropping
	// it has to keep it as a statement, and nothing may write it twice.
	Effects bool
	// Pending is the id of the object `new` left on the stack, which is not a
	// value until its constructor has run. Every copy carries the same id, so
	// the call can put `new C(...)` in all of their places at once. Zero means
	// the value is not one.
	Pending int
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

func binaryExpr(left expr, operator string, right expr, prec int, typ string) expr {
	// Every operator here is left-associative, so the right operand needs one
	// more level to keep `a - (b - c)` from losing its parentheses.
	return expr{Text: at(left, prec) + " " + operator + " " + at(right, prec+1), Prec: prec, Type: typ}
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

func ternaryExpr(condition, thenValue, elseValue expr) (expr, bool) {
	whenTrue, whenFalse := thenValue, elseValue
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
	// what the source said.
	switch {
	case whenTrue.Text == "true":
		return logicalExpr(logicOr, condition, whenFalse), true
	case whenFalse.Text == "false":
		return logicalExpr(logicAnd, condition, whenTrue), true
	case whenTrue.Text == "false":
		return logicalExpr(logicAnd, negate(condition), whenFalse), true
	case whenFalse.Text == "true":
		return logicalExpr(logicOr, negate(condition), whenTrue), true
	}
	typ := whenTrue.Type
	if whenTrue.Type != whenFalse.Type {
		left, right := widthOf(whenTrue.Type), widthOf(whenFalse.Type)
		if left >= 0 && right >= 0 && right > left {
			typ = whenFalse.Type
		}
	}
	return expr{
		Text: at(condition, precTernary+1) + " ? " + at(whenTrue, precTernary) +
			" : " + at(whenFalse, precTernary),
		Prec: precTernary,
		Type: typ,
	}, true
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
	// but where a number is what belongs.
	if e.AsInt != "" && target != "boolean" {
		return e.AsInt
	}
	if e.Type != "int" || !isIntegerText(e.Text) {
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

// stmt is a statement, or the body of a nested block. The tree is flattened
// only once the method is done, so a retype can still reach a statement that
// has already been placed inside an `if`.
type stmt struct {
	Text   string
	Nested *[]stmt
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
	// Writes records where every assignment landed, so a retype can rewrite them.
	Writes []localWrite
	// Declaration is where the declaration landed.
	Declaration *localDeclaration
	// StoreBlocks are the blocks that store to it, which is what says whether a
	// read is unambiguous.
	StoreBlocks map[int]bool
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

func buildBlocks(instructions []Instruction, exceptions []ExceptionEntry) (map[int]*block, error) {
	entry := 0
	if len(instructions) > 0 {
		entry = instructions[0].Pc
	}
	leaders := map[int]bool{entry: true}
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
		// A catch-all guards `finally`, `synchronized` and try-with-resources,
		// all of which javac writes as duplicated code plus a rethrow: not this
		// phase.
		if entry.CatchType == "" {
			return nil, bail("a finally or synchronized block")
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
func loopFollow(blocks map[int]*block, header int, latches []int, body map[int]bool) (int, error) {
	outside := func(start int) []int {
		var out []int
		if blocks[start] == nil {
			return out
		}
		for _, successor := range blocks[start].Successors {
			if !body[successor] {
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
func withExceptionEdges(blocks map[int]*block, regions []*tryRegion) map[int]*block {
	if len(regions) == 0 {
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

func findLoops(blocks map[int]*block, entry int, regions []*tryRegion) (map[int]*loop, error) {
	// Everything that asks which blocks a loop is made of runs over the graph
	// with the throwing edges in it; where the loop *ends* is read off the real
	// one, where entering a handler is not a way out of the body.
	flow := withExceptionEdges(blocks, regions)
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
		follow, err := loopFollow(blocks, header, latches, body)
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
var pureMnemonics = regexp.MustCompile(`^(?:nop|aconst_null|[ilfd]const_\w+|bipush|sipush|ldc\w*|` +
	`[ilfda]load(?:_\d|_w)?|arraylength|[ilfdabcs]aload|` +
	`[ilfd](?:add|sub|mul|div|rem|neg|shl|shr|ushr|and|or|xor)|[ilfd]2[ilfdbcs]|` +
	`lcmp|[fd]cmp[lg]|getstatic|getfield|checkcast|instanceof|dup)$`)

// isConditionBlock reports whether a condition may be folded from a block. Like
// isPureBlock, but a call is allowed: folding runs it exactly once and in the
// same place, which is not true of the ternary arms isPureBlock guards - those
// can be evaluated twice.
func isConditionBlock(b *block) bool {
	for i, instruction := range b.Instructions {
		if pureMnemonics.MatchString(instruction.Mnemonic) || invokes[instruction.Mnemonic] ||
			instruction.Mnemonic == "invokedynamic" {
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
func (d *bodyDecompiler) coerceInto(value expr, target string) string {
	if entry, ok := d.byName[value.Text]; ok && !entry.Authoritative &&
		entry.Type == "int" && erasedToInt[target] {
		d.retype(entry, target)
	}
	return coerce(value, target)
}

// retype gives a local a narrower type, rewriting its declaration and assignments.
func (d *bodyDecompiler) retype(entry *local, target string) {
	entry.Type = target
	declaration := entry.Declaration
	if declaration != nil && !declaration.Inline {
		(*declaration.List)[declaration.Index] = stmt{Text: target + " " + entry.Name + ";"}
	}
	for i, write := range entry.Writes {
		assigned := coerce(write.Value, target)
		text := entry.Name + " = " + assigned + ";"
		if i == 0 && declaration != nil && declaration.Inline {
			text = target + " " + entry.Name + " = " + assigned + ";"
		}
		(*write.List)[write.Index] = stmt{Text: text}
	}
	for _, emitted := range d.conditions {
		(*emitted.List)[emitted.Index] = stmt{Text: emitted.Wrap(d.renderCondition(emitted.Condition).Text)}
	}
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

var (
	expressionStart = regexp.MustCompile(`^[\w$.\[\]()]`)
	notAnExpression = regexp.MustCompile(`^(?:return|break|continue|throw|new)\b`)
)

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
		entry, ok := d.byName[logic.Left.Text]
		if !ok || entry.Type == logic.Left.Type {
			return condition
		}
		value := primary(entry.Name, entry.Type)
		if entry.Type == "boolean" && logic.Right.Text == "0" {
			if logic.Op == "!=" {
				return value
			}
			if logic.Op == "==" {
				return notExpr(value)
			}
		}
		return compareExpr(value, logic.Op, primary(coerce(*logic.Right, entry.Type), entry.Type))
	}
}

// reads reports whether text reads name - a variable or a field reference,
// matched whole so a longer name that contains it does not count.
func reads(text, name string) bool {
	if !strings.Contains(text, name) {
		return false
	}
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(text)
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
	elements := append(append([]string{}, init.Elements...), d.coerceInto(value, init.Element))
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
		if scoped != nil {
			// javac writes one row per scope range, so the same variable can
			// appear twice for one slot (once per arm of an `if`); the name and
			// type are what say it is the same one.
			origin := existing.Origin
			if origin == scoped ||
				(origin != nil && origin.Name == scoped.Name && origin.Type == scoped.Type) {
				return existing, nil
			}
		} else if !isStore || existing.Authoritative || existing.Type == fallbackType {
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

// callArguments pops a call's arguments in reverse and writes them as the
// parameters.
func (d *bodyDecompiler) callArguments(descriptor string) ([]string, error) {
	params := parameterSlots(descriptor, true)
	args := make([]string, len(params))
	for i := len(params) - 1; i >= 0; i-- {
		value, err := d.pop()
		if err != nil {
			return nil, err
		}
		target := params[i].Type
		text := d.coerceInto(value, target)
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
		if narrows || (text == "null" && target == "java.lang.Object") {
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
func (d *bodyDecompiler) coercedExpr(value expr, target string) expr {
	text := d.coerceInto(value, target)
	if text == value.Text {
		return value
	}
	// What the rewrite produces is a literal (`true`, `'a'`) or something with an
	// operator in it (`(char) 200`, a materialized boolean's own ternary); the
	// latter gets the lowest precedence, so it is parenthesized wherever it lands
	// rather than needing its real level worked out.
	prec := 0
	if noSpaceText.MatchString(text) {
		prec = precPrimary
	}
	return expr{Text: text, Prec: prec, Type: target}
}

// concat reconstructs a string concatenation, which javac compiles to an
// invokedynamic whose bootstrap is StringConcatFactory.makeConcatWithConstants:
// the recipe (its first bootstrap argument) is the result text with \u0001 where
// a stack argument goes and \u0002 where one of the remaining bootstrap
// constants does. makeConcat is the same call with no literal parts at all.
func (d *bodyDecompiler) concat(index uint16) error {
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
	// Every other bootstrap - a lambda, a method reference, a record's generated
	// members, a pattern switch - is a later phase.
	if !haveSite || !haveBootstrap || !haveFactory ||
		factory.Owner != "java/lang/invoke/StringConcatFactory" ||
		(factory.Name != "makeConcatWithConstants" && factory.Name != "makeConcat") ||
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
		args[i] = d.coercedExpr(value, params[i].Type)
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
func (d *bodyDecompiler) receiverCallee(mnemonic, owner, name string) (string, error) {
	receiver, err := d.pop()
	if err != nil {
		return "", err
	}
	if mnemonic != "invokespecial" || owner == d.classFile.ThisClass {
		return at(receiver, precPrimary) + "." + name, nil
	}
	// The only other invokespecial source writes is `super.m()`; an interface's
	// `Iface.super.m()` needs the interface named, which this phase does not do.
	superClass := d.classFile.SuperClass
	if superClass == "" {
		superClass = "java/lang/Object"
	}
	if owner != superClass || receiver.Text != "this" {
		return "", bail("unsupported instruction invokespecial")
	}
	return "super." + name, nil
}

// construct writes a constructor call: either `new C(...)`, whose object is
// already on the stack, or the `super(...)`/`this(...)` that opens a
// constructor - which is not a call in source but the shape of one, and without
// which no constructor decompiles at all.
func (d *bodyDecompiler) construct(target MemberRef) error {
	// An inner class's constructor takes the enclosing instance as its first
	// argument, and source cannot pass it: `outer.new Inner(...)` is the only
	// way to write one. The InnerClasses attribute of *this* file says which of
	// the nested classes it names are `static`; without an entry, the shape of
	// the descriptor is all there is to go on.
	if cut := strings.LastIndexByte(target.Owner, '$'); cut > 0 {
		inner := false
		if access, ok := d.innerFlags[target.Owner]; ok {
			inner = access&accStatic == 0
		} else {
			enclosing := strings.ReplaceAll(target.Owner[:cut], "/", ".")
			params := parameterSlots(target.Descriptor, true)
			inner = len(params) > 0 && params[0].Type == enclosing
		}
		if inner {
			return bail("an inner class constructor")
		}
	}
	args, err := d.callArguments(target.Descriptor)
	if err != nil {
		return err
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
		if len(d.statements) > 0 || d.depth > 0 {
			return bail("constructor call is not first")
		}
		// javac writes the implicit `super()` into every constructor; source
		// does not, and re-emitting puts it back. An enum constructor's
		// `super(name, ordinal)` is generated too - and writing it is a compile
		// error.
		if isSuper && (len(args) == 0 || isEnumDeclaration(d.classFile)) {
			return nil
		}
		keyword := "this"
		if isSuper {
			keyword = "super"
		}
		d.emit(keyword + "(" + strings.Join(args, ", ") + ");")
		return nil
	}
	value := expr{
		Text:    "new " + receiver.Type + "(" + strings.Join(args, ", ") + ")",
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

func (d *bodyDecompiler) store(slot, scopePc int, value expr, declaredType string) error {
	target, err := d.local(slot, scopePc, declaredType, true)
	if err != nil {
		return err
	}
	target.StoreBlocks[d.currentBlock] = true
	text := d.coerceInto(value, target.Type)
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
	regions, err := tryRegions(exceptions, blocks, d.self())
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
	if len(d.regions) > 0 {
		d.dominators = dominators(withExceptionEdges(blocks, d.regions), entry)
	}
	loops, err := findLoops(blocks, entry, d.regions)
	if err != nil {
		return err
	}
	d.loops = loops
	d.entryPc = entry
	d.currentBlock = entry
	if err := d.structure(entry, exitBlock); err != nil {
		return err
	}
	if len(d.stack) > 0 {
		return bail("values left on the stack")
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
	at := entry
	for at != stop && at != exitBlock {
		jump, jumped, err := d.loopJump(at)
		if err != nil {
			return err
		}
		if jumped {
			d.emit(jump)
			return nil
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
// loop's next iteration, or the code after it, begins. Leaving an *enclosing*
// loop needs a label, which this phase does not write.
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
	for _, outer := range outerLoops {
		if at == outer.ContinueTarget || at == outer.Loop.Follow {
			return "", false, bail("a labeled break or continue")
		}
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
	if clause == "" {
		d.emitCondition(condition, whileWrap)
	} else {
		d.emitCondition(condition, func(text string) string {
			return "for (; " + text + "; " + clause + ") {"
		})
	}
	*d.current = append(*d.current, stmt{Nested: &statements})
	d.emit("}")
	return l.Follow, nil
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
	if len(latch.Instructions) < 2 || d.visited[latch.Start] {
		return nil
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

// updateClause is the `for`'s update clause: the update block's statements,
// which have to be expressions - a `for` takes a comma-separated list of them,
// nothing else. It reports "" when they are not.
func (d *bodyDecompiler) updateClause(update *block) (string, error) {
	d.visited[update.Start] = true
	statements, err := d.capture(func() error {
		return d.runInstructions(update.Instructions[:len(update.Instructions)-1], endOf(update), update.Start)
	})
	if err != nil {
		return "", err
	}
	var written []string
	for _, statement := range statements {
		if statement.Nested != nil || !strings.HasSuffix(statement.Text, ";") {
			return "", nil
		}
		text := strings.TrimSuffix(statement.Text, ";")
		// A declaration or a jump is not an expression; a `for` takes no others.
		if !expressionStart.MatchString(text) || notAnExpression.MatchString(text) {
			return "", nil
		}
		written = append(written, text)
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
	d.emit("do {")
	*d.current = append(*d.current, stmt{Nested: &statements})
	d.emitCondition(condition, doWhileWrap)
	return l.Follow, nil
}

// foreverLoop writes `while (true) { ... }`: nothing at the head decides whether
// to go round again.
func (d *bodyDecompiler) foreverLoop(l *loop) (int, error) {
	d.active = append(d.active, activeLoop{Loop: l, ContinueTarget: l.Header, Continues: true})
	defer func() { d.active = d.active[:len(d.active)-1] }()
	statements, err := d.capture(func() error { return d.structure(l.Header, exitBlock) })
	if err != nil {
		return 0, err
	}
	statements = trimTail(statements, "continue;")
	d.emit("while (true) {")
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
		d.dominators = dominators(withExceptionEdges(d.blocks, d.regions), d.entryPc)
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
	// A `continue` in one case skips the end of the statement, which leaves the
	// post-dominator on the loop's own edge instead of on it. What the statement
	// really ends at is then what the rule below finds - it never lands there.
	if follow != exitBlock {
		onLoopEdge := false
		for _, entered := range d.active {
			if entered.ContinueTarget == follow || entered.Loop.Follow == follow {
				onLoopEdge = true
				break
			}
		}
		if onLoopEdge {
			// Only a block every case leads to: the `default`'s own body is where
			// a `switch` with no `default` ends, and inside a loop that is not it.
			if earlier := d.switchFollowOf(b, cases, defaultTarget, false); earlier != exitBlock && earlier < follow {
				follow = earlier
			}
		}
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
	return compareExpr(value, op, intLiteral(0)), nil
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

func (d *bodyDecompiler) runInstructions(instructions []Instruction, endPc, blockStart int) error {
	outer := d.currentBlock
	d.currentBlock = blockStart
	err := d.runSteps(instructions, endPc)
	d.currentBlock = outer
	return err
}

func (d *bodyDecompiler) runSteps(instructions []Instruction, endPc int) error {
	for i, instruction := range instructions {
		// A store's variable comes into scope after the store, so the debug table
		// is searched at the next instruction's pc, not the store's own.
		nextPc := endPc
		if i+1 < len(instructions) {
			nextPc = instructions[i+1].Pc
		}
		if err := d.step(instruction, nextPc); err != nil {
			return err
		}
	}
	return nil
}

func isOneOf(base string, prefixes string, suffix string) bool {
	return len(base) == len(suffix)+1 && strings.HasSuffix(base, suffix) &&
		strings.IndexByte(prefixes, base[0]) >= 0
}

func (d *bodyDecompiler) step(instruction Instruction, nextPc int) error {
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
	if isOneOf(base, "ilfda", "load") {
		slot := slotOf(instruction)
		if base == "aload" && slot == 0 && !d.isStatic {
			d.push(primary("this", d.self()))
			return nil
		}
		target, err := d.local(slot, pc, primitiveOfPrefix[base[0]], false)
		if err != nil {
			return err
		}
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
		return d.store(slotOf(instruction), nextPc, value, fallback)
	}
	if base == "iinc" {
		target, err := d.local(instruction.Arg, pc, "int", false)
		if err != nil {
			return err
		}
		// `i++` is a statement here, so anything already on the stack that reads
		// the variable would read the *new* value: `f(i++, i)` and `"x" + i++ + i`
		// are not what this writes back. Their form needs the increment to stay an
		// expression, which this phase does not do.
		for _, value := range d.stack {
			if reads(value.Text, target.Name) {
				return bail("an increment of a variable that is already on the stack")
			}
		}
		switch delta := instruction.Arg2; {
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
		d.push(binaryExpr(numeric(left), operator.operator, numeric(right),
			operator.prec, primitiveOfPrefix[mnemonic[0]]))
		return nil
	}
	if isOneOf(mnemonic, "ilfd", "neg") {
		value, err := d.pop()
		if err != nil {
			return err
		}
		value = numeric(value)
		// A unary operand needs the parens too: `-(-a)` is not `--a`.
		d.push(expr{Text: "-" + at(value, precUnary+1), Prec: precUnary, Type: primitiveOfPrefix[mnemonic[0]]})
		return nil
	}
	if conversion, ok := conversions[mnemonic]; ok {
		value, err := d.pop()
		if err != nil {
			return err
		}
		value = numeric(value)
		d.push(expr{Text: "(" + conversion + ") " + at(value, precUnary), Prec: precUnary, Type: conversion})
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
		target, err := d.pop()
		if err != nil {
			return err
		}
		d.push(primary(at(target, precPrimary)+"."+field.Name, fieldType))
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
			receiver, err := d.pop()
			if err != nil {
				return err
			}
			target = at(receiver, precPrimary) + "." + field.Name
		}
		// The assignment is a statement here, so anything already on the stack that
		// reads the same field would read the *new* value: `arr[idx++]` writes the
		// increment out first, and the read has to stay behind it.
		for _, stacked := range d.stack {
			if reads(stacked.Text, target) {
				return bail("an assignment to a field that is already on the stack")
			}
		}
		d.emit(target + " = " + d.coerceInto(value, fieldType) + ";")
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
		index, err := d.pop()
		if err != nil {
			return err
		}
		array, err := d.pop()
		if err != nil {
			return err
		}
		d.push(primary(at(array, precPrimary)+"["+index.Text+"]", elementType(array.Type, mnemonic[0])))
		return nil
	}
	if isOneOf(mnemonic, "ilfdabcs", "astore") {
		value, err := d.pop()
		if err != nil {
			return err
		}
		index, err := d.pop()
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
		d.emit(at(array, precPrimary) + "[" + coerce(index, "int") + "] = " +
			d.coerceInto(value, element) + ";")
		return nil
	}
	if mnemonic == "newarray" {
		length, err := d.pop()
		if err != nil {
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
		d.push(expr{Text: "(" + typ + ") " + at(value, precUnary), Prec: precUnary, Type: typ})
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
		} else if callee, err = d.receiverCallee(mnemonic, target.Owner, target.Name); err != nil {
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
		return d.concat(uint16(instruction.Arg))
	}

	// Stack shuffling. A dup of anything but a name or an array being filled in
	// would duplicate the expression itself (`new int[2][0] = 1;`), so only
	// those cases are taken; the rest waits for a later phase.
	if mnemonic == "dup" {
		// popRaw, because the copy being duplicated is the array literal that is
		// still being filled in, which pop rejects as incomplete.
		value, err := d.popRaw()
		if err != nil {
			return err
		}
		if value.Effects {
			return bail("dup of a call")
		}
		if value.Compared != nil {
			return bail("dup of a comparison")
		}
		if value.Pending == 0 && value.Init == nil &&
			(value.Prec != precPrimary || strings.HasPrefix(value.Text, "new ")) {
			return bail("dup of a non-trivial value")
		}
		d.push(value)
		d.push(value)
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
		d.emit("return " + d.coerceInto(value, d.returnType) + ";")
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
// be initialized to.
func constantValue(field Member, classFile *ClassFile) (expr, bool) {
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
		if len(params) == 0 {
			return "" // the implicit super(), regenerated
		}
		args := make([]string, len(params))
		for i, parameter := range params {
			args[i] = defaultValue(sourceTypeText(parameter.Type, selfOf(classFile)))
		}
		keyword := "this"
		if isSuper {
			keyword = "super"
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
	body, reached, err := decompileBody(classFile, code, instructions, locals, localTable, method, isStatic)
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
		if len(reached) > 0 && (strings.HasPrefix(reached[0], "super(") || strings.HasPrefix(reached[0], "this(")) {
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
) (body []string, reached []string, err error) {
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
	}
	d.current = &d.statements
	for _, parameter := range locals {
		d.names[parameter.Name] = true
		d.byName[parameter.Name] = parameter
	}
	if err := d.run(instructions, code.Exceptions); err != nil {
		return nil, flattenStatements(d.statements), err
	}
	body = append(flattenStatements(d.hoisted), flattenStatements(d.statements)...)
	// Every void method ends in a `return` javac inserted; source does not.
	if len(body) > 0 && body[len(body)-1] == "return;" {
		body = body[:len(body)-1]
	}
	return body, flattenStatements(d.statements), nil
}

// generatedFields are the fields javac writes for itself, and regenerates from
// source.
var generatedFields = map[string]bool{"$VALUES": true, "$assertionsDisabled": true}

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

func classHead(classFile *ClassFile) string {
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
		body, reconstructed, err := methodSource(method, classFile)
		if err != nil {
			return "", err
		}
		if !reconstructed && method.Name == "<clinit>" {
			staticInitializerLost = true
		}
		bodies = append(bodies, body)
	}

	lines = append(lines, classHead(classFile))
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
