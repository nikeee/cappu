package compiler

// Statement grammar: blocks, control flow, try, switch (constant/default
// labels), local declarations, expression statements, plus variable
// initializers (incl. array initializers) and `var`. Port of the statement
// grammar in src/compiler/parser.ts. Record-pattern case labels and the SE21
// guard-with-pattern forms arrive with the pattern slice.

func (p *Parser) parseTypeOrVar() *Node {
	if p.isContextualKeyword("var") {
		pos := p.getNodePos()
		p.nextToken()
		return p.finishNode(p.factory.NewVarType(), pos, -1)
	}
	return p.parseType()
}

func (p *Parser) parseVariableInitializer() *Node {
	if p.token() == OpenBraceToken {
		return p.parseArrayInitializer()
	}
	return p.parseExpression()
}

func (p *Parser) parseArrayInitializer() *Node {
	pos := p.getNodePos()
	p.parseExpected(OpenBraceToken, nil)
	elements := p.parseDelimitedList(ctxArrayInitializerElements, p.parseVariableInitializer)
	p.parseExpected(CloseBraceToken, nil)
	return p.finishNode(p.factory.NewArrayInitializer(elements), pos, -1)
}

func (p *Parser) parseBlock() *Node {
	pos := p.getNodePos()
	p.parseExpected(OpenBraceToken, nil)
	statements := p.parseList(ctxBlockStatements, p.parseStatement)
	p.parseExpected(CloseBraceToken, nil)
	return p.finishNode(p.factory.NewBlock(statements), pos, -1)
}

func (p *Parser) isStartOfStatementToken() bool {
	switch p.token() {
	case SemicolonToken, OpenBraceToken, IfKeyword, WhileKeyword, DoKeyword, ForKeyword,
		TryKeyword, SwitchKeyword, ReturnKeyword, ThrowKeyword, BreakKeyword, ContinueKeyword,
		SynchronizedKeyword, AssertKeyword, ClassKeyword, InterfaceKeyword, EnumKeyword,
		AtToken, FinalKeyword:
		return true
	default:
		return p.isStartOfExpression()
	}
}

func (p *Parser) isLocalVariableDeclarationStart() bool {
	return parserLookAhead(p, func() bool {
		p.parseType()
		return p.token() == Identifier
	})
}

func (p *Parser) parseStatement() *Node {
	switch p.token() {
	case SemicolonToken:
		return p.parseEmptyStatement()
	case OpenBraceToken:
		return p.parseBlock()
	case IfKeyword:
		return p.parseIfStatement()
	case WhileKeyword:
		return p.parseWhileStatement()
	case DoKeyword:
		return p.parseDoStatement()
	case ForKeyword:
		return p.parseForStatement()
	case TryKeyword:
		return p.parseTryStatement()
	case SwitchKeyword:
		return p.parseSwitchStatement()
	case ReturnKeyword:
		return p.parseReturnStatement()
	case ThrowKeyword:
		return p.parseThrowStatement()
	case BreakKeyword:
		return p.parseBreakOrContinue(BreakStatement)
	case ContinueKeyword:
		return p.parseBreakOrContinue(ContinueStatement)
	case SynchronizedKeyword:
		return p.parseSynchronizedStatement()
	case AssertKeyword:
		return p.parseAssertStatement()
	case ClassKeyword, InterfaceKeyword, EnumKeyword:
		return p.parseTypeDeclaration()
	}
	if p.isContextualKeyword("yield") && parserLookAhead(p, func() bool { p.nextToken(); return p.isStartOfExpression() }) {
		return p.parseYieldStatement()
	}
	if p.token() == Identifier && parserLookAhead(p, func() bool { p.nextToken(); return p.token() == ColonToken }) {
		return p.parseLabeledStatement()
	}
	if isModifierKeyword(p.token()) || p.token() == AtToken || p.isLocalVariableDeclarationStart() {
		return p.parseLocalDeclarationStatement()
	}
	pos := p.getNodePos()
	expression := p.parseExpression()
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewExpressionStatement(expression), pos, -1)
}

