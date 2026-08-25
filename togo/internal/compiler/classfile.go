package compiler

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// A complete class-file reader (JVMS chapter 4): every constant-pool tag is
// retained and attribute bodies are kept as raw bytes, decoded on demand. This
// is the input side of `cappu decompile` (nikeee/cappu#43).
//
// Distinct from classfile_reader.go, which parses the same format down to the
// bare minimum a resolution stub needs (Utf8 + Class entries, every attribute
// body skipped) and is load-bearing for the LSP classpath.
//
// Port of src/compiler/classfile.ts.

// ErrNotClassFile is returned for input that is not a class file at all.
var ErrNotClassFile = errors.New("not a class file")

// ConstantTag names a constant-pool entry kind.
type ConstantTag string

const (
	TagUtf8               ConstantTag = "utf8"
	TagInt                ConstantTag = "int"
	TagFloat              ConstantTag = "float"
	TagLong               ConstantTag = "long"
	TagDouble             ConstantTag = "double"
	TagClass              ConstantTag = "class"
	TagString             ConstantTag = "string"
	TagFieldref           ConstantTag = "fieldref"
	TagMethodref          ConstantTag = "methodref"
	TagInterfaceMethodref ConstantTag = "interfaceMethodref"
	TagNameAndType        ConstantTag = "nameAndType"
	TagMethodHandle       ConstantTag = "methodHandle"
	TagMethodType         ConstantTag = "methodType"
	TagDynamic            ConstantTag = "dynamic"
	TagInvokeDynamic      ConstantTag = "invokeDynamic"
	TagModule             ConstantTag = "module"
	TagPackage            ConstantTag = "package"
)

// Constant is one constant-pool entry; only the fields its tag defines are set.
type Constant struct {
	Tag ConstantTag

	Text        string  // utf8
	Int         int32   // int
	Long        int64   // long
	Float       float32 // float
	Double      float64 // double
	Index       uint16  // class/string/methodType/module/package: name or value index
	ClassIndex  uint16  // fieldref/methodref/interfaceMethodref
	NameAndType uint16  // fieldref/methodref/interfaceMethodref/dynamic/invokeDynamic
	NameIndex   uint16  // nameAndType
	DescIndex   uint16  // nameAndType
	RefKind     uint8   // methodHandle
	RefIndex    uint16  // methodHandle
	Bootstrap   uint16  // dynamic/invokeDynamic
}

// Attribute is one attribute, with its body left undecoded.
type Attribute struct {
	Name  string
	Bytes []byte
}

// Member is one field or method.
type Member struct {
	Flags      uint16
	Name       string
	Descriptor string
	Attributes []Attribute
}

// ClassFile is a parsed class file.
type ClassFile struct {
	Minor uint16
	Major uint16
	// Pool is 1-based; index 0 and the second slot of long/double entries are nil.
	Pool       []*Constant
	Flags      uint16
	ThisClass  string
	SuperClass string
	Interfaces []string
	Fields     []Member
	Methods    []Member
	Attributes []Attribute
}

// ExceptionEntry is one row of a Code attribute's exception table.
type ExceptionEntry struct {
	StartPc   uint16
	EndPc     uint16
	HandlerPc uint16
	// CatchType is the caught class' internal name, empty for a catch-all ("any").
	CatchType string
}

// Code is a decoded Code attribute (JVMS 4.7.3).
type Code struct {
	MaxStack   uint16
	MaxLocals  uint16
	Code       []byte
	Exceptions []ExceptionEntry
	Attributes []Attribute
}

// BootstrapMethod is one row of the BootstrapMethods attribute (JVMS 4.7.23).
type BootstrapMethod struct {
	// HandleIndex is the CONSTANT_MethodHandle index of the bootstrap method.
	HandleIndex     uint16
	ArgumentIndexes []uint16
}

// cursor reads big-endian class-file bytes.
type cursor struct {
	b  []byte
	at int
}

func (c *cursor) require(n int) error {
	if c.at+n > len(c.b) {
		return errors.New("truncated class file")
	}
	return nil
}

func (c *cursor) u1() (uint8, error) {
	if err := c.require(1); err != nil {
		return 0, err
	}
	v := c.b[c.at]
	c.at++
	return v, nil
}

func (c *cursor) u2() (uint16, error) {
	if err := c.require(2); err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint16(c.b[c.at:])
	c.at += 2
	return v, nil
}

