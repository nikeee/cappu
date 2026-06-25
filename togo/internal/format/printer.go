// Port of src/format/printer.ts.
//
// Lower a parsed Java source file to the Doc IR (doc.go), which is then printed
// at the configured width. The visitor regenerates all layout from the AST -
// google-java-format does the same, discarding original whitespace - so cappu's
// trivia-free AST is sufficient. The only thing recovered from source is whether
// the user left a blank line between two members/statements (g-j-f preserves one).
//
// Node kinds not yet handled fall back to the verbatim source slice (degrade,
// never crash), matching the emitter's discipline.

package format

import (
	"errors"
	"sort"
	"strings"

	"github.com/nikeee/cappu/internal/compiler"
)

// FormatOptions selects the layout style.
type FormatOptions struct {
	Style string // "google" or "aosp"
}

const width = 100

// ErrUnsupportedSyntax is returned when the formatter cannot format the input
// without losing information.
var ErrUnsupportedSyntax = errors.New("unsupported syntax")

func formatSourceFile(sf *compiler.Node, options FormatOptions) (string, error) {
	p := newPrinter(sf)
	doc := p.sourceFile(sf.AsSourceFile())
	indentUnit := 2
	if options.Style == "aosp" {
		indentUnit = 4
	}
	out := printDoc(doc, printOptions{width: width, indentUnit: indentUnit})
	// Safety net: the printer attaches comments at member/statement granularity.
	// If a comment sat somewhere it does not yet handle, refuse rather than
	// silently drop it - the CLI then leaves the file untouched.
	if !p.allCommentsEmitted() {
		return "", ErrUnsupportedSyntax
	}
	// Exactly one trailing newline, like google-java-format.
	return strings.TrimRight(out, "\n") + "\n", nil
}

// modifierOrder is the canonical JLS modifier order google-java-format reorders to.
var modifierOrder = []compiler.SyntaxKind{
	compiler.PublicKeyword,
	compiler.ProtectedKeyword,
	compiler.PrivateKeyword,
	compiler.AbstractKeyword,
	compiler.DefaultKeyword,
	compiler.StaticKeyword,
	compiler.FinalKeyword,
	compiler.TransientKeyword,
	compiler.VolatileKeyword,
	compiler.SynchronizedKeyword,
	compiler.NativeKeyword,
	compiler.StrictfpKeyword,
}

type printer struct {
	sf       *compiler.Node
	text     string
	comments []comment
	ci       int // index of the next not-yet-emitted comment
}

func newPrinter(sf *compiler.Node) *printer {
	text := sf.AsSourceFile().Text
	return &printer{sf: sf, text: text, comments: collectComments(text)}
}

// raw is the exact source spelling of a leaf node (identifier, literal, ...).
func (p *printer) raw(node *compiler.Node) string {
	return p.text[compiler.SkipTrivia(p.text, node.Pos):node.End]
}

// start is the offset where a node's token text actually begins (past leading trivia).
func (p *printer) start(node *compiler.Node) int {
	return compiler.SkipTrivia(p.text, node.Pos)
}

// blankBeforePos reports whether >= 2 newlines separate from from pos (a blank line).
func (p *printer) blankBeforePos(from, pos int) bool {
	if from >= pos {
		return false
	}
	return strings.Count(p.text[from:pos], "\n") >= 2
}

func (p *printer) hasCommentBefore(pos int) bool {
	return p.ci < len(p.comments) && p.comments[p.ci].pos < pos
}

func (p *printer) commentsBefore(pos int) []comment {
	var out []comment
	for p.ci < len(p.comments) && p.comments[p.ci].pos < pos {
		out = append(out, p.comments[p.ci])
		p.ci++
	}
	return out
}

func (p *printer) allCommentsEmitted() bool {
	return p.ci >= len(p.comments)
}

// listDocs renders a member/statement list with its comments. It returns the
// inner docs already interleaved with hardline/blank separators; the caller
// supplies the leading hardline and the surrounding braces. forced applies the
// blank-line-around-methods rule (members only). endPos bounds the trailing
// "dangling" comments that sit before the closing brace.
func (p *printer) listDocs(list []*compiler.Node, forced bool, endPos int) []Doc {
	var out []Doc
	first := true
	prevEnd := endPos
	if len(list) > 0 {
		prevEnd = p.start(list[0])
	}

	push := func(doc Doc, blankBefore bool) {
		if !first {
			if blankBefore {
				out = append(out, concat(hardline, hardline))
			} else {
				out = append(out, hardline)
			}
		}
		out = append(out, doc)
		first = false
	}

	for i, item := range list {
		itemStart := p.start(item)
		// The blank line required before this whole entry (its leading comments
		// and the item) - g-j-f puts it before a method's doc comment, not between.
		firstPos := itemStart
		if p.hasCommentBefore(itemStart) {
			firstPos = p.comments[p.ci].pos
		}
		entryBlank := i > 0 &&
			(p.blankBeforePos(prevEnd, firstPos) || (forced && forcedBlank(list[i-1], item)))
		pushedInEntry := false
		pushEntry := func(doc Doc, srcBlank bool) {
			if pushedInEntry {
				push(doc, srcBlank)
			} else {
				push(doc, entryBlank || srcBlank)
			}
			pushedInEntry = true
		}

		for _, c := range p.commentsBefore(itemStart) {
			if !c.ownLine && !pushedInEntry && i > 0 {
				// A comment after code on the same line: attach to the previous entry.
				out[len(out)-1] = concat(out[len(out)-1], text(" "), text(c.text))
			} else {
				pushEntry(text(c.text), p.blankBeforePos(prevEnd, c.pos))
			}
			prevEnd = c.end
		}

		itemDoc := p.node(item)
		if trailing, ok := p.trailingCommentAfter(item); ok {
			itemDoc = concat(itemDoc, text(" "), text(trailing.text))
			prevEnd = trailing.end
		} else {
			prevEnd = item.End
		}
		pushEntry(itemDoc, false)
	}

	for _, c := range p.commentsBefore(endPos) {
		push(text(c.text), p.blankBeforePos(prevEnd, c.pos))
		prevEnd = c.end
	}
	return out
}

