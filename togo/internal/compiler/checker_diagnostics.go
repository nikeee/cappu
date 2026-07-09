package compiler

// Checker hover/signature rendering and high-precision semantic diagnostics.
// Port of the remaining parts of src/compiler/checker.ts.

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
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
		dep, ok := ReadDeprecation(info.Decl)
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
		dep, ok := ReadDeprecation(c.declarationOf(sym))
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
		dep, ok := ReadDeprecation(fieldDecl)
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

// getFieldsThatCanBeFinal returns the declarators of private fields that are
// never reassigned - assigned only by their initializer, or exactly once in
// every constructor - and can therefore be declared `final` (nikeee/cappu#38).
// The `private` gate keeps a whole-file analysis sound: every legal write site
// is in this compilation unit. When a write cannot be classified, stay silent -
// the suggestion must never propose a `final` that fails to compile.
// Port of getFieldsThatCanBeFinal in src/compiler/checker.ts.
func (c *Checker) getFieldsThatCanBeFinal(sourceFile *Node) []*Node {
	data := sourceFile.AsSourceFile()
	if len(data.ParseDiagnostics) > 0 {
		return nil
	}

	var fields []*Node                        // candidate FieldDeclarations, in source order
	writes := map[*Symbol][]*Node{}           // field symbol -> assignment target nodes
	unresolvedWriteNames := map[string]bool{} // disqualify by name when unresolvable

	recordWrite := func(target *Node) {
		// Only a bare name or a member access writes a variable binding
		// (a[i] = x writes the array element, not the field itself).
		var name *Node
		switch target.Kind {
		case Identifier:
			name = target
		case PropertyAccessExpression:
			name = target.AsPropertyAccessExpression().Name
		default:
			return
		}
		sym := c.ResolveName(name)
		if sym == nil {
			unresolvedWriteNames[name.AsIdentifier().Text] = true
			return
		}
		writes[sym] = append(writes[sym], target)
	}

	var walk func(node *Node)
	walk = func(node *Node) {
		switch node.Kind {
		case FieldDeclaration:
			fd := node.AsFieldDeclaration()
			if hasModifierKind(fd.Modifiers, PrivateKeyword) &&
				!hasModifierKind(fd.Modifiers, FinalKeyword) &&
				!hasModifierKind(fd.Modifiers, VolatileKeyword) {
				fields = append(fields, node)
			}
		case AssignmentExpression:
			recordWrite(node.AsAssignmentExpression().Left)
		case PrefixUnaryExpression:
			u := node.AsPrefixUnaryExpression()
			if u.Operator == PlusPlusToken || u.Operator == MinusMinusToken {
				recordWrite(u.Operand)
			}
		case PostfixUnaryExpression:
			u := node.AsPostfixUnaryExpression()
			if u.Operator == PlusPlusToken || u.Operator == MinusMinusToken {
				recordWrite(u.Operand)
			}
		}
		node.ForEachChild(func(child *Node) bool {
			walk(child)
			return false
		})
	}
	walk(sourceFile)

	// A write compatible with a blank final: a plain `=` to a bare name or
	// `this.name`, forming a whole top-level statement of a constructor body.
	ctorOfTopLevelWrite := func(target *Node) *Node {
		if target.Kind == PropertyAccessExpression &&
			target.AsPropertyAccessExpression().Expression.Kind != ThisExpression {
			return nil // other.x = ... is never a legal blank-final assignment
		}
		assignment := target.Parent
		if assignment == nil || assignment.Kind != AssignmentExpression {
			return nil
		}
		a := assignment.AsAssignmentExpression()
		if a.Left != target || a.OperatorToken != EqualsToken {
			return nil
		}
		statement := assignment.Parent
		if statement == nil || statement.Kind != ExpressionStatement {
			return nil
		}
		block := statement.Parent
		if block == nil || block.Kind != Block {
			return nil
		}
		ctor := block.Parent
		if ctor == nil || ctor.Kind != ConstructorDeclaration {
			return nil
		}
		return ctor
	}

	delegatesToThis := func(ctor *Node) bool {
		stmts := arrayNodes(ctor.AsConstructorDeclaration().Body.AsBlock().Statements)
		if len(stmts) == 0 || stmts[0].Kind != ExpressionStatement {
			return false
		}
		expr := stmts[0].AsExpressionStatement().Expression
		return expr.Kind == CallExpression && expr.AsCallExpression().Expression.Kind == ThisExpression
	}

	var containsReturn func(node *Node) bool
	containsReturn = func(node *Node) bool {
		if node.Kind == ReturnStatement {
			return true
		}
		found := false
		node.ForEachChild(func(child *Node) bool {
			if containsReturn(child) {
				found = true
				return true
			}
			return false
		})
		return found
	}

	qualifies := func(sym *Symbol, field, declarator *Node, isStatic bool) bool {
		if unresolvedWriteNames[declarator.AsVariableDeclarator().Name.AsIdentifier().Text] {
			return false
		}
		fieldWrites := writes[sym]
		if declarator.AsVariableDeclarator().Initializer != nil {
			return len(fieldWrites) == 0
		}
		// Blank field: prove exactly one assignment per construction. Static
		// initializer blocks are out of scope - initializer-assigned only.
		if isStatic {
			return false
		}
		var ctors []*Node
		for _, m := range membersOf(field.Parent) {
			if m.Kind == ConstructorDeclaration {
				ctors = append(ctors, m)
			}
		}
		if len(ctors) == 0 {
			return false
		}
		perCtor := map[*Node]int{}
		for _, write := range fieldWrites {
			ctor := ctorOfTopLevelWrite(write)
			if ctor == nil || !slices.Contains(ctors, ctor) {
				return false
			}
			perCtor[ctor]++
		}
		for _, ctor := range ctors {
			assignments := perCtor[ctor]
			// A this(...) delegate already assigned the field; assigning again would
			// double-assign. An early return could leave a path unassigned - order-aware
			// flow analysis is not worth it here, so any return disqualifies.
			if delegatesToThis(ctor) {
				if assignments != 0 {
					return false
				}
			} else if assignments != 1 || containsReturn(ctor.AsConstructorDeclaration().Body) {
				return false
			}
		}
		return true
	}

	// A multi-declarator field is all-or-nothing: one `final` covers every
	// declarator, so a single reassigned one silences the whole declaration.
	var result []*Node
	for _, field := range fields {
		fd := field.AsFieldDeclaration()
		isStatic := hasModifierKind(fd.Modifiers, StaticKeyword)
		declarators := arrayNodes(fd.Declarators)
		all := true
		for _, d := range declarators {
			sym := c.ResolveName(d.AsVariableDeclarator().Name)
			if sym == nil || !qualifies(sym, field, d, isStatic) {
				all = false
				break
			}
		}
		if all {
			result = append(result, declarators...)
		}
	}
	return result
}

