package compiler

// Type checker. Resolves AST type nodes to the Type model, computes the type of
// expressions, and resolves member access (a.b). Covers declared types, the
// common expression forms and member typing, plus assignability, overload
// resolution and a pragmatic JLS 18 type-argument inference subset. Everything
// unknown degrades to errorType. Port of src/compiler/checker.ts.

import (
	"regexp"
	"strings"
)

// SamName is a single abstract method's name (the invokedynamic call name).
type SamName = string

// LambdaInfo is what the emitter needs to lower a lambda expression.
type LambdaInfo struct {
	InterfaceType *Type   // the target functional interface (a Class type)
	SamName       SamName // the single abstract method's name
	ErasedParams  []*Type // SAM parameter types unsubstituted (type variables erase)
	ErasedReturn  *Type
	InstParams    []*Type // SAM parameter/return with the target's type arguments substituted
	InstReturn    *Type
}

// MethodRefInfo is a method reference's SAM info plus the referenced method.
type MethodRefInfo struct {
	LambdaInfo
	Kind        string  // static | bound | unbound | constructor | arrayConstructor
	OwnerSymbol *Symbol // declaring/constructed type (absent for an array constructor ref)
	Target      *Node   // the referenced method declaration (nil for a constructor ref)
}

// CallInfo is a resolved call's chosen overload and receiver substitution.
type CallInfo struct {
	Decl          *Node // MethodDeclaration
	ReceiverSubst substMap
}

type substMap map[*Symbol]*Type

// Checker analyses a program's types. Created with NewChecker.
type Checker struct {
	program         *Program
	symbolTypes     map[*Symbol]*Type
	expressionTypes map[*Node]*Type
	callInfoCache   map[*Node]*callInfoEntry

	booleanType, intType, charType *Type
	arrayLengthSymbol              *Symbol
}

type callInfoEntry struct {
	info     *CallInfo
	computed bool
}

// NewChecker creates a checker over a program.
func NewChecker(program *Program) *Checker {
	c := &Checker{
		program:         program,
		symbolTypes:     map[*Symbol]*Type{},
		expressionTypes: map[*Node]*Type{},
		callInfoCache:   map[*Node]*callInfoEntry{},
		booleanType:     primitiveType("boolean"),
		intType:         primitiveType("int"),
		charType:        primitiveType("char"),
	}
	// Synthetic symbol for the implicit `length` field of every array (JLS 10.7).
	c.arrayLengthSymbol = &Symbol{Flags: SymbolFlagsField, EscapedName: "length"}
	c.symbolTypes[c.arrayLengthSymbol] = c.intType
	return c
}

// --- primitive widening (JLS 5.1.2) and boxing (JLS 5.1.7) -------------------

var widening = map[string][]string{
	"byte":  {"short", "int", "long", "float", "double"},
	"short": {"int", "long", "float", "double"},
	"char":  {"int", "long", "float", "double"},
	"int":   {"long", "float", "double"},
	"long":  {"float", "double"},
	"float": {"double"},
}

var box = map[string]string{
	"boolean": "java.lang.Boolean",
	"byte":    "java.lang.Byte",
	"short":   "java.lang.Short",
	"char":    "java.lang.Character",
	"int":     "java.lang.Integer",
	"long":    "java.lang.Long",
	"float":   "java.lang.Float",
	"double":  "java.lang.Double",
}

var unbox = func() map[string]string {
	m := map[string]string{}
	for prim, fqn := range box {
		m[fqn] = prim
	}
	return m
}()

func primitiveWidens(from, to string) bool {
	if from == to {
		return true
	}
	for _, w := range widening[from] {
		if w == to {
			return true
		}
	}
	return false
}

func isComparisonOperator(op SyntaxKind) bool {
	switch op {
	case LessThanToken, GreaterThanToken, LessThanEqualsToken, GreaterThanEqualsToken,
		EqualsEqualsToken, ExclamationEqualsToken, AmpersandAmpersandToken, BarBarToken:
		return true
	default:
		return false
	}
}

func checkerIsTypeDeclarationKind(kind SyntaxKind) bool {
	switch kind {
	case ClassDeclaration, InterfaceDeclaration, EnumDeclaration, AnnotationTypeDeclaration, RecordDeclaration:
		return true
	default:
		return false
	}
}

func enclosingTypeSymbol(node *Node) *Symbol {
	current := node
	for current != nil {
		if checkerIsTypeDeclarationKind(current.Kind) && current.Symbol != nil {
			return current.Symbol
		}
		current = current.Parent
	}
	return nil
}

// --- generic AST accessors ---------------------------------------------------

// nodeTypeParameters returns the typeParameters NodeArray for any declaration
// that has them, or nil.
func nodeTypeParameters(node *Node) *NodeArray {
	switch node.Kind {
	case ClassDeclaration:
		return node.AsClassDeclaration().TypeParameters
	case InterfaceDeclaration:
		return node.AsInterfaceDeclaration().TypeParameters
	case RecordDeclaration:
		return node.AsRecordDeclaration().TypeParameters
	case MethodDeclaration:
		return node.AsMethodDeclaration().TypeParameters
	case ConstructorDeclaration:
		return node.AsConstructorDeclaration().TypeParameters
	default:
		return nil
	}
}

// nodeModifiers returns the modifiers NodeArray for declarations that have them.
func nodeModifiers(node *Node) *NodeArray {
	switch node.Kind {
	case MethodDeclaration:
		return node.AsMethodDeclaration().Modifiers
	case ConstructorDeclaration:
		return node.AsConstructorDeclaration().Modifiers
	case FieldDeclaration:
		return node.AsFieldDeclaration().Modifiers
	default:
		return nil
	}
}

// checkerSuperTypeNodes returns the direct super-type references of a type
// declaration (class extends/implements, interface extends, enum/record implements).
func checkerSuperTypeNodes(declaration *Node) []*Node {
	var out []*Node
	switch declaration.Kind {
	case ClassDeclaration:
		c := declaration.AsClassDeclaration()
		if c.ExtendsType != nil {
			out = append(out, c.ExtendsType)
		}
		if c.ImplementsTypes != nil {
			out = append(out, c.ImplementsTypes.Nodes...)
		}
	case InterfaceDeclaration:
		if e := declaration.AsInterfaceDeclaration().ExtendsTypes; e != nil {
			out = append(out, e.Nodes...)
		}
	case EnumDeclaration:
		if i := declaration.AsEnumDeclaration().ImplementsTypes; i != nil {
			out = append(out, i.Nodes...)
		}
	case RecordDeclaration:
		if i := declaration.AsRecordDeclaration().ImplementsTypes; i != nil {
			out = append(out, i.Nodes...)
		}
	}
	return out
}

func checkerExtendsType(node *Node) *Node {
	if node.Kind == ClassDeclaration {
		return node.AsClassDeclaration().ExtendsType
	}
	return nil
}

// --- type-variable substitution (JLS 4.5, 18) -------------------------------

func (c *Checker) declarationOf(symbol *Symbol) *Node {
	if symbol.ValueDeclaration != nil {
		return symbol.ValueDeclaration
	}
	if len(symbol.Declarations) > 0 {
		return symbol.Declarations[0]
	}
	return nil
}

// classTypeParameters returns the type-parameter symbols a generic type declares.
func (c *Checker) classTypeParameters(symbol *Symbol) []*Symbol {
	declaration := c.declarationOf(symbol)
	var out []*Symbol
	if declaration == nil {
		return out
	}
	if tps := nodeTypeParameters(declaration); tps != nil {
		for _, tp := range tps.Nodes {
			if tp.Symbol != nil {
				out = append(out, tp.Symbol)
			}
		}
	}
	return out
}