// trailingCommentAfter returns a comment immediately after node on the same line.
func (p *printer) trailingCommentAfter(node *compiler.Node) (comment, bool) {
	if p.ci >= len(p.comments) {
		return comment{}, false
	}
	c := p.comments[p.ci]
	if c.ownLine || c.pos < node.End {
		return comment{}, false
	}
	if strings.Contains(p.text[node.End:c.pos], "\n") {
		return comment{}, false
	}
	p.ci++
	return c, true
}

func (p *printer) sourceFile(sf *compiler.SourceFileData) Doc {
	// Blocks are separated by a blank line: an optional file-leading comment
	// (a license header), package, static imports, non-static imports, then the
	// type declarations (members separated among themselves).
	var blocks []Doc
	firstStart := p.firstConstructStart(sf)
	header := p.commentsBefore(firstStart)
	if len(header) > 0 {
		texts := make([]Doc, len(header))
		for i, c := range header {
			texts[i] = text(c.text)
		}
		blocks = append(blocks, join(hardline, texts))
	}
	if sf.PackageDeclaration != nil {
		blocks = append(blocks, concat(text("package "), text(p.entityName(sf.PackageDeclaration.AsPackageDeclaration().Name)), text(";")))
	}
	var statics, nonStatics []*compiler.Node
	for _, imp := range nodes(sf.Imports) {
		if imp.AsImportDeclaration().IsStatic {
			statics = append(statics, imp)
		} else {
			nonStatics = append(nonStatics, imp)
		}
	}
	for _, g := range [][]*compiler.Node{statics, nonStatics} {
		if len(g) > 0 {
			blocks = append(blocks, p.importGroup(g))
		}
	}
	if sf.ModuleDeclaration != nil {
		blocks = append(blocks, p.moduleDeclaration(sf.ModuleDeclaration.AsModuleDeclaration()))
	}
	if sf.Statements.Len() > 0 {
		blocks = append(blocks, concat(p.listDocs(nodes(sf.Statements), true, len(p.text))...))
	}
	return join(concat(hardline, hardline), blocks)
}

// firstConstructStart is the offset of the first real construct.
func (p *printer) firstConstructStart(sf *compiler.SourceFileData) int {
	best := len(p.text)
	consider := func(n *compiler.Node) {
		if n != nil {
			if s := p.start(n); s < best {
				best = s
			}
		}
	}
	consider(sf.PackageDeclaration)
	if sf.Imports.Len() > 0 {
		consider(sf.Imports.Nodes[0])
	}
	if sf.Statements.Len() > 0 {
		consider(sf.Statements.Nodes[0])
	}
	consider(sf.ModuleDeclaration)
	return best
}

// moduleDeclaration lays out module-info.java (SE9).
func (p *printer) moduleDeclaration(m *compiler.ModuleDeclarationData) Doc {
	var head []Doc
	for _, a := range nodes(m.Annotations) {
		head = append(head, p.annotation(a.AsAnnotation()), hardline)
	}
	if m.IsOpen {
		head = append(head, text("open "))
	}
	head = append(head, text("module "), text(p.entityName(m.Name)), text(" "))
	dirs := nodes(m.Directives)
	if len(dirs) == 0 {
		return concat(append(head, text("{}"))...)
	}
	var body []Doc
	for i, d := range dirs {
		if i > 0 {
			if d.Kind != dirs[i-1].Kind {
				body = append(body, concat(hardline, hardline))
			} else {
				body = append(body, hardline)
			}
		}
		body = append(body, p.directive(d))
	}
	parts := append([]Doc{}, head...)
	parts = append(parts, text("{"), indent(concat(append([]Doc{hardline}, body...)...)), hardline, text("}"))
	return concat(parts...)
}

func (p *printer) directive(d *compiler.Node) Doc {
	switch d.Kind {
	case compiler.RequiresDirective:
		r := d.AsRequiresDirective()
		mods := ""
		if r.IsTransitive {
			mods += "transitive "
		}
		if r.IsStatic {
			mods += "static "
		}
		return concat(text("requires "), text(mods), text(p.entityName(r.Name)), text(";"))
	case compiler.ExportsDirective:
		e := d.AsExportsDirective()
		return p.exportsLike("exports", e.PackageName, e.ToModules)
	case compiler.OpensDirective:
		o := d.AsOpensDirective()
		return p.exportsLike("opens", o.PackageName, o.ToModules)
	case compiler.UsesDirective:
		return concat(text("uses "), text(p.entityName(d.AsUsesDirective().TypeName)), text(";"))
	case compiler.ProvidesDirective:
		pr := d.AsProvidesDirective()
		return concat(text("provides "), text(p.entityName(pr.TypeName)), text(" with"), p.moduleNameList(pr.WithTypes), text(";"))
	default:
		return text(p.raw(d))
	}
}

func (p *printer) exportsLike(keyword string, pkg *compiler.Node, toModules *compiler.NodeArray) Doc {
	if toModules.Len() == 0 {
		return concat(text(keyword), text(" "), text(p.entityName(pkg)), text(";"))
	}
	return concat(text(keyword), text(" "), text(p.entityName(pkg)), text(" to"), p.moduleNameList(toModules), text(";"))
}

// moduleNameList renders a to/with module-name list: always broken, one per line.
func (p *printer) moduleNameList(names *compiler.NodeArray) Doc {
	items := make([]Doc, names.Len())
	for i, n := range nodes(names) {
		items[i] = text(p.entityName(n))
	}
	// g-j-f indents the continuation by two units (4 spaces google / 8 aosp).
	return indent(indent(concat(hardline, join(concat(text(","), hardline), items))))
}

