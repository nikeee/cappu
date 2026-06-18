package compiler

// Statement and member node payloads. Field/child order mirrors
// src/compiler/types.ts. Port of the statement and member node interfaces.

// --- members -----------------------------------------------------------------

// MethodDeclarationData is a method (body absent for abstract/interface methods).
type MethodDeclarationData struct {
	Modifiers      *NodeArray
	TypeParameters *NodeArray
	ReturnType     *Node
	Name           *Node
	Parameters     *NodeArray
	Throws         *NodeArray
	Body           *Node // optional
	DefaultValue   *Node // optional (annotation element default)
}

func (d *MethodDeclarationData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Modifiers) || visitNodes(v, d.TypeParameters) || visit(v, d.ReturnType) ||
		visit(v, d.Name) || visitNodes(v, d.Parameters) || visitNodes(v, d.Throws) ||
		visit(v, d.DefaultValue) || visit(v, d.Body)
}

func (f *NodeFactory) NewMethodDeclaration(modifiers, typeParameters *NodeArray, returnType, name *Node, parameters, throws *NodeArray, body, defaultValue *Node) *Node {
	return f.newNode(MethodDeclaration, &MethodDeclarationData{
		Modifiers: modifiers, TypeParameters: typeParameters, ReturnType: returnType, Name: name,
		Parameters: parameters, Throws: throws, Body: body, DefaultValue: defaultValue,
	})
}

func (n *Node) AsMethodDeclaration() *MethodDeclarationData { return n.data.(*MethodDeclarationData) }

// ConstructorDeclarationData is a constructor.
type ConstructorDeclarationData struct {
	Modifiers      *NodeArray
	TypeParameters *NodeArray
	Name           *Node
	Parameters     *NodeArray
	Throws         *NodeArray
	Body           *Node
}

func (d *ConstructorDeclarationData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Modifiers) || visitNodes(v, d.TypeParameters) || visit(v, d.Name) ||
		visitNodes(v, d.Parameters) || visitNodes(v, d.Throws) || visit(v, d.Body)
}

func (f *NodeFactory) NewConstructorDeclaration(modifiers, typeParameters *NodeArray, name *Node, parameters, throws *NodeArray, body *Node) *Node {
	return f.newNode(ConstructorDeclaration, &ConstructorDeclarationData{
		Modifiers: modifiers, TypeParameters: typeParameters, Name: name, Parameters: parameters, Throws: throws, Body: body,
	})
}

func (n *Node) AsConstructorDeclaration() *ConstructorDeclarationData {
	return n.data.(*ConstructorDeclarationData)
}

// InitializerBlockData is a `static {}` or instance `{}` initializer.
type InitializerBlockData struct {
	IsStatic bool
	Body     *Node
}

func (d *InitializerBlockData) forEachChild(v Visitor) bool { return visit(v, d.Body) }

func (f *NodeFactory) NewInitializerBlock(isStatic bool, body *Node) *Node {
	return f.newNode(InitializerBlock, &InitializerBlockData{IsStatic: isStatic, Body: body})
}

func (n *Node) AsInitializerBlock() *InitializerBlockData { return n.data.(*InitializerBlockData) }

// ParameterData is a formal parameter (or receiver parameter).
type ParameterData struct {
	Modifiers          *NodeArray
	Type               *Node
	IsVarArgs          bool
	Name               *Node // absent for a receiver parameter
	ArrayRankAfterName int
	IsReceiver         bool
}

func (d *ParameterData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Modifiers) || visit(v, d.Type) || visit(v, d.Name)
}

func (f *NodeFactory) NewParameter(modifiers *NodeArray, typ *Node, isVarArgs bool, name *Node, arrayRank int, isReceiver bool) *Node {
	return f.newNode(Parameter, &ParameterData{Modifiers: modifiers, Type: typ, IsVarArgs: isVarArgs, Name: name, ArrayRankAfterName: arrayRank, IsReceiver: isReceiver})
}

