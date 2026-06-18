package compiler

// Concrete AST node payloads, factory constructors and forEachChild methods.
// Field names and child order mirror src/compiler/types.ts so traversal order
// matches the Node build. This is the foundational set the early grammar needs;
// remaining kinds are added as the parser reaches them. Port of the node
// interfaces in types.ts + their forEachChild in parser.ts.

// --- names -------------------------------------------------------------------

// IdentifierData is an Identifier's payload.
type IdentifierData struct{ Text string }

func (d *IdentifierData) forEachChild(Visitor) bool { return false }

func (f *NodeFactory) NewIdentifier(text string) *Node {
	return f.newNode(Identifier, &IdentifierData{Text: text})
}

// AsIdentifier recovers the Identifier payload.
func (n *Node) AsIdentifier() *IdentifierData { return n.data.(*IdentifierData) }

// QualifiedNameData is "left.right" (an EntityName).
type QualifiedNameData struct {
	Left  *Node // EntityName
	Right *Node // Identifier
}

func (d *QualifiedNameData) forEachChild(v Visitor) bool {
	return visit(v, d.Left) || visit(v, d.Right)
}

func (f *NodeFactory) NewQualifiedName(left, right *Node) *Node {
	return f.newNode(QualifiedName, &QualifiedNameData{Left: left, Right: right})
}

func (n *Node) AsQualifiedName() *QualifiedNameData { return n.data.(*QualifiedNameData) }

// --- type nodes --------------------------------------------------------------

// PrimitiveTypeData carries the primitive keyword kind (IntKeyword, ...).
type PrimitiveTypeData struct{ Keyword SyntaxKind }

func (d *PrimitiveTypeData) forEachChild(Visitor) bool { return false }

func (f *NodeFactory) NewPrimitiveType(keyword SyntaxKind) *Node {
	return f.newNode(PrimitiveType, &PrimitiveTypeData{Keyword: keyword})
}

func (n *Node) AsPrimitiveType() *PrimitiveTypeData { return n.data.(*PrimitiveTypeData) }

// VarTypeData is the SE10 inferred 'var' type (no children).
type VarTypeData struct{}

func (d *VarTypeData) forEachChild(Visitor) bool { return false }

func (f *NodeFactory) NewVarType() *Node { return f.newNode(VarType, &VarTypeData{}) }

// TypeReferenceData is a named type with optional type arguments.
type TypeReferenceData struct {
	TypeName      *Node      // EntityName
	TypeArguments *NodeArray // TypeNode | WildcardType
}

func (d *TypeReferenceData) forEachChild(v Visitor) bool {
	return visit(v, d.TypeName) || visitNodes(v, d.TypeArguments)
}

func (f *NodeFactory) NewTypeReference(typeName *Node, typeArguments *NodeArray) *Node {
	return f.newNode(TypeReference, &TypeReferenceData{TypeName: typeName, TypeArguments: typeArguments})
}

func (n *Node) AsTypeReference() *TypeReferenceData { return n.data.(*TypeReferenceData) }

// ArrayTypeData is `elementType[]`.
type ArrayTypeData struct{ ElementType *Node }

func (d *ArrayTypeData) forEachChild(v Visitor) bool { return visit(v, d.ElementType) }

func (f *NodeFactory) NewArrayType(elementType *Node) *Node {
	return f.newNode(ArrayType, &ArrayTypeData{ElementType: elementType})
}

func (n *Node) AsArrayType() *ArrayTypeData { return n.data.(*ArrayTypeData) }

// WildcardTypeData is `?`, `? extends T` or `? super T`.
type WildcardTypeData struct {
	HasExtends bool
	HasSuper   bool
	Type       *Node // optional
}

func (d *WildcardTypeData) forEachChild(v Visitor) bool { return visit(v, d.Type) }

func (f *NodeFactory) NewWildcardType(hasExtends, hasSuper bool, typ *Node) *Node {
	return f.newNode(WildcardType, &WildcardTypeData{HasExtends: hasExtends, HasSuper: hasSuper, Type: typ})
}

func (n *Node) AsWildcardType() *WildcardTypeData { return n.data.(*WildcardTypeData) }

// --- compilation unit --------------------------------------------------------

// SourceFileData is the root of a parsed file.
type SourceFileData struct {
	PackageDeclaration *Node      // optional
	Imports            *NodeArray // ImportDeclaration
	Statements         *NodeArray // top-level type declarations
	EndOfFileToken     *Node
	ModuleDeclaration  *Node // optional (module-info.java)
	FileName           string
	Text               string
	ParseDiagnostics   []Diagnostic
	BindDiagnostics    []Diagnostic
}

func (d *SourceFileData) forEachChild(v Visitor) bool {
	return visit(v, d.PackageDeclaration) ||
		visitNodes(v, d.Imports) ||
		visit(v, d.ModuleDeclaration) ||
		visitNodes(v, d.Statements) ||
		visit(v, d.EndOfFileToken)
}