func (c *Checker) substitutionFor(symbol *Symbol, args []*Type) substMap {
	params := c.classTypeParameters(symbol)
	m := substMap{}
	for i, p := range params {
		if i < len(args) {
			m[p] = args[i]
		}
	}
	return m
}

func (c *Checker) substitute(t *Type, m substMap) *Type {
	if len(m) == 0 {
		return t
	}
	switch t.Kind {
	case TypeKindTypeVariable:
		if r, ok := m[t.Symbol]; ok {
			return r
		}
		return t
	case TypeKindClass:
		if len(t.TypeArguments) == 0 {
			return t
		}
		args := make([]*Type, len(t.TypeArguments))
		for i, a := range t.TypeArguments {
			args[i] = c.substitute(a, m)
		}
		return classType(t.Symbol, args)
	case TypeKindArray:
		return arrayType(c.substitute(t.ElementType, m))
	case TypeKindWildcard:
		if t.Bound != nil {
			nw := *t
			nw.Bound = c.substitute(t.Bound, m)
			return &nw
		}
		return t
	case TypeKindIntersection:
		types := make([]*Type, len(t.Types))
		for i, x := range t.Types {
			types[i] = c.substitute(x, m)
		}
		return &Type{Kind: TypeKindIntersection, Types: types}
	default:
		return t
	}
}

type typedMember struct {
	symbol *Symbol
	subst  substMap
}

// lookupTypedMember finds a member and the substitution to apply to its declared
// type, threading type arguments through the inheritance chain.
func (c *Checker) lookupTypedMember(receiver *Type, name string, seen map[*Symbol]bool) *typedMember {
	if seen == nil {
		seen = map[*Symbol]bool{}
	}
	symbol := receiver.Symbol
	if seen[symbol] {
		return nil
	}
	seen[symbol] = true
	subst := c.substitutionFor(symbol, receiver.TypeArguments)
	if own := symbol.Members[name]; own != nil {
		return &typedMember{symbol: own, subst: subst}
	}
	declaration := c.declarationOf(symbol)
	if declaration != nil {
		for _, typeNode := range checkerSuperTypeNodes(declaration) {
			if typeNode.Kind != TypeReference {
				continue
			}
			superType := c.substitute(c.resolveType(typeNode, declaration), subst)
			if superType.Kind == TypeKindClass {
				if found := c.lookupTypedMember(superType, name, seen); found != nil {
					return found
				}
			}
		}
		if declaration.Kind == EnumDeclaration {
			enumType := c.classTypeByFqn("java.lang.Enum")
			if enumType.Kind == TypeKindClass {
				if found := c.lookupTypedMember(enumType, name, seen); found != nil {
					return found
				}
			}
		}
	}
	object := c.objectSymbol()
	if object != nil && symbol != object {
		if inherited := object.Members[name]; inherited != nil {
			return &typedMember{symbol: inherited, subst: substMap{}}
		}
	}
	return nil
}

type typedOverload struct {
	decl  *Node
	subst substMap
}

// collectTypedOverloads gathers all method declarations named `name` reachable
// from `receiver`, each paired with the declaring type's substitution.
func (c *Checker) collectTypedOverloads(receiver *Type, name string, seen map[*Symbol]bool, out []typedOverload, sigs map[string]bool) []typedOverload {
	if seen == nil {
		seen = map[*Symbol]bool{}
	}
	if sigs == nil {
		sigs = map[string]bool{}
	}
	symbol := receiver.Symbol
	if seen[symbol] {
		return out
	}
	seen[symbol] = true
	subst := c.substitutionFor(symbol, receiver.TypeArguments)
	add := func(sym *Symbol, s substMap) {
		if sym == nil {
			return
		}
		for _, d := range sym.Declarations {
			if d.Kind != MethodDeclaration {
				continue
			}
			parts := []string{}
			for _, p := range c.methodParams(d) {
				parts = append(parts, typeToString(c.substitute(c.paramSlotType(p), s)))
			}
			sig := strings.Join(parts, ",")
			if sigs[sig] {
				continue
			}
			sigs[sig] = true
			out = append(out, typedOverload{decl: d, subst: s})
		}
	}
	add(symbol.Members[name], subst)
	declaration := c.declarationOf(symbol)
	if declaration != nil {
		for _, typeNode := range checkerSuperTypeNodes(declaration) {
			if typeNode.Kind != TypeReference {
				continue
			}
			superType := c.substitute(c.resolveType(typeNode, declaration), subst)
			if superType.Kind == TypeKindClass {
				out = c.collectTypedOverloads(superType, name, seen, out, sigs)
			}
		}
		if declaration.Kind == EnumDeclaration {
			e := c.classTypeByFqn("java.lang.Enum")
			if e.Kind == TypeKindClass {
				out = c.collectTypedOverloads(e, name, seen, out, sigs)
			}
		}
	}
	object := c.objectSymbol()
	if object != nil && symbol != object {
		add(object.Members[name], substMap{})
	}
	return out
}

// isClosedType reports whether a class type's full member set is known.
func (c *Checker) isClosedType(t *Type) bool {
	if t.Symbol.Flags&(SymbolFlagsEnum|SymbolFlagsRecord|SymbolFlagsAnnotation) != 0 {
		return false
	}
	var supertypesResolve func(symbol *Symbol, seen map[*Symbol]bool) bool
	supertypesResolve = func(symbol *Symbol, seen map[*Symbol]bool) bool {
		if seen[symbol] {
			return true
		}
		seen[symbol] = true
		declaration := c.declarationOf(symbol)
		if declaration == nil {
			return false
		}
		for _, typeNode := range checkerSuperTypeNodes(declaration) {
			if typeNode.Kind != TypeReference {
				return false
			}
			superSymbol := ResolveTypeEntityName(typeNode.AsTypeReference().TypeName, declaration, c.program)
			if superSymbol == nil || !supertypesResolve(superSymbol, seen) {
				return false
			}
		}
		return true
	}
	return supertypesResolve(t.Symbol, map[*Symbol]bool{})
}

// classTypeByFqn is the trusted dotted-name boundary: literal JDK names enter here.
func (c *Checker) classTypeByFqn(fqn string) *Type {
	symbol := c.program.GetGlobalIndex().GetType(Fqn(fqn))
	if symbol != nil {
		return classType(symbol, nil)
	}
	return errorType
}

// ResolveType resolves an AST type node to a Type.
func (c *Checker) ResolveType(typeNode, fromNode *Node) *Type {
	return c.resolveType(typeNode, fromNode)
}

func (c *Checker) resolveType(typeNode, fromNode *Node) *Type {
	switch typeNode.Kind {
	case PrimitiveType:
		name := tokenToString(typeNode.AsPrimitiveType().Keyword)
		if name == "" {
			name = "<error>"
		}
		return primitiveType(name)
	case ArrayType:
		return arrayType(c.resolveType(typeNode.AsArrayType().ElementType, fromNode))
	case WildcardType:
		w := typeNode.AsWildcardType()
		var bound *Type
		if w.Type != nil {
			bound = c.resolveType(w.Type, fromNode)
		}
		return &Type{Kind: TypeKindWildcard, IsExtends: w.HasExtends, IsSuper: w.HasSuper, Bound: bound}
	case VarType:
		return errorType // 'var' inference is P8
	case TypeReference:
		ref := typeNode.AsTypeReference()
		symbol := ResolveTypeEntityName(ref.TypeName, fromNode, c.program)
		if symbol == nil {
			return errorType
		}
		// Through getTypeOfSymbol so the variable carries its bound (cached once).
		if symbol.Flags&SymbolFlagsTypeParameter != 0 {
			return c.getTypeOfSymbol(symbol)
		}
		var args []*Type
		if ref.TypeArguments != nil {
			for _, a := range ref.TypeArguments.Nodes {
				args = append(args, c.resolveType(a, fromNode))
			}
		}
		return classType(symbol, args)
	default:
		return errorType
	}
}

