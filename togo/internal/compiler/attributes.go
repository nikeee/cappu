package compiler

import "strings"

// fieldInfo is a resolved field/enum-constant reference: where it lives and its descriptor.
type fieldInfo struct {
	owner      internalName
	name       string
	descriptor descriptor
	isStatic   bool
}

// localSlotInfo is a local variable / parameter slot and its descriptor
// (long/double take two slots but one entry).
type localSlotInfo struct {
	slot       slot
	descriptor descriptor
	// Debug info (LocalVariableTable), only under emitDebugInfo.
	name        string
	lvtStart    pc
	hasLvtStart bool
}

// exceptionTableEntry is one Code-attribute exception_table entry (JVMS 4.7.3).
// catchType 0 is a catch-all (used for finally).
type exceptionTableEntry struct {
	start, end, handler pc
	catchType           cpIndex // class cp entry of the caught type, or 0 for catch-all
}

// finallyAction is cleanup an abrupt exit must run on the way out. kind is
// "block" (a user finally), "resource" (try-with-resources close()), or "monitor".
type finallyAction struct {
	kind          string
	block         *Node
	slot          int
	ownerInternal internalName
	isInterface   bool
	guarded       bool
}

// lambdaImpl is a synthetic method holding a lambda body.
type lambdaImpl struct {
	name             string
	params           []paramSym
	returnDescriptor descriptor
	body             *Node
	isInstance       bool
}

// enumConstantClinit describes how to construct one enum constant.
type enumConstantClinit struct {
	name           string
	ordinal        int
	ctorDescriptor methodDescriptor // (Ljava/lang/String;I<userparams>)V
	userParamDescs []descriptor
	args           []*Node
}

// enumClinit is the data the enum <clinit> needs.
type enumClinit struct {
	enumInternal internalName
	selfDesc     descriptor // L<enum>;
	arrayDesc    descriptor // [L<enum>;
	valuesField  string     // synthetic $VALUES field name
	constants    []enumConstantClinit
}

// --- class attributes + nest/inner-class computation ------------------------

func writeSignatureAttribute(info *byteBuffer, cp *constantPool, signature jvmSignature) {
	index := cp.utf8(string(signature))
	info.u2(int(cp.utf8("Signature")))
	info.u4(2)
	info.u2(int(index))
}

// innerClassMap is an insertion-ordered map of inner-class records; the order is
// significant (innerClassOrder iterates members in declaration order).
type innerClassMap struct {
	keys   []internalName
	values map[internalName]innerClassRecord
}

func newInnerClassMap() *innerClassMap {
	return &innerClassMap{values: map[internalName]innerClassRecord{}}
}

