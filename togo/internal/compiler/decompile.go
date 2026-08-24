package compiler

// Port of src/compiler/decompile.ts.
//
// `cappu decompile`, phases 1.3 and 1.4 (nikeee/cappu#43): reconstruct Java
// source from bytecode without a loop in it. A symbolic stack interpreter walks
// a method's basic blocks and turns them back into expressions and statements,
// with the branches structured into `if`/`else`, `&&`/`||` and `?:`; anything
// that needs a loop or a method call (later phases) renders as its disassembly
// plus a `throw new UnsupportedOperationException(...)`, so the output is always
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

func compareExpr(left expr, op string, right expr) expr {
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
// is a boolean, and the condition is what source wrote - but the int form is
// kept for a use that wants a number back.
func materializedBoolean(condition expr, thenValue string) expr {
	elseValue := "0"
	if thenValue == "0" {
		elseValue = "1"
	}
	out := condition
	out.AsInt = at(condition, precTernary+1) + " ? " + thenValue + " : " + elseValue
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

// Phase 1.4 covers acyclic control flow only: every edge runs forward, so a
// block's pc order is a topological order and one reverse pass computes the
// post-dominators. A back edge is a loop, which is phase 1.5.

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

func isGotoMnemonic(mnemonic string) bool {
	return mnemonic == "goto" || mnemonic == "goto_w"
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
	blockEnd
)

type block struct {
	Start        int
	Instructions []Instruction
	Kind         blockKind
	// Successors of a conditional are [fallthrough, target], in that order.
	Successors []int
}

func buildBlocks(instructions []Instruction) (map[int]*block, error) {
	entry := 0
	if len(instructions) > 0 {
		entry = instructions[0].Pc
	}
	leaders := map[int]bool{entry: true}
	for i, instruction := range instructions {
		mnemonic := instruction.Mnemonic
		if mnemonic == "tableswitch" || mnemonic == "lookupswitch" {
			return nil, bail("switch is not decompiled yet (%s)", mnemonic)
		}
		// The subroutine opcodes: gone since Java 6, and their control flow is
		// not expressible as a branch.
		if subroutineOpcodes[mnemonic] {
			return nil, bail("unsupported instruction %s", mnemonic)
		}
		branch := conditionalBranches[mnemonic] || isGotoMnemonic(mnemonic)
		if branch {
			leaders[instruction.Arg] = true
		}
		if branch || isBlockEndMnemonic(mnemonic) {
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
		case isBlockEndMnemonic(last.Mnemonic):
			kind = blockEnd
			successors = nil
		case !hasNext:
			return bail("the code runs off the end of the method")
		}
		for _, successor := range successors {
			if successor <= start {
				return bail("loops are not decompiled yet")
			}
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

// reachableBlocks are the blocks reachable from the entry, which is all the
// structuring may cover.
func reachableBlocks(blocks map[int]*block, entry int) map[int]bool {
	seen := map[int]bool{}
	queue := []int{entry}
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
func postDominators(blocks map[int]*block) map[int]int {
	starts := make([]int, 0, len(blocks))
	for start := range blocks {
		starts = append(starts, start)
	}
	sort.Ints(starts)
	sets := map[int]map[int]bool{}
	for i := len(starts) - 1; i >= 0; i-- {
		start := starts[i]
		successors := blocks[start].Successors
		if len(successors) == 0 {
			successors = []int{exitBlock}
		}
		var shared map[int]bool
		for _, successor := range successors {
			of := map[int]bool{exitBlock: true}
			if successor != exitBlock && sets[successor] != nil {
				of = sets[successor]
			}
			if shared == nil {
				shared = map[int]bool{}
				for at := range of {
					shared[at] = true
				}
				continue
			}
			for at := range shared {
				if !of[at] {
					delete(shared, at)
				}
			}
		}
		shared[start] = true
		sets[start] = shared
	}
	immediate := map[int]int{}
	for _, start := range starts {
		nearest := exitBlock
		for at := range sets[start] {
			if at == start || at == exitBlock {
				continue
			}
			if nearest == exitBlock || at < nearest {
				nearest = at
			}
		}
		immediate[start] = nearest
	}
	return immediate
}

// pureMnemonics are the instructions a *condition* may be built from: no store,
// no call, nothing that is a statement. A block made only of these can be folded
// into the condition of the branch before it (`a && b`) or into a ternary
// without changing what runs.
var pureMnemonics = regexp.MustCompile(`^(?:nop|aconst_null|[ilfd]const_\w+|bipush|sipush|ldc\w*|` +
	`[ilfda]load(?:_\d|_w)?|arraylength|[ilfdabcs]aload|` +
	`[ilfd](?:add|sub|mul|div|rem|neg|shl|shr|ushr|and|or|xor)|[ilfd]2[ilfdbcs]|` +
	`lcmp|[fd]cmp[lg]|getstatic|getfield|checkcast|instanceof|dup)$`)

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
	names        map[string]bool
	byName       map[string]*local
	blocks       map[int]*block
	followOf     map[int]int
	visited      map[int]bool
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
	return top, nil
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

func (d *bodyDecompiler) run(instructions []Instruction) error {
	blocks, err := buildBlocks(instructions)
	if err != nil {
		return err
	}
	d.blocks = blocks
	d.followOf = postDominators(blocks)
	entry := 0
	if len(instructions) > 0 {
		entry = instructions[0].Pc
	}
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
	for start := range reachableBlocks(d.blocks, entry) {
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
		b := d.blocks[at]
		if b == nil {
			return bail("a branch lands outside the method")
		}
		if d.visited[at] {
			return bail("unstructured control flow")
		}
		d.visited[at] = true
		body := b.Instructions
		if b.Kind == blockConditional || b.Kind == blockGoto {
			body = body[:len(body)-1]
		}
		if err := d.runInstructions(body, endOf(b), b.Start); err != nil {
			return err
		}
		if b.Kind == blockEnd {
			return nil
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
	follow, ok := d.followOf[b.Start]
	if !ok {
		follow = exitBlock
	}

	if follow != exitBlock {
		value, found, err := d.tryTernary(condition, whenTrue, whenFalse, follow)
		if err != nil {
			return 0, err
		}
		if found {
			d.push(value)
			return follow, nil
		}
	}
	// `if (c) return x;` has no merge point: the arm that leaves the method is
	// the whole statement, and the rest of the body follows it at the same level,
	// not inside an `else`.
	if follow == exitBlock {
		if !d.alwaysExits(whenTrue) && d.alwaysExits(whenFalse) {
			condition = negate(condition)
			whenTrue, whenFalse = whenFalse, whenTrue
		}
		if d.alwaysExits(whenTrue) {
			exiting, err := d.capture(func() error { return d.structure(whenTrue, exitBlock) })
			if err != nil {
				return 0, err
			}
			d.emit("if (" + condition.Text + ") {")
			*d.current = append(*d.current, stmt{Nested: &exiting})
			d.emit("}")
			return whenFalse, nil
		}
	}
	// With no merge of their own the arms run to the end of the region they sit
	// in, which is the enclosing `if`'s follow, not the end of the method.
	stopAt := follow
	if follow == exitBlock {
		stopAt = stop
	}
	thenStatements, err := d.capture(func() error { return d.structure(whenTrue, stopAt) })
	if err != nil {
		return 0, err
	}
	elseStatements, err := d.capture(func() error { return d.structure(whenFalse, stopAt) })
	if err != nil {
		return 0, err
	}
	if len(thenStatements) == 0 && len(elseStatements) > 0 {
		d.emit("if (" + negate(condition).Text + ") {")
		*d.current = append(*d.current, stmt{Nested: &elseStatements})
		d.emit("}")
		return stopAt, nil
	}
	d.emit("if (" + condition.Text + ") {")
	*d.current = append(*d.current, stmt{Nested: &thenStatements})
	if len(elseStatements) > 0 {
		d.emit("} else {")
		*d.current = append(*d.current, stmt{Nested: &elseStatements})
	}
	d.emit("}")
	return stopAt, nil
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
	if next == nil || next.Kind != blockConditional || !isPureBlock(next) {
		return jump{}, false, nil, nil
	}
	if folded[start] || d.visited[start] {
		return jump{}, false, nil, nil
	}
	// Nothing outside the chain may reach it, or folding would skip a path in.
	if d.predecessorsOf(start, folded) != 0 {
		return jump{}, false, nil, nil
	}
	stackDepth := len(d.stack)
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
		d.stack = d.stack[:stackDepth]
		*taken = (*taken)[:takenCount]
		for at := range folded {
			delete(folded, at)
		}
		for _, at := range foldedBefore {
			folded[at] = true
		}
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
		return compareExpr(left, op, right), nil
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

// alwaysExits reports whether every path from start leaves the method.
func (d *bodyDecompiler) alwaysExits(start int) bool {
	b := d.blocks[start]
	if b == nil {
		return false
	}
	if b.Kind == blockEnd {
		return true
	}
	for _, successor := range b.Successors {
		if !d.alwaysExits(successor) {
			return false
		}
	}
	return true
}

// tryTernary writes the two arms of a branch as `condition ? a : b`, when both
// are side-effect-free and leave one value behind - which is how javac writes a
// conditional expression, and how a boolean ends up in a variable.
func (d *bodyDecompiler) tryTernary(condition expr, whenTrue, whenFalse, follow int) (expr, bool, error) {
	consumed := []int{}
	before := len(d.stack)
	value, found, err := d.armValues(condition, whenTrue, whenFalse, follow, &consumed)
	if err != nil {
		return expr{}, false, err
	}
	if !found {
		if len(d.stack) > before {
			d.stack = d.stack[:before]
		}
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
		return materializedBoolean(condition, "1"), true, nil
	}
	if thenValue.Text == "0" && elseValue.Text == "1" {
		return materializedBoolean(negate(condition), "0"), true, nil
	}
	value, ok := ternaryExpr(condition, thenValue, elseValue)
	return value, ok, nil
}

// valueOfRegion reports the blocks from start to follow as a single value:
// either one side-effect-free block that leaves it on the stack, or - because a
// short-circuit nests them - another branch whose arms are values themselves.
func (d *bodyDecompiler) valueOfRegion(start, follow int, consumed *[]int) (expr, bool, error) {
	b := d.blocks[start]
	if b == nil || !isPureBlock(b) {
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
		// when the value knows which one it is, that is the variable's type.
		fallback := primitiveOfPrefix[base[0]]
		if base == "astore" || erasedToInt[value.Type] {
			fallback = value.Type
		}
		return d.store(slotOf(instruction), nextPc, value, fallback)
	}
	if base == "iinc" {
		target, err := d.local(instruction.Arg, pc, "int", false)
		if err != nil {
			return err
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
		d.push(binaryExpr(left, operator.operator, right, operator.prec, primitiveOfPrefix[mnemonic[0]]))
		return nil
	}
	if isOneOf(mnemonic, "ilfd", "neg") {
		value, err := d.pop()
		if err != nil {
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
		array, err := d.pop()
		if err != nil {
			return err
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
		d.push(primary("new "+instruction.Operand+"["+length.Text+"]", instruction.Operand+"[]"))
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
		d.push(primary("new "+base+"["+length.Text+"]"+rest, element+"[]"))
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
			sizes[i] = size.Text
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

	// Constructor chaining. `super(...)`/`this(...)` is not a method call in
	// source, it is the shape of a constructor - without it no constructor
	// decompiles at all. Any other invokespecial (a `new`, a private call) is
	// still a later phase.
	if mnemonic == "invokespecial" {
		target, ok := PoolMemberRef(pool, uint16(instruction.Arg))
		if !ok || target.Name != "<init>" {
			return bail("unsupported instruction invokespecial")
		}
		superClass := d.classFile.SuperClass
		if superClass == "" {
			superClass = "java/lang/Object"
		}
		isSuper := target.Owner == superClass
		if !isSuper && target.Owner != d.classFile.ThisClass {
			return bail("constructor call to an unrelated class")
		}
		params := parameterSlots(target.Descriptor, true)
		args := make([]string, len(params))
		for i := len(params) - 1; i >= 0; i-- {
			value, err := d.pop()
			if err != nil {
				return err
			}
			args[i] = d.coerceInto(value, params[i].Type)
		}
		receiver, err := d.pop()
		if err != nil {
			return err
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

	// Stack shuffling. A dup of anything but a name would duplicate the
	// expression itself (`new int[2][0] = 1;`), so only the trivial case is
	// taken; array initializers and the rest wait for a later phase.
	if mnemonic == "dup" {
		value, err := d.pop()
		if err != nil {
			return err
		}
		if value.Prec != precPrimary || strings.HasPrefix(value.Text, "new ") {
			return bail("dup of a non-trivial value")
		}
		d.push(value)
		d.push(value)
		return nil
	}
	if mnemonic == "pop" {
		_, err := d.pop()
		return err
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
	if len(code.Exceptions) > 0 {
		return nil, nil, bail("the method catches exceptions")
	}
	d := &bodyDecompiler{
		classFile:  classFile,
		locals:     locals,
		localTable: localTable,
		returnType: methodReturnType(method.Descriptor),
		isStatic:   isStatic,
		names:      map[string]bool{},
		byName:     map[string]*local{},
		visited:    map[int]bool{},
	}
	d.current = &d.statements
	for _, parameter := range locals {
		d.names[parameter.Name] = true
		d.byName[parameter.Name] = parameter
	}
	if err := d.run(instructions); err != nil {
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