func (p *Parser) parseLocalVariableDeclarationRest(pos int, modifiers *NodeArray) *Node {
	typ := p.parseTypeOrVar()
	declaratorsPos := p.getNodePos()
	declarators := []*Node{p.parseVariableDeclarator(p.parseIdentifier())}
	for p.parseOptional(CommaToken) {
		declarators = append(declarators, p.parseVariableDeclarator(p.parseIdentifier()))
	}
	return p.finishNode(p.factory.NewLocalVariableDeclarationStatement(modifiers, typ, p.createNodeArray(declarators, declaratorsPos, -1)), pos, -1)
}

func (p *Parser) parseLocalDeclarationStatement() *Node {
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
	}
	node := p.parseLocalVariableDeclarationRest(pos, modifiers)
	p.parseExpected(SemicolonToken, nil)
	return node
}

func (p *Parser) parseIfStatement() *Node {
	pos := p.getNodePos()
	p.parseExpected(IfKeyword, nil)
	p.parseExpected(OpenParenToken, nil)
	condition := p.parseExpression()
	p.parseExpected(CloseParenToken, nil)
	then := p.parseStatement()
	var els *Node
	if p.parseOptional(ElseKeyword) {
		els = p.parseStatement()
	}
	return p.finishNode(p.factory.NewIfStatement(condition, then, els), pos, -1)
}

func (p *Parser) parseWhileStatement() *Node {
	pos := p.getNodePos()
	p.parseExpected(WhileKeyword, nil)
	p.parseExpected(OpenParenToken, nil)
	condition := p.parseExpression()
	p.parseExpected(CloseParenToken, nil)
	return p.finishNode(p.factory.NewWhileStatement(condition, p.parseStatement()), pos, -1)
}

func (p *Parser) parseDoStatement() *Node {
	pos := p.getNodePos()
	p.parseExpected(DoKeyword, nil)
	statement := p.parseStatement()
	p.parseExpected(WhileKeyword, nil)
	p.parseExpected(OpenParenToken, nil)
	condition := p.parseExpression()
	p.parseExpected(CloseParenToken, nil)
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewDoStatement(statement, condition), pos, -1)
}

func (p *Parser) isForEachHeader() bool {
	return parserLookAhead(p, func() bool {
		p.parseModifiers()
		if p.token() == SemicolonToken {
			return false
		}
		p.parseType()
		if p.token() != Identifier {
			return false
		}
		p.nextToken()
		return p.token() == ColonToken
	})
}

func (p *Parser) parseForStatement() *Node {
	pos := p.getNodePos()
	p.parseExpected(ForKeyword, nil)
	p.parseExpected(OpenParenToken, nil)

	if p.isForEachHeader() {
		parameter := p.parseParameter()
		p.parseExpected(ColonToken, nil)
		expression := p.parseExpression()
		p.parseExpected(CloseParenToken, nil)
		return p.finishNode(p.factory.NewForEachStatement(parameter, expression, p.parseStatement()), pos, -1)
	}

	var initializer *Node
	var initializerExpressions *NodeArray
	if p.token() == SemicolonToken {
		p.nextToken()
	} else {
		if isModifierKeyword(p.token()) || p.token() == AtToken || p.isLocalVariableDeclarationStart() {
			initPos := p.getNodePos()
			initializer = p.parseLocalVariableDeclarationRest(initPos, p.parseModifiers())
		} else {
			initPos := p.getNodePos()
			list := []*Node{p.parseExpression()}
			for p.parseOptional(CommaToken) {
				list = append(list, p.parseExpression())
			}
			initializerExpressions = p.createNodeArray(list, initPos, -1)
		}
		p.parseExpected(SemicolonToken, nil)
	}

	var condition *Node
	if p.token() != SemicolonToken {
		condition = p.parseExpression()
	}
	p.parseExpected(SemicolonToken, nil)

	var incrementors *NodeArray
	if p.token() != CloseParenToken {
		incPos := p.getNodePos()
		list := []*Node{p.parseExpression()}
		for p.parseOptional(CommaToken) {
			list = append(list, p.parseExpression())
		}
		incrementors = p.createNodeArray(list, incPos, -1)
	}
	p.parseExpected(CloseParenToken, nil)
	return p.finishNode(p.factory.NewForStatement(initializer, initializerExpressions, condition, incrementors, p.parseStatement()), pos, -1)
}

