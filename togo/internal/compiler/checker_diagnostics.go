package compiler

// Checker hover/signature rendering and high-precision semantic diagnostics.
// Port of the remaining parts of src/compiler/checker.ts.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// --- hover / signatures ------------------------------------------------------

// TypeStringOfSymbol returns a display string for a symbol's type (for hover).
func (c *Checker) TypeStringOfSymbol(symbol *Symbol) string {
	declared := c.declaredTypeNodeOf(symbol)
	if declared != nil && declared.typeNode.Kind != VarType {
		if text := c.nodeSourceText(declared.typeNode); text != "" {
			return text
		}
	}
	t := c.getTypeOfSymbol(symbol)
	if !isError(t) {
		return typeToString(t)
	}
	if declared != nil && declared.typeNode.Kind == VarType {
		return "var"
	}
	return typeToString(t)
}

// SignatureOfSymbol returns the full signature of a method/constructor symbol.
func (c *Checker) SignatureOfSymbol(symbol *Symbol) (string, bool) {
	declaration := c.declarationOf(symbol)
	if declaration == nil {
		return "", false
	}
	return c.SignatureOfDeclaration(declaration)
}

// SignatureOfDeclaration returns the signature of a method/constructor declaration.
func (c *Checker) SignatureOfDeclaration(declaration *Node) (string, bool) {
	if declaration.Kind != MethodDeclaration && declaration.Kind != ConstructorDeclaration {
		return "", false
	}
	var parts []string
	if tps := nodeTypeParameters(declaration); tps != nil && tps.Len() > 0 {
		var labels []string
		for _, tp := range tps.Nodes {
			labels = append(labels, c.nodeSourceText(tp))
		}
		parts = append(parts, "<"+strings.Join(labels, ", ")+">")
	}
	if declaration.Kind == MethodDeclaration {
		parts = append(parts, c.nodeSourceText(declaration.AsMethodDeclaration().ReturnType))
	}
	var params []string
	for _, p := range declarationParameters(declaration).Nodes {
		params = append(params, c.nodeSourceText(p))
	}
	name := declarationName(declaration).AsIdentifier().Text
	prefix := strings.Join(parts, " ")
	if len(parts) > 0 {
		prefix += " "
	}
	signature := prefix + name + "(" + strings.Join(params, ", ") + ")"
	if declaration.Kind == MethodDeclaration {
		if throws := declaration.AsMethodDeclaration().Throws; throws != nil && throws.Len() > 0 {
			var ts []string
			for _, t := range throws.Nodes {
				ts = append(ts, c.nodeSourceText(t))
			}
			signature += " throws " + strings.Join(ts, ", ")
		}
	}
	return signature, true
}

// InstantiatedSignatureOfCall renders the resolved overload's signature with the
// receiver's (and inferred method) type arguments substituted in, or "" if none.
func (c *Checker) InstantiatedSignatureOfCall(call *Node) (string, bool) {
	info := c.resolveCallInfo(call)
	if info == nil {
		return "", false
	}
	decl := info.Decl
	md := decl.AsMethodDeclaration()
	var methodSubst substMap
	if vars := c.methodTypeParameters(decl); len(vars) > 0 {
		methodSubst = c.inferMethodTypeArguments(decl, c.argTypes(call), info.ReceiverSubst, vars)
	}
	renderType := func(typeNode *Node) string {
		if typeNode == nil {
			return "?"
		}
		t := c.substitute(c.resolveType(typeNode, decl), info.ReceiverSubst)
		if methodSubst != nil {
			t = c.substitute(t, methodSubst)
		}
		if isError(t) {
			return c.nodeSourceText(typeNode)
		}
		return typeToString(t)
	}
	var params []string
	for _, p := range md.Parameters.Nodes {
		pd := p.AsParameter()
		name := ""
		if pd.Name != nil {
			name = " " + pd.Name.AsIdentifier().Text
		}
		varargs := ""
		if pd.IsVarArgs {
			varargs = "..."
		}
		params = append(params, renderType(pd.Type)+varargs+name)
	}
	return renderType(md.ReturnType) + " " + md.Name.AsIdentifier().Text + "(" + strings.Join(params, ", ") + ")", true
}

// ParameterLabelsOf returns the source text of each parameter of a declaration.
func (c *Checker) ParameterLabelsOf(declaration *Node) []string {
	if declaration.Kind != MethodDeclaration && declaration.Kind != ConstructorDeclaration {
		return nil
	}
	var out []string
	for _, p := range declarationParameters(declaration).Nodes {
		out = append(out, c.nodeSourceText(p))
	}
	return out
}

var javadocBlock = regexp.MustCompile(`(?s)/\*\*.*?\*/`)
var javadocLinePrefix = regexp.MustCompile(`^\s*\*? ?`)

