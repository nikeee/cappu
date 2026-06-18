package compiler

// Recursive-descent parser core. The TS original uses module-level mutable
// state; following tsgo this becomes a Parser struct with methods. finishNode
// stamps pos/end and the error flag; parseList/parseDelimitedList do
// context-aware error recovery that always makes forward progress. Port of the
// parser core in src/compiler/parser.ts.
//
// Grammar coverage grows in slices: this step is the compilation-unit skeleton
// (empty statements + recovery + EOF). isStartOfStatement is scoped to ';'
// until the type-declaration grammar is ported.

// ParsingContext identifies the list currently being parsed (for terminators
// and recovery). Values mirror the TS enum.
type ParsingContext int

const (
	ctxSourceElements ParsingContext = iota
	ctxBlockStatements
	ctxClassMembers
	ctxEnumConstants
	ctxTypeArguments
	ctxTypeParameters
	ctxParameters
	ctxArgumentExpressions
	ctxVariableDeclarations
	ctxArrayInitializerElements
	ctxSwitchClauses
	ctxCatchClauses
	ctxModuleDirectives
	ctxAnnotationValues
	ctxCount
)

// Parser holds the per-parse mutable state.
type Parser struct {
	scanner                          *Scanner
	factory                          NodeFactory
	sourceText                       string
	fileName                         string
	currentToken                     SyntaxKind
	parseDiagnostics                 []Diagnostic
	parsingContext                   int
	parseErrorBeforeNextFinishedNode bool
}

// ParseSourceFile parses text into a SourceFile node.
func ParseSourceFile(fileName, text string) *Node {
	p := &Parser{sourceText: text, fileName: fileName, currentToken: Unknown}
	p.scanner = NewScanner(text, func(message DiagnosticMessage, errPos, length int) {
		p.parseErrorAtPosition(errPos, length, message)
	})

	p.nextToken()
	pos := p.getNodePos()
	var pkg *Node
	if p.token() == PackageKeyword {
		pkg = p.parsePackageDeclaration()
	}
	imports := p.parseImportDeclarations()
	// module declarations and type declarations arrive with later grammar slices.
	statements := p.parseList(ctxSourceElements, p.parseSourceElement)
	eof := p.parseExpectedToken(EndOfFileToken, nil)

	sf := p.factory.NewSourceFile(pkg, imports, statements, eof, nil)
	p.finishNode(sf, pos, -1)
	data := sf.AsSourceFile()
	data.FileName = p.fileName
	data.Text = p.sourceText
	data.ParseDiagnostics = p.parseDiagnostics
	return sf
}

func (p *Parser) token() SyntaxKind     { return p.currentToken }
func (p *Parser) nextToken() SyntaxKind { p.currentToken = p.scanner.Scan(); return p.currentToken }
func (p *Parser) getNodePos() int       { return p.scanner.TokenFullStart() }

// finishNode stamps pos/end (end<0 means "current full start") and the error flag.
func (p *Parser) finishNode(node *Node, pos, end int) *Node {
	if end < 0 {
		end = p.scanner.TokenFullStart()
	}
	node.Pos = pos
	node.End = end
	if p.parseErrorBeforeNextFinishedNode {
		p.parseErrorBeforeNextFinishedNode = false
		node.Flags |= NodeFlagThisNodeHasError
	}
	return node
}

func (p *Parser) createNodeArray(nodes []*Node, pos, end int) *NodeArray {
	if end < 0 {
		end = p.getNodePos()
	}
	return &NodeArray{Nodes: nodes, Pos: pos, End: end}
}

// --- diagnostics -------------------------------------------------------------

func (p *Parser) parseErrorAtPosition(start, length int, message DiagnosticMessage, args ...string) {
	if n := len(p.parseDiagnostics); n == 0 || start != p.parseDiagnostics[n-1].Pos {
		p.parseDiagnostics = append(p.parseDiagnostics, CreateDiagnostic(start, length, message, args...))
	}
	// Tell the next finishNode that the node it completes spans a parse error.
	p.parseErrorBeforeNextFinishedNode = true
}

func (p *Parser) parseErrorAt(start, end int, message DiagnosticMessage, args ...string) {
	p.parseErrorAtPosition(start, end-start, message, args...)
}

func (p *Parser) parseErrorAtCurrentToken(message DiagnosticMessage, args ...string) {
	p.parseErrorAt(p.scanner.TokenStart(), p.scanner.TokenEnd(), message, args...)
}