type declaredTypeNode struct {
	typeNode *Node
	from     *Node
}

func (c *Checker) declaredTypeNodeOf(symbol *Symbol) *declaredTypeNode {
	declaration := c.declarationOf(symbol)
	if declaration == nil {
		return nil
	}
	switch declaration.Kind {
	case VariableDeclarator:
		parent := declaration.Parent
		var typeNode *Node
		switch parent.Kind {
		case FieldDeclaration:
			typeNode = parent.AsFieldDeclaration().Type
		case LocalVariableDeclarationStatement:
			typeNode = parent.AsLocalVariableDeclarationStatement().Type
		}
		if typeNode != nil {
			return &declaredTypeNode{typeNode: typeNode, from: declaration}
		}
		return nil
	case Parameter:
		if t := declaration.AsParameter().Type; t != nil {
			return &declaredTypeNode{typeNode: t, from: declaration}
		}
		return nil
	case RecordComponent:
		if t := declaration.AsRecordComponent().Type; t != nil {
			return &declaredTypeNode{typeNode: t, from: declaration}
		}
		return nil
	case TypePattern:
		if t := declaration.AsTypePattern().Type; t != nil {
			return &declaredTypeNode{typeNode: t, from: declaration}
		}
		return nil
	case MethodDeclaration:
		return &declaredTypeNode{typeNode: declaration.AsMethodDeclaration().ReturnType, from: declaration}
	case Resource:
		if t := declaration.AsResource().Type; t != nil {
			return &declaredTypeNode{typeNode: t, from: declaration}
		}
		return nil
	case Identifier:
		parent := declaration.Parent
		if parent.Kind == CatchClause {
			if ct := parent.AsCatchClause().CatchTypes; ct != nil && ct.Len() > 0 {
				return &declaredTypeNode{typeNode: ct.Nodes[0], from: declaration}
			}
		}
		if parent.Kind == InstanceofExpression && parent.AsInstanceofExpression().Type != nil {
			return &declaredTypeNode{typeNode: parent.AsInstanceofExpression().Type, from: declaration}
		}
		return nil
	default:
		return nil
	}
}

// GetTypeOfSymbol returns the type of a symbol.
func (c *Checker) GetTypeOfSymbol(symbol *Symbol) *Type { return c.getTypeOfSymbol(symbol) }

func (c *Checker) getTypeOfSymbol(symbol *Symbol) *Type {
	if cached, ok := c.symbolTypes[symbol]; ok {
		return cached
	}
	var t *Type
	switch {
	case symbol.Flags&SymbolFlagsTypeParameter != 0:
		tv := typeVariable(symbol)
		// Cache before resolving the bound: `T extends Comparable<T>` mentions T.
		c.symbolTypes[symbol] = tv
		declaration := c.declarationOf(symbol)
		var constraint *Node
		if declaration != nil && declaration.Kind == TypeParameter {
			if con := declaration.AsTypeParameter().Constraint; con != nil && con.Len() > 0 {
				constraint = con.Nodes[0]
			}
		}
		if constraint != nil {
			tv.Bound = c.resolveType(constraint, declaration)
		}
		return tv
	case symbol.Flags&SymbolFlagsType != 0:
		t = classType(symbol, nil)
	case symbol.Flags&SymbolFlagsEnumConstant != 0:
		if symbol.Parent != nil {
			t = classType(symbol.Parent, nil)
		} else {
			t = errorType
		}
	default:
		declared := c.declaredTypeNodeOf(symbol)
		if declared != nil {
			if declared.typeNode.Kind == VarType {
				c.symbolTypes[symbol] = errorType
				t = c.inferVarType(declared.from)
			} else {
				t = c.resolveType(declared.typeNode, declared.from)
				if declared.from.Kind == Parameter && declared.from.AsParameter().IsVarArgs {
					t = arrayType(t)
				}
				rank := 0
				switch declared.from.Kind {
				case VariableDeclarator:
					rank = declared.from.AsVariableDeclarator().ArrayRankAfterName
				case Parameter:
					rank = declared.from.AsParameter().ArrayRankAfterName
				}
				for rank > 0 {
					t = arrayType(t)
					rank--
				}
			}
		} else {
			t = c.inferLambdaParameterType(symbol)
		}
	}
	c.symbolTypes[symbol] = t
	return t
}

// functionalMethod finds a functional interface's single abstract method (SAM).
func (c *Checker) functionalMethod(typeSymbol *Symbol, seen map[*Symbol]bool) *Node {
	if seen == nil {
		seen = map[*Symbol]bool{}
	}
	if seen[typeSymbol] {
		return nil
	}
	seen[typeSymbol] = true
	// The SAM is the single abstract instance method (JLS 9.8): a method with no
	// body that is neither static nor private. default/static/private methods
	// (which carry a body) are not abstract and never the SAM. The TS reference
	// relies on the members map's declaration order to surface the abstract method
	// first; a Go map randomizes order, so filter to abstract explicitly.
	for _, member := range typeSymbol.Members {
		if member.Flags&SymbolFlagsMethod == 0 {
			continue
		}
		for _, d := range member.Declarations {
			if d.Kind != MethodDeclaration {
				continue
			}
			md := d.AsMethodDeclaration()
			if md.Body != nil || hasModifierKind(md.Modifiers, StaticKeyword) || hasModifierKind(md.Modifiers, PrivateKeyword) {
				continue // default / static / private: has a body, not the SAM
			}
			return d
		}
	}
	for _, superSymbol := range GetDirectSuperTypeSymbols(typeSymbol, c.program) {
		if found := c.functionalMethod(superSymbol, seen); found != nil {
			return found
		}
	}
	return nil
}

