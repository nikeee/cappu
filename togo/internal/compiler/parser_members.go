package compiler

// Class/interface/enum members: fields, methods, constructors, initializer
// blocks, nested types, parameters. Port of the member grammar in
// src/compiler/parser.ts. (Record compact constructors arrive with the record
// slice.)

// isStartOfClassMember (widened now that members are parsed).
func (p *Parser) isStartOfClassMember() bool {
	switch p.token() {
	case SemicolonToken, OpenBraceToken, LessThanToken, AtToken, ClassKeyword, InterfaceKeyword, EnumKeyword:
		return true
	default:
		return isModifierKeyword(p.token()) || p.isStartOfType()
	}
}

func (p *Parser) parseArrayRankAfterName() int {
	rank := 0
	for p.token() == OpenBracketToken {
		p.nextToken()
		p.parseExpected(CloseBracketToken, nil)
		rank++
	}
	return rank
}

func (p *Parser) parseVariableDeclarator(name *Node) *Node {
	arrayRank := p.parseArrayRankAfterName()
	var initializer *Node
	if p.parseOptional(EqualsToken) {
		initializer = p.parseVariableInitializer()
	}
	return p.finishNode(p.factory.NewVariableDeclarator(name, arrayRank, initializer), name.Pos, -1)
}

func (p *Parser) parseFieldDeclaration(pos int, modifiers *NodeArray, typ, firstName *Node) *Node {
	declaratorsPos := firstName.Pos
	declarators := []*Node{p.parseVariableDeclarator(firstName)}
	for p.parseOptional(CommaToken) {
		declarators = append(declarators, p.parseVariableDeclarator(p.parseIdentifier()))
	}
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewFieldDeclaration(modifiers, typ, p.createNodeArray(declarators, declaratorsPos, -1)), pos, -1)
}

func (p *Parser) parseParameter() *Node {
	pos := p.getNodePos()
	modifiers := p.parseModifiers()
	typ := p.parseTypeOrVar()

	// Receiver parameter: [Identifier .] this.
	if p.token() == ThisKeyword {
		p.nextToken()
		return p.finishNode(p.factory.NewParameter(modifiers, typ, false, nil, 0, true), pos, -1)
	}
	if p.token() == Identifier && parserLookAhead(p, func() bool {
		p.nextToken()
		if p.token() != DotToken {
			return false
		}
		p.nextToken()
		return p.token() == ThisKeyword
	}) {
		p.parseIdentifier() // qualifier
		p.parseExpected(DotToken, nil)
		p.parseExpected(ThisKeyword, nil)
		return p.finishNode(p.factory.NewParameter(modifiers, typ, false, nil, 0, true), pos, -1)
	}

	isVarArgs := p.parseOptional(DotDotDotToken)
	name := p.parseIdentifier()
	arrayRank := p.parseArrayRankAfterName()
	return p.finishNode(p.factory.NewParameter(modifiers, typ, isVarArgs, name, arrayRank, false), pos, -1)
}

func (p *Parser) parseFormalParameters() *NodeArray {
	p.parseExpected(OpenParenToken, nil)
	parameters := p.parseDelimitedList(ctxParameters, p.parseParameter)
	p.parseExpected(CloseParenToken, nil)
	return parameters
}

func (p *Parser) parseThrows() *NodeArray {
	if p.parseOptional(ThrowsKeyword) {
		return p.parseTypeList()
	}
	return nil
}

// parseElementValue is an annotation element value: a nested annotation, a
// `{ ... }` array, or a constant expression (used for method default values).
func (p *Parser) parseElementValue() *Node {
	if p.token() == OpenBraceToken {
		return p.parseElementValueArrayInitializer()
	}
	if p.token() == AtToken && !p.isAnnotationTypeDeclarationStart() {
		return p.parseAnnotation()
	}
	return p.parseConditionalExpression()
}

