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

import (
	"encoding/binary"
	"errors"
	"fmt"
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

// typeName renders a `Foo$Bar` binary name as source text. Nested names keep the `$`.
func typeName(internal string) string {
	if strings.HasPrefix(internal, "[") {
		text, _ := DescriptorType(internal, 0)
		return text
	}
	return strings.ReplaceAll(internal, "/", ".")
}

func intLiteral(value int) expr {
	// A negative literal is a unary minus, not part of the token.
	prec := precPrimary
	if value < 0 {
		prec = precUnary
	}
	return expr{Text: strconv.Itoa(value), Prec: prec, Type: "int"}
}

func negatablePrec(text string) int {
	if strings.HasPrefix(text, "-") {
		return precUnary
	}
	return precPrimary
}

func constantExpr(pool []*Constant, index uint16) (expr, error) {
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
		text := JavaFloatText(entry.Float) + "f"
		return expr{Text: text, Prec: negatablePrec(text), Type: "float"}, nil
	case TagDouble:
		text := JavaDoubleText(entry.Double)
		return expr{Text: text, Prec: negatablePrec(text), Type: "double"}, nil
	case TagString:
		return primary(`"`+escapeString(PoolUtf8(pool, entry.Index))+`"`, "java.lang.String"), nil
	case TagClass:
		return primary(typeName(PoolUtf8(pool, entry.Index))+".class", "java.lang.Class"), nil
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

type local struct {
	Name     string
	Type     string
	Declared bool
	// Origin is the debug-table row this name came from, when there is one.
	Origin *localEntry
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
	names map[string]bool
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
func (d *bodyDecompiler) local(slot, pc int, fallbackType string, isStore bool) *local {
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
				return existing
			}
		} else if !isStore || existing.Type == fallbackType {
			return existing
		}
	}
	created := &local{Type: fallbackType, Origin: scoped}
	if scoped != nil && scoped.Name != "" {
		created.Name = scoped.Name
	} else {
		created.Name = d.freshName(slot)
	}
	if scoped != nil && scoped.Type != "" {
		created.Type = scoped.Type
	}
	d.names[created.Name] = true
	d.locals[slot] = created
	return created
}

// freshName is `var<slot>`, kept distinct from the names already handed out.
func (d *bodyDecompiler) freshName(slot int) string {
	name := "var" + strconv.Itoa(slot)
	for n := 2; d.names[name]; n++ {
		name = "var" + strconv.Itoa(slot) + "_" + strconv.Itoa(n)
	}
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

func (d *bodyDecompiler) store(slot, scopePc int, value expr, declaredType string) {
	target := d.local(slot, scopePc, declaredType, true)
	text := coerce(value, target.Type)
	if target.Declared {
		d.statements = append(d.statements, target.Name+" = "+text+";")
		return
	}
	target.Declared = true
	d.statements = append(d.statements, target.Type+" "+target.Name+" "+"= "+text+";")
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
		constant, err := constantExpr(pool, uint16(instruction.Arg))
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
			d.push(primary("this", typeName(d.classFile.ThisClass)))
			return nil
		}
		target := d.local(slot, pc, primitiveOfPrefix[base[0]], false)
		d.push(primary(target.Name, target.Type))
		return nil
	}
	if isOneOf(base, "ilfda", "store") {
		value, err := d.pop()
		if err != nil {
			return err
		}
		fallback := primitiveOfPrefix[base[0]]
		if base == "astore" {
			fallback = value.Type
		}
		d.store(slotOf(instruction), nextPc, value, fallback)
		return nil
	}
	if base == "iinc" {
		target := d.local(instruction.Arg, pc, "int", false)
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
		d.push(expr{Text: "-" + at(value, precUnary), Prec: precUnary, Type: primitiveOfPrefix[mnemonic[0]]})
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
		owner := typeName(field.Owner)
		if mnemonic == "getfield" {
			target, err := d.pop()
			if err != nil {
				return err
			}
			owner = at(target, precPrimary)
		}
		fieldType, _ := DescriptorType(field.Descriptor, 0)
		d.push(primary(owner+"."+field.Name, fieldType))
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
		fieldType, _ := DescriptorType(field.Descriptor, 0)
		owner := typeName(field.Owner)
		if mnemonic == "putfield" {
			target, err := d.pop()
			if err != nil {
				return err
			}
			owner = at(target, precPrimary)
		}
		d.statements = append(d.statements, owner+"."+field.Name+" = "+coerce(value, fieldType)+";")
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
			at(array, precPrimary)+"["+index.Text+"] = "+coerce(value, element)+";")
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
		element := typeName(orDefault(PoolClassName(pool, uint16(instruction.Arg))))
		length, err := d.pop()
		if err != nil {
			return err
		}
		d.push(primary("new "+element+"["+length.Text+"]", element+"[]"))
		return nil
	}
	if mnemonic == "multianewarray" {
		typ := typeName(PoolClassName(pool, uint16(instruction.Arg)))
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
		typ := typeName(orDefault(PoolClassName(pool, uint16(instruction.Arg))))
		value, err := d.pop()
		if err != nil {
			return err
		}
		d.push(expr{Text: "(" + typ + ") " + at(value, precUnary), Prec: precUnary, Type: typ})
		return nil
	}
	if mnemonic == "instanceof" {
		typ := typeName(orDefault(PoolClassName(pool, uint16(instruction.Arg))))
		value, err := d.pop()
		if err != nil {
			return err
		}
		d.push(expr{Text: at(value, precRel+1) + " instanceof " + typ, Prec: precRel, Type: "boolean"})
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
		d.statements = append(d.statements, "return "+coerce(value, d.returnType)+";")
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
	value, err := constantExpr(classFile.Pool, binary.BigEndian.Uint16(attribute.Bytes))
	if err != nil {
		return expr{}, false
	}
	return value, true
}

