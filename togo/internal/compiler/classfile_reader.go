package compiler

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Classpath support: parse compiled .class files and regenerate a Java stub
// source (public API only, erased types), which feeds through the normal
// parse/bind pipeline exactly like the hand-written JDK stub. Loaded types
// resolve for compilation and the LSP, but carry no code. Port of
// src/compiler/classfileReader.ts.

const (
	accPublic     = 0x0001
	accProtected  = 0x0004
	accStatic     = 0x0008
	accFinal      = 0x0010
	accBridge     = 0x0040
	accVarargs    = 0x0080
	accInterface  = 0x0200
	accAbstract   = 0x0400
	accSynthetic  = 0x1000
	accAnnotation = 0x2000
	accEnum       = 0x4000
)

type memberInfo struct {
	flags      int
	name       string
	descriptor string
	signature  string // generic signature (JVMS 4.7.9), "" when not generic
}

type classInfo struct {
	flags      int
	name       string // binary name, e.g. "com/app/Foo"
	superName  string // "" when absent
	interfaces []string
	fields     []memberInfo
	methods    []memberInfo
	signature  string
}

var errClassFile = errors.New("class file error")

// parseClassFile reads just the structure a stub needs: constant pool (for
// names), the class header, and each member's name/descriptor/flags. Attribute
// bodies are skipped.
func parseClassFile(b []byte) (classInfo, error) {
	at := 0
	bad := func() (classInfo, error) { return classInfo{}, errClassFile }
	avail := func(n int) bool { return at+n <= len(b) }
	u1 := func() int {
		v := int(b[at])
		at++
		return v
	}
	u2 := func() int {
		v := int(binary.BigEndian.Uint16(b[at:]))
		at += 2
		return v
	}
	u4 := func() int {
		v := int(binary.BigEndian.Uint32(b[at:]))
		at += 4
		return v
	}

	if !avail(4) || u4() != 0xcafebabe {
		return bad()
	}
	at += 4 // minor + major version
	if !avail(2) {
		return bad()
	}

	poolCount := u2()
	utf8 := make([]string, poolCount)
	classNameIndex := make([]int, poolCount) // 0 = absent
	for i := 1; i < poolCount; i++ {
		if !avail(1) {
			return bad()
		}
		tag := u1()
		switch tag {
		case 1:
			if !avail(2) {
				return bad()
			}
			length := u2()
			if !avail(length) {
				return bad()
			}
			utf8[i] = string(b[at : at+length])
			at += length
		case 7:
			if !avail(2) {
				return bad()
			}
			classNameIndex[i] = u2()
		case 8, 16, 19, 20:
			at += 2
		case 15:
			at += 3
		case 3, 4, 9, 10, 11, 12, 17, 18:
			at += 4
		case 5, 6:
			at += 8
			i++ // longs/doubles take two pool slots
		default:
			return bad()
		}
	}
	className := func(index int) string {
		if index <= 0 || index >= poolCount {
			return ""
		}
		ni := classNameIndex[index]
		if ni <= 0 || ni >= poolCount {
			return ""
		}
		return utf8[ni]
	}
	utf8At := func(index int) string {
		if index <= 0 || index >= poolCount {
			return ""
		}
		return utf8[index]
	}

	if !avail(6) {
		return bad()
	}
	flags := u2()
	name := className(u2())
	if name == "" {
		return bad()
	}
	superIndex := u2()
	superName := ""
	if superIndex != 0 {
		superName = className(superIndex)
	}
	if !avail(2) {
		return bad()
	}
	interfaceCount := u2()
	var interfaces []string
	for i := 0; i < interfaceCount; i++ {
		if !avail(2) {
			return bad()
		}
		if n := className(u2()); n != "" {
			interfaces = append(interfaces, n)
		}
	}

	// readAttributes returns the Signature attribute's string if one is present;
	// every attribute body is skipped.
	readAttributes := func() (string, bool) {
		if !avail(2) {
			return "", false
		}
		attributeCount := u2()
		signature := ""
		for a := 0; a < attributeCount; a++ {
			if !avail(6) {
				return "", false
			}
			attrName := utf8At(u2())
			length := u4()
			if attrName == "Signature" && length == 2 && avail(2) {
				signature = utf8At(int(binary.BigEndian.Uint16(b[at:])))
			}
			at += length
			if at > len(b) {
				return "", false
			}
		}
		return signature, true
	}

	readMembers := func() ([]memberInfo, bool) {
		if !avail(2) {
			return nil, false
		}
		count := u2()
		members := make([]memberInfo, 0, count)
		for i := 0; i < count; i++ {
			if !avail(6) {
				return nil, false
			}
			memberFlags := u2()
			memberName := utf8At(u2())
			descriptor := utf8At(u2())
			signature, ok := readAttributes()
			if !ok {
				return nil, false
			}
			members = append(members, memberInfo{flags: memberFlags, name: memberName, descriptor: descriptor, signature: signature})
		}
		return members, true
	}

	fields, ok := readMembers()
	if !ok {
		return bad()
	}
	methods, ok := readMembers()
	if !ok {
		return bad()
	}
	signature, ok := readAttributes() // class-level attributes
	if !ok {
		return bad()
	}
	return classInfo{flags: flags, name: name, superName: superName, interfaces: interfaces, fields: fields, methods: methods, signature: signature}, nil
}