func (f *NodeFactory) NewSourceFile(pkg *Node, imports, statements *NodeArray, eof, module *Node) *Node {
	return f.newNode(SourceFile, &SourceFileData{
		PackageDeclaration: pkg, Imports: imports, Statements: statements,
		EndOfFileToken: eof, ModuleDeclaration: module,
	})
}

func (n *Node) AsSourceFile() *SourceFileData { return n.data.(*SourceFileData) }

// PackageDeclarationData is `package a.b.c;` with optional annotations.
type PackageDeclarationData struct {
	Annotations *NodeArray
	Name        *Node // EntityName
}

func (d *PackageDeclarationData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Annotations) || visit(v, d.Name)
}

func (f *NodeFactory) NewPackageDeclaration(annotations *NodeArray, name *Node) *Node {
	return f.newNode(PackageDeclaration, &PackageDeclarationData{Annotations: annotations, Name: name})
}

func (n *Node) AsPackageDeclaration() *PackageDeclarationData {
	return n.data.(*PackageDeclarationData)
}

// ImportDeclarationData is `import [static] a.b.*?;`.
type ImportDeclarationData struct {
	IsStatic   bool
	Name       *Node // EntityName
	IsOnDemand bool
}

func (d *ImportDeclarationData) forEachChild(v Visitor) bool { return visit(v, d.Name) }

func (f *NodeFactory) NewImportDeclaration(isStatic bool, name *Node, isOnDemand bool) *Node {
	return f.newNode(ImportDeclaration, &ImportDeclarationData{IsStatic: isStatic, Name: name, IsOnDemand: isOnDemand})
}

func (n *Node) AsImportDeclaration() *ImportDeclarationData {
	return n.data.(*ImportDeclarationData)
}

// EmptyStatementData is `;`.
type EmptyStatementData struct{}

func (d *EmptyStatementData) forEachChild(Visitor) bool { return false }

func (f *NodeFactory) NewEmptyStatement() *Node {
	return f.newNode(EmptyStatement, &EmptyStatementData{})
}

// --- declarations ------------------------------------------------------------

// ClassDeclarationData is a class declaration.
type ClassDeclarationData struct {
	Modifiers       *NodeArray
	Name            *Node // Identifier
	TypeParameters  *NodeArray
	ExtendsType     *Node // optional
	ImplementsTypes *NodeArray
	PermitsTypes    *NodeArray
	Members         *NodeArray
}

func (d *ClassDeclarationData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Modifiers) ||
		visit(v, d.Name) ||
		visitNodes(v, d.TypeParameters) ||
		visit(v, d.ExtendsType) ||
		visitNodes(v, d.ImplementsTypes) ||
		visitNodes(v, d.PermitsTypes) ||
		visitNodes(v, d.Members)
}

func (f *NodeFactory) NewClassDeclaration(modifiers *NodeArray, name *Node, typeParameters *NodeArray, extendsType *Node, implementsTypes, permitsTypes, members *NodeArray) *Node {
	return f.newNode(ClassDeclaration, &ClassDeclarationData{
		Modifiers: modifiers, Name: name, TypeParameters: typeParameters,
		ExtendsType: extendsType, ImplementsTypes: implementsTypes, PermitsTypes: permitsTypes, Members: members,
	})
}

func (n *Node) AsClassDeclaration() *ClassDeclarationData { return n.data.(*ClassDeclarationData) }

// FieldDeclarationData is one or more declarators of a type.
type FieldDeclarationData struct {
	Modifiers   *NodeArray
	Type        *Node
	Declarators *NodeArray // VariableDeclarator
}

func (d *FieldDeclarationData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Modifiers) || visit(v, d.Type) || visitNodes(v, d.Declarators)
}

func (f *NodeFactory) NewFieldDeclaration(modifiers *NodeArray, typ *Node, declarators *NodeArray) *Node {
	return f.newNode(FieldDeclaration, &FieldDeclarationData{Modifiers: modifiers, Type: typ, Declarators: declarators})
}

func (n *Node) AsFieldDeclaration() *FieldDeclarationData { return n.data.(*FieldDeclarationData) }

// VariableDeclaratorData is `name[]* (= initializer)?`.
type VariableDeclaratorData struct {
	Name               *Node
	ArrayRankAfterName int
	Initializer        *Node // optional
}

func (d *VariableDeclaratorData) forEachChild(v Visitor) bool {
	return visit(v, d.Name) || visit(v, d.Initializer)
}

func (f *NodeFactory) NewVariableDeclarator(name *Node, arrayRankAfterName int, initializer *Node) *Node {
	return f.newNode(VariableDeclarator, &VariableDeclaratorData{Name: name, ArrayRankAfterName: arrayRankAfterName, Initializer: initializer})
}

