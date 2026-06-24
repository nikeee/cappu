package compiler

// Types, modifiers, annotations and type declarations. Port of the
// corresponding grammar in src/compiler/parser.ts. Member bodies (fields,
// methods, constructors, statements, expressions) and annotation arguments
// arrive with later slices; isStartOfClassMember is scoped off until then.

// --- contextual keywords -----------------------------------------------------

func (p *Parser) isContextualKeyword(text string) bool {
	return p.token() == Identifier && p.scanner.TokenValue() == text
}

func (p *Parser) parseContextualKeyword(text string) bool {
	if p.isContextualKeyword(text) {
		p.nextToken()
		return true
	}
	return false
}

// --- start predicates --------------------------------------------------------

func (p *Parser) isStartOfStatement() bool {
	switch p.token() {
	case SemicolonToken, ClassKeyword, InterfaceKeyword, EnumKeyword, AtToken:
		return true
	default:
		return isModifierKeyword(p.token()) ||
			p.isContextualKeyword("sealed") || p.isContextualKeyword("non") ||
			p.isRecordDeclarationStart()
	}
}

func (p *Parser) isStartOfType() bool {
	return isPrimitiveTypeKeyword(p.token()) || p.token() == VoidKeyword ||
		p.token() == Identifier || p.token() == QuestionToken || p.token() == AtToken
}

func (p *Parser) isStartOfParameter() bool {
	return p.token() == AtToken || p.token() == FinalKeyword || p.isStartOfType()
}

// --- types -------------------------------------------------------------------

func (p *Parser) parseType() *Node {
	typ := p.parseNonArrayType()
	// '[' is an array marker only when immediately closed by ']'.
	for p.token() == OpenBracketToken && parserLookAhead(p, func() bool { p.nextToken(); return p.token() == CloseBracketToken }) {
		pos := typ.Pos
		p.nextToken() // '['
		p.parseExpected(CloseBracketToken, nil)
		typ = p.finishNode(p.factory.NewArrayType(typ), pos, -1)
	}
	return typ
}

func (p *Parser) parseNonArrayType() *Node {
	pos := p.getNodePos()
	// SE8 type-use annotations, e.g. @NonNull String. Attached to the produced node
	// so the nullness checker can read List<@Nullable String> (nikeee/cappu#25).
	typeAnnotations := p.parseAnnotations()
	if isPrimitiveTypeKeyword(p.token()) || p.token() == VoidKeyword {
		keyword := p.token()
		p.nextToken()
		node := p.factory.NewPrimitiveType(keyword)
		node.AsPrimitiveType().Annotations = typeAnnotations
		return p.finishNode(node, pos, -1)
	}
	if p.token() == QuestionToken {
		return p.parseWildcardType()
	}
	typeName := p.parseEntityName()
	var typeArguments *NodeArray
	if p.token() == LessThanToken {
		typeArguments = p.parseTypeArguments()
	}
	node := p.factory.NewTypeReference(typeName, typeArguments)
	node.AsTypeReference().Annotations = typeAnnotations
	return p.finishNode(node, pos, -1)
}

func (p *Parser) parseWildcardType() *Node {
	pos := p.getNodePos()
	p.parseExpected(QuestionToken, nil)
	hasExtends, hasSuper := false, false
	var typ *Node
	if p.parseOptional(ExtendsKeyword) {
		hasExtends = true
		typ = p.parseType()
	} else if p.parseOptional(SuperKeyword) {
		hasSuper = true
		typ = p.parseType()
	}
	return p.finishNode(p.factory.NewWildcardType(hasExtends, hasSuper, typ), pos, -1)
}

func (p *Parser) parseTypeArgument() *Node {
	if p.token() == QuestionToken {
		return p.parseWildcardType()
	}
	return p.parseType()
}

func (p *Parser) parseTypeArguments() *NodeArray {
	p.parseExpected(LessThanToken, nil)
	list := p.parseDelimitedList(ctxTypeArguments, p.parseTypeArgument)
	p.parseExpected(GreaterThanToken, nil)
	return list
}