func (c *Checker) methodTypeParamSymbols(decl *Node) map[*Symbol]bool {
	tps := decl.AsMethodDeclaration().TypeParameters
	if tps == nil || tps.Len() == 0 {
		return nil
	}
	out := map[*Symbol]bool{}
	for _, p := range tps.Nodes {
		if p.Symbol != nil {
			out[p.Symbol] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isConcrete(t *Type) bool {
	switch t.Kind {
	case TypeKindPrimitive, TypeKindNull:
		return true
	case TypeKindClass:
		for _, a := range t.TypeArguments {
			if !isConcrete(a) {
				return false
			}
		}
		return true
	case TypeKindArray:
		return isConcrete(t.ElementType)
	default:
		return false
	}
}

func (c *Checker) unifyInto(pattern, actual *Type, vars map[*Symbol]bool, bindings substMap) {
	if pattern.Kind == TypeKindWildcard {
		if pattern.Bound != nil {
			c.unifyInto(pattern.Bound, actual, vars, bindings)
		}
		return
	}
	if actual.Kind == TypeKindWildcard {
		if actual.Bound != nil {
			c.unifyInto(pattern, actual.Bound, vars, bindings)
		}
		return
	}
	if pattern.Kind == TypeKindTypeVariable && vars[pattern.Symbol] {
		if _, has := bindings[pattern.Symbol]; !has && isConcrete(actual) {
			bindings[pattern.Symbol] = actual
		}
		return
	}
	if pattern.Kind == TypeKindClass && actual.Kind == TypeKindClass && pattern.Symbol == actual.Symbol {
		shared := len(pattern.TypeArguments)
		if len(actual.TypeArguments) < shared {
			shared = len(actual.TypeArguments)
		}
		for i := 0; i < shared; i++ {
			c.unifyInto(pattern.TypeArguments[i], actual.TypeArguments[i], vars, bindings)
		}
		return
	}
	if pattern.Kind == TypeKindArray && actual.Kind == TypeKindArray {
		c.unifyInto(pattern.ElementType, actual.ElementType, vars, bindings)
	}
}

func (c *Checker) contextualTypeOfCall(call *Node) *Type {
	parent := call.Parent
	switch parent.Kind {
	case VariableDeclarator:
		if parent.Symbol != nil {
			return c.getTypeOfSymbol(parent.Symbol)
		}
	case ReturnStatement:
		return c.enclosingReturnType(parent)
	case CallExpression:
		outer := parent.AsCallExpression()
		index := indexOfNode(outer.Arguments, call)
		if index < 0 {
			return nil
		}
		info := c.resolveCallInfo(parent)
		if info == nil {
			return nil
		}
		param := nodeParameterAt(info.Decl, index)
		if param != nil && param.AsParameter().Type != nil {
			return c.substitute(c.resolveType(param.AsParameter().Type, info.Decl), info.ReceiverSubst)
		}
	}
	return nil
}

func (c *Checker) inferredMethodSubst(call *Node, info *CallInfo) substMap {
	bindings := substMap{}
	vars := c.methodTypeParamSymbols(info.Decl)
	if vars == nil {
		return bindings
	}
	args := call.AsCallExpression().Arguments
	if args != nil {
		for i, argument := range args.Nodes {
			if argument.Kind == LambdaExpression || argument.Kind == MethodReferenceExpression {
				continue
			}
			param := nodeParameterAt(info.Decl, i)
			if param == nil || param.AsParameter().Type == nil {
				continue
			}
			c.unifyInto(
				c.substitute(c.resolveType(param.AsParameter().Type, info.Decl), info.ReceiverSubst),
				c.getTypeOfExpression(argument), vars, bindings)
		}
	}
	expected := c.contextualTypeOfCall(call)
	if expected != nil {
		c.unifyInto(
			c.substitute(c.resolveType(info.Decl.AsMethodDeclaration().ReturnType, info.Decl), info.ReceiverSubst),
			expected, vars, bindings)
	}
	return bindings
}

func (c *Checker) lambdaTargetType(lambda *Node) *Type {
	parent := lambda.Parent
	switch parent.Kind {
	case VariableDeclarator:
		if parent.Symbol != nil {
			return c.getTypeOfSymbol(parent.Symbol)
		}
		return nil
	case ReturnStatement:
		return c.enclosingReturnType(parent)
	case ObjectCreationExpression:
		creation := parent.AsObjectCreationExpression()
		index := indexOfNode(creation.Arguments, lambda)
		if index < 0 {
			return nil
		}
		created := c.getTypeOfExpression(parent)
		if created.Kind != TypeKindClass {
			return nil
		}
		declaration := c.declarationOf(created.Symbol)
		if declaration == nil || declaration.Kind != ClassDeclaration {
			return nil
		}
		var ctors []*Node
		for _, m := range declaration.AsClassDeclaration().Members.Nodes {
			if m.Kind == ConstructorDeclaration {
				ctors = append(ctors, m)
			}
		}
		argc := nodeArrayLen(creation.Arguments)
		var ctor *Node
		for _, ct := range ctors {
			if ct.AsConstructorDeclaration().Parameters.Len() == argc {
				ctor = ct
				break
			}
		}
		if ctor == nil && len(ctors) == 1 {
			ctor = ctors[0]
		}
		if ctor == nil {
			return nil
		}
		param := nodeParameterAt(ctor, index)
		if param == nil || param.AsParameter().Type == nil {
			return nil
		}
		return c.substitute(c.resolveType(param.AsParameter().Type, ctor),
			c.substitutionFor(created.Symbol, created.TypeArguments))
	case CallExpression:
		call := parent.AsCallExpression()
		index := indexOfNode(call.Arguments, lambda)
		if index < 0 {
			return nil
		}
		info := c.resolveCallInfo(parent)
		if info == nil {
			return nil
		}
		param := nodeParameterAt(info.Decl, index)
		if param == nil || param.AsParameter().Type == nil {
			return nil
		}
		declared := c.substitute(c.resolveType(param.AsParameter().Type, info.Decl), info.ReceiverSubst)
		methodSubst := c.inferredMethodSubst(parent, info)
		if len(methodSubst) > 0 {
			return c.substitute(declared, methodSubst)
		}
		return declared
	}
	return nil
}

// GetLambdaInfo returns lambda lowering info, or nil.
func (c *Checker) GetLambdaInfo(lambda *Node) *LambdaInfo {
	if lambda.Kind != LambdaExpression {
		return nil
	}
	target := c.lambdaTargetType(lambda)
	if target == nil || target.Kind != TypeKindClass {
		return nil
	}
	sam := c.functionalMethod(target.Symbol, nil)
	if sam == nil {
		return nil
	}
	info := c.functionalInfo(target, sam)
	return &info
}

func (c *Checker) functionalInfo(target *Type, sam *Node) LambdaInfo {
	subst := c.substitutionFor(target.Symbol, target.TypeArguments)
	md := sam.AsMethodDeclaration()
	var erasedParams []*Type
	for _, p := range md.Parameters.Nodes {
		erasedParams = append(erasedParams, c.resolveType(p.AsParameter().Type, sam))
	}
	erasedReturn := c.resolveType(md.ReturnType, sam)
	instParams := make([]*Type, len(erasedParams))
	for i, t := range erasedParams {
		instParams[i] = c.substitute(t, subst)
	}
	return LambdaInfo{
		InterfaceType: target,
		SamName:       md.Name.AsIdentifier().Text,
		ErasedParams:  erasedParams,
		ErasedReturn:  erasedReturn,
		InstParams:    instParams,
		InstReturn:    c.substitute(erasedReturn, subst),
	}
}

// GetMethodRefInfo returns method-reference lowering info, or nil.
func (c *Checker) GetMethodRefInfo(node *Node) *MethodRefInfo {
	if node.Kind != MethodReferenceExpression {
		return nil
	}
	ref := node.AsMethodReferenceExpression()
	target := c.lambdaTargetType(node)
	if target == nil || target.Kind != TypeKindClass {
		return nil
	}
	sam := c.functionalMethod(target.Symbol, nil)
	if sam == nil {
		return nil
	}
	fi := c.functionalInfo(target, sam)

	var asType func(e *Node) *Symbol
	asType = func(e *Node) *Symbol {
		if e.Kind == Identifier {
			return ResolveTypeEntityName(e, e, c.program)
		}
		if e.Kind == PropertyAccessExpression {
			access := e.AsPropertyAccessExpression()
			outer := asType(access.Expression)
			if outer != nil {
				nested := LookupMember(outer, access.Name.AsIdentifier().Text, MeaningType, c.program)
				if nested != nil && nested.Flags&SymbolFlagsType != 0 {
					return nested
				}
			}
		}
		return nil
	}
	overloads := func(typeSymbol *Symbol, name string) []*Node {
		m := LookupMember(typeSymbol, name, MeaningValue, c.program)
		var out []*Node
		if m != nil {
			for _, d := range m.Declarations {
				if d.Kind == MethodDeclaration {
					out = append(out, d)
				}
			}
		}
		return out
	}
	isStaticDecl := func(d *Node) bool {
		mods := nodeModifiers(d)
		if mods == nil {
			return false
		}
		for _, m := range mods.Nodes {
			if m.Kind == StaticKeyword {
				return true
			}
		}
		return false
	}
	arity := len(fi.InstParams)

	if ref.IsConstructorRef {
		if ref.Expression.Kind == ClassLiteralExpression {
			t := ref.Expression.AsClassLiteralExpression().Type
			if t != nil && t.Kind == ArrayType {
				return &MethodRefInfo{LambdaInfo: fi, Kind: "arrayConstructor"}
			}
		}
		owner := asType(ref.Expression)
		if owner != nil {
			return &MethodRefInfo{LambdaInfo: fi, Kind: "constructor", OwnerSymbol: owner}
		}
		return nil
	}
	name := ref.Name.AsIdentifier().Text
	typeSym := asType(ref.Expression)
	if typeSym != nil {
		cands := overloads(typeSym, name)
		for _, d := range cands {
			if isStaticDecl(d) && d.AsMethodDeclaration().Parameters.Len() == arity {
				return &MethodRefInfo{LambdaInfo: fi, Kind: "static", OwnerSymbol: typeSym, Target: d}
			}
		}
		var unbound *Node
		for _, d := range cands {
			if !isStaticDecl(d) && d.AsMethodDeclaration().Parameters.Len() == arity-1 {
				unbound = d
				break
			}
		}
		if unbound == nil && len(cands) == 1 {
			unbound = cands[0]
		}
		if unbound != nil {
			return &MethodRefInfo{LambdaInfo: fi, Kind: "unbound", OwnerSymbol: typeSym, Target: unbound}
		}
		return nil
	}
	recv := c.getTypeOfExpression(ref.Expression)
	if recv.Kind != TypeKindClass {
		return nil
	}
	cands := overloads(recv.Symbol, name)
	var decl *Node
	for _, d := range cands {
		if d.AsMethodDeclaration().Parameters.Len() == arity {
			decl = d
			break
		}
	}
	if decl == nil && len(cands) == 1 {
		decl = cands[0]
	}
	if decl != nil {
		return &MethodRefInfo{LambdaInfo: fi, Kind: "bound", OwnerSymbol: recv.Symbol, Target: decl}
	}
	return nil
}

func (c *Checker) inferLambdaParameterType(symbol *Symbol) *Type {
	declaration := c.declarationOf(symbol)
	if declaration == nil || declaration.Kind != Identifier {
		return errorType
	}
	lambda := declaration.Parent
	if lambda == nil || lambda.Kind != LambdaExpression {
		return errorType
	}
	index := indexOfNode(lambda.AsLambdaExpression().Parameters, declaration)
	target := c.lambdaTargetType(lambda)
	if index < 0 || target == nil || target.Kind != TypeKindClass {
		return errorType
	}
	sam := c.functionalMethod(target.Symbol, nil)
	if sam == nil || index >= sam.AsMethodDeclaration().Parameters.Len() {
		return errorType
	}
	paramType := c.resolveType(sam.AsMethodDeclaration().Parameters.Nodes[index].AsParameter().Type, sam)
	substituted := c.substitute(paramType, c.substitutionFor(target.Symbol, target.TypeArguments))
	if substituted.Kind == TypeKindWildcard && substituted.Bound != nil {
		return substituted.Bound
	}
	return substituted
}

func (c *Checker) inferVarType(declaration *Node) *Type {
	if declaration.Kind == VariableDeclarator {
		init := declaration.AsVariableDeclarator().Initializer
		if init != nil {
			return c.getTypeOfExpression(init)
		}
		return errorType
	}
	if declaration.Kind == Parameter && declaration.Parent.Kind == ForEachStatement {
		iterable := c.getTypeOfExpression(declaration.Parent.AsForEachStatement().Expression)
		return c.elementTypeOf(iterable)
	}
	return errorType
}

func (c *Checker) elementTypeOf(iterable *Type) *Type {
	if iterable.Kind == TypeKindArray {
		return iterable.ElementType
	}
	if iterable.Kind == TypeKindClass {
		iterableSymbol := c.program.GetGlobalIndex().GetType("java.lang.Iterable")
		if iterableSymbol != nil {
			instance := c.asInstanceOf(iterable, iterableSymbol, nil)
			if instance != nil && len(instance.TypeArguments) > 0 {
				return instance.TypeArguments[0]
			}
		}
	}
	return errorType
}

func (c *Checker) asInstanceOf(receiver *Type, targetSymbol *Symbol, seen map[*Symbol]bool) *Type {
	if receiver.Symbol == targetSymbol {
		return receiver
	}
	if seen == nil {
		seen = map[*Symbol]bool{}
	}
	if seen[receiver.Symbol] {
		return nil
	}
	seen[receiver.Symbol] = true
	declaration := c.declarationOf(receiver.Symbol)
	if declaration == nil {
		return nil
	}
	subst := c.substitutionFor(receiver.Symbol, receiver.TypeArguments)
	for _, typeNode := range checkerSuperTypeNodes(declaration) {
		if typeNode.Kind != TypeReference {
			continue
		}
		superType := c.substitute(c.resolveType(typeNode, declaration), subst)
		if superType.Kind == TypeKindClass {
			if found := c.asInstanceOf(superType, targetSymbol, seen); found != nil {
				return found
			}
		}
	}
	return nil
}

func (c *Checker) arrayMember(name string) *Symbol {
	if name == "length" {
		return c.arrayLengthSymbol
	}
	if obj := c.objectSymbol(); obj != nil {
		return obj.Members[name]
	}
	return nil
}

func (c *Checker) typeOfMemberAccess(access *PropertyAccessExpressionData) *Type {
	receiver := c.getTypeOfExpression(access.Expression)
	if receiver.Kind == TypeKindArray {
		member := c.arrayMember(access.Name.AsIdentifier().Text)
		if member != nil {
			return c.getTypeOfSymbol(member)
		}
		return errorType
	}
	if receiver.Kind != TypeKindClass {
		return errorType
	}
	found := c.lookupTypedMember(receiver, access.Name.AsIdentifier().Text, nil)
	if found == nil {
		return errorType
	}
	return c.substitute(c.getTypeOfSymbol(found.symbol), found.subst)
}

var collapseWhitespace = regexp.MustCompile(`\s+`)

func (c *Checker) nodeSourceText(node *Node) string {
	text := GetSourceFileOfNode(node).AsSourceFile().Text
	start := skipTrivia(text, node.Pos)
	if start > node.End {
		start = node.End
	}
	return collapseWhitespace.ReplaceAllString(strings.TrimSpace(text[start:node.End]), " ")
}

// --- name resolution for member access ---------------------------------------

func (c *Checker) receiverClassType(t *Type, depth int) *Type {
	if t.Kind == TypeKindClass {
		return t
	}
	if t.Kind == TypeKindTypeVariable && t.Bound != nil && depth < 8 {
		return c.receiverClassType(t.Bound, depth+1)
	}
	return nil
}

func (c *Checker) resolveMemberAccess(access *PropertyAccessExpressionData) *Symbol {
	targetType := c.getTypeOfExpression(access.Expression)
	if targetType.Kind == TypeKindArray {
		return c.arrayMember(access.Name.AsIdentifier().Text)
	}
	receiver := c.receiverClassType(targetType, 0)
	if receiver == nil {
		return nil
	}
	return LookupMember(receiver.Symbol, access.Name.AsIdentifier().Text, MeaningAny, c.program)
}

// ResolveName resolves a name use OR a member access (a.b) to its symbol.
func (c *Checker) ResolveName(identifier *Node) *Symbol {
	parent := identifier.Parent
	if parent != nil && parent.Kind == PropertyAccessExpression && parent.AsPropertyAccessExpression().Name == identifier {
		return c.resolveMemberAccess(parent.AsPropertyAccessExpression())
	}
	if direct := ResolveIdentifier(identifier, c.program); direct != nil {
		return direct
	}
	return c.resolveQualifiedSegment(identifier)
}

func (c *Checker) qualifiedPrefix(identifier *Node) (string, bool) {
	parent := identifier.Parent
	if parent == nil || parent.Kind != QualifiedName {
		return "", false
	}
	qn := parent.AsQualifiedName()
	if qn.Right == identifier {
		return entityNameToString(parent), true
	}
	if qn.Left == identifier {
		return identifier.AsIdentifier().Text, true
	}
	return "", false
}

func (c *Checker) resolveQualifiedSegment(identifier *Node) *Symbol {
	prefix, ok := c.qualifiedPrefix(identifier)
	if !ok {
		return nil
	}
	index := c.program.GetGlobalIndex()
	if t := index.GetType(Fqn(prefix)); t != nil {
		return t
	}
	return index.GetPackageByName(PackageName(prefix))
}

// --- expression typing -------------------------------------------------------

func (c *Checker) numericLiteralType(value string) *Type {
	v := strings.ReplaceAll(value, "_", "")
	if len(v) >= 2 && v[0] == '0' && (v[1] == 'x' || v[1] == 'X' || v[1] == 'b' || v[1] == 'B') {
		if strings.ContainsAny(v, "pP") {
			return primitiveType("double")
		}
		if hasSuffix(v, "lL") {
			return primitiveType("long")
		}
		return c.intType
	}
	if hasSuffix(v, "lL") {
		return primitiveType("long")
	}
	if hasSuffix(v, "fF") {
		return primitiveType("float")
	}
	if hasSuffix(v, "dD") || strings.ContainsAny(v, ".eE") {
		return primitiveType("double")
	}
	return c.intType
}

func hasSuffix(s, set string) bool {
	if len(s) == 0 {
		return false
	}
	return strings.IndexByte(set, s[len(s)-1]) >= 0
}

func (c *Checker) unaryPromoted(t *Type) *Type {
	if t.Kind == TypeKindPrimitive && (t.Name == "byte" || t.Name == "short" || t.Name == "char") {
		return c.intType
	}
	return t
}

func (c *Checker) widerNumeric(a, b *Type) *Type {
	order := []string{"int", "long", "float", "double"}
	rank := func(t *Type) int {
		if t.Kind != TypeKindPrimitive {
			return -1
		}
		promoted := t.Name
		if promoted == "byte" || promoted == "short" || promoted == "char" {
			promoted = "int"
		}
		for i, o := range order {
			if o == promoted {
				return i
			}
		}
		return -1
	}
	ra, rb := rank(a), rank(b)
	if ra < 0 && rb < 0 {
		return errorType
	}
	m := ra
	if rb > m {
		m = rb
	}
	return primitiveType(order[m])
}

func isStringType(t, stringType *Type) bool {
	return t.Kind == TypeKindClass && stringType.Kind == TypeKindClass && t.Symbol == stringType.Symbol
}

// GetTypeOfExpression returns the type of an expression node.
func (c *Checker) GetTypeOfExpression(node *Node) *Type { return c.getTypeOfExpression(node) }

func (c *Checker) getTypeOfExpression(node *Node) *Type {
	if cached, ok := c.expressionTypes[node]; ok {
		return cached
	}
	t := c.computeExpressionType(node)
	c.expressionTypes[node] = t
	return t
}

func (c *Checker) computeExpressionType(node *Node) *Type {
	switch node.Kind {
	case NumericLiteral:
		return c.numericLiteralType(node.AsLiteralExpression().Value)
	case StringLiteral, TextBlockLiteral:
		return c.classTypeByFqn("java.lang.String")
	case CharacterLiteral:
		return c.charType
	case TrueKeyword, FalseKeyword:
		return c.booleanType
	case NullKeyword:
		return nullType
	case Identifier:
		symbol := ResolveIdentifier(node, c.program)
		if symbol != nil {
			return c.getTypeOfSymbol(symbol)
		}
		return errorType
	case ThisExpression:
		if qualifier := node.AsThisExpression().Qualifier; qualifier != nil {
			return c.getTypeOfExpression(qualifier)
		}
		enclosing := enclosingTypeSymbol(node)
		if enclosing != nil {
			return classType(enclosing, nil)
		}
		return errorType
	case SuperExpression:
		enclosing := enclosingTypeSymbol(node)
		var decl *Node
		if enclosing != nil {
			decl = c.declarationOf(enclosing)
		}
		if decl != nil {
			if ext := checkerExtendsType(decl); ext != nil {
				base := c.resolveType(ext, decl)
				if base.Kind == TypeKindClass {
					return base
				}
			}
		}
		return c.classTypeByFqn("java.lang.Object")
	case ParenthesizedExpression:
		return c.getTypeOfExpression(node.AsParenthesizedExpression().Expression)
	case CastExpression:
		return c.resolveType(node.AsCastExpression().Type, node)
	case PropertyAccessExpression:
		return c.typeOfMemberAccess(node.AsPropertyAccessExpression())
	case CallExpression:
		return c.typeOfCall(node)
	case ObjectCreationExpression:
		return c.resolveType(node.AsObjectCreationExpression().Type, node)
	case ArrayCreationExpression:
		n := node.AsArrayCreationExpression()
		t := c.resolveType(n.ElementType, node)
		for i := 0; i < nodeArrayLen(n.Dimensions)+n.AdditionalRank; i++ {
			t = arrayType(t)
		}
		return t
	case ElementAccessExpression:
		target := c.getTypeOfExpression(node.AsElementAccessExpression().Expression)
		if target.Kind == TypeKindArray {
			return target.ElementType
		}
		return errorType
	case InstanceofExpression:
		return c.booleanType
	case PrefixUnaryExpression:
		u := node.AsPrefixUnaryExpression()
		if u.Operator == ExclamationToken {
			return c.booleanType
		}
		operand := c.getTypeOfExpression(u.Operand)
		if u.Operator == PlusToken || u.Operator == MinusToken || u.Operator == TildeToken {
			return c.unaryPromoted(operand)
		}
		return operand
	case PostfixUnaryExpression:
		return c.getTypeOfExpression(node.AsPostfixUnaryExpression().Operand)
	case ConditionalExpression:
		cond := node.AsConditionalExpression()
		t := c.getTypeOfExpression(cond.WhenTrue)
		f := c.getTypeOfExpression(cond.WhenFalse)
		if t.Kind == TypeKindError {
			return f
		}
		if f.Kind == TypeKindError {
			return t
		}
		if t.Kind == TypeKindNull {
			return f
		}
		if f.Kind == TypeKindNull {
			return t
		}
		num := c.widerNumeric(t, f)
		if num.Kind != TypeKindError {
			return num
		}
		if c.isAssignableTo(f, t, false) {
			return t
		}
		if c.isAssignableTo(t, f, false) {
			return f
		}
		return c.classTypeByFqn("java.lang.Object")
	case BinaryExpression:
		b := node.AsBinaryExpression()
		if isComparisonOperator(b.OperatorToken) {
			return c.booleanType
		}
		left := c.getTypeOfExpression(b.Left)
		right := c.getTypeOfExpression(b.Right)
		if b.OperatorToken == PlusToken {
			stringType := c.classTypeByFqn("java.lang.String")
			if isStringType(left, stringType) || isStringType(right, stringType) {
				return stringType
			}
		}
		if b.OperatorToken == LessThanLessThanToken || b.OperatorToken == GreaterThanGreaterThanToken || b.OperatorToken == GreaterThanGreaterThanGreaterThanToken {
			return c.widerNumeric(left, c.intType)
		}
		isBool := func(t *Type) bool { return t.Kind == TypeKindPrimitive && t.Name == "boolean" }
		if (b.OperatorToken == AmpersandToken || b.OperatorToken == BarToken || b.OperatorToken == CaretToken) && isBool(left) && isBool(right) {
			return c.booleanType
		}
		return c.widerNumeric(left, right)
	default:
		return errorType
	}
}

func (c *Checker) objectSymbol() *Symbol {
	return c.program.GetGlobalIndex().GetType("java.lang.Object")
}

func (c *Checker) isClassSubtype(sourceSym, targetSym *Symbol) bool {
	if sourceSym == targetSym {
		return true
	}
	if targetSym == c.objectSymbol() {
		return true
	}
	seen := map[*Symbol]bool{}
	queue := []*Symbol{sourceSym}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == targetSym {
			return true
		}
		if seen[current] {
			continue
		}
		seen[current] = true
		queue = append(queue, GetDirectSuperTypeSymbols(current, c.program)...)
	}
	return false
}

func typesEqual(a, b *Type) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case TypeKindPrimitive:
		return a.Name == b.Name
	case TypeKindClass:
		if a.Symbol != b.Symbol || len(a.TypeArguments) != len(b.TypeArguments) {
			return false
		}
		for i := range a.TypeArguments {
			if !typesEqual(a.TypeArguments[i], b.TypeArguments[i]) {
				return false
			}
		}
		return true
	case TypeKindArray:
		return typesEqual(a.ElementType, b.ElementType)
	case TypeKindTypeVariable:
		return a.Symbol == b.Symbol
	default:
		return true
	}
}

