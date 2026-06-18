package compiler

// Expression grammar: precedence-climbing binary parsing, assignment,
// conditional, unary, cast, instanceof, postfix and primary (literals, names,
// this/super, parenthesized, member access, element access, calls). Port of the
// expression parser in src/compiler/parser.ts.
//
// Exotic forms - object/array creation, lambdas, method references, switch
// expressions, class literals and record patterns - are stubbed (recovered)
// here and arrive with their own slice; no current test exercises them.

func (p *Parser) isStartOfExpression() bool {
	switch p.token() {
	case NumericLiteral, StringLiteral, CharacterLiteral, TextBlockLiteral,
		TrueKeyword, FalseKeyword, NullKeyword, ThisKeyword, SuperKeyword, NewKeyword,
		SwitchKeyword, Identifier, OpenParenToken, ExclamationToken, TildeToken,
		PlusToken, MinusToken, PlusPlusToken, MinusMinusToken:
		return true
	default:
		return isPrimitiveTypeKeyword(p.token()) || p.token() == VoidKeyword
	}
}

func (p *Parser) reScanGreaterIfNeeded() {
	if p.token() == GreaterThanToken {
		p.currentToken = p.scanner.ReScanGreaterToken()
	}
}

func binaryOperatorPrecedence(kind SyntaxKind) int {
	switch kind {
	case BarBarToken:
		return 1
	case AmpersandAmpersandToken:
		return 2
	case BarToken:
		return 3
	case CaretToken:
		return 4
	case AmpersandToken:
		return 5
	case EqualsEqualsToken, ExclamationEqualsToken:
		return 6
	case LessThanToken, GreaterThanToken, LessThanEqualsToken, GreaterThanEqualsToken:
		return 7
	case LessThanLessThanToken, GreaterThanGreaterThanToken, GreaterThanGreaterThanGreaterThanToken:
		return 8
	case PlusToken, MinusToken:
		return 9
	case AsteriskToken, SlashToken, PercentToken:
		return 10
	default:
		return 0
	}
}

const relationalPrecedence = 7

func (p *Parser) parseExpression() *Node { return p.parseAssignmentExpression() }

func (p *Parser) parseAssignmentExpression() *Node {
	// lambda detection arrives with the lambda slice.
	pos := p.getNodePos()
	left := p.parseConditionalExpression()
	p.reScanGreaterIfNeeded()
	if isAssignmentOperator(p.token()) {
		op := p.token()
		p.nextToken()
		right := p.parseAssignmentExpression()
		return p.finishNode(p.factory.NewAssignmentExpression(left, op, right), pos, -1)
	}
	return left
}

func (p *Parser) parseConditionalExpression() *Node {
	pos := p.getNodePos()
	condition := p.parseBinaryExpression(1)
	if p.parseOptional(QuestionToken) {
		whenTrue := p.parseAssignmentExpression()
		p.parseExpected(ColonToken, nil)
		whenFalse := p.parseAssignmentExpression()
		return p.finishNode(p.factory.NewConditionalExpression(condition, whenTrue, whenFalse), pos, -1)
	}
	return condition
}

func (p *Parser) parseBinaryExpression(minPrecedence int) *Node {
	pos := p.getNodePos()
	left := p.parseUnaryExpression()
	return p.parseBinaryExpressionRest(left, minPrecedence, pos)
}

func (p *Parser) parseBinaryExpressionRest(left *Node, minPrecedence, pos int) *Node {
	for {
		if p.token() == InstanceofKeyword {
			if relationalPrecedence < minPrecedence {
				break
			}
			p.nextToken()
			typ := p.parseType()
			var name *Node
			// SE16 type pattern: o instanceof String s. (Record patterns arrive
			// with the pattern slice.)
			if p.token() == Identifier {
				name = p.parseIdentifier()
			}
			left = p.finishNode(p.factory.NewInstanceofExpression(left, typ, name, nil), pos, -1)
			continue
		}
		p.reScanGreaterIfNeeded()
		precedence := binaryOperatorPrecedence(p.token())
		if precedence == 0 || precedence < minPrecedence {
			break
		}
		op := p.token()
		p.nextToken()
		right := p.parseBinaryExpression(precedence + 1)
		left = p.finishNode(p.factory.NewBinaryExpression(left, op, right), pos, -1)
	}
	return left
}

func (p *Parser) parseUnaryExpression() *Node {
	t := p.token()
	switch t {
	case PlusToken, MinusToken, TildeToken, ExclamationToken, PlusPlusToken, MinusMinusToken:
		pos := p.getNodePos()
		p.nextToken()
		operand := p.parseUnaryExpression()
		return p.finishNode(p.factory.NewPrefixUnaryExpression(t, operand), pos, -1)
	}
	if t == OpenParenToken && p.isCastExpression() {
		return p.parseCastExpression()
	}
	return p.parsePostfixExpression()
}