func (p *Parser) parseTypeParameter() *Node {
	pos := p.getNodePos()
	annotations := p.parseAnnotations()
	name := p.parseIdentifier()
	var constraint *NodeArray
	if p.parseOptional(ExtendsKeyword) {
		bounds := []*Node{p.parseType()}
		boundsPos := bounds[0].Pos
		for p.parseOptional(AmpersandToken) {
			bounds = append(bounds, p.parseType())
		}
		constraint = p.createNodeArray(bounds, boundsPos, -1)
	}
	return p.finishNode(p.factory.NewTypeParameter(annotations, name, constraint), pos, -1)
}

func (p *Parser) parseTypeParameters() *NodeArray {
	if p.token() != LessThanToken {
		return nil
	}
	p.parseExpected(LessThanToken, nil)
	list := p.parseDelimitedList(ctxTypeParameters, p.parseTypeParameter)
	p.parseExpected(GreaterThanToken, nil)
	return list
}

func (p *Parser) parseTypeList() *NodeArray {
	pos := p.getNodePos()
	list := []*Node{p.parseType()}
	for p.parseOptional(CommaToken) {
		list = append(list, p.parseType())
	}
	return p.createNodeArray(list, pos, -1)
}

// --- modifiers and annotations -----------------------------------------------

// isAnnotationTypeDeclarationStart: the current '@' introduces @interface when
// the next token is 'interface'.
func (p *Parser) isAnnotationTypeDeclarationStart() bool {
	return LookAhead(p.scanner, func() SyntaxKind { return p.scanner.Scan() }) == InterfaceKeyword
}

func (p *Parser) isContextualModifierFollow() bool {
	return isModifierKeyword(p.token()) || p.token() == AtToken ||
		p.token() == ClassKeyword || p.token() == InterfaceKeyword || p.token() == EnumKeyword ||
		p.isContextualKeyword("sealed") || p.isContextualKeyword("non") || p.isContextualKeyword("record")
}

func (p *Parser) parseModifiers() *NodeArray {
	pos := p.getNodePos()
	var list []*Node
	for {
		if isModifierKeyword(p.token()) {
			list = append(list, p.parseTokenNode())
			continue
		}
		if p.token() == AtToken && !p.isAnnotationTypeDeclarationStart() {
			list = append(list, p.parseAnnotation())
			continue
		}
		if p.isContextualKeyword("sealed") && parserLookAhead(p, func() bool { p.nextToken(); return p.isContextualModifierFollow() }) {
			list = append(list, p.parseIdentifier()) // 'sealed'
			continue
		}
		if p.isContextualKeyword("non") && parserLookAhead(p, func() bool {
			p.nextToken()
			if p.token() != MinusToken {
				return false
			}
			p.nextToken()
			return p.isContextualKeyword("sealed")
		}) {
			mod := p.parseIdentifier() // 'non'
			p.parseExpected(MinusToken, nil)
			p.parseContextualKeyword("sealed")
			list = append(list, mod)
			continue
		}
		break
	}
	if len(list) == 0 {
		return nil
	}
	return p.createNodeArray(list, pos, -1)
}

// parseAnnotationArgument parses one element value, optionally named (`name =`).
func (p *Parser) parseAnnotationArgument() *Node {
	pos := p.getNodePos()
	var name *Node
	// NormalAnnotation pair: Identifier = ElementValue.
	if p.token() == Identifier && parserLookAhead(p, func() bool { p.nextToken(); return p.token() == EqualsToken }) {
		name = p.parseIdentifier()
		p.parseExpected(EqualsToken, nil)
	}
	value := p.parseElementValue()
	return p.finishNode(p.factory.NewAnnotationArgument(name, value), pos, -1)
}