func (c *Checker) typeArgumentsCompatible(srcArgs, tgtArgs []*Type) bool {
	if len(srcArgs) == 0 || len(tgtArgs) == 0 {
		return true
	}
	if len(srcArgs) != len(tgtArgs) {
		return true
	}
	for i, tgt := range tgtArgs {
		src := srcArgs[i]
		if tgt.Kind == TypeKindWildcard {
			if tgt.IsExtends && tgt.Bound != nil {
				if !c.isAssignableTo(src, tgt.Bound, true) {
					return false
				}
				continue
			}
			if tgt.IsSuper && tgt.Bound != nil {
				if !c.isAssignableTo(tgt.Bound, src, true) {
					return false
				}
				continue
			}
			continue
		}
		if !typesEqual(src, tgt) {
			return false
		}
	}
	return true
}

func (c *Checker) isAssignableToClass(source, target *Type, allowBoxing bool) bool {
	switch source.Kind {
	case TypeKindNull:
		return true
	case TypeKindPrimitive:
		if !allowBoxing {
			return false
		}
		if boxed, ok := box[source.Name]; ok {
			return c.isAssignableToClass(c.classTypeByFqn(boxed), target, true)
		}
		return false
	case TypeKindClass:
		if source.Symbol == target.Symbol {
			return c.typeArgumentsCompatible(source.TypeArguments, target.TypeArguments)
		}
		return c.isClassSubtype(source.Symbol, target.Symbol)
	case TypeKindArray:
		return target.Symbol == c.objectSymbol()
	case TypeKindTypeVariable:
		return target.Symbol == c.objectSymbol()
	default:
		return false
	}
}