// --- token consumption -------------------------------------------------------

func (p *Parser) parseExpected(kind SyntaxKind, message *DiagnosticMessage) bool {
	if p.token() == kind {
		p.nextToken()
		return true
	}
	if message != nil {
		p.parseErrorAtCurrentToken(*message)
	} else {
		p.parseErrorAtCurrentToken(Diagnostics.Expected0, tokenToString(kind))
	}
	return false
}

func (p *Parser) parseOptional(kind SyntaxKind) bool {
	if p.token() == kind {
		p.nextToken()
		return true
	}
	return false
}

func (p *Parser) parseTokenNode() *Node {
	pos := p.getNodePos()
	kind := p.token()
	p.nextToken()
	return p.finishNode(p.factory.newToken(kind), pos, -1)
}

func (p *Parser) parseExpectedToken(kind SyntaxKind, message *DiagnosticMessage) *Node {
	if p.token() == kind {
		return p.parseTokenNode()
	}
	return p.createMissingNode(kind, false, message, tokenToString(kind))
}

func (p *Parser) createMissingNode(kind SyntaxKind, reportAtCurrentPosition bool, message *DiagnosticMessage, args ...string) *Node {
	switch {
	case reportAtCurrentPosition:
		m := Diagnostics.Expected0
		if message != nil {
			m = *message
		}
		p.parseErrorAtPosition(p.scanner.TokenFullStart(), 0, m, args...)
	case message != nil:
		p.parseErrorAtCurrentToken(*message, args...)
	}
	pos := p.getNodePos()
	var node *Node
	if kind == Identifier {
		node = p.factory.NewIdentifier("")
	} else {
		node = p.factory.newToken(kind)
	}
	return p.finishNode(node, pos, -1)
}

// parserLookAhead peeks with cb without consuming input: it saves and restores
// the parser-level state (current token, diagnostics, error flag) on top of the
// scanner's lookahead. Mirrors speculationHelper(callback, isLookahead=true).
func parserLookAhead[T any](p *Parser, cb func() T) T {
	saveToken := p.currentToken
	saveLen := len(p.parseDiagnostics)
	saveErr := p.parseErrorBeforeNextFinishedNode
	result := LookAhead(p.scanner, cb)
	p.currentToken = saveToken
	p.parseDiagnostics = p.parseDiagnostics[:saveLen]
	p.parseErrorBeforeNextFinishedNode = saveErr
	return result
}

func (p *Parser) parseIdentifier() *Node {
	if p.token() == Identifier {
		pos := p.getNodePos()
		text := p.scanner.TokenValue()
		p.nextToken()
		return p.finishNode(p.factory.NewIdentifier(text), pos, -1)
	}
	return p.createMissingNode(Identifier, false, &Diagnostics.IdentifierExpected)
}

// --- list parsing with error recovery ----------------------------------------

func (p *Parser) isListTerminator(context ParsingContext) bool {
	if p.token() == EndOfFileToken {
		return true
	}
	switch context {
	case ctxBlockStatements, ctxClassMembers, ctxEnumConstants, ctxSwitchClauses,
		ctxArrayInitializerElements, ctxModuleDirectives:
		return p.token() == CloseBraceToken
	case ctxParameters, ctxArgumentExpressions, ctxAnnotationValues:
		return p.token() == CloseParenToken
	case ctxTypeArguments, ctxTypeParameters:
		return p.token() == GreaterThanToken
	default:
		return false
	}
}

func (p *Parser) isListElement(context ParsingContext) bool {
	switch context {
	case ctxSourceElements:
		return p.isStartOfStatement()
	case ctxClassMembers:
		return p.isStartOfClassMember()
	case ctxTypeArguments:
		return p.isStartOfType()
	case ctxTypeParameters:
		return p.token() == Identifier || p.token() == AtToken
	case ctxParameters:
		return p.isStartOfParameter()
	case ctxArgumentExpressions:
		return p.isStartOfExpression()
	default:
		return false
	}
}

func (p *Parser) isInSomeParsingContext() bool {
	for context := ParsingContext(0); context < ctxCount; context++ {
		if p.parsingContext&(1<<context) != 0 {
			if p.isListElement(context) || p.isListTerminator(context) {
				return true
			}
		}
	}
	return false
}