// --- descriptor -> source type --------------------------------------------------

var primitives = map[byte]string{
	'B': "byte", 'C': "char", 'D': "double", 'F': "float",
	'I': "int", 'J': "long", 'S': "short", 'Z': "boolean", 'V': "void",
}

func primitiveName(c byte) (string, bool) {
	v, ok := primitives[c]
	return v, ok
}

// charAt returns the byte at i, or 0 when out of range (mirrors TS's `?? ""`).
func charAt(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

func typeAt(descriptor string, at int) (text string, next int) {
	arrays := 0
	for charAt(descriptor, at) == '[' {
		arrays++
		at++
	}
	var base string
	if charAt(descriptor, at) == 'L' {
		// A truncated descriptor (no ';') consumes the rest: at must never move
		// backwards, or the caller's scan would not terminate.
		end := strings.IndexByte(descriptor[at:], ';')
		if end < 0 {
			base = strings.NewReplacer("/", ".", "$", ".").Replace(descriptor[at+1:])
			at = len(descriptor)
		} else {
			end += at
			base = strings.NewReplacer("/", ".", "$", ".").Replace(descriptor[at+1 : end])
			at = end + 1
		}
	} else {
		if p, ok := primitiveName(charAt(descriptor, at)); ok {
			base = p
		} else {
			base = "java.lang.Object"
		}
		at++
	}
	return base + strings.Repeat("[]", arrays), at
}

func methodTypes(descriptor string) (params []string, returns string) {
	at := 1 // past '('
	for charAt(descriptor, at) != ')' && at < len(descriptor) {
		text, next := typeAt(descriptor, at)
		params = append(params, text)
		at = next
	}
	returns, _ = typeAt(descriptor, at+1)
	return params, returns
}

// --- generic signature -> source (JVMS 4.7.9.1) -----------------------------------

type signatureReader struct {
	text string
	at   int
}

func (r *signatureReader) peek() byte { return charAt(r.text, r.at) }
func (r *signatureReader) take() byte {
	c := charAt(r.text, r.at)
	r.at++
	return c
}

// typeParameters: `<T:...:...U:...>` -> "<T extends X & Y, U>" or "".
func (r *signatureReader) typeParameters() string {
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
		colon += r.at
		name := r.text[r.at:colon]
		r.at = colon
		var bounds []string
		for r.peek() == ':' {
			r.take()
			if r.peek() == ':' {
				continue // empty class bound (interface first)
			}
			bounds = append(bounds, r.referenceType())
		}
		var real []string
		for _, b := range bounds {
			if b != "java.lang.Object" {
				real = append(real, b)
			}
		}
		if len(real) > 0 {
			params = append(params, name+" extends "+strings.Join(real, " & "))
		} else {
			params = append(params, name)
		}
	}
	r.take() // '>'
	return "<" + strings.Join(params, ", ") + ">"
}

// javaType: a primitive or a ReferenceTypeSignature.
func (r *signatureReader) javaType() string {
	if p, ok := primitiveName(r.peek()); ok {
		r.take()
		return p
	}
	return r.referenceType()
}