func (c *Checker) isAssignableToPrimitive(source, target *Type, allowBoxing bool) bool {
	if source.Kind == TypeKindPrimitive {
		return primitiveWidens(source.Name, target.Name)
	}
	if allowBoxing && source.Kind == TypeKindClass {
		if unboxed, ok := unbox[c.fqnOf(source)]; ok {
			return primitiveWidens(unboxed, target.Name)
		}
	}
	return false
}

func (c *Checker) fqnOf(t *Type) string {
	parts := []string{t.Symbol.EscapedName}
	parent := t.Symbol.Parent
	for parent != nil && parent.EscapedName != "" {
		parts = append([]string{parent.EscapedName}, parts...)
		parent = parent.Parent
	}
	return strings.Join(parts, ".")
}

// IsAssignableTo reports whether a value of source can be assigned to target.
func (c *Checker) IsAssignableTo(source, target *Type) bool {
	return c.isAssignableTo(source, target, true)
}

func (c *Checker) isAssignableTo(source, target *Type, allowBoxing bool) bool {
	if isError(source) || isError(target) {
		return true
	}
	if source == target {
		return true
	}
	switch target.Kind {
	case TypeKindPrimitive:
		return c.isAssignableToPrimitive(source, target, allowBoxing)
	case TypeKindClass:
		return c.isAssignableToClass(source, target, allowBoxing)
	case TypeKindArray:
		if source.Kind == TypeKindNull {
			return true
		}
		if source.Kind != TypeKindArray {
			return false
		}
		if source.ElementType.Kind == TypeKindPrimitive || target.ElementType.Kind == TypeKindPrimitive {
			return typesEqual(source.ElementType, target.ElementType)
		}
		return c.isAssignableTo(source.ElementType, target.ElementType, true)
	case TypeKindTypeVariable:
		return source.Kind != TypeKindPrimitive || allowBoxing
	default:
		return source.Kind == TypeKindNull || isError(source)
	}
}

