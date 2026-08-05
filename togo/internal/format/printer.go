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
	"unicode/utf8"

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
	// gjf relies on javac attaching a TYPE_USE annotation to the type it
	// precedes; we have no symbol resolution, so we list the jetbrains nullness
	// annotations (declared @Target(TYPE_USE)) to keep them inline before the type
	// as javac/gjf would. They are not in gjf's own list (it does not need them).
	"org.jetbrains.annotations.NotNull":  true,
	"org.jetbrains.annotations.Nullable": true,
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
		// A reflow leaf carries a raw comment or a multi-line text block; rewrite it
		// at its column.
		commentRewriter: func(raw string, col int, noWrap bool) string {
			if strings.HasPrefix(raw, `"""`) {
				return reindentTextBlock(raw)
			}
			return rewriteComment(raw, col, strings.HasPrefix(raw, "//"), noWrap)
		},
	})
	// Safety net: the printer attaches comments at member/statement granularity.
	// If a comment sat somewhere it does not yet handle, refuse rather than
	// silently drop it - the CLI then leaves the file untouched.
	if !p.allCommentsEmitted() {
		return "", ErrUnsupportedSyntax
	}
	// Exactly one trailing newline, like google-java-format, in the source's own
	// line separator (gjf's Newlines.guessLineSeparator: the first one it sees).
	out = strings.TrimRight(out, "\r\n") + "\n"
	if sep := guessLineSeparator(p.text); sep != "\n" {
		out = strings.NewReplacer("\r\n", sep, "\r", sep, "\n", sep).Replace(out)
	}
	return out, nil
}

// guessLineSeparator returns the first line separator in text, mirroring gjf's
// Newlines.guessLineSeparator.
func guessLineSeparator(text string) string {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				return "\r\n"
			}
			return "\r"
		case '\n':
			return "\n"
		}
	}
	return "\n"
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
	// emittedAhead holds comment indices already emitted out of order (see
	// braceTrailAhead).
	emittedAhead map[int]bool
	// importTrailing holds each import's same-line trailing comment (see
	// importTrailingComments).
	importTrailing map[*compiler.Node]string
	// mult is the indent multiplier (1 google / 2 aosp); a few gjf decisions
	// (e.g. the method-chain "small receiver" threshold) need it at build time.
	mult int
	// typeAnnotationNames holds simple names imported as a well-known nullness
	// type annotation (e.g. "Nullable" with `import org.jspecify...Nullable;`).
	typeAnnotationNames map[string]bool
}