func (p *Parser) parseReturnStatement() *Node {
	pos := p.getNodePos()
	p.parseExpected(ReturnKeyword, nil)
	var expression *Node
	if p.token() != SemicolonToken {
		expression = p.parseExpression()
	}
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewReturnStatement(expression), pos, -1)
}

func (p *Parser) parseThrowStatement() *Node {
	pos := p.getNodePos()
	p.parseExpected(ThrowKeyword, nil)
	expression := p.parseExpression()
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewThrowStatement(expression), pos, -1)
}

func (p *Parser) parseBreakOrContinue(kind SyntaxKind) *Node {
	pos := p.getNodePos()
	p.nextToken() // 'break' / 'continue'
	var label *Node
	if p.token() == Identifier {
		label = p.parseIdentifier()
	}
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewBreakOrContinue(kind, label), pos, -1)
}

func (p *Parser) parseSynchronizedStatement() *Node {
	pos := p.getNodePos()
	p.parseExpected(SynchronizedKeyword, nil)
	p.parseExpected(OpenParenToken, nil)
	expression := p.parseExpression()
	p.parseExpected(CloseParenToken, nil)
	return p.finishNode(p.factory.NewSynchronizedStatement(expression, p.parseBlock()), pos, -1)
}

func (p *Parser) parseAssertStatement() *Node {
	pos := p.getNodePos()
	p.parseExpected(AssertKeyword, nil)
	condition := p.parseExpression()
	var message *Node
	if p.parseOptional(ColonToken) {
		message = p.parseExpression()
	}
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewAssertStatement(condition, message), pos, -1)
}

func (p *Parser) parseLabeledStatement() *Node {
	pos := p.getNodePos()
	label := p.parseIdentifier()
	p.parseExpected(ColonToken, nil)
	return p.finishNode(p.factory.NewLabeledStatement(label, p.parseStatement()), pos, -1)
}

func (p *Parser) parseYieldStatement() *Node {
	pos := p.getNodePos()
	p.parseContextualKeyword("yield")
	expression := p.parseExpression()
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewYieldStatement(expression), pos, -1)
}

// --- try ---------------------------------------------------------------------

func (p *Parser) parseResource() *Node {
	pos := p.getNodePos()
	modifiers := p.parseModifiers()
	isDeclaration := modifiers != nil || parserLookAhead(p, func() bool {
		p.parseTypeOrVar()
		if p.token() != Identifier {
			return false
		}
		p.nextToken()
		return p.token() == EqualsToken
	})
	if isDeclaration {
		typ := p.parseTypeOrVar()
		name := p.parseIdentifier()
		p.parseExpected(EqualsToken, nil)
		initializer := p.parseExpression()
		return p.finishNode(p.factory.NewResource(modifiers, typ, name, initializer, nil), pos, -1)
	}
	return p.finishNode(p.factory.NewResource(modifiers, nil, nil, nil, p.parseExpression()), pos, -1)
}

func (p *Parser) parseResourceSpecification() *NodeArray {
	pos := p.getNodePos()
	p.parseExpected(OpenParenToken, nil)
	resources := []*Node{p.parseResource()}
	for p.parseOptional(SemicolonToken) {
		if p.token() == CloseParenToken {
			break // trailing ';'
		}
		resources = append(resources, p.parseResource())
	}
	p.parseExpected(CloseParenToken, nil)
	return p.createNodeArray(resources, pos, -1)
}