func (c *cursor) u4() (uint32, error) {
	if err := c.require(4); err != nil {
		return 0, err
	}
	v := binary.BigEndian.Uint32(c.b[c.at:])
	c.at += 4
	return v, nil
}

func (c *cursor) slice(n int) ([]byte, error) {
	if n < 0 {
		return nil, errors.New("truncated class file")
	}
	if err := c.require(n); err != nil {
		return nil, err
	}
	out := c.b[c.at : c.at+n]
	c.at += n
	return out, nil
}

// decodeModifiedUTF8 decodes modified UTF-8 (JVMS 4.4.7): like UTF-8, except
// U+0000 is encoded as the two bytes c0 80 and supplementary characters appear
// as a surrogate pair of three-byte sequences. Decoded through UTF-16 code
// units, so both fall out.
func decodeModifiedUTF8(b []byte) string {
	units := make([]uint16, 0, len(b))
	for i := 0; i < len(b); {
		switch {
		case b[i] < 0x80:
			units = append(units, uint16(b[i]))
			i++
		case b[i]&0xe0 == 0xc0:
			if i+1 >= len(b) {
				return encodeUTF16(units)
			}
			units = append(units, uint16(b[i]&0x1f)<<6|uint16(b[i+1]&0x3f))
			i += 2
		default:
			if i+2 >= len(b) {
				return encodeUTF16(units)
			}
			units = append(units, uint16(b[i]&0x0f)<<12|uint16(b[i+1]&0x3f)<<6|uint16(b[i+2]&0x3f))
			i += 3
		}
	}
	return encodeUTF16(units)
}

func readConstantPool(c *cursor) ([]*Constant, error) {
	count, err := c.u2()
	if err != nil {
		return nil, err
	}
	pool := make([]*Constant, count)
	for i := 1; i < int(count); i++ {
		tag, err := c.u1()
		if err != nil {
			return nil, err
		}
		entry := &Constant{}
		switch tag {
		case 1:
			length, err := c.u2()
			if err != nil {
				return nil, err
			}
			body, err := c.slice(int(length))
			if err != nil {
				return nil, err
			}
			entry.Tag, entry.Text = TagUtf8, decodeModifiedUTF8(body)
		case 3:
			v, err := c.u4()
			if err != nil {
				return nil, err
			}
			entry.Tag, entry.Int = TagInt, int32(v)
		case 4:
			v, err := c.u4()
			if err != nil {
				return nil, err
			}
			entry.Tag, entry.Float = TagFloat, math.Float32frombits(v)
		case 5, 6:
			hi, err := c.u4()
			if err != nil {
				return nil, err
			}
			lo, err := c.u4()
			if err != nil {
				return nil, err
			}
			bits := uint64(hi)<<32 | uint64(lo)
			if tag == 5 {
				entry.Tag, entry.Long = TagLong, int64(bits)
			} else {
				entry.Tag, entry.Double = TagDouble, math.Float64frombits(bits)
			}
			pool[i] = entry
			i++ // longs and doubles take two pool slots
			continue
		case 7, 8, 16, 19, 20:
			index, err := c.u2()
			if err != nil {
				return nil, err
			}
			switch tag {
			case 7:
				entry.Tag = TagClass
			case 8:
				entry.Tag = TagString
			case 16:
				entry.Tag = TagMethodType
			case 19:
				entry.Tag = TagModule
			default:
				entry.Tag = TagPackage
			}
			entry.Index = index
		case 9, 10, 11:
			classIndex, err := c.u2()
			if err != nil {
				return nil, err
			}
			nameAndType, err := c.u2()
			if err != nil {
				return nil, err
			}
			switch tag {
			case 9:
				entry.Tag = TagFieldref
			case 10:
				entry.Tag = TagMethodref
			default:
				entry.Tag = TagInterfaceMethodref
			}
			entry.ClassIndex, entry.NameAndType = classIndex, nameAndType
		case 12:
			nameIndex, err := c.u2()
			if err != nil {
				return nil, err
			}
			descIndex, err := c.u2()
			if err != nil {
				return nil, err
			}
			entry.Tag, entry.NameIndex, entry.DescIndex = TagNameAndType, nameIndex, descIndex
		case 15:
			kind, err := c.u1()
			if err != nil {
				return nil, err
			}
			refIndex, err := c.u2()
			if err != nil {
				return nil, err
			}
			entry.Tag, entry.RefKind, entry.RefIndex = TagMethodHandle, kind, refIndex
		case 17, 18:
			bootstrap, err := c.u2()
			if err != nil {
				return nil, err
			}
			nameAndType, err := c.u2()
			if err != nil {
				return nil, err
			}
			if tag == 17 {
				entry.Tag = TagDynamic
			} else {
				entry.Tag = TagInvokeDynamic
			}
			entry.Bootstrap, entry.NameAndType = bootstrap, nameAndType
		default:
			return nil, fmt.Errorf("unknown constant pool tag %d", tag)
		}
		pool[i] = entry
	}
	return pool, nil
}