func cleanJavadoc(raw string) string {
	body := raw[3 : len(raw)-2] // strip "/**" and "*/"
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(javadocLinePrefix.ReplaceAllString(line, ""), " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// GetDocumentationOfNode returns the Javadoc comment attached to a node, cleaned.
func (c *Checker) GetDocumentationOfNode(node *Node) (string, bool) {
	text := GetSourceFileOfNode(node).AsSourceFile().Text
	leading := text[node.Pos:skipTrivia(text, node.Pos)]
	blocks := javadocBlock.FindAllString(leading, -1)
	if len(blocks) == 0 {
		return "", false
	}
	doc := cleanJavadoc(blocks[len(blocks)-1])
	if doc == "" {
		return "", false
	}
	return doc, true
}

// GetDocumentation returns the Javadoc attached to a symbol's declaration.
func (c *Checker) GetDocumentation(symbol *Symbol) (string, bool) {
	declaration := c.declarationOf(symbol)
	if declaration == nil {
		return "", false
	}
	return c.GetDocumentationOfNode(declaration)
}

// --- @Override (JLS 9.6.4.4) -------------------------------------------------

func hasOverrideAnnotation(decl *Node) bool {
	mods := decl.AsMethodDeclaration().Modifiers
	if mods == nil {
		return false
	}
	for _, modifier := range mods.Nodes {
		if modifier.Kind != Annotation {
			continue
		}
		name := entityNameToString(modifier.AsAnnotation().TypeName)
		if name == "Override" || strings.HasSuffix(name, ".Override") {
			return true
		}
	}
	return false
}

func (c *Checker) overrideStatus(decl *Node) string {
	enclosing := enclosingTypeSymbol(decl)
	if enclosing == nil {
		return "unknown"
	}
	name := decl.AsMethodDeclaration().Name.AsIdentifier().Text
	seen := map[*Symbol]bool{}
	incomplete := false

	declaresMethod := func(typeSymbol *Symbol) bool {
		member := typeSymbol.Members[name]
		return member != nil && member.Flags&SymbolFlagsMethod != 0
	}
	var search func(typeSymbol *Symbol) bool
	search = func(typeSymbol *Symbol) bool {
		if seen[typeSymbol] {
			return false
		}
		seen[typeSymbol] = true
		declaration := c.declarationOf(typeSymbol)
		if declaration == nil {
			incomplete = true
			return false
		}
		for _, typeNode := range checkerSuperTypeNodes(declaration) {
			if typeNode.Kind != TypeReference {
				incomplete = true
				continue
			}
			superSymbol := ResolveTypeEntityName(typeNode.AsTypeReference().TypeName, declaration, c.program)
			if superSymbol == nil {
				incomplete = true
				continue
			}
			if declaresMethod(superSymbol) || search(superSymbol) {
				return true
			}
		}
		return false
	}

	if search(enclosing) {
		return "ok"
	}
	objectSymbol := c.program.GetGlobalIndex().GetType("java.lang.Object")
	if objectSymbol == nil {
		return "unknown"
	}
	if declaresMethod(objectSymbol) {
		return "ok"
	}
	if incomplete {
		return "unknown"
	}
	return "missing"
}

// --- switch-expression exhaustiveness over enums (JLS 14.11.1.1) -------------

func (c *Checker) missingEnumLabels(sw *Node) ([]string, bool) {
	swd := sw.AsSwitchExpression()
	selector := c.getTypeOfExpression(swd.Expression)
	if selector.Kind != TypeKindClass || selector.Symbol.Flags&SymbolFlagsEnum == 0 {
		return nil, false
	}
	declaration := c.declarationOf(selector.Symbol)
	if declaration == nil || declaration.Kind != EnumDeclaration {
		return nil, false
	}
	var constants []string
	for _, cn := range declaration.AsEnumDeclaration().EnumConstants.Nodes {
		constants = append(constants, cn.AsEnumConstantDeclaration().Name.AsIdentifier().Text)
	}
	if len(constants) == 0 {
		return nil, false
	}
	covered := map[string]bool{}
	for _, clause := range swd.Clauses.Nodes {
		cd := clause.AsSwitchClause()
		if cd.IsDefault || cd.Guard != nil {
			return nil, false
		}
		if cd.Labels != nil {
			for _, label := range cd.Labels.Nodes {
				if label.Kind != Identifier {
					return nil, false
				}
				covered[label.AsIdentifier().Text] = true
			}
		}
	}
	var missing []string
	for _, cn := range constants {
		if !covered[cn] {
			missing = append(missing, cn)
		}
	}
	return missing, true
}

// --- unused imports ----------------------------------------------------------

// FindUnusedImports returns single-type imports whose simple name never appears
// in the file body (plus exact duplicate imports).
func FindUnusedImports(sourceFile *Node) []*Node {
	data := sourceFile.AsSourceFile()
	if data.Imports == nil || data.Imports.Len() == 0 {
		return nil
	}
	used := map[string]bool{}
	var collect func(node *Node)
	collect = func(node *Node) {
		if node.Kind == Identifier {
			used[node.AsIdentifier().Text] = true
		}
		node.ForEachChild(func(child *Node) bool {
			collect(child)
			return false
		})
	}
	for _, statement := range data.Statements.Nodes {
		collect(statement)
	}

	var unused []*Node
	seen := map[string]bool{}
	for _, imp := range data.Imports.Nodes {
		d := imp.AsImportDeclaration()
		fqn := entityNameToString(d.Name)
		key := ""
		if d.IsStatic {
			key = "static "
		}
		key += fqn
		if d.IsOnDemand {
			key += ".*"
		}
		if seen[key] {
			unused = append(unused, imp)
			continue
		}
		seen[key] = true
		if d.IsOnDemand {
			continue
		}
		simple := fqn
		if dot := strings.LastIndex(fqn, "."); dot >= 0 {
			simple = fqn[dot+1:]
		}
		if !used[simple] {
			unused = append(unused, imp)
		}
	}
	return unused
}

// --- semantic diagnostics ----------------------------------------------------

// GetSemanticDiagnostics returns high-precision semantic diagnostics.
// deprecatedUseAt returns a use of a @Deprecated method (a call) or type (a type
// reference) at node, with the referenced name's span and the annotation's
// since/forRemoval; ok is false otherwise. text is the source file's text.
func (c *Checker) deprecatedUseAt(node *Node, text string) (DeprecatedUse, bool) {
	switch node.Kind {
	case CallExpression:
		info := c.resolveCallInfo(node)
		if info == nil || info.Decl == nil {
			return DeprecatedUse{}, false
		}
		dep, ok := readDeprecation(info.Decl)
		if !ok {
			return DeprecatedUse{}, false
		}
		callee := node.AsCallExpression().Expression
		var nameNode *Node
		switch callee.Kind {
		case PropertyAccessExpression:
			nameNode = callee.AsPropertyAccessExpression().Name
		case Identifier:
			nameNode = callee
		}
		if nameNode == nil {
			return DeprecatedUse{}, false
		}
		return DeprecatedUse{Pos: skipTrivia(text, nameNode.Pos), End: nameNode.End,
			Name: nameNode.AsIdentifier().Text, Kind: "method",
			Since: dep.Since, HasSince: dep.HasSince, ForRemoval: dep.ForRemoval}, true
	case TypeReference:
		ref := node.AsTypeReference()
		sym := ResolveTypeEntityName(ref.TypeName, node, c.program)
		if sym == nil {
			return DeprecatedUse{}, false
		}
		dep, ok := readDeprecation(c.declarationOf(sym))
		if !ok {
			return DeprecatedUse{}, false
		}
		return DeprecatedUse{Pos: skipTrivia(text, ref.TypeName.Pos), End: ref.TypeName.End,
			Name: entityNameToString(ref.TypeName), Kind: "type",
			Since: dep.Since, HasSince: dep.HasSince, ForRemoval: dep.ForRemoval}, true
	case PropertyAccessExpression:
		access := node.AsPropertyAccessExpression()
		// A call's callee (obj.m()) is the CallExpression's method use, reported
		// above - don't also report it as a field access here.
		if node.Parent != nil && node.Parent.Kind == CallExpression &&
			node.Parent.AsCallExpression().Expression == node {
			return DeprecatedUse{}, false
		}
		sym := c.ResolveName(access.Name)
		if sym == nil || sym.Flags&SymbolFlagsField == 0 {
			return DeprecatedUse{}, false
		}
		// A field's declaration node is the VariableDeclarator; @Deprecated sits on
		// the enclosing FieldDeclaration, so read the annotation from there.
		fieldDecl := c.declarationOf(sym)
		if fieldDecl != nil && fieldDecl.Kind == VariableDeclarator {
			fieldDecl = fieldDecl.Parent
		}
		dep, ok := readDeprecation(fieldDecl)
		if !ok {
			return DeprecatedUse{}, false
		}
		return DeprecatedUse{Pos: skipTrivia(text, access.Name.Pos), End: access.Name.End,
			Name: access.Name.AsIdentifier().Text, Kind: "field",
			Since: dep.Since, HasSince: dep.HasSince, ForRemoval: dep.ForRemoval}, true
	}
	return DeprecatedUse{}, false
}

// GetDeprecatedUses returns every use of a deprecated method or type in a
// (cleanly parsed) source file.
func (c *Checker) GetDeprecatedUses(sourceFile *Node) []DeprecatedUse {
	data := sourceFile.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}
	var uses []DeprecatedUse
	var walk func(node *Node)
	walk = func(node *Node) {
		if use, ok := c.deprecatedUseAt(node, data.Text); ok {
			uses = append(uses, use)
		}
		node.ForEachChild(func(child *Node) bool {
			walk(child)
			return false
		})
	}
	walk(sourceFile)
	return uses
}

func (c *Checker) GetSemanticDiagnostics(sourceFile *Node) []Diagnostic {
	data := sourceFile.AsSourceFile()
	var diagnostics []Diagnostic
	cleanParse := len(data.ParseDiagnostics) == 0

	narrowingRange := map[string][2]int64{
		"byte":  {-128, 127},
		"short": {-32768, 32767},
		"char":  {0, 65535},
	}
	checkPrimitiveAssignment := func(valueNode *Node, value, target string) {
		if value == target || primitiveWidens(value, target) {
			return
		}
		rng, hasRange := narrowingRange[target]
		constNarrowable := hasRange && (value == "byte" || value == "short" || value == "char" || value == "int")
		if constNarrowable {
			folded := FoldConstant(valueNode)
			if folded == nil {
				return
			}
			if folded.Kind == ConstInt && folded.Int >= rng[0] && folded.Int <= rng[1] {
				return
			}
		}
		diagnostics = append(diagnostics, CreateDiagnostic(valueNode.Pos, valueNode.End-valueNode.Pos,
			Diagnostics.IncompatibleTypes01, value, target))
	}

	checkAssignment := func(valueNode *Node, targetType *Type) {
		if targetType.Kind == TypeKindPrimitive && targetType.Name == "void" {
			return
		}
		if !isConcrete(targetType) {
			return
		}
		valueType := c.getTypeOfExpression(valueNode)
		if !isConcrete(valueType) {
			return
		}
		if valueNode.Kind == CallExpression {
			var declarations []*Node
			if resolved := c.ResolveCall(valueNode); resolved != nil && resolved.Symbol != nil {
				declarations = resolved.Symbol.Declarations
			}
			if len(declarations) > 1 {
				return
			}
			if len(declarations) == 1 && declarations[0].Kind == MethodDeclaration {
				parameters := declarations[0].AsMethodDeclaration().Parameters
				argc := nodeArrayLen(valueNode.AsCallExpression().Arguments)
				accepts := false
				if last := lastNode(parameters); last != nil && last.AsParameter().IsVarArgs {
					accepts = argc >= parameters.Len()-1
				} else {
					accepts = argc == parameters.Len()
				}
				if !accepts {
					return
				}
			}
		}
		if targetType.Kind == TypeKindPrimitive && valueType.Kind == TypeKindPrimitive {
			checkPrimitiveAssignment(valueNode, valueType.Name, targetType.Name)
			return
		}
		oneIsPrimitive := (targetType.Kind == TypeKindPrimitive) != (valueType.Kind == TypeKindPrimitive)
		if !oneIsPrimitive {
			return
		}
		if !c.isAssignableTo(valueType, targetType, true) {
			diagnostics = append(diagnostics, CreateDiagnostic(valueNode.Pos, valueNode.End-valueNode.Pos,
				Diagnostics.IncompatibleTypes01, typeToString(valueType), typeToString(targetType)))
		}
	}

	// --- call/creation arity (JLS 15.12.2.1) ---
	arityAccepts := func(parameters *NodeArray, argc int) bool {
		if last := lastNode(parameters); last != nil && last.AsParameter().IsVarArgs {
			return argc >= parameters.Len()-1
		}
		return argc == nodeArrayLen(parameters)
	}
	isProjectSymbol := func(typeSymbol *Symbol) bool {
		declaration := c.declarationOf(typeSymbol)
		if declaration == nil {
			return false
		}
		fileName := GetSourceFileOfNode(declaration).AsSourceFile().FileName
		return !strings.HasPrefix(fileName, "jdk:") && !strings.HasPrefix(fileName, "classpath:")
	}
	projectOverloads := func(start *Symbol, name string) []*Node {
		var overloads []*Node
		seen := map[*Symbol]bool{}
		queue := []*Symbol{start}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			if seen[current] {
				continue
			}
			seen[current] = true
			if current == c.program.GetGlobalIndex().GetType("java.lang.Object") {
				continue
			}
			if !isProjectSymbol(current) {
				return nil
			}
			if member := current.Members[name]; member != nil {
				for _, declaration := range member.Declarations {
					if declaration.Kind == MethodDeclaration {
						overloads = append(overloads, declaration)
					}
				}
			}
			declaration := c.declarationOf(current)
			if declaration == nil {
				return nil
			}
			for _, typeNode := range checkerSuperTypeNodes(declaration) {
				if typeNode.Kind != TypeReference {
					return nil
				}
				superSymbol := ResolveTypeEntityName(typeNode.AsTypeReference().TypeName, declaration, c.program)
				if superSymbol == nil {
					return nil
				}
				queue = append(queue, superSymbol)
			}
		}
		if len(overloads) > 0 {
			return overloads
		}
		return nil
	}
	describeArities := func(parameterLists []*NodeArray) string {
		set := map[string]bool{}
		for _, parameters := range parameterLists {
			if last := lastNode(parameters); last != nil && last.AsParameter().IsVarArgs {
				set[strconv.Itoa(parameters.Len()-1)+"+"] = true
			} else {
				set[strconv.Itoa(nodeArrayLen(parameters))] = true
			}
		}
		var arities []string
		for a := range set {
			arities = append(arities, a)
		}
		sort.Slice(arities, func(i, j int) bool {
			return ariticInt(arities[i]) < ariticInt(arities[j])
		})
		return strings.Join(arities, " or ")
	}
	reportArity := func(after *Node, end int, expected string, argc int) {
		length := end - after.End
		if length < 1 {
			length = 1
		}
		diagnostics = append(diagnostics, CreateDiagnostic(after.End, length,
			Diagnostics.InvalidNumberOfArgumentsExpected0Got1, expected, strconv.Itoa(argc)))
	}

	// jspecify nullness (nikeee/cappu#25). Purely syntactic: the value is treated
	// as possibly-null only when it is the `null` literal or a use of a declared
	// @Nullable method/field/variable; no flow narrowing.
	// The value/target nullness both come from the type model: a value is
	// possibly-null when its type is the `null` literal or carries a @Nullable facet
	// (incl. a @Nullable generic element via substitution); a target is non-null when
	// its type carries @NonNull.
	valueMayBeNull := func(node *Node) bool {
		t := c.getTypeOfExpression(node)
		return t.Kind == TypeKindNull || nullnessOf(t) == NullnessNullable
	}
	checkNullness := func(valueNode *Node, targetType *Type, name string) {
		if c.nullness == nil {
			return
		}
		if nullnessOf(targetType) != NullnessNonNull {
			return
		}
		if !valueMayBeNull(valueNode) {
			return
		}
		diagnostics = append(diagnostics, CreateDiagnostic(valueNode.Pos, valueNode.End-valueNode.Pos,
			Diagnostics.PossiblyNullValueAssignedToNonNull0, name))
	}

	// Dereferencing a possibly-null receiver (x.foo(), x.field, x[i]). Flow-aware:
	// a receiver narrowed non-null by a preceding guard is not flagged.
	checkDereference := func(receiver *Node) {
		if c.nullness == nil || receiver.Kind == SuperExpression || !valueMayBeNull(receiver) {
			return
		}
		text := GetSourceFileOfNode(receiver).AsSourceFile().Text
		start := skipTrivia(text, receiver.Pos)
		diagnostics = append(diagnostics, CreateDiagnostic(start, receiver.End-start,
			Diagnostics.DereferenceOfPossiblyNullValue0, text[start:receiver.End]))
	}

	// A switch on a null selector throws NPE - except under JEP 441, where a
	// `case null` label handles it. The selector is dereferenced only when no
	// such label is present.
	switchHasNullCase := func(clauses *NodeArray) bool {
		if clauses == nil {
			return false
		}
		for _, clause := range clauses.Nodes {
			labels := clause.AsSwitchClause().Labels
			if labels == nil {
				continue
			}
			for _, l := range labels.Nodes {
				if l.Kind == NullKeyword {
					return true
				}
			}
		}
		return false
	}
	checkSwitchSelector := func(expr *Node, clauses *NodeArray) {
		if !switchHasNullCase(clauses) {
			checkDereference(expr)
		}
	}

	// Argument nullness against a resolved signature: each parameter type is
	// instantiated with subst (the receiver's / created type's type arguments), so a
	// null into the non-null element of List<@NonNull String>.add(E) is caught.
	checkParamNullness := func(args *NodeArray, parameters *NodeArray, subst substMap) {
		if c.nullness == nil {
			return
		}
		fixed := nodeArrayLen(parameters)
		if last := lastNode(parameters); last != nil && last.AsParameter().IsVarArgs {
			fixed = parameters.Len() - 1
		}
		n := nodeArrayLen(args)
		if fixed < n {
			n = fixed
		}
		for i := 0; i < n; i++ {
			p := parameters.Nodes[i]
			if p.Symbol == nil {
				continue
			}
			targetType := c.substitute(c.getTypeOfSymbol(p.Symbol), subst)
			pd := p.AsParameter()
			pname := fmt.Sprintf("parameter %d", i+1)
			if pd.Name != nil {
				pname = pd.Name.AsIdentifier().Text
			}
			checkNullness(args.Nodes[i], targetType, pname)
		}
	}
	checkCallNullness := func(call *Node) {
		if c.nullness == nil {
			return
		}
		if info := c.resolveCallInfo(call); info != nil {
			checkParamNullness(call.AsCallExpression().Arguments, info.Decl.AsMethodDeclaration().Parameters, info.ReceiverSubst)
		}
	}

	checkArgumentTypes := func(args *NodeArray, parameters *NodeArray) {
		fixed := nodeArrayLen(parameters)
		if last := lastNode(parameters); last != nil && last.AsParameter().IsVarArgs {
			fixed = parameters.Len() - 1
		}
		n := nodeArrayLen(args)
		if fixed < n {
			n = fixed
		}
		for i := 0; i < n; i++ {
			if parameters.Nodes[i].Symbol == nil {
				continue
			}
			checkAssignment(args.Nodes[i], c.getTypeOfSymbol(parameters.Nodes[i].Symbol))
		}
	}

	checkCallArity := func(call *Node) {
		callee := call.AsCallExpression().Expression
		var nameNode *Node
		switch callee.Kind {
		case Identifier:
			nameNode = callee
		case PropertyAccessExpression:
			nameNode = callee.AsPropertyAccessExpression().Name
		default:
			return
		}
		symbol := c.ResolveName(nameNode)
		if symbol == nil || symbol.Flags&SymbolFlagsMethod == 0 {
			return
		}
		var start *Symbol
		if callee.Kind == PropertyAccessExpression {
			receiver := c.getTypeOfExpression(callee.AsPropertyAccessExpression().Expression)
			if receiver.Kind == TypeKindClass {
				start = receiver.Symbol
			}
		} else {
			start = enclosingTypeSymbol(call)
		}
		if start == nil {
			return
		}
		overloads := projectOverloads(start, nameNode.AsIdentifier().Text)
		if overloads == nil {
			return
		}
		argc := nodeArrayLen(call.AsCallExpression().Arguments)
		var applicable []*Node
		for _, o := range overloads {
			if arityAccepts(o.AsMethodDeclaration().Parameters, argc) {
				applicable = append(applicable, o)
			}
		}
		if len(applicable) == 0 {
			var lists []*NodeArray
			for _, o := range overloads {
				lists = append(lists, o.AsMethodDeclaration().Parameters)
			}
			reportArity(callee, call.End, describeArities(lists), argc)
			return
		}
		if len(applicable) == 1 {
			checkArgumentTypes(call.AsCallExpression().Arguments, applicable[0].AsMethodDeclaration().Parameters)
		}
	}

	checkCreationArity := func(node *Node) {
		creation := node.AsObjectCreationExpression()
		if creation.Type.Kind != TypeReference {
			return
		}
		symbol := ResolveTypeEntityName(creation.Type.AsTypeReference().TypeName, node, c.program)
		if symbol == nil || !isProjectSymbol(symbol) {
			return
		}
		declaration := c.declarationOf(symbol)
		if declaration == nil {
			return
		}
		argc := nodeArrayLen(creation.Arguments)
		if declaration.Kind == ClassDeclaration {
			var ctors []*Node
			for _, m := range declaration.AsClassDeclaration().Members.Nodes {
				if m.Kind == ConstructorDeclaration {
					ctors = append(ctors, m)
				}
			}
			var applicable []*Node
			for _, ct := range ctors {
				if arityAccepts(ct.AsConstructorDeclaration().Parameters, argc) {
					applicable = append(applicable, ct)
				}
			}
			ok := false
			if len(ctors) == 0 {
				ok = argc == 0
			} else {
				ok = len(applicable) > 0
			}
			if !ok {
				end := node.End
				if creation.ClassBody != nil {
					if last := lastNode(creation.Arguments); last != nil {
						end = last.End
					} else {
						end = creation.Type.End
					}
				}
				expected := "0"
				if len(ctors) > 0 {
					var lists []*NodeArray
					for _, ct := range ctors {
						lists = append(lists, ct.AsConstructorDeclaration().Parameters)
					}
					expected = describeArities(lists)
				}
				reportArity(creation.Type, end, expected, argc)
			} else if len(applicable) == 1 && creation.ClassBody == nil {
				checkArgumentTypes(creation.Arguments, applicable[0].AsConstructorDeclaration().Parameters)
				var subst substMap
				if created := c.resolveType(creation.Type, node); created.Kind == TypeKindClass {
					subst = c.substitutionFor(symbol, created.TypeArguments)
				}
				checkParamNullness(creation.Arguments, applicable[0].AsConstructorDeclaration().Parameters, subst)
			}
		} else if declaration.Kind == RecordDeclaration {
			record := declaration.AsRecordDeclaration()
			hasDeclaredCtor := false
			for _, m := range record.Members.Nodes {
				if m.Kind == ConstructorDeclaration {
					hasDeclaredCtor = true
					break
				}
			}
			if hasDeclaredCtor {
				return
			}
			if argc != record.RecordComponents.Len() {
				reportArity(creation.Type, node.End, strconv.Itoa(record.RecordComponents.Len()), argc)
			}
			// The canonical constructor's parameters are the record components, so a
			// possibly-null argument into a non-null component is caught here. Each
			// component carries its own nullness (annotation or @NullMarked default).
			// A dedicated loop (not checkParamNullness) since components are not Parameters.
			if c.nullness != nil {
				var subst substMap
				if created := c.resolveType(creation.Type, node); created.Kind == TypeKindClass {
					subst = c.substitutionFor(symbol, created.TypeArguments)
				}
				comps := record.RecordComponents
				fixed := comps.Len()
				if last := lastNode(comps); last != nil && last.AsRecordComponent().IsVarArgs {
					fixed = comps.Len() - 1
				}
				n := nodeArrayLen(creation.Arguments)
				if fixed < n {
					n = fixed
				}
				for i := 0; i < n; i++ {
					comp := comps.Nodes[i]
					if comp.Symbol == nil {
						continue
					}
					targetType := c.substitute(c.getTypeOfSymbol(comp.Symbol), subst)
					checkNullness(creation.Arguments.Nodes[i], targetType, comp.AsRecordComponent().Name.AsIdentifier().Text)
				}
			}
		}
	}

	var visit func(node *Node)
	visit = func(node *Node) {
		switch node.Kind {
		case VariableDeclarator:
			d := node.AsVariableDeclarator()
			if d.Initializer != nil && node.Symbol != nil && d.Initializer.Kind != ArrayInitializer {
				checkAssignment(d.Initializer, c.getTypeOfSymbol(node.Symbol))
				checkNullness(d.Initializer, c.getTypeOfSymbol(node.Symbol), d.Name.AsIdentifier().Text)
			} else if d.Initializer != nil && d.Initializer.Kind == ArrayInitializer && node.Symbol != nil {
				// Each element initializes a slot of the array, so a null element into a
				// non-null element type is flagged (nikeee/cappu#25).
				if t := c.getTypeOfSymbol(node.Symbol); t.Kind == TypeKindArray {
					for _, el := range d.Initializer.AsArrayInitializer().Elements.Nodes {
						if el.Kind != ArrayInitializer {
							checkNullness(el, t.ElementType, d.Name.AsIdentifier().Text)
						}
					}
				}
			}
		case AssignmentExpression:
			a := node.AsAssignmentExpression()
			if a.OperatorToken == EqualsToken {
				checkAssignment(a.Right, c.getTypeOfExpression(a.Left))
				var leftName *Node
				switch a.Left.Kind {
				case Identifier:
					leftName = a.Left
				case PropertyAccessExpression:
					leftName = a.Left.AsPropertyAccessExpression().Name
				}
				if leftName != nil {
					checkNullness(a.Right, c.getTypeOfExpression(a.Left), leftName.AsIdentifier().Text)
				}
			}
		case CallExpression:
			if cleanParse {
				checkCallArity(node)
				checkCallNullness(node)
			}
		case ObjectCreationExpression:
			if cleanParse {
				checkCreationArity(node)
			}
		case ReturnStatement:
			r := node.AsReturnStatement()
			if r.Expression != nil {
				if ret := c.enclosingReturnType(node); ret != nil {
					checkAssignment(r.Expression, ret)
				}
				// The return targets the nearest enclosing function: a method's declared
				// return, or (inside a lambda) the SAM's instantiated return.
				fn := node
				for fn != nil && fn.Kind != MethodDeclaration && fn.Kind != LambdaExpression {
					fn = fn.Parent
				}
				if fn != nil && fn.Kind == MethodDeclaration && fn.Symbol != nil {
					checkNullness(r.Expression, c.getTypeOfSymbol(fn.Symbol), fn.AsMethodDeclaration().Name.AsIdentifier().Text)
				} else if fn != nil && fn.Kind == LambdaExpression {
					if info := c.GetLambdaInfo(fn); info != nil {
						checkNullness(r.Expression, info.InstReturn, string(info.SamName))
					}
				}
			}
		case LambdaExpression:
			// An expression-bodied lambda (() -> e) implicitly returns e, so check it
			// against the SAM's return nullness (block bodies go through ReturnStatement).
			lam := node.AsLambdaExpression()
			if c.nullness != nil && lam.Body.Kind != Block {
				if info := c.GetLambdaInfo(node); info != nil {
					checkNullness(lam.Body, info.InstReturn, string(info.SamName))
				}
			}
		case MethodDeclaration:
			if hasOverrideAnnotation(node) && c.overrideStatus(node) == "missing" {
				name := node.AsMethodDeclaration().Name
				diagnostics = append(diagnostics, CreateDiagnostic(name.Pos, name.End-name.Pos,
					Diagnostics.MethodDoesNotOverrideASupertypeMethod))
			}
		case ElementAccessExpression:
			checkDereference(node.AsElementAccessExpression().Expression)
		// Implicit-dereference positions that unconditionally NPE on null: the
		// thrown value, the synchronized lock, and the iterated collection.
		case ThrowStatement:
			checkDereference(node.AsThrowStatement().Expression)
		case SynchronizedStatement:
			checkDereference(node.AsSynchronizedStatement().Expression)
		case ForEachStatement:
			fe := node.AsForEachStatement()
			checkDereference(fe.Expression)
			// The loop binds each element to the variable; a nullable element into a
			// non-null (e.g. explicitly typed) loop variable is an unsafe binding.
			if c.nullness != nil && fe.Parameter.Symbol != nil {
				elem := c.elementTypeOf(c.getTypeOfExpression(fe.Expression))
				varType := c.getTypeOfSymbol(fe.Parameter.Symbol)
				if nullnessOf(varType) == NullnessNonNull &&
					(elem.Kind == TypeKindNull || nullnessOf(elem) == NullnessNullable) {
					text := GetSourceFileOfNode(fe.Parameter).AsSourceFile().Text
					start := skipTrivia(text, fe.Parameter.Pos)
					name := "variable"
					if pn := fe.Parameter.AsParameter().Name; pn != nil {
						name = pn.AsIdentifier().Text
					}
					diagnostics = append(diagnostics, CreateDiagnostic(start, fe.Parameter.End-start,
						Diagnostics.PossiblyNullValueAssignedToNonNull0, name))
				}
			}
		case PropertyAccessExpression:
			access := node.AsPropertyAccessExpression()
			checkDereference(access.Expression)
			if access.Expression.Kind != SuperExpression {
				receiver := c.getTypeOfExpression(access.Expression)
				if receiver.Kind == TypeKindClass && c.isClosedType(receiver) &&
					c.lookupTypedMember(receiver, access.Name.AsIdentifier().Text, nil) == nil &&
					!isSynthesizedEnumMember(receiver, access.Name.AsIdentifier().Text) {
					text := GetSourceFileOfNode(access.Name).AsSourceFile().Text
					start := skipTrivia(text, access.Name.Pos)
					diagnostics = append(diagnostics, CreateDiagnostic(start, access.Name.End-start,
						Diagnostics.CannotResolveMember0In1, access.Name.AsIdentifier().Text, typeToString(receiver)))
				}
			}
		case SwitchStatement:
			s := node.AsSwitchStatement()
			checkSwitchSelector(s.Expression, s.Clauses)
		case SwitchExpression:
			se := node.AsSwitchExpression()
			checkSwitchSelector(se.Expression, se.Clauses)
			if missing, ok := c.missingEnumLabels(node); ok && len(missing) > 0 {
				expr := se.Expression
				diagnostics = append(diagnostics, CreateDiagnostic(expr.Pos, expr.End-expr.Pos,
					Diagnostics.SwitchExpressionNotExhaustive0, typeToString(c.getTypeOfExpression(expr))))
			}
		}
		node.ForEachChild(func(child *Node) bool {
			visit(child)
			return false
		})
	}
	visit(sourceFile)

	if cleanParse {
		for _, imp := range FindUnusedImports(sourceFile) {
			start := skipTrivia(data.Text, imp.Pos)
			diagnostics = append(diagnostics, CreateDiagnostic(start, imp.End-start,
				Diagnostics.UnusedImport0, entityNameToString(imp.AsImportDeclaration().Name)))
		}
	}
	// Uses of @Deprecated methods/types (warnings).
	for _, u := range c.GetDeprecatedUses(sourceFile) {
		diagnostics = append(diagnostics, CreateDiagnostic(u.Pos, u.End-u.Pos,
			Diagnostics.Deprecated0, u.Name))
	}
	return diagnostics
}

// ariticInt parses the leading integer of an arity label ("2" or "1+").
func ariticInt(s string) int {
	s = strings.TrimSuffix(s, "+")
	n, _ := strconv.Atoi(s)
	return n
}

func lastNode(arr *NodeArray) *Node {
	if arr == nil || len(arr.Nodes) == 0 {
		return nil
	}
	return arr.Nodes[len(arr.Nodes)-1]
}

func declarationParameters(declaration *Node) *NodeArray {
	switch declaration.Kind {
	case MethodDeclaration:
		return declaration.AsMethodDeclaration().Parameters
	case ConstructorDeclaration:
		return declaration.AsConstructorDeclaration().Parameters
	default:
		return &NodeArray{}
	}
}

func declarationName(declaration *Node) *Node {
	switch declaration.Kind {
	case MethodDeclaration:
		return declaration.AsMethodDeclaration().Name
	case ConstructorDeclaration:
		return declaration.AsConstructorDeclaration().Name
	default:
		return nil
	}
}
