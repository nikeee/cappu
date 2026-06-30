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
	"cmp"
	"errors"
	"slices"
	"strings"

	"github.com/nikeee/cappu/internal/compiler"
)

// FormatOptions selects the layout style.
type FormatOptions struct {
	Style string // "google" or "aosp"
}

const width = 100

// google-java-format continuation indents (columns at google scale; the style
// multiplier is applied at print time): +2 = one indent level (block body,
// array-initializer continuation); +4 = a continuation (broken argument /
// parameter / type lists, operator chains).
var (
	plus2  = indentConst(2)
	plus4  = indentConst(4)
	minus2 = indentConst(-2)
)

// google-java-format glues a dereference chain's receiver through a call to one
// of these methods (see gjf JavaInputAstVisitor#handleStream): the call's index
// becomes a chain-prefix boundary, so `x.stream().a().b()` keeps `x.stream()`
// together and breaks before the rest.
var streamPrefixMethods = map[string]bool{"stream": true, "parallelStream": true, "toBuilder": true}

// Well-known nullness type annotations (gjf JavaInputAstVisitor#typeAnnotations).
// An `@Nullable`/`@NonNull` imported from one of these is a TYPE annotation and
// renders inline before the type rather than on its own line.
var typeAnnotationFQNs = map[string]bool{
	"org.jspecify.annotations.NonNull":                    true,
	"org.jspecify.annotations.Nullable":                   true,
	"org.checkerframework.checker.nullness.qual.NonNull":  true,
	"org.checkerframework.checker.nullness.qual.Nullable": true,
}

// ErrUnsupportedSyntax is returned when the formatter cannot format the input
// without losing information.
var ErrUnsupportedSyntax = errors.New("unsupported syntax")

func formatSourceFile(sf *compiler.Node, options FormatOptions) (string, error) {
	mult := 1
	if options.Style == "aosp" {
		mult = 2
	}
	p := newPrinter(sf, mult)
	doc := p.sourceFile(sf.AsSourceFile())
	out := printDoc(doc, printOptions{
		width:      width,
		indentMult: mult,
		// A reflow leaf carries a raw comment; rewrite it at its column.
		commentRewriter: func(raw string, col int) string {
			return rewriteComment(raw, col, strings.HasPrefix(raw, "//"))
		},
	})
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
	// mult is the indent multiplier (1 google / 2 aosp); a few gjf decisions
	// (e.g. the method-chain "small receiver" threshold) need it at build time.
	mult int
	// typeAnnotationNames holds simple names imported as a well-known nullness
	// type annotation (e.g. "Nullable" with `import org.jspecify...Nullable;`).
	typeAnnotationNames map[string]bool
}

func newPrinter(sf *compiler.Node, mult int) *printer {
	text := sf.AsSourceFile().Text
	p := &printer{sf: sf, text: text, comments: collectComments(text), mult: mult, typeAnnotationNames: map[string]bool{}}
	for _, imp := range nodes(sf.AsSourceFile().Imports) {
		id := imp.AsImportDeclaration()
		if id.IsStatic {
			continue
		}
		fqn := p.entityName(id.Name)
		if typeAnnotationFQNs[fqn] {
			p.typeAnnotationNames[fqn[strings.LastIndex(fqn, ".")+1:]] = true
		}
	}
	return p
}