func (n *Node) AsParameter() *ParameterData { return n.data.(*ParameterData) }

// --- statements --------------------------------------------------------------

// LocalVariableDeclarationStatementData is `[modifiers] type a = e, b;`.
type LocalVariableDeclarationStatementData struct {
	Modifiers   *NodeArray
	Type        *Node
	Declarators *NodeArray
}

func (d *LocalVariableDeclarationStatementData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Modifiers) || visit(v, d.Type) || visitNodes(v, d.Declarators)
}

func (f *NodeFactory) NewLocalVariableDeclarationStatement(modifiers *NodeArray, typ *Node, declarators *NodeArray) *Node {
	return f.newNode(LocalVariableDeclarationStatement, &LocalVariableDeclarationStatementData{Modifiers: modifiers, Type: typ, Declarators: declarators})
}

func (n *Node) AsLocalVariableDeclarationStatement() *LocalVariableDeclarationStatementData {
	return n.data.(*LocalVariableDeclarationStatementData)
}

// IfStatementData is `if (c) then [else e]`.
type IfStatementData struct {
	Condition     *Node
	ThenStatement *Node
	ElseStatement *Node
}

func (d *IfStatementData) forEachChild(v Visitor) bool {
	return visit(v, d.Condition) || visit(v, d.ThenStatement) || visit(v, d.ElseStatement)
}

func (f *NodeFactory) NewIfStatement(condition, then, els *Node) *Node {
	return f.newNode(IfStatement, &IfStatementData{Condition: condition, ThenStatement: then, ElseStatement: els})
}

func (n *Node) AsIfStatement() *IfStatementData { return n.data.(*IfStatementData) }

// WhileStatementData is `while (c) s`.
type WhileStatementData struct {
	Condition *Node
	Statement *Node
}

func (d *WhileStatementData) forEachChild(v Visitor) bool {
	return visit(v, d.Condition) || visit(v, d.Statement)
}

func (f *NodeFactory) NewWhileStatement(condition, statement *Node) *Node {
	return f.newNode(WhileStatement, &WhileStatementData{Condition: condition, Statement: statement})
}

func (n *Node) AsWhileStatement() *WhileStatementData { return n.data.(*WhileStatementData) }

// DoStatementData is `do s while (c);`.
type DoStatementData struct {
	Statement *Node
	Condition *Node
}

func (d *DoStatementData) forEachChild(v Visitor) bool {
	return visit(v, d.Statement) || visit(v, d.Condition)
}

func (f *NodeFactory) NewDoStatement(statement, condition *Node) *Node {
	return f.newNode(DoStatement, &DoStatementData{Statement: statement, Condition: condition})
}

func (n *Node) AsDoStatement() *DoStatementData { return n.data.(*DoStatementData) }

// ForStatementData is a C-style for.
type ForStatementData struct {
	Initializer            *Node      // local-var-decl form, optional
	InitializerExpressions *NodeArray // statement-expression-list form, optional
	Condition              *Node      // optional
	Incrementors           *NodeArray // optional
	Statement              *Node
}

func (d *ForStatementData) forEachChild(v Visitor) bool {
	return visit(v, d.Initializer) || visitNodes(v, d.InitializerExpressions) ||
		visit(v, d.Condition) || visitNodes(v, d.Incrementors) || visit(v, d.Statement)
}

func (f *NodeFactory) NewForStatement(initializer *Node, initializerExpressions *NodeArray, condition *Node, incrementors *NodeArray, statement *Node) *Node {
	return f.newNode(ForStatement, &ForStatementData{Initializer: initializer, InitializerExpressions: initializerExpressions, Condition: condition, Incrementors: incrementors, Statement: statement})
}

func (n *Node) AsForStatement() *ForStatementData { return n.data.(*ForStatementData) }

// ForEachStatementData is `for (param : expr) s`.
type ForEachStatementData struct {
	Parameter  *Node
	Expression *Node
	Statement  *Node
}

