package compiler

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A javap-compatible disassembler: class bytes in, `javap -c -p` shaped text
// out (nikeee/cappu#43, phase 1.2). Matching javap's text exactly is what lets
// the tests reuse javapnormalize.go as an oracle over the checked-in javac
// baselines - no JDK needed at test time.
//
// Port of src/compiler/disasm.ts.

// javap's layout: a 6 column indent with the pc right-aligned in the next 4
// (wider pcs push the line out), the mnemonic in a 13 column field, and the
// `// ...` comment starting at column 46.
// ACC_MODULE (JVMS 4.1) marks a module-info.class; not in the shared flag set.
const accModule = 0x8000

const (
	pcIndent      = 6
	pcWidth       = 4
	mnemonicWidth = 13
	commentColumn = 46
)

// --- opcodes ----------------------------------------------------------------------

type operandKind int

const (
	operandNone operandKind = iota
	operandLocal
	operandI1
	operandI2
	operandCp1
	operandCp2
	operandBranch2
	operandBranch4
	operandIinc
	operandAtype
	operandInvokeInterface
	operandInvokeDynamic
	operandMultiANewArray
	operandTableSwitch
	operandLookupSwitch
	operandWide
)

// Opcodes 0x00..0xc9 in order.
var mnemonics = strings.Fields(
	`nop aconst_null iconst_m1 iconst_0 iconst_1 iconst_2 iconst_3 iconst_4 iconst_5 lconst_0
	lconst_1 fconst_0 fconst_1 fconst_2 dconst_0 dconst_1 bipush sipush ldc ldc_w ldc2_w
	iload lload fload dload aload iload_0 iload_1 iload_2 iload_3 lload_0 lload_1 lload_2
	lload_3 fload_0 fload_1 fload_2 fload_3 dload_0 dload_1 dload_2 dload_3 aload_0 aload_1
	aload_2 aload_3 iaload laload faload daload aaload baload caload saload istore lstore
	fstore dstore astore istore_0 istore_1 istore_2 istore_3 lstore_0 lstore_1 lstore_2
	lstore_3 fstore_0 fstore_1 fstore_2 fstore_3 dstore_0 dstore_1 dstore_2 dstore_3
	astore_0 astore_1 astore_2 astore_3 iastore lastore fastore dastore aastore bastore
	castore sastore pop pop2 dup dup_x1 dup_x2 dup2 dup2_x1 dup2_x2 swap iadd ladd fadd
	dadd isub lsub fsub dsub imul lmul fmul dmul idiv ldiv fdiv ddiv irem lrem frem drem
	ineg lneg fneg dneg ishl lshl ishr lshr iushr lushr iand land ior lor ixor lxor iinc
	i2l i2f i2d l2i l2f l2d f2i f2l f2d d2i d2l d2f i2b i2c i2s lcmp fcmpl fcmpg dcmpl
	dcmpg ifeq ifne iflt ifge ifgt ifle if_icmpeq if_icmpne if_icmplt if_icmpge if_icmpgt
	if_icmple if_acmpeq if_acmpne goto jsr ret tableswitch lookupswitch ireturn lreturn
	freturn dreturn areturn return getstatic putstatic getfield putfield invokevirtual
	invokespecial invokestatic invokeinterface invokedynamic new newarray anewarray
	arraylength athrow checkcast instanceof monitorenter monitorexit wide multianewarray
	ifnull ifnonnull goto_w jsr_w`)

var operands = map[string]operandKind{
	"bipush":          operandI1,
	"sipush":          operandI2,
	"ldc":             operandCp1,
	"ldc_w":           operandCp2,
	"ldc2_w":          operandCp2,
	"iload":           operandLocal,
	"lload":           operandLocal,
	"fload":           operandLocal,
	"dload":           operandLocal,
	"aload":           operandLocal,
	"istore":          operandLocal,
	"lstore":          operandLocal,
	"fstore":          operandLocal,
	"dstore":          operandLocal,
	"astore":          operandLocal,
	"ret":             operandLocal,
	"iinc":            operandIinc,
	"tableswitch":     operandTableSwitch,
	"lookupswitch":    operandLookupSwitch,
	"getstatic":       operandCp2,
	"putstatic":       operandCp2,
	"getfield":        operandCp2,
	"putfield":        operandCp2,
	"invokevirtual":   operandCp2,
	"invokespecial":   operandCp2,
	"invokestatic":    operandCp2,
	"invokeinterface": operandInvokeInterface,
	"invokedynamic":   operandInvokeDynamic,
	"new":             operandCp2,
	"newarray":        operandAtype,
	"anewarray":       operandCp2,
	"checkcast":       operandCp2,
	"instanceof":      operandCp2,
	"wide":            operandWide,
	"multianewarray":  operandMultiANewArray,
	"goto_w":          operandBranch4,
	"jsr_w":           operandBranch4,
}