// parseElementValueArrayInitializer parses an annotation element-value array
// (JLS 9.7.1) like `{@Index("a"), @Index("b")}`: each element is itself an
// element value (recursively allowing nested annotations and arrays), unlike a
// plain array initializer whose elements are expressions.
func (p *Parser) parseElementValueArrayInitializer() *Node {
	pos := p.getNodePos()
	p.parseExpected(OpenBraceToken, nil)
	elements := p.parseDelimitedList(ctxArrayInitializerElements, p.parseElementValue)
	p.parseExpected(CloseBraceToken, nil)
	return p.finishNode(p.factory.NewArrayInitializer(elements), pos, -1)
}

func (p *Parser) parseMethodDeclaration(pos int, modifiers, typeParameters *NodeArray, returnType, name *Node) *Node {
	parameters := p.parseFormalParameters()
	// C-style array return rank: int m()[]
	actualReturnType := returnType
	for i := 0; i < p.parseArrayRankAfterName(); i++ {
		actualReturnType = p.finishNode(p.factory.NewArrayType(actualReturnType), actualReturnType.Pos, -1)
	}
	throwsClause := p.parseThrows()
	var defaultValue *Node
	if p.parseOptional(DefaultKeyword) {
		defaultValue = p.parseElementValue()
	}
	var body *Node
	if p.token() == OpenBraceToken {
		body = p.parseBlock()
	} else {
		p.parseExpected(SemicolonToken, nil)
	}
	return p.finishNode(p.factory.NewMethodDeclaration(modifiers, typeParameters, actualReturnType, name, parameters, throwsClause, body, defaultValue), pos, -1)
}

func (p *Parser) parseConstructorDeclaration(pos int, modifiers, typeParameters *NodeArray) *Node {
	name := p.parseIdentifier()
	parameters := p.parseFormalParameters()
	throwsClause := p.parseThrows()
	body := p.parseBlock()
	return p.finishNode(p.factory.NewConstructorDeclaration(modifiers, typeParameters, name, parameters, throwsClause, body), pos, -1)
}

func (p *Parser) hasStaticModifier(modifiers *NodeArray) bool {
	if modifiers == nil {
		return false
	}
	for _, m := range modifiers.Nodes {
		if m.Kind == StaticKeyword {
			return true
		}
	}
	return false
}

func (p *Parser) parseInitializerBlock(pos int, modifiers *NodeArray) *Node {
	body := p.parseBlock()
	return p.finishNode(p.factory.NewInitializerBlock(p.hasStaticModifier(modifiers), body), pos, -1)
}

// isConstructorDeclaration: after optional type parameters, a member is a
// constructor when it is `Identifier (` with no return type.
func (p *Parser) isConstructorDeclaration() bool {
	return p.token() == Identifier && parserLookAhead(p, func() bool {
		p.nextToken()
		return p.token() == OpenParenToken
	})
}

func (p *Parser) parseClassMember() *Node {
	pos := p.getNodePos()
	if p.token() == SemicolonToken {
		return p.parseEmptyStatement()
	}
	modifiers := p.parseModifiers()

	if p.token() == OpenBraceToken {
		return p.parseInitializerBlock(pos, modifiers)
	}
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
	}

	typeParameters := p.parseTypeParameters()
	// Record compact constructor: Name { ... } (no parameter list).
	if p.token() == Identifier && parserLookAhead(p, func() bool { p.nextToken(); return p.token() == OpenBraceToken }) {
		return p.parseCompactConstructor(pos, modifiers)
	}
	if p.isConstructorDeclaration() {
		return p.parseConstructorDeclaration(pos, modifiers, typeParameters)
	}
	typ := p.parseType()
	name := p.parseIdentifier()
	if p.token() == OpenParenToken {
		return p.parseMethodDeclaration(pos, modifiers, typeParameters, typ, name)
	}
	return p.parseFieldDeclaration(pos, modifiers, typ, name)
}

func (p *Parser) parseCompactConstructor(pos int, modifiers *NodeArray) *Node {
	name := p.parseIdentifier()
	body := p.parseBlock()
	return p.finishNode(p.factory.NewCompactConstructorDeclaration(modifiers, name, body), pos, -1)
}