func (p *Parser) parsingContextErrors(context ParsingContext) {
	switch context {
	case ctxSourceElements, ctxBlockStatements:
		p.parseErrorAtCurrentToken(Diagnostics.DeclarationOrStatementExpected)
	case ctxParameters:
		p.parseErrorAtCurrentToken(Diagnostics.ParameterDeclarationExpected)
	default:
		p.parseErrorAtCurrentToken(Diagnostics.UnexpectedToken)
	}
}

// abortParsingListOrMoveToNextToken aborts the current list when the token is
// valid in an enclosing context, else skips it. Always makes progress.
func (p *Parser) abortParsingListOrMoveToNextToken(context ParsingContext) bool {
	p.parsingContextErrors(context)
	if p.isInSomeParsingContext() {
		return true
	}
	p.nextToken()
	return false
}

func (p *Parser) parseList(context ParsingContext, parseElement func() *Node) *NodeArray {
	saveParsingContext := p.parsingContext
	p.parsingContext |= 1 << context
	var list []*Node
	listPos := p.getNodePos()

	for !p.isListTerminator(context) {
		if p.isListElement(context) {
			list = append(list, parseElement())
			continue
		}
		if p.abortParsingListOrMoveToNextToken(context) {
			break
		}
	}

	p.parsingContext = saveParsingContext
	return p.createNodeArray(list, listPos, -1)
}

// parseDelimitedList parses a comma-separated list, with the "skip a token if no
// progress" guard so malformed input cannot loop. Port of parseDelimitedList.
func (p *Parser) parseDelimitedList(context ParsingContext, parseElement func() *Node) *NodeArray {
	saveParsingContext := p.parsingContext
	p.parsingContext |= 1 << context
	var list []*Node
	listPos := p.getNodePos()

	for {
		if p.isListElement(context) {
			startPos := p.scanner.TokenFullStart()
			list = append(list, parseElement())
			if p.parseOptional(CommaToken) {
				continue
			}
			if p.isListTerminator(context) {
				break
			}
			p.parseExpected(CommaToken, nil)
			if startPos == p.scanner.TokenFullStart() {
				p.nextToken()
			}
			continue
		}
		if p.isListTerminator(context) {
			break
		}
		if p.abortParsingListOrMoveToNextToken(context) {
			break
		}
	}

	p.parsingContext = saveParsingContext
	return p.createNodeArray(list, listPos, -1)
}

// --- names -------------------------------------------------------------------

func (p *Parser) makeQualifiedName(left, right *Node) *Node {
	return p.finishNode(p.factory.NewQualifiedName(left, right), left.Pos, -1)
}

func (p *Parser) parseEntityName() *Node {
	entity := p.parseIdentifier()
	// Only consume a dot followed by an identifier, so a ".<T>" generic call is
	// left for the expression parser rather than eaten here.
	for p.token() == DotToken && parserLookAhead(p, func() bool { p.nextToken(); return p.token() == Identifier }) {
		p.parseExpected(DotToken, nil)
		entity = p.makeQualifiedName(entity, p.parseIdentifier())
	}
	return entity
}

// --- compilation unit --------------------------------------------------------

func (p *Parser) parsePackageDeclaration() *Node {
	pos := p.getNodePos()
	p.parseExpected(PackageKeyword, nil)
	name := p.parseEntityName()
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewPackageDeclaration(nil, name), pos, -1)
}

func (p *Parser) parseImportDeclaration() *Node {
	pos := p.getNodePos()
	p.parseExpected(ImportKeyword, nil)
	isStatic := p.parseOptional(StaticKeyword)
	name := p.parseIdentifier()
	isOnDemand := false
	for p.parseOptional(DotToken) {
		if p.token() == AsteriskToken {
			p.nextToken()
			isOnDemand = true
			break
		}
		name = p.makeQualifiedName(name, p.parseIdentifier())
	}
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewImportDeclaration(isStatic, name, isOnDemand), pos, -1)
}

func (p *Parser) parseImportDeclarations() *NodeArray {
	pos := p.getNodePos()
	var list []*Node
	for p.token() == ImportKeyword {
		list = append(list, p.parseImportDeclaration())
	}
	return p.createNodeArray(list, pos, -1)
}

// --- statements --------------------------------------------------------------

func (p *Parser) parseSourceElement() *Node {
	if p.token() == SemicolonToken {
		return p.parseEmptyStatement()
	}
	return p.parseTypeDeclaration()
}

func (p *Parser) parseEmptyStatement() *Node {
	pos := p.getNodePos()
	p.parseExpected(SemicolonToken, nil)
	return p.finishNode(p.factory.NewEmptyStatement(), pos, -1)
}