func (p *printer) importGroup(imports []*compiler.Node) Doc {
	sorted := append([]*compiler.Node{}, imports...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return p.entityName(sorted[i].AsImportDeclaration().Name) < p.entityName(sorted[j].AsImportDeclaration().Name)
	})
	seen := map[string]bool{}
	var lines []Doc
	for _, imp := range sorted {
		t := p.importLine(imp.AsImportDeclaration())
		if seen[t] {
			continue // dedupe identical imports
		}
		seen[t] = true
		lines = append(lines, text(t))
	}
	return join(hardline, lines)
}

func (p *printer) importLine(imp *compiler.ImportDeclarationData) string {
	name := p.entityName(imp.Name)
	onDemand := ""
	if imp.IsOnDemand {
		onDemand = ".*"
	}
	static := ""
	if imp.IsStatic {
		static = "static "
	}
	return "import " + static + name + onDemand + ";"
}

// members renders members of a type body, with comments and blank lines.
func (p *printer) members(list *compiler.NodeArray, endPos int) []Doc {
	return p.listDocs(nodes(list), true, endPos)
}

func (p *printer) entityName(name *compiler.Node) string {
	if name.Kind == compiler.Identifier {
		return p.raw(name)
	}
	q := name.AsQualifiedName()
	return p.entityName(q.Left) + "." + p.raw(q.Right)
}

// modifiers renders a modifier list. annoMode controls annotation placement:
//   - "own": each declaration annotation on its own line.
//   - "var": fields/locals - annotation with arguments goes on its own line, a
//     parameterless marker annotation stays inline.
//   - "inline": always on the same line (parameters, record components).
func (p *printer) modifiers(mods *compiler.NodeArray, annoMode string) Doc {
	if mods.Len() == 0 {
		return text("")
	}
	var annotations, keywords []*compiler.Node
	for _, m := range nodes(mods) {
		if m.Kind == compiler.Annotation {
			annotations = append(annotations, m)
		} else {
			keywords = append(keywords, m)
		}
	}
	sort.SliceStable(keywords, func(i, j int) bool {
		return rank(keywords[i].Kind) < rank(keywords[j].Kind)
	})
	var parts []Doc
	for _, a := range annotations {
		ad := a.AsAnnotation()
		ownLine := annoMode == "own" || (annoMode == "var" && ad.Args != nil && ad.Args.Len() > 0)
		if ownLine {
			parts = append(parts, p.annotation(ad), hardline)
		} else {
			parts = append(parts, p.annotation(ad), text(" "))
		}
	}
	for _, k := range keywords {
		parts = append(parts, concat(text(p.modifierText(k)), text(" ")))
	}
	return concat(parts...)
}

func (p *printer) modifierText(k *compiler.Node) string {
	// The parser represents `non-sealed` as just the `non` identifier; restore
	// the full spelling here.
	if k.Kind == compiler.Identifier && p.raw(k) == "non" {
		return "non-sealed"
	}
	if s := compiler.TokenToString(k.Kind); s != "" {
		return s
	}
	return p.raw(k)
}

func (p *printer) annotation(a *compiler.AnnotationData) Doc {
	name := "@" + p.entityName(a.TypeName)
	if a.Args == nil {
		return text(name) // no argument list in source
	}
	if a.Args.Len() == 0 {
		return text(name + "()") // explicit empty parens are kept
	}
	args := make([]Doc, a.Args.Len())
	for i, arg := range nodes(a.Args) {
		aa := arg.AsAnnotationArgument()
		value := p.node(aa.Value)
		if aa.Name != nil {
			args[i] = concat(text(p.raw(aa.Name)), text(" = "), value)
		} else {
			args[i] = value
		}
	}
	return concat(text(name), text("("), join(text(", "), args), text(")"))
}

// annotations renders a run of inline annotations, each followed by a space.
func (p *printer) annotations(anns *compiler.NodeArray) Doc {
	if anns.Len() == 0 {
		return text("")
	}
	var parts []Doc
	for _, a := range nodes(anns) {
		parts = append(parts, concat(p.annotation(a.AsAnnotation()), text(" ")))
	}
	return concat(parts...)
}

func (p *printer) typeParameters(tps *compiler.NodeArray) Doc {
	if tps.Len() == 0 {
		return text("")
	}
	params := make([]Doc, tps.Len())
	for i, tpn := range nodes(tps) {
		tp := tpn.AsTypeParameter()
		name := p.raw(tp.Name)
		if tp.Constraint.Len() == 0 {
			params[i] = text(name)
			continue
		}
		bounds := make([]Doc, tp.Constraint.Len())
		for j, b := range nodes(tp.Constraint) {
			bounds[j] = p.typ(b)
		}
		params[i] = concat(text(name), text(" extends "), join(text(" & "), bounds))
	}
	return concat(text("<"), join(text(", "), params), text(">"))
}

func (p *printer) typeArguments(args *compiler.NodeArray) Doc {
	if args == nil {
		return text("")
	}
	if args.Len() == 0 {
		return text("<>") // diamond
	}
	ts := make([]Doc, args.Len())
	for i, t := range nodes(args) {
		ts[i] = p.typ(t)
	}
	return concat(text("<"), join(text(", "), ts), text(">"))
}

func (p *printer) typ(t *compiler.Node) Doc {
	switch t.Kind {
	case compiler.PrimitiveType:
		if s := compiler.TokenToString(t.AsPrimitiveType().Keyword); s != "" {
			return text(s)
		}
		return text(p.raw(t))
	case compiler.VarType:
		return text("var")
	case compiler.ArrayType:
		return concat(p.typ(t.AsArrayType().ElementType), text("[]"))
	case compiler.TypeReference:
		tr := t.AsTypeReference()
		return concat(text(p.entityName(tr.TypeName)), p.typeArguments(tr.TypeArguments))
	case compiler.WildcardType:
		w := t.AsWildcardType()
		if w.HasExtends && w.Type != nil {
			return concat(text("? extends "), p.typ(w.Type))
		}
		if w.HasSuper && w.Type != nil {
			return concat(text("? super "), p.typ(w.Type))
		}
		return text("?")
	default:
		return text(p.raw(t))
	}
}