func init() {
	for _, branch := range []string{
		"ifeq", "ifne", "iflt", "ifge", "ifgt", "ifle",
		"if_icmpeq", "if_icmpne", "if_icmplt", "if_icmpge", "if_icmpgt", "if_icmple",
		"if_acmpeq", "if_acmpne", "goto", "jsr", "ifnull", "ifnonnull",
	} {
		operands[branch] = operandBranch2
	}
}

var arrayTypes = map[uint8]string{
	4: "boolean", 5: "char", 6: "float", 7: "double",
	8: "byte", 9: "short", 10: "int", 11: "long",
}

var referenceKinds = map[uint8]string{
	1: "REF_getField", 2: "REF_getStatic", 3: "REF_putField", 4: "REF_putStatic",
	5: "REF_invokeVirtual", 6: "REF_invokeStatic", 7: "REF_invokeSpecial",
	8: "REF_newInvokeSpecial", 9: "REF_invokeInterface",
}

// --- constants as javap renders them ----------------------------------------------

// javaFloatingPoint formats a finite value the way Double.toString and
// Float.toString do.
func javaFloatingPoint(negative bool, digits string, exponent int) string {
	sign := ""
	if negative {
		sign = "-"
	}
	if exponent < -3 || exponent >= 7 {
		tail := digits[1:]
		if tail == "" {
			tail = "0"
		}
		return fmt.Sprintf("%s%c.%sE%d", sign, digits[0], tail, exponent)
	}
	if exponent >= 0 {
		whole := digits
		if len(whole) > exponent+1 {
			whole = whole[:exponent+1]
		}
		whole += strings.Repeat("0", exponent+1-len(whole))
		fraction := ""
		if len(digits) > exponent+1 {
			fraction = digits[exponent+1:]
		}
		if fraction == "" {
			fraction = "0"
		}
		return sign + whole + "." + fraction
	}
	return sign + "0." + strings.Repeat("0", -exponent-1) + digits
}

type roundedDigits struct {
	digits string
	carry  bool
}

// bracket returns the two `precision`-digit decimals bracketing a digit string.
func bracket(digits string, precision int) (down, up roundedDigits) {
	kept := digits
	if len(kept) > precision {
		kept = kept[:precision]
	}
	kept += strings.Repeat("0", precision-len(kept))
	raised := new(big.Int)
	raised.SetString(kept, 10)
	raised.Add(raised, big.NewInt(1))
	raisedText := raised.String()
	if len(raisedText) > precision {
		return roundedDigits{digits: kept}, roundedDigits{digits: raisedText[:precision], carry: true}
	}
	return roundedDigits{digits: kept},
		roundedDigits{digits: strings.Repeat("0", precision-len(raisedText)) + raisedText}
}

// shortestDigits finds the shortest decimal that reads back as the same value -
// what Float.toString and Double.toString render. Both bracketing candidates are
// tried at every length; when both read back, the one closer to the exact value
// wins, and an exact halfway case goes to the even digit. Java never renders a
// subnormal with a single significant digit, so those start the search at two.
func shortestDigits(
	magnitude float64,
	maximumDigits, minimumDigits int,
	readsBack func(candidate string) bool,
) (string, int) {
	// 40 significant digits is exact enough to round correctly (and to spot the
	// exact halfway cases, which terminate well inside that).
	mantissa, exponentText, _ := strings.Cut(strconv.FormatFloat(magnitude, 'e', 39, 64), "e")
	exact := strings.Replace(mantissa, ".", "", 1)
	exponent, _ := strconv.Atoi(exponentText)
	value := func(rounded roundedDigits, precision int) string {
		shift := exponent - precision + 1
		if rounded.carry {
			shift++
		}
		return fmt.Sprintf("%se%d", rounded.digits, shift)
	}

	for precision := minimumDigits; precision <= maximumDigits; precision++ {
		down, up := bracket(exact, precision)
		rest := ""
		if len(exact) > precision {
			rest = exact[precision:]
		}
		downFits := readsBack(value(down, precision))
		upFits := readsBack(value(up, precision))
		var pick *roundedDigits
		switch {
		case downFits && upFits:
			half := "5" + strings.Repeat("0", max(0, len(rest)-1))
			switch {
			case rest > half:
				pick = &up
			case rest < half:
				pick = &down
			case (down.digits[precision-1]-'0')%2 == 0:
				pick = &down
			default:
				pick = &up
			}
		case downFits:
			pick = &down
		case upFits:
			pick = &up
		}
		if pick != nil {
			carried := 0
			if pick.carry {
				carried = 1
			}
			return pick.digits, exponent + carried
		}
	}
	_, up := bracket(exact, maximumDigits)
	carried := 0
	if up.carry {
		carried = 1
	}
	return up.digits, exponent + carried
}