func readAttributeTable(c *cursor, pool []*Constant) ([]Attribute, error) {
	count, err := c.u2()
	if err != nil {
		return nil, err
	}
	attributes := make([]Attribute, 0, count)
	for i := 0; i < int(count); i++ {
		nameIndex, err := c.u2()
		if err != nil {
			return nil, err
		}
		length, err := c.u4()
		if err != nil {
			return nil, err
		}
		body, err := c.slice(int(length))
		if err != nil {
			return nil, err
		}
		attributes = append(attributes, Attribute{Name: PoolUtf8(pool, nameIndex), Bytes: body})
	}
	return attributes, nil
}

func readMemberTable(c *cursor, pool []*Constant) ([]Member, error) {
	count, err := c.u2()
	if err != nil {
		return nil, err
	}
	members := make([]Member, 0, count)
	for i := 0; i < int(count); i++ {
		flags, err := c.u2()
		if err != nil {
			return nil, err
		}
		nameIndex, err := c.u2()
		if err != nil {
			return nil, err
		}
		descIndex, err := c.u2()
		if err != nil {
			return nil, err
		}
		attributes, err := readAttributeTable(c, pool)
		if err != nil {
			return nil, err
		}
		members = append(members, Member{
			Flags:      flags,
			Name:       PoolUtf8(pool, nameIndex),
			Descriptor: PoolUtf8(pool, descIndex),
			Attributes: attributes,
		})
	}
	return members, nil
}

// ReadClassFile parses class file bytes.
func ReadClassFile(b []byte) (*ClassFile, error) {
	c := &cursor{b: b}
	magic, err := c.u4()
	if err != nil {
		return nil, err // a file shorter than the magic is truncated, not foreign
	}
	if magic != 0xcafebabe {
		return nil, ErrNotClassFile
	}
	minor, err := c.u2()
	if err != nil {
		return nil, err
	}
	major, err := c.u2()
	if err != nil {
		return nil, err
	}
	pool, err := readConstantPool(c)
	if err != nil {
		return nil, err
	}
	flags, err := c.u2()
	if err != nil {
		return nil, err
	}
	thisIndex, err := c.u2()
	if err != nil {
		return nil, err
	}
	thisClass := PoolClassName(pool, thisIndex)
	if thisClass == "" {
		return nil, errors.New("missing this_class")
	}
	superIndex, err := c.u2()
	if err != nil {
		return nil, err
	}
	superClass := ""
	if superIndex != 0 {
		superClass = PoolClassName(pool, superIndex)
	}
	interfaceCount, err := c.u2()
	if err != nil {
		return nil, err
	}
	var interfaces []string
	for i := 0; i < int(interfaceCount); i++ {
		index, err := c.u2()
		if err != nil {
			return nil, err
		}
		if name := PoolClassName(pool, index); name != "" {
			interfaces = append(interfaces, name)
		}
	}
	fields, err := readMemberTable(c, pool)
	if err != nil {
		return nil, err
	}
	methods, err := readMemberTable(c, pool)
	if err != nil {
		return nil, err
	}
	attributes, err := readAttributeTable(c, pool)
	if err != nil {
		return nil, err
	}
	return &ClassFile{
		Minor:      minor,
		Major:      major,
		Pool:       pool,
		Flags:      flags,
		ThisClass:  thisClass,
		SuperClass: superClass,
		Interfaces: interfaces,
		Fields:     fields,
		Methods:    methods,
		Attributes: attributes,
	}, nil
}

// --- constant pool accessors -----------------------------------------------------

