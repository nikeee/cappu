package compiler

// Port of src/compiler/parser.ts module-declaration parsing (SE9, module-info.java).

func (p *Parser) parseModuleName() *Node {
	return p.parseEntityName()
}

func (p *Parser) parseRequiresDirective() *Node {
	pos := p.getNodePos()
	p.parseContextualKeyword("requires")
	isTransitive := false
	isStatic := false
	// 'static' is a real keyword, 'transitive' is contextual; either order.
	for p.token() == StaticKeyword || p.isContextualKeyword("transitive") {
		if p.token() == StaticKeyword {
			isStatic = true
		} else {
			isTransitive = true
		}
		p.nextToken()
	}
	name := p.parseModuleName()
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewRequiresDirective(isTransitive, isStatic, name), pos, -1)
}

func (p *Parser) parseToModuleList() *NodeArray {
	pos := p.getNodePos()
	list := []*Node{p.parseModuleName()}
	for p.parseOptional(CommaToken) {
		list = append(list, p.parseModuleName())
	}
	return p.createNodeArray(list, pos, -1)
}

func (p *Parser) parseExportsOrOpensDirective(keyword string, kind SyntaxKind) *Node {
	pos := p.getNodePos()
	p.parseContextualKeyword(keyword)
	packageName := p.parseEntityName()
	var toModules *NodeArray
	if p.parseContextualKeyword("to") {
		toModules = p.parseToModuleList()
	}
	p.parseExpected(SemicolonToken, nil)
	if kind == ExportsDirective {
		return p.finishNode(p.factory.NewExportsDirective(packageName, toModules), pos, -1)
	}
	return p.finishNode(p.factory.NewOpensDirective(packageName, toModules), pos, -1)
}

func (p *Parser) parseUsesDirective() *Node {
	pos := p.getNodePos()
	p.parseContextualKeyword("uses")
	typeName := p.parseEntityName()
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewUsesDirective(typeName), pos, -1)
}

func (p *Parser) parseProvidesDirective() *Node {
	pos := p.getNodePos()
	p.parseContextualKeyword("provides")
	typeName := p.parseEntityName()
	p.parseContextualKeyword("with")
	withPos := p.getNodePos()
	withTypes := []*Node{p.parseEntityName()}
	for p.parseOptional(CommaToken) {
		withTypes = append(withTypes, p.parseEntityName())
	}
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewProvidesDirective(typeName, p.createNodeArray(withTypes, withPos, -1)), pos, -1)
}

func (p *Parser) isModuleDirectiveStart() bool {
	return p.isContextualKeyword("requires") ||
		p.isContextualKeyword("exports") ||
		p.isContextualKeyword("opens") ||
		p.isContextualKeyword("uses") ||
		p.isContextualKeyword("provides")
}

func (p *Parser) parseModuleDirective() *Node {
	if p.isContextualKeyword("requires") {
		return p.parseRequiresDirective()
	}
	if p.isContextualKeyword("exports") {
		return p.parseExportsOrOpensDirective("exports", ExportsDirective)
	}
	if p.isContextualKeyword("opens") {
		return p.parseExportsOrOpensDirective("opens", OpensDirective)
	}
	if p.isContextualKeyword("uses") {
		return p.parseUsesDirective()
	}
	return p.parseProvidesDirective()
}

func (p *Parser) parseModuleDeclaration() *Node {
	pos := p.getNodePos()
	annotations := p.parseAnnotations()
	isOpen := p.parseContextualKeyword("open")
	p.parseContextualKeyword("module")
	name := p.parseModuleName()
	p.parseExpected(OpenBraceToken, nil)
	directives := p.parseList(ctxModuleDirectives, p.parseModuleDirective)
	p.parseExpected(CloseBraceToken, nil)
	return p.finishNode(p.factory.NewModuleDeclaration(annotations, isOpen, name, directives), pos, -1)
}