const (
	smallestNormalFloat  = 1.1754943508222875e-38 // 2^-126
	smallestNormalDouble = 2.2250738585072014e-308
)

// JavaDoubleText renders a double the way Double.toString does.
func JavaDoubleText(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	}
	negative := math.Signbit(value)
	if value == 0 {
		if negative {
			return "-0.0"
		}
		return "0.0"
	}
	magnitude := math.Abs(value)
	minimum := 1
	if magnitude < smallestNormalDouble {
		minimum = 2
	}
	digits, exponent := shortestDigits(magnitude, 17, minimum, func(candidate string) bool {
		parsed, err := strconv.ParseFloat(candidate, 64)
		return err == nil && parsed == magnitude
	})
	return javaFloatingPoint(negative, digits, exponent)
}

// JavaFloatText renders a float the way Float.toString does.
func JavaFloatText(value float32) string {
	magnitude64 := math.Abs(float64(value))
	switch {
	case math.IsNaN(magnitude64):
		return "NaN"
	case math.IsInf(magnitude64, 1):
		if math.Signbit(float64(value)) {
			return "-Infinity"
		}
		return "Infinity"
	}
	negative := math.Signbit(float64(value))
	if value == 0 {
		if negative {
			return "-0.0"
		}
		return "0.0"
	}
	magnitude := float32(math.Abs(float64(value)))
	minimum := 1
	if float64(magnitude) < smallestNormalFloat {
		minimum = 2
	}
	digits, exponent := shortestDigits(float64(magnitude), 9, minimum, func(candidate string) bool {
		parsed, err := strconv.ParseFloat(candidate, 32)
		return err == nil && float32(parsed) == magnitude
	})
	return javaFloatingPoint(negative, digits, exponent)
}

func escapeString(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); {
		// An unpaired surrogate survives decoding as its WTF-8 bytes (see
		// decodeModifiedUTF8); it has no encoding at all, and javap writes "?".
		if i+2 < len(value) && value[i] == 0xed && value[i+1] >= 0xa0 {
			out.WriteByte('?')
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		switch {
		case r == '\\':
			out.WriteString(`\\`)
		case r == '"':
			out.WriteString(`\"`)
		case r == '\'':
			out.WriteString(`\'`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case r == '\b':
			out.WriteString(`\b`)
		case r == '\f':
			out.WriteString(`\f`)
		// javap escapes the C0 and C1 control ranges; everything else prints as is.
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			fmt.Fprintf(&out, "\\u%04x", r)
		default:
			out.WriteString(value[i : i+size])
		}
		i += size
	}
	return out.String()
}

// isJavaIdentifier mirrors Character.isJavaIdentifierStart / isJavaIdentifierPart:
// letters of any script, not just ASCII (javap prints `café` bare, not quoted).
func isJavaIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		start := unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || r == '$' || r == '_'
		if start {
			continue
		}
		if i == 0 {
			return false
		}
		if !unicode.IsDigit(r) && !unicode.Is(unicode.Mn, r) &&
			!unicode.Is(unicode.Mc, r) && !unicode.Is(unicode.Pc, r) {
			return false
		}
	}
	return true
}

// quoteName quotes a member name that is not a plain Java identifier (`"<init>"`).
func quoteName(name string) string {
	if isJavaIdentifier(name) {
		return name
	}
	return `"` + name + `"`
}

// quoteClass quotes a class name that is an array descriptor (`class "[[I"`).
func quoteClass(name string) string {
	if strings.HasPrefix(name, "[") {
		return `"` + name + `"`
	}
	return name
}