// --- declarations --------------------------------------------------------

func (p *printer) classLike(keyword string, mods *compiler.NodeArray, name *compiler.Node, typeParams *compiler.NodeArray, members *compiler.NodeArray, end int, afterName Doc) Doc {
	header := concat(
		p.modifiers(mods, "own"),
		text(keyword),
		text(" "),
		text(p.raw(name)),
		p.typeParameters(typeParams),
		afterName,
		text(" "),
	)
	return concat(header, p.body(members, end))
}

// body renders a brace-delimited member body. endPos is the offset just past
// the closing brace, bounding trailing comments.
func (p *printer) body(members *compiler.NodeArray, endPos int) Doc {
	if members.Len() == 0 && !p.hasCommentBefore(endPos) {
		return text("{}")
	}
	return concat(
		text("{"),
		indent(concat(append([]Doc{hardline}, p.members(members, endPos)...)...)),
		hardline,
		text("}"),
	)
}

func (p *printer) classDeclaration(d *compiler.ClassDeclarationData, end int) Doc {
	var after []Doc
	if d.ExtendsType != nil {
		after = append(after, concat(text(" extends "), p.typ(d.ExtendsType)))
	}
	if d.ImplementsTypes.Len() > 0 {
		after = append(after, concat(text(" implements "), p.typeList(d.ImplementsTypes)))
	}
	if d.PermitsTypes.Len() > 0 {
		after = append(after, concat(text(" permits "), p.typeList(d.PermitsTypes)))
	}
	return p.classLike("class", d.Modifiers, d.Name, d.TypeParameters, d.Members, end, concat(after...))
}

func (p *printer) interfaceDeclaration(d *compiler.InterfaceDeclarationData, end int) Doc {
	var after []Doc
	if d.ExtendsTypes.Len() > 0 {
		after = append(after, concat(text(" extends "), p.typeList(d.ExtendsTypes)))
	}
	if d.PermitsTypes.Len() > 0 {
		after = append(after, concat(text(" permits "), p.typeList(d.PermitsTypes)))
	}
	return p.classLike("interface", d.Modifiers, d.Name, d.TypeParameters, d.Members, end, concat(after...))
}

func (p *printer) typeList(arr *compiler.NodeArray) Doc {
	ts := make([]Doc, arr.Len())
	for i, t := range nodes(arr) {
		ts[i] = p.typ(t)
	}
	return join(text(", "), ts)
}

func (p *printer) enumDeclaration(d *compiler.EnumDeclarationData, end int) Doc {
	var after []Doc
	if d.ImplementsTypes.Len() > 0 {
		after = append(after, concat(text(" implements "), p.typeList(d.ImplementsTypes)))
	}
	header := concat(
		p.modifiers(d.Modifiers, "own"),
		text("enum "),
		text(p.raw(d.Name)),
		concat(after...),
		text(" "),
	)
	if d.EnumConstants.Len() == 0 && d.Members.Len() == 0 {
		return concat(header, text("{}"))
	}
	consts := nodes(d.EnumConstants)
	constantDocs := make([]Doc, len(consts))
	for i, c := range consts {
		constantDocs[i] = p.enumConstant(c.AsEnumConstantDeclaration())
	}
	// google-java-format always lays enum constants one per line.
	constantsDoc := join(concat(text(","), hardline), constantDocs)
	bodyParts := []Doc{hardline, constantsDoc}
	if d.Members.Len() > 0 {
		// A blank line separates the constant list from the member declarations.
		bodyParts = append(bodyParts, text(";"), hardline, hardline)
		bodyParts = append(bodyParts, p.members(d.Members, end)...)
	} else if len(constantDocs) > 0 {
		// A trailing `;` after the last constant is preserved from the source.
		last := consts[len(consts)-1]
		if idx := compiler.SkipTrivia(p.text, last.End); idx < len(p.text) && p.text[idx] == ';' {
			bodyParts = append(bodyParts, text(";"))
		}
	}
	return concat(header, text("{"), indent(concat(bodyParts...)), hardline, text("}"))
}

func (p *printer) enumConstant(c *compiler.EnumConstantDeclarationData) Doc {
	parts := []Doc{p.modifiers(c.Modifiers, "own"), text(p.raw(c.Name))}
	if c.Arguments != nil {
		args := make([]Doc, c.Arguments.Len())
		for i, a := range nodes(c.Arguments) {
			args[i] = p.node(a)
		}
		parts = append(parts, text("("), join(text(", "), args), text(")"))
	}
	if c.ClassBody != nil {
		parts = append(parts, text(" "), p.body(c.ClassBody, c.ClassBody.End))
	}
	return concat(parts...)
}

func (p *printer) recordDeclaration(d *compiler.RecordDeclarationData, end int) Doc {
	comps := make([]Doc, d.RecordComponents.Len())
	for i, rcn := range nodes(d.RecordComponents) {
		rc := rcn.AsRecordComponent()
		sep := " "
		if rc.IsVarArgs {
			sep = "... "
		}
		comps[i] = concat(p.annotations(rc.Annotations), p.typ(rc.Type), text(sep), text(p.raw(rc.Name)))
	}
	after := []Doc{concat(text("("), join(text(", "), comps), text(")"))}
	if d.ImplementsTypes.Len() > 0 {
		after = append(after, concat(text(" implements "), p.typeList(d.ImplementsTypes)))
	}
	header := concat(
		p.modifiers(d.Modifiers, "own"),
		text("record "),
		text(p.raw(d.Name)),
		p.typeParameters(d.TypeParameters),
		concat(after...),
		text(" "),
	)
	return concat(header, p.body(d.Members, end))
}

func (p *printer) declaratorList(arr *compiler.NodeArray) Doc {
	ds := make([]Doc, arr.Len())
	for i, v := range nodes(arr) {
		ds[i] = p.declarator(v.AsVariableDeclarator())
	}
	return join(text(", "), ds)
}