func (n *Node) AsVariableDeclarator() *VariableDeclaratorData {
	return n.data.(*VariableDeclaratorData)
}

// --- statements --------------------------------------------------------------

// BlockData is `{ statements }`.
type BlockData struct{ Statements *NodeArray }

func (d *BlockData) forEachChild(v Visitor) bool { return visitNodes(v, d.Statements) }

func (f *NodeFactory) NewBlock(statements *NodeArray) *Node {
	return f.newNode(Block, &BlockData{Statements: statements})
}

func (n *Node) AsBlock() *BlockData { return n.data.(*BlockData) }

// ExpressionStatementData is `expression;`.
type ExpressionStatementData struct{ Expression *Node }

func (d *ExpressionStatementData) forEachChild(v Visitor) bool { return visit(v, d.Expression) }

func (f *NodeFactory) NewExpressionStatement(expr *Node) *Node {
	return f.newNode(ExpressionStatement, &ExpressionStatementData{Expression: expr})
}

func (n *Node) AsExpressionStatement() *ExpressionStatementData {
	return n.data.(*ExpressionStatementData)
}

// ReturnStatementData is `return expr?;`.
type ReturnStatementData struct{ Expression *Node } // optional

func (d *ReturnStatementData) forEachChild(v Visitor) bool { return visit(v, d.Expression) }

func (f *NodeFactory) NewReturnStatement(expr *Node) *Node {
	return f.newNode(ReturnStatement, &ReturnStatementData{Expression: expr})
}

func (n *Node) AsReturnStatement() *ReturnStatementData { return n.data.(*ReturnStatementData) }

// --- expressions -------------------------------------------------------------

// LiteralExpressionData backs numeric/string/character/text-block literals; the
// node's Kind distinguishes them.
type LiteralExpressionData struct{ Value string }

func (d *LiteralExpressionData) forEachChild(Visitor) bool { return false }

func (f *NodeFactory) NewLiteralExpression(kind SyntaxKind, value string) *Node {
	return f.newNode(kind, &LiteralExpressionData{Value: value})
}

func (n *Node) AsLiteralExpression() *LiteralExpressionData {
	return n.data.(*LiteralExpressionData)
}

// BinaryExpressionData is `left op right`.
type BinaryExpressionData struct {
	Left          *Node
	OperatorToken SyntaxKind
	Right         *Node
}

func (d *BinaryExpressionData) forEachChild(v Visitor) bool {
	return visit(v, d.Left) || visit(v, d.Right)
}

func (f *NodeFactory) NewBinaryExpression(left *Node, op SyntaxKind, right *Node) *Node {
	return f.newNode(BinaryExpression, &BinaryExpressionData{Left: left, OperatorToken: op, Right: right})
}

func (n *Node) AsBinaryExpression() *BinaryExpressionData { return n.data.(*BinaryExpressionData) }

// PropertyAccessExpressionData is `expression.name`.
type PropertyAccessExpressionData struct {
	Expression *Node
	Name       *Node // Identifier
}

func (d *PropertyAccessExpressionData) forEachChild(v Visitor) bool {
	return visit(v, d.Expression) || visit(v, d.Name)
}

func (f *NodeFactory) NewPropertyAccessExpression(expr, name *Node) *Node {
	return f.newNode(PropertyAccessExpression, &PropertyAccessExpressionData{Expression: expr, Name: name})
}

func (n *Node) AsPropertyAccessExpression() *PropertyAccessExpressionData {
	return n.data.(*PropertyAccessExpressionData)
}

// CallExpressionData is `expression<typeArgs>(arguments)`.
type CallExpressionData struct {
	Expression    *Node
	TypeArguments *NodeArray
	Arguments     *NodeArray
}

func (d *CallExpressionData) forEachChild(v Visitor) bool {
	return visit(v, d.Expression) || visitNodes(v, d.TypeArguments) || visitNodes(v, d.Arguments)
}

func (f *NodeFactory) NewCallExpression(expr *Node, typeArgs, args *NodeArray) *Node {
	return f.newNode(CallExpression, &CallExpressionData{Expression: expr, TypeArguments: typeArgs, Arguments: args})
}

func (n *Node) AsCallExpression() *CallExpressionData { return n.data.(*CallExpressionData) }

// ParenthesizedExpressionData is `(expression)`.
type ParenthesizedExpressionData struct{ Expression *Node }

func (d *ParenthesizedExpressionData) forEachChild(v Visitor) bool { return visit(v, d.Expression) }

func (f *NodeFactory) NewParenthesizedExpression(expr *Node) *Node {
	return f.newNode(ParenthesizedExpression, &ParenthesizedExpressionData{Expression: expr})
}

func (n *Node) AsParenthesizedExpression() *ParenthesizedExpressionData {
	return n.data.(*ParenthesizedExpressionData)
}