// parseAnnotation parses `@TypeName` with an optional argument list `(...)`.
func (p *Parser) parseAnnotation() *Node {
	pos := p.getNodePos()
	p.parseExpected(AtToken, nil)
	typeName := p.parseEntityName()
	var args *NodeArray
	if p.token() == OpenParenToken {
		p.parseExpected(OpenParenToken, nil)
		args = p.parseDelimitedList(ctxAnnotationValues, p.parseAnnotationArgument)
		p.parseExpected(CloseParenToken, nil)
	}
	return p.finishNode(p.factory.NewAnnotation(typeName, args), pos, -1)
}

func (p *Parser) parseAnnotations() *NodeArray {
	pos := p.getNodePos()
	var list []*Node
	for p.token() == AtToken && !p.isAnnotationTypeDeclarationStart() {
		list = append(list, p.parseAnnotation())
	}
	if len(list) == 0 {
		return nil
	}
	return p.createNodeArray(list, pos, -1)
}

// --- type declarations -------------------------------------------------------

func (p *Parser) parseClassBody() *NodeArray {
	pos := p.getNodePos()
	if p.token() != OpenBraceToken {
		p.parseExpected(OpenBraceToken, nil)
		return p.createNodeArray(nil, pos, -1)
	}
	p.parseExpected(OpenBraceToken, nil)
	members := p.parseList(ctxClassMembers, p.parseClassMember)
	p.parseExpected(CloseBraceToken, nil)
	return members
}

func (p *Parser) isStartOfEnumConstant() bool {
	return p.token() == Identifier || p.token() == AtToken
}

func (p *Parser) parseEnumConstant() *Node {
	pos := p.getNodePos()
	annotations := p.parseAnnotations()
	name := p.parseIdentifier()
	var args *NodeArray
	if p.token() == OpenParenToken {
		args = p.parseArgumentList()
	}
	var classBody *NodeArray
	if p.token() == OpenBraceToken {
		classBody = p.parseClassBody()
	}
	return p.finishNode(p.factory.NewEnumConstantDeclaration(annotations, name, args, classBody), pos, -1)
}

func (p *Parser) parseEnumBody() (enumConstants, members *NodeArray) {
	constantsPos := p.getNodePos()
	p.parseExpected(OpenBraceToken, nil)
	var constants []*Node
	if p.isStartOfEnumConstant() {
		constants = append(constants, p.parseEnumConstant())
		for p.parseOptional(CommaToken) {
			if !p.isStartOfEnumConstant() {
				break // trailing comma
			}
			constants = append(constants, p.parseEnumConstant())
		}
	}
	enumConstants = p.createNodeArray(constants, constantsPos, -1)
	if p.parseOptional(SemicolonToken) {
		members = p.parseList(ctxClassMembers, p.parseClassMember)
	} else {
		members = p.createNodeArray(nil, p.getNodePos(), -1)
	}
	p.parseExpected(CloseBraceToken, nil)
	return enumConstants, members
}

func (p *Parser) parseClassDeclaration(pos int, modifiers *NodeArray) *Node {
	p.parseExpected(ClassKeyword, nil)
	name := p.parseIdentifier()
	typeParameters := p.parseTypeParameters()
	var extendsType *Node
	if p.parseOptional(ExtendsKeyword) {
		extendsType = p.parseType()
	}
	var implementsTypes *NodeArray
	if p.parseOptional(ImplementsKeyword) {
		implementsTypes = p.parseTypeList()
	}
	var permitsTypes *NodeArray
	if p.parseContextualKeyword("permits") {
		permitsTypes = p.parseTypeList()
	}
	members := p.parseClassBody()
	return p.finishNode(p.factory.NewClassDeclaration(modifiers, name, typeParameters, extendsType, implementsTypes, permitsTypes, members), pos, -1)
}

func (p *Parser) parseInterfaceDeclaration(pos int, modifiers *NodeArray) *Node {
	p.parseExpected(InterfaceKeyword, nil)
	name := p.parseIdentifier()
	typeParameters := p.parseTypeParameters()
	var extendsTypes *NodeArray
	if p.parseOptional(ExtendsKeyword) {
		extendsTypes = p.parseTypeList()
	}
	var permitsTypes *NodeArray
	if p.parseContextualKeyword("permits") {
		permitsTypes = p.parseTypeList()
	}
	members := p.parseClassBody()
	return p.finishNode(p.factory.NewInterfaceDeclaration(modifiers, name, typeParameters, extendsTypes, permitsTypes, members), pos, -1)
}