// --- overload resolution (JLS 15.12.2) ---------------------------------------

type paramInfo struct {
	typ       *Type
	isVarArgs bool
}

func (c *Checker) methodParams(decl *Node) []paramInfo {
	var out []paramInfo
	for _, p := range decl.AsMethodDeclaration().Parameters.Nodes {
		pd := p.AsParameter()
		out = append(out, paramInfo{typ: c.resolveType(pd.Type, decl), isVarArgs: pd.IsVarArgs})
	}
	return out
}

func (c *Checker) paramSlotType(p paramInfo) *Type {
	if p.isVarArgs {
		return arrayType(p.typ)
	}
	return p.typ
}

func (c *Checker) applicable(params []paramInfo, args []*Type, allowBoxing, varargs bool) bool {
	if !varargs {
		if len(params) != len(args) {
			return false
		}
		for i, p := range params {
			if !c.isAssignableTo(args[i], c.paramSlotType(p), allowBoxing) {
				return false
			}
		}
		return true
	}
	if len(params) == 0 {
		return false
	}
	last := params[len(params)-1]
	if !last.isVarArgs {
		return false
	}
	if len(args) < len(params)-1 {
		return false
	}
	for i := 0; i < len(params)-1; i++ {
		if !c.isAssignableTo(args[i], params[i].typ, true) {
			return false
		}
	}
	for i := len(params) - 1; i < len(args); i++ {
		if !c.isAssignableTo(args[i], last.typ, true) {
			return false
		}
	}
	return true
}

func (c *Checker) moreSpecific(a, b *Node) bool {
	pa := c.methodParams(a)
	pb := c.methodParams(b)
	if len(pa) != len(pb) {
		return false
	}
	for i := range pa {
		if !c.isAssignableTo(c.paramSlotType(pa[i]), c.paramSlotType(pb[i]), true) {
			return false
		}
	}
	return true
}

func (c *Checker) chooseOverload(decls []*Node, args []*Type) *Node {
	phases := [][2]bool{{false, false}, {true, false}, {true, true}}
	for _, ph := range phases {
		var ok []*Node
		for _, d := range decls {
			if c.applicable(c.methodParams(d), args, ph[0], ph[1]) {
				ok = append(ok, d)
			}
		}
		if len(ok) > 0 {
			best := ok[0]
			for _, d := range ok[1:] {
				if c.moreSpecific(d, best) {
					best = d
				}
			}
			return best
		}
	}
	return decls[0]
}

func (c *Checker) resolveCallInfo(call *Node) *CallInfo {
	if cached, ok := c.callInfoCache[call]; ok && cached.computed {
		return cached.info
	}
	result := c.resolveCallInfoWorker(call)
	c.callInfoCache[call] = &callInfoEntry{info: result, computed: true}
	return result
}