func (p *printer) fieldDeclaration(d *compiler.FieldDeclarationData) Doc {
	return concat(
		p.modifiers(d.Modifiers, "var"),
		p.typ(d.Type),
		text(" "),
		p.declaratorList(d.Declarators),
		text(";"),
	)
}

func (p *printer) declarator(v *compiler.VariableDeclaratorData) Doc {
	name := concat(text(p.raw(v.Name)), text(strings.Repeat("[]", v.ArrayRankAfterName)))
	if v.Initializer == nil {
		return name
	}
	return group(concat(name, text(" ="), indent(concat(line, p.node(v.Initializer)))))
}

func (p *printer) parameters(params *compiler.NodeArray) Doc {
	if params.Len() == 0 {
		return text("()")
	}
	ps := make([]Doc, params.Len())
	for i, pp := range nodes(params) {
		ps[i] = p.parameter(pp.AsParameter())
	}
	return group(concat(text("("), indent(concat(softline, join(concat(text(","), line), ps))), softline, text(")")))
}

func (p *printer) parameter(pp *compiler.ParameterData) Doc {
	parts := []Doc{p.modifiers(pp.Modifiers, "inline"), p.typ(pp.Type)}
	if pp.IsVarArgs {
		parts = append(parts, text("..."))
	}
	if pp.Name != nil {
		parts = append(parts, text(" "), text(p.raw(pp.Name)))
	}
	return concat(parts...)
}

// methodLike renders a method or constructor. returnType may be nil.
func (p *printer) methodLike(mods, typeParams *compiler.NodeArray, returnType, name *compiler.Node, params, throws *compiler.NodeArray, body *compiler.Node) Doc {
	tp := p.typeParameters(typeParams)
	head := []Doc{p.modifiers(mods, "own")}
	if !isEmpty(tp) {
		head = append(head, tp, text(" "))
	}
	if returnType != nil {
		head = append(head, p.typ(returnType), text(" "))
	}
	head = append(head, text(p.raw(name)), p.parameters(params))
	if throws.Len() > 0 {
		head = append(head, concat(text(" throws "), p.typeList(throws)))
	}
	if body == nil {
		return concat(append(head, text(";"))...)
	}
	return concat(append(head, text(" "), p.block(body.AsBlock(), body.End))...)
}

func (p *printer) initializerBlock(d *compiler.InitializerBlockData) Doc {
	static := ""
	if d.IsStatic {
		static = "static "
	}
	return concat(text(static), p.block(d.Body.AsBlock(), d.Body.End))
}

// --- statements ----------------------------------------------------------

func (p *printer) block(b *compiler.BlockData, endPos int) Doc {
	if b.Statements.Len() == 0 && !p.hasCommentBefore(endPos) {
		return text("{}")
	}
	return concat(
		text("{"),
		indent(concat(append([]Doc{hardline}, p.listDocs(nodes(b.Statements), false, endPos)...)...)),
		hardline,
		text("}"),
	)
}

func (p *printer) localVar(d *compiler.LocalVariableDeclarationStatementData) Doc {
	return concat(
		p.modifiers(d.Modifiers, "var"),
		p.typ(d.Type),
		text(" "),
		p.declaratorList(d.Declarators),
		text(";"),
	)
}

func (p *printer) ifStatement(s *compiler.IfStatementData) Doc {
	parts := []Doc{
		group(concat(text("if ("), p.node(s.Condition), text(")"))),
		p.clauseBody(s.ThenStatement),
	}
	if s.ElseStatement != nil {
		elseOnSameLine := s.ThenStatement.Kind == compiler.Block
		if elseOnSameLine {
			parts = append(parts, text(" else"))
		} else {
			parts = append(parts, concat(hardline, text("else")))
		}
		if s.ElseStatement.Kind == compiler.IfStatement {
			parts = append(parts, text(" "), p.node(s.ElseStatement))
		} else {
			parts = append(parts, p.clauseBody(s.ElseStatement))
		}
	}
	return concat(parts...)
}

// clauseBody renders the controlled statement of if/for/while with its leading
// separator. A block follows after a space; a single statement stays on the
// same line when it fits and otherwise breaks onto an indented line.
func (p *printer) clauseBody(s *compiler.Node) Doc {
	if s.Kind == compiler.Block {
		return concat(text(" "), p.block(s.AsBlock(), s.End))
	}
	return group(indent(concat(line, p.node(s))))
}

func (p *printer) whileStatement(s *compiler.WhileStatementData) Doc {
	return concat(group(concat(text("while ("), p.node(s.Condition), text(")"))), p.clauseBody(s.Statement))
}

func (p *printer) doStatement(s *compiler.DoStatementData) Doc {
	var body Doc
	if s.Statement.Kind == compiler.Block {
		body = concat(text(" "), p.block(s.Statement.AsBlock(), s.Statement.End))
	} else {
		body = p.clauseBody(s.Statement)
	}
	return concat(text("do"), body, text(" while ("), p.node(s.Condition), text(");"))
}

func (p *printer) forStatement(s *compiler.ForStatementData) Doc {
	var init Doc
	switch {
	case s.Initializer != nil:
		init = p.forInit(s.Initializer)
	case s.InitializerExpressions.Len() > 0:
		es := make([]Doc, s.InitializerExpressions.Len())
		for i, e := range nodes(s.InitializerExpressions) {
			es[i] = p.node(e)
		}
		init = join(text(", "), es)
	default:
		init = text("")
	}
	cond := Doc{kind: docText}
	if s.Condition != nil {
		cond = p.node(s.Condition)
	}
	upd := text("")
	if s.Incrementors.Len() > 0 {
		es := make([]Doc, s.Incrementors.Len())
		for i, e := range nodes(s.Incrementors) {
			es[i] = p.node(e)
		}
		upd = join(text(", "), es)
	}
	header := group(concat(text("for ("), init, text("; "), cond, text("; "), upd, text(")")))
	return concat(header, p.clauseBody(s.Statement))
}