func (p *Parser) parseCatchClause() *Node {
	pos := p.getNodePos()
	p.parseExpected(CatchKeyword, nil)
	p.parseExpected(OpenParenToken, nil)
	p.parseModifiers() // 'final' allowed but not retained
	typesPos := p.getNodePos()
	catchTypes := []*Node{p.parseType()}
	for p.parseOptional(BarToken) {
		catchTypes = append(catchTypes, p.parseType())
	}
	name := p.parseIdentifier()
	p.parseExpected(CloseParenToken, nil)
	block := p.parseBlock()
	return p.finishNode(p.factory.NewCatchClause(p.createNodeArray(catchTypes, typesPos, -1), name, block), pos, -1)
}

func (p *Parser) parseTryStatement() *Node {
	pos := p.getNodePos()
	p.parseExpected(TryKeyword, nil)
	var resources *NodeArray
	if p.token() == OpenParenToken {
		resources = p.parseResourceSpecification()
	}
	tryBlock := p.parseBlock()
	catchPos := p.getNodePos()
	var catchClauses []*Node
	for p.token() == CatchKeyword {
		catchClauses = append(catchClauses, p.parseCatchClause())
	}
	var finallyBlock *Node
	if p.parseOptional(FinallyKeyword) {
		finallyBlock = p.parseBlock()
	}
	return p.finishNode(p.factory.NewTryStatement(resources, tryBlock, p.createNodeArray(catchClauses, catchPos, -1), finallyBlock), pos, -1)
}

// --- switch (constant/default labels; patterns arrive with the pattern slice) -

func (p *Parser) parseCaseLabelElement() *Node {
	if p.token() == DefaultKeyword {
		return p.parseTokenNode()
	}
	return p.parseConditionalExpression()
}

func (p *Parser) makeExpressionStatement(expression *Node) *Node {
	return p.finishNode(p.factory.NewExpressionStatement(expression), expression.Pos, -1)
}

func (p *Parser) parseSwitchClause() *Node {
	pos := p.getNodePos()
	isDefault := false
	var labels *NodeArray
	var guard *Node
	if p.parseOptional(CaseKeyword) {
		labelsPos := p.getNodePos()
		list := []*Node{p.parseCaseLabelElement()}
		for p.parseOptional(CommaToken) {
			list = append(list, p.parseCaseLabelElement())
		}
		labels = p.createNodeArray(list, labelsPos, -1)
		if p.parseContextualKeyword("when") {
			guard = p.parseConditionalExpression()
		}
	} else {
		p.parseExpected(DefaultKeyword, nil)
		isDefault = true
	}

	isArrow := false
	statementsPos := p.getNodePos()
	var statements []*Node
	if p.parseOptional(ArrowToken) {
		isArrow = true
		switch {
		case p.token() == OpenBraceToken:
			statements = []*Node{p.parseBlock()}
		case p.token() == ThrowKeyword:
			statements = []*Node{p.parseThrowStatement()}
		default:
			expression := p.parseExpression()
			p.parseExpected(SemicolonToken, nil)
			statements = []*Node{p.makeExpressionStatement(expression)}
		}
	} else {
		p.parseExpected(ColonToken, nil)
		for p.token() != CaseKeyword && p.token() != DefaultKeyword && p.token() != CloseBraceToken && p.token() != EndOfFileToken {
			if !p.isStartOfStatementToken() {
				break
			}
			statements = append(statements, p.parseStatement())
		}
	}
	return p.finishNode(p.factory.NewSwitchClause(isDefault, isArrow, labels, guard, p.createNodeArray(statements, statementsPos, -1)), pos, -1)
}

func (p *Parser) parseSwitchBlock() *NodeArray {
	p.parseExpected(OpenBraceToken, nil)
	clauses := p.parseList(ctxSwitchClauses, p.parseSwitchClause)
	p.parseExpected(CloseBraceToken, nil)
	return clauses
}

func (p *Parser) parseSwitchStatement() *Node {
	pos := p.getNodePos()
	p.parseExpected(SwitchKeyword, nil)
	p.parseExpected(OpenParenToken, nil)
	expression := p.parseExpression()
	p.parseExpected(CloseParenToken, nil)
	return p.finishNode(p.factory.NewSwitchStatement(expression, p.parseSwitchBlock()), pos, -1)
}
