package compiler

// Node payloads for the "exotic" expression forms (creation, lambdas, method
// references, switch expressions, class literals), record/type patterns, and
// record compact constructors. Port of the remaining node interfaces in
// src/compiler/types.ts.

// ObjectCreationExpressionData is `new Type(args) [classBody]` (or qualified
// `outer.new Inner(args)`).
type ObjectCreationExpressionData struct {
	Type      *Node
	Arguments *NodeArray
	ClassBody *NodeArray // anonymous class body, optional
	Qualifier *Node      // optional (outer.new)
}

func (d *ObjectCreationExpressionData) forEachChild(v Visitor) bool {
	return visit(v, d.Qualifier) || visit(v, d.Type) || visitNodes(v, d.Arguments) || visitNodes(v, d.ClassBody)
}

func (f *NodeFactory) NewObjectCreationExpression(typ *Node, arguments *NodeArray, classBody *NodeArray, qualifier *Node) *Node {
	return f.newNode(ObjectCreationExpression, &ObjectCreationExpressionData{Type: typ, Arguments: arguments, ClassBody: classBody, Qualifier: qualifier})
}

func (n *Node) AsObjectCreationExpression() *ObjectCreationExpressionData {
	return n.data.(*ObjectCreationExpressionData)
}

// ArrayCreationExpressionData is `new T[dims][]{...}`.
type ArrayCreationExpressionData struct {
	ElementType    *Node
	Dimensions     *NodeArray
	AdditionalRank int
	Initializer    *Node // optional
}

func (d *ArrayCreationExpressionData) forEachChild(v Visitor) bool {
	return visit(v, d.ElementType) || visitNodes(v, d.Dimensions) || visit(v, d.Initializer)
}

func (f *NodeFactory) NewArrayCreationExpression(elementType *Node, dimensions *NodeArray, additionalRank int, initializer *Node) *Node {
	return f.newNode(ArrayCreationExpression, &ArrayCreationExpressionData{ElementType: elementType, Dimensions: dimensions, AdditionalRank: additionalRank, Initializer: initializer})
}

func (n *Node) AsArrayCreationExpression() *ArrayCreationExpressionData {
	return n.data.(*ArrayCreationExpressionData)
}

// LambdaExpressionData is `params -> body`.
type LambdaExpressionData struct {
	Parameters *NodeArray
	Body       *Node // Expression or Block
}

func (d *LambdaExpressionData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Parameters) || visit(v, d.Body)
}

func (f *NodeFactory) NewLambdaExpression(parameters *NodeArray, body *Node) *Node {
	return f.newNode(LambdaExpression, &LambdaExpressionData{Parameters: parameters, Body: body})
}

func (n *Node) AsLambdaExpression() *LambdaExpressionData { return n.data.(*LambdaExpressionData) }

// MethodReferenceExpressionData is `expr::[typeArgs](name|new)`.
type MethodReferenceExpressionData struct {
	Expression       *Node
	TypeArguments    *NodeArray
	Name             *Node // absent for a constructor reference
	IsConstructorRef bool
}

func (d *MethodReferenceExpressionData) forEachChild(v Visitor) bool {
	return visit(v, d.Expression) || visitNodes(v, d.TypeArguments) || visit(v, d.Name)
}

func (f *NodeFactory) NewMethodReferenceExpression(expr *Node, typeArguments *NodeArray, name *Node, isConstructorRef bool) *Node {
	return f.newNode(MethodReferenceExpression, &MethodReferenceExpressionData{Expression: expr, TypeArguments: typeArguments, Name: name, IsConstructorRef: isConstructorRef})
}

func (n *Node) AsMethodReferenceExpression() *MethodReferenceExpressionData {
	return n.data.(*MethodReferenceExpressionData)
}

// ClassLiteralExpressionData is `Type.class`.
type ClassLiteralExpressionData struct{ Type *Node }

func (d *ClassLiteralExpressionData) forEachChild(v Visitor) bool { return visit(v, d.Type) }

func (f *NodeFactory) NewClassLiteralExpression(typ *Node) *Node {
	return f.newNode(ClassLiteralExpression, &ClassLiteralExpressionData{Type: typ})
}

func (n *Node) AsClassLiteralExpression() *ClassLiteralExpressionData {
	return n.data.(*ClassLiteralExpressionData)
}

// SwitchExpressionData is `switch (e) { clauses }` in value position.
type SwitchExpressionData struct {
	Expression *Node
	Clauses    *NodeArray
}

func (d *SwitchExpressionData) forEachChild(v Visitor) bool {
	return visit(v, d.Expression) || visitNodes(v, d.Clauses)
}

func (f *NodeFactory) NewSwitchExpression(expression *Node, clauses *NodeArray) *Node {
	return f.newNode(SwitchExpression, &SwitchExpressionData{Expression: expression, Clauses: clauses})
}

func (n *Node) AsSwitchExpression() *SwitchExpressionData { return n.data.(*SwitchExpressionData) }

// TypePatternData is `Type name` (SE16).
type TypePatternData struct {
	Type *Node
	Name *Node
}

func (d *TypePatternData) forEachChild(v Visitor) bool { return visit(v, d.Type) || visit(v, d.Name) }

func (f *NodeFactory) NewTypePattern(typ, name *Node) *Node {
	return f.newNode(TypePattern, &TypePatternData{Type: typ, Name: name})
}

func (n *Node) AsTypePattern() *TypePatternData { return n.data.(*TypePatternData) }

// RecordPatternData is `Type(patterns)` (SE21 deconstruction).
type RecordPatternData struct {
	Type     *Node
	Patterns *NodeArray
}

func (d *RecordPatternData) forEachChild(v Visitor) bool {
	return visit(v, d.Type) || visitNodes(v, d.Patterns)
}

func (f *NodeFactory) NewRecordPattern(typ *Node, patterns *NodeArray) *Node {
	return f.newNode(RecordPattern, &RecordPatternData{Type: typ, Patterns: patterns})
}

func (n *Node) AsRecordPattern() *RecordPatternData { return n.data.(*RecordPatternData) }

// MatchAllPatternData is the unnamed pattern `_`.
type MatchAllPatternData struct{}

func (d *MatchAllPatternData) forEachChild(Visitor) bool { return false }

func (f *NodeFactory) NewMatchAllPattern() *Node {
	return f.newNode(MatchAllPattern, &MatchAllPatternData{})
}

// CompactConstructorDeclarationData is a record's `Name { ... }` compact constructor.
type CompactConstructorDeclarationData struct {
	Modifiers *NodeArray
	Name      *Node
	Body      *Node
}

func (d *CompactConstructorDeclarationData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Modifiers) || visit(v, d.Name) || visit(v, d.Body)
}

func (f *NodeFactory) NewCompactConstructorDeclaration(modifiers *NodeArray, name, body *Node) *Node {
	return f.newNode(CompactConstructorDeclaration, &CompactConstructorDeclarationData{Modifiers: modifiers, Name: name, Body: body})
}

func (n *Node) AsCompactConstructorDeclaration() *CompactConstructorDeclarationData {
	return n.data.(*CompactConstructorDeclarationData)
}