func (p *printer) forInit(init *compiler.Node) Doc {
	// A local variable declaration used as a for-init has no trailing `;`.
	if init.Kind == compiler.LocalVariableDeclarationStatement {
		d := init.AsLocalVariableDeclarationStatement()
		return concat(p.modifiers(d.Modifiers, "inline"), p.typ(d.Type), text(" "), p.declaratorList(d.Declarators))
	}
	return p.node(init)
}

func (p *printer) forEachStatement(s *compiler.ForEachStatementData) Doc {
	return concat(
		group(concat(text("for ("), p.parameter(s.Parameter.AsParameter()), text(" : "), p.node(s.Expression), text(")"))),
		p.clauseBody(s.Statement),
	)
}

func (p *printer) tryStatement(s *compiler.TryStatementData) Doc {
	parts := []Doc{text("try")}
	if s.Resources.Len() > 0 {
		rs := make([]Doc, s.Resources.Len())
		for i, r := range nodes(s.Resources) {
			rs[i] = p.resource(r.AsResource())
		}
		parts = append(parts, text(" ("), join(text("; "), rs), text(")"))
	}
	parts = append(parts, text(" "), p.block(s.TryBlock.AsBlock(), s.TryBlock.End))
	for _, cn := range nodes(s.CatchClauses) {
		c := cn.AsCatchClause()
		ts := make([]Doc, c.CatchTypes.Len())
		for i, t := range nodes(c.CatchTypes) {
			ts[i] = p.typ(t)
		}
		parts = append(parts, text(" catch ("), join(text(" | "), ts), text(" "), text(p.raw(c.Name)), text(") "), p.block(c.Block.AsBlock(), c.Block.End))
	}
	if s.FinallyBlock != nil {
		parts = append(parts, text(" finally "), p.block(s.FinallyBlock.AsBlock(), s.FinallyBlock.End))
	}
	return concat(parts...)
}

func (p *printer) resource(r *compiler.ResourceData) Doc {
	if r.Expression != nil {
		return p.node(r.Expression)
	}
	parts := []Doc{p.modifiers(r.Modifiers, "inline")}
	if r.Type != nil {
		parts = append(parts, concat(p.typ(r.Type), text(" ")))
	}
	if r.Name != nil {
		parts = append(parts, text(p.raw(r.Name)))
	}
	if r.Initializer != nil {
		parts = append(parts, concat(text(" = "), p.node(r.Initializer)))
	}
	return concat(parts...)
}

func (p *printer) switchLike(expr *compiler.Node, clauses *compiler.NodeArray) Doc {
	body := make([]Doc, clauses.Len())
	for i, c := range nodes(clauses) {
		body[i] = p.switchClause(c.AsSwitchClause(), c.End)
	}
	return concat(
		group(concat(text("switch ("), p.node(expr), text(")"))),
		text(" {"),
		indent(concat(hardline, join(hardline, body))),
		hardline,
		text("}"),
	)
}

func (p *printer) switchClause(c *compiler.SwitchClauseData, end int) Doc {
	var label Doc
	if c.IsDefault {
		label = text("default")
	} else {
		labels := make([]Doc, c.Labels.Len())
		for i, l := range nodes(c.Labels) {
			labels[i] = p.node(l)
		}
		label = concat(text("case "), join(text(", "), labels))
	}
	guard := text("")
	if c.Guard != nil {
		guard = concat(text(" when "), p.node(c.Guard))
	}
	if c.IsArrow {
		stmts := nodes(c.Statements)
		if len(stmts) == 1 && stmts[0].Kind == compiler.Block {
			return concat(label, guard, text(" -> "), p.block(stmts[0].AsBlock(), stmts[0].End))
		}
		ss := make([]Doc, len(stmts))
		for i, st := range stmts {
			ss[i] = p.node(st)
		}
		return concat(label, guard, text(" -> "), join(text(" "), ss))
	}
	return concat(label, guard, text(":"), indent(concat(append([]Doc{hardline}, p.listDocs(nodes(c.Statements), false, end)...)...)))
}

// --- expressions ---------------------------------------------------------

func (p *printer) binary(left *compiler.Node, op compiler.SyntaxKind, right *compiler.Node) Doc {
	opStr := compiler.TokenToString(op)
	if opStr == "" {
		opStr = "?"
	}
	return group(concat(p.node(left), text(" "), text(opStr), text(" "), p.node(right)))
}

func (p *printer) call(e *compiler.CallExpressionData) Doc {
	return concat(p.node(e.Expression), p.typeArguments(e.TypeArguments), p.argList(e.Arguments))
}

func (p *printer) argList(args *compiler.NodeArray) Doc {
	if args.Len() == 0 {
		return text("()")
	}
	as := make([]Doc, args.Len())
	for i, a := range nodes(args) {
		as[i] = p.node(a)
	}
	return group(concat(
		text("("),
		indent(concat(softline, join(concat(text(","), line), as))),
		softline,
		text(")"),
	))
}

func (p *printer) objectCreation(e *compiler.ObjectCreationExpressionData, end int) Doc {
	var parts []Doc
	if e.Qualifier != nil {
		parts = append(parts, p.node(e.Qualifier), text("."))
	}
	parts = append(parts, text("new "), p.typ(e.Type), p.argList(e.Arguments))
	if e.ClassBody != nil {
		parts = append(parts, text(" "), p.body(e.ClassBody, end))
	}
	return concat(parts...)
}

func (p *printer) arrayCreation(e *compiler.ArrayCreationExpressionData) Doc {
	var dims []Doc
	for _, d := range nodes(e.Dimensions) {
		dims = append(dims, concat(text("["), p.node(d), text("]")))
	}
	extra := strings.Repeat("[]", e.AdditionalRank)
	init := text("")
	if e.Initializer != nil {
		init = concat(text(" "), p.arrayInitializer(e.Initializer.AsArrayInitializer()))
	}
	return concat(text("new "), p.typ(e.ElementType), concat(dims...), text(extra), init)
}