// constantComment is the `// ...` comment javap prints for a constant-pool operand.
func constantComment(classFile *ClassFile, index uint16) (string, bool) {
	pool := classFile.Pool
	entry := PoolAt(pool, index)
	if entry == nil {
		return "", false
	}
	switch entry.Tag {
	case TagClass:
		return "class " + quoteClass(PoolClassName(pool, index)), true
	case TagString:
		return "String " + escapeString(PoolUtf8(pool, entry.Index)), true
	case TagInt:
		return fmt.Sprintf("int %d", entry.Int), true
	case TagLong:
		return fmt.Sprintf("long %dl", entry.Long), true
	case TagFloat:
		return "float " + JavaFloatText(entry.Float) + "f", true
	case TagDouble:
		return "double " + JavaDoubleText(entry.Double) + "d", true
	case TagFieldref, TagMethodref, TagInterfaceMethodref:
		ref, ok := PoolMemberRef(pool, index)
		if !ok {
			return "", false
		}
		kind := "Method"
		switch entry.Tag {
		case TagFieldref:
			kind = "Field"
		case TagInterfaceMethodref:
			kind = "InterfaceMethod"
		}
		// javap omits the owner when the member lives in the class being printed.
		owner := ""
		if ref.Owner != classFile.ThisClass {
			owner = quoteClass(ref.Owner) + "."
		}
		return fmt.Sprintf("%s %s%s:%s", kind, owner, quoteName(ref.Name), ref.Descriptor), true
	case TagMethodType:
		return "MethodType " + PoolUtf8(pool, entry.Index), true
	case TagMethodHandle:
		target, _ := constantComment(classFile, entry.RefIndex)
		kind, ok := referenceKinds[entry.RefKind]
		if !ok {
			kind = fmt.Sprintf("REF_%d", entry.RefKind)
		}
		// The nested comment is "Method x.y:()V"; javap prints only its body.
		if space := strings.Index(target, " "); space >= 0 {
			target = target[space+1:]
		}
		return fmt.Sprintf("MethodHandle %s %s", kind, target), true
	case TagDynamic, TagInvokeDynamic:
		name, descriptor, _ := PoolNameAndType(pool, entry.NameAndType)
		kind := "InvokeDynamic"
		if entry.Tag == TagDynamic {
			kind = "Dynamic"
		}
		return fmt.Sprintf("%s #%d:%s:%s", kind, entry.Bootstrap, name, descriptor), true
	}
	return "", false
}

// --- instruction decoding ----------------------------------------------------------

// Instruction is one decoded instruction in javap's rendering.
type Instruction struct {
	Pc         int
	Mnemonic   string
	Operand    string
	Comment    string
	HasComment bool
	// ExtraLines are the `{ ... }` body lines of a table/lookupswitch.
	ExtraLines []string
	// Arg is the instruction's numeric operand, for consumers that need it
	// structured rather than rendered (decompile.go): a constant-pool index, a
	// local slot or an immediate value, depending on the mnemonic. 0 when there
	// is none.
	Arg int
	// Arg2 is the second numeric operand: the `iinc` delta, `multianewarray`
	// dimensions.
	Arg2 int
}

// Bounds-checked, because the code array comes straight from the file: an
// instruction that runs off the end is a corrupt class, not a crash (javap:
// "Fatal error: attribute Code too big to handle", exit 1). Once `overrun` is
// set every read yields zero, so decoding unwinds instead of walking on.
type codeCursor struct {
	b       []byte
	at      int
	overrun bool
}

func (c *codeCursor) require(n int) bool {
	if c.at+n > len(c.b) {
		c.overrun = true
		return false
	}
	return true
}

func (c *codeCursor) u1() uint8 {
	if !c.require(1) {
		return 0
	}
	v := c.b[c.at]
	c.at++
	return v
}

func (c *codeCursor) i1() int8 { return int8(c.u1()) }

func (c *codeCursor) u2() uint16 {
	if !c.require(2) {
		return 0
	}
	v := binary.BigEndian.Uint16(c.b[c.at:])
	c.at += 2
	return v
}

func (c *codeCursor) i2() int16 { return int16(c.u2()) }

func (c *codeCursor) i4() int32 {
	if !c.require(4) {
		return 0
	}
	v := int32(binary.BigEndian.Uint32(c.b[c.at:]))
	c.at += 4
	return v
}

// align4 moves to the 4-byte boundary a table/lookupswitch's operands start at.
func (c *codeCursor) align4() {
	c.at += (4 - (c.at % 4)) % 4
	c.require(0)
}

func switchBody(keys []string, targets []int) []string {
	lines := make([]string, 0, len(keys)+1)
	for i, key := range keys {
		lines = append(lines, fmt.Sprintf("%24s: %d", key, targets[i]))
	}
	return append(lines, strings.Repeat(" ", 12)+"}")
}