func (p *Parser) parseEnumDeclaration(pos int, modifiers *NodeArray) *Node {
	p.parseExpected(EnumKeyword, nil)
	name := p.parseIdentifier()
	var implementsTypes *NodeArray
	if p.parseOptional(ImplementsKeyword) {
		implementsTypes = p.parseTypeList()
	}
	enumConstants, members := p.parseEnumBody()
	return p.finishNode(p.factory.NewEnumDeclaration(modifiers, name, implementsTypes, enumConstants, members), pos, -1)
}

func (p *Parser) parseAnnotationTypeDeclaration(pos int, modifiers *NodeArray) *Node {
	p.parseExpected(AtToken, nil)
	p.parseExpected(InterfaceKeyword, nil)
	name := p.parseIdentifier()
	members := p.parseClassBody()
	return p.finishNode(p.factory.NewAnnotationTypeDeclaration(modifiers, name, members), pos, -1)
}

// isRecordDeclarationStart: 'record' is contextual; a record needs an
// identifier then '(' or type parameters.
func (p *Parser) isRecordDeclarationStart() bool {
	return p.isContextualKeyword("record") && parserLookAhead(p, func() bool {
		p.nextToken() // 'record'
		if p.token() != Identifier {
			return false
		}
		p.nextToken() // name
		return p.token() == OpenParenToken || p.token() == LessThanToken
	})
}

func (p *Parser) parseRecordComponent() *Node {
	pos := p.getNodePos()
	annotations := p.parseAnnotations()
	typ := p.parseType()
	isVarArgs := p.parseOptional(DotDotDotToken)
	name := p.parseIdentifier()
	return p.finishNode(p.factory.NewRecordComponent(annotations, typ, isVarArgs, name), pos, -1)
}

func (p *Parser) parseRecordDeclaration(pos int, modifiers *NodeArray) *Node {
	p.parseContextualKeyword("record")
	name := p.parseIdentifier()
	typeParameters := p.parseTypeParameters()
	p.parseExpected(OpenParenToken, nil)
	recordComponents := p.parseDelimitedList(ctxParameters, p.parseRecordComponent)
	p.parseExpected(CloseParenToken, nil)
	var implementsTypes *NodeArray
	if p.parseOptional(ImplementsKeyword) {
		implementsTypes = p.parseTypeList()
	}
	members := p.parseClassBody()
	return p.finishNode(p.factory.NewRecordDeclaration(modifiers, name, typeParameters, recordComponents, implementsTypes, members), pos, -1)
}

func (p *Parser) parseTypeDeclaration() *Node {
	pos := p.getNodePos()
	modifiers := p.parseModifiers()
	if p.isRecordDeclarationStart() {
		return p.parseRecordDeclaration(pos, modifiers)
	}
	switch p.token() {
	case ClassKeyword:
		return p.parseClassDeclaration(pos, modifiers)
	case InterfaceKeyword:
		return p.parseInterfaceDeclaration(pos, modifiers)
	case EnumKeyword:
		return p.parseEnumDeclaration(pos, modifiers)
	case AtToken:
		return p.parseAnnotationTypeDeclaration(pos, modifiers)
	default:
		p.parseErrorAtCurrentToken(Diagnostics.DeclarationExpected)
		if p.token() != EndOfFileToken {
			p.nextToken()
		}
		// An empty class declaration (not a bare token tagged ClassDeclaration):
		// the binder/checker/emitter read its fields via As accessors, which would
		// panic on a token payload. An empty-named class is a crash-free best
		// effort over malformed input (TS reads undefined fields off its token).
		return p.finishNode(p.factory.NewClassDeclaration(modifiers, p.factory.NewIdentifier(""), nil, nil, nil, nil, nil), pos, -1)
	}
}
