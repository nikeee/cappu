package compiler

// Node payloads for SE9 module declarations (module-info.java). Port of the
// module interfaces in src/compiler/types.ts.

// ModuleDeclarationData is `[open] module Name { directives }`.
type ModuleDeclarationData struct {
	Annotations *NodeArray
	IsOpen      bool
	Name        *Node // EntityName
	Directives  *NodeArray
}

func (d *ModuleDeclarationData) forEachChild(v Visitor) bool {
	return visitNodes(v, d.Annotations) || visit(v, d.Name) || visitNodes(v, d.Directives)
}

func (f *NodeFactory) NewModuleDeclaration(annotations *NodeArray, isOpen bool, name *Node, directives *NodeArray) *Node {
	return f.newNode(ModuleDeclaration, &ModuleDeclarationData{Annotations: annotations, IsOpen: isOpen, Name: name, Directives: directives})
}

func (n *Node) AsModuleDeclaration() *ModuleDeclarationData { return n.data.(*ModuleDeclarationData) }

// RequiresDirectiveData is `requires [transitive] [static] Name;`.
type RequiresDirectiveData struct {
	IsTransitive bool
	IsStatic     bool
	Name         *Node // EntityName
}

func (d *RequiresDirectiveData) forEachChild(v Visitor) bool { return visit(v, d.Name) }

func (f *NodeFactory) NewRequiresDirective(isTransitive, isStatic bool, name *Node) *Node {
	return f.newNode(RequiresDirective, &RequiresDirectiveData{IsTransitive: isTransitive, IsStatic: isStatic, Name: name})
}

func (n *Node) AsRequiresDirective() *RequiresDirectiveData { return n.data.(*RequiresDirectiveData) }

// ExportsDirectiveData is `exports pkg [to m1, m2];`.
type ExportsDirectiveData struct {
	PackageName *Node      // EntityName
	ToModules   *NodeArray // optional
}

func (d *ExportsDirectiveData) forEachChild(v Visitor) bool {
	return visit(v, d.PackageName) || visitNodes(v, d.ToModules)
}

func (f *NodeFactory) NewExportsDirective(packageName *Node, toModules *NodeArray) *Node {
	return f.newNode(ExportsDirective, &ExportsDirectiveData{PackageName: packageName, ToModules: toModules})
}

func (n *Node) AsExportsDirective() *ExportsDirectiveData { return n.data.(*ExportsDirectiveData) }

// OpensDirectiveData is `opens pkg [to m1, m2];`.
type OpensDirectiveData struct {
	PackageName *Node      // EntityName
	ToModules   *NodeArray // optional
}

func (d *OpensDirectiveData) forEachChild(v Visitor) bool {
	return visit(v, d.PackageName) || visitNodes(v, d.ToModules)
}

func (f *NodeFactory) NewOpensDirective(packageName *Node, toModules *NodeArray) *Node {
	return f.newNode(OpensDirective, &OpensDirectiveData{PackageName: packageName, ToModules: toModules})
}

func (n *Node) AsOpensDirective() *OpensDirectiveData { return n.data.(*OpensDirectiveData) }

// UsesDirectiveData is `uses Service;`.
type UsesDirectiveData struct{ TypeName *Node }

func (d *UsesDirectiveData) forEachChild(v Visitor) bool { return visit(v, d.TypeName) }

func (f *NodeFactory) NewUsesDirective(typeName *Node) *Node {
	return f.newNode(UsesDirective, &UsesDirectiveData{TypeName: typeName})
}

func (n *Node) AsUsesDirective() *UsesDirectiveData { return n.data.(*UsesDirectiveData) }

// ProvidesDirectiveData is `provides Service with Impl1, Impl2;`.
type ProvidesDirectiveData struct {
	TypeName  *Node // EntityName
	WithTypes *NodeArray
}

func (d *ProvidesDirectiveData) forEachChild(v Visitor) bool {
	return visit(v, d.TypeName) || visitNodes(v, d.WithTypes)
}

func (f *NodeFactory) NewProvidesDirective(typeName *Node, withTypes *NodeArray) *Node {
	return f.newNode(ProvidesDirective, &ProvidesDirectiveData{TypeName: typeName, WithTypes: withTypes})
}

func (n *Node) AsProvidesDirective() *ProvidesDirectiveData { return n.data.(*ProvidesDirectiveData) }