// PoolAt returns the constant at an index, or nil.
func PoolAt(pool []*Constant, index uint16) *Constant {
	if int(index) >= len(pool) {
		return nil
	}
	return pool[index]
}

// PoolUtf8 returns the string behind a CONSTANT_Utf8 index, or "".
func PoolUtf8(pool []*Constant, index uint16) string {
	if entry := PoolAt(pool, index); entry != nil && entry.Tag == TagUtf8 {
		return entry.Text
	}
	return ""
}

// PoolClassName returns the internal name (`java/lang/String`) behind a
// CONSTANT_Class index, or "".
func PoolClassName(pool []*Constant, index uint16) string {
	if entry := PoolAt(pool, index); entry != nil && entry.Tag == TagClass {
		return PoolUtf8(pool, entry.Index)
	}
	return ""
}

// MemberRef is a resolved Fieldref/Methodref/InterfaceMethodref.
type MemberRef struct {
	Owner      string
	Name       string
	Descriptor string
}

// PoolMemberRef resolves a Fieldref/Methodref/InterfaceMethodref index.
func PoolMemberRef(pool []*Constant, index uint16) (MemberRef, bool) {
	entry := PoolAt(pool, index)
	if entry == nil {
		return MemberRef{}, false
	}
	if entry.Tag != TagFieldref && entry.Tag != TagMethodref && entry.Tag != TagInterfaceMethodref {
		return MemberRef{}, false
	}
	name, descriptor, ok := PoolNameAndType(pool, entry.NameAndType)
	if !ok {
		return MemberRef{}, false
	}
	return MemberRef{Owner: PoolClassName(pool, entry.ClassIndex), Name: name, Descriptor: descriptor}, true
}

// PoolNameAndType resolves a CONSTANT_NameAndType index.
func PoolNameAndType(pool []*Constant, index uint16) (name, descriptor string, ok bool) {
	entry := PoolAt(pool, index)
	if entry == nil || entry.Tag != TagNameAndType {
		return "", "", false
	}
	return PoolUtf8(pool, entry.NameIndex), PoolUtf8(pool, entry.DescIndex), true
}

// --- attributes -------------------------------------------------------------------

// FindAttribute returns the named attribute, if present.
func FindAttribute(attributes []Attribute, name string) (Attribute, bool) {
	for _, a := range attributes {
		if a.Name == name {
			return a, true
		}
	}
	return Attribute{}, false
}

// InnerClassFlags reports the access flags of every nested class this file
// names, from its InnerClasses attribute (JVMS 4.7.6), keyed by binary name. It
// is what says whether `Outer$Inner` is `static`: an inner class's constructor
// takes the enclosing instance, which source cannot pass.
func InnerClassFlags(classFile *ClassFile) map[string]uint16 {
	flags := map[string]uint16{}
	attribute, ok := FindAttribute(classFile.Attributes, "InnerClasses")
	if !ok {
		return flags
	}
	c := &cursor{b: attribute.Bytes}
	count, err := c.u2()
	if err != nil {
		return flags
	}
	for i := 0; i < int(count); i++ {
		index, err := c.u2()
		if err != nil {
			return flags
		}
		if _, err := c.u2(); err != nil { // the enclosing class, which the name carries
			return flags
		}
		if _, err := c.u2(); err != nil { // the simple name, empty when anonymous
			return flags
		}
		access, err := c.u2()
		if err != nil {
			return flags
		}
		if inner := PoolClassName(classFile.Pool, index); inner != "" {
			flags[inner] = access
		}
	}
	return flags
}

// ReadCode decodes a method's Code attribute (JVMS 4.7.3), when it has one.
func ReadCode(method Member, pool []*Constant) (*Code, error) {
	attribute, ok := FindAttribute(method.Attributes, "Code")
	if !ok {
		return nil, nil
	}
	c := &cursor{b: attribute.Bytes}
	maxStack, err := c.u2()
	if err != nil {
		return nil, err
	}
	maxLocals, err := c.u2()
	if err != nil {
		return nil, err
	}
	length, err := c.u4()
	if err != nil {
		return nil, err
	}
	code, err := c.slice(int(length))
	if err != nil {
		return nil, err
	}
	exceptionCount, err := c.u2()
	if err != nil {
		return nil, err
	}
	var exceptions []ExceptionEntry
	for i := 0; i < int(exceptionCount); i++ {
		startPc, err := c.u2()
		if err != nil {
			return nil, err
		}
		endPc, err := c.u2()
		if err != nil {
			return nil, err
		}
		handlerPc, err := c.u2()
		if err != nil {
			return nil, err
		}
		catchIndex, err := c.u2()
		if err != nil {
			return nil, err
		}
		catchType := ""
		if catchIndex != 0 {
			catchType = PoolClassName(pool, catchIndex)
		}
		exceptions = append(exceptions, ExceptionEntry{
			StartPc: startPc, EndPc: endPc, HandlerPc: handlerPc, CatchType: catchType,
		})
	}
	attributes, err := readAttributeTable(c, pool)
	if err != nil {
		return nil, err
	}
	return &Code{
		MaxStack:   maxStack,
		MaxLocals:  maxLocals,
		Code:       code,
		Exceptions: exceptions,
		Attributes: attributes,
	}, nil
}