// Lookup tables for GetSemanticDiagnostics, hoisted so they are not rebuilt per file.
var narrowingRange = map[string][2]int64{
	"byte":  {-128, 127},
	"short": {-32768, 32767},
	"char":  {0, 65535},
}
var formatMethods = map[string]bool{
	"java.lang.String#format":    true,
	"java.io.PrintStream#format": true,
	"java.io.PrintStream#printf": true,
	"java.io.PrintWriter#format": true,
	"java.io.PrintWriter#printf": true,
	"java.io.Console#format":     true,
	"java.io.Console#printf":     true,
	"java.util.Formatter#format": true,
}
var regexMethods = map[string]bool{
	"java.util.regex.Pattern#compile": true,
	"java.util.regex.Pattern#matches": true,
	"java.lang.String#matches":        true,
	"java.lang.String#split":          true,
	"java.lang.String#replaceAll":     true,
	"java.lang.String#replaceFirst":   true,
}
var parseMethods = map[string]string{
	"java.lang.Integer#parseInt": "int",
	"java.lang.Integer#valueOf":  "int",
	"java.lang.Long#parseLong":   "long",
	"java.lang.Long#valueOf":     "long",
	"java.lang.Short#parseShort": "short",
	"java.lang.Short#valueOf":    "short",
	"java.lang.Byte#parseByte":   "byte",
	"java.lang.Byte#valueOf":     "byte",
}