func fieldSource(field Member, classFile *ClassFile) string {
	fieldType, _ := DescriptorType(field.Descriptor, 0)
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
	if value, ok := constantValue(field, classFile); ok {
		initializer = " = " + coerce(value, fieldType)
	}
	return strings.Join(append(modifiers, fieldType), " ") + " " + field.Name + initializer + ";"
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
func buildLocals(method Member, localTable []localEntry, isStatic bool) map[int]*local {
	locals := map[int]*local{}
	for index, parameter := range parameterSlots(method.Descriptor, isStatic) {
		entry := &local{Name: "arg" + strconv.Itoa(index), Type: parameter.Type, Declared: true}
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
		locals[parameter.Slot] = entry
	}
	return locals
}

func parameterList(method Member, locals map[int]*local, isStatic bool) string {
	slots := parameterSlots(method.Descriptor, isStatic)
	parameters := make([]string, 0, len(slots))
	for index, parameter := range slots {
		name := "arg" + strconv.Itoa(index)
		typ := parameter.Type
		if entry, ok := locals[parameter.Slot]; ok {
			name, typ = entry.Name, entry.Type
		}
		if method.Flags&accVarargs != 0 && index == len(slots)-1 && strings.HasSuffix(typ, "[]") {
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

// isDefaultConstructor reports the `<init>()` javac writes when a class declares
// no constructor: nothing but the implicit `super()` call. Java puts it back, so
// it is not source.
func isDefaultConstructor(method Member, classFile *ClassFile) bool {
	if method.Name != "<init>" || method.Descriptor != "()V" {
		return false
	}
	code, err := ReadCode(method, classFile.Pool)
	if err != nil || code == nil || len(code.Exceptions) > 0 {
		return false
	}
	instructions, err := DecodeInstructions(classFile, code.Code)
	if err != nil || len(instructions) != 3 {
		return false
	}
	if instructions[0].Mnemonic != "aload_0" || instructions[1].Mnemonic != "invokespecial" ||
		instructions[2].Mnemonic != "return" {
		return false
	}
	target, ok := PoolMemberRef(classFile.Pool, uint16(instructions[1].Arg))
	superClass := classFile.SuperClass
	if superClass == "" {
		superClass = "java/lang/Object"
	}
	return ok && target.Name == "<init>" && target.Descriptor == "()V" && target.Owner == superClass
}

func methodSource(method Member, classFile *ClassFile) ([]string, error) {
	isStatic := method.Flags&accStatic != 0
	code, err := ReadCode(method, classFile.Pool)
	if err != nil {
		return nil, err
	}
	var localTable []localEntry
	if code != nil {
		localTable = readLocalVariables(code, classFile.Pool)
	}
	locals := buildLocals(method, localTable, isStatic)

	head := "static"
	if method.Name != "<clinit>" {
		parts := methodModifiers(method, classFile)
		if method.Name == "<init>" {
			parts = append(parts, simpleClassName(classFile.ThisClass)+"("+parameterList(method, locals, isStatic)+")")
		} else {
			parts = append(parts, methodReturnType(method.Descriptor),
				method.Name+"("+parameterList(method, locals, isStatic)+")")
		}
		head = strings.Join(parts, " ")
		var thrown []string
		for _, name := range ReadThrownExceptions(method, classFile.Pool) {
			thrown = append(thrown, typeName(name))
		}
		if len(thrown) > 0 {
			head += " throws " + strings.Join(thrown, ", ")
		}
	}

	if code == nil {
		return []string{head + ";"}, nil
	}

	instructions, err := DecodeInstructions(classFile, code.Code)
	if err != nil {
		return nil, err
	}
	body, err := decompileBody(classFile, code, instructions, locals, localTable, method, isStatic)
	if err != nil {
		var reason *notDecompilable
		if !errors.As(err, &reason) {
			return nil, err
		}
		body = append(bailComment(instructions, reason.reason),
			`throw new UnsupportedOperationException("cappu: not decompiled");`)
	}
	return append(append([]string{head + " {"}, body...), "}"), nil
}

func decompileBody(
	classFile *ClassFile,
	code *Code,
	instructions []Instruction,
	locals map[int]*local,
	localTable []localEntry,
	method Member,
	isStatic bool,
) ([]string, error) {
	if len(code.Exceptions) > 0 {
		return nil, bail("the method catches exceptions")
	}
	d := &bodyDecompiler{
		classFile:  classFile,
		locals:     locals,
		localTable: localTable,
		returnType: methodReturnType(method.Descriptor),
		isStatic:   isStatic,
		names:      map[string]bool{},
	}
	for _, parameter := range locals {
		d.names[parameter.Name] = true
	}
	if err := d.run(instructions); err != nil {
		return nil, err
	}
	body := d.statements
	// Every void method ends in a `return` javac inserted; source does not.
	if len(body) > 0 && body[len(body)-1] == "return;" {
		body = body[:len(body)-1]
	}
	return body, nil
}

func classHead(classFile *ClassFile) string {
	isInterface := classFile.Flags&accInterface != 0
	isAnnotation := classFile.Flags&accAnnotation != 0
	isEnum := classFile.Flags&accEnum != 0
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
	if !isInterface && classFile.Flags&accAbstract != 0 {
		head = append(head, "abstract")
	}
	head = append(head, keyword, simpleClassName(classFile.ThisClass))
	// The implicit supertypes are not written in source.
	implicit := map[string]bool{"java/lang/Object": true, "java/lang/Enum": true, "java/lang/Record": true}
	if !isInterface && classFile.SuperClass != "" && !implicit[classFile.SuperClass] {
		head = append(head, "extends", typeName(classFile.SuperClass))
	}
	var interfaces []string
	for _, name := range classFile.Interfaces {
		if name != "java/lang/annotation/Annotation" {
			interfaces = append(interfaces, typeName(name))
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
	if slash := strings.LastIndex(classFile.ThisClass, "/"); slash > 0 {
		lines = append(lines, "package "+strings.ReplaceAll(classFile.ThisClass[:slash], "/", ".")+";", "")
	}
	lines = append(lines, classHead(classFile))
	for _, field := range classFile.Fields {
		if field.Flags&(accSynthetic|accEnum) != 0 {
			continue
		}
		lines = append(lines, fieldSource(field, classFile))
	}
	for _, method := range classFile.Methods {
		if method.Flags&(accSynthetic|accBridge) != 0 || isDefaultConstructor(method, classFile) {
			continue
		}
		source, err := methodSource(method, classFile)
		if err != nil {
			return "", err
		}
		lines = append(append(lines, ""), source...)
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