// DecodeInstructions decodes a Code attribute's bytes into javap's instruction
// stream, or reports that the stream ends inside an instruction.
func DecodeInstructions(classFile *ClassFile, code []byte) ([]Instruction, error) {
	c := &codeCursor{b: code}
	var out []Instruction
	for c.at < len(code) && !c.overrun {
		pc := c.at
		opcode := int(c.u1())
		if opcode >= len(mnemonics) {
			out = append(out, Instruction{Pc: pc, Mnemonic: fmt.Sprintf("unknown 0x%x", opcode)})
			continue
		}
		mnemonic := mnemonics[opcode]
		kind := operands[mnemonic]
		wide := false
		if kind == operandWide {
			// The wide prefix widens the following instruction's operands; javap
			// prints those as `<mnemonic>_w`.
			wide = true
			widenedOpcode := int(c.u1())
			if widenedOpcode >= len(mnemonics) {
				return nil, errors.New("wide prefix on an unknown opcode")
			}
			widened := mnemonics[widenedOpcode]
			mnemonic = widened + "_w"
			kind = operands[widened]
		}
		instruction := Instruction{Pc: pc, Mnemonic: mnemonic}
		switch kind {
		case operandNone:
		case operandLocal:
			if wide {
				instruction.Arg = int(c.u2())
			} else {
				instruction.Arg = int(c.u1())
			}
			instruction.Operand = strconv.Itoa(instruction.Arg)
		case operandI1:
			instruction.Arg = int(c.i1())
			instruction.Operand = strconv.Itoa(instruction.Arg)
		case operandI2:
			instruction.Arg = int(c.i2())
			instruction.Operand = strconv.Itoa(instruction.Arg)
		case operandCp1, operandCp2:
			var index uint16
			if kind == operandCp1 {
				index = uint16(c.u1())
			} else {
				index = c.u2()
			}
			instruction.Arg = int(index)
			instruction.Operand = fmt.Sprintf("#%d", index)
			instruction.Comment, instruction.HasComment = constantComment(classFile, index)
		case operandBranch2:
			// The absolute target, which is what javap prints and what
			// decompile.go builds its control-flow graph from.
			instruction.Arg = pc + int(c.i2())
			instruction.Operand = strconv.Itoa(instruction.Arg)
		case operandBranch4:
			instruction.Arg = pc + int(c.i4())
			instruction.Operand = strconv.Itoa(instruction.Arg)
		case operandIinc:
			var slot, delta int
			if wide {
				slot, delta = int(c.u2()), int(c.i2())
			} else {
				slot, delta = int(c.u1()), int(c.i1())
			}
			instruction.Arg, instruction.Arg2 = slot, delta
			instruction.Operand = fmt.Sprintf("%d, %d", slot, delta)
		case operandAtype:
			name, ok := arrayTypes[c.u1()]
			if !ok {
				name = "?"
			}
			instruction.Operand = name
		case operandInvokeInterface:
			index := c.u2()
			count := c.u1()
			c.u1() // the required trailing zero byte
			instruction.Arg, instruction.Arg2 = int(index), int(count)
			instruction.Operand = fmt.Sprintf("#%d,  %d", index, count)
			instruction.Comment, instruction.HasComment = constantComment(classFile, index)
		case operandInvokeDynamic:
			index := c.u2()
			c.u2() // two zero bytes
			instruction.Arg = int(index)
			instruction.Operand = fmt.Sprintf("#%d,  0", index)
			instruction.Comment, instruction.HasComment = constantComment(classFile, index)
		case operandMultiANewArray:
			index := c.u2()
			dimensions := c.u1()
			instruction.Arg, instruction.Arg2 = int(index), int(dimensions)
			instruction.Operand = fmt.Sprintf("#%d,  %d", index, dimensions)
			instruction.Comment, instruction.HasComment = constantComment(classFile, index)
		case operandTableSwitch:
			c.align4()
			defaultTarget := pc + int(c.i4())
			low := int(c.i4())
			high := int(c.i4())
			var keys []string
			var targets []int
			for key := low; key <= high && !c.overrun; key++ {
				keys = append(keys, strconv.Itoa(key))
				targets = append(targets, pc+int(c.i4()))
			}
			keys = append(keys, "default")
			targets = append(targets, defaultTarget)
			instruction.Operand = fmt.Sprintf("{ // %d to %d", low, high)
			instruction.ExtraLines = switchBody(keys, targets)
		case operandLookupSwitch:
			c.align4()
			defaultTarget := pc + int(c.i4())
			pairs := int(c.i4())
			var keys []string
			var targets []int
			for i := 0; i < pairs && !c.overrun; i++ {
				keys = append(keys, strconv.Itoa(int(c.i4())))
				targets = append(targets, pc+int(c.i4()))
			}
			keys = append(keys, "default")
			targets = append(targets, defaultTarget)
			instruction.Operand = fmt.Sprintf("{ // %d", pairs)
			instruction.ExtraLines = switchBody(keys, targets)
		}
		out = append(out, instruction)
	}
	if c.overrun {
		return nil, errors.New("truncated code attribute")
	}
	return out, nil
}