func (c *Checker) GetSemanticDiagnostics(sourceFile *Node) []Diagnostic {
	data := sourceFile.AsSourceFile()
	var diagnostics []Diagnostic
	cleanParse := len(data.ParseDiagnostics) == 0

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
		slices.SortFunc(arities, func(a, b string) int {
			return cmp.Compare(arityInt(a), arityInt(b))
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

	// --- format-string arity/type check (String.format & friends) -----------
	// Port of checkFormatCall in src/compiler/checker.ts. The java.util.Formatter
	// %-syntax methods take (..., String, Object...), so a wrong argument count or
	// type is arity-valid against the declaration yet throws at runtime. When the
	// format string is a literal we parse its specifiers and warn - staying silent
	// on anything unprovable.
	argTypeDescriptor := func(t *Type) (ArgTypeDescriptor, bool) {
		switch t.Kind {
		case TypeKindPrimitive:
			return ArgTypeDescriptor{Primitive: t.Name}, true
		case TypeKindClass:
			return ArgTypeDescriptor{Fqn: c.fqnOf(t)}, true
		default:
			return ArgTypeDescriptor{}, false // array / type-variable / null / error
		}
	}
	checkFormatCall := func(call *Node) {
		callee := call.AsCallExpression().Expression
		if callee.Kind != PropertyAccessExpression {
			return
		}
		access := callee.AsPropertyAccessExpression()
		receiver := c.receiverClassType(c.getTypeOfExpression(access.Expression), 0)
		if receiver == nil {
			return
		}
		methodName := access.Name.AsIdentifier().Text
		key := c.fqnOf(receiver) + "#" + methodName
		fmtIsReceiver := c.fqnOf(receiver) == "java.lang.String" && methodName == "formatted"
		if !formatMethods[key] && !fmtIsReceiver {
			return
		}

		// Locate the format-string node and where the format arguments begin.
		var fmtNode *Node
		var argsStart int
		args := call.AsCallExpression().Arguments
		if fmtIsReceiver {
			fmtNode = access.Expression // "text".formatted(args)
			argsStart = 0
		} else {
			// The format string is the fixed parameter right before the Object...
			// varargs; the resolved overload gives its position (handling the
			// Locale-first String.format overload with no special-casing).
			info := c.resolveCallInfo(call)
			if info == nil || info.Decl == nil {
				return
			}
			params := info.Decl.AsMethodDeclaration().Parameters
			last := lastNode(params)
			if last == nil || !last.AsParameter().IsVarArgs || params.Len() < 2 {
				return
			}
			fmtPos := params.Len() - 2
			if fmtPos >= nodeArrayLen(args) {
				return
			}
			fmtNode = args.Nodes[fmtPos]
			argsStart = params.Len() - 1
		}

		if fmtNode.Kind != StringLiteral && fmtNode.Kind != TextBlockLiteral {
			return // non-literal format string: cannot analyze
		}
		parsed, ok := ParseFormatString(fmtNode.AsLiteralExpression().Value)
		if !ok {
			return
		}

		provided := nodeArrayLen(args) - argsStart
		if provided < 0 {
			return
		}
		span := call.End - callee.End
		if span < 1 {
			span = 1
		}
		if provided < parsed.MaxIndex {
			diagnostics = append(diagnostics, CreateDiagnostic(callee.End, span,
				Diagnostics.FormatNotEnoughArguments01, strconv.Itoa(parsed.MaxIndex), strconv.Itoa(provided)))
			return // too few is the headline; a type pass would just add noise
		}
		if provided > parsed.MaxIndex {
			diagnostics = append(diagnostics, CreateDiagnostic(callee.End, span,
				Diagnostics.FormatTooManyArguments01, strconv.Itoa(parsed.MaxIndex), strconv.Itoa(provided)))
		}
		// Each consuming specifier against the static type of its mapped argument.
		for _, cons := range parsed.Consumers {
			idx := argsStart + cons.ArgIndex - 1
			if idx < 0 || idx >= nodeArrayLen(args) {
				continue
			}
			argNode := args.Nodes[idx]
			argType := c.getTypeOfExpression(argNode)
			desc, ok := argTypeDescriptor(argType)
			if !ok {
				continue
			}
			if ConversionAccepts(cons.Conversion, desc) == AcceptsNo {
				diagnostics = append(diagnostics, CreateDiagnostic(argNode.Pos, argNode.End-argNode.Pos,
					Diagnostics.FormatConversionIncompatible01, cons.Conversion, typeToString(argType)))
			}
		}
	}

	// Shared entry for the "known JDK method with a literal argument" checks:
	// returns the receiver's class FQN and the method name, or ok=false when the
	// callee is not a resolvable member access. Ports memberCallTarget /
	// literalStringArg in src/compiler/checker.ts.
	memberCallTarget := func(call *Node) (string, string, bool) {
		callee := call.AsCallExpression().Expression
		if callee.Kind != PropertyAccessExpression {
			return "", "", false
		}
		access := callee.AsPropertyAccessExpression()
		receiver := c.receiverClassType(c.getTypeOfExpression(access.Expression), 0)
		if receiver == nil {
			return "", "", false
		}
		return c.fqnOf(receiver), access.Name.AsIdentifier().Text, true
	}
	literalStringArg := func(call *Node, index int) *Node {
		args := call.AsCallExpression().Arguments
		if index >= nodeArrayLen(args) {
			return nil
		}
		arg := args.Nodes[index]
		if arg.Kind == StringLiteral || arg.Kind == TextBlockLiteral {
			return arg
		}
		return nil
	}

	// Regex literal validation: a malformed literal regex throws
	// PatternSyntaxException; the regex is argument 0 for every method here.
	checkRegexCall := func(call *Node) {
		fqn, name, ok := memberCallTarget(call)
		if !ok || !regexMethods[fqn+"#"+name] {
			return
		}
		arg := literalStringArg(call, 0)
		if arg == nil {
			return
		}
		if reason, bad := ValidateRegex(arg.AsLiteralExpression().Value); bad {
			diagnostics = append(diagnostics, CreateDiagnostic(arg.Pos, arg.End-arg.Pos,
				Diagnostics.InvalidRegularExpression0, reason))
		}
	}

	// DateTimeFormatter.ofPattern: unknown letters throw, Y/D/h footguns run wrong.
	checkDateTimeCall := func(call *Node) {
		fqn, name, ok := memberCallTarget(call)
		if !ok || fqn+"#"+name != "java.time.format.DateTimeFormatter#ofPattern" {
			return
		}
		arg := literalStringArg(call, 0)
		if arg == nil {
			return
		}
		report := CheckDateTimePattern(arg.AsLiteralExpression().Value)
		for _, letter := range report.InvalidLetters {
			diagnostics = append(diagnostics, CreateDiagnostic(arg.Pos, arg.End-arg.Pos,
				Diagnostics.InvalidDateTimePatternLetter0, letter))
		}
		for _, f := range report.Footguns {
			diagnostics = append(diagnostics, CreateDiagnostic(arg.Pos, arg.End-arg.Pos,
				Diagnostics.SuspiciousDateTimePatternLetter012, f.Letter, f.Meaning, f.Suggest))
		}
	}

	// Integer parsing (Integer/Long/Short/Byte parse*/valueOf): a non-parseable
	// literal or an out-of-range radix throws NumberFormatException. The string
	// is argument 0; a second numeric-literal argument, if any, is the radix.
	checkNumberParseCall := func(call *Node) {
		fqn, name, ok := memberCallTarget(call)
		if !ok {
			return
		}
		typeName, found := parseMethods[fqn+"#"+name]
		if !found {
			return
		}
		arg := literalStringArg(call, 0)
		if arg == nil {
			return
		}
		radix := 10
		args := call.AsCallExpression().Arguments
		if nodeArrayLen(args) > 1 {
			radixArg := args.Nodes[1]
			if radixArg.Kind != NumericLiteral {
				return // unknown radix: bail
			}
			r, err := strconv.Atoi(radixArg.AsLiteralExpression().Value)
			if err != nil {
				return
			}
			radix = r
			if radix < MinRadix || radix > MaxRadix {
				diagnostics = append(diagnostics, CreateDiagnostic(radixArg.Pos, radixArg.End-radixArg.Pos,
					Diagnostics.Radix0OutOfRange, strconv.Itoa(radix)))
				return
			}
		}
		value := arg.AsLiteralExpression().Value
		if !IsParseableInteger(value, radix) {
			diagnostics = append(diagnostics, CreateDiagnostic(arg.Pos, arg.End-arg.Pos,
				Diagnostics.String0IsNotAValid1, value, typeName))
		}
	}

	// Optional.ofNullable(x).ifPresent(...) (nikeee/cappu#42): the chain is a
	// roundabout null check; a plain `if (x != null)` says the same thing
	// without allocating an Optional. The matching quick fix lives in
	// code_actions.go. Ports checkOptionalIfPresentCall in src/compiler/checker.ts.
	checkOptionalIfPresentCall := func(call *Node) {
		fqn, name, ok := memberCallTarget(call)
		if !ok || fqn+"#"+name != "java.util.Optional#ifPresent" || nodeArrayLen(call.AsCallExpression().Arguments) != 1 {
			return
		}
		receiver := call.AsCallExpression().Expression.AsPropertyAccessExpression().Expression
		if receiver.Kind != CallExpression {
			return
		}
		innerFqn, innerName, ok := memberCallTarget(receiver)
		if !ok || innerFqn+"#"+innerName != "java.util.Optional#ofNullable" || nodeArrayLen(receiver.AsCallExpression().Arguments) != 1 {
			return
		}
		start := SkipTrivia(sourceFile.AsSourceFile().Text, call.Pos)
		diagnostics = append(diagnostics, CreateDiagnostic(start, call.End-start,
			Diagnostics.OptionalOfNullableIfPresentCanBeReplacedWithANullCheck))
	}

	// Optional#get() without an isPresent()/isEmpty() guard (nikeee/cappu#42): a
	// syntactic heuristic that flags `x.get()` unless some `x.isPresent()` /
	// `x.isEmpty()` check on the same variable name appears anywhere in the
	// enclosing method. No autofix - the right guard shape is the caller's call.
	// ponytail: name-based, not real data/control-flow; upgrade to flow analysis
	// if this proves noisy in practice. Ports checkOptionalGetCall in
	// src/compiler/checker.ts.
	checkOptionalGetCall := func(call *Node) {
		fqn, name, ok := memberCallTarget(call)
		if !ok || name != "get" || fqn != "java.util.Optional" {
			return
		}
		receiver := call.AsCallExpression().Expression.AsPropertyAccessExpression().Expression
		if receiver.Kind != Identifier {
			return // unprovable: stay silent
		}
		varName := receiver.AsIdentifier().Text
		fn := call.Parent
		for fn != nil && fn.Kind != MethodDeclaration && fn.Kind != ConstructorDeclaration {
			fn = fn.Parent
		}
		if fn == nil {
			return
		}
		guarded := false
		var scan func(n *Node)
		scan = func(n *Node) {
			if guarded {
				return
			}
			if n.Kind == CallExpression {
				_, tName, tok := memberCallTarget(n)
				if tok && (tName == "isPresent" || tName == "isEmpty") {
					recv := n.AsCallExpression().Expression.AsPropertyAccessExpression().Expression
					if recv.Kind == Identifier && recv.AsIdentifier().Text == varName {
						guarded = true
						return
					}
				}
			}
			n.ForEachChild(func(child *Node) bool {
				scan(child)
				return false
			})
		}
		scan(fn)
		if !guarded {
			start := SkipTrivia(sourceFile.AsSourceFile().Text, call.Pos)
			diagnostics = append(diagnostics, CreateDiagnostic(start, call.End-start,
				Diagnostics.OptionalGet0CalledWithoutAnIsPresentGuard, varName))
		}
	}

	// Optional.of(null) -> ofNullable (nikeee/cappu#42 follow-up): always throws
	// NullPointerException. Only a literal `null` argument is provably
	// always-throwing. Ports checkOptionalOfNull in src/compiler/checker.ts.
	checkOptionalOfNull := func(call *Node) {
		fqn, name, ok := memberCallTarget(call)
		if !ok || fqn+"#"+name != "java.util.Optional#of" {
			return
		}
		args := call.AsCallExpression().Arguments
		if nodeArrayLen(args) != 1 || args.Nodes[0].Kind != NullKeyword {
			return
		}
		text := sourceFile.AsSourceFile().Text
		start := SkipTrivia(text, call.Pos)
		diagnostics = append(diagnostics, CreateDiagnostic(start, call.End-start,
			Diagnostics.OptionalOfNullAlwaysThrows))
	}

	// size()/length() compared to 0/1 -> isEmpty()/!isEmpty() (nikeee/cappu#42):
	// one shared rule for both `X.size() <op> N` (Collection/Map/etc) and
	// `X.length() <op> N` (String); each is a roundabout emptiness check.
	// Ports the block of the same name in src/compiler/checker.ts.
	countFqns := map[string]bool{
		"java.util.Collection": true, "java.util.List": true, "java.util.Set": true,
		"java.util.Queue": true, "java.util.SortedSet": true, "java.util.Map": true,
		"java.util.SortedMap": true, "java.util.ArrayList": true, "java.util.HashMap": true,
		"java.util.HashSet": true, "java.util.LinkedHashMap": true, "java.util.LinkedHashSet": true,
		"java.util.LinkedList": true, "java.util.TreeMap": true, "java.util.TreeSet": true,
	}
	countCallReceiver := func(n *Node) *Node {
		if n.Kind != CallExpression || nodeArrayLen(n.AsCallExpression().Arguments) != 0 {
			return nil
		}
		fqn, name, ok := memberCallTarget(n)
		if !ok {
			return nil
		}
		if name == "size" && countFqns[fqn] {
			return n.AsCallExpression().Expression.AsPropertyAccessExpression().Expression
		}
		if name == "length" && fqn == "java.lang.String" {
			return n.AsCallExpression().Expression.AsPropertyAccessExpression().Expression
		}
		return nil
	}
	flipComparison := func(op SyntaxKind) SyntaxKind {
		switch op {
		case LessThanToken:
			return GreaterThanToken
		case GreaterThanToken:
			return LessThanToken
		case LessThanEqualsToken:
			return GreaterThanEqualsToken
		case GreaterThanEqualsToken:
			return LessThanEqualsToken
		default:
			return op
		}
	}
	countCheckNegates := func(op SyntaxKind, literal string) (negate bool, ok bool) {
		switch literal {
		case "0":
			switch op {
			case EqualsEqualsToken:
				return false, true
			case ExclamationEqualsToken:
				return true, true
			case GreaterThanToken:
				return true, true
			case LessThanEqualsToken:
				return false, true
			}
		case "1":
			switch op {
			case LessThanToken:
				return false, true
			case GreaterThanEqualsToken:
				return true, true
			}
		}
		return false, false
	}
	checkCountComparedToZero := func(bin *Node) {
		b := bin.AsBinaryExpression()
		receiver := countCallReceiver(b.Left)
		var literalNode *Node
		op := b.OperatorToken
		if receiver != nil && b.Right.Kind == NumericLiteral {
			literalNode = b.Right
		} else {
			receiver = countCallReceiver(b.Right)
			if receiver == nil || b.Left.Kind != NumericLiteral {
				return
			}
			literalNode = b.Left
			op = flipComparison(op)
		}
		negate, ok := countCheckNegates(op, literalNode.AsLiteralExpression().Value)
		if !ok {
			return
		}
		text := sourceFile.AsSourceFile().Text
		receiverStart := SkipTrivia(text, receiver.Pos)
		receiverText := text[receiverStart:receiver.End]
		start := SkipTrivia(text, bin.Pos)
		before := text[start:bin.End]
		after := receiverText + ".isEmpty()"
		if negate {
			after = "!" + after
		}
		diagnostics = append(diagnostics, CreateDiagnostic(start, bin.End-start,
			Diagnostics.CountCheck0CanBeReplacedWith1, before, after))
	}

	// == / != on Strings -> equals() (nikeee/cappu#42). Ports checkStringEquality
	// in src/compiler/checker.ts.
	isStringType := func(n *Node) bool {
		t := c.getTypeOfExpression(n)
		return t.Kind == TypeKindClass && c.fqnOf(t) == "java.lang.String"
	}
	checkStringEquality := func(bin *Node) {
		b := bin.AsBinaryExpression()
		if b.OperatorToken != EqualsEqualsToken && b.OperatorToken != ExclamationEqualsToken {
			return
		}
		if b.Left.Kind == NullKeyword || b.Right.Kind == NullKeyword {
			return
		}
		if !isStringType(b.Left) || !isStringType(b.Right) {
			return
		}
		opText := "!="
		if b.OperatorToken == EqualsEqualsToken {
			opText = "=="
		}
		text := sourceFile.AsSourceFile().Text
		start := SkipTrivia(text, bin.Pos)
		diagnostics = append(diagnostics, CreateDiagnostic(start, bin.End-start,
			Diagnostics.StringsShouldBeComparedWithEqualsNot0, opText))
	}

	// Boxing constructors (`new Integer(...)`, ...) -> valueOf() (nikeee/cappu#42):
	// all eight boxed types ship a `valueOf` factory; the stub declares no
	// constructors for them (matching by resolved type FQN, not a resolved
	// constructor symbol). Ports checkBoxingConstructor in src/compiler/checker.ts.
	boxingTypes := map[string]bool{
		"java.lang.Integer": true, "java.lang.Long": true, "java.lang.Short": true,
		"java.lang.Byte": true, "java.lang.Double": true, "java.lang.Float": true,
		"java.lang.Boolean": true, "java.lang.Character": true,
	}
	checkBoxingConstructor := func(node *Node) {
		oce := node.AsObjectCreationExpression()
		if oce.ClassBody != nil || oce.Qualifier != nil || oce.Type.Kind != TypeReference {
			return
		}
		t := c.resolveType(oce.Type, node)
		if t.Kind != TypeKindClass {
			return
		}
		fqn := c.fqnOf(t)
		if !boxingTypes[fqn] {
			return
		}
		typeName := fqn
		if i := strings.LastIndex(fqn, "."); i >= 0 {
			typeName = fqn[i+1:]
		}
		text := sourceFile.AsSourceFile().Text
		start := SkipTrivia(text, node.Pos)
		diagnostics = append(diagnostics, CreateDiagnostic(start, node.End-start,
			Diagnostics.BoxingConstructorNew0IsDeprecated, typeName))
	}

	// Boxed reference == comparison -> equals() (nikeee/cappu#42 follow-up): a
	// classic reference-vs-value bug; only some boxed values are cached, so this
	// often "works" in testing and breaks past that range. Ports
	// checkBoxedEquality in src/compiler/checker.ts.
	isBoxedType := func(n *Node) bool {
		t := c.getTypeOfExpression(n)
		return t.Kind == TypeKindClass && boxingTypes[c.fqnOf(t)]
	}
	checkBoxedEquality := func(bin *Node) {
		b := bin.AsBinaryExpression()
		if b.OperatorToken != EqualsEqualsToken && b.OperatorToken != ExclamationEqualsToken {
			return
		}
		if b.Left.Kind == NullKeyword || b.Right.Kind == NullKeyword {
			return
		}
		if !isBoxedType(b.Left) || !isBoxedType(b.Right) {
			return
		}
		opText := "!="
		if b.OperatorToken == EqualsEqualsToken {
			opText = "=="
		}
		text := sourceFile.AsSourceFile().Text
		start := SkipTrivia(text, bin.Pos)
		diagnostics = append(diagnostics, CreateDiagnostic(start, bin.End-start,
			Diagnostics.BoxedTypesShouldBeComparedWithEqualsNot0, opText))
	}

	// indexOf(...) != -1 -> contains(...) (nikeee/cappu#42). Ports
	// checkIndexOfComparedToNegativeOne in src/compiler/checker.ts.
	indexOfFqns := map[string]bool{
		"java.lang.String": true, "java.util.List": true, "java.util.ArrayList": true,
		"java.util.LinkedList": true, "java.util.Vector": true, "java.util.Stack": true,
	}
	isNegativeOne := func(n *Node) bool {
		return n.Kind == PrefixUnaryExpression &&
			n.AsPrefixUnaryExpression().Operator == MinusToken &&
			n.AsPrefixUnaryExpression().Operand.Kind == NumericLiteral &&
			n.AsPrefixUnaryExpression().Operand.AsLiteralExpression().Value == "1"
	}
	indexOfCall := func(n *Node) *Node {
		if n.Kind != CallExpression || nodeArrayLen(n.AsCallExpression().Arguments) != 1 {
			return nil
		}
		fqn, name, ok := memberCallTarget(n)
		if !ok || name != "indexOf" || !indexOfFqns[fqn] {
			return nil
		}
		return n
	}
	checkIndexOfComparedToNegativeOne := func(bin *Node) {
		b := bin.AsBinaryExpression()
		if b.OperatorToken != EqualsEqualsToken && b.OperatorToken != ExclamationEqualsToken {
			return
		}
		var call *Node
		if isNegativeOne(b.Right) {
			call = indexOfCall(b.Left)
		} else if isNegativeOne(b.Left) {
			call = indexOfCall(b.Right)
		}
		if call == nil {
			return
		}
		access := call.AsCallExpression().Expression.AsPropertyAccessExpression()
		text := sourceFile.AsSourceFile().Text
		receiverStart := SkipTrivia(text, access.Expression.Pos)
		receiverText := text[receiverStart:access.Expression.End]
		arg := call.AsCallExpression().Arguments.Nodes[0]
		argStart := SkipTrivia(text, arg.Pos)
		argText := text[argStart:arg.End]
		negate := b.OperatorToken == EqualsEqualsToken
		start := SkipTrivia(text, bin.Pos)
		before := text[start:bin.End]
		after := receiverText + ".contains(" + argText + ")"
		if negate {
			after = "!" + after
		}
		diagnostics = append(diagnostics, CreateDiagnostic(start, bin.End-start,
			Diagnostics.IndexOfCheck0CanBeReplacedWith1, before, after))
	}

	// Redundant new String(...) (nikeee/cappu#42). Ports checkRedundantNewString
	// in src/compiler/checker.ts.
	checkRedundantNewString := func(node *Node) {
		oce := node.AsObjectCreationExpression()
		if oce.ClassBody != nil || oce.Qualifier != nil || oce.Type.Kind != TypeReference {
			return
		}
		t := c.resolveType(oce.Type, node)
		if t.Kind != TypeKindClass || c.fqnOf(t) != "java.lang.String" {
			return
		}
		text := sourceFile.AsSourceFile().Text
		start := SkipTrivia(text, node.Pos)
		before := text[start:node.End]
		var after string
		switch nodeArrayLen(oce.Arguments) {
		case 0:
			after = `""`
		case 1:
			arg := oce.Arguments.Nodes[0]
			if !isStringType(arg) {
				return // byte[]/char[] conversion: a real conversion, not redundant
			}
			argStart := SkipTrivia(text, arg.Pos)
			after = text[argStart:arg.End]
		default:
			return
		}
		diagnostics = append(diagnostics, CreateDiagnostic(start, node.End-start,
			Diagnostics.NewString0CanBeReplacedWith1, before, after))
	}

	// equals("") -> isEmpty() (nikeee/cappu#42). `s.equals("")` autofixes
	// (code_actions.go); `"".equals(s)` is a deliberate null-safe idiom - warn
	// only, no autofix. Ports checkEqualsEmptyString in src/compiler/checker.ts.
	isEmptyStringLiteral := func(n *Node) bool {
		return n.Kind == StringLiteral && n.AsLiteralExpression().Value == ""
	}
	checkEqualsEmptyString := func(call *Node) {
		ce := call.AsCallExpression()
		if ce.Expression.Kind != PropertyAccessExpression {
			return
		}
		access := ce.Expression.AsPropertyAccessExpression()
		if access.Name.AsIdentifier().Text != "equals" || nodeArrayLen(ce.Arguments) != 1 {
			return
		}
		arg := ce.Arguments.Nodes[0]
		var receiver *Node
		switch {
		case isEmptyStringLiteral(arg) && isStringType(access.Expression):
			receiver = access.Expression // s.equals("")
		case isEmptyStringLiteral(access.Expression) && isStringType(arg):
			receiver = arg // "".equals(s)
		default:
			return
		}
		text := sourceFile.AsSourceFile().Text
		receiverStart := SkipTrivia(text, receiver.Pos)
		receiverText := text[receiverStart:receiver.End]
		start := SkipTrivia(text, call.Pos)
		before := text[start:call.End]
		after := receiverText + ".isEmpty()"
		diagnostics = append(diagnostics, CreateDiagnostic(start, call.End-start,
			Diagnostics.EqualsEmpty0CanBeReplacedWith1, before, after))
	}

	// Self-comparison (nikeee/cappu#42): restricted to call-free "stable read"
	// shapes (Identifier, or a this/field-access chain) so `next() == next()` -
	// two calls that happen to read the same text but aren't provably the same
	// value - is left alone. No autofix. Ports stableReadText and the two
	// checkSelfComparison* functions in src/compiler/checker.ts.
	var stableReadText func(n *Node) (string, bool)
	stableReadText = func(n *Node) (string, bool) {
		switch n.Kind {
		case Identifier:
			return n.AsIdentifier().Text, true
		case ThisExpression:
			return "this", true
		case PropertyAccessExpression:
			pa := n.AsPropertyAccessExpression()
			base, ok := stableReadText(pa.Expression)
			if !ok {
				return "", false
			}
			return base + "." + pa.Name.AsIdentifier().Text, true
		default:
			return "", false
		}
	}
	selfComparisonOps := map[SyntaxKind]bool{
		EqualsEqualsToken: true, ExclamationEqualsToken: true,
		GreaterThanToken: true, LessThanToken: true,
		GreaterThanEqualsToken: true, LessThanEqualsToken: true,
	}
	checkSelfComparisonBinary := func(bin *Node) {
		b := bin.AsBinaryExpression()
		if !selfComparisonOps[b.OperatorToken] {
			return
		}
		leftText, leftOk := stableReadText(b.Left)
		rightText, rightOk := stableReadText(b.Right)
		if !leftOk || !rightOk || leftText != rightText {
			return
		}
		text := sourceFile.AsSourceFile().Text
		start := SkipTrivia(text, bin.Pos)
		diagnostics = append(diagnostics, CreateDiagnostic(start, bin.End-start,
			Diagnostics.SuspiciousSelfComparison0, leftText))
	}
	checkSelfComparisonCall := func(call *Node) {
		ce := call.AsCallExpression()
		if ce.Expression.Kind != PropertyAccessExpression {
			return
		}
		access := ce.Expression.AsPropertyAccessExpression()
		name := access.Name.AsIdentifier().Text
		if (name != "equals" && name != "compareTo") || nodeArrayLen(ce.Arguments) != 1 {
			return
		}
		receiverText, receiverOk := stableReadText(access.Expression)
		argText, argOk := stableReadText(ce.Arguments.Nodes[0])
		if !receiverOk || !argOk || receiverText != argText {
			return
		}
		text := sourceFile.AsSourceFile().Text
		start := SkipTrivia(text, call.Pos)
		diagnostics = append(diagnostics, CreateDiagnostic(start, call.End-start,
			Diagnostics.SuspiciousSelfComparison0, receiverText))
	}

	// Node kinds that can have a leading `!` applied without changing meaning
	// (Java's primary/postfix expressions). Anything else must be
	// parenthesized first. Shared by the boolean-comparison and
	// boolean-ternary simplifications below.
	safeNotOperandKinds := map[SyntaxKind]bool{
		Identifier: true, PropertyAccessExpression: true, CallExpression: true,
		ParenthesizedExpression: true, ThisExpression: true, StringLiteral: true,
		TextBlockLiteral: true, ObjectCreationExpression: true,
	}
	negatedText := func(cond *Node, condText string) string {
		if safeNotOperandKinds[cond.Kind] {
			return "!" + condText
		}
		return "!(" + condText + ")"
	}

	// Boolean literal comparison simplification (nikeee/cappu#42 follow-up):
	// `b == true` -> `b`, `b == false` -> `!b`, `b != true` -> `!b`,
	// `b != false` -> `b`. Semantics-preserving including the null-unboxing
	// case. Ports checkBooleanComparison in src/compiler/checker.ts.
	checkBooleanComparison := func(bin *Node) {
		b := bin.AsBinaryExpression()
		if b.OperatorToken != EqualsEqualsToken && b.OperatorToken != ExclamationEqualsToken {
			return
		}
		isBoolLiteral := func(n *Node) bool { return n.Kind == TrueKeyword || n.Kind == FalseKeyword }
		var cond *Node
		var literalIsTrue bool
		switch {
		case isBoolLiteral(b.Right) && !isBoolLiteral(b.Left):
			cond = b.Left
			literalIsTrue = b.Right.Kind == TrueKeyword
		case isBoolLiteral(b.Left) && !isBoolLiteral(b.Right):
			cond = b.Right
			literalIsTrue = b.Left.Kind == TrueKeyword
		default:
			return
		}
		negate := literalIsTrue == (b.OperatorToken != EqualsEqualsToken)
		text := sourceFile.AsSourceFile().Text
		start := SkipTrivia(text, bin.Pos)
		before := text[start:bin.End]
		condStart := SkipTrivia(text, cond.Pos)
		condText := text[condStart:cond.End]
		after := condText
		if negate {
			after = negatedText(cond, condText)
		}
		diagnostics = append(diagnostics, CreateDiagnostic(start, bin.End-start,
			Diagnostics.RedundantBooleanComparison0CanBeReplacedWith1, before, after))
	}

	// Empty catch block (nikeee/cappu#42 follow-up): a catch block with no
	// statements silently discards the exception. A block containing only a
	// comment is assumed intentional and left alone. Ports checkEmptyCatchBlocks
	// in src/compiler/checker.ts.
	checkEmptyCatchBlocks := func(tryStmt *Node) {
		text := sourceFile.AsSourceFile().Text
		for _, clause := range tryStmt.AsTryStatement().CatchClauses.Nodes {
			cc := clause.AsCatchClause()
			block := cc.Block.AsBlock()
			if nodeArrayLen(block.Statements) > 0 {
				continue
			}
			braceStart := SkipTrivia(text, cc.Block.Pos)
			inner := strings.TrimSpace(text[braceStart+1 : cc.Block.End-1])
			if inner != "" {
				continue // a comment: assume intentional
			}
			start := SkipTrivia(text, clause.Pos)
			diagnostics = append(diagnostics, CreateDiagnostic(start, clause.End-start,
				Diagnostics.EmptyCatchBlockFor0, cc.Name.AsIdentifier().Text))
		}
	}

	// If/else returning booleans -> return cond (nikeee/cappu#42 follow-up):
	// `if (cond) return true; else return false;` -> `return cond;` (and the
	// negated form). Ports singleReturnBoolean/checkIfElseReturningBoolean in
	// src/compiler/checker.ts.
	singleReturnBoolean := func(stmt *Node) (value bool, ok bool) {
		if stmt == nil {
			return false, false
		}
		ret := stmt
		if stmt.Kind == Block {
			statements := stmt.AsBlock().Statements
			if nodeArrayLen(statements) != 1 {
				return false, false
			}
			ret = statements.Nodes[0]
		}
		if ret.Kind != ReturnStatement {
			return false, false
		}
		expr := ret.AsReturnStatement().Expression
		if expr == nil {
			return false, false
		}
		switch expr.Kind {
		case TrueKeyword:
			return true, true
		case FalseKeyword:
			return false, true
		default:
			return false, false
		}
	}
	checkIfElseReturningBoolean := func(ifStmt *Node) {
		is := ifStmt.AsIfStatement()
		thenValue, thenOk := singleReturnBoolean(is.ThenStatement)
		elseValue, elseOk := singleReturnBoolean(is.ElseStatement)
		if !thenOk || !elseOk || thenValue == elseValue {
			return
		}
		negate := !thenValue
		text := sourceFile.AsSourceFile().Text
		start := SkipTrivia(text, ifStmt.Pos)
		before := text[start:ifStmt.End]
		condStart := SkipTrivia(text, is.Condition.Pos)
		condText := text[condStart:is.Condition.End]
		after := "return " + condText + ";"
		if negate {
			after = "return " + negatedText(is.Condition, condText) + ";"
		}
		diagnostics = append(diagnostics, CreateDiagnostic(start, ifStmt.End-start,
			Diagnostics.IfElseReturningBooleans0CanBeReplacedWith1, before, after))
	}

	// Collapsible nested if -> merge with && (nikeee/cappu#42 follow-up):
	// `if (a) { if (b) { ... } }` -> `if (a && b) { ... }`. Both conditions are
	// parenthesized unconditionally in the merged text. Ports
	// singleStatementIf/checkCollapsibleIf in src/compiler/checker.ts.
	singleStatementIf := func(stmt *Node) *Node {
		if stmt.Kind == IfStatement {
			return stmt
		}
		if stmt.Kind == Block {
			statements := stmt.AsBlock().Statements
			if nodeArrayLen(statements) == 1 && statements.Nodes[0].Kind == IfStatement {
				return statements.Nodes[0]
			}
		}
		return nil
	}
	checkCollapsibleIf := func(outer *Node) {
		oi := outer.AsIfStatement()
		if oi.ElseStatement != nil {
			return
		}
		inner := singleStatementIf(oi.ThenStatement)
		if inner == nil || inner.AsIfStatement().ElseStatement != nil {
			return
		}
		ii := inner.AsIfStatement()
		text := sourceFile.AsSourceFile().Text
		outerCondStart := SkipTrivia(text, oi.Condition.Pos)
		outerCondText := text[outerCondStart:oi.Condition.End]
		innerCondStart := SkipTrivia(text, ii.Condition.Pos)
		innerCondText := text[innerCondStart:ii.Condition.End]
		merged := "(" + outerCondText + ") && (" + innerCondText + ")"
		start := SkipTrivia(text, outer.Pos)
		diagnostics = append(diagnostics, CreateDiagnostic(start, outer.End-start,
			Diagnostics.NestedIfCanBeCollapsedToIf0, merged))
	}

	// Ternary with boolean literals (nikeee/cappu#42 follow-up): `cond ? true :
	// false` -> `cond`; `cond ? false : true` -> `!cond`. Ports
	// checkTernaryBooleanLiterals in src/compiler/checker.ts.
	checkTernaryBooleanLiterals := func(expr *Node) {
		ce := expr.AsConditionalExpression()
		isBoolLiteral := func(n *Node) bool { return n.Kind == TrueKeyword || n.Kind == FalseKeyword }
		if !isBoolLiteral(ce.WhenTrue) || !isBoolLiteral(ce.WhenFalse) {
			return
		}
		whenTrueIsTrue := ce.WhenTrue.Kind == TrueKeyword
		whenFalseIsTrue := ce.WhenFalse.Kind == TrueKeyword
		if whenTrueIsTrue == whenFalseIsTrue {
			return
		}
		negate := !whenTrueIsTrue
		text := sourceFile.AsSourceFile().Text
		start := SkipTrivia(text, expr.Pos)
		before := text[start:expr.End]
		condStart := SkipTrivia(text, ce.Condition.Pos)
		condText := text[condStart:ce.Condition.End]
		after := condText
		if negate {
			after = negatedText(ce.Condition, condText)
		}
		diagnostics = append(diagnostics, CreateDiagnostic(start, expr.End-start,
			Diagnostics.TernaryWithBooleanLiterals0CanBeReplacedWith1, before, after))
	}

	// Optional as field/parameter type (nikeee/cappu#42 follow-up): discouraged
	// by Effective Java #55. The return-type use is the recommended pattern and
	// is deliberately not flagged here. Ports isOptionalType/
	// checkOptionalFieldType/checkOptionalParameterType in src/compiler/checker.ts.
	isOptionalType := func(typeNode, fromNode *Node) bool {
		if typeNode.Kind != TypeReference {
			return false
		}
		t := c.resolveType(typeNode, fromNode)
		return t.Kind == TypeKindClass && c.fqnOf(t) == "java.util.Optional"
	}
	checkOptionalFieldType := func(field *Node) {
		fd := field.AsFieldDeclaration()
		if !isOptionalType(fd.Type, field) {
			return
		}
		text := sourceFile.AsSourceFile().Text
		for _, d := range fd.Declarators.Nodes {
			name := d.AsVariableDeclarator().Name
			start := SkipTrivia(text, name.Pos)
			diagnostics = append(diagnostics, CreateDiagnostic(start, name.End-start,
				Diagnostics.Type01ShouldNotBeOfTypeOptional, "Field", name.AsIdentifier().Text))
		}
	}
	checkOptionalParameterType := func(param *Node) {
		p := param.AsParameter()
		if p.Name == nil || !isOptionalType(p.Type, param) {
			return
		}
		text := sourceFile.AsSourceFile().Text
		start := SkipTrivia(text, p.Name.Pos)
		diagnostics = append(diagnostics, CreateDiagnostic(start, p.Name.End-start,
			Diagnostics.Type01ShouldNotBeOfTypeOptional, "Parameter", p.Name.AsIdentifier().Text))
	}

	// Indexed for-loop over a List -> suggest enhanced for (nikeee/cappu#42
	// follow-up). No autofix: converting to `for (T x : xs)` risks CME/off-by-one
	// changes a purely syntactic pass can't rule out. The scope is narrowed
	// instead: only fires when the index is used for nothing but `xs.get(i)`
	// and nothing else touches the receiver.
	// ponytail: syntactic-only safety scan (no real data/control-flow); a
	// shadowing redeclaration of the loop or receiver name inside the body
	// could slip past this. Ports checkIndexedForLoop in src/compiler/checker.ts.
	indexedLoopFqns := map[string]bool{
		"java.util.List": true, "java.util.ArrayList": true, "java.util.LinkedList": true,
		"java.util.Vector": true, "java.util.Stack": true,
	}
	checkIndexedForLoop := func(forStmt *Node) {
		fs := forStmt.AsForStatement()
		if fs.Initializer == nil || fs.Initializer.Kind != LocalVariableDeclarationStatement {
			return
		}
		initStmt := fs.Initializer.AsLocalVariableDeclarationStatement()
		if nodeArrayLen(initStmt.Declarators) != 1 {
			return
		}
		decl := initStmt.Declarators.Nodes[0].AsVariableDeclarator()
		if decl.Initializer == nil || decl.Initializer.Kind != NumericLiteral ||
			decl.Initializer.AsLiteralExpression().Value != "0" {
			return
		}
		loopVar := decl.Name.AsIdentifier().Text

		if fs.Condition == nil || fs.Condition.Kind != BinaryExpression {
			return
		}
		cond := fs.Condition.AsBinaryExpression()
		if cond.OperatorToken != LessThanToken {
			return
		}
		if cond.Left.Kind != Identifier || cond.Left.AsIdentifier().Text != loopVar {
			return
		}
		if cond.Right.Kind != CallExpression {
			return
		}
		sizeFqn, sizeName, sizeOk := memberCallTarget(cond.Right)
		if !sizeOk || sizeName != "size" || !indexedLoopFqns[sizeFqn] {
			return
		}
		receiver := cond.Right.AsCallExpression().Expression.AsPropertyAccessExpression().Expression
		if receiver.Kind != Identifier {
			return
		}
		receiverName := receiver.AsIdentifier().Text

		if nodeArrayLen(fs.Incrementors) != 1 {
			return
		}
		inc := fs.Incrementors.Nodes[0]
		isLoopVarIncrement := false
		switch inc.Kind {
		case PostfixUnaryExpression:
			pu := inc.AsPostfixUnaryExpression()
			isLoopVarIncrement = pu.Operator == PlusPlusToken && pu.Operand.Kind == Identifier &&
				pu.Operand.AsIdentifier().Text == loopVar
		case PrefixUnaryExpression:
			pu := inc.AsPrefixUnaryExpression()
			isLoopVarIncrement = pu.Operator == PlusPlusToken && pu.Operand.Kind == Identifier &&
				pu.Operand.AsIdentifier().Text == loopVar
		}
		if !isLoopVarIncrement {
			return
		}

		disqualified := false
		var scan func(n *Node)
		scan = func(n *Node) {
			if disqualified {
				return
			}
			if n.Kind == Identifier && n.AsIdentifier().Text == loopVar {
				p := n.Parent
				isSoleGetArg := false
				if p != nil && p.Kind == CallExpression {
					pc := p.AsCallExpression()
					if nodeArrayLen(pc.Arguments) == 1 && pc.Arguments.Nodes[0] == n &&
						pc.Expression.Kind == PropertyAccessExpression {
						access := pc.Expression.AsPropertyAccessExpression()
						isSoleGetArg = access.Name.AsIdentifier().Text == "get" &&
							access.Expression.Kind == Identifier &&
							access.Expression.AsIdentifier().Text == receiverName
					}
				}
				if !isSoleGetArg {
					disqualified = true
					return
				}
			}
			if n.Kind == PropertyAccessExpression {
				access := n.AsPropertyAccessExpression()
				if access.Expression.Kind == Identifier &&
					access.Expression.AsIdentifier().Text == receiverName &&
					access.Name.AsIdentifier().Text != "get" {
					disqualified = true
					return
				}
			}
			n.ForEachChild(func(child *Node) bool {
				scan(child)
				return false
			})
		}
		scan(fs.Statement)
		if disqualified {
			return
		}

		text := sourceFile.AsSourceFile().Text
		start := SkipTrivia(text, forStmt.Pos)
		diagnostics = append(diagnostics, CreateDiagnostic(start, forStmt.End-start,
			Diagnostics.IndexedLoopOver0CanBeAForEachLoop, receiverName))
	}

	var visit func(node *Node)
	visit = func(node *Node) {
		switch node.Kind {
		case TryStatement:
			checkEmptyCatchBlocks(node)
		case FieldDeclaration:
			checkOptionalFieldType(node)
		case Parameter:
			checkOptionalParameterType(node)
		case ForStatement:
			checkIndexedForLoop(node)
		case ConditionalExpression:
			checkTernaryBooleanLiterals(node)
		case IfStatement:
			checkIfElseReturningBoolean(node)
			checkCollapsibleIf(node)
		case BinaryExpression:
			if cleanParse {
				checkCountComparedToZero(node)
				checkStringEquality(node)
				checkIndexOfComparedToNegativeOne(node)
				checkSelfComparisonBinary(node)
				checkBoxedEquality(node)
				checkBooleanComparison(node)
			}
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
				checkFormatCall(node)
				checkRegexCall(node)
				checkDateTimeCall(node)
				checkNumberParseCall(node)
				checkOptionalIfPresentCall(node)
				checkOptionalGetCall(node)
				checkEqualsEmptyString(node)
				checkSelfComparisonCall(node)
				checkOptionalOfNull(node)
			}
		case ObjectCreationExpression:
			if cleanParse {
				checkCreationArity(node)
				checkBoxingConstructor(node)
				checkRedundantNewString(node)
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
	// Private fields that could be declared final (suggestions).
	for _, declarator := range c.getFieldsThatCanBeFinal(sourceFile) {
		name := declarator.AsVariableDeclarator().Name
		start := skipTrivia(data.Text, name.Pos)
		diagnostics = append(diagnostics, CreateDiagnostic(start, name.End-start,
			Diagnostics.Field0CanBeFinal, name.AsIdentifier().Text))
	}
	// Uses of @Deprecated methods/types (warnings).
	for _, u := range c.GetDeprecatedUses(sourceFile) {
		diagnostics = append(diagnostics, CreateDiagnostic(u.Pos, u.End-u.Pos,
			Diagnostics.Deprecated0, u.Name))
	}
	return diagnostics
}

// arityInt parses the leading integer of an arity label ("2" or "1+").
func arityInt(s string) int {
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
