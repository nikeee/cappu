package compiler

// Port of src/compiler/decompile.ts.
//
// `cappu decompile`, phase 1.3 (nikeee/cappu#43): reconstruct Java source from
// straight-line bytecode. A symbolic stack interpreter walks a method's
// instructions and turns them back into expressions and statements; anything
// that needs control flow or a method call (later phases) renders as its
// disassembly plus a `throw new UnsupportedOperationException(...)`, so the
// output is always compilable Java.
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
	precOr      = 5
	precXor     = 6
	precAnd     = 7
	precRel     = 9
	precShift   = 10
	precAdd     = 11
	precMul     = 12
	precUnary   = 14
	precPrimary = 15
)

// expr is a reconstructed value: its source text, how tightly it binds, and its
// Java type (used to declare locals).
type expr struct {
	Text string
	Prec int
	Type string
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

type localWrite struct {
	Index int
	Value expr
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
	// Writes records where the declaration and every assignment landed, so a
	// retype can rewrite them.
	Writes []localWrite
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
type bodyDecompiler struct {
	classFile  *ClassFile
	locals     map[int]*local
	localTable []localEntry
	returnType string
	isStatic   bool
	stack      []expr
	statements []string
	// names is every local name handed out so far, so a reused slot cannot
	// shadow one.
	names  map[string]bool
	byName map[string]*local
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
		entry.Type = target
		for i, write := range entry.Writes {
			assigned := coerce(write.Value, target)
			if i == 0 {
				d.statements[write.Index] = target + " " + entry.Name + " = " + assigned + ";"
			} else {
				d.statements[write.Index] = entry.Name + " = " + assigned + ";"
			}
		}
	}
	return coerce(value, target)
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

func (d *bodyDecompiler) pop() (expr, error) {
	if len(d.stack) == 0 {
		return expr{}, bail("stack underflow")
	}
	top := d.stack[len(d.stack)-1]
	d.stack = d.stack[:len(d.stack)-1]
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
			if existing.Origin == scoped {
				return existing, nil
			}
		} else if !isStore || existing.Authoritative || existing.Type == fallbackType {
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
	text := d.coerceInto(value, target.Type)
	target.Writes = append(target.Writes, localWrite{Index: len(d.statements), Value: value})
	if target.Declared {
		d.statements = append(d.statements, target.Name+" = "+text+";")
		return nil
	}
	target.Declared = true
	d.statements = append(d.statements, target.Type+" "+target.Name+" = "+text+";")
	return nil
}

func (d *bodyDecompiler) run(instructions []Instruction) error {
	for i, instruction := range instructions {
		// A store's variable comes into scope after the store, so the debug
		// table is searched at the next instruction's pc, not the store's own.
		nextPc := instruction.Pc + 1
		if i+1 < len(instructions) {
			nextPc = instructions[i+1].Pc
		}
		if err := d.step(instruction, nextPc); err != nil {
			return err
		}
		if strings.HasSuffix(instruction.Mnemonic, "return") && i != len(instructions)-1 {
			// Code after a return only exists because something branches over it.
			return bail("unreachable code")
		}
	}
	if len(d.stack) > 0 {
		return bail("values left on the stack")
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
		fallback := primitiveOfPrefix[base[0]]
		if base == "astore" {
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
			d.statements = append(d.statements, target.Name+"++;")
		case delta == -1:
			d.statements = append(d.statements, target.Name+"--;")
		case delta < 0:
			d.statements = append(d.statements, fmt.Sprintf("%s -= %d;", target.Name, -delta))
		default:
			d.statements = append(d.statements, fmt.Sprintf("%s += %d;", target.Name, delta))
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
		d.statements = append(d.statements, target+" = "+d.coerceInto(value, fieldType)+";")
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
		d.statements = append(d.statements,
			at(array, precPrimary)+"["+index.Text+"] = "+d.coerceInto(value, element)+";")
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
		if len(d.statements) > 0 {
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
		d.statements = append(d.statements, keyword+"("+strings.Join(args, ", ")+");")
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

	// Returns.
	if mnemonic == "return" {
		d.statements = append(d.statements, "return;")
		return nil
	}
	if isOneOf(mnemonic, "ilfda", "return") {
		value, err := d.pop()
		if err != nil {
			return err
		}
		d.statements = append(d.statements, "return "+d.coerceInto(value, d.returnType)+";")
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
			Declared: true, Authoritative: true,
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
	}
	for _, parameter := range locals {
		d.names[parameter.Name] = true
		d.byName[parameter.Name] = parameter
	}
	if err := d.run(instructions); err != nil {
		return nil, d.statements, err
	}
	body = d.statements
	// Every void method ends in a `return` javac inserted; source does not.
	if len(body) > 0 && body[len(body)-1] == "return;" {
		body = body[:len(body)-1]
	}
	return body, d.statements, nil
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