// isCastExpression distinguishes `(Type) operand` from a parenthesized
// expression by lookahead.
func (p *Parser) isCastExpression() bool {
	return parserLookAhead(p, func() bool {
		p.nextToken() // '('
		if p.token() == CloseParenToken {
			return false
		}
		primitive := isPrimitiveTypeKeyword(p.token()) || p.token() == VoidKeyword
		p.parseType()
		for p.token() == AmpersandToken { // SE8 intersection cast
			p.nextToken()
			p.parseType()
		}
		if p.token() != CloseParenToken {
			return false
		}
		p.nextToken() // ')'
		// A primitive cast is unambiguous; a reference cast must be followed by a
		// token that begins a unary operand but is not a binary-operator prefix,
		// so "(a) - b" stays a subtraction.
		if primitive {
			return p.isStartOfExpression()
		}
		return p.isStartOfReferenceCastOperand()
	})
}

func (p *Parser) isStartOfReferenceCastOperand() bool {
	switch p.token() {
	case Identifier, NumericLiteral, StringLiteral, CharacterLiteral, TextBlockLiteral,
		TrueKeyword, FalseKeyword, NullKeyword, OpenParenToken, ThisKeyword, SuperKeyword,
		NewKeyword, SwitchKeyword, ExclamationToken, TildeToken:
		return true
	default:
		return false
	}
}

func (p *Parser) parseCastExpression() *Node {
	pos := p.getNodePos()
	p.parseExpected(OpenParenToken, nil)
	typ := p.parseType()
	var bounds *NodeArray
	if p.token() == AmpersandToken {
		boundsPos := p.getNodePos()
		var list []*Node
		for p.parseOptional(AmpersandToken) {
			list = append(list, p.parseType())
		}
		bounds = p.createNodeArray(list, boundsPos, -1)
	}
	p.parseExpected(CloseParenToken, nil)
	expression := p.parseUnaryExpression()
	return p.finishNode(p.factory.NewCastExpression(typ, bounds, expression), pos, -1)
}

func (p *Parser) parsePostfixExpression() *Node {
	expr := p.parsePrimaryExpression()
	if p.token() == PlusPlusToken || p.token() == MinusMinusToken {
		operator := p.token()
		p.nextToken()
		return p.finishNode(p.factory.NewPostfixUnaryExpression(expr, operator), expr.Pos, -1)
	}
	return expr
}

func (p *Parser) parsePrimaryExpression() *Node {
	return p.parseExpressionSuffixes(p.parseAtom())
}

func (p *Parser) parseAtom() *Node {
	pos := p.getNodePos()
	switch p.token() {
	case NumericLiteral, StringLiteral, CharacterLiteral, TextBlockLiteral:
		kind := p.token()
		value := p.scanner.TokenValue()
		p.nextToken()
		return p.finishNode(p.factory.NewLiteralExpression(kind, value), pos, -1)
	case TrueKeyword, FalseKeyword, NullKeyword:
		return p.parseTokenNode()
	case ThisKeyword:
		p.nextToken()
		return p.finishNode(p.factory.NewThisExpression(nil), pos, -1)
	case SuperKeyword:
		p.nextToken()
		return p.finishNode(p.factory.NewSuperExpression(nil), pos, -1)
	case OpenParenToken:
		p.nextToken()
		expression := p.parseExpression()
		p.parseExpected(CloseParenToken, nil)
		return p.finishNode(p.factory.NewParenthesizedExpression(expression), pos, -1)
	case Identifier:
		return p.parseIdentifier()
	default:
		// new / switch-expression / primitive class literal arrive with later
		// slices; recover as a missing identifier for now.
		return p.createMissingNode(Identifier, false, &Diagnostics.ExpressionExpected)
	}
}

func (p *Parser) makePropertyAccess(expr, name *Node) *Node {
	return p.finishNode(p.factory.NewPropertyAccessExpression(expr, name), expr.Pos, -1)
}

func (p *Parser) parseArgumentList() *NodeArray {
	p.parseExpected(OpenParenToken, nil)
	args := p.parseDelimitedList(ctxArgumentExpressions, p.parseExpression)
	p.parseExpected(CloseParenToken, nil)
	return args
}

// parseExpressionSuffixes consumes the postfix chain: `.name`, calls and element
// access. (Class literals, qualified new/this/super and method references arrive
// with later slices.)
func (p *Parser) parseExpressionSuffixes(start *Node) *Node {
	expr := start
	for {
		exprPos := expr.Pos
		if p.token() == DotToken {
			p.nextToken()
			var typeArguments *NodeArray
			if p.token() == LessThanToken {
				typeArguments = p.parseTypeArguments()
			}
			name := p.parseIdentifier()
			if p.token() == OpenParenToken {
				target := p.makePropertyAccess(expr, name)
				args := p.parseArgumentList()
				expr = p.finishNode(p.factory.NewCallExpression(target, typeArguments, args), exprPos, -1)
			} else {
				expr = p.makePropertyAccess(expr, name)
			}
			continue
		}
		if p.token() == OpenBracketToken {
			p.nextToken()
			argumentExpression := p.parseExpression()
			p.parseExpected(CloseBracketToken, nil)
			expr = p.finishNode(p.factory.NewElementAccessExpression(expr, argumentExpression), exprPos, -1)
			continue
		}
		if p.token() == OpenParenToken {
			args := p.parseArgumentList()
			expr = p.finishNode(p.factory.NewCallExpression(expr, nil, args), exprPos, -1)
			continue
		}
		break
	}
	return expr
}