func instructionLine(instruction Instruction) string {
	prefix := fmt.Sprintf("%s%*d: ", strings.Repeat(" ", pcIndent), pcWidth, instruction.Pc)
	line := prefix + instruction.Mnemonic
	if instruction.Operand != "" {
		// `newarray` is the one mnemonic javap pads one column wider.
		width := mnemonicWidth
		if instruction.Mnemonic == "newarray" {
			width++
		}
		line = fmt.Sprintf("%s%-*s %s", prefix, width, instruction.Mnemonic, instruction.Operand)
	}
	if instruction.HasComment {
		line = fmt.Sprintf("%-*s// %s", commentColumn, line, instruction.Comment)
	}
	// Java's String.trim (which javap applies to each line) strips characters up
	// to U+0020 only, so a trailing NBSP inside a string constant survives.
	end := len(line)
	for end > 0 && line[end-1] <= 0x20 {
		end--
	}
	return line[:end]
}

// --- declarations -------------------------------------------------------------------

var descriptorPrimitives = map[byte]string{
	'B': "byte", 'C': "char", 'D': "double", 'F': "float",
	'I': "int", 'J': "long", 'S': "short", 'Z': "boolean", 'V': "void",
}

// DescriptorType renders a field/method descriptor type as source text; nested
// names keep their `$`.
func DescriptorType(descriptor string, at int) (string, int) {
	arrays := 0
	for at < len(descriptor) && descriptor[at] == '[' {
		arrays++
		at++
	}
	base := "java.lang.Object"
	if at < len(descriptor) && descriptor[at] == 'L' {
		end := strings.IndexByte(descriptor[at:], ';')
		stop := len(descriptor)
		next := len(descriptor)
		if end >= 0 {
			stop = at + end
			next = stop + 1
		}
		base = internalToJava(descriptor[at+1 : stop])
		at = next
	} else if at < len(descriptor) {
		if primitive, ok := descriptorPrimitives[descriptor[at]]; ok {
			base = primitive
		}
		at++
	}
	return base + strings.Repeat("[]", arrays), at
}

func methodDescriptorTypes(descriptor string) (params []string, returns string) {
	at := 1 // past '('
	for at < len(descriptor) && descriptor[at] != ')' {
		text, next := DescriptorType(descriptor, at)
		params = append(params, text)
		at = next
	}
	returns, _ = DescriptorType(descriptor, at+1)
	return params, returns
}

// signatureCursor reads a generic signature (JVMS 4.7.9.1). Deliberately
// separate from the one in classfile_reader.go: that one renders stub source,
// which flattens `$` to `.` and drops throws clauses; javap keeps both.
type signatureCursor struct {
	text string
	at   int
}

func (r *signatureCursor) peek() byte {
	if r.at >= len(r.text) {
		return 0
	}
	return r.text[r.at]
}

func (r *signatureCursor) take() byte {
	c := r.peek()
	r.at++
	return c
}

func (r *signatureCursor) typeParameters() string {
	if r.peek() != '<' {
		return ""
	}
	r.take()
	var params []string
	for r.peek() != '>' && r.at < len(r.text) {
		colon := strings.IndexByte(r.text[r.at:], ':')
		if colon < 0 {
			break // truncated signature
		}
		name := r.text[r.at : r.at+colon]
		r.at += colon
		var bounds []string
		for r.peek() == ':' {
			r.take()
			if r.peek() == ':' {
				continue // empty class bound (interface first)
			}
			bounds = append(bounds, r.referenceType())
		}
		var shown []string
		for _, bound := range bounds {
			if bound != "java.lang.Object" {
				shown = append(shown, bound)
			}
		}
		if len(shown) > 0 {
			params = append(params, name+" extends "+strings.Join(shown, " & "))
		} else {
			params = append(params, name)
		}
	}
	r.take() // '>'
	return "<" + strings.Join(params, ", ") + ">"
}

func (r *signatureCursor) javaType() string {
	if primitive, ok := descriptorPrimitives[r.peek()]; ok {
		r.take()
		return primitive
	}
	return r.referenceType()
}