func newPrinter(sf *compiler.Node, mult int) *printer {
	text := sf.AsSourceFile().Text
	p := &printer{sf: sf, text: text, comments: collectComments(text), mult: mult, typeAnnotationNames: map[string]bool{}, emittedAhead: map[int]bool{}, importTrailing: map[*compiler.Node]string{}}
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

// inlineBlockComments consumes the block comments sitting inline just before
// pos (no newline between them and it) and renders them as prefixes. gjf hangs
// a comment off the token it precedes, so a block comment between a
// declaration's modifiers and its type stays there instead of drifting to the
// end of the statement.
func (p *printer) inlineBlockComments(pos int) []Doc {
	var out []Doc
	for p.ci < len(p.comments) {
		c := p.comments[p.ci]
		if c.line || c.pos >= pos || strings.Contains(p.text[c.end:pos], "\n") {
			break
		}
		p.ci++
		out = append(out, reflowNoWrap(c.text), text(" "))
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

	// gjf's addBodyDeclarations: a member wants a blank line before it unless it
	// is a field without javadoc, and a member after one that wanted a blank gets
	// one too (`lastOneGotBlankLineBefore`).
	prevWantsBlank := false

	for i, item := range list {
		itemStart := p.start(item)
		// The blank line required before this whole entry (its leading comments
		// and the item) - g-j-f puts it before a method's doc comment, not between.
		leadComments := p.commentsBefore(itemStart)
		firstPos := itemStart
		if len(leadComments) > 0 {
			firstPos = leadComments[0].pos
		}
		wantsBlank := forced &&
			(isBlankForcing(item.Kind) || fieldSpansMultipleLines(item) || anyJavadoc(leadComments))
		entryBlank := i > 0 && (p.blankBeforePos(prevEnd, firstPos) || wantsBlank || prevWantsBlank)
		prevWantsBlank = wantsBlank
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

		var lastOwnLead *comment
		for _, c := range leadComments {
			if !c.ownLine && !pushedInEntry && i > 0 {
				// A comment after code on the same line: attach to the previous entry.
				out[len(out)-1] = concat(out[len(out)-1], text(" "), reflowNoWrap(c.text))
			} else {
				pushEntry(reflow(c.text), p.blankBeforePos(prevEnd, c.pos))
				lastOwnLead = &c
			}
			prevEnd = c.end
		}

		// gjf preserves one source blank line between a leading own-line comment
		// and the item it precedes (a "section header" comment set off from its
		// member). Only when own-line comments were already pushed for this entry.
		afterComments := prevEnd
		// A same-line trailing comment counts towards the item's own fit check, so
		// route it into the item like the trailing `;` (see nodeWithTail). A
		// multi-line block comment cannot: its width would force every break.
		var tail Doc
		if peeked, ok := p.peekTrailingComment(item); ok && !strings.Contains(peeked.text, "\n") {
			tail = concat(text(" "), reflowNoWrap(peeked.text))
		}
		var itemDoc Doc
		if tail != nil {
			itemDoc = p.nodeWithTail(item, tail)
		} else {
			itemDoc = p.node(item)
		}
		if inlineLead != nil {
			itemDoc = concat(reflowNoWrap(inlineLead.text), text(" "), itemDoc)
		}
		if trailing, ok := p.trailingCommentAfter(item); ok {
			if tail == nil {
				itemDoc = concat(itemDoc, text(" "), reflowNoWrap(trailing.text))
			}
			prevEnd = trailing.end
		} else {
			prevEnd = item.End
		}
		// ... but a javadoc comment documents the declaration, so gjf glues it:
		// the source blank between `*/` and the declaration is dropped.
		itemBlank := pushedInEntry && inlineLead == nil && !isJavadocComment(lastOwnLead) &&
			p.blankBeforePos(afterComments, itemStart)
		pushEntry(itemDoc, itemBlank)
	}

	for _, c := range p.commentsBefore(endPos) {
		push(reflow(c.text), p.blankBeforePos(prevEnd, c.pos))
		prevEnd = c.end
	}
	return out
}

// trailingCommentAfter returns a comment immediately after node on the same line.
// trailsDirectly reports whether a comment at pos still trails the construct
// ending at from: same line, with nothing but whitespace and the construct's own
// separator (`,`, `;`) in between. Any further code in between means the comment
// trails THAT code - without this check `{{0, 1}, {2, 3}}; // note` hung the
// comment on the literal `1`, which forced the initializer open (differently on
// a re-run).
func (p *printer) trailsDirectly(from, pos int) bool {
	if from < 0 || pos > len(p.text) || from > pos {
		return false
	}
	seenSeparator := false
	for i := from; i < pos; i++ {
		switch {
		case isInlineWhitespace(p.text[i]):
		case (p.text[i] == ';' || p.text[i] == ',') && !seenSeparator:
			seenSeparator = true
		default:
			return false
		}
	}
	return true
}

// lastCommentEndBefore returns the end of the last comment between from and
// end, or from when there is none - i.e. where a block's rendered content
// really stops.
func (p *printer) lastCommentEndBefore(from, end int) int {
	if c, ok := p.lastCommentBefore(from, end); ok {
		return c.end
	}
	return from
}

// lastCommentBefore returns the last comment between from and end, if any.
func (p *printer) lastCommentBefore(from, end int) (comment, bool) {
	var out comment
	found := false
	for i := p.ci; i < len(p.comments) && p.comments[i].pos < end; i++ {
		if p.comments[i].pos >= from {
			out = p.comments[i]
			found = true
		}
	}
	return out, found
}

// blankAfterLastComment reports whether a blank line must be kept before the
// closing brace at endPos-1: gjf forces one when the body ends with a comment
// that the source separates from the brace by a blank line (OpsBuilder's
// allowBlankAfterLastComment, which excludes javadoc), whatever the enclosing
// construct wants.
func (p *printer) blankAfterLastComment(from, endPos int) bool {
	c, ok := p.lastCommentBefore(from, endPos)
	return ok && !isJavadocComment(&c) && p.blankBeforePos(c.end, endPos-1)
}

// peekTrailingComment returns the comment that will trail node once the node
// itself is rendered - the same test as trailingCommentAfter, but looking past
// the comments inside node (which its own rendering consumes first) and
// consuming nothing.
func (p *printer) peekTrailingComment(node *compiler.Node) (comment, bool) {
	i := p.ci
	for i < len(p.comments) && p.comments[i].pos < node.End {
		i++
	}
	if i >= len(p.comments) {
		return comment{}, false
	}
	c := p.comments[i]
	if c.ownLine || !p.trailsDirectly(node.End, c.pos) {
		return comment{}, false
	}
	return c, true
}

// nodeWithTail renders item with tail - a same-line trailing comment - routed
// INSIDE the level that owns the item's last break, the way
// google-java-format's DocBuilder pulls a trailing token into its appendLevel
// ("the semicolon moves inside the inner Doc"). The comment's width then drives
// the item's fit check, so `foo(bar); // comment` past column 100 breaks the
// call instead of overflowing. Kinds that do not route their `;` just append it.
func (p *printer) nodeWithTail(item *compiler.Node, tail Doc) Doc {
	semi := concat(text(";"), tail)
	switch item.Kind {
	case compiler.ExpressionStatement:
		return p.statementTail(item.AsExpressionStatement().Expression, semi)
	case compiler.ReturnStatement:
		if r := item.AsReturnStatement(); r.Expression != nil {
			return concat(text("return "), p.statementTail(r.Expression, semi))
		}
	case compiler.ThrowStatement:
		return concat(text("throw "), p.statementTail(item.AsThrowStatement().Expression, semi))
	case compiler.LocalVariableDeclarationStatement:
		return p.localVarTail(item.AsLocalVariableDeclarationStatement(), tail)
	case compiler.FieldDeclaration:
		return p.fieldDeclarationTail(item.AsFieldDeclaration(), tail)
	}
	return concat(p.node(item), tail)
}

func (p *printer) trailingCommentAfter(node *compiler.Node) (comment, bool) {
	if p.ci >= len(p.comments) {
		return comment{}, false
	}
	c := p.comments[p.ci]
	if c.ownLine || c.pos < node.End {
		return comment{}, false
	}
	if !p.trailsDirectly(node.End, c.pos) {
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
		// A package-info.java may carry annotations; they precede the `package`
		// keyword, one per line (gjf's visitPackage). Dropping them would delete
		// code, not just reformat it.
		pd := sf.PackageDeclaration.AsPackageDeclaration()
		var pkg []Doc
		for _, a := range nodes(pd.Annotations) {
			pkg = append(pkg, p.annotation(a.AsAnnotation()))
			if tc, ok := p.trailingCommentAfter(a); ok {
				pkg = append(pkg, text(" "), reflow(tc.text))
			}
			pkg = append(pkg, hardline)
		}
		pkg = append(pkg, text("package "), text(p.entityName(pd.Name)), text(";"))
		blocks = append(blocks, concat(pkg...))
	}
	var statics, nonStatics []*compiler.Node
	for _, imp := range nodes(sf.Imports) {
		if imp.AsImportDeclaration().IsStatic {
			statics = append(statics, imp)
		} else {
			nonStatics = append(nonStatics, imp)
		}
	}
	// A comment between the package declaration and the first import belongs to
	// the imports and stays in front of them; the sorting moves the imports around
	// it. Without this it stayed pending and surfaced after the whole import
	// block, as a leading comment of the first type declaration.
	var importLead Doc
	if sf.Imports.Len() > 0 {
		importStart := p.start(sf.Imports.Nodes[0])
		lead := p.commentsBefore(importStart)
		if len(lead) > 0 {
			var parts []Doc
			for i, c := range lead {
				if i > 0 {
					if p.blankBeforePos(lead[i-1].end, c.pos) {
						parts = append(parts, hardline)
					}
					parts = append(parts, hardline)
				}
				parts = append(parts, reflow(c.text))
			}
			if p.blankBeforePos(lead[len(lead)-1].end, importStart) {
				parts = append(parts, hardline)
			}
			parts = append(parts, hardline)
			importLead = concat(parts...)
		}
	}
	p.importTrailingComments(sf.Imports)
	for _, g := range [][]*compiler.Node{statics, nonStatics} {
		if len(g) > 0 {
			if importLead != nil {
				blocks = append(blocks, concat(importLead, p.importGroup(g)))
				importLead = nil
			} else {
				blocks = append(blocks, p.importGroup(g))
			}
		}
	}
	if sf.ModuleDeclaration != nil {
		blocks = append(blocks, p.moduleDeclaration(sf.ModuleDeclaration))
	}
	if sf.Statements.Len() > 0 {
		blocks = append(blocks, concat(p.listDocs(nodes(sf.Statements), true, len(p.text))...))
	}
	if len(header) > 0 {
		// Preserve a source blank line between consecutive leading comments (e.g. a
		// license header and the package/type javadoc in a package-info file).
		var headerParts []Doc
		for i, c := range header {
			if i > 0 {
				if p.blankBeforePos(header[i-1].end, c.pos) {
					headerParts = append(headerParts, concat(hardline, hardline))
				} else {
					headerParts = append(headerParts, hardline)
				}
			}
			headerParts = append(headerParts, reflow(c.text))
		}
		headerDoc := concat(headerParts...)
		// A leading comment glued to the first construct (no blank line in source)
		// is its doc comment - keep it attached. One followed by a blank line is a
		// file header (e.g. a license), separated like other blocks.
		last := header[len(header)-1]
		glued := len(blocks) > 0 &&
			(!p.blankBeforePos(last.end, firstStart) || isJavadocComment(&last))
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
func (p *printer) moduleDeclaration(node *compiler.Node) Doc {
	m := node.AsModuleDeclaration()
	var head []Doc
	for _, a := range nodes(m.Annotations) {
		head = append(head, p.annotation(a.AsAnnotation()))
		if tc, ok := p.trailingCommentAfter(a); ok {
			head = append(head, text(" "), reflowNoWrap(tc.text))
		}
		head = append(head, hardline)
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
		// A comment before a directive stays with it, own-line and reflowed; one
		// after it on the same line stays on that line.
		lead := p.commentsBefore(p.start(d))
		// gjf (visitModule) wants a blank line exactly where the directive kind
		// changes, and none between directives of the same kind - plus any blank
		// the author left, which gjf preserves around comments.
		if i > 0 {
			firstPos := p.start(d)
			if len(lead) > 0 {
				firstPos = lead[0].pos
			}
			if d.Kind != dirs[i-1].Kind || p.blankBeforePos(dirs[i-1].End, firstPos) {
				body = append(body, concat(hardline, hardline))
			} else {
				body = append(body, hardline)
			}
		}
		for ci, c := range lead {
			body = append(body, reflow(c.text), hardline)
			next := p.start(d)
			if ci+1 < len(lead) {
				next = lead[ci+1].pos
			}
			if p.blankBeforePos(c.end, next) {
				body = append(body, hardline)
			}
		}
		doc := p.directive(d)
		if tc, ok := p.trailingCommentAfter(d); ok {
			doc = concat(doc, text(" "), reflowNoWrap(tc.text))
		}
		body = append(body, doc)
	}
	// Comments between the last directive and the closing brace.
	for _, c := range p.commentsBefore(node.End - 1) {
		body = append(body, hardline, reflow(c.text))
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
	// Sort keys are precomputed: entityName rebuilds the dotted name from the
	// source text, so keying per comparison would be O(n log n) rebuilds.
	type keyed struct {
		key string
		imp *compiler.Node
	}
	entries := make([]keyed, len(imports))
	for i, imp := range imports {
		entries[i] = keyed{p.entityName(imp.AsImportDeclaration().Name), imp}
	}
	slices.SortStableFunc(entries, func(a, b keyed) int { return cmp.Compare(a.key, b.key) })
	sorted := make([]*compiler.Node, len(entries))
	for i, e := range entries {
		sorted[i] = e.imp
	}
	seen := map[string]int{}
	var lines []Doc
	for _, imp := range sorted {
		t := p.importLine(imp.AsImportDeclaration())
		// A trailing comment stays on its import's line (`import x; // NOPMD`). It
		// was consumed in source order up front (importTrailingComments), since
		// this list is sorted and the comment cursor only moves forward.
		comment, hasComment := p.importTrailing[imp]
		if at, dup := seen[t]; dup {
			// Identical import: drop the duplicate line but keep its comment, which
			// has already been consumed and would otherwise be lost.
			if hasComment {
				lines[at] = concat(lines[at], text(" "), reflowNoWrap(comment))
			}
			continue
		}
		seen[t] = len(lines)
		if hasComment {
			lines = append(lines, concat(text(t), text(" "), reflowNoWrap(comment)))
		} else {
			lines = append(lines, text(t))
		}
	}
	return join(hardline, lines)
}

// importTrailingComments consumes every import's same-line trailing comment, in
// SOURCE order - the comment cursor only moves forward, but importGroup renders
// the imports sorted, so they cannot be picked up there.
func (p *printer) importTrailingComments(imports *compiler.NodeArray) {
	for _, imp := range nodes(imports) {
		if c, ok := p.trailingCommentAfter(imp); ok {
			p.importTrailing[imp] = c.text
		}
	}
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
	// gjf keeps the source order of annotations and modifiers (its
	// AnnotationOrModifier list is sorted by position); only the modifier keywords
	// are reordered among themselves, by ModifierOrderer, into their own slots. So
	// `final @TempDir Path` keeps the `final` first. The leading run of
	// annotations - those before the first keyword - is what gets the annotation
	// break.
	declMods := all[:cut]
	firstKeyword := 0
	for firstKeyword < len(declMods) && declMods[firstKeyword].Kind == compiler.Annotation {
		firstKeyword++
	}
	annotations := declMods[:firstKeyword]
	rest := declMods[firstKeyword:]
	var sortedKeywords []*compiler.Node
	for _, m := range rest {
		if m.Kind != compiler.Annotation {
			sortedKeywords = append(sortedKeywords, m)
		}
	}
	slices.SortStableFunc(sortedKeywords, func(a, b *compiler.Node) int {
		return cmp.Compare(rank(a.Kind), rank(b.Kind))
	})
	tailMods := make([]*compiler.Node, len(rest))
	nextKeyword := 0
	for i, m := range rest {
		if m.Kind == compiler.Annotation {
			tailMods[i] = m
			continue
		}
		tailMods[i] = sortedKeywords[nextKeyword]
		nextKeyword++
	}
	var parts []Doc
	for _, a := range annotations {
		ad := a.AsAnnotation()
		ownLine := annoMode == "own" || (annoMode == "var" && ad.Args != nil && ad.Args.Len() > 0)
		parts = append(parts, p.annotation(ad))
		// A comment on the same line as an own-line annotation stays with it
		// (`@SuppressWarnings("x") // why`) instead of floating away.
		if ownLine {
			if tc, ok := p.trailingCommentAfter(a); ok {
				parts = append(parts, text(" "), reflowNoWrap(tc.text))
			}
			parts = append(parts, hardline)
			// An own-line comment between this annotation and whatever follows it
			// stays where it is (`@Test` / `// why` / `public void m()`); without
			// this it leaks past the declaration and lands inside the body.
			// SkipTrivia is exactly the bound: the next real token.
			bound := compiler.SkipTrivia(p.text, a.End)
			after := p.commentsBefore(bound)
			from := a.End
			for ci, c := range after {
				// A blank line the author left before the comment survives too.
				if p.blankBeforePos(from, c.pos) {
					parts = append(parts, hardline)
				}
				from = c.end
				parts = append(parts, reflow(c.text), hardline)
				// gjf's allowBlankAfterLastComment: a blank the author left after the
				// comment survives.
				next := bound
				if ci+1 < len(after) {
					next = after[ci+1].pos
				}
				if p.blankBeforePos(c.end, next) {
					parts = append(parts, hardline)
				}
			}
		} else if annoMode == "var" {
			// gjf visitModifiers separates a horizontal-direction annotation from
			// what follows with a real break, not a space: a marker annotation on a
			// field or local moves to its own line when the declaration overflows.
			parts = append(parts, brk(fillUnified, " ", ZERO, nil))
		} else {
			parts = append(parts, text(" "))
		}
	}
	for _, m := range tailMods {
		if m.Kind == compiler.Annotation {
			parts = append(parts, concat(p.annotation(m.AsAnnotation()), text(" ")))
			continue
		}
		parts = append(parts, concat(text(p.modifierText(m)), text(" ")))
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
	// gjf's visitSingleMemberAnnotation: a lone unnamed array value hugs the
	// parenthesis (`@CsvSource({`) - no break after `(`, no continuation indent,
	// the initializer's own braces do the breaking.
	if a.Args.Len() == 1 {
		only := nodes(a.Args)[0].AsAnnotationArgument()
		if only.Name == nil && only.Value.Kind == compiler.ArrayInitializer {
			return concat(text(name), text("("), p.node(only.Value), text(")"))
		}
	}
	args := make([]Doc, a.Args.Len())
	for i, arg := range nodes(a.Args) {
		aa := arg.AsAnnotationArgument()
		value := p.node(aa.Value)
		switch {
		case aa.Name == nil:
			args[i] = value
		case aa.Value.Kind == compiler.ArrayInitializer:
			// An array value hugs the `=` (its braces do the breaking).
			args[i] = concat(text(p.raw(aa.Name)), text(" = "), value)
		default:
			// gjf visitAnnotationArgument: `name = <value>` is a +4 level with a
			// break after the `=`, so a long value folds onto its own line.
			args[i] = level(plus4, []Doc{text(p.raw(aa.Name)), text(" ="), brk(fillUnified, " ", ZERO, nil), value})
		}
	}
	// gjf forces an annotation's element-value pairs one-per-line when there is
	// more than one and any pair is array-valued (`name = {..}`), even if they
	// would fit; otherwise they fill (one per line only on overflow).
	hasArrayValue := false
	if a.Args.Len() > 1 {
		for _, arg := range nodes(a.Args) {
			if arg.AsAnnotationArgument().Value.Kind == compiler.ArrayInitializer {
				hasArrayValue = true
				break
			}
		}
	}
	// Annotation arguments wrap like a call's: break after `(` at +4 and lay one
	// element-value pair per line (fill only when every arg is short).
	fill := fillUnified
	switch {
	case hasArrayValue:
		fill = fillForced
	case p.allShortItems(nodes(a.Args)):
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
	return p.typeParametersBreak(tps, nil)
}

// classTypeParamIndent is +4 when a header clause follows the type parameters
// (nesting inside the header's own +4 level lands them at +8), else the header
// indent itself.
func classTypeParamIndent(hasClause bool) *Indent {
	i := ZERO
	if hasClause {
		i = plus4
	}
	return &i
}

// typeParametersBreak renders `<T, U extends V>`. breakIndent mirrors gjf's
// typeParametersRest: a class header wraps its list onto a continuation line (a
// break right after the `<`, the whole list in a level at that indent) when it
// does not fit; elsewhere the list is unbreakable (nil).
func (p *printer) typeParametersBreak(tps *compiler.NodeArray, breakIndent *Indent) Doc {
	if tps.Len() == 0 {
		return text("")
	}
	params := make([]Doc, tps.Len())
	for i, tpn := range nodes(tps) {
		tp := tpn.AsTypeParameter()
		// <@A T extends ...>: the type parameter's own annotations (JSR-308).
		name := concat(p.annotations(tp.Annotations), text(p.raw(tp.Name)))
		if tp.Constraint.Len() == 0 {
			params[i] = name
			continue
		}
		bounds := make([]Doc, tp.Constraint.Len())
		for j, b := range nodes(tp.Constraint) {
			bounds[j] = p.typ(b)
		}
		params[i] = concat(name, text(" extends "), join(text(" & "), bounds))
	}
	if breakIndent == nil {
		return concat(text("<"), join(text(", "), params), text(">"))
	}
	var inner []Doc
	for i, pd := range params {
		if i > 0 {
			inner = append(inner, text(","), brk(fillUnified, " ", ZERO, nil))
		}
		inner = append(inner, pd)
	}
	inner = append(inner, text(">"))
	return concat(text("<"), level(*breakIndent, []Doc{brk(fillUnified, "", ZERO, nil), level(ZERO, inner)}))
}

func (p *printer) typeArguments(args *compiler.NodeArray) Doc {
	if args == nil {
		return text("")
	}
	if args.Len() == 0 {
		return text("<>") // diamond
	}
	// gjf visitParameterizedType: a type-argument list breaks right after the `<`
	// and continues at +4, with the arguments in their own level (so the `>`,
	// which gjf appends to that level, counts in its fit check).
	var inner []Doc
	for i, t := range nodes(args) {
		if i > 0 {
			inner = append(inner, text(","), brk(fillUnified, " ", ZERO, nil))
		}
		inner = append(inner, p.typ(t))
	}
	inner = append(inner, text(">"))
	return concat(text("<"), level(plus4, []Doc{brk(fillUnified, "", ZERO, nil), level(ZERO, inner)}))
}

func (p *printer) typ(t *compiler.Node) Doc {
	// Annotations on an array dimension or a qualified segment, and type
	// arguments on a non-final segment, are not in the tree (see
	// TypeReferenceData.Verbatim) - print those types from source.
	if (t.Kind == compiler.TypeReference && t.AsTypeReference().Verbatim) ||
		(t.Kind == compiler.ArrayType && t.AsArrayType().Verbatim) ||
		(t.Kind == compiler.WildcardType && t.AsWildcardType().Verbatim) {
		return text(p.raw(t))
	}
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
	empty := p.bodyIsEmpty(members, end)
	bodyToken := " {"
	if empty {
		bodyToken = " {}"
	}
	header := concat(
		p.modifiers(mods, "own"),
		text(keyword),
		text(" "),
		text(p.raw(name)),
		// gjf opens ONE +4 level around the type parameters AND the
		// extends/implements/permits clauses (visitClassDeclaration), so a
		// type-parameter list that had to break forces the clause break too. The
		// type parameters take a further +4 when a clause follows (landing at +8),
		// else the header's own indent. The body's `{` rides inside that level too
		// (gjf's DocBuilder appends it to the innermost level that last took a
		// break), so a header that only overflows on the brace breaks its clause.
		level(plus4, append(append([]Doc{p.typeParametersBreak(typeParams, classTypeParamIndent(len(tail) > 0))}, tail...), text(bodyToken))),
	)
	if empty {
		return header
	}
	return concat(header, p.bodyRest(members, end))
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
	if p.bodyIsEmpty(members, endPos) {
		return text("{}")
	}
	return concat(text("{"), p.bodyRest(members, endPos))
}

func (p *printer) bodyIsEmpty(members *compiler.NodeArray, endPos int) bool {
	return members.Len() == 0 && !p.hasCommentBefore(endPos)
}

// bodyRest is a body minus its opening `{` (which rides into the header).
func (p *printer) bodyRest(members *compiler.NodeArray, endPos int) Doc {
	lead := hardline
	from := -1
	if members.Len() > 0 {
		lead = p.braceLead(members.Nodes[0].Pos, p.start(members.Nodes[0]))
		from = members.Nodes[members.Len()-1].End
	} else if p.ci < len(p.comments) {
		from = p.comments[p.ci].pos
	}
	closeLead := hardline
	if from >= 0 && p.blankAfterLastComment(from, endPos) {
		closeLead = concat(hardline, hardline)
	}
	return concat(
		indent(concat(append([]Doc{lead}, p.members(members, endPos)...)...)),
		closeLead,
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
	// The last constant's trailing comment is held back: its separator (`,`/`;`)
	// is only known below, and a separator printed after the comment would be
	// commented out - "A(1) // note," does not compile.
	lastTrailing := ""
	for i, c := range consts {
		leadComments := p.commentsBefore(p.start(c))
		firstPos := p.start(c)
		if len(leadComments) > 0 {
			firstPos = leadComments[0].pos
		}
		if i > 0 {
			// gjf preserves one source blank line between enum constants.
			if p.blankBeforePos(prevConstEnd, firstPos) {
				constantParts = append(constantParts, hardline, hardline)
			} else {
				constantParts = append(constantParts, hardline)
			}
		}
		// Blank lines between the leading comments, and between the last one and the
		// constant, are preserved like a member list's - except after a javadoc,
		// which glues to the declaration it documents.
		for ci, cm := range leadComments {
			if ci > 0 && p.blankBeforePos(leadComments[ci-1].end, cm.pos) {
				constantParts = append(constantParts, hardline)
			}
			constantParts = append(constantParts, reflow(cm.text), hardline)
		}
		if n := len(leadComments); n > 0 {
			last := leadComments[n-1]
			if !isJavadocComment(&last) && p.blankBeforePos(last.end, p.start(c)) {
				constantParts = append(constantParts, hardline)
			}
		}
		isLast := i == len(consts)-1
		cdoc := p.enumConstant(c.AsEnumConstantDeclaration())
		if !isLast {
			cdoc = concat(cdoc, text(","))
		}
		if trailing, ok := p.trailingCommentAfter(c); ok {
			if isLast {
				lastTrailing = trailing.text
			} else {
				cdoc = concat(cdoc, text(" "), reflowNoWrap(trailing.text))
			}
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
	lastComment := text("")
	if lastTrailing != "" {
		lastComment = concat(text(" "), text(lastTrailing))
	}
	switch {
	case d.Members.Len() > 0:
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
		bodyParts = append(bodyParts, text(";"), lastComment, hardline)
		if len(consts) > 0 && realMember {
			bodyParts = append(bodyParts, hardline)
		}
		bodyParts = append(bodyParts, p.members(d.Members, end)...)
	case semicolonAfter:
		bodyParts = append(bodyParts, text(";"), lastComment)
	default:
		bodyParts = append(bodyParts, lastComment)
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
	return p.fieldDeclarationTail(d, nil)
}

// fieldDeclarationTail is fieldDeclaration with a trailing comment routed in
// after the `;`.
func (p *printer) fieldDeclarationTail(d *compiler.FieldDeclarationData, tail Doc) Doc {
	// The whole declaration is one level so the modifiers' annotation break can
	// take when it overflows (gjf visitModifiers).
	if d.Declarators.Len() == 1 {
		return level(ZERO, append(
			append([]Doc{p.modifiers(d.Modifiers, "var")}, p.inlineBlockComments(p.start(d.Type))...),
			p.singleDeclaration(p.typ(d.Type), nodes(d.Declarators)[0].AsVariableDeclarator(), semiWithTail(tail)),
		))
	}
	return level(ZERO, append(
		append([]Doc{p.modifiers(d.Modifiers, "var")}, p.inlineBlockComments(p.start(d.Type))...),
		p.typ(d.Type),
		text(" "),
		p.declaratorList(d.Declarators),
		semiWithTail(tail),
	))
}

// singleDeclaration renders `<type> <name> = <init><trailing>` with the gjf
// break-before-name option: when `<type> <name> =` does not fit, break after the
// type (name onto a +4 line) so `<name> = <init>` can stay together; the
// initializer keeps its own break-after-`=`, indented +4 (or +8 when the name
// also broke). The break-before-name's fit check excludes the initializer (a
// sibling level), so a long initializer alone does not trigger it.
func (p *printer) singleDeclaration(typ Doc, v *compiler.VariableDeclaratorData, trailing Doc) Doc {
	name := concat(append(p.inlineBlockComments(p.start(v.Name)), text(p.raw(v.Name)), text(strings.Repeat("[]", v.ArrayRankAfterName)))...)
	// No initializer: the same declareOne break, with nothing after the name - so
	// the `;` and any trailing comment ride inside the level and count in its fit
	// check (`Map<String, Map<Integer, Integer>> index; // note`).
	if v.Initializer == nil {
		inner := []Doc{typ, brk(fillUnified, " ", ZERO, nil), name}
		if trailing != nil {
			inner = append(inner, trailing)
		}
		return level(plus4, inner)
	}
	// An array/hugging initializer owns its own braces: keep the simple shape
	// (the declarator handles the `=`).
	if v.Initializer.Kind == compiler.ArrayInitializer {
		return concat(typ, text(" "), p.declarator(v, trailing))
	}
	// A `//` comment right after the `=` stays on its line and forces the break
	// (gjf hangs it off the `=` token), instead of drifting into the initializer.
	nameTag := &BreakTag{}
	sig := []Doc{typ, brk(fillUnified, " ", ZERO, nameTag), name, text(" =")}
	firstBreak := line
	if eq, ok := p.lineCommentAfterAssign(v.Name.End, p.start(v.Initializer)); ok {
		sig = append(sig, text(" "), reflowNoWrap(eq.text))
		firstBreak = hardline
	}
	return level(ZERO, []Doc{
		level(plus4, sig),
		level(indentIf(nameTag, indentConst(8), plus4), []Doc{firstBreak, p.statementTail(v.Initializer, trailing)}),
	})
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
	// A `//` comment right after the `=` stays on its line and forces the break
	// (gjf hangs it off the `=` token); without this it drifts into the
	// initializer and comes out inside its argument list.
	head := []Doc{name, text(" =")}
	firstBreak := line
	if eq, ok := p.lineCommentAfterAssign(v.Name.End, p.start(v.Initializer)); ok {
		head = append(head, text(" "), reflowNoWrap(eq.text))
		firstBreak = hardline
	}
	head = append(head, level(plus4, []Doc{firstBreak, p.statementTail(v.Initializer, trailing)}))
	return concat(head...)
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
		// The comment must immediately trail this item - stop at a separator or a
		// closing delimiter, so a comment past `)`/`,`/`:` is not mis-attached.
		if !t.line && !t.ownLine && t.pos >= endPos && !strings.ContainsAny(p.text[endPos:t.pos], "\n,):") {
			p.ci++
			parts = append(parts, text(" "), reflowNoWrap(t.text))
			return parts, true
		}
	}
	return parts, false
}

// listItem is a rendered delimited-list item plus how its comments attach:
// leadTrailing is a line comment that trails the PREVIOUS item on its line, and
// leadStart is where this item's rendered content begins (for blank-line detection).
type listItem struct {
	doc          Doc
	comment      bool
	leadTrailing string // "" when none
	leadStart    int
}

func (p *printer) itemWithCommentsEx(node *compiler.Node, render func() Doc, extractLeadTrailing bool) listItem {
	var parts []Doc
	comment := false
	leadTrailing := ""
	// Where this item's rendered content begins (its first comment, else the
	// item) - used by callers to measure a source blank line before it.
	leadStart := p.start(node)
	if p.ci < len(p.comments) && p.comments[p.ci].pos < p.start(node) {
		leadStart = p.comments[p.ci].pos
	}
	lead := p.commentsBefore(p.start(node))
	for i, c := range lead {
		comment = true
		switch {
		case extractLeadTrailing && i == 0 && c.line && !c.ownLine:
			// A line comment on the previous element's line trails THAT element.
			leadTrailing = c.text
		case c.ownLine || c.line:
			parts = append(parts, reflow(c.text), hardline)
			// gjf's allowBlankAfterLastComment: a blank line the author left after a
			// comment survives, before the next comment or the item itself.
			next := p.start(node)
			if i+1 < len(lead) {
				next = lead[i+1].pos
			}
			if p.blankBeforePos(c.end, next) {
				parts = append(parts, hardline)
			}
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
	doc := concat(parts...)
	if len(parts) == 1 {
		doc = parts[0]
	}
	return listItem{doc: doc, comment: comment, leadTrailing: leadTrailing, leadStart: leadStart}
}

// listItems builds a delimited list's items with their comments consumed,
// reporting whether any item carried a comment.
func (p *printer) listItems(ns []*compiler.Node, render func(*compiler.Node) Doc) ([]Doc, bool) {
	rs := p.listItemsEx(ns, render, false)
	items := make([]Doc, len(rs))
	anyComment := false
	for i, r := range rs {
		items[i] = r.doc
		if r.comment {
			anyComment = true
		}
	}
	return items, anyComment
}

func (p *printer) listItemsEx(ns []*compiler.Node, render func(*compiler.Node) Doc, extractLeadTrailing bool) []listItem {
	rs := make([]listItem, len(ns))
	for i, n := range ns {
		n := n
		rs[i] = p.itemWithCommentsEx(n, func() Doc { return render(n) }, extractLeadTrailing)
	}
	return rs
}

// parametersOpen renders the parameter list with the opening delimiter as a
// parameter: the method-name break owns the `(` when a return type precedes it.
func (p *printer) parametersOpen(params *compiler.NodeArray, trailing Doc, open string) Doc {
	if params.Len() == 0 {
		closeOnly := "()"
		if open == "" {
			closeOnly = ")"
		}
		// Even with no parameters the trailing run (a `throws` clause + brace) may
		// carry a break, so it must sit in a +4 level to fold and indent correctly.
		inner := []Doc{text(closeOnly)}
		if trailing != nil {
			inner = append(inner, trailing)
		}
		return level(plus4, inner)
	}
	// Parameters are never filled (gjf uses a UNIFIED inter-parameter break).
	items, _ := p.listItems(nodes(params), func(n *compiler.Node) Doc { return p.parameterBreak(n, true) })
	return p.argsLikeTrailing(open, items, ")", fillUnified, trailing)
}

// paramListChildren returns `(`, break-before-args, the param list and `)` as
// flat children, for the throws case where they must share the signature level
// with the throws break (gjf's open(ZERO) around visitFormals + visitThrowsClause).
// The break before the args is INDEPENDENT so the params stay inline when they
// fit and break to the +4 line only when they overflow.
func (p *printer) paramListChildrenOpen(params *compiler.NodeArray, open string) []Doc {
	if params.Len() == 0 {
		if open == "" {
			return []Doc{text(")")}
		}
		return []Doc{text("()")}
	}
	items, _ := p.listItems(nodes(params), func(n *compiler.Node) Doc { return p.parameterBreak(n, true) })
	innerParts := make([]Doc, 0, len(items)*2)
	for i, it := range items {
		if i > 0 {
			innerParts = append(innerParts, text(","), brk(fillUnified, " ", ZERO, nil))
		}
		innerParts = append(innerParts, it)
	}
	// The `)` sits INSIDE the parameter level: gjf's DocBuilder appends it to the
	// innermost level that last took a break (its appendLevel), so the list's own
	// fit check counts it and a run that only overflows on the `)` breaks one
	// parameter per line.
	innerParts = append(innerParts, text(")"))
	return []Doc{text(open), brk(fillIndependent, "", ZERO, nil), level(ZERO, innerParts)}
}

func (p *printer) parameter(n *compiler.Node) Doc {
	return p.parameterBreak(n, false)
}

// parameterBreak is parameter with gjf's declareOne break between the type and
// the name (an INDEPENDENT break, the name indented +4) for the cases that have
// it: a method or constructor parameter list. The break is a direct child of
// the enclosing parameter-list level, so its fit check counts the rest of the
// line - the `)` and the body's `{`. A lambda parameter does NOT get it.
func (p *printer) parameterBreak(n *compiler.Node, breakBeforeName bool) Doc {
	pp := n.AsParameter()
	// A receiver parameter (`@A Foo this`, `Outer.this`) has no name node and its
	// qualifier is not kept in the tree, so print it from source.
	if pp.IsReceiver {
		return text(p.raw(n))
	}
	parts := []Doc{p.modifiers(pp.Modifiers, "inline"), p.typ(pp.Type)}
	if pp.IsVarArgs {
		parts = append(parts, text("..."))
	}
	if pp.Name != nil {
		if breakBeforeName {
			parts = append(parts, brk(fillIndependent, " ", plus4, nil))
		} else {
			parts = append(parts, text(" "))
		}
		parts = append(parts, text(p.raw(pp.Name)))
		// C-style trailing brackets belong to the parameter's type (`byte b[]`);
		// dropping them changed the signature.
		if pp.ArrayRankAfterName > 0 {
			parts = append(parts, text(strings.Repeat("[]", pp.ArrayRankAfterName)))
		}
	}
	return concat(parts...)
}

// methodLike renders a method or constructor. returnType may be nil.
func (p *printer) methodLike(mods, typeParams *compiler.NodeArray, returnType, name *compiler.Node, params, throws *compiler.NodeArray, body *compiler.Node) Doc {
	return p.methodLikeDefault(mods, typeParams, returnType, name, params, throws, body, nil)
}

// methodLikeDefault is methodLike with an annotation element's `default <value>`,
// which precedes the `;`.
func (p *printer) methodLikeDefault(mods, typeParams *compiler.NodeArray, returnType, name *compiler.Node, params, throws *compiler.NodeArray, body, defaultValue *compiler.Node) Doc {
	tp := p.typeParameters(typeParams)
	head := []Doc{p.modifiers(mods, "own")}
	if !isEmpty(tp) {
		head = append(head, tp, text(" "))
	}
	if returnType != nil {
		head = append(head, p.typ(returnType))
	}
	hasThrows := throws.Len() > 0
	// With no throws clause the body-open token rides inside the param level
	// (rest-of-line rule). With throws, see the hasThrows branch below.
	emptyBody := body != nil && p.blockIsEmpty(body.AsBlock(), p.start(body), body.End)
	// A comment on the body's `{` line rides with the brace (gjf's toksAfter), so
	// its width counts in the signature's fit check and the parameters wrap
	// instead of the comment. See braceTrailAhead for the emitted-ahead marking.
	defaultPart := text("")
	if defaultValue != nil {
		defaultPart = concat(text(" default "), p.node(defaultValue))
	}
	var bodyToken Doc
	switch {
	case body == nil:
		bodyToken = concat(defaultPart, text(";"))
	case emptyBody:
		bodyToken = text(" {}")
	default:
		bodyToken = concat(text(" {"), p.braceTrailAhead(p.start(body)))
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
		open := "("
		if returnType != nil {
			open = ""
		}
		sig = level(plus4, append(
			p.paramListChildrenOpen(params, open),
			brk(fillIndependent, " ", ZERO, nil),
			level(throwsIndent, throwsParts),
			bodyToken,
		))
	} else {
		open := "("
		if returnType != nil {
			open = ""
		}
		sig = p.parametersOpen(params, bodyToken, open)
	}
	// gjf breaks between the return type and the name when `name(` no longer
	// fits, indenting the name +4 (visitMethodDeclaration's breakBeforeType). The
	// break sits in a level holding just the name and the opening paren, so it
	// fires on exactly that run - breaking the PARAMETER LIST is preferred
	// whenever that is enough, which is what gjf does.
	if returnType != nil {
		nameRun := []Doc{brk(fillIndependent, " ", plus4, nil), text(p.raw(name))}
		if params.Len() == 0 && !hasThrows {
			// With no parameters there is no break-before-args to end the split, so
			// the whole `name() {` run has to sit in the level for the fit check.
			head = append(head, level(ZERO, append(nameRun, text("()"), bodyToken)))
		} else {
			head = append(head, level(ZERO, append(nameRun, text("("))), sig)
		}
	} else {
		head = append(head, text(p.raw(name)), sig)
	}
	// Emit the rest of the block when there is a real body, else the signature
	// (with its trailing `;`/` {}`) is complete.
	if body == nil || emptyBody {
		return concat(head...)
	}
	return concat(append(head, p.blockRest(body.AsBlock(), body.End, false))...)
}

func (p *printer) initializerBlock(d *compiler.InitializerBlockData) Doc {
	static := ""
	if d.IsStatic {
		static = "static "
	}
	return concat(text(static), p.blockOpen(d.Body.AsBlock(), p.start(d.Body), d.Body.End, false))
}

// --- statements ----------------------------------------------------------

func (p *printer) blockIsEmpty(b *compiler.BlockData, startPos, endPos int) bool {
	if b.Statements.Len() > 0 {
		return false
	}
	// Only a comment *inside* the block (after its `{`) makes it non-empty. The
	// test must be positional, not cursor-based: callers ask before the
	// surrounding construct has rendered (clauseClose runs before the condition),
	// so comments[ci] can still point at a comment that lies BEFORE the block -
	// which made a comment-only block look empty and emitted `{}` plus the
	// comments plus a second `}`.
	for _, c := range p.comments {
		if c.pos >= endPos {
			break
		}
		if c.pos > startPos {
			return false
		}
	}
	return true
}

func (p *printer) block(b *compiler.BlockData, endPos int) Doc {
	return p.blockTB(b, endPos, false)
}

// blockOpen is block with gjf's CollapseEmptyOrNot: an empty body is written
// `{}` for a method, if/while/for/do and a try body, but stays open (`{`
// newline `}`) for else, catch, finally, synchronized, an initializer and a
// switch.
func (p *printer) blockOpen(b *compiler.BlockData, startPos, endPos int, collapse bool) Doc {
	if !collapse && p.blockIsEmpty(b, startPos, endPos) {
		return concat(text("{"), hardline, text("}"))
	}
	return p.blockTB(b, endPos, false)
}

// blockTB is block with allowTrailingBlank: gjf preserves a source blank line
// before the closing `}` only when a clause follows (`} else`/`catch`/`finally`).
func (p *printer) blockTB(b *compiler.BlockData, endPos int, allowTrailingBlank bool) Doc {
	// Here the comment cursor is already positioned past anything preceding the
	// block, so a pending comment before endPos is genuinely inside it.
	if b.Statements.Len() == 0 && !p.hasCommentBefore(endPos) {
		return text("{}")
	}
	return concat(text("{"), p.blockRest(b, endPos, allowTrailingBlank))
}

// blockRest is a block's body after the opening `{` (the `{` is emitted by the
// caller, so it can be placed inside another level to count toward a wrap
// decision).
func (p *printer) blockRest(b *compiler.BlockData, endPos int, allowTrailingBlank bool) Doc {
	// A comment already emitted with the opening brace (braceTrailAhead) must not
	// be rendered again here - braceTrailingComment only runs when the block has
	// statements, and a comment-only block would emit it twice.
	for p.emittedAhead[p.ci] {
		p.ci++
	}
	// A comment on the same source line as the opening `{` stays on that line
	// (gjf): `if (...) { // note`. Emit it before the indented body so it rides
	// the brace line, and consume it here so listDocs does not re-emit it own-line.
	var braceComment = text("")
	lead := hardline
	if b.Statements.Len() > 0 {
		braceComment = p.braceTrailingComment(b.Statements.Nodes[0].Pos)
		lead = p.braceLead(b.Statements.Nodes[0].Pos, p.start(b.Statements.Nodes[0]))
	}
	// The blank must be measured from the last thing actually rendered - a
	// dangling comment after the last statement, when there is one. Measuring
	// from the statement counts the comment's own line as the blank.
	closeLead := hardline
	from := -1
	if b.Statements.Len() > 0 {
		from = b.Statements.Nodes[b.Statements.Len()-1].End
	} else if p.ci < len(p.comments) {
		from = p.comments[p.ci].pos
	}
	if from >= 0 {
		lastEnd := p.lastCommentEndBefore(from, endPos)
		if (allowTrailingBlank && p.blankBeforePos(lastEnd, endPos-1)) ||
			p.blankAfterLastComment(from, endPos) {
			closeLead = concat(hardline, hardline)
		}
	}
	return concat(
		braceComment,
		indent(concat(append([]Doc{lead}, p.listDocs(nodes(b.Statements), false, endPos)...)...)),
		closeLead,
		text("}"),
	)
}

// braceTrailingComment consumes and returns a comment that trails the opening
// `{` on its line (` // note`), or "" when the next pending comment starts on a
// later line. afterBrace is the offset just past the `{`.
func (p *printer) braceTrailingComment(afterBrace int) Doc {
	// Already emitted with the header's `{` (see braceTrailAhead).
	if p.emittedAhead[p.ci] {
		p.ci++
		return text("")
	}
	if p.ci >= len(p.comments) {
		return text("")
	}
	c := p.comments[p.ci]
	if c.pos < afterBrace || strings.Contains(p.text[afterBrace:c.pos], "\n") {
		return text("")
	}
	p.ci++
	return concat(text(" "), reflowNoWrap(c.text))
}

func (p *printer) localVar(d *compiler.LocalVariableDeclarationStatementData) Doc {
	return p.localVarTail(d, nil)
}

// localVarTail is localVar with a trailing comment routed in after the `;`.
func (p *printer) localVarTail(d *compiler.LocalVariableDeclarationStatementData, tail Doc) Doc {
	semi := semiWithTail(tail)
	ds := nodes(d.Declarators)
	if len(ds) == 1 {
		return level(ZERO, append(
			append([]Doc{p.modifiers(d.Modifiers, "var")}, p.inlineBlockComments(p.start(d.Type))...),
			p.singleDeclaration(p.typ(d.Type), ds[0].AsVariableDeclarator(), semi),
		))
	}
	parts := append(
		append([]Doc{p.modifiers(d.Modifiers, "var")}, p.inlineBlockComments(p.start(d.Type))...),
		p.typ(d.Type), text(" "))
	for i, v := range ds {
		if i > 0 {
			parts = append(parts, text(", "))
		}
		// The terminating `;` rides into the last declarator's initializer.
		var tr Doc
		if i == len(ds)-1 {
			tr = semi
		}
		parts = append(parts, p.declarator(v.AsVariableDeclarator(), tr))
	}
	return level(ZERO, parts)
}

// semiWithTail builds the `;` plus an optional trailing comment. Never concat a
// nil Doc - the engine panics on one.
func semiWithTail(tail Doc) Doc {
	if tail == nil {
		return text(";")
	}
	return concat(text(";"), tail)
}

func (p *printer) ifStatement(s *compiler.IfStatementData) Doc {
	header := group(concat(text("if ("), p.statementTail(s.Condition, p.clauseClose(s.ThenStatement))))
	parts := []Doc{
		header,
		// gjf preserves a source blank line before the then-block's `}` when an
		// `else` follows.
		p.clauseRest(s.ThenStatement, s.ElseStatement != nil),
	}
	if s.ElseStatement != nil {
		elseOnSameLine := s.ThenStatement.Kind == compiler.Block
		switch lead, ok := p.clauseKeywordLead(p.start(s.ElseStatement), elseOnSameLine); {
		case ok:
			parts = append(parts, lead, text("else"))
		case elseOnSameLine:
			parts = append(parts, text(" else"))
		default:
			parts = append(parts, concat(hardline, text("else")))
		}
		switch s.ElseStatement.Kind {
		case compiler.IfStatement:
			parts = append(parts, text(" "), p.node(s.ElseStatement))
		case compiler.Block:
			// The else-block owns its brace, so a comment on the brace's line rides
			// it (`} else { // note`) instead of starting the body.
			parts = append(parts, p.braceOpenCollapse(s.ElseStatement, false), p.clauseRestCollapse(s.ElseStatement, false, false))
		default:
			parts = append(parts, p.clauseBody(s.ElseStatement))
		}
	}
	return concat(parts...)
}

// clauseBody renders the controlled statement of if/for/while with its leading
// separator. A block follows after a space; a single statement stays on the
// same line when it fits and otherwise breaks onto an indented line.
func (p *printer) clauseBody(s *compiler.Node) Doc {
	return p.clauseBodyTB(s, false)
}

func (p *printer) clauseBodyTB(s *compiler.Node, allowTrailingBlank bool) Doc {
	if s.Kind == compiler.Block {
		return concat(text(" "), p.blockTB(s.AsBlock(), s.End, allowTrailingBlank))
	}
	return group(indent(concat(line, p.node(s))))
}

// clauseKeywordLead returns the comments between a block's `}` and the clause
// keyword that follows (`else`/`catch`/`finally`), with the separators around
// them. gjf hangs a same-line comment off the brace and puts the others on
// their own line; the keyword then always starts a new line. ok=false when
// there is no comment, so the caller keeps its usual `} else` spacing.
func (p *printer) clauseKeywordLead(bound int, afterBlock bool) (Doc, bool) {
	cs := p.commentsBefore(bound)
	if len(cs) == 0 {
		return nil, false
	}
	var parts []Doc
	for i, c := range cs {
		sameLine := i == 0 && !c.ownLine && afterBlock
		if sameLine {
			parts = append(parts, text(" "), reflowNoWrap(c.text))
			continue
		}
		parts = append(parts, hardline, reflow(c.text))
	}
	parts = append(parts, hardline)
	return concat(parts...), true
}

// clauseClose is the token that closes an if/while/for header. gjf's DocBuilder
// appends the block's opening `{` to the header's level (its appendLevel), so
// `if (cond) {` breaks the condition when the line overflows *including* the
// brace - the fit check must see it. Pure: consumes no comments, so the header
// can still be rendered before the body.
func (p *printer) clauseClose(s *compiler.Node) Doc {
	if s.Kind != compiler.Block {
		return text(")")
	}
	return concat(text(")"), p.braceOpen(s))
}

// braceOpen is a block's opening ` {` plus a comment that rides it, or ` {}`
// when empty. Callers that own the brace themselves (catch/finally/else) use
// this so the comment counts in THEIR fit check, like clauseClose does for a
// header.
func (p *printer) braceOpen(s *compiler.Node) Doc {
	return p.braceOpenCollapse(s, true)
}

func (p *printer) braceOpenCollapse(s *compiler.Node, collapse bool) Doc {
	if p.blockIsEmpty(s.AsBlock(), p.start(s), s.End) {
		if collapse {
			return text(" {}")
		}
		return text(" {")
	}
	return concat(text(" {"), p.braceTrailAhead(p.start(s)))
}

// braceTrailAhead returns a comment on the same line as a clause body's `{`
// (`if (...) { // note`). gjf hangs it off the brace, so it belongs to the
// header's routed close and its width counts in the header's fit check. The
// condition has not rendered yet, so the comment cannot be consumed in order:
// mark it emitted instead and let braceTrailingComment skip it when the body
// renders.
func (p *printer) braceTrailAhead(blockStart int) Doc {
	bracePos := blockStart + 1
	for i := p.ci; i < len(p.comments); i++ {
		c := p.comments[i]
		if c.pos < bracePos {
			continue
		}
		if strings.Contains(p.text[bracePos:c.pos], "\n") {
			return text("")
		}
		// Only a `//` comment rides the brace: gjf emits `{` with
		// breakAndIndentTrailingComment, which forces a block comment onto its own
		// indented line (`void h() { /* c */ }` -> `{`, `/* c */`, `}`).
		if !c.line {
			return text("")
		}
		p.emittedAhead[i] = true
		return concat(text(" "), reflowNoWrap(c.text))
	}
	return text("")
}

// clauseRest is the clause body after clauseClose already emitted its `{`.
func (p *printer) clauseRest(s *compiler.Node, allowTrailingBlank bool) Doc {
	return p.clauseRestCollapse(s, allowTrailingBlank, true)
}

func (p *printer) clauseRestCollapse(s *compiler.Node, allowTrailingBlank, collapse bool) Doc {
	if s.Kind != compiler.Block {
		return p.clauseBody(s)
	}
	b := s.AsBlock()
	if p.blockIsEmpty(b, p.start(s), s.End) {
		if collapse {
			return text("")
		}
		// Not collapsed: braceOpen emitted the bare `{`, so close it here. gjf
		// passes AllowLeadingBlankLine.YES for these clauses, so a blank the author
		// left inside the empty block survives.
		if p.blankBeforePos(p.start(s)+1, s.End-1) {
			return concat(hardline, hardline, text("}"))
		}
		return concat(hardline, text("}"))
	}
	return p.blockRest(b, s.End, allowTrailingBlank)
}

func (p *printer) whileStatement(s *compiler.WhileStatementData) Doc {
	header := group(concat(text("while ("), p.statementTail(s.Condition, p.clauseClose(s.Statement))))
	return concat(header, p.clauseRest(s.Statement, false))
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
	// gjf visitForLoop: the three clauses are direct children of one +4 level
	// separated by UNIFIED breaks, so they go one per line together once the
	// header (including the body's `{`) overflows.
	updPart := text(" ")
	if s.Incrementors.Len() > 0 {
		updPart = concat(line, upd)
	}
	header := level(plus4, []Doc{
		text("for ("), init, text(";"), line, cond, text(";"), updPart, p.clauseClose(s.Statement),
	})
	return concat(header, p.clauseRest(s.Statement, false))
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
		concat(text("for ("), level(plus4, []Doc{p.parameter(s.Parameter), text(" :"), line, p.node(s.Expression)}), p.clauseClose(s.Statement)),
		p.clauseRest(s.Statement, false),
	)
}

func (p *printer) tryStatement(s *compiler.TryStatementData) Doc {
	parts := []Doc{text("try")}
	// gjf preserves a source blank line before a block's `}` when another clause
	// (catch/finally) follows.
	hasFinally := s.FinallyBlock != nil
	catches := nodes(s.CatchClauses)
	tryBlank := len(catches) > 0 || hasFinally
	if s.Resources.Len() > 0 {
		// The first resource stays on the `try (` line; subsequent ones break
		// before themselves at +4 (one per line), each `;`-terminated. A trailing
		// `;` after the last resource in source is preserved as `; )`. The block's
		// `{` rides along so the resource list wraps when it pushes past column 100.
		res := nodes(s.Resources)
		last := res[len(res)-1]
		closeTok := ")"
		if idx := compiler.SkipTrivia(p.text, last.End); idx < len(p.text) && p.text[idx] == ';' {
			closeTok = "; )"
		}
		emptyTry := p.blockIsEmpty(s.TryBlock.AsBlock(), p.start(s.TryBlock), s.TryBlock.End)
		if emptyTry {
			closeTok += " {}"
		} else {
			closeTok += " {"
		}
		if len(res) == 1 {
			// A single resource stays on the `try (` line; its own initializer level
			// supplies the +4 continuation indent, so no extra resource-list level
			// (which would double-indent the broken initializer to +8).
			parts = append(parts, text(" ("), p.resourceTrailing(res[0].AsResource(), text(closeTok)))
		} else {
			// gjf's visitTry uses a FORCED break between resources, so more than one
			// always goes one per line even when they would fit together.
			var inner []Doc
			for i, r := range res {
				if i > 0 {
					inner = append(inner, hardline)
				}
				// The separator that follows a resource - its `;`, or the `) {` for the
				// last one - rides INSIDE it (gjf's DocBuilder appends it to the
				// innermost level that last took a break), so a resource that only
				// overflows on it breaks after its `=`.
				// gjf emits `token(";"); builder.space()` after a resource, and that
				// SPACE counts in the fit check even though the break drops it - so a
				// resource list breaks one column earlier than the rendered line.
				sep := text("; ")
				if i == len(res)-1 {
					sep = text(closeTok)
				}
				inner = append(inner, p.resourceTrailing(r.AsResource(), sep))
			}
			parts = append(parts, text(" ("), level(plus4, inner))
		}
		if !emptyTry {
			parts = append(parts, p.blockRest(s.TryBlock.AsBlock(), s.TryBlock.End, tryBlank))
		}
	} else {
		parts = append(parts, text(" "), p.blockTB(s.TryBlock.AsBlock(), s.TryBlock.End, tryBlank))
	}
	for i, cn := range catches {
		c := cn.AsCatchClause()
		open := text(" catch (")
		if lead, ok := p.clauseKeywordLead(p.start(cn), true); ok {
			open = concat(lead, text("catch ("))
		}
		// gjf visitUnionType: the parameter sits in a +4 level and breaks BEFORE
		// each `|`, so a long multi-catch lays one alternative per line.
		paramParts := []Doc{p.modifiers(c.Modifiers, "inline")}
		for j, t := range nodes(c.CatchTypes) {
			if j > 0 {
				paramParts = append(paramParts, brk(fillUnified, " ", ZERO, nil), text("| "))
			}
			paramParts = append(paramParts, p.typ(t))
		}
		paramParts = append(paramParts, text(" "), text(p.raw(c.Name)), text(")"))
		parts = append(parts, open, level(plus4, []Doc{level(ZERO, paramParts)}), p.braceOpenCollapse(c.Block, false), p.clauseRestCollapse(c.Block, i < len(catches)-1 || hasFinally, false))
	}
	if s.FinallyBlock != nil {
		open := text(" finally")
		if lead, ok := p.clauseKeywordLead(p.start(s.FinallyBlock), true); ok {
			open = concat(lead, text("finally"))
		}
		parts = append(parts, open, p.braceOpenCollapse(s.FinallyBlock, false), p.clauseRestCollapse(s.FinallyBlock, false, false))
	}
	return concat(parts...)
}

func (p *printer) resource(r *compiler.ResourceData) Doc {
	return p.resourceTrailing(r, nil)
}

// resourceTrailing is resource with the `) {` that closes the resource list
// routed inside the initializer's level, so the break fires on the whole rest
// of the line.
func (p *printer) resourceTrailing(r *compiler.ResourceData, trailing Doc) Doc {
	appendTrailing := func(d Doc) Doc {
		if trailing == nil {
			return d
		}
		return concat(d, trailing)
	}
	if r.Expression != nil {
		return appendTrailing(p.node(r.Expression))
	}
	// gjf declares a resource with fieldAnnotationDirection, so an annotation
	// carrying arguments goes on its own line and the declaration follows at the
	// resource's +4 continuation (a marker annotation stays inline).
	vertical := false
	for _, m := range nodes(r.Modifiers) {
		if m.Kind == compiler.Annotation {
			if a := m.AsAnnotation(); a.Args != nil && a.Args.Len() > 0 {
				vertical = true
				break
			}
		}
	}
	annoMode := "inline"
	if vertical {
		annoMode = "var"
	}
	head := []Doc{p.modifiers(r.Modifiers, annoMode)}
	if r.Type != nil {
		head = append(head, concat(p.typ(r.Type), text(" ")))
	}
	if r.Name != nil {
		head = append(head, text(p.raw(r.Name)))
	}
	if vertical {
		rest := text("")
		switch {
		case r.Initializer == nil:
			if trailing != nil {
				rest = trailing
			}
		case r.Initializer.Kind == compiler.ArrayInitializer:
			rest = appendTrailing(concat(text(" = "), p.node(r.Initializer)))
		default:
			rest = concat(text(" ="), level(plus4, []Doc{line, p.statementTail(r.Initializer, trailing)}))
		}
		return level(plus4, []Doc{concat(head...), rest})
	}
	if r.Initializer == nil {
		return appendTrailing(concat(head...))
	}
	// A `//` comment between the name and the initializer stays on the name's line
	// and forces the break, exactly like a declarator's. Without this it stayed
	// pending and the initializer's dot-chain emitted it own-line-style, glued to
	// the receiver with no space (`recycler// note`) - which then moved on the next
	// run, so the output was not idempotent.
	var eq *comment
	if r.Name != nil {
		if c, ok := p.lineCommentAfterAssign(r.Name.End, p.start(r.Initializer)); ok {
			eq = &c
		}
	}
	// Like a variable declarator, a long initializer folds onto a +4
	// continuation line after `=` (gjf), rather than breaking the RHS in place.
	if r.Initializer.Kind == compiler.ArrayInitializer {
		return appendTrailing(concat(concat(head...), text(" = "), p.node(r.Initializer)))
	}
	// The comment stays on whichever side of the `=` the source put it: gjf hangs
	// it off the token it follows, so `name //` keeps the `=` for the next line
	// while `name = //` keeps the `=` up here.
	if eq != nil && !strings.Contains(p.text[r.Name.End:eq.pos], "=") {
		return concat(
			concat(head...),
			text(" "),
			reflowNoWrap(eq.text),
			level(plus4, []Doc{hardline, text("= "), p.statementTail(r.Initializer, trailing)}),
		)
	}
	firstBreak := line
	eqDoc := text("")
	if eq != nil {
		eqDoc = concat(text(" "), reflowNoWrap(eq.text))
		firstBreak = hardline
	}
	inner := []Doc{firstBreak, p.statementTail(r.Initializer, trailing)}
	return concat(concat(head...), text(" ="), eqDoc, level(plus4, inner))
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
	// A `//` comment on the switch's `{` line rides the brace (gjf's toksAfter),
	// so consume it before the clauses claim it as their own-line lead.
	braceTrail := p.braceTrailingComment(strings.Index(p.text[expr.End:], "{") + expr.End + 1)
	var entries []entry
	prevEnd := -1
	for _, c := range nodes(clauses) {
		comments := p.commentsBefore(p.start(c))
		// A line comment on the previous clause's line (`case 'a': // fall through`)
		// trails THAT clause, not the next - append it to the previous entry.
		if len(comments) > 0 && !comments[0].ownLine && len(entries) > 0 {
			entries[len(entries)-1].doc = concat(entries[len(entries)-1].doc, text(" "), reflowNoWrap(comments[0].text))
			comments = comments[1:]
		}
		start := p.start(c)
		if len(comments) > 0 {
			start = comments[0].pos
		}
		leading := prevEnd >= 0 && p.blankBeforePos(prevEnd, start)
		for ci, cm := range comments {
			entries = append(entries, entry{reflow(cm.text), leading})
			// gjf's allowBlankAfterLastComment: a blank the author left after a
			// comment survives, before the next comment or the clause itself.
			next := p.start(c)
			if ci+1 < len(comments) {
				next = comments[ci+1].pos
			}
			leading = p.blankBeforePos(cm.end, next)
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
	header := group(concat(text("switch ("), p.node(expr), text(")")))
	// An empty switch body stays open but holds no blank line (gjf does not
	// collapse it to `{}` the way it collapses an if or a loop).
	if len(entries) == 0 {
		return concat(header, text(" {"), braceTrail, hardline, text("}"))
	}
	return concat(
		header,
		text(" {"),
		braceTrail,
		indent(concat(append([]Doc{hardline}, body...)...)),
		hardline,
		text("}"),
	)
}

func (p *printer) switchClause(c *compiler.SwitchClauseData, end int) Doc {
	// gjf wraps the whole case (labels, `when` guard, `->` and the arrow body) in
	// one level (arrow cases at +4): the labels break one-per-line (UNIFIED), the
	// guard folds before `when` (INDEPENDENT), and the body folds onto its own
	// continuation line - all together when the run overflows.
	var parts []Doc
	if c.IsDefault {
		parts = []Doc{text("default")}
	} else {
		ls := nodes(c.Labels)
		var labelParts []Doc
		for i, l := range ls {
			if i > 0 {
				labelParts = append(labelParts, text(","), brk(fillUnified, " ", ZERO, nil))
			}
			labelParts = append(labelParts, p.node(l))
		}
		parts = []Doc{text("case "), level(ZERO, labelParts)}
	}
	if c.Guard != nil {
		parts = append(parts, brk(fillIndependent, " ", ZERO, nil), text("when "), p.node(c.Guard))
	}
	if c.IsArrow {
		stmts := nodes(c.Statements)
		parts = append(parts, text(" ->"))
		if len(stmts) == 1 && stmts[0].Kind == compiler.Block {
			return concat(level(plus4, parts), text(" "), p.block(stmts[0].AsBlock(), stmts[0].End))
		}
		bodyStart := p.start(stmts[0])
		// A comment before the body sits own-line on the +4 continuation, forcing
		// the break. Consume comments BEFORE rendering the body.
		if p.hasCommentBefore(bodyStart) {
			var cparts []Doc
			for _, c2 := range p.commentsBefore(bodyStart) {
				cparts = append(cparts, reflow(c2.text), hardline)
			}
			ss := make([]Doc, len(stmts))
			for i, st := range stmts {
				ss[i] = p.node(st)
			}
			cparts = append(cparts, join(text(" "), ss))
			parts = append(parts, hardline, concat(cparts...))
		} else {
			ss := make([]Doc, len(stmts))
			for i, st := range stmts {
				ss[i] = p.node(st)
			}
			parts = append(parts, brk(fillUnified, " ", ZERO, nil), join(text(" "), ss))
		}
		return level(plus4, parts)
	}
	parts = append(parts, text(":"))
	// A comment trailing the `case X:` / `default:` label on its line stays there
	// (`case 'a': // fall through`) rather than moving onto the next line.
	bound := end
	if c.Statements.Len() > 0 {
		bound = p.start(c.Statements.Nodes[0])
	}
	head := level(ZERO, parts)
	if p.ci < len(p.comments) {
		t := p.comments[p.ci]
		if !t.ownLine && t.pos < bound {
			p.ci++
			head = concat(head, text(" "), reflowNoWrap(t.text))
		}
	}
	// A fall-through case with no body is just its label; the switch body's clause
	// separator supplies the newline to the next clause.
	if c.Statements.Len() == 0 {
		return head
	}
	return concat(head, indent(concat(append([]Doc{hardline}, p.listDocs(nodes(c.Statements), false, end)...)...)))
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
		// A `//` comment on the same line as the previous operand trails IT (gjf
		// hangs a token's toksAfter off that token, then forces the break), so it
		// goes before the break instead of onto the operator's line. Which side of
		// the operator it sits on decides where it goes: `a + // why` keeps the
		// operator up top, `a // why` leaves it for the continuation line.
		opPos := strings.Index(p.text[operands[i].End:], op)
		afterOp := false
		if opPos >= 0 && p.ci < len(p.comments) {
			opPos += operands[i].End
			c := p.comments[p.ci]
			if c.line && !c.ownLine && c.pos > opPos && strings.TrimLeft(p.text[opPos+len(op):c.pos], " \t") == "" {
				afterOp = true
				p.ci++
				parts = append(parts, text(" "), text(op), text(" "), reflowNoWrap(c.text), hardline)
			}
		}
		if !afterOp {
			if tc, ok := p.trailingLineComment(operands[i].End); ok {
				parts = append(parts, text(" "), reflowNoWrap(tc.text), hardline)
			} else {
				parts = append(parts, brk(fill, " ", ZERO, nil))
			}
		}
		// A comment before the next operand sits on its own line before the
		// operator (gjf), not inside the operand - so consume it here.
		for _, c := range p.commentsBefore(p.start(operands[i+1])) {
			parts = append(parts, reflow(c.text), hardline)
		}
		if afterOp {
			if i == len(operators)-1 {
				parts = append(parts, p.statementTail(operands[i+1], trailing))
			} else {
				parts = append(parts, p.node(operands[i+1]))
			}
			continue
		}
		// The trailing token (`;`, `) {`, ...) rides into the LAST operand's own
		// innermost level - gjf's appendLevel puts it there, so a call in the last
		// operand wraps its arguments when the whole rest of the line overflows.
		if i == len(operators)-1 {
			parts = append(parts, text(op), text(" "), p.statementTail(operands[i+1], trailing))
		} else {
			parts = append(parts, text(op), text(" "), p.node(operands[i+1]))
		}
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

// dotLinkLead renders a comment that sits before a `.link` in a dereference
// chain (between the prior selector and this one) own-line at the chain indent
// (gjf), not pushed into the link's argument list. Consumes such comments.
func (p *printer) dotLinkLead(namePos int) Doc {
	if !p.hasCommentBefore(namePos) {
		return text("")
	}
	var parts []Doc
	for _, c := range p.commentsBefore(namePos) {
		parts = append(parts, reflow(c.text), hardline)
	}
	return concat(parts...)
}

func (p *printer) dotChainTrailing(root *compiler.Node, trailing Doc) Doc {
	// Collect the chain's links WITHOUT rendering them yet: a link's argument
	// list consumes comments, and comments must be consumed in source order (left
	// to right). Rendering eagerly here would consume the OUTER call's args before
	// the receiver's, mis-attributing a receiver-arg comment (e.g.
	// `new Pretty(/*writer*/ null, /*sourceOutput*/ true).operatorName(tag)`).
	// trail is a `//` comment on the same line as the PREVIOUS link
	// (`.foo() // why` / `.bar()`); gjf hangs it off that line, so it is emitted
	// before this link's break rather than after it. Filled in during render.
	type linkT struct {
		isCall bool
		name   string
		trail  *string
		render func() Doc
	}
	var links []*linkT
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
			lk := &linkT{isCall: true, name: name}
			// Explicit method type arguments go between the dot and the name:
			// `obj.<String>foo(x)`, not `obj.foo<String>(x)`.
			lk.render = func() Doc {
				if tc, ok := p.trailingLineComment(pa.Expression.End); ok {
					lk.trail = &tc.text
				}
				return concat(p.dotLinkLead(p.start(pa.Name)), text("."), p.typeArguments(ce.TypeArguments), text(name), p.argListTrailing(ce.Arguments, argTrailing))
			}
			links = append([]*linkT{lk}, links...)
			cur = pa.Expression
			continue
		case cur.Kind == compiler.PropertyAccessExpression:
			pa := cur.AsPropertyAccessExpression()
			name := p.raw(pa.Name)
			lk := &linkT{isCall: false, name: name}
			lk.render = func() Doc {
				if tc, ok := p.trailingLineComment(pa.Expression.End); ok {
					lk.trail = &tc.text
				}
				return concat(p.dotLinkLead(p.start(pa.Name)), text("."), text(name))
			}
			links = append([]*linkT{lk}, links...)
			cur = pa.Expression
			continue
		}
		break
	}
	// Render the base (leftmost receiver) first, then each link in source order,
	// so comments are consumed left to right. An own-line comment BEFORE the base
	// belongs above the whole chain (gjf), not between the receiver and its first
	// dereference - consume it here so dotLinkLead cannot claim it.
	var chainLead []Doc
	for _, c := range p.commentsBefore(p.start(cur)) {
		chainLead = append(chainLead, reflow(c.text), hardline)
	}
	base := p.node(cur)
	linkDocs := make([]Doc, len(links))
	for i, l := range links {
		linkDocs[i] = l.render()
	}
	// A trailing token not routed into a rightmost call's args (chain ends in a
	// field access) is appended after the whole chain.
	finish := func(doc Doc) Doc {
		if trailing != nil && !trailingRouted {
			doc = concat(doc, trailing)
		}
		if len(chainLead) == 0 {
			return doc
		}
		return concat(append(append([]Doc{}, chainLead...), doc)...)
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
	// An anonymous class receiver (`new X() {..}`) already spans multiple lines and
	// provides its own indentation; gjf glues a single dereference onto its closing
	// `}` (`}.scan(..)`) rather than starting a +4 chain that re-indents the body.
	baseIsAnonClass := baseIsNew && cur.AsObjectCreationExpression().ClassBody != nil
	// A multi-line text-block receiver breaks before its dereference (gjf puts
	// `.replace(..)` on its own +4 line after the closing `"""`).
	baseIsMultilineTextBlock := cur.Kind == compiler.TextBlockLiteral && strings.Contains(p.raw(cur), "\n")
	// A string-literal receiver is not a type-name prefix, so gjf never glues the
	// dereference to it: `"...long...".getBytes(x)` breaks before the `.` when it
	// does not fit, instead of wrapping the call's arguments.
	baseIsStringLiteral := cur.Kind == compiler.StringLiteral
	if callCount == 1 && !baseIsCall && (!baseIsNew || baseIsAnonClass) && !baseIsMultilineTextBlock &&
		!baseIsStringLiteral {
		parts := []Doc{base}
		for i, l := range links {
			if l.trail != nil {
				parts = append(parts, text(" "), reflowNoWrap(*l.trail), hardline)
			}
			parts = append(parts, linkDocs[i])
		}
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
	for i, l := range links {
		// A comment trailing the previous link rides that line and forces the
		// break (gjf always breaks after a line comment).
		switch {
		case l.trail != nil:
			parts = append(parts, text(" "), reflowNoWrap(*l.trail), hardline)
		case i >= glue:
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
	case compiler.ConditionalExpression:
		return p.conditionalTrailing(e.AsConditionalExpression(), trailing)
	case compiler.LambdaExpression:
		return p.lambdaTrailing(e.AsLambdaExpression(), trailing)
	case compiler.PrefixUnaryExpression:
		// `!foo(...)` - the operator is glued, so the tail belongs to the operand.
		u := e.AsPrefixUnaryExpression()
		if op := compiler.TokenToString(u.Operator); op == "!" || op == "~" {
			return concat(text(op), p.statementTail(u.Operand, trailing))
		}
	case compiler.ParenthesizedExpression:
		// The closing `)` is part of the rest of the line, so it rides in too.
		pe := e.AsParenthesizedExpression()
		closeTok := text(")")
		if trailing != nil {
			closeTok = concat(text(")"), trailing)
		}
		return concat(text("("), p.statementTail(pe.Expression, closeTok))
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

// pairedArgList lays out two arguments per line, each pair its own +4 level
// (gjf addArguments). The closing `)` rides inside the last pair, as gjf's
// DocBuilder places it.
func (p *printer) pairedArgList(args []*compiler.Node, trailing Doc) Doc {
	var pairs []Doc
	for i := 0; i < len(args); i += 2 {
		if i > 0 {
			pairs = append(pairs, text(","), brk(fillForced, "", ZERO, nil))
		}
		pair := []Doc{p.node(args[i]), text(","), brk(fillUnified, " ", ZERO, nil), p.node(args[i+1])}
		if i+2 >= len(args) {
			pair = append(pair, text(")"))
			if trailing != nil {
				pair = append(pair, trailing)
			}
		}
		pairs = append(pairs, level(plus4, pair))
	}
	return concat(text("("), level(plus4, []Doc{brk(fillForced, "", ZERO, nil), level(ZERO, pairs)}))
}

func (p *printer) argListTrailing(args *compiler.NodeArray, trailing Doc) Doc {
	if args.Len() == 0 {
		if trailing != nil {
			return concat(text("()"), trailing)
		}
		return text("()")
	}
	argNodes := nodes(args)
	// gjf's addArguments lays arguments out two per line when the author already
	// did (a map-constructor-like call): an even count laid out as a two-column
	// grid in source. Comments would land mid-pair, so leave those to the general
	// path.
	if len(argNodes)%2 == 0 && p.argumentsAreTabular(argNodes) == 2 &&
		(p.ci >= len(p.comments) || p.comments[p.ci].pos > argNodes[len(argNodes)-1].End) {
		return p.pairedArgList(argNodes, trailing)
	}
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
	leads := make([]string, len(argNodes))
	leadStarts := make([]int, len(argNodes))
	// A `//` comment on the `(`'s own line stays there (`foo( // why`), which also
	// forces the break before the first argument.
	openTrail := ""
	for i, a := range argNodes {
		var parts []Doc
		leadStarts[i] = p.start(a)
		if p.ci < len(p.comments) && p.comments[p.ci].pos < p.start(a) {
			leadStarts[i] = p.comments[p.ci].pos
		}
		// Leading comments: a block comment renders inline before the argument
		// (`/* a= */ 1`); a line comment forces a break after itself. A line comment
		// on the PREVIOUS argument's line (non-own-line) trails THAT argument - the
		// join emits it before the inter-argument break.
		for ci, c := range p.commentsBefore(p.start(a)) {
			anyComment = true
			switch {
			case ci == 0 && c.line && !c.ownLine:
				// Trails the `(` (first argument) or the previous argument's line.
				if i > 0 {
					leads[i] = c.text
				} else {
					openTrail = c.text
				}
			case c.line:
				parts = append(parts, reflow(c.text), hardline)
			default:
				pc := c.text
				if norm, ok := reformatParamComment(c.text); ok {
					pc = norm
				}
				parts = append(parts, text(pc), text(" "))
			}
		}
		var follow = text(",")
		if i == lastI {
			if trailing != nil {
				follow = concat(text(")"), trailing)
			} else {
				follow = text(")")
			}
		}
		// Only whitespace may separate the argument from its trailing comment: a
		// `)` in between means the comment trails the CALL (`foo(a); // note`), and
		// a `,` means it trails the separator, not this argument.
		trailsThis := false
		var t comment
		if p.ci < len(p.comments) {
			t = p.comments[p.ci]
			trailsThis = !t.ownLine && t.pos >= a.End &&
				!strings.ContainsAny(p.text[a.End:t.pos], "\n,)")
		}
		// A `//` comment trailing the LAST argument keeps the `)` off its line -
		// the comment would swallow it. gjf breaks before the `)`, which then lands
		// at the arguments' own indent.
		switch {
		case trailsThis && t.line && i == lastI:
			p.ci++
			anyComment = true
			parts = append(parts, p.node(a), text(" "), reflowNoWrap(t.text), hardline, text(")"))
			if trailing != nil {
				parts = append(parts, trailing)
			}
		case trailsThis && !t.line:
			parts = append(parts, p.node(a))
			var attached bool
			parts, attached = p.attachTrailingBlockComment(parts, a.End)
			anyComment = anyComment || attached
			parts = append(parts, follow)
		default:
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
	// A source blank line between `(` and the first argument's leading comment is
	// preserved (gjf preserves a blank before an own-line comment, not a bare arg).
	if leadStarts[0] < p.start(argNodes[0]) && p.blankBeforePos(argNodes[0].Pos, leadStarts[0]) {
		innerParts = append(innerParts, hardline)
	}
	for i, it := range as {
		if i > 0 {
			// A line comment trailing the previous argument stays on its line (before
			// the break); gjf also preserves one source blank line between arguments.
			blank := p.blankBeforePos(argNodes[i-1].End, leadStarts[i])
			if leads[i] != "" {
				innerParts = append(innerParts, text(" "), reflowNoWrap(leads[i]), hardline)
			} else {
				innerParts = append(innerParts, brk(fill, " ", ZERO, nil))
			}
			if blank {
				innerParts = append(innerParts, hardline)
			}
		}
		innerParts = append(innerParts, it)
	}
	open := []Doc{text("(")}
	firstBreak := brk(fillUnified, "", ZERO, nil)
	if openTrail != "" {
		open = append(open, text(" "), reflowNoWrap(openTrail))
		firstBreak = hardline
	}
	open = append(open, level(plus4, []Doc{firstBreak, level(ZERO, innerParts)}))
	return concat(open...)
}

// isFormatMethod is gjf's isFormatMethod: a call whose first argument is a
// string-literal concatenation containing a format specifier, with >= 2 args.
func (p *printer) isFormatMethod(args []*compiler.Node) bool {
	return len(args) >= 2 && p.isFormatStringConcat(args[0])
}

// reindentTextBlock keeps a multi-line text block at its source indentation, but
// strips the block's common indentation entirely (content to column 0) when any
// content line would overflow 100 columns at that indentation. raw is the
// verbatim `"""..."""` source.
func reindentTextBlock(raw string) string {
	lines := strings.Split(raw, "\n")
	if len(lines) < 3 {
		return raw
	}
	content := lines[1 : len(lines)-1]
	overflow := false
	for _, l := range content {
		if strings.TrimSpace(l) != "" && utf8.RuneCountInString(l) > width {
			overflow = true
			break
		}
	}
	if !overflow {
		return raw
	}
	closing := lines[len(lines)-1]
	sourceIndent := len(closing) - len(strings.TrimLeft(closing, " \t"))
	strip := func(l string) string {
		if len(l) >= sourceIndent {
			return l[sourceIndent:]
		}
		return strings.TrimLeft(l, " \t")
	}
	out := []string{lines[0]}
	for _, l := range content {
		if strings.TrimSpace(l) == "" {
			out = append(out, "")
		} else {
			out = append(out, strip(l))
		}
	}
	out = append(out, strip(closing))
	return strings.Join(out, "\n")
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
		init = concat(text(" "), p.arrayInitializer(e.Initializer.AsArrayInitializer(), e.Initializer.End))
	}
	return concat(text("new "), p.typ(e.ElementType), concat(dims...), text(extra), init)
}

// The source column an element starts at, used by the tabular check below.
// gjf's OpsBuilder.actualStartColumn: a comment on the element's own line moves
// the start back to the comment (`/* 0 */ {'A', 'B'}` starts at the comment),
// but a comment on an earlier line ends the scan and the element keeps its own
// column.
func (p *printer) elementColumn(n *compiler.Node) int {
	column := func(pos int) int { return pos - (strings.LastIndex(p.text[:pos], "\n") + 1) }
	start := compiler.SkipTrivia(p.text, n.Pos)
	at := start
	for _, c := range p.comments {
		if c.pos < n.Pos {
			continue
		}
		if c.pos >= start {
			break
		}
		if strings.Contains(p.text[c.pos:start], "\n") {
			return column(start)
		}
		if c.pos < at {
			at = c.pos
		}
	}
	return column(at)
}

// initializerOf returns the array initializer a grid row element holds, if any.
func initializerOf(n *compiler.Node) *compiler.ArrayInitializerData {
	switch n.Kind {
	case compiler.ArrayInitializer:
		return n.AsArrayInitializer()
	case compiler.ArrayCreationExpression:
		if init := n.AsArrayCreationExpression().Initializer; init != nil {
			return init.AsArrayInitializer()
		}
	}
	return nil
}

// gjf rowLength: a nested array initializer counts as its own element count,
// so `{{1, 2}, {3, 4}}` is a row of four, not of two.
func rowLength(row []*compiler.Node) int {
	size := 0
	for _, n := range row {
		if inner := initializerOf(n); inner != nil {
			size += rowLength(nodes(inner.Elements))
			continue
		}
		size++
	}
	return size
}

// gjf expressionsAreParallel: at least atLeastM rows have the same node kind in
// this column. A unary expression counts as its operand's kind, so `-1` and `1`
// are the same shape.
func expressionsAreParallel(rows [][]*compiler.Node, column, atLeastM int) bool {
	counts := map[compiler.SyntaxKind]int{}
	for _, row := range rows {
		if column >= len(row) {
			continue
		}
		n := row[column]
		k := n.Kind
		if k == compiler.PrefixUnaryExpression {
			k = n.AsPrefixUnaryExpression().Operand.Kind
		}
		counts[k]++
	}
	for _, c := range counts {
		if c >= atLeastM {
			return true
		}
	}
	return false
}

// gjf argumentsAreTabular: when the author already laid the elements out as a
// grid (every row starting at the same column, parallel expression kinds), gjf
// preserves those rows. Returns the number of columns, or -1.
func (p *printer) argumentsAreTabular(args []*compiler.Node) int {
	if len(args) == 0 {
		return -1
	}
	var rows [][]*compiler.Node
	col0 := p.elementColumn(args[0])
	i := 0
	row := []*compiler.Node{args[i]}
	i++
	for i < len(args) && p.elementColumn(args[i]) > col0 {
		row = append(row, args[i])
		i++
	}
	if i >= len(args) || rowLength(row) <= 1 {
		return -1
	}
	rows = append(rows, row)
	for i < len(args) {
		if p.elementColumn(args[i]) != col0 {
			return -1
		}
		row := []*compiler.Node{args[i]}
		i++
		for i < len(args) && p.elementColumn(args[i]) > col0 {
			row = append(row, args[i])
			i++
		}
		rows = append(rows, row)
	}
	size0 := len(rows[0])
	if !expressionsAreParallel(rows, 0, len(rows)) {
		return -1
	}
	for c := 1; c < size0; c++ {
		if !expressionsAreParallel(rows, c, len(rows)/2+1) {
			return -1
		}
	}
	// With only two rows they must be the same length; otherwise a ragged last
	// row is allowed (but no shorter middle row, and no longer last row).
	if len(rows) == 2 {
		if size0 == len(rows[1]) {
			return size0
		}
		return -1
	}
	for r := 1; r < len(rows)-1; r++ {
		if size0 != len(rows[r]) {
			return -1
		}
	}
	if size0 < len(rows[len(rows)-1]) {
		return -1
	}
	return size0
}

// gjf visitArrayInitializer's tabular branch: cols elements per line, each row
// its own level so a too-long row fills at +4 (rows of arrays stay at the row
// indent).
func (p *printer) tabularArrayInitializer(els []*compiler.Node, cols, end int) Doc {
	parts := []Doc{brk(fillForced, "", ZERO, nil)}
	for start := 0; start < len(els); start += cols {
		end := start + cols
		if end > len(els) {
			end = len(els)
		}
		row := els[start:end]
		if start > 0 {
			parts = append(parts, brk(fillForced, "", ZERO, nil))
		}
		// An own-line comment before the row (a column header, say) keeps its own
		// line above it.
		rowStart := compiler.SkipTrivia(p.text, row[0].Pos)
		lead := p.commentsBefore(rowStart)
		// gjf preserves one source blank line between rows.
		if start > 0 {
			leadStart := rowStart
			if len(lead) > 0 {
				leadStart = lead[0].pos
			}
			if p.blankBeforePos(els[start-1].End, leadStart) {
				parts = append(parts, brk(fillForced, "", ZERO, nil))
			}
		}
		for _, c := range lead {
			parts = append(parts, reflow(c.text), brk(fillForced, "", ZERO, nil))
		}
		var rowParts []Doc
		for j, el := range row {
			if j > 0 {
				rowParts = append(rowParts, text(","))
				// A `//` comment trailing the previous element stays on its line, and
				// forces the break there - which is how a long row wraps mid-row.
				var taken bool
				rowParts, taken = p.takeTrailingRowComment(rowParts, row[j-1])
				if taken {
					rowParts = append(rowParts, brk(fillForced, "", ZERO, nil))
				} else {
					rowParts = append(rowParts, brk(fillIndependent, " ", ZERO, nil))
				}
			}
			rowParts = append(rowParts, p.node(el))
		}
		last := row[len(row)-1]
		if idx := compiler.SkipTrivia(p.text, last.End); idx < len(p.text) && p.text[idx] == ',' {
			rowParts = append(rowParts, text(","))
		}
		rowParts, _ = p.takeTrailingRowComment(rowParts, last)
		rowIndent := plus4
		if cols == 1 || initializerOf(row[0]) != nil {
			rowIndent = ZERO
		}
		parts = append(parts, level(rowIndent, rowParts))
	}
	// Own-line comments between the last row and the `}` belong inside the braces.
	for _, c := range p.commentsBefore(end - 1) {
		parts = append(parts, brk(fillForced, "", ZERO, nil), reflow(c.text))
	}
	parts = append(parts, brk(fillForced, "", minus2, nil))
	return concat(text("{"), level(plus2, parts), text("}"))
}

// takeTrailingRowComment consumes a `//` comment sitting on the same line just
// after el (past its comma) and appends it to the row.
func (p *printer) takeTrailingRowComment(rowParts []Doc, el *compiler.Node) ([]Doc, bool) {
	// Skip whitespace only - SkipTrivia would step over the very comment we are
	// looking for.
	after := el.End
	for after < len(p.text) && (p.text[after] == ' ' || p.text[after] == '\t') {
		after++
	}
	if after < len(p.text) && p.text[after] == ',' {
		after++
	}
	if p.ci >= len(p.comments) {
		return rowParts, false
	}
	c := p.comments[p.ci]
	if !c.line || c.pos < after {
		return rowParts, false
	}
	// Only whitespace may separate them, and no newline: the comment must sit
	// directly after this element on its line.
	gap := p.text[after:c.pos]
	if strings.TrimSpace(gap) != "" || strings.Contains(gap, "\n") {
		return rowParts, false
	}
	p.ci++
	return append(rowParts, text(" "), reflowNoWrap(c.text)), true
}

// tabularCommentsPlaceable reports whether every comment inside the initializer
// sits where the tabular layout can place it: on its own line before a row, or
// trailing an element. An own-line comment in the middle of a row has nowhere
// to go.
func (p *printer) tabularCommentsPlaceable(els []*compiler.Node, cols int) bool {
	for i := 1; i < len(els); i++ {
		if i%cols == 0 {
			continue
		}
		start := compiler.SkipTrivia(p.text, els[i].Pos)
		for k := p.ci; k < len(p.comments); k++ {
			c := p.comments[k]
			if c.pos >= start {
				break
			}
			if c.pos > els[i-1].End && (c.ownLine || !c.line) {
				return false
			}
		}
	}
	return true
}

func (p *printer) arrayInitializer(e *compiler.ArrayInitializerData, end int) Doc {
	if e.Elements.Len() == 0 {
		return text("{}")
	}
	// A source-laid-out grid is preserved verbatim as rows (gjf).
	if cols := p.argumentsAreTabular(nodes(e.Elements)); cols != -1 && p.tabularCommentsPlaceable(nodes(e.Elements), cols) {
		return p.tabularArrayInitializer(nodes(e.Elements), cols, end)
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
	rs := p.listItemsEx(els, func(el *compiler.Node) Doc { return p.node(el) }, true)
	anyComment := false
	for _, r := range rs {
		if r.comment {
			anyComment = true
		}
	}
	// A comment forces one-per-line (gjf), else short items fill.
	fill := p.fillMode(anyComment, els)
	var innerParts []Doc
	for i, r := range rs {
		if i > 0 {
			innerParts = append(innerParts, text(","))
			// gjf preserves one source blank line between elements.
			blank := p.blankBeforePos(els[i-1].End, r.leadStart)
			// A line comment trailing the previous element stays on its line, then
			// forces the break before this element.
			if r.leadTrailing != "" {
				innerParts = append(innerParts, text(" "), text(r.leadTrailing), hardline)
			} else {
				innerParts = append(innerParts, brk(fill, " ", ZERO, nil))
			}
			if blank {
				innerParts = append(innerParts, hardline)
			}
		}
		innerParts = append(innerParts, r.doc)
	}
	if trailingComma {
		innerParts = append(innerParts, text(","))
	}
	// A line comment after the last element (before `}`) stays on its line.
	closingComment := ""
	if p.ci < len(p.comments) {
		t := p.comments[p.ci]
		lastEnd := els[len(els)-1].End
		if t.line && !t.ownLine && t.pos > lastEnd && p.trailsDirectly(lastEnd, t.pos) {
			p.ci++
			closingComment = t.text
		}
	}
	if closingComment != "" {
		innerParts = append(innerParts, text(" "), reflowNoWrap(closingComment))
	}
	// Own-line comments between the last element and the `}` belong INSIDE the
	// braces (gjf); they used to stay pending and surface after the whole
	// declaration, e.g. a `// @formatter:on` marker jumping past its `};`.
	dangling := p.commentsBefore(end - 1)
	for _, c := range dangling {
		innerParts = append(innerParts, hardline, reflow(c.text))
	}
	open := fillUnified
	if trailingComma || closingComment != "" || len(dangling) > 0 {
		open = fillForced
	}
	inner := level(ZERO, innerParts)
	return concat(
		text("{"),
		level(plus2, []Doc{brk(open, "", ZERO, nil), inner, brk(open, "", minus2, nil)}),
		text("}"),
	)
}

func (p *printer) lambda(e *compiler.LambdaExpressionData) Doc {
	return p.lambdaTrailing(e, nil)
}

// lambdaTrailing is lambda with the rest of the line (the argument's `,`, the
// call's `)`, the `;`) routed inside the body's level, so it counts in the
// body's own fit check (gjf's appendLevel); a body that only overflows because
// of it breaks after `->`.
func (p *printer) lambdaTrailing(e *compiler.LambdaExpressionData, trailing Doc) Doc {
	appendTrailing := func(d Doc) Doc {
		if trailing == nil {
			return d
		}
		return concat(d, trailing)
	}
	params := nodes(e.Parameters)
	var head Doc
	if len(params) == 1 && params[0].Kind == compiler.Identifier {
		head = text(p.raw(params[0]))
	} else {
		ps := make([]Doc, len(params))
		for i, pp := range params {
			if pp.Kind == compiler.Parameter {
				ps[i] = p.parameter(pp)
			} else {
				ps[i] = text(p.raw(pp))
			}
		}
		head = concat(text("("), join(text(", "), ps), text(")"))
	}
	if e.Body.Kind == compiler.Block {
		return appendTrailing(concat(head, text(" -> "), p.block(e.Body.AsBlock(), e.Body.End)))
	}
	// A comment before an expression body sits own-line at a +8 continuation
	// indent (gjf), forcing `-> ` onto its own line; the comment forces the break.
	if p.hasCommentBefore(p.start(e.Body)) {
		var parts []Doc
		for _, c := range p.commentsBefore(p.start(e.Body)) {
			parts = append(parts, reflow(c.text), hardline)
		}
		parts = append(parts, p.node(e.Body))
		if trailing != nil {
			parts = append(parts, trailing)
		}
		return concat(head, text(" ->"), level(plus4, []Doc{hardline, concat(parts...)}))
	}
	// An expression body folds onto a +4 continuation line after `->` when it
	// does not fit (gjf), like the switch-arrow body above.
	return concat(head, text(" ->"), level(plus4, []Doc{line, p.statementTail(e.Body, trailing)}))
}

// conditional lays out a ternary: the condition stays on the line, `?` and `:`
// break onto +4 continuation lines (UNIFIED).
func (p *printer) conditional(e *compiler.ConditionalExpressionData) Doc {
	return p.conditionalTrailing(e, nil)
}

// conditionalTrailing is conditional with the statement's `;` (and any trailing
// comment) routed inside the level, so the branches break when the whole rest
// of the line overflows.
func (p *printer) conditionalTrailing(e *compiler.ConditionalExpressionData, trailing Doc) Doc {
	parts := []Doc{
		p.node(e.Condition),
		brk(fillUnified, " ", ZERO, nil),
		text("? "),
		p.node(e.WhenTrue),
	}
	// A comment trailing the then-branch on its line stays there (gjf); a line
	// comment forces the `:` onto the next line (it would otherwise comment it out).
	if tc, ok := p.trailingComment(e.WhenTrue.End); ok {
		parts = append(parts, text(" "), reflowNoWrap(tc.text))
		if tc.line {
			parts = append(parts, hardline)
		} else {
			parts = append(parts, brk(fillUnified, " ", ZERO, nil))
		}
	} else {
		parts = append(parts, brk(fillUnified, " ", ZERO, nil))
	}
	parts = append(parts, text(": "))
	// A comment before the else-branch renders inline before it (`: /* a= */ x`).
	for _, c := range p.commentsBefore(p.start(e.WhenFalse)) {
		if c.line {
			parts = append(parts, reflow(c.text), hardline)
		} else {
			parts = append(parts, text(c.text), text(" "))
		}
	}
	parts = append(parts, p.node(e.WhenFalse))
	if trailing != nil {
		parts = append(parts, trailing)
	}
	return level(plus4, parts)
}

// trailingComment consumes and returns a comment trailing endPos on the same
// source line (only whitespace between), or ok=false otherwise.
// trailingLineComment consumes a `//` comment trailing endPos on its line. gjf
// attaches such a comment to the token it follows (its toksAfter) and forces a
// break after it, so callers emit it before their own break rather than after.
func (p *printer) trailingLineComment(endPos int) (comment, bool) {
	if p.ci >= len(p.comments) {
		return comment{}, false
	}
	t := p.comments[p.ci]
	if !t.line || t.ownLine {
		return comment{}, false
	}
	return p.trailingComment(endPos)
}

// lineCommentAfterAssign consumes a `//` comment that sits between a
// declarator's `=` and its initializer on the `=`'s line (`X x = // why`).
func (p *printer) lineCommentAfterAssign(from, initStart int) (comment, bool) {
	if p.ci >= len(p.comments) {
		return comment{}, false
	}
	t := p.comments[p.ci]
	// The pending comment can lie BEFORE the name (an earlier one this
	// declaration does not own), which is not ours and would slice backwards.
	if !t.line || t.ownLine || t.pos < from || t.pos >= initStart {
		return comment{}, false
	}
	if strings.Contains(p.text[from:t.pos], "\n") {
		return comment{}, false
	}
	p.ci++
	return t, true
}

func (p *printer) trailingComment(endPos int) (comment, bool) {
	if p.ci < len(p.comments) {
		t := p.comments[p.ci]
		if !t.ownLine && t.pos >= endPos && onlySpaces(p.text[endPos:t.pos]) {
			p.ci++
			return t, true
		}
	}
	return comment{}, false
}

func onlySpaces(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return false
		}
	}
	return true
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
		return p.methodLikeDefault(m.Modifiers, m.TypeParameters, m.ReturnType, m.Name, m.Parameters, m.Throws, m.Body, m.DefaultValue)
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
		return concat(text("synchronized ("), p.node(s.Expression), text(") "), p.blockOpen(s.Body.AsBlock(), p.start(s.Body), s.Body.End, false))
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
		return p.arrayInitializer(node.AsArrayInitializer(), node.End)
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
		// An array constructor reference (Foo[]::new, int[]::new) parses as a
		// class literal carrying the array type, but has no ".class" to print.
		// Only the ::new form: String.class::getName keeps its ".class".
		receiver := p.node(e.Expression)
		if e.IsConstructorRef && e.Expression.Kind == compiler.ClassLiteralExpression {
			receiver = p.typ(e.Expression.AsClassLiteralExpression().Type)
		}
		// Explicit type arguments sit between `::` and the name
		// (`ObjectUtils::<String>median`); dropping them changed the code.
		return concat(receiver, text("::"), p.typeArguments(e.TypeArguments), text(ref))
	// Qualified forms keep their qualifier: Outer.this, Outer.super.m(), and the
	// qualified superclass constructor call outer.super(...).
	case compiler.ThisExpression:
		if q := node.AsThisExpression().Qualifier; q != nil {
			return concat(p.node(q), text(".this"))
		}
		return text("this")
	case compiler.SuperExpression:
		if q := node.AsSuperExpression().Qualifier; q != nil {
			return concat(p.node(q), text(".super"))
		}
		return text("super")
	case compiler.ClassLiteralExpression:
		return concat(p.typ(node.AsClassLiteralExpression().Type), text(".class"))

	case compiler.TextBlockLiteral:
		raw := p.raw(node)
		// A multi-line text block is reflowed at write time: gjf strips the block's
		// common indentation (to column 0) when a content line would overflow at its
		// current indent; otherwise it keeps the source indent.
		if strings.Contains(raw, "\n") {
			return reflow(raw)
		}
		return text(raw)

	case compiler.Identifier, compiler.NumericLiteral, compiler.StringLiteral,
		compiler.CharacterLiteral, compiler.TrueKeyword,
		compiler.FalseKeyword, compiler.NullKeyword:
		return text(p.raw(node))

	case compiler.PrimitiveType, compiler.TypeReference, compiler.ArrayType,
		compiler.WildcardType, compiler.VarType:
		return p.typ(node)

	case compiler.CompactConstructorDeclaration:
		// `record R(int x) { R { validate(x); } }` - no parameter list, so the
		// body's `{` carries any trailing comment itself. It used to fall through
		// to the verbatim slice, leaving its body unformatted.
		c := node.AsCompactConstructorDeclaration()
		return concat(
			p.modifiers(c.Modifiers, "own"),
			text(p.raw(c.Name)),
			text(" "),
			p.block(c.Body.AsBlock(), c.Body.End),
		)

	case compiler.AnnotationTypeDeclaration:
		// Rendered like an interface: `@interface Name` plus a member body. It used
		// to fall through to the verbatim slice, which left an empty body as `{\n}`
		// and skipped every other rule inside it.
		d := node.AsAnnotationTypeDeclaration()
		return concat(
			p.modifiers(d.Modifiers, "own"),
			text("@interface "),
			text(p.raw(d.Name)),
			text(" "),
			p.body(d.Members, node.End),
		)

	case compiler.QualifiedName:
		return text(p.entityName(node))
	case compiler.Annotation:
		return p.annotation(node.AsAnnotation())

	default:
		// Degrade, do not crash: emit the verbatim source slice. Any comment
		// inside it is part of that slice, so drop it from the pending stream:
		// otherwise it is flushed again at the end of the file and the output
		// grows another copy on every run (an @interface with javadoc'd
		// members, which lands here, did exactly that).
		raw := text(p.raw(node))
		for p.ci < len(p.comments) && p.comments[p.ci].pos < node.End {
			p.ci++
		}
		return raw
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

// isJavadocComment reports whether c is a javadoc comment. It belongs to the
// declaration that follows it, so google-java-format emits them adjacent even
// when the source left a blank line in between. Any other comment keeps its
// blank.
func isJavadocComment(c *comment) bool {
	return c != nil && !c.line && strings.HasPrefix(c.text, "/**")
}

// anyJavadoc reports whether any of the leading comments is a javadoc, which
// makes even a field want a blank line before it (gjf's hasJavaDoc).
func anyJavadoc(comments []comment) bool {
	for i := range comments {
		if isJavadocComment(&comments[i]) {
			return true
		}
	}
	return false
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