// ReadThrownExceptions returns the `throws` clause (JVMS 4.7.5) as internal names.
func ReadThrownExceptions(member Member, pool []*Constant) []string {
	attribute, ok := FindAttribute(member.Attributes, "Exceptions")
	if !ok {
		return nil
	}
	c := &cursor{b: attribute.Bytes}
	count, err := c.u2()
	if err != nil {
		return nil
	}
	var names []string
	for i := 0; i < int(count); i++ {
		index, err := c.u2()
		if err != nil {
			return names
		}
		if name := PoolClassName(pool, index); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// ReadBootstrapMethods returns the BootstrapMethods table, the target of every
// invokedynamic in the class.
func ReadBootstrapMethods(classFile *ClassFile) []BootstrapMethod {
	attribute, ok := FindAttribute(classFile.Attributes, "BootstrapMethods")
	if !ok {
		return nil
	}
	c := &cursor{b: attribute.Bytes}
	count, err := c.u2()
	if err != nil {
		return nil
	}
	var methods []BootstrapMethod
	for i := 0; i < int(count); i++ {
		handleIndex, err := c.u2()
		if err != nil {
			return methods
		}
		argumentCount, err := c.u2()
		if err != nil {
			return methods
		}
		var arguments []uint16
		for a := 0; a < int(argumentCount); a++ {
			argument, err := c.u2()
			if err != nil {
				return methods
			}
			arguments = append(arguments, argument)
		}
		methods = append(methods, BootstrapMethod{HandleIndex: handleIndex, ArgumentIndexes: arguments})
	}
	return methods
}

// SourceFileName returns the source file from the SourceFile attribute (JVMS 4.7.10).
func SourceFileName(classFile *ClassFile) string {
	attribute, ok := FindAttribute(classFile.Attributes, "SourceFile")
	if !ok || len(attribute.Bytes) < 2 {
		return ""
	}
	return PoolUtf8(classFile.Pool, binary.BigEndian.Uint16(attribute.Bytes))
}

// SignatureOf returns the generic signature (JVMS 4.7.9) of a class or member.
func SignatureOf(attributes []Attribute, pool []*Constant) string {
	attribute, ok := FindAttribute(attributes, "Signature")
	if !ok || len(attribute.Bytes) < 2 {
		return ""
	}
	return PoolUtf8(pool, binary.BigEndian.Uint16(attribute.Bytes))
}

// encodeUTF16 turns UTF-16 code units into a string. A surrogate pair becomes
// the supplementary character it stands for; an UNPAIRED surrogate is written
// as its own three-byte (WTF-8) sequence, because Go's rune-to-string
// conversion would silently replace it with U+FFFD and the caller needs to see
// that the class file held one (disasm.go renders it as javap's "?").
func encodeUTF16(units []uint16) string {
	out := make([]byte, 0, len(units)+len(units)/2)
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xd800 && u <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
			out = utf8.AppendRune(out, ((rune(u)-0xd800)<<10|(rune(units[i+1])-0xdc00))+0x10000)
			i++
			continue
		}
		if u >= 0xd800 && u <= 0xdfff {
			out = append(out, byte(0xe0|u>>12), byte(0x80|(u>>6)&0x3f), byte(0x80|u&0x3f))
			continue
		}
		out = utf8.AppendRune(out, rune(u))
	}
	return string(out)
}

// internalToJava turns an internal class name into its external form.
func internalToJava(name string) string {
	return strings.ReplaceAll(name, "/", ".")
}