func (m *innerClassMap) set(k internalName, v innerClassRecord) {
	if _, ok := m.values[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.values[k] = v
}

func (m *innerClassMap) get(k internalName) (innerClassRecord, bool) {
	v, ok := m.values[k]
	return v, ok
}

func (m *innerClassMap) has(k internalName) bool {
	_, ok := m.values[k]
	return ok
}

func (m *innerClassMap) len() int { return len(m.keys) }

func cutAtDollar(name internalName) internalName {
	if i := strings.IndexByte(string(name), '$'); i >= 0 {
		return name[:i]
	}
	return name
}

// classAttributes is the result of buildClassAttributes.
type classAttributes struct {
	buffer *byteBuffer
	count  int
}

// buildClassAttributes builds the class-level attributes shared by classes and
// enums: Signature, SourceFile, BootstrapMethods, NestHost/NestMembers and
// InnerClasses. Must run before the constant pool is written so the attribute
// name Utf8s are interned.
func buildClassAttributes(cp *constantPool, sourceName string, name internalName, nestMembers map[string][]internalName, signature jvmSignature, hasSignature bool, innerClasses *innerClassMap) classAttributes {
	buffer := &byteBuffer{}
	count := 0
	refCountBeforeAttrs := len(cp.referencedClasses)
	if hasSignature {
		writeSignatureAttribute(buffer, cp, signature)
		count++
	}
	if sourceName != "" {
		buffer.u2(int(cp.utf8("SourceFile")))
		buffer.u4(2)
		buffer.u2(int(cp.utf8(sourceName)))
		count++
	}
	if cp.bootstrapMethodCount() > 0 {
		buffer.u2(int(cp.utf8("BootstrapMethods")))
		body := cp.bootstrapMethodsBody()
		buffer.u4(body.length())
		buffer.appendBuf(body)
		count++
	}
	if name != "" && nestMembers != nil {
		host := cutAtDollar(name)
		if name == host {
			var members []internalName
			for _, n := range nestMembers[string(host)] {
				if n != host {
					members = append(members, n)
				}
			}
			if len(members) > 0 {
				buffer.u2(int(cp.utf8("NestMembers")))
				buffer.u4(2 + 2*len(members))
				buffer.u2(len(members))
				for _, mm := range members {
					buffer.u2(int(cp.classInfo(string(mm))))
				}
				count++
			}
		} else {
			buffer.u2(int(cp.utf8("NestHost")))
			buffer.u4(2)
			buffer.u2(int(cp.classInfo(string(host))))
			count++
		}
	}
	if innerClasses != nil && innerClasses.len() > 0 {
		order := innerClassOrder(name, cp.referencedClasses, refCountBeforeAttrs, innerClasses, cp.bootstrapMethodCount() > 0)
		if len(order) > 0 {
			buffer.u2(int(cp.utf8("InnerClasses")))
			buffer.u4(2 + 8*len(order))
			buffer.u2(len(order))
			for _, n := range order {
				record, _ := innerClasses.get(n)
				buffer.u2(int(cp.classInfo(string(n))))
				if record.outer != "" {
					buffer.u2(int(cp.classInfo(string(record.outer))))
				} else {
					buffer.u2(0)
				}
				if record.simpleName != "" {
					buffer.u2(int(cp.utf8(record.simpleName)))
				} else {
					buffer.u2(0)
				}
				buffer.u2(record.flags)
			}
			count++
		}
	}
	return classAttributes{buffer: buffer, count: count}
}

// computeNestMembers is the nest grouping of a source file: host binary name ->
// every member of that nest (including the host).
func computeNestMembers(sourceFile *Node, program *Program) map[string][]internalName {
	program.GetGlobalIndex()
	byHost := map[string][]internalName{}
	add := func(n internalName) {
		host := string(cutAtDollar(n))
		byHost[host] = append(byHost[host], n)
	}
	var visit func(node *Node)
	visit = func(node *Node) {
		switch {
		case node.Kind == ClassDeclaration:
			d := node.AsClassDeclaration()
			if node.Symbol != nil {
				add(binaryName(node.Symbol))
			} else if d.Name != nil {
				add(internalName(d.Name.AsIdentifier().Text))
			}
		case node.Kind == InterfaceDeclaration || node.Kind == EnumDeclaration || node.Kind == RecordDeclaration:
			if node.Symbol != nil {
				add(binaryName(node.Symbol))
			}
		case node.Kind == ObjectCreationExpression && node.AsObjectCreationExpression().ClassBody != nil && anonymousTarget(node, program) != nil:
			add(anonymousClassName(node, program))
		}
		node.ForEachChild(func(c *Node) bool {
			visit(c)
			return false
		})
	}
	visit(sourceFile)
	return byHost
}

// computeInnerClassInfo returns every nested class declared in the file, keyed by
// binary name - the data an InnerClasses entry needs (JVMS 4.7.6).
func computeInnerClassInfo(sourceFile *Node, program *Program) *innerClassMap {
	program.GetGlobalIndex()
	info := newInnerClassMap()
	var visit func(node *Node)
	visit = func(node *Node) {
		switch {
		case isTypeDeclarationKind(node.Kind) && node.Symbol != nil:
			name := binaryName(node.Symbol)
			if strings.Contains(string(name), "$") {
				memberOfType := node.Parent != nil && isTypeDeclarationKind(node.Parent.Kind)
				outer := internalName("")
				if memberOfType {
					outer = binaryName(node.Parent.Symbol)
				}
				simpleName := ""
				if nm := nodeName(node); nm != nil {
					simpleName = nm.AsIdentifier().Text
				}
				info.set(name, innerClassRecord{outer: outer, simpleName: simpleName, flags: innerClassFlags(node)})
			}
		case node.Kind == ObjectCreationExpression && node.AsObjectCreationExpression().ClassBody != nil && anonymousTarget(node, program) != nil:
			info.set(anonymousClassName(node, program), innerClassRecord{flags: 0})
		}
		node.ForEachChild(func(c *Node) bool {
			visit(c)
			return false
		})
	}
	visit(sourceFile)
	return info
}

// innerClassOrder returns the order javac writes InnerClasses entries (JVMS 4.7.6).
func innerClassOrder(thisName internalName, referencedClasses []string, refCountBeforeAttrs int, innerClasses *innerClassMap, hasBootstrap bool) []internalName {
	if hasBootstrap && !innerClasses.has(lookupName) {
		innerClasses.set(lookupName, innerClassRecord{
			outer:      "java/lang/invoke/MethodHandles",
			simpleName: "Lookup",
			flags:      accPublic | accStatic | accFinal,
		})
	}
	var result []internalName
	seen := map[internalName]bool{}
	var enter func(n internalName)
	enter = func(n internalName) {
		if seen[n] {
			return
		}
		record, ok := innerClasses.get(n)
		if !ok {
			return // not a known nested class: no InnerClasses entry
		}
		if record.outer != "" && innerClasses.has(record.outer) {
			enter(record.outer)
		}
		if seen[n] {
			return
		}
		seen[n] = true
		result = append(result, n)
	}
	// Pass 1: classes referenced while writing the class body, in intern order.
	limit := refCountBeforeAttrs
	if len(referencedClasses) < limit {
		limit = len(referencedClasses)
	}
	for i := 0; i < limit; i++ {
		n := internalName(referencedClasses[i])
		if n != thisName {
			enter(n)
		}
	}
	if thisName != "" {
		enter(thisName)
	}
	// Pass 2: the declared-member tree rooted at this class, breadth-first, each
	// level in reverse-declaration order.
	referenced := map[internalName]bool{}
	for _, c := range referencedClasses {
		referenced[internalName(c)] = true
	}
	childrenReverse := func(owner internalName) []internalName {
		var kids []internalName
		for _, name := range innerClasses.keys {
			if record := innerClasses.values[name]; record.outer == owner {
				kids = append(kids, name)
			}
		}
		// reverse
		for i, j := 0, len(kids)-1; i < j; i, j = i+1, j-1 {
			kids[i], kids[j] = kids[j], kids[i]
		}
		return kids
	}
	type queueItem struct {
		n     internalName
		depth int
	}
	var queue []queueItem
	for _, n := range childrenReverse(thisName) {
		queue = append(queue, queueItem{n: n, depth: 1})
	}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if item.depth == 1 || referenced[item.n] {
			enter(item.n)
		}
		for _, c := range childrenReverse(item.n) {
			queue = append(queue, queueItem{n: c, depth: item.depth + 1})
		}
	}
	if hasBootstrap {
		enter(lookupName)
	}
	return result
}

// --- method body code generation (generateBody) -----------------------------