func (c *Checker) resolveCallInfoWorker(call *Node) *CallInfo {
	callee := call.AsCallExpression().Expression
	var symbol *Symbol
	receiverSubst := substMap{}
	if callee.Kind == Identifier {
		symbol = ResolveIdentifier(callee, c.program)
	} else if callee.Kind == PropertyAccessExpression {
		access := callee.AsPropertyAccessExpression()
		receiver := c.receiverClassType(c.getTypeOfExpression(access.Expression), 0)
		if receiver != nil {
			cands := c.collectTypedOverloads(receiver, access.Name.AsIdentifier().Text, nil, nil, nil)
			if len(cands) == 0 {
				return nil
			}
			var decl *Node
			if len(cands) == 1 {
				decl = cands[0].decl
			} else {
				var decls []*Node
				for _, cd := range cands {
					decls = append(decls, cd.decl)
				}
				decl = c.chooseOverload(decls, c.argTypes(call))
			}
			var subst substMap
			for _, cd := range cands {
				if cd.decl == decl {
					subst = cd.subst
					break
				}
			}
			return &CallInfo{Decl: decl, ReceiverSubst: subst}
		}
		symbol = c.resolveMemberAccess(access)
	}
	if symbol == nil {
		return nil
	}
	var decls []*Node
	for _, d := range symbol.Declarations {
		if d.Kind == MethodDeclaration {
			decls = append(decls, d)
		}
	}
	if len(decls) == 0 {
		return nil
	}
	var decl *Node
	if len(decls) == 1 {
		decl = decls[0]
	} else {
		decl = c.chooseOverload(decls, c.argTypes(call))
	}
	return &CallInfo{Decl: decl, ReceiverSubst: receiverSubst}
}

func (c *Checker) argTypes(call *Node) []*Type {
	var out []*Type
	if args := call.AsCallExpression().Arguments; args != nil {
		for _, a := range args.Nodes {
			out = append(out, c.getTypeOfExpression(a))
		}
	}
	return out
}

// ResolveCall returns the chosen overload for a call, or nil.
func (c *Checker) ResolveCall(call *Node) *Node {
	if info := c.resolveCallInfo(call); info != nil {
		return info.Decl
	}
	return nil
}

// ResolveCallCandidates returns every overload declaration a call could bind to.
func (c *Checker) ResolveCallCandidates(call *Node) []*Node {
	callee := call.AsCallExpression().Expression
	if callee.Kind == PropertyAccessExpression {
		access := callee.AsPropertyAccessExpression()
		receiver := c.receiverClassType(c.getTypeOfExpression(access.Expression), 0)
		if receiver != nil {
			var out []*Node
			for _, cd := range c.collectTypedOverloads(receiver, access.Name.AsIdentifier().Text, nil, nil, nil) {
				out = append(out, cd.decl)
			}
			return out
		}
		symbol := c.resolveMemberAccess(access)
		return methodDeclsOf(symbol)
	}
	if callee.Kind == Identifier {
		return methodDeclsOf(ResolveIdentifier(callee, c.program))
	}
	return nil
}

func methodDeclsOf(symbol *Symbol) []*Node {
	if symbol == nil {
		return nil
	}
	var out []*Node
	for _, d := range symbol.Declarations {
		if d.Kind == MethodDeclaration {
			out = append(out, d)
		}
	}
	return out
}

func (c *Checker) methodTypeParameters(decl *Node) map[*Symbol]bool {
	out := map[*Symbol]bool{}
	if tps := decl.AsMethodDeclaration().TypeParameters; tps != nil {
		for _, tp := range tps.Nodes {
			if tp.Symbol != nil {
				out[tp.Symbol] = true
			}
		}
	}
	return out
}

func (c *Checker) boxIfPrimitive(t *Type) *Type {
	if t.Kind == TypeKindPrimitive {
		if boxed, ok := box[t.Name]; ok {
			return c.classTypeByFqn(boxed)
		}
	}
	return t
}

func (c *Checker) unify(param, arg *Type, vars map[*Symbol]bool, out substMap) {
	switch param.Kind {
	case TypeKindTypeVariable:
		if vars[param.Symbol] {
			if _, has := out[param.Symbol]; !has && !isError(arg) && arg.Kind != TypeKindNull {
				out[param.Symbol] = arg
			}
		}
	case TypeKindClass:
		if arg.Kind == TypeKindClass && param.Symbol == arg.Symbol {
			for i, pa := range param.TypeArguments {
				a := errorType
				if i < len(arg.TypeArguments) {
					a = arg.TypeArguments[i]
				}
				c.unify(pa, a, vars, out)
			}
		}
	case TypeKindArray:
		if arg.Kind == TypeKindArray {
			c.unify(param.ElementType, arg.ElementType, vars, out)
		}
	}
}

func (c *Checker) inferMethodTypeArguments(decl *Node, argTypes []*Type, receiverSubst substMap, vars map[*Symbol]bool) substMap {
	out := substMap{}
	for i, p := range decl.AsMethodDeclaration().Parameters.Nodes {
		if i >= len(argTypes) {
			break
		}
		paramType := c.substitute(c.resolveType(p.AsParameter().Type, decl), receiverSubst)
		c.unify(paramType, c.boxIfPrimitive(argTypes[i]), vars, out)
	}
	return out
}

func (c *Checker) enumStaticCallType(call *Node) *Type {
	callee := call.AsCallExpression().Expression
	if callee.Kind != PropertyAccessExpression {
		return nil
	}
	access := callee.AsPropertyAccessExpression()
	if access.Expression.Kind != Identifier {
		return nil
	}
	sym := ResolveTypeEntityName(access.Expression, access.Expression, c.program)
	if sym == nil || sym.Flags&SymbolFlagsEnum == 0 {
		return nil
	}
	t := classType(sym, nil)
	argc := nodeArrayLen(call.AsCallExpression().Arguments)
	name := access.Name.AsIdentifier().Text
	if name == "values" && argc == 0 {
		return arrayType(t)
	}
	if name == "valueOf" && argc == 1 {
		return t
	}
	return nil
}

func (c *Checker) typeOfCall(call *Node) *Type {
	callee := call.AsCallExpression().Expression
	if callee.Kind == PropertyAccessExpression && nodeArrayLen(call.AsCallExpression().Arguments) == 0 {
		pa := callee.AsPropertyAccessExpression()
		if pa.Name.AsIdentifier().Text == "clone" {
			recv := c.getTypeOfExpression(pa.Expression)
			if recv.Kind == TypeKindArray {
				return recv
			}
		}
	}
	if enumType := c.enumStaticCallType(call); enumType != nil {
		return enumType
	}
	info := c.resolveCallInfo(call)
	if info == nil {
		return errorType
	}
	returnType := c.substitute(c.resolveType(info.Decl.AsMethodDeclaration().ReturnType, info.Decl), info.ReceiverSubst)
	vars := c.methodTypeParameters(info.Decl)
	if len(vars) > 0 {
		returnType = c.substitute(returnType, c.inferMethodTypeArguments(info.Decl, c.argTypes(call), info.ReceiverSubst, vars))
	}
	return returnType
}

func (c *Checker) enclosingReturnType(node *Node) *Type {
	current := node
	for current != nil {
		if current.Kind == MethodDeclaration {
			return c.resolveType(current.AsMethodDeclaration().ReturnType, current)
		}
		if current.Kind == LambdaExpression {
			if info := c.GetLambdaInfo(current); info != nil {
				return info.InstReturn
			}
			return nil
		}
		current = current.Parent
	}
	return nil
}

// --- small node helpers ------------------------------------------------------

func indexOfNode(arr *NodeArray, node *Node) int {
	if arr == nil {
		return -1
	}
	for i, n := range arr.Nodes {
		if n == node {
			return i
		}
	}
	return -1
}

func nodeArrayLen(arr *NodeArray) int {
	if arr == nil {
		return 0
	}
	return len(arr.Nodes)
}

// nodeParameterAt returns the i-th parameter of a method/constructor declaration.
func nodeParameterAt(decl *Node, i int) *Node {
	var params *NodeArray
	switch decl.Kind {
	case MethodDeclaration:
		params = decl.AsMethodDeclaration().Parameters
	case ConstructorDeclaration:
		params = decl.AsConstructorDeclaration().Parameters
	}
	if params == nil || i < 0 || i >= len(params.Nodes) {
		return nil
	}
	return params.Nodes[i]
}