func (d *ForEachStatementData) forEachChild(v Visitor) bool {
	return visit(v, d.Parameter) || visit(v, d.Expression) || visit(v, d.Statement)
}

func (f *NodeFactory) NewForEachStatement(parameter, expression, statement *Node) *Node {
	return f.newNode(ForEachStatement, &ForEachStatementData{Parameter: parameter, Expression: expression, Statement: statement})
}

func (n *Node) AsForEachStatement() *ForEachStatementData { return n.data.(*ForEachStatementData) }

// ThrowStatementData is `throw e;`.
type ThrowStatementData struct{ Expression *Node }

func (d *ThrowStatementData) forEachChild(v Visitor) bool { return visit(v, d.Expression) }

func (f *NodeFactory) NewThrowStatement(expr *Node) *Node {
	return f.newNode(ThrowStatement, &ThrowStatementData{Expression: expr})
}

func (n *Node) AsThrowStatement() *ThrowStatementData { return n.data.(*ThrowStatementData) }

// LabelStatementData backs both break and continue (the node Kind distinguishes).
type LabelStatementData struct{ Label *Node } // optional

func (d *LabelStatementData) forEachChild(v Visitor) bool { return visit(v, d.Label) }

func (f *NodeFactory) NewBreakOrContinue(kind SyntaxKind, label *Node) *Node {
	return f.newNode(kind, &LabelStatementData{Label: label})
}

func (n *Node) AsLabelStatement() *LabelStatementData { return n.data.(*LabelStatementData) }

// SynchronizedStatementData is `synchronized (e) {}`.
type SynchronizedStatementData struct {
	Expression *Node
	Body       *Node
}

func (d *SynchronizedStatementData) forEachChild(v Visitor) bool {
	return visit(v, d.Expression) || visit(v, d.Body)
}

func (f *NodeFactory) NewSynchronizedStatement(expr, body *Node) *Node {
	return f.newNode(SynchronizedStatement, &SynchronizedStatementData{Expression: expr, Body: body})
}

func (n *Node) AsSynchronizedStatement() *SynchronizedStatementData {
	return n.data.(*SynchronizedStatementData)
}

// AssertStatementData is `assert c [: message];`.
type AssertStatementData struct {
	Condition *Node
	Message   *Node // optional
}

func (d *AssertStatementData) forEachChild(v Visitor) bool {
	return visit(v, d.Condition) || visit(v, d.Message)
}

func (f *NodeFactory) NewAssertStatement(condition, message *Node) *Node {
	return f.newNode(AssertStatement, &AssertStatementData{Condition: condition, Message: message})
}

func (n *Node) AsAssertStatement() *AssertStatementData { return n.data.(*AssertStatementData) }

// LabeledStatementData is `label: statement`.
type LabeledStatementData struct {
	Label     *Node
	Statement *Node
}

func (d *LabeledStatementData) forEachChild(v Visitor) bool {
	return visit(v, d.Label) || visit(v, d.Statement)
}

func (f *NodeFactory) NewLabeledStatement(label, statement *Node) *Node {
	return f.newNode(LabeledStatement, &LabeledStatementData{Label: label, Statement: statement})
}

func (n *Node) AsLabeledStatement() *LabeledStatementData { return n.data.(*LabeledStatementData) }

// YieldStatementData is `yield e;`.
type YieldStatementData struct{ Expression *Node }

func (d *YieldStatementData) forEachChild(v Visitor) bool { return visit(v, d.Expression) }

func (f *NodeFactory) NewYieldStatement(expr *Node) *Node {
	return f.newNode(YieldStatement, &YieldStatementData{Expression: expr})
}

func (n *Node) AsYieldStatement() *YieldStatementData { return n.data.(*YieldStatementData) }

// --- try / switch ------------------------------------------------------------

// ResourceData is one try-with-resources resource (declaration or variable form).
type ResourceData struct {
	Modifiers   *NodeArray
	Type        *Node // declaration form
	Name        *Node
	Initializer *Node
	Expression  *Node // variable-access form
}