func (p *printer) arrayInitializer(e *compiler.ArrayInitializerData) Doc {
	if e.Elements.Len() == 0 {
		return text("{}")
	}
	els := make([]Doc, e.Elements.Len())
	for i, el := range nodes(e.Elements) {
		els[i] = p.node(el)
	}
	return group(concat(
		text("{"),
		indent(concat(softline, join(concat(text(","), line), els))),
		softline,
		text("}"),
	))
}

func (p *printer) lambda(e *compiler.LambdaExpressionData) Doc {
	params := nodes(e.Parameters)
	var head Doc
	if len(params) == 1 && params[0].Kind == compiler.Identifier {
		head = text(p.raw(params[0]))
	} else {
		ps := make([]Doc, len(params))
		for i, pp := range params {
			if pp.Kind == compiler.Parameter {
				ps[i] = p.parameter(pp.AsParameter())
			} else {
				ps[i] = text(p.raw(pp))
			}
		}
		head = concat(text("("), join(text(", "), ps), text(")"))
	}
	var body Doc
	if e.Body.Kind == compiler.Block {
		body = p.block(e.Body.AsBlock(), e.Body.End)
	} else {
		body = p.node(e.Body)
	}
	return concat(head, text(" -> "), body)
}

func (p *printer) conditional(e *compiler.ConditionalExpressionData) Doc {
	return group(concat(p.node(e.Condition), text(" ? "), p.node(e.WhenTrue), text(" : "), p.node(e.WhenFalse)))
}

func (p *printer) instanceOf(e *compiler.InstanceofExpressionData) Doc {
	parts := []Doc{p.node(e.Expression), text(" instanceof ")}
	if e.Type != nil {
		parts = append(parts, p.typ(e.Type))
	}
	if e.Name != nil {
		parts = append(parts, text(" "), text(p.raw(e.Name)))
	}
	return concat(parts...)
}

// --- dispatch ------------------------------------------------------------