func (r *signatureCursor) referenceType() string {
	switch r.peek() {
	case 'T':
		r.take()
		semi := strings.IndexByte(r.text[r.at:], ';')
		stop := len(r.text)
		next := len(r.text)
		if semi >= 0 {
			stop = r.at + semi
			next = stop + 1
		}
		name := r.text[r.at:stop]
		r.at = next
		return name
	case '[':
		r.take()
		return r.javaType() + "[]"
	}
	r.take() // 'L'
	var out strings.Builder
	for {
		c := r.take()
		if c == ';' || c == 0 {
			break
		}
		switch c {
		case '/':
			out.WriteByte('.')
		case '<':
			out.WriteString("<" + r.typeArguments() + ">")
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

func (r *signatureCursor) typeArguments() string {
	var args []string
	for r.peek() != '>' && r.at < len(r.text) {
		switch r.peek() {
		case '*':
			r.take()
			args = append(args, "?")
		case '+':
			r.take()
			args = append(args, "? extends "+r.referenceType())
		case '-':
			r.take()
			args = append(args, "? super "+r.referenceType())
		default:
			args = append(args, r.referenceType())
		}
	}
	r.take() // '>'
	return strings.Join(args, ", ")
}

func accessModifier(flags uint16) []string {
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

func fieldDeclaration(field Member, classFile *ClassFile) string {
	modifiers := accessModifier(field.Flags)
	if field.Flags&accStatic != 0 {
		modifiers = append(modifiers, "static")
	}
	if field.Flags&accFinal != 0 {
		modifiers = append(modifiers, "final")
	}
	if field.Flags&accVolatile != 0 {
		modifiers = append(modifiers, "volatile")
	}
	if field.Flags&accTransient != 0 {
		modifiers = append(modifiers, "transient")
	}
	fieldType := ""
	if signature := SignatureOf(field.Attributes, classFile.Pool); signature != "" {
		fieldType = (&signatureCursor{text: signature}).javaType()
	} else {
		fieldType, _ = DescriptorType(field.Descriptor, 0)
	}
	return strings.Join(append(modifiers, fieldType), " ") + " " + field.Name + ";"
}

func methodDeclaration(method Member, classFile *ClassFile) string {
	isInterface := classFile.Flags&accInterface != 0
	isStatic := method.Flags&accStatic != 0
	isAbstract := method.Flags&accAbstract != 0
	if method.Name == "<clinit>" {
		return "static {};"
	}

	modifiers := accessModifier(method.Flags)
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
	// A private interface method is not a default method.
	if isInterface && !isStatic && !isAbstract && method.Flags&accPrivate == 0 {
		modifiers = append(modifiers, "default")
	}

	typeParameters := ""
	var params []string
	var returns string
	var thrown []string
	for _, name := range ReadThrownExceptions(method, classFile.Pool) {
		thrown = append(thrown, internalToJava(name))
	}
	if signature := SignatureOf(method.Attributes, classFile.Pool); signature != "" {
		cursor := &signatureCursor{text: signature}
		typeParameters = cursor.typeParameters()
		cursor.take() // '('
		for cursor.peek() != ')' && cursor.at < len(signature) {
			params = append(params, cursor.javaType())
		}
		cursor.take() // ')'
		returns = cursor.javaType()
		var signatureThrows []string
		for cursor.peek() == '^' {
			cursor.take()
			signatureThrows = append(signatureThrows, cursor.referenceType())
		}
		if len(signatureThrows) > 0 {
			thrown = signatureThrows
		}
	} else {
		params, returns = methodDescriptorTypes(method.Descriptor)
	}

	if method.Flags&accVarargs != 0 && len(params) > 0 {
		if last := params[len(params)-1]; strings.HasSuffix(last, "[]") {
			params[len(params)-1] = strings.TrimSuffix(last, "[]") + "..."
		}
	}

	if typeParameters != "" {
		modifiers = append(modifiers, typeParameters)
	}
	head := strings.Join(modifiers, " ")
	if head != "" {
		head += " "
	}
	declared := fmt.Sprintf("%s %s(%s)", returns, method.Name, strings.Join(params, ", "))
	if method.Name == "<init>" {
		declared = fmt.Sprintf("%s(%s)", internalToJava(classFile.ThisClass), strings.Join(params, ", "))
	}
	throwsClause := ""
	if len(thrown) > 0 {
		throwsClause = " throws " + strings.Join(thrown, ", ")
	}
	return head + declared + throwsClause + ";"
}

func classDeclaration(classFile *ClassFile) string {
	isInterface := classFile.Flags&accInterface != 0
	superType := internalToJava(classFile.SuperClass)
	interfaces := make([]string, 0, len(classFile.Interfaces))
	for _, name := range classFile.Interfaces {
		interfaces = append(interfaces, internalToJava(name))
	}
	// javap renders the erased interface list without spaces after the commas,
	// but a generic one (from the Signature attribute) with them.
	interfaceSeparator := ","
	typeParameters := ""
	if signature := SignatureOf(classFile.Attributes, classFile.Pool); signature != "" {
		interfaceSeparator = ", "
		cursor := &signatureCursor{text: signature}
		typeParameters = cursor.typeParameters()
		superType = cursor.referenceType()
		interfaces = nil
		for cursor.at < len(signature) {
			interfaces = append(interfaces, cursor.referenceType())
		}
	}
	// Only ACC_PUBLIC is meaningful on a class; a nested type's real access lives
	// in the InnerClasses attribute, which javap ignores here too.
	var head []string
	if classFile.Flags&accPublic != 0 {
		head = append(head, "public")
	}
	if !isInterface && classFile.Flags&accFinal != 0 {
		head = append(head, "final")
	}
	if !isInterface && classFile.Flags&accAbstract != 0 {
		head = append(head, "abstract")
	}
	kind := "class"
	if isInterface {
		kind = "interface"
	}
	head = append(head, kind, internalToJava(classFile.ThisClass)+typeParameters)
	if base, _, _ := strings.Cut(superType, "<"); superType != "" && base != "java.lang.Object" {
		head = append(head, "extends", superType)
	}
	if len(interfaces) > 0 {
		keyword := "implements"
		if isInterface {
			keyword = "extends"
		}
		head = append(head, keyword, strings.Join(interfaces, interfaceSeparator))
	}
	return strings.Join(head, " ") + " {"
}

// --- rendering -----------------------------------------------------------------------

func exceptionTableLines(code *Code) []string {
	if len(code.Exceptions) == 0 {
		return nil
	}
	lines := []string{"      Exception table:", "         from    to  target type"}
	for _, entry := range code.Exceptions {
		kind := "any"
		if entry.CatchType != "" {
			kind = "Class " + entry.CatchType
		}
		lines = append(lines, fmt.Sprintf("%14d%6d%6d   %s", entry.StartPc, entry.EndPc, entry.HandlerPc, kind))
	}
	return lines
}

func memberBlock(classFile *ClassFile, method Member) ([]string, error) {
	lines := []string{"  " + methodDeclaration(method, classFile)}
	code, err := ReadCode(method, classFile.Pool)
	if err != nil {
		return nil, err
	}
	if code == nil {
		return lines, nil // abstract or native: no Code attribute
	}
	instructions, err := DecodeInstructions(classFile, code.Code)
	if err != nil {
		return nil, err
	}
	lines = append(lines, "    Code:")
	for _, instruction := range instructions {
		lines = append(lines, instructionLine(instruction))
		lines = append(lines, instruction.ExtraLines...)
	}
	return append(lines, exceptionTableLines(code)...), nil
}

// RenderClass renders one class in `javap -c -p` layout.
func RenderClass(classFile *ClassFile) (string, error) {
	var lines []string
	if source := SourceFileName(classFile); source != "" {
		// Not %q: javap prints the name as it stands, with no Go-style escaping.
		lines = append(lines, `Compiled from "`+source+`"`)
	}
	lines = append(lines, classDeclaration(classFile))
	// javap terminates every field with a blank line, but only separates methods
	// with one - so a field-only class keeps a blank line before the closing brace.
	for _, field := range classFile.Fields {
		lines = append(lines, "  "+fieldDeclaration(field, classFile), "")
	}
	for i, method := range classFile.Methods {
		if i > 0 {
			lines = append(lines, "")
		}
		block, err := memberBlock(classFile, method)
		if err != nil {
			return "", err
		}
		lines = append(lines, block...)
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n") + "\n", nil
}

// Disassemble disassembles one class file's bytes.
func Disassemble(b []byte) (string, error) {
	classFile, err := ReadClassFile(b)
	if err != nil {
		return "", err
	}
	// A module-info.class carries a Module attribute instead of members; rendering
	// it as a class would print a plausible-looking empty type (nikeee/cappu#43).
	if classFile.Flags&accModule != 0 {
		return "", errors.New("module descriptors are not supported yet")
	}
	return RenderClass(classFile)
}