func (r *signatureReader) referenceType() string {
	c := r.peek()
	if c == 'T' {
		r.take()
		semi := strings.IndexByte(r.text[r.at:], ';')
		var name string
		if semi < 0 {
			name = r.text[r.at:]
			r.at = len(r.text)
		} else {
			semi += r.at
			name = r.text[r.at:semi]
			r.at = semi + 1
		}
		return name
	}
	if c == '[' {
		r.take()
		return r.javaType() + "[]"
	}
	// ClassTypeSignature: Lpkg/Name<args>.Inner<args>;
	r.take() // 'L'
	var out strings.Builder
	for {
		ch := r.take()
		if ch == ';' || ch == 0 {
			break // 0: ran off a truncated signature
		}
		switch ch {
		case '/', '$', '.':
			out.WriteByte('.')
		case '<':
			out.WriteString("<" + r.typeArguments() + ">")
		default:
			out.WriteByte(ch)
		}
	}
	return out.String()
}

func (r *signatureReader) typeArguments() string {
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

type classSignature struct {
	typeParameters string
	superType      string
	interfaces     []string
}

func parseClassSignature(signature string) classSignature {
	r := &signatureReader{text: signature}
	tp := r.typeParameters()
	super := r.referenceType()
	var interfaces []string
	for r.at < len(signature) {
		interfaces = append(interfaces, r.referenceType())
	}
	return classSignature{typeParameters: tp, superType: super, interfaces: interfaces}
}

type methodSignature struct {
	typeParameters string
	params         []string
	returns        string
}

func parseMethodSignature(signature string) methodSignature {
	r := &signatureReader{text: signature}
	tp := r.typeParameters()
	r.take() // '('
	var params []string
	for r.peek() != ')' {
		if r.at >= len(r.text) {
			break // truncated
		}
		params = append(params, r.javaType())
	}
	r.take() // ')'
	return methodSignature{typeParameters: tp, params: params, returns: r.javaType()}
}

// --- stub source generation ------------------------------------------------------

func defaultReturn(typ string) string {
	switch typ {
	case "void":
		return ""
	case "boolean":
		return " return false;"
	case "byte", "char", "short", "int", "long", "float", "double":
		return " return 0;"
	default:
		return " return null;"
	}
}

func visibleMember(m memberInfo) bool {
	if m.flags&(accPublic|accProtected) == 0 {
		return false
	}
	return m.flags&(accSynthetic|accBridge) == 0
}

var implicitSupers = map[string]bool{
	"java.lang.Object": true, "java.lang.Enum": true, "java.lang.Record": true,
}

// typeDeclLines renders the declaration lines of one type (and, recursively, its
// nested types). nestedOf yields the directly nested classes of a binary name; a
// nested declaration is rendered static (its real inner-ness lives in the
// InnerClasses attribute, which a resolution stub does not need).
func typeDeclLines(info classInfo, simpleName string, nestedOf func(string) []classInfo, indent string, nested bool) []string {
	isInterface := info.flags&accInterface != 0
	isEnum := info.flags&accEnum != 0
	var lines []string

	var classSig *classSignature
	if info.signature != "" {
		cs := parseClassSignature(info.signature)
		classSig = &cs
	}
	superSource := "java.lang.Object"
	if classSig != nil {
		superSource = classSig.superType
	} else if info.superName != "" {
		superSource = strings.ReplaceAll(info.superName, "/", ".")
	}
	superBase := superSource
	if i := strings.IndexByte(superBase, '<'); i >= 0 {
		superBase = superBase[:i]
	}
	var interfaceSources []string
	if classSig != nil {
		interfaceSources = classSig.interfaces
	} else {
		for _, i := range info.interfaces {
			interfaceSources = append(interfaceSources, strings.ReplaceAll(i, "/", "."))
		}
	}

	head := []string{"public"}
	if nested {
		head = append(head, "static")
	}
	if !isInterface && !isEnum && info.flags&accAbstract != 0 {
		head = append(head, "abstract")
	}
	if !isInterface && !isEnum && info.flags&accFinal != 0 {
		head = append(head, "final")
	}
	kind := "class"
	if isInterface {
		kind = "interface"
	} else if isEnum {
		kind = "enum"
	}
	typeParams := ""
	if classSig != nil {
		typeParams = classSig.typeParameters
	}
	head = append(head, kind, simpleName+typeParams)
	if !isInterface && !isEnum && !implicitSupers[superBase] {
		head = append(head, "extends", superSource)
	}
	var interfaceNames []string
	for _, i := range interfaceSources {
		interfaceNames = append(interfaceNames, strings.ReplaceAll(i, "$", "."))
	}
	if len(interfaceNames) > 0 {
		kw := "implements"
		if isInterface {
			kw = "extends"
		}
		head = append(head, kw, strings.Join(interfaceNames, ", "))
	}
	lines = append(lines, indent+strings.Join(head, " ")+" {")

	if isEnum {
		var constants []string
		for _, f := range info.fields {
			if f.flags&accEnum != 0 && f.descriptor == "L"+info.name+";" {
				constants = append(constants, f.name)
			}
		}
		lines = append(lines, indent+"  "+strings.Join(constants, ", ")+";")
	}

	for _, field := range info.fields {
		if !visibleMember(field) {
			continue
		}
		if isEnum && field.flags&accEnum != 0 {
			continue // already listed as constants
		}
		mods := []string{"public"}
		if field.flags&accProtected != 0 {
			mods[0] = "protected"
		}
		if field.flags&accStatic != 0 {
			mods = append(mods, "static")
		}
		if field.flags&accFinal != 0 {
			mods = append(mods, "final")
		}
		var fieldType string
		if field.signature != "" {
			fieldType = (&signatureReader{text: field.signature}).javaType()
		} else {
			fieldType, _ = typeAt(field.descriptor, 0)
		}
		lines = append(lines, indent+"  "+strings.Join(mods, " ")+" "+fieldType+" "+field.name+";")
	}

	for _, method := range info.methods {
		if !visibleMember(method) || method.name == "<clinit>" {
			continue
		}
		if isEnum && (method.name == "values" || method.name == "valueOf") {
			continue
		}
		var msig *methodSignature
		if method.signature != "" {
			ms := parseMethodSignature(method.signature)
			msig = &ms
		}
		var params []string
		var returns string
		if msig != nil {
			params, returns = msig.params, msig.returns
		} else {
			params, returns = methodTypes(method.descriptor)
		}
		typeParamsPrefix := ""
		if msig != nil && msig.typeParameters != "" {
			typeParamsPrefix = msig.typeParameters + " "
		}
		isVarargs := method.flags&accVarargs != 0
		var paramParts []string
		for i, p := range params {
			if isVarargs && i == len(params)-1 && strings.HasSuffix(p, "[]") {
				paramParts = append(paramParts, p[:len(p)-2]+"... p"+strconv.Itoa(i))
			} else {
				paramParts = append(paramParts, p+" p"+strconv.Itoa(i))
			}
		}
		paramList := strings.Join(paramParts, ", ")
		isAbstract := method.flags&accAbstract != 0
		isStatic := method.flags&accStatic != 0
		access := "public"
		if method.flags&accProtected != 0 {
			access = "protected"
		}
		if method.name == "<init>" {
			if isEnum {
				continue // enum constructors are not callable from outside
			}
			lines = append(lines, indent+"  "+access+" "+typeParamsPrefix+simpleName+"("+paramList+") {}")
			continue
		}
		var mods []string
		if isInterface {
			if isStatic {
				mods = []string{"static"}
			} else if !isAbstract {
				mods = []string{"default"}
			}
		} else {
			mods = []string{access}
			if isStatic {
				mods = append(mods, "static")
			}
			if isAbstract {
				mods = append(mods, "abstract")
			}
		}
		modPrefix := ""
		if len(mods) > 0 {
			modPrefix = strings.Join(mods, " ") + " "
		}
		signature := modPrefix + typeParamsPrefix + returns + " " + method.name + "(" + paramList + ")"
		if isAbstract {
			lines = append(lines, indent+"  "+signature+";")
		} else {
			lines = append(lines, indent+"  "+signature+" {"+defaultReturn(returns)+" }")
		}
	}

	for _, child := range nestedOf(info.name) {
		childSimple := child.name[strings.LastIndexByte(child.name, '$')+1:]
		lines = append(lines, "")
		lines = append(lines, typeDeclLines(child, childSimple, nestedOf, indent+"  ", true)...)
	}

	lines = append(lines, indent+"}")
	return lines
}

// isAnonymousOrLocal: a nested binary name segment starting with a digit is an
// anonymous or local class - never referencable from source, so never stubbed.
func isAnonymousOrLocal(binaryName string) bool {
	for i, segment := range strings.Split(binaryName, "$") {
		if i > 0 && len(segment) > 0 && segment[0] >= '0' && segment[0] <= '9' {
			return true
		}
	}
	return false
}

func stubbable(info classInfo) bool {
	return info.name != "module-info" && info.flags&accAnnotation == 0
}

type classStub struct {
	Name   string
	Source string
}

// buildStubs groups parsed classes into top-level stub sources, nesting
// Outer$Inner. A nested class whose outer type is missing from the classpath
// stays orphaned - it was unreachable from source without the outer type anyway.
func buildStubs(classes []classInfo) []classStub {
	byParent := map[string][]classInfo{}
	for _, c := range classes {
		if strings.Contains(c.name, "$") {
			parent := c.name[:strings.LastIndexByte(c.name, '$')]
			byParent[parent] = append(byParent[parent], c)
		}
	}
	nestedOf := func(binaryName string) []classInfo {
		var out []classInfo
		for _, c := range byParent[binaryName] {
			if stubbable(c) && !isAnonymousOrLocal(c.name) {
				out = append(out, c)
			}
		}
		return out
	}
	var stubs []classStub
	for _, info := range classes {
		if strings.Contains(info.name, "$") || !stubbable(info) {
			continue
		}
		slash := strings.LastIndexByte(info.name, '/')
		packageName := ""
		simpleName := info.name
		if slash >= 0 {
			packageName = strings.ReplaceAll(info.name[:slash], "/", ".")
			simpleName = info.name[slash+1:]
		}
		var lines []string
		if packageName != "" {
			lines = append(lines, "package "+packageName+";", "")
		}
		lines = append(lines, typeDeclLines(info, simpleName, nestedOf, "", false)...)
		lines = append(lines, "")
		stubs = append(stubs, classStub{Name: info.name, Source: strings.Join(lines, "\n")})
	}
	return stubs
}

// ClassDeclaresMain reports whether compiled class bytes declare
// `public static void main(String[])`.
func ClassDeclaresMain(b []byte) bool {
	info, err := parseClassFile(b)
	if err != nil {
		return false
	}
	for _, m := range info.methods {
		if m.name == "main" && m.descriptor == "([Ljava/lang/String;)V" &&
			m.flags&(accPublic|accStatic) == (accPublic|accStatic) {
			return true
		}
	}
	return false
}

// ClassFileToStub returns the Java stub source for a single parsed class, or
// ok=false when the class cannot be expressed standalone (nested classes,
// annotations, module-info) or the bytes are not a parseable class file.
func ClassFileToStub(b []byte) (classStub, bool) {
	info, err := parseClassFile(b)
	if err != nil {
		return classStub{}, false
	}
	if strings.Contains(info.name, "$") || !stubbable(info) {
		return classStub{}, false
	}
	stubs := buildStubs([]classInfo{info})
	if len(stubs) == 0 {
		return classStub{}, false
	}
	return stubs[0], true
}

// LoadClassPath scans classpath entries - directories (recursively) or .jar
// files - for .class files and registers each as a stub source under
// classpath:///<binary-name>.java. Returns the number of types loaded;
// unreadable or inexpressible classes are skipped.
func LoadClassPath(program *Program, entries []string) int {
	var collected []classInfo
	addClassBytes := func(b []byte) {
		if info, err := parseClassFile(b); err == nil {
			collected = append(collected, info)
		}
	}
	visitJar := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		for _, entry := range ReadZipEntries(data) {
			if !strings.HasSuffix(entry.Name, ".class") || strings.HasPrefix(entry.Name, "META-INF/") {
				continue
			}
			addClassBytes(entry.Read())
		}
	}
	visitDirectory := func(dir string) {
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			switch {
			case strings.HasSuffix(path, ".class"):
				if b, e := os.ReadFile(path); e == nil {
					addClassBytes(b)
				}
			case strings.HasSuffix(path, ".jar"):
				visitJar(path)
			}
			return nil
		})
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry, ".jar") {
			visitJar(entry)
		} else {
			visitDirectory(entry)
		}
	}
	stubs := buildStubs(collected)
	for _, stub := range stubs {
		program.AddProjectFile(URI("classpath:///"+stub.Name+".java"), stub.Source)
	}
	return len(stubs)
}