func (d *ResourceData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Modifiers) || visit(v, d.Type) || visit(v, d.Name) ||
		visit(v, d.Initializer) || visit(v, d.Expression)
}

func (f *NodeFactory) NewResource(modifiers *NodeArray, typ, name, initializer, expression *Node) *Node {
	return f.newNode(Resource, &ResourceData{Modifiers: modifiers, Type: typ, Name: name, Initializer: initializer, Expression: expression})
}

func (n *Node) AsResource() *ResourceData { return n.data.(*ResourceData) }

// CatchClauseData is `catch (A | B e) {}`.
type CatchClauseData struct {
	CatchTypes *NodeArray
	Name       *Node
	Block      *Node
}

func (d *CatchClauseData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.CatchTypes) || visit(v, d.Name) || visit(v, d.Block)
}

func (f *NodeFactory) NewCatchClause(catchTypes *NodeArray, name, block *Node) *Node {
	return f.newNode(CatchClause, &CatchClauseData{CatchTypes: catchTypes, Name: name, Block: block})
}

func (n *Node) AsCatchClause() *CatchClauseData { return n.data.(*CatchClauseData) }

// TryStatementData is `try [(resources)] block catch... [finally]`.
type TryStatementData struct {
	Resources    *NodeArray
	TryBlock     *Node
	CatchClauses *NodeArray
	FinallyBlock *Node
}

func (d *TryStatementData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Resources) || visit(v, d.TryBlock) || visitNodes(v, d.CatchClauses) || visit(v, d.FinallyBlock)
}

func (f *NodeFactory) NewTryStatement(resources *NodeArray, tryBlock *Node, catchClauses *NodeArray, finallyBlock *Node) *Node {
	return f.newNode(TryStatement, &TryStatementData{Resources: resources, TryBlock: tryBlock, CatchClauses: catchClauses, FinallyBlock: finallyBlock})
}

func (n *Node) AsTryStatement() *TryStatementData { return n.data.(*TryStatementData) }

// SwitchClauseData is one case/default clause (colon or arrow form).
type SwitchClauseData struct {
	IsDefault  bool
	IsArrow    bool
	Labels     *NodeArray // case label elements, optional
	Guard      *Node      // SE21 `when` guard, optional
	Statements *NodeArray
}

func (d *SwitchClauseData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Labels) || visit(v, d.Guard) || visitNodes(v, d.Statements)
}

func (f *NodeFactory) NewSwitchClause(isDefault, isArrow bool, labels *NodeArray, guard *Node, statements *NodeArray) *Node {
	return f.newNode(SwitchClause, &SwitchClauseData{IsDefault: isDefault, IsArrow: isArrow, Labels: labels, Guard: guard, Statements: statements})
}

func (n *Node) AsSwitchClause() *SwitchClauseData { return n.data.(*SwitchClauseData) }

// SwitchStatementData is `switch (e) { clauses }`.
type SwitchStatementData struct {
	Expression *Node
	Clauses    *NodeArray
}

func (d *SwitchStatementData) forEachChild(v Visitor) bool {
	return visit(v, d.Expression) || visitNodes(v, d.Clauses)
}

func (f *NodeFactory) NewSwitchStatement(expression *Node, clauses *NodeArray) *Node {
	return f.newNode(SwitchStatement, &SwitchStatementData{Expression: expression, Clauses: clauses})
}

func (n *Node) AsSwitchStatement() *SwitchStatementData { return n.data.(*SwitchStatementData) }

// ArrayInitializerData is `{ e, e, ... }`.
type ArrayInitializerData struct{ Elements *NodeArray }

func (d *ArrayInitializerData) forEachChild(v Visitor) bool { return visitNodes(v, d.Elements) }

func (f *NodeFactory) NewArrayInitializer(elements *NodeArray) *Node {
	return f.newNode(ArrayInitializer, &ArrayInitializerData{Elements: elements})
}

func (n *Node) AsArrayInitializer() *ArrayInitializerData { return n.data.(*ArrayInitializerData) }