// isTypeAnnotation reports whether a is a well-known type-use annotation
// imported in this file.
func (p *printer) isTypeAnnotation(a *compiler.Node) bool {
	n := a.AsAnnotation().TypeName
	var simple string
	if n.Kind == compiler.Identifier {
		simple = p.raw(n)
	} else {
		simple = p.raw(n.AsQualifiedName().Right)
	}
	return p.typeAnnotationNames[simple]
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

// braceLead is the separator after an opening `{`, before the first body entry.
// google-java-format preserves one source blank line here, so emit two
// hardlines when the source left a blank between the brace and the first
// rendered thing (a leading comment if present, else the entry). bracePos is
// the offset just after `{` (a node's raw .Pos, before its trivia);
// firstItemStart is the first entry's trivia-skipped start.
func (p *printer) braceLead(bracePos, firstItemStart int) Doc {
	firstContent := firstItemStart
	if p.hasCommentBefore(firstItemStart) {
		firstContent = p.comments[p.ci].pos
	}
	if p.blankBeforePos(bracePos, firstContent) {
		return concat(hardline, hardline)
	}
	return hardline
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
		leadComments := p.commentsBefore(itemStart)
		firstPos := itemStart
		if len(leadComments) > 0 {
			firstPos = leadComments[0].pos
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

		// A block comment on the same line as the item attaches inline before it
		// (`/* package */ final int x;`); the rest are own-line leading comments.
		var inlineLead *comment
		if n := len(leadComments); n > 0 {
			last := leadComments[n-1]
			// A multi-line comment/javadoc stays own-line; only a single-line
			// block comment abutting the item attaches inline.
			if !last.line && !strings.Contains(last.text, "\n") && !strings.Contains(p.text[last.end:itemStart], "\n") {
				inlineLead = &last
				leadComments = leadComments[:n-1]
			}
		}

		for _, c := range leadComments {
			if !c.ownLine && !pushedInEntry && i > 0 {
				// A comment after code on the same line: attach to the previous entry.
				out[len(out)-1] = concat(out[len(out)-1], text(" "), text(c.text))
			} else {
				pushEntry(reflow(c.text), p.blankBeforePos(prevEnd, c.pos))
			}
			prevEnd = c.end
		}

		// gjf preserves one source blank line between a leading own-line comment
		// and the item it precedes (a "section header" comment set off from its
		// member). Only when own-line comments were already pushed for this entry.
		afterComments := prevEnd
		itemDoc := p.node(item)
		if inlineLead != nil {
			itemDoc = concat(reflow(inlineLead.text), text(" "), itemDoc)
		}
		if trailing, ok := p.trailingCommentAfter(item); ok {
			itemDoc = concat(itemDoc, text(" "), text(trailing.text))
			prevEnd = trailing.end
		} else {
			prevEnd = item.End
		}
		itemBlank := pushedInEntry && inlineLead == nil && p.blankBeforePos(afterComments, itemStart)
		pushEntry(itemDoc, itemBlank)
	}

	for _, c := range p.commentsBefore(endPos) {
		push(reflow(c.text), p.blankBeforePos(prevEnd, c.pos))
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
	if len(header) > 0 {
		texts := make([]Doc, len(header))
		for i, c := range header {
			texts[i] = reflow(c.text)
		}
		headerDoc := join(hardline, texts)
		// A leading comment glued to the first construct (no blank line in source)
		// is its doc comment - keep it attached. One followed by a blank line is a
		// file header (e.g. a license), separated like other blocks.
		glued := len(blocks) > 0 && !p.blankBeforePos(header[len(header)-1].end, firstStart)
		if glued {
			blocks[0] = concat(headerDoc, hardline, blocks[0])
		} else {
			blocks = append([]Doc{headerDoc}, blocks...)
		}
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
	sorted := slices.Clone(imports)
	slices.SortStableFunc(sorted, func(a, b *compiler.Node) int {
		return cmp.Compare(p.entityName(a.AsImportDeclaration().Name), p.entityName(b.AsImportDeclaration().Name))
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
	all := nodes(mods)
	// Peel a trailing run of well-known type-use annotations (`@Nullable` etc.):
	// gjf renders these inline right before the type, not on their own line. The
	// rest are declaration modifiers, placed as usual.
	cut := len(all)
	if annoMode != "inline" {
		for cut > 0 && all[cut-1].Kind == compiler.Annotation && p.isTypeAnnotation(all[cut-1]) {
			cut--
		}
	}
	var annotations, keywords []*compiler.Node
	for _, m := range all[:cut] {
		if m.Kind == compiler.Annotation {
			annotations = append(annotations, m)
		} else {
			keywords = append(keywords, m)
		}
	}
	slices.SortStableFunc(keywords, func(a, b *compiler.Node) int {
		return cmp.Compare(rank(a.Kind), rank(b.Kind))
	})
	var parts []Doc
	for _, a := range annotations {
		ad := a.AsAnnotation()
		ownLine := annoMode == "own" || (annoMode == "var" && ad.Args != nil && ad.Args.Len() > 0)
		parts = append(parts, p.annotation(ad))
		// A comment on the same line as an own-line annotation stays with it
		// (`@SuppressWarnings("x") // why`) instead of floating away.
		if ownLine {
			if tc, ok := p.trailingCommentAfter(a); ok {
				parts = append(parts, text(" "), text(tc.text))
			}
			parts = append(parts, hardline)
		} else {
			parts = append(parts, text(" "))
		}
	}
	for _, k := range keywords {
		parts = append(parts, concat(text(p.modifierText(k)), text(" ")))
	}
	// Type-use annotation suffix, inline before the type.
	for _, a := range all[cut:] {
		parts = append(parts, p.annotation(a.AsAnnotation()), text(" "))
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
	// Annotation arguments wrap like a call's: break after `(` at +4 and lay one
	// element-value pair per line (fill only when every arg is short).
	fill := fillUnified
	if p.allShortItems(nodes(a.Args)) {
		fill = fillIndependent
	}
	return concat(text(name), p.argsLike("(", args, ")", fill))
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
		pt := t.AsPrimitiveType()
		keyword := compiler.TokenToString(pt.Keyword)
		if keyword == "" {
			keyword = p.raw(t)
		}
		// SE8 type-use annotations precede the type: `@Nullable int`.
		return concat(p.annotations(pt.Annotations), text(keyword))
	case compiler.VarType:
		return text("var")
	case compiler.ArrayType:
		return concat(p.typ(t.AsArrayType().ElementType), text("[]"))
	case compiler.TypeReference:
		tr := t.AsTypeReference()
		return concat(p.annotations(tr.Annotations), text(p.entityName(tr.TypeName)), p.typeArguments(tr.TypeArguments))
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

func (p *printer) classLike(keyword string, mods *compiler.NodeArray, name *compiler.Node, typeParams *compiler.NodeArray, members *compiler.NodeArray, end int, tail []Doc) Doc {
	header := concat(
		p.modifiers(mods, "own"),
		text(keyword),
		text(" "),
		text(p.raw(name)),
		p.typeParameters(typeParams),
		// extends/implements/permits live in one +4 level: each clause begins with
		// a fill break, so a long clause folds onto its own continuation line.
		level(plus4, tail),
		text(" "),
	)
	return concat(header, p.body(members, end))
}

// typeListClause is a gjf class-header type list (`implements A, B, C`): a fill
// break before the keyword, then the keyword and the types. With more than one
// type the list indents +4 and its commas break UNIFIED (one per line); a single
// type stays attached.
func (p *printer) typeListClause(keyword string, types []*compiler.Node) Doc {
	if len(types) == 0 {
		return text("")
	}
	inner := []Doc{text(keyword), text(" ")}
	for i, t := range types {
		if i > 0 {
			inner = append(inner, text(","), brk(fillUnified, " ", ZERO, nil))
		}
		inner = append(inner, p.typ(t))
	}
	plus := ZERO
	if len(types) > 1 {
		plus = plus4
	}
	return concat(brk(fillIndependent, " ", ZERO, nil), level(plus, inner))
}

// body renders a brace-delimited member body. endPos is the offset just past
// the closing brace, bounding trailing comments.
func (p *printer) body(members *compiler.NodeArray, endPos int) Doc {
	if members.Len() == 0 && !p.hasCommentBefore(endPos) {
		return text("{}")
	}
	lead := hardline
	if members.Len() > 0 {
		lead = p.braceLead(members.Nodes[0].Pos, p.start(members.Nodes[0]))
	}
	return concat(
		text("{"),
		indent(concat(append([]Doc{lead}, p.members(members, endPos)...)...)),
		hardline,
		text("}"),
	)
}

func (p *printer) classDeclaration(d *compiler.ClassDeclarationData, end int) Doc {
	var tail []Doc
	// A class's `extends` is a single supertype (no list).
	if d.ExtendsType != nil {
		tail = append(tail, concat(brk(fillIndependent, " ", ZERO, nil), text("extends "), p.typ(d.ExtendsType)))
	}
	tail = append(tail, p.typeListClause("implements", nodes(d.ImplementsTypes)))
	tail = append(tail, p.typeListClause("permits", nodes(d.PermitsTypes)))
	return p.classLike("class", d.Modifiers, d.Name, d.TypeParameters, d.Members, end, tail)
}

func (p *printer) interfaceDeclaration(d *compiler.InterfaceDeclarationData, end int) Doc {
	var tail []Doc
	tail = append(tail, p.typeListClause("extends", nodes(d.ExtendsTypes)))
	tail = append(tail, p.typeListClause("permits", nodes(d.PermitsTypes)))
	return p.classLike("interface", d.Modifiers, d.Name, d.TypeParameters, d.Members, end, tail)
}

func (p *printer) enumDeclaration(d *compiler.EnumDeclarationData, end int) Doc {
	tail := []Doc{p.typeListClause("implements", nodes(d.ImplementsTypes))}
	header := concat(
		p.modifiers(d.Modifiers, "own"),
		text("enum "),
		text(p.raw(d.Name)),
		level(plus4, tail),
		text(" "),
	)
	if d.EnumConstants.Len() == 0 && d.Members.Len() == 0 {
		return concat(header, text("{}"))
	}
	consts := nodes(d.EnumConstants)
	// Leading blank after `{` (before any constant comment is consumed below).
	lead := hardline
	if len(consts) > 0 {
		lead = p.braceLead(consts[0].Pos, p.start(consts[0]))
	}
	// google-java-format always lays enum constants one per line. A comment
	// before a constant stays attached to it (own-line, reflowed); a trailing
	// comment on the constant's line is kept after it.
	var constantParts []Doc
	prevConstEnd := -1
	for i, c := range consts {
		leadComments := p.commentsBefore(p.start(c))
		firstPos := p.start(c)
		if len(leadComments) > 0 {
			firstPos = leadComments[0].pos
		}
		if i > 0 {
			// gjf preserves one source blank line between enum constants.
			if p.blankBeforePos(prevConstEnd, firstPos) {
				constantParts = append(constantParts, text(","), hardline, hardline)
			} else {
				constantParts = append(constantParts, text(","), hardline)
			}
		}
		for _, cm := range leadComments {
			constantParts = append(constantParts, reflow(cm.text), hardline)
		}
		cdoc := p.enumConstant(c.AsEnumConstantDeclaration())
		if trailing, ok := p.trailingCommentAfter(c); ok {
			cdoc = concat(cdoc, text(" "), text(trailing.text))
			prevConstEnd = trailing.end
		} else {
			prevConstEnd = c.End
		}
		constantParts = append(constantParts, cdoc)
	}
	// A trailing comma and/or `;` after the last constant, preserved from source
	// (gjf keeps a trailing comma; `enum { A, B, }`).
	semicolonAfter := false
	if len(consts) > 0 {
		p2 := compiler.SkipTrivia(p.text, consts[len(consts)-1].End)
		if p2 < len(p.text) && p.text[p2] == ',' {
			constantParts = append(constantParts, text(","))
			p2 = compiler.SkipTrivia(p.text, p2+1)
		}
		semicolonAfter = p2 < len(p.text) && p.text[p2] == ';'
	}
	bodyParts := []Doc{lead, concat(constantParts...)}
	if d.Members.Len() > 0 {
		// The constant list is `;`-terminated, then the members. A blank line
		// separates them only when there are constants above (a bare leading `;`
		// with no constants gets no blank line before the members) AND a real
		// member follows - a trailing empty statement (`;`) gets no blank line.
		realMember := false
		for _, m := range nodes(d.Members) {
			if m.Kind != compiler.EmptyStatement {
				realMember = true
				break
			}
		}
		bodyParts = append(bodyParts, text(";"), hardline)
		if len(consts) > 0 && realMember {
			bodyParts = append(bodyParts, hardline)
		}
		bodyParts = append(bodyParts, p.members(d.Members, end)...)
	} else if semicolonAfter {
		bodyParts = append(bodyParts, text(";"))
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
	renderComp := func(n *compiler.Node) Doc {
		rc := n.AsRecordComponent()
		sep := " "
		if rc.IsVarArgs {
			sep = "... "
		}
		return concat(p.annotations(rc.Annotations), p.typ(rc.Type), text(sep), text(p.raw(rc.Name)))
	}
	var recordParens Doc
	if d.RecordComponents.Len() == 0 {
		recordParens = text("()")
	} else {
		items, _ := p.listItems(nodes(d.RecordComponents), renderComp)
		recordParens = p.argsLike("(", items, ")", fillUnified)
	}
	after := []Doc{recordParens}
	// The `implements` clause folds onto its own +4 continuation line when the
	// record header overflows (gjf), same shape as a class header's clause.
	if d.ImplementsTypes.Len() > 0 {
		after = append(after, level(plus4, []Doc{p.typeListClause("implements", nodes(d.ImplementsTypes))}))
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
		ds[i] = p.declarator(v.AsVariableDeclarator(), nil)
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

func (p *printer) declarator(v *compiler.VariableDeclaratorData, trailing Doc) Doc {
	name := concat(text(p.raw(v.Name)), text(strings.Repeat("[]", v.ArrayRankAfterName)))
	if v.Initializer == nil {
		if trailing != nil {
			return concat(name, trailing)
		}
		return name
	}
	// An array initializer hugs the `=` (its own braces break); others fold onto
	// a +4 continuation line after `=`. The statement's `;` rides into the
	// initializer's tail call (rest-of-line).
	if v.Initializer.Kind == compiler.ArrayInitializer {
		if trailing != nil {
			return concat(name, text(" = "), p.node(v.Initializer), trailing)
		}
		return concat(name, text(" = "), p.node(v.Initializer))
	}
	return concat(name, text(" ="), level(plus4, []Doc{line, p.statementTail(v.Initializer, trailing)}))
}

// argsLike is a gjf parenthesized comma list. When it does not fit, a UNIFIED
// break fires after `(` (continuation at +4) so the items always start on the
// next line; a nested zero-indent level keeps them on one continuation line if
// they fit, else the fill mode decides (UNIFIED one per line, INDEPENDENT fill).
// The closing `)` stays attached to the last item's line.
func (p *printer) argsLike(open string, items []Doc, closeTok string, fill FillMode) Doc {
	return p.argsLikeTrailing(open, items, closeTok, fill, nil)
}

// argsLikeTrailing is argsLike with a trailing token (e.g. a method signature's
// `;`) placed inside the breaking level so its width counts toward the fit
// decision - gjf's rest-of-line rule. With a trailing token the open delimiter
// also goes inside the level so the fit check at the column before `(` spans the
// whole `(...)<trailing>` run. The empty-trailing path is the common call case.
func (p *printer) argsLikeTrailing(open string, items []Doc, closeTok string, fill FillMode, trailing Doc) Doc {
	var innerParts []Doc
	for i, it := range items {
		if i > 0 {
			innerParts = append(innerParts, text(","), brk(fill, " ", ZERO, nil))
		}
		innerParts = append(innerParts, it)
	}
	// The close delimiter (and any token trailing it on the same line, e.g. a
	// method signature's `;` or a routed-in separator) goes INSIDE the args level
	// so the args' own one-per-line fit check counts it: gjf breaks a delimited
	// list when the whole `(...)<close><trailing>` run overflows, not just the
	// bare args.
	innerParts = append(innerParts, text(closeTok))
	if trailing != nil {
		innerParts = append(innerParts, trailing)
	}
	inner := level(ZERO, innerParts)
	return concat(text(open), level(plus4, []Doc{brk(fillUnified, "", ZERO, nil), inner}))
}

// allShortItems reports whether every node's source text is under
// MAX_ITEM_LENGTH_FOR_FILLING (10) chars - gjf's fill heuristic.
func (p *printer) allShortItems(ns []*compiler.Node) bool {
	for _, n := range ns {
		if n.End-p.start(n) >= 10 {
			return false
		}
	}
	return true
}

// fillMode picks gjf's inter-item fill: one item per line (UNIFIED) when any
// item carries a comment, else fill (INDEPENDENT) only when every item is short.
func (p *printer) fillMode(anyComment bool, ns []*compiler.Node) FillMode {
	if !anyComment && p.allShortItems(ns) {
		return fillIndependent
	}
	return fillUnified
}

// attachTrailingBlockComment attaches a same-line trailing block comment
// (`item /* note */`) after an item if the next pending comment is one. A line
// comment is left to the statement boundary (it would comment out the following
// separator). Returns the parts and whether a comment was consumed.
func (p *printer) attachTrailingBlockComment(parts []Doc, endPos int) ([]Doc, bool) {
	if p.ci < len(p.comments) {
		t := p.comments[p.ci]
		if !t.line && !t.ownLine && t.pos >= endPos && !strings.ContainsAny(p.text[endPos:t.pos], "\n,") {
			p.ci++
			parts = append(parts, text(" "), text(t.text))
			return parts, true
		}
	}
	return parts, false
}

// itemWithComments renders a delimited-list item with the comments attached to
// it: leading comments before the item (own-line ones get a forced break after,
// which also forces the whole list to break; an inline block comment stays
// inline) and a trailing block comment on the item's line before the separator.
func (p *printer) itemWithComments(node *compiler.Node, render func() Doc) (Doc, bool) {
	var parts []Doc
	comment := false
	for _, c := range p.commentsBefore(p.start(node)) {
		comment = true
		switch {
		case c.ownLine:
			parts = append(parts, reflow(c.text), hardline)
		case c.line:
			parts = append(parts, text(c.text), hardline)
		default:
			pc := c.text
			if norm, ok := reformatParamComment(c.text); ok {
				pc = norm
			}
			parts = append(parts, text(pc), text(" "))
		}
	}
	parts = append(parts, render())
	parts, attached := p.attachTrailingBlockComment(parts, node.End)
	comment = comment || attached
	if len(parts) == 1 {
		return parts[0], comment
	}
	return concat(parts...), comment
}

// listItems builds a delimited list's items with their comments consumed,
// reporting whether any item carried a comment.
func (p *printer) listItems(ns []*compiler.Node, render func(*compiler.Node) Doc) ([]Doc, bool) {
	items := make([]Doc, len(ns))
	anyComment := false
	for i, n := range ns {
		n := n
		doc, c := p.itemWithComments(n, func() Doc { return render(n) })
		items[i] = doc
		if c {
			anyComment = true
		}
	}
	return items, anyComment
}

func (p *printer) parameters(params *compiler.NodeArray, trailing Doc) Doc {
	if params.Len() == 0 {
		// Even with no parameters the trailing run (a `throws` clause + brace) may
		// carry a break, so it must sit in a +4 level to fold and indent correctly.
		inner := []Doc{text("()")}
		if trailing != nil {
			inner = append(inner, trailing)
		}
		return level(plus4, inner)
	}
	// Parameters are never filled (gjf uses a UNIFIED inter-parameter break).
	items, _ := p.listItems(nodes(params), func(n *compiler.Node) Doc { return p.parameter(n.AsParameter()) })
	return p.argsLikeTrailing("(", items, ")", fillUnified, trailing)
}

// paramListChildren returns `(`, break-before-args, the param list and `)` as
// flat children, for the throws case where they must share the signature level
// with the throws break (gjf's open(ZERO) around visitFormals + visitThrowsClause).
// The break before the args is INDEPENDENT so the params stay inline when they
// fit and break to the +4 line only when they overflow.
func (p *printer) paramListChildren(params *compiler.NodeArray) []Doc {
	if params.Len() == 0 {
		return []Doc{text("()")}
	}
	items, _ := p.listItems(nodes(params), func(n *compiler.Node) Doc { return p.parameter(n.AsParameter()) })
	innerParts := make([]Doc, 0, len(items)*2)
	for i, it := range items {
		if i > 0 {
			innerParts = append(innerParts, text(","), brk(fillUnified, " ", ZERO, nil))
		}
		innerParts = append(innerParts, it)
	}
	return []Doc{text("("), brk(fillIndependent, "", ZERO, nil), level(ZERO, innerParts), text(")")}
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
	hasThrows := throws.Len() > 0
	// With no throws clause the body-open token rides inside the param level
	// (rest-of-line rule). With throws, see the hasThrows branch below.
	emptyBody := body != nil && p.blockIsEmpty(body.AsBlock(), p.start(body), body.End)
	bodyToken := " {"
	switch {
	case body == nil:
		bodyToken = ";"
	case emptyBody:
		bodyToken = " {}"
	}
	var sig Doc
	if hasThrows {
		throwsParts := []Doc{text("throws ")}
		for i, t := range nodes(throws) {
			if i > 0 {
				throwsParts = append(throwsParts, text(","), brk(fillUnified, " ", ZERO, nil))
			}
			throwsParts = append(throwsParts, p.typ(t))
		}
		// Throws-type continuation indents +4 beyond the `throws` line, which is
		// itself on sig's +4 continuation -> +8 from the method (col 10).
		throwsIndent := ZERO
		if throws.Len() > 1 {
			throwsIndent = indentConst(4)
		}
		// Mirror gjf's visitMethodDeclaration: `(`, a break-before-args, the param
		// list, `)`, the `throws` break, the throws clause, and the body token are
		// all direct children of ONE +4 level (the indent rides the level, breaks
		// sit at ZERO so they land at +4). Both breaks are INDEPENDENT (gjf's
		// breakToFill): params break to their own line only when they overflow, and
		// `) throws X {` GLUES whenever it fits after the params' rendered end
		// column. When the params explode one-per-line, the param split's flat
		// width overflows the +4 line, propagating mustBreak so the throws clause
		// also breaks - matching gjf with no engine change.
		sig = level(plus4, append(
			p.paramListChildren(params),
			brk(fillIndependent, " ", ZERO, nil),
			level(throwsIndent, throwsParts),
			text(bodyToken),
		))
	} else {
		sig = p.parameters(params, text(bodyToken))
	}
	head = append(head, text(p.raw(name)), sig)
	// Emit the rest of the block when there is a real body, else the signature
	// (with its trailing `;`/` {}`) is complete.
	if body == nil || emptyBody {
		return concat(head...)
	}
	return concat(append(head, p.blockRest(body.AsBlock(), body.End))...)
}

func (p *printer) initializerBlock(d *compiler.InitializerBlockData) Doc {
	static := ""
	if d.IsStatic {
		static = "static "
	}
	return concat(text(static), p.block(d.Body.AsBlock(), d.Body.End))
}

// --- statements ----------------------------------------------------------

func (p *printer) blockIsEmpty(b *compiler.BlockData, startPos, endPos int) bool {
	if b.Statements.Len() > 0 {
		return false
	}
	// Only a comment *inside* the block (after its `{`) makes it non-empty; a
	// pending comment before the block (e.g. an unconsumed parameter comment)
	// must not be miscounted - blockIsEmpty can be queried before those are
	// consumed (methodLike computes the body shape before rendering params).
	return !p.hasCommentBefore(endPos) || p.comments[p.ci].pos <= startPos
}

func (p *printer) block(b *compiler.BlockData, endPos int) Doc {
	// Here the comment cursor is already positioned past anything preceding the
	// block, so a pending comment before endPos is genuinely inside it.
	if b.Statements.Len() == 0 && !p.hasCommentBefore(endPos) {
		return text("{}")
	}
	return concat(text("{"), p.blockRest(b, endPos))
}

// blockRest is a block's body after the opening `{` (the `{` is emitted by the
// caller, so it can be placed inside another level to count toward a wrap
// decision).
func (p *printer) blockRest(b *compiler.BlockData, endPos int) Doc {
	// A comment on the same source line as the opening `{` stays on that line
	// (gjf): `if (...) { // note`. Emit it before the indented body so it rides
	// the brace line, and consume it here so listDocs does not re-emit it own-line.
	var braceComment Doc = text("")
	lead := hardline
	if b.Statements.Len() > 0 {
		braceComment = p.braceTrailingComment(b.Statements.Nodes[0].Pos)
		lead = p.braceLead(b.Statements.Nodes[0].Pos, p.start(b.Statements.Nodes[0]))
	}
	return concat(
		braceComment,
		indent(concat(append([]Doc{lead}, p.listDocs(nodes(b.Statements), false, endPos)...)...)),
		hardline,
		text("}"),
	)
}

// braceTrailingComment consumes and returns a comment that trails the opening
// `{` on its line (` // note`), or "" when the next pending comment starts on a
// later line. afterBrace is the offset just past the `{`.
func (p *printer) braceTrailingComment(afterBrace int) Doc {
	if p.ci >= len(p.comments) {
		return text("")
	}
	c := p.comments[p.ci]
	if c.pos < afterBrace || strings.Contains(p.text[afterBrace:c.pos], "\n") {
		return text("")
	}
	p.ci++
	return concat(text(" "), text(c.text))
}

func (p *printer) localVar(d *compiler.LocalVariableDeclarationStatementData) Doc {
	ds := nodes(d.Declarators)
	parts := []Doc{p.modifiers(d.Modifiers, "var"), p.typ(d.Type), text(" ")}
	for i, v := range ds {
		if i > 0 {
			parts = append(parts, text(", "))
		}
		// The terminating `;` rides into the last declarator's initializer.
		var tr Doc
		if i == len(ds)-1 {
			tr = text(";")
		}
		parts = append(parts, p.declarator(v.AsVariableDeclarator(), tr))
	}
	return concat(parts...)
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
	cond := text("")
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
	// gjf visitEnhancedForLoop: "for (" open(+4) param " :" breakOp(" ") expr
	// close ")". The iterable breaks after the ":" at +4 when it overflows.
	return concat(
		concat(text("for ("), level(plus4, []Doc{p.parameter(s.Parameter.AsParameter()), text(" :"), line, p.node(s.Expression)}), text(")")),
		p.clauseBody(s.Statement),
	)
}

func (p *printer) tryStatement(s *compiler.TryStatementData) Doc {
	parts := []Doc{text("try")}
	if s.Resources.Len() > 0 {
		// The first resource stays on the `try (` line; subsequent ones break
		// before themselves at +4 (one per line), each `;`-terminated. A trailing
		// `;` after the last resource in source is preserved as `; )`.
		res := nodes(s.Resources)
		last := res[len(res)-1]
		closeTok := ")"
		if idx := compiler.SkipTrivia(p.text, last.End); idx < len(p.text) && p.text[idx] == ';' {
			closeTok = "; )"
		}
		if len(res) == 1 {
			// A single resource stays on the `try (` line; its own initializer level
			// supplies the +4 continuation indent, so no extra resource-list level
			// (which would double-indent the broken initializer to +8).
			parts = append(parts, text(" ("), p.resource(res[0].AsResource()), text(closeTok))
		} else {
			var inner []Doc
			for i, r := range res {
				if i > 0 {
					inner = append(inner, text(";"), brk(fillUnified, " ", ZERO, nil))
				}
				inner = append(inner, p.resource(r.AsResource()))
			}
			parts = append(parts, text(" ("), level(plus4, inner), text(closeTok))
		}
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
	head := []Doc{p.modifiers(r.Modifiers, "inline")}
	if r.Type != nil {
		head = append(head, concat(p.typ(r.Type), text(" ")))
	}
	if r.Name != nil {
		head = append(head, text(p.raw(r.Name)))
	}
	if r.Initializer == nil {
		return concat(head...)
	}
	// Like a variable declarator, a long initializer folds onto a +4
	// continuation line after `=` (gjf), rather than breaking the RHS in place.
	if r.Initializer.Kind == compiler.ArrayInitializer {
		return concat(concat(head...), text(" = "), p.node(r.Initializer))
	}
	return concat(concat(head...), text(" ="), level(plus4, []Doc{line, p.node(r.Initializer)}))
}

func (p *printer) switchLike(expr *compiler.Node, clauses *compiler.NodeArray, endPos int) Doc {
	// Comments before a `case`/`default` label sit on their own line at the
	// clause indent (gjf), so consume them per clause like a member list does.
	// A single source blank line between clauses is preserved (gjf), so the
	// separator before a clause becomes a double hardline when the source left a
	// blank between the previous clause and this one's first rendered thing.
	type entry struct {
		doc   Doc
		blank bool
	}
	var entries []entry
	prevEnd := -1
	for _, c := range nodes(clauses) {
		comments := p.commentsBefore(p.start(c))
		start := p.start(c)
		if len(comments) > 0 {
			start = comments[0].pos
		}
		leading := prevEnd >= 0 && p.blankBeforePos(prevEnd, start)
		for _, cm := range comments {
			entries = append(entries, entry{reflow(cm.text), leading})
			leading = false
		}
		entries = append(entries, entry{p.switchClause(c.AsSwitchClause(), c.End), leading})
		prevEnd = c.End
	}
	for _, cm := range p.commentsBefore(endPos) {
		entries = append(entries, entry{reflow(cm.text), false})
	}
	var body []Doc
	for i, e := range entries {
		if i > 0 {
			if e.blank {
				body = append(body, concat(hardline, hardline))
			} else {
				body = append(body, hardline)
			}
		}
		body = append(body, e.doc)
	}
	return concat(
		group(concat(text("switch ("), p.node(expr), text(")"))),
		text(" {"),
		indent(concat(append([]Doc{hardline}, body...)...)),
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
		// A comment before the body sits own-line on the +4 continuation and
		// forces the break, so `case X ->` keeps only the label (gjf), like the
		// lambda-body case below.
		bodyStart := p.start(stmts[0])
		if p.hasCommentBefore(bodyStart) {
			var parts []Doc
			for _, c := range p.commentsBefore(bodyStart) {
				parts = append(parts, reflow(c.text), hardline)
			}
			ss := make([]Doc, len(stmts))
			for i, st := range stmts {
				ss[i] = p.node(st)
			}
			parts = append(parts, join(text(" "), ss))
			return concat(label, guard, text(" ->"), level(plus4, []Doc{hardline, concat(parts...)}))
		}
		ss := make([]Doc, len(stmts))
		for i, st := range stmts {
			ss[i] = p.node(st)
		}
		// A non-block arrow body (an expression, throw, or yield statement) folds
		// onto a +4 continuation line after the `->` when it does not fit (gjf).
		return concat(label, guard, text(" ->"), level(plus4, []Doc{line, join(text(" "), ss)}))
	}
	return concat(label, guard, text(":"), indent(concat(append([]Doc{hardline}, p.listDocs(nodes(c.Statements), false, end)...)...)))
}

// --- expressions ---------------------------------------------------------

// binary lays out an operator chain. gjf collects all same-precedence operands
// into one +4 level and breaks *before* each operator; the breaks fill when
// every operand is short, else go one per line.
func (p *printer) binary(node *compiler.Node) Doc {
	return p.binaryTrailing(node, nil)
}

func (p *printer) binaryTrailing(node *compiler.Node, trailing Doc) Doc {
	b := node.AsBinaryExpression()
	prec := precedence(b.OperatorToken)
	var operands []*compiler.Node
	var operators []string
	p.walkInfix(prec, node, &operands, &operators)
	fill := p.fillMode(false, operands)
	parts := []Doc{p.node(operands[0])}
	for i, op := range operators {
		parts = append(parts, brk(fill, " ", ZERO, nil), text(op), text(" "), p.node(operands[i+1]))
	}
	// A statement's trailing `;` rides inside the +4 level (gjf counts it in the
	// level width), so `a && b;` breaks when the `;` is what tips it past 100.
	if trailing != nil {
		parts = append(parts, trailing)
	}
	return level(plus4, parts)
}

// walkInfix flattens a left-associative chain of same-precedence binary
// operators into a flat operand/operator list (a + b - c -> [a,b,c], [+,-]).
func (p *printer) walkInfix(prec int, node *compiler.Node, operands *[]*compiler.Node, operators *[]string) {
	if node.Kind == compiler.BinaryExpression && precedence(node.AsBinaryExpression().OperatorToken) == prec {
		b := node.AsBinaryExpression()
		p.walkInfix(prec, b.Left, operands, operators)
		op := compiler.TokenToString(b.OperatorToken)
		if op == "" {
			op = "?"
		}
		*operators = append(*operators, op)
		p.walkInfix(prec, b.Right, operands, operators)
	} else {
		*operands = append(*operands, node)
	}
}

// assignment lays out `a = b` / `a += b`: the RHS folds onto a +4 continuation
// line after the operator when it does not fit.
func (p *printer) assignment(e *compiler.AssignmentExpressionData) Doc {
	op := compiler.TokenToString(e.OperatorToken)
	if op == "" {
		op = "="
	}
	return concat(p.node(e.Left), text(" "), text(op), level(plus4, []Doc{line, p.node(e.Right)}))
}

// dotChain lays out a dotted dereference chain (`a.b().c().d`). A chain with at
// least two method invocations (a builder chain) breaks before every dot at +4;
// a chain with at most one invocation stays glued unless its receiver is itself
// a call. The first dot does not break after a tiny receiver.
// ponytail: type-name prefixes and stream chains are not yet treated as units.
func (p *printer) dotChain(root *compiler.Node) Doc {
	return p.dotChainTrailing(root, nil)
}

func (p *printer) dotChainTrailing(root *compiler.Node, trailing Doc) Doc {
	// Collect the chain's links WITHOUT rendering them yet: a link's argument
	// list consumes comments, and comments must be consumed in source order (left
	// to right). Rendering eagerly here would consume the OUTER call's args before
	// the receiver's, mis-attributing a receiver-arg comment (e.g.
	// `new Pretty(/*writer*/ null, /*sourceOutput*/ true).operatorName(tag)`).
	type linkT struct {
		isCall bool
		name   string
		render func() Doc
	}
	var links []linkT
	cur := root
	trailingRouted := false
	for {
		switch {
		case cur.Kind == compiler.CallExpression &&
			cur.AsCallExpression().Expression.Kind == compiler.PropertyAccessExpression:
			ce := cur.AsCallExpression()
			pa := ce.Expression.AsPropertyAccessExpression()
			// The rightmost link (last in source order) carries the statement's
			// trailing token inside its argument list (rest-of-line rule).
			var argTrailing Doc
			if len(links) == 0 {
				argTrailing = trailing
				if trailing != nil {
					trailingRouted = true
				}
			}
			name := p.raw(pa.Name)
			links = append([]linkT{{
				isCall: true,
				name:   name,
				// Explicit method type arguments go between the dot and the name:
				// `obj.<String>foo(x)`, not `obj.foo<String>(x)`.
				render: func() Doc {
					return concat(text("."), p.typeArguments(ce.TypeArguments), text(name), p.argListTrailing(ce.Arguments, argTrailing))
				},
			}}, links...)
			cur = pa.Expression
			continue
		case cur.Kind == compiler.PropertyAccessExpression:
			pa := cur.AsPropertyAccessExpression()
			name := p.raw(pa.Name)
			links = append([]linkT{{isCall: false, name: name, render: func() Doc { return concat(text("."), text(name)) }}}, links...)
			cur = pa.Expression
			continue
		}
		break
	}
	// Render the base (leftmost receiver) first, then each link in source order,
	// so comments are consumed left to right.
	base := p.node(cur)
	linkDocs := make([]Doc, len(links))
	for i, l := range links {
		linkDocs[i] = l.render()
	}
	// A trailing token not routed into a rightmost call's args (chain ends in a
	// field access) is appended after the whole chain.
	finish := func(doc Doc) Doc {
		if trailing == nil || trailingRouted {
			return doc
		}
		return concat(doc, trailing)
	}
	callCount := 0
	for _, l := range links {
		if l.isCall {
			callCount++
		}
	}
	baseIsCall := cur.Kind == compiler.CallExpression
	// A single dereference invocation after a non-invocation prefix stays glued
	// (`myField.foo()`); but when the receiver is itself a call or a primary
	// expression like `new X()` (gjf's `node != null` path) the dereference still
	// breaks. A pure field-access chain still breaks before its last selectors
	// when it overflows (the break path below, gated by the prefix).
	baseIsNew := cur.Kind == compiler.ObjectCreationExpression
	if callCount == 1 && !baseIsCall && !baseIsNew {
		parts := []Doc{base}
		parts = append(parts, linkDocs...)
		return finish(concat(parts...))
	}
	// The leading links glued to the base (no break before them): a type-name
	// prefix (`ImmutableList.builder()` stays a unit), else just the first link
	// when the receiver is tiny.
	baseLen := cur.End - p.start(cur)
	glue := 0
	if baseLen <= p.mult*4 {
		glue = 1
	}
	if cur.Kind == compiler.Identifier {
		names := []string{p.raw(cur)}
		for _, l := range links {
			names = append(names, l.name)
			if l.isCall {
				break // the first method name ends the type-name prefix
			}
		}
		if pfx := typePrefixLength(names); pfx >= 0 {
			glue = pfx
		}
	}
	// gjf glues the receiver through a `.stream()`/`.parallelStream()`/
	// `.toBuilder()` call (its index becomes a chain-prefix boundary), so
	// `x.stream().map(..).collect(..)` keeps `x.stream()` on the first line and
	// breaks before the rest - rather than stranding the receiver on its own.
	for i, l := range links {
		if l.isCall && streamPrefixMethods[l.name] && i+1 > glue {
			glue = i + 1
		}
	}
	parts := []Doc{base}
	for i := range links {
		if i >= glue {
			parts = append(parts, brk(fillUnified, "", ZERO, nil))
		}
		parts = append(parts, linkDocs[i])
	}
	return finish(level(plus4, parts))
}

func (p *printer) call(e *compiler.CallExpressionData) Doc {
	return p.callTrailing(e, nil)
}

// statementTail emits an expression that a statement terminates with trailing
// (a `;`), routing that token into the expression's tail delimited level (a
// plain call or constructor argument list) so the list wraps when the whole
// `(...);` run overflows - gjf's rest-of-line rule. Mirrors node()'s dispatch:
// a call on a `.`-access renders via dotChain, which takes no trailing token,
// so only a plain `foo(args)` call routes the `;` inward.
func (p *printer) statementTail(e *compiler.Node, trailing Doc) Doc {
	switch e.Kind {
	case compiler.CallExpression:
		ce := e.AsCallExpression()
		// Mirror node()'s dispatch: a call on a `.`-access renders via dotChain.
		if ce.Expression.Kind == compiler.PropertyAccessExpression {
			return p.dotChainTrailing(e, trailing)
		}
		return p.callTrailing(ce, trailing)
	case compiler.PropertyAccessExpression:
		return p.dotChainTrailing(e, trailing)
	case compiler.BinaryExpression:
		return p.binaryTrailing(e, trailing)
	case compiler.ObjectCreationExpression:
		oc := e.AsObjectCreationExpression()
		if oc.ClassBody == nil {
			return p.objectCreationTrailing(oc, e.End, trailing)
		}
	case compiler.AssignmentExpression:
		// `x = foo(...);` - the `;` rides into the assignment's RHS tail.
		a := e.AsAssignmentExpression()
		op := compiler.TokenToString(a.OperatorToken)
		if op == "" {
			op = "="
		}
		return concat(p.node(a.Left), text(" "), text(op), level(plus4, []Doc{line, p.statementTail(a.Right, trailing)}))
	}
	if trailing == nil {
		return p.node(e)
	}
	return concat(p.node(e), trailing)
}

func (p *printer) callTrailing(e *compiler.CallExpressionData, trailing Doc) Doc {
	return concat(p.node(e.Expression), p.typeArguments(e.TypeArguments), p.argListTrailing(e.Arguments, trailing))
}

func (p *printer) argListTrailing(args *compiler.NodeArray, trailing Doc) Doc {
	if args.Len() == 0 {
		if trailing != nil {
			return concat(text("()"), trailing)
		}
		return text("()")
	}
	argNodes := nodes(args)
	anyComment := false
	lastI := len(argNodes) - 1
	// Render each argument with the token that FOLLOWS it routed into the
	// argument's innermost delimited level: the inter-argument `,` for a non-last
	// arg, or the closing `)` (plus any outer trailing token) for the last. gjf
	// breaks a nested call/chain when the whole `(...)<close><sep>` run overflows,
	// so that trailing token must count in the nested level's own fit check
	// (rest-of-line rule); this also subsumes the lone dot-chain case. An argument
	// carrying a same-line trailing comment cannot route - the comment must sit
	// between the value and the separator - so it appends the token instead.
	as := make([]Doc, len(argNodes))
	for i, a := range argNodes {
		var parts []Doc
		// Leading comments: a block comment renders inline before the argument
		// (`/* a= */ 1`); a line comment forces a break after itself.
		for _, c := range p.commentsBefore(p.start(a)) {
			anyComment = true
			if c.line {
				parts = append(parts, text(c.text), hardline)
			} else {
				pc := c.text
				if norm, ok := reformatParamComment(c.text); ok {
					pc = norm
				}
				parts = append(parts, text(pc), text(" "))
			}
		}
		var follow Doc = text(",")
		if i == lastI {
			if trailing != nil {
				follow = concat(text(")"), trailing)
			} else {
				follow = text(")")
			}
		}
		hasTrailingComment := false
		if p.ci < len(p.comments) {
			t := p.comments[p.ci]
			hasTrailingComment = !t.line && !t.ownLine && t.pos >= a.End &&
				!strings.ContainsAny(p.text[a.End:t.pos], "\n,")
		}
		if hasTrailingComment {
			parts = append(parts, p.node(a))
			var attached bool
			parts, attached = p.attachTrailingBlockComment(parts, a.End)
			anyComment = anyComment || attached
			parts = append(parts, follow)
		} else {
			parts = append(parts, p.statementTail(a, follow))
		}
		if len(parts) == 1 {
			as[i] = parts[0]
		} else {
			as[i] = concat(parts...)
		}
	}
	fill := p.fillMode(anyComment, argNodes)
	// gjf's format-method layout (String.format / printf-style): when the first
	// arg is a string-literal concatenation carrying a format specifier, it sits
	// on its own line and the value args fill below it as a group - instead of
	// every arg going one-per-line just because the long format string is not a
	// "short item". Mirrors JavaInputAstVisitor.addArguments / isFormatMethod. The
	// `,` after the format string and the closing `)` are already in the items.
	if !anyComment && p.isFormatMethod(argNodes) {
		restFill := p.fillMode(false, argNodes[1:])
		var restInner []Doc
		for i, it := range as[1:] {
			if i > 0 {
				restInner = append(restInner, brk(restFill, " ", ZERO, nil))
			}
			restInner = append(restInner, it)
		}
		return concat(text("("), level(plus4, []Doc{
			brk(fillUnified, "", ZERO, nil),
			level(ZERO, []Doc{as[0], brk(fillUnified, " ", ZERO, nil), level(ZERO, restInner)}),
		}))
	}
	// The inter-argument `,` and the closing `)` are routed into the items, so the
	// inner level only joins them with fill breaks; its own fit check then counts
	// the `)` and any outer trailing token (rest-of-line).
	var innerParts []Doc
	for i, it := range as {
		if i > 0 {
			innerParts = append(innerParts, brk(fill, " ", ZERO, nil))
		}
		innerParts = append(innerParts, it)
	}
	return concat(text("("), level(plus4, []Doc{brk(fillUnified, "", ZERO, nil), level(ZERO, innerParts)}))
}

// isFormatMethod is gjf's isFormatMethod: a call whose first argument is a
// string-literal concatenation containing a format specifier, with >= 2 args.
func (p *printer) isFormatMethod(args []*compiler.Node) bool {
	return len(args) >= 2 && p.isFormatStringConcat(args[0])
}

// isFormatStringConcat reports whether node is built only from string literals
// joined by `+` and at least one literal carries a format specifier - gjf's
// isStringConcat.
func (p *printer) isFormatStringConcat(node *compiler.Node) bool {
	hasSpecifier := false
	var walk func(n *compiler.Node) bool
	walk = func(n *compiler.Node) bool {
		switch n.Kind {
		case compiler.StringLiteral, compiler.TextBlockLiteral:
			if hasFormatSpecifier(p.raw(n)) {
				hasSpecifier = true
			}
			return true
		case compiler.BinaryExpression:
			b := n.AsBinaryExpression()
			if b.OperatorToken == compiler.PlusToken {
				return walk(b.Left) && walk(b.Right)
			}
		}
		return false
	}
	return walk(node) && hasSpecifier
}

// hasFormatSpecifier matches gjf's FORMAT_SPECIFIER pattern `%|\{[0-9]\}`.
func hasFormatSpecifier(s string) bool {
	if strings.Contains(s, "%") {
		return true
	}
	for i := 0; i+2 < len(s); i++ {
		if s[i] == '{' && s[i+1] >= '0' && s[i+1] <= '9' && s[i+2] == '}' {
			return true
		}
	}
	return false
}

func (p *printer) objectCreation(e *compiler.ObjectCreationExpressionData, end int) Doc {
	return p.objectCreationTrailing(e, end, nil)
}

func (p *printer) objectCreationTrailing(e *compiler.ObjectCreationExpressionData, end int, trailing Doc) Doc {
	var parts []Doc
	if e.Qualifier != nil {
		parts = append(parts, p.node(e.Qualifier), text("."))
	}
	// A trailing token only rides inside the argument list when there is no
	// anonymous class body (otherwise it belongs after the `}`).
	argTrailing := trailing
	if e.ClassBody != nil {
		argTrailing = nil
	}
	parts = append(parts, text("new "), p.typ(e.Type), p.argListTrailing(e.Arguments, argTrailing))
	if e.ClassBody != nil {
		parts = append(parts, text(" "), p.body(e.ClassBody, end))
		if trailing != nil {
			parts = append(parts, trailing)
		}
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
	// gjf: contents indent +2; when broken, elements fill (INDEPENDENT) if all
	// short, else one per line (UNIFIED); the closing `}` goes on its own line
	// back at the parent indent (a -2 break cancels the +2).
	// A trailing comma in source is the author's "keep this vertical" signal:
	// gjf preserves the comma and FORCES one element per line.
	els := nodes(e.Elements)
	trailingComma := false
	if idx := compiler.SkipTrivia(p.text, els[len(els)-1].End); idx < len(p.text) && p.text[idx] == ',' {
		trailingComma = true
	}
	items, anyComment := p.listItems(els, func(el *compiler.Node) Doc { return p.node(el) })
	// A comment forces one-per-line (gjf), else short items fill.
	fill := p.fillMode(anyComment, els)
	// A trailing comma forces the braces open (newline after `{` and before `}`)
	// but elements still fill (`{\n  1, 2, 3,\n}`).
	open := fillUnified
	if trailingComma {
		open = fillForced
	}
	var innerParts []Doc
	for i, el := range items {
		if i > 0 {
			innerParts = append(innerParts, text(","), brk(fill, " ", ZERO, nil))
		}
		innerParts = append(innerParts, el)
	}
	if trailingComma {
		innerParts = append(innerParts, text(","))
	}
	inner := level(ZERO, innerParts)
	return concat(
		text("{"),
		level(plus2, []Doc{brk(open, "", ZERO, nil), inner, brk(open, "", minus2, nil)}),
		text("}"),
	)
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
	if e.Body.Kind == compiler.Block {
		return concat(head, text(" -> "), p.block(e.Body.AsBlock(), e.Body.End))
	}
	// A comment before an expression body sits own-line at a +8 continuation
	// indent (gjf), forcing `-> ` onto its own line; the comment forces the break.
	if p.hasCommentBefore(p.start(e.Body)) {
		var parts []Doc
		for _, c := range p.commentsBefore(p.start(e.Body)) {
			parts = append(parts, reflow(c.text), hardline)
		}
		parts = append(parts, p.node(e.Body))
		return concat(head, text(" ->"), level(plus4, []Doc{hardline, concat(parts...)}))
	}
	// An expression body folds onto a +4 continuation line after `->` when it
	// does not fit (gjf), like the switch-arrow body above.
	return concat(head, text(" ->"), level(plus4, []Doc{line, p.node(e.Body)}))
}

// conditional lays out a ternary: the condition stays on the line, `?` and `:`
// break onto +4 continuation lines (UNIFIED).
func (p *printer) conditional(e *compiler.ConditionalExpressionData) Doc {
	return level(plus4, []Doc{
		p.node(e.Condition),
		brk(fillUnified, " ", ZERO, nil),
		text("? "),
		p.node(e.WhenTrue),
		brk(fillUnified, " ", ZERO, nil),
		text(": "),
		p.node(e.WhenFalse),
	})
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
		return p.statementTail(node.AsExpressionStatement().Expression, text(";"))
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
			return concat(text("return "), p.statementTail(r.Expression, text(";")))
		}
		return text("return;")
	case compiler.ThrowStatement:
		return concat(text("throw "), p.statementTail(node.AsThrowStatement().Expression, text(";")))
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
		return p.switchLike(s.Expression, s.Clauses, node.End)

	case compiler.SwitchExpression:
		s := node.AsSwitchExpression()
		return p.switchLike(s.Expression, s.Clauses, node.End)
	case compiler.BinaryExpression:
		return p.binary(node)
	case compiler.AssignmentExpression:
		return p.assignment(node.AsAssignmentExpression())
	case compiler.ConditionalExpression:
		return p.conditional(node.AsConditionalExpression())
	case compiler.CallExpression:
		e := node.AsCallExpression()
		// A method call on a `.`-access is part of a dereference chain.
		if e.Expression.Kind == compiler.PropertyAccessExpression {
			return p.dotChain(node)
		}
		return p.call(e)
	case compiler.PropertyAccessExpression:
		e := node.AsPropertyAccessExpression()
		// Route through the chain layout only when the receiver is itself a
		// call/access (a real chain); a plain `obj.field` stays inline.
		k := e.Expression.Kind
		if k == compiler.CallExpression || k == compiler.PropertyAccessExpression || k == compiler.ElementAccessExpression {
			return p.dotChain(node)
		}
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
		// gjf visitTypeCast: open(+4); "(" type ")" breakOp(" ") expr; close.
		// The cast and its operand share a +4 level, so a multi-line operand
		// breaks after the ")" instead of gluing to it.
		return level(plus4, []Doc{text("("), join(text(" & "), types), text(")"), brk(fillUnified, " ", ZERO, nil), p.node(e.Expression)})
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
	if t, ok := d.(*token); ok {
		return t.text == ""
	}
	return false
}

// precedenceTable gives Java binary-operator precedence groups (higher binds
// tighter); operators in the same group flatten into one chain when wrapping.
var precedenceTable = map[compiler.SyntaxKind]int{
	compiler.AsteriskToken: 10, compiler.SlashToken: 10, compiler.PercentToken: 10,
	compiler.PlusToken: 9, compiler.MinusToken: 9,
	compiler.LessThanLessThanToken: 8, compiler.GreaterThanGreaterThanToken: 8,
	compiler.GreaterThanGreaterThanGreaterThanToken: 8,
	compiler.LessThanToken:                          7, compiler.GreaterThanToken: 7,
	compiler.LessThanEqualsToken: 7, compiler.GreaterThanEqualsToken: 7,
	compiler.EqualsEqualsToken: 6, compiler.ExclamationEqualsToken: 6,
	compiler.AmpersandToken: 5, compiler.CaretToken: 4, compiler.BarToken: 3,
	compiler.AmpersandAmpersandToken: 2, compiler.BarBarToken: 1,
}

func precedence(op compiler.SyntaxKind) int { return precedenceTable[op] }

// caseFormat / typePrefixLength port google-java-format's TypeNameClassifier:
// the inclusive end index of the longest leading run of nameParts that looks
// like a type name (optionally with one trailing static member), or -1. Lets a
// chain keep a type prefix glued (`ImmutableList.builder()` stays a unit).
type caseFormat int

const (
	caseUpper caseFormat = iota
	caseLower
	caseUpperCamel
	caseLowerCamel
)

func javaCaseFormat(name string) caseFormat {
	firstUpper, hasUpper, hasLower, first := false, false, false, true
	for _, c := range name {
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !isLetter {
			continue
		}
		if first {
			firstUpper = c >= 'A' && c <= 'Z'
			first = false
		}
		if c >= 'A' && c <= 'Z' {
			hasUpper = true
		}
		if c >= 'a' && c <= 'z' {
			hasLower = true
		}
	}
	if firstUpper {
		if hasLower || len(name) == 1 {
			return caseUpperCamel
		}
		return caseUpper
	}
	if hasUpper {
		return caseLowerCamel
	}
	return caseLower
}

type tyState int

const (
	tyStart tyState = iota
	tyType
	tyFirstStatic
	tyAmbiguous
	tyReject
)

func tySingleUnit(s tyState) bool { return s == tyType || s == tyFirstStatic }

func tyNext(state tyState, n caseFormat) tyState {
	switch state {
	case tyStart:
		switch n {
		case caseUpper:
			return tyAmbiguous
		case caseLowerCamel:
			return tyReject
		case caseLower:
			return tyStart
		default: // caseUpperCamel
			return tyType
		}
	case tyType:
		if n == caseUpperCamel {
			return tyType
		}
		return tyFirstStatic
	case tyFirstStatic:
		return tyReject
	case tyAmbiguous:
		switch n {
		case caseUpper:
			return tyAmbiguous
		case caseUpperCamel:
			return tyType
		default:
			return tyReject
		}
	default:
		return tyReject
	}
}

func typePrefixLength(nameParts []string) int {
	state := tyStart
	typeLength := -1
	for i, part := range nameParts {
		state = tyNext(state, javaCaseFormat(part))
		if state == tyReject {
			break
		}
		if tySingleUnit(state) {
			typeLength = i
		}
	}
	return typeLength
}