func (p *printer) node(node *compiler.Node) Doc {
	switch node.Kind {
	case compiler.ClassDeclaration:
		return p.classDeclaration(node.AsClassDeclaration(), node.End)
	case compiler.InterfaceDeclaration:
		return p.interfaceDeclaration(node.AsInterfaceDeclaration(), node.End)
	case compiler.EnumDeclaration:
		return p.enumDeclaration(node.AsEnumDeclaration(), node.End)
	case compiler.RecordDeclaration:
		return p.recordDeclaration(node.AsRecordDeclaration(), node.End)
	case compiler.FieldDeclaration:
		return p.fieldDeclaration(node.AsFieldDeclaration())
	case compiler.MethodDeclaration:
		m := node.AsMethodDeclaration()
		return p.methodLike(m.Modifiers, m.TypeParameters, m.ReturnType, m.Name, m.Parameters, m.Throws, m.Body)
	case compiler.ConstructorDeclaration:
		c := node.AsConstructorDeclaration()
		return p.methodLike(c.Modifiers, c.TypeParameters, nil, c.Name, c.Parameters, c.Throws, c.Body)
	case compiler.InitializerBlock:
		return p.initializerBlock(node.AsInitializerBlock())

	case compiler.Block:
		return p.block(node.AsBlock(), node.End)
	case compiler.EmptyStatement:
		return text(";")
	case compiler.LocalVariableDeclarationStatement:
		return p.localVar(node.AsLocalVariableDeclarationStatement())
	case compiler.ExpressionStatement:
		return concat(p.node(node.AsExpressionStatement().Expression), text(";"))
	case compiler.IfStatement:
		return p.ifStatement(node.AsIfStatement())
	case compiler.WhileStatement:
		return p.whileStatement(node.AsWhileStatement())
	case compiler.DoStatement:
		return p.doStatement(node.AsDoStatement())
	case compiler.ForStatement:
		return p.forStatement(node.AsForStatement())
	case compiler.ForEachStatement:
		return p.forEachStatement(node.AsForEachStatement())
	case compiler.ReturnStatement:
		r := node.AsReturnStatement()
		if r.Expression != nil {
			return concat(text("return "), p.node(r.Expression), text(";"))
		}
		return text("return;")
	case compiler.ThrowStatement:
		return concat(text("throw "), p.node(node.AsThrowStatement().Expression), text(";"))
	case compiler.BreakStatement:
		b := node.AsLabelStatement()
		if b.Label != nil {
			return concat(text("break "), text(p.raw(b.Label)), text(";"))
		}
		return text("break;")
	case compiler.ContinueStatement:
		c := node.AsLabelStatement()
		if c.Label != nil {
			return concat(text("continue "), text(p.raw(c.Label)), text(";"))
		}
		return text("continue;")
	case compiler.YieldStatement:
		return concat(text("yield "), p.node(node.AsYieldStatement().Expression), text(";"))
	case compiler.SynchronizedStatement:
		s := node.AsSynchronizedStatement()
		return concat(text("synchronized ("), p.node(s.Expression), text(") "), p.block(s.Body.AsBlock(), s.Body.End))
	case compiler.AssertStatement:
		s := node.AsAssertStatement()
		if s.Message != nil {
			return concat(text("assert "), p.node(s.Condition), text(" : "), p.node(s.Message), text(";"))
		}
		return concat(text("assert "), p.node(s.Condition), text(";"))
	case compiler.LabeledStatement:
		s := node.AsLabeledStatement()
		return concat(text(p.raw(s.Label)), text(":"), hardline, p.node(s.Statement))
	case compiler.TryStatement:
		return p.tryStatement(node.AsTryStatement())
	case compiler.SwitchStatement:
		s := node.AsSwitchStatement()
		return p.switchLike(s.Expression, s.Clauses)

	case compiler.SwitchExpression:
		s := node.AsSwitchExpression()
		return p.switchLike(s.Expression, s.Clauses)
	case compiler.BinaryExpression:
		e := node.AsBinaryExpression()
		return p.binary(e.Left, e.OperatorToken, e.Right)
	case compiler.AssignmentExpression:
		e := node.AsAssignmentExpression()
		return p.binary(e.Left, e.OperatorToken, e.Right)
	case compiler.ConditionalExpression:
		return p.conditional(node.AsConditionalExpression())
	case compiler.CallExpression:
		return p.call(node.AsCallExpression())
	case compiler.PropertyAccessExpression:
		e := node.AsPropertyAccessExpression()
		return concat(p.node(e.Expression), text("."), text(p.raw(e.Name)))
	case compiler.ElementAccessExpression:
		e := node.AsElementAccessExpression()
		return concat(p.node(e.Expression), text("["), p.node(e.ArgumentExpression), text("]"))
	case compiler.ObjectCreationExpression:
		return p.objectCreation(node.AsObjectCreationExpression(), node.End)
	case compiler.ArrayCreationExpression:
		return p.arrayCreation(node.AsArrayCreationExpression())
	case compiler.ArrayInitializer:
		return p.arrayInitializer(node.AsArrayInitializer())
	case compiler.ParenthesizedExpression:
		return concat(text("("), p.node(node.AsParenthesizedExpression().Expression), text(")"))
	case compiler.PrefixUnaryExpression:
		e := node.AsPrefixUnaryExpression()
		op := compiler.TokenToString(e.Operator)
		// A space goes between a +/- operator and an operand that itself starts
		// with +/- so the tokens do not merge into ++/-- (e.g. `- -1`, not `--1`).
		sep := ""
		if e.Operator == compiler.PlusToken || e.Operator == compiler.MinusToken {
			if e.Operand.Kind == compiler.PrefixUnaryExpression {
				oo := e.Operand.AsPrefixUnaryExpression().Operator
				if oo == compiler.PlusToken || oo == compiler.MinusToken || oo == compiler.PlusPlusToken || oo == compiler.MinusMinusToken {
					sep = " "
				}
			}
		}
		return concat(text(op), text(sep), p.node(e.Operand))
	case compiler.PostfixUnaryExpression:
		e := node.AsPostfixUnaryExpression()
		return concat(p.node(e.Operand), text(compiler.TokenToString(e.Operator)))
	case compiler.CastExpression:
		e := node.AsCastExpression()
		types := []Doc{p.typ(e.Type)}
		for _, b := range nodes(e.Bounds) {
			types = append(types, p.typ(b))
		}
		return concat(text("("), join(text(" & "), types), text(") "), p.node(e.Expression))
	case compiler.InstanceofExpression:
		return p.instanceOf(node.AsInstanceofExpression())
	case compiler.LambdaExpression:
		return p.lambda(node.AsLambdaExpression())
	case compiler.MethodReferenceExpression:
		e := node.AsMethodReferenceExpression()
		ref := ""
		if e.IsConstructorRef {
			ref = "new"
		} else if e.Name != nil {
			ref = p.raw(e.Name)
		}
		return concat(p.node(e.Expression), text("::"), text(ref))
	case compiler.ThisExpression:
		return text("this")
	case compiler.SuperExpression:
		return text("super")
	case compiler.ClassLiteralExpression:
		return concat(p.typ(node.AsClassLiteralExpression().Type), text(".class"))

	case compiler.Identifier, compiler.NumericLiteral, compiler.StringLiteral,
		compiler.CharacterLiteral, compiler.TextBlockLiteral, compiler.TrueKeyword,
		compiler.FalseKeyword, compiler.NullKeyword:
		return text(p.raw(node))

	case compiler.PrimitiveType, compiler.TypeReference, compiler.ArrayType,
		compiler.WildcardType, compiler.VarType:
		return p.typ(node)

	case compiler.QualifiedName:
		return text(p.entityName(node))
	case compiler.Annotation:
		return p.annotation(node.AsAnnotation())

	default:
		// Degrade, do not crash: emit the verbatim source slice.
		return text(p.raw(node))
	}
}

func rank(kind compiler.SyntaxKind) int {
	for i, k := range modifierOrder {
		if k == kind {
			return i
		}
	}
	return len(modifierOrder)
}

// forcedBlank reports whether a blank line is forced between two members. A
// method, constructor, initializer or nested type forces it; consecutive fields
// stay together unless the user separated them (a source blank line).
func forcedBlank(a, b *compiler.Node) bool {
	return isBlankForcing(a.Kind) || isBlankForcing(b.Kind) ||
		fieldSpansMultipleLines(a) || fieldSpansMultipleLines(b)
}

// fieldSpansMultipleLines reports whether a field renders across multiple lines,
// which happens when an annotation lands on its own line (a "var"-mode
// annotation carrying arguments). google-java-format pads such fields with
// blank lines.
func fieldSpansMultipleLines(node *compiler.Node) bool {
	if node.Kind != compiler.FieldDeclaration {
		return false
	}
	for _, m := range nodes(node.AsFieldDeclaration().Modifiers) {
		if m.Kind == compiler.Annotation {
			a := m.AsAnnotation()
			if a.Args != nil && a.Args.Len() > 0 {
				return true
			}
		}
	}
	return false
}

func isBlankForcing(kind compiler.SyntaxKind) bool {
	switch kind {
	case compiler.MethodDeclaration, compiler.ConstructorDeclaration, compiler.InitializerBlock,
		compiler.ClassDeclaration, compiler.InterfaceDeclaration, compiler.EnumDeclaration,
		compiler.RecordDeclaration, compiler.AnnotationTypeDeclaration:
		return true
	default:
		return false
	}
}

// nodes returns the slice backing a (possibly nil) NodeArray.
func nodes(a *compiler.NodeArray) []*compiler.Node {
	if a == nil {
		return nil
	}
	return a.Nodes
}

// isEmpty reports whether a Doc is the empty-string text node.
func isEmpty(d Doc) bool {
	return d.kind == docText && d.text == ""
}
