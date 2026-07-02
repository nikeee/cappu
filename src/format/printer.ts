// Lower a parsed Java source file to the Doc IR (doc.ts), which is then printed
// at the configured width. The visitor regenerates all layout from the AST -
// google-java-format does the same, discarding original whitespace - so cappu's
// trivia-free AST is sufficient. The only thing recovered from source is whether
// the user left a blank line between two members/statements (g-j-f preserves one).
//
// Node kinds not yet handled fall back to the verbatim source slice (degrade,
// never crash), matching the emitter's discipline.

import { skipTrivia, tokenToString } from "../compiler/utilities.ts";
import { reformatParamComment, rewriteComment } from "./comment-rewrite.ts";
import { type Comment, collectComments } from "./comments.ts";
import {
  type Annotation,
  type ArrayCreationExpression,
  type ArrayInitializer,
  type ArrayType,
  type AssertStatement,
  type AssignmentExpression,
  type BinaryExpression,
  type Block,
  type CallExpression,
  type CastExpression,
  type ClassDeclaration,
  type ClassLiteralExpression,
  type ConditionalExpression,
  type ConstructorDeclaration,
  type DoStatement,
  type ElementAccessExpression,
  type EntityName,
  type ExportsDirective,
  type ExpressionStatement,
  type EnumConstantDeclaration,
  type EnumDeclaration,
  type Expression,
  type FieldDeclaration,
  type ForEachStatement,
  type ForStatement,
  type IfStatement,
  type ImportDeclaration,
  type InitializerBlock,
  type InstanceofExpression,
  type InterfaceDeclaration,
  type LabeledStatement,
  type LambdaExpression,
  type LocalVariableDeclarationStatement,
  type MethodDeclaration,
  type MethodReferenceExpression,
  type ModifierLike,
  type ModuleDeclaration,
  type Node,
  type NodeArray,
  type ObjectCreationExpression,
  type OpensDirective,
  type Parameter,
  type ParenthesizedExpression,
  type PostfixUnaryExpression,
  type PrimitiveType,
  type PrefixUnaryExpression,
  type PropertyAccessExpression,
  type ProvidesDirective,
  type RecordComponent,
  type RecordDeclaration,
  type RequiresDirective,
  type Resource,
  type ReturnStatement,
  type SourceFile,
  type Statement,
  type SwitchClause,
  type SwitchExpression,
  type SwitchStatement,
  type SynchronizedStatement,
  SyntaxKind,
  type ThrowStatement,
  type TryStatement,
  type TypeNode,
  type TypeParameter,
  type TypeReference,
  type UsesDirective,
  type VariableDeclarator,
  type WhileStatement,
  type WildcardType,
  type YieldStatement,
} from "../compiler/types.ts";
import {
  brk,
  BreakTag,
  concat,
  type Doc,
  type FillMode,
  group,
  hardline,
  indent,
  indentConst,
  indentIf,
  join,
  level,
  line,
  printDoc,
  reflow,
  ZERO,
} from "./doc.ts";

// google-java-format continuation indents (columns at google scale; the printer
// is built once and the style multiplier is applied at print time):
//   +2 = one indent level (block body, array-initializer continuation)
//   +4 = a continuation (broken argument/parameter/type lists, operator chains)
const PLUS2 = indentConst(2);
const PLUS4 = indentConst(4);
const MINUS2 = indentConst(-2);

// google-java-format glues a dereference chain's receiver through a call to one
// of these methods (see gjf JavaInputAstVisitor#handleStream): the call's index
// becomes a chain-prefix boundary, so `x.stream().a().b()` keeps `x.stream()`
// together and breaks before the rest.
const STREAM_PREFIX_METHODS = new Set(["stream", "parallelStream", "toBuilder"]);

// Well-known nullness type annotations (gjf JavaInputAstVisitor#typeAnnotations).
// An `@Nullable`/`@NonNull` imported from one of these is a TYPE annotation and
// renders inline before the type rather than on its own line.
const TYPE_ANNOTATION_FQNS = new Set([
  "org.jspecify.annotations.NonNull",
  "org.jspecify.annotations.Nullable",
  "org.checkerframework.checker.nullness.qual.NonNull",
  "org.checkerframework.checker.nullness.qual.Nullable",
  // gjf relies on javac attaching a TYPE_USE annotation to the type it precedes;
  // we have no symbol resolution, so we list the jetbrains nullness annotations
  // (declared @Target(TYPE_USE)) to keep them inline before the type as javac/gjf
  // would. They are not in gjf's own list (it does not need them).
  "org.jetbrains.annotations.NotNull",
  "org.jetbrains.annotations.Nullable",
]);

export interface FormatOptions {
  style: "google" | "aosp";
}

const WIDTH = 100;

/** Thrown when the formatter cannot format the input without losing information. */
export class UnsupportedSyntaxError extends Error {}

// gjf keeps a multi-line text block at its source indentation, but strips the
// block's common indentation entirely (content to column 0) when any content line
// overflows 100 columns at that indentation. `raw` is the verbatim `"""..."""`
// source. (This models gjf's overflow de-indent; it does not re-align a block up
// when the enclosing indentation changes - not needed for the google corpus.)
function reindentTextBlock(raw: string): string {
  const lines = raw.split("\n");
  if (lines.length < 3) return raw; // single-line text block: nothing to reindent
  const content = lines.slice(1, -1);
  if (!content.some(l => l.trim() !== "" && l.length > WIDTH)) return raw;
  const closing = lines[lines.length - 1];
  const sourceIndent = closing.length - closing.trimStart().length;
  const strip = (l: string): string => (l.length >= sourceIndent ? l.slice(sourceIndent) : l.trimStart());
  const out = [lines[0]];
  for (const l of content) out.push(l.trim() === "" ? "" : strip(l));
  out.push(strip(closing));
  return out.join("\n");
}

export function formatSourceFile(sf: SourceFile, options: FormatOptions): string {
  const mult = options.style === "aosp" ? 2 : 1;
  const p = new Printer(sf, mult);
  const doc = p.sourceFile(sf);
  const text = printDoc(doc, {
    width: WIDTH,
    indentMultiplier: mult,
    // A `reflow` leaf carries a raw comment or a multi-line text block; rewrite it
    // at the column it lands at.
    commentRewriter: (raw, col) =>
      raw.startsWith('"""') ? reindentTextBlock(raw) : rewriteComment(raw, col, raw.startsWith("//")),
  });
  // Safety net: the printer attaches comments at member/statement granularity.
  // If a comment sat somewhere it does not yet handle, refuse rather than
  // silently drop it - the CLI then leaves the file untouched.
  if (!p.allCommentsEmitted()) {
    throw new UnsupportedSyntaxError("comment in an unsupported position");
  }
  // Exactly one trailing newline, like google-java-format.
  return text.replace(/\n*$/, "") + "\n";
}

// The canonical JLS modifier order google-java-format reorders to.
const MODIFIER_ORDER: SyntaxKind[] = [
  SyntaxKind.PublicKeyword,
  SyntaxKind.ProtectedKeyword,
  SyntaxKind.PrivateKeyword,
  SyntaxKind.AbstractKeyword,
  SyntaxKind.DefaultKeyword,
  SyntaxKind.StaticKeyword,
  SyntaxKind.FinalKeyword,
  SyntaxKind.TransientKeyword,
  SyntaxKind.VolatileKeyword,
  SyntaxKind.SynchronizedKeyword,
  SyntaxKind.NativeKeyword,
  SyntaxKind.StrictfpKeyword,
];

class Printer {
  private readonly text: string;
  private readonly comments: Comment[];
  /** Index of the next not-yet-emitted comment in `comments`. */
  private ci = 0;
  // The indent multiplier (1 google / 2 aosp). Most layout defers the multiplier
  // to print time, but a few gjf decisions (e.g. the method-chain "small
  // receiver" threshold) depend on it at build time.
  // Simple names imported as a well-known nullness type annotation (e.g.
  // "Nullable" when `import org.jspecify.annotations.Nullable;` is present).
  private readonly typeAnnotationNames = new Set<string>();

  constructor(
    private readonly sf: SourceFile,
    private readonly mult: number,
  ) {
    this.text = sf.text;
    this.comments = collectComments(sf.text);
    for (const imp of sf.imports) {
      if (imp.isStatic) continue;
      const fqn = this.entityName(imp.name);
      if (TYPE_ANNOTATION_FQNS.has(fqn)) {
        this.typeAnnotationNames.add(fqn.slice(fqn.lastIndexOf(".") + 1));
      }
    }
  }

  /** Whether `a` is a well-known type-use annotation imported in this file. */
  private isTypeAnnotation(a: Annotation): boolean {
    const n = a.typeName;
    const simple =
      n.kind === SyntaxKind.Identifier ? this.raw(n) : this.raw((n as { right: Node }).right);
    return this.typeAnnotationNames.has(simple);
  }

  /** The exact source spelling of a leaf node (identifier, literal, ...). */
  private raw(node: Node): string {
    return this.text.slice(skipTrivia(this.text, node.pos), node.end);
  }

  /** The offset where a node's token text actually begins (past leading trivia). */
  private start(node: Node): number {
    return skipTrivia(this.text, node.pos);
  }

  /** Whether >= 2 newlines separate `from` from `pos` (a blank line in source). */
  private blankBeforePos(from: number, pos: number): boolean {
    return (this.text.slice(from, pos).match(/\n/g)?.length ?? 0) >= 2;
  }

  // The separator after an opening `{`, before the first body entry.
  // google-java-format preserves one source blank line here, so emit two
  // hardlines when the source left a blank between the brace and the first
  // rendered thing (a leading comment if present, else the entry). `bracePos`
  // is the offset just after `{` (a node's raw `.pos`, before its trivia);
  // `firstItemStart` is the first entry's trivia-skipped start.
  private braceLead(bracePos: number, firstItemStart: number): Doc {
    const firstContent = this.hasCommentBefore(firstItemStart)
      ? this.comments[this.ci].pos
      : firstItemStart;
    return this.blankBeforePos(bracePos, firstContent) ? concat([hardline, hardline]) : hardline;
  }

  /** Whether a pending comment begins before `pos` (without consuming it). */
  private hasCommentBefore(pos: number): boolean {
    return this.ci < this.comments.length && this.comments[this.ci].pos < pos;
  }

  /** Consume and return every pending comment whose text begins before `pos`. */
  private commentsBefore(pos: number): Comment[] {
    const out: Comment[] = [];
    while (this.ci < this.comments.length && this.comments[this.ci].pos < pos) {
      out.push(this.comments[this.ci++]);
    }
    return out;
  }

  /** Whether every collected comment was emitted (else we would lose one). */
  allCommentsEmitted(): boolean {
    return this.ci >= this.comments.length;
  }

  /**
   * Render a member/statement list with its comments. Returns the inner docs
   * already interleaved with hardline/blank separators; the caller supplies the
   * leading hardline and the surrounding braces. `forced` applies the
   * blank-line-around-methods rule (members only). `endPos` bounds the trailing
   * "dangling" comments that sit before the closing brace.
   */
  private listDocs(list: readonly Node[], forced: boolean, endPos: number): Doc[] {
    const out: Doc[] = [];
    let first = true;
    let prevEnd = list.length > 0 ? this.start(list[0]) : endPos;

    const push = (doc: Doc, blankBefore: boolean) => {
      if (!first) out.push(blankBefore ? concat([hardline, hardline]) : hardline);
      out.push(doc);
      first = false;
    };

    list.forEach((item, i) => {
      const itemStart = this.start(item);
      // The blank line required before this whole entry (its leading comments
      // and the item) - g-j-f puts it before a method's doc comment, not between.
      // Measure the source blank from the previous entry's end to the first thing
      // here (a leading comment or the item) so comment lines are not miscounted.
      const leadComments = this.commentsBefore(itemStart);
      const firstPos = leadComments.length > 0 ? leadComments[0].pos : itemStart;
      const entryBlank =
        i > 0 &&
        (this.blankBeforePos(prevEnd, firstPos) || (forced && forcedBlank(list[i - 1], item)));
      let pushedInEntry = false;
      const pushEntry = (doc: Doc, srcBlank: boolean) => {
        push(doc, pushedInEntry ? srcBlank : entryBlank || srcBlank);
        pushedInEntry = true;
      };

      // A block comment on the same line as the item attaches inline before it
      // (`/* package */ final int x;`); the rest are own-line leading comments.
      let inlineLead: Comment | undefined;
      const lastLead = leadComments[leadComments.length - 1];
      if (
        lastLead &&
        !lastLead.line &&
        !lastLead.text.includes("\n") && // a multi-line comment/javadoc stays own-line
        !this.text.slice(lastLead.end, itemStart).includes("\n")
      ) {
        inlineLead = leadComments.pop();
      }

      for (const c of leadComments) {
        if (!c.ownLine && !pushedInEntry && i > 0) {
          // A comment after code on the same line: attach to the previous entry.
          out[out.length - 1] = concat([out[out.length - 1], " ", c.text]);
        } else {
          // Own-line comment: reflow it at the column it is written at.
          pushEntry(reflow(c.text), this.blankBeforePos(prevEnd, c.pos));
        }
        prevEnd = c.end;
      }

      // gjf preserves one source blank line between a leading own-line comment
      // and the item it precedes (a "section header" comment set off from its
      // member). Only when own-line comments were already pushed for this entry.
      const afterComments = prevEnd;
      let itemDoc = this.node(item);
      if (inlineLead) itemDoc = concat([reflow(inlineLead.text), " ", itemDoc]);
      const trailing = this.trailingCommentAfter(item);
      if (trailing) {
        itemDoc = concat([itemDoc, " ", trailing.text]);
        prevEnd = trailing.end;
      } else {
        prevEnd = item.end;
      }
      const itemBlank =
        pushedInEntry && !inlineLead && this.blankBeforePos(afterComments, itemStart);
      pushEntry(itemDoc, itemBlank);
    });

    for (const c of this.commentsBefore(endPos)) {
      push(reflow(c.text), this.blankBeforePos(prevEnd, c.pos));
      prevEnd = c.end;
    }
    return out;
  }

  /** A comment immediately after `node` on the same source line, if any. */
  private trailingCommentAfter(node: Node): Comment | undefined {
    const c = this.comments[this.ci];
    if (!c || c.ownLine || c.pos < node.end) return undefined;
    // Same line: no newline between the node's end and the comment.
    if (this.text.slice(node.end, c.pos).includes("\n")) return undefined;
    this.ci++;
    return c;
  }

  sourceFile(sf: SourceFile): Doc {
    // Blocks are separated by a blank line: an optional file-leading comment
    // (a license header), package, static imports, non-static imports, then the
    // type declarations (members separated among themselves).
    const blocks: Doc[] = [];
    const firstStart = this.firstConstructStart(sf);
    const header = this.commentsBefore(firstStart);
    if (sf.packageDeclaration) {
      blocks.push(concat(["package ", this.entityName(sf.packageDeclaration.name), ";"]));
    }
    const statics = sf.imports.filter(i => i.isStatic);
    const nonStatics = sf.imports.filter(i => !i.isStatic);
    for (const g of [statics, nonStatics]) {
      if (g.length > 0) blocks.push(this.importGroup(g));
    }
    if (sf.moduleDeclaration) {
      blocks.push(this.moduleDeclaration(sf.moduleDeclaration));
    }
    if (sf.statements.length > 0) {
      blocks.push(concat(this.listDocs(sf.statements, true, this.text.length)));
    }
    if (header.length > 0) {
      // Preserve a source blank line between consecutive leading comments (e.g. a
      // license header and the package/type javadoc in a package-info file).
      const headerParts: Doc[] = [];
      header.forEach((c, i) => {
        if (i > 0) {
          headerParts.push(
            this.blankBeforePos(header[i - 1].end, c.pos) ? concat([hardline, hardline]) : hardline,
          );
        }
        headerParts.push(reflow(c.text));
      });
      const headerDoc = concat(headerParts);
      // A leading comment glued to the first construct (no blank line in source)
      // is its doc comment - keep it attached. One followed by a blank line is a
      // file header (e.g. a license), separated by a blank line like other blocks.
      const glued =
        blocks.length > 0 && !this.blankBeforePos(header[header.length - 1].end, firstStart);
      if (glued) {
        blocks[0] = concat([headerDoc, hardline, blocks[0]]);
      } else {
        blocks.unshift(headerDoc);
      }
    }
    return join(concat([hardline, hardline]), blocks);
  }

  /** Offset of the first real construct (package, import, type or module). */
  private firstConstructStart(sf: SourceFile): number {
    const candidates: number[] = [];
    if (sf.packageDeclaration) candidates.push(this.start(sf.packageDeclaration));
    if (sf.imports.length > 0) candidates.push(this.start(sf.imports[0]));
    if (sf.statements.length > 0) candidates.push(this.start(sf.statements[0]));
    if (sf.moduleDeclaration) candidates.push(this.start(sf.moduleDeclaration));
    return candidates.length > 0 ? Math.min(...candidates) : this.text.length;
  }

  // module-info.java (SE9). Directives are grouped by kind with a blank line on
  // each kind change; the `to`/`with` module lists always break, one name per
  // line at a continuation indent. Mirrors google-java-format's module layout.
  private moduleDeclaration(m: ModuleDeclaration): Doc {
    const head: Doc[] = [];
    for (const a of m.annotations ?? []) head.push(this.annotation(a), hardline);
    if (m.isOpen) head.push("open ");
    head.push("module ", this.entityName(m.name), " ");
    if (m.directives.length === 0) return concat([...head, "{}"]);
    const body: Doc[] = [];
    m.directives.forEach((d, i) => {
      if (i > 0) {
        const kindChanged = d.kind !== m.directives[i - 1].kind;
        body.push(kindChanged ? concat([hardline, hardline]) : hardline);
      }
      body.push(this.directive(d));
    });
    return concat([...head, "{", indent(concat([hardline, ...body])), hardline, "}"]);
  }

  private directive(d: Node): Doc {
    switch (d.kind) {
      case SyntaxKind.RequiresDirective: {
        const r = d as RequiresDirective;
        const mods = `${r.isTransitive ? "transitive " : ""}${r.isStatic ? "static " : ""}`;
        return concat(["requires ", mods, this.entityName(r.name), ";"]);
      }
      case SyntaxKind.ExportsDirective: {
        const e = d as ExportsDirective;
        return this.exportsLike("exports", e.packageName, e.toModules);
      }
      case SyntaxKind.OpensDirective: {
        const o = d as OpensDirective;
        return this.exportsLike("opens", o.packageName, o.toModules);
      }
      case SyntaxKind.UsesDirective:
        return concat(["uses ", this.entityName((d as UsesDirective).typeName), ";"]);
      case SyntaxKind.ProvidesDirective: {
        const p = d as ProvidesDirective;
        return concat([
          "provides ",
          this.entityName(p.typeName),
          " with",
          this.moduleNameList(p.withTypes),
          ";",
        ]);
      }
      default:
        return this.raw(d);
    }
  }

  private exportsLike(
    keyword: string,
    pkg: EntityName,
    toModules: NodeArray<EntityName> | undefined,
  ): Doc {
    if (!toModules || toModules.length === 0) {
      return concat([keyword, " ", this.entityName(pkg), ";"]);
    }
    return concat([keyword, " ", this.entityName(pkg), " to", this.moduleNameList(toModules), ";"]);
  }

  /** A `to`/`with` module-name list: always broken, one name per continuation line. */
  private moduleNameList(names: NodeArray<EntityName>): Doc {
    const items = names.map(n => this.entityName(n));
    // g-j-f indents the continuation by two units (4 spaces google / 8 aosp).
    return indent(indent(concat([hardline, join(concat([",", hardline]), items)])));
  }

  private importGroup(imports: ImportDeclaration[]): Doc {
    // Sort keys are precomputed: entityName rebuilds the dotted name from the
    // source text, so keying per comparison would be O(n log n) rebuilds.
    const sorted = imports
      .map(imp => ({ key: this.entityName(imp.name), imp }))
      .sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0))
      .map(entry => entry.imp);
    const seen = new Set<string>();
    const lines: Doc[] = [];
    for (const imp of sorted) {
      const text = this.importLine(imp);
      if (seen.has(text)) continue; // dedupe identical imports
      seen.add(text);
      lines.push(text);
    }
    return join(hardline, lines);
  }

  private importLine(imp: ImportDeclaration): string {
    const name = this.entityName(imp.name);
    const onDemand = imp.isOnDemand ? ".*" : "";
    return `import ${imp.isStatic ? "static " : ""}${name}${onDemand};`;
  }

  /** Members of a type body, with comments and blank lines. */
  private members(list: NodeArray<Node>, endPos: number): Doc[] {
    return this.listDocs(list, true, endPos);
  }

  private entityName(name: EntityName): string {
    if (name.kind === SyntaxKind.Identifier) return this.raw(name);
    return `${this.entityName((name as { left: EntityName }).left)}.${this.raw((name as { right: Node }).right)}`;
  }

  // google-java-format's annotation placement:
  // - "own": each declaration annotation on its own line (methods, types,
  //   constructors, enum constants).
  // - "var": fields and locals - an annotation with arguments goes on its own
  //   line, a parameterless marker annotation stays inline.
  // - "inline": always on the same line (parameters, record components).
  private modifiers(
    mods: NodeArray<ModifierLike> | undefined,
    annoMode: "own" | "var" | "inline" = "inline",
  ): Doc {
    if (!mods || mods.length === 0) return "";
    // Peel a trailing run of well-known type-use annotations (`@Nullable` etc.):
    // gjf renders these inline right before the type, not on their own line. The
    // rest are declaration modifiers, placed as usual.
    let cut = mods.length;
    if (annoMode !== "inline") {
      while (cut > 0) {
        const m = mods[cut - 1];
        if (m.kind === SyntaxKind.Annotation && this.isTypeAnnotation(m as Annotation)) cut--;
        else break;
      }
    }
    const declMods = cut === mods.length ? mods : mods.slice(0, cut);
    const annotations = declMods.filter(m => m.kind === SyntaxKind.Annotation) as Annotation[];
    const keywords = declMods.filter(m => m.kind !== SyntaxKind.Annotation);
    keywords.sort((a, b) => rank(a.kind) - rank(b.kind));
    const parts: Doc[] = [];
    for (const a of annotations) {
      const ownLine =
        annoMode === "own" || (annoMode === "var" && a.args !== undefined && a.args.length > 0);
      parts.push(this.annotation(a));
      // A comment on the same line as an own-line annotation stays with it
      // (`@SuppressWarnings("x") // why`) instead of floating away.
      if (ownLine) {
        const tc = this.trailingCommentAfter(a);
        if (tc) parts.push(" ", tc.text);
      }
      parts.push(ownLine ? hardline : " ");
    }
    for (const k of keywords) parts.push(concat([this.modifierText(k), " "]));
    // Type-use annotation suffix, inline before the type.
    for (let i = cut; i < mods.length; i++) parts.push(this.annotation(mods[i] as Annotation), " ");
    return concat(parts);
  }

  private modifierText(k: ModifierLike): string {
    // The parser represents `non-sealed` as just the `non` identifier (the
    // `-sealed` is consumed but not kept in the AST); `non` is a modifier only
    // in that one context, so restore the full spelling here.
    if (k.kind === SyntaxKind.Identifier && this.raw(k) === "non") return "non-sealed";
    return tokenToString(k.kind) ?? this.raw(k);
  }

  private annotation(a: Annotation): Doc {
    const name = `@${this.entityName(a.typeName)}`;
    if (!a.args) return name; // no argument list in source
    if (a.args.length === 0) return `${name}()`; // explicit empty parens are kept
    const args = a.args.map(arg => {
      const argName = (arg as { name?: Node }).name;
      const value = this.node((arg as { value: Node }).value);
      return argName ? concat([this.raw(argName), " = ", value]) : value;
    });
    // gjf forces an annotation's element-value pairs one-per-line when there is
    // more than one and any pair is array-valued (`name = {..}`), even if they
    // would fit; otherwise they fill (one per line only on overflow).
    const hasArrayValue =
      a.args.length > 1 &&
      a.args.some(arg => (arg as { value?: Node }).value?.kind === SyntaxKind.ArrayInitializer);
    const fillMode: FillMode = hasArrayValue
      ? "forced"
      : this.allShortItems([...a.args])
        ? "independent"
        : "unified";
    // Annotation arguments wrap like a call's: break after `(` at +4 and lay
    // one element-value pair per line (fill only when every arg is short).
    return concat([name, this.argsLike("(", args, ")", fillMode)]);
  }

  /** A run of annotations, each followed by a space (inline, e.g. on a component). */
  private annotations(anns: NodeArray<Annotation> | undefined): Doc {
    if (!anns || anns.length === 0) return "";
    return concat(anns.map(a => concat([this.annotation(a), " "])));
  }

  private typeParameters(tps: NodeArray<TypeParameter> | undefined): Doc {
    if (!tps || tps.length === 0) return "";
    const params = tps.map(tp => {
      const name = this.raw(tp.name);
      if (!tp.constraint || tp.constraint.length === 0) return name as Doc;
      return concat([
        name,
        " extends ",
        join(
          " & ",
          tp.constraint.map(t => this.type(t)),
        ),
      ]);
    });
    return concat(["<", join(", ", params), ">"]);
  }

  private typeArguments(args: NodeArray<TypeNode | WildcardType> | undefined): Doc {
    if (!args) return "";
    if (args.length === 0) return "<>"; // diamond
    return concat([
      "<",
      join(
        ", ",
        args.map(t => this.type(t)),
      ),
      ">",
    ]);
  }

  private type(t: TypeNode | WildcardType): Doc {
    switch (t.kind) {
      case SyntaxKind.PrimitiveType: {
        const pt = t as PrimitiveType;
        const keyword = tokenToString(pt.keyword) ?? this.raw(t);
        // SE8 type-use annotations precede the type: `@Nullable int`.
        return concat([this.annotations(pt.annotations), keyword]);
      }
      case SyntaxKind.VarType:
        return "var";
      case SyntaxKind.ArrayType:
        return concat([this.type((t as ArrayType).elementType), "[]"]);
      case SyntaxKind.TypeReference: {
        const tr = t as TypeReference;
        return concat([
          this.annotations(tr.annotations),
          this.entityName(tr.typeName),
          this.typeArguments(tr.typeArguments),
        ]);
      }
      case SyntaxKind.WildcardType: {
        const w = t as WildcardType;
        if (w.hasExtends && w.type) return concat(["? extends ", this.type(w.type)]);
        if (w.hasSuper && w.type) return concat(["? super ", this.type(w.type)]);
        return "?";
      }
      default:
        return this.raw(t);
    }
  }

  // --- declarations --------------------------------------------------------

  private classLike(
    keyword: string,
    decl: {
      modifiers?: NodeArray<ModifierLike>;
      name: Node;
      typeParameters?: NodeArray<TypeParameter>;
      members: NodeArray<Node>;
      end: number;
    },
    tail: Doc[],
  ): Doc {
    const header = concat([
      this.modifiers(decl.modifiers, "own"),
      keyword,
      " ",
      this.raw(decl.name),
      this.typeParameters(decl.typeParameters),
      // extends/implements/permits live in one +4 level: each clause begins with
      // a fill break, so a long clause folds onto its own continuation line.
      level(PLUS4, tail),
      " ",
    ]);
    return concat([header, this.body(decl.members, decl.end)]);
  }

  // A gjf class-header type list (`implements A, B, C`): a fill break before the
  // keyword, then the keyword and the types. With more than one type the list
  // itself indents +4 and its commas break UNIFIED (one per line); a single type
  // stays attached.
  private typeListClause(keyword: string, types: TypeNode[]): Doc {
    if (types.length === 0) return "";
    const inner: Doc[] = [keyword, " "];
    types.forEach((t, i) => {
      if (i > 0) inner.push(",", brk("unified", " ", ZERO));
      inner.push(this.type(t));
    });
    return concat([brk("independent", " ", ZERO), level(types.length > 1 ? PLUS4 : ZERO, inner)]);
  }

  /** A brace-delimited member body: `{` ... `}` or `{}` when empty. `endPos` is
   * the offset just past the closing brace, bounding trailing comments. */
  private body(members: NodeArray<Node>, endPos: number): Doc {
    if (members.length === 0 && !this.hasCommentBefore(endPos)) return "{}";
    const lead =
      members.length > 0 ? this.braceLead(members[0].pos, this.start(members[0])) : hardline;
    return concat(["{", indent(concat([lead, ...this.members(members, endPos)])), hardline, "}"]);
  }

  private classDeclaration(d: ClassDeclaration): Doc {
    const tail: Doc[] = [];
    // A class's `extends` is a single supertype (no list).
    if (d.extendsType)
      tail.push(concat([brk("independent", " ", ZERO), "extends ", this.type(d.extendsType)]));
    if (d.implementsTypes) tail.push(this.typeListClause("implements", [...d.implementsTypes]));
    if (d.permitsTypes) tail.push(this.typeListClause("permits", [...d.permitsTypes]));
    return this.classLike("class", d, tail);
  }

  private interfaceDeclaration(d: InterfaceDeclaration): Doc {
    const tail: Doc[] = [];
    if (d.extendsTypes) tail.push(this.typeListClause("extends", [...d.extendsTypes]));
    if (d.permitsTypes) tail.push(this.typeListClause("permits", [...d.permitsTypes]));
    return this.classLike("interface", d, tail);
  }

  private enumDeclaration(d: EnumDeclaration): Doc {
    const tail: Doc[] = [];
    if (d.implementsTypes) tail.push(this.typeListClause("implements", [...d.implementsTypes]));
    const header = concat([
      this.modifiers(d.modifiers, "own"),
      "enum ",
      this.raw(d.name),
      level(PLUS4, tail),
      " ",
    ]);
    if (d.enumConstants.length === 0 && d.members.length === 0) return concat([header, "{}"]);
    // Leading blank after `{` (before any constant comment is consumed below).
    const lead =
      d.enumConstants.length > 0
        ? this.braceLead(d.enumConstants[0].pos, this.start(d.enumConstants[0]))
        : hardline;
    // google-java-format always lays enum constants one per line. A comment
    // before a constant stays attached to it (own-line, reflowed); a trailing
    // comment on the constant's line is kept after it.
    const constantParts: Doc[] = [];
    let prevConstEnd = -1;
    d.enumConstants.forEach((c, i) => {
      const lead = this.commentsBefore(this.start(c));
      const firstPos = lead.length > 0 ? lead[0].pos : this.start(c);
      if (i > 0) {
        // gjf preserves one source blank line between enum constants.
        const blank = this.blankBeforePos(prevConstEnd, firstPos);
        constantParts.push(",", blank ? concat([hardline, hardline]) : hardline);
      }
      for (const cm of lead) constantParts.push(reflow(cm.text), hardline);
      let cdoc = this.enumConstant(c);
      const trailing = this.trailingCommentAfter(c);
      if (trailing) {
        cdoc = concat([cdoc, " ", trailing.text]);
        prevConstEnd = trailing.end;
      } else {
        prevConstEnd = c.end;
      }
      constantParts.push(cdoc);
    });
    const constants = d.enumConstants;
    // A trailing comma and/or `;` after the last constant, preserved from source
    // (gjf keeps a trailing comma; `enum { A, B, }`).
    let semicolonAfter = false;
    if (constants.length > 0) {
      let p = skipTrivia(this.text, constants[constants.length - 1].end);
      if (this.text[p] === ",") {
        constantParts.push(",");
        p = skipTrivia(this.text, p + 1);
      }
      semicolonAfter = this.text[p] === ";";
    }
    const bodyParts: Doc[] = [lead, concat(constantParts)];
    if (d.members.length > 0) {
      // The constant list is `;`-terminated, then the members. A blank line
      // separates them only when there are constants above (a bare leading `;`
      // with no constants gets no blank line before the members) AND a real
      // member follows - a trailing empty statement (`;`) gets no blank line.
      const realMember = d.members.some(m => m.kind !== SyntaxKind.EmptyStatement);
      bodyParts.push(";", hardline);
      if (constants.length > 0 && realMember) bodyParts.push(hardline);
      bodyParts.push(...this.members(d.members, d.end));
    } else if (semicolonAfter) {
      bodyParts.push(";");
    }
    return concat([header, "{", indent(concat(bodyParts)), hardline, "}"]);
  }

  private enumConstant(c: EnumConstantDeclaration): Doc {
    const parts: Doc[] = [this.modifiers(c.modifiers, "own"), this.raw(c.name)];
    if (c.arguments)
      parts.push(
        "(",
        join(
          ", ",
          c.arguments.map(a => this.node(a)),
        ),
        ")",
      );
    if (c.classBody) parts.push(" ", this.body(c.classBody, c.end));
    return concat(parts);
  }

  private recordDeclaration(d: RecordDeclaration): Doc {
    const renderComp = (n: Node): Doc => {
      const rc = n as RecordComponent;
      return concat([
        this.annotations(rc.annotations),
        this.type(rc.type),
        rc.isVarArgs ? "... " : " ",
        this.raw(rc.name),
      ]);
    };
    const recordParens =
      d.recordComponents.length === 0
        ? "()"
        : this.argsLike("(", this.listItems(d.recordComponents, renderComp).items, ")", "unified");
    const after: Doc[] = [recordParens];
    // The `implements` clause folds onto its own +4 continuation line when the
    // record header overflows (gjf), same shape as a class header's clause.
    if (d.implementsTypes && d.implementsTypes.length > 0)
      after.push(level(PLUS4, [this.typeListClause("implements", [...d.implementsTypes])]));
    const header = concat([
      this.modifiers(d.modifiers, "own"),
      "record ",
      this.raw(d.name),
      this.typeParameters(d.typeParameters),
      concat(after),
      " ",
    ]);
    return concat([header, this.body(d.members, d.end)]);
  }

  private fieldDeclaration(d: FieldDeclaration): Doc {
    if (d.declarators.length === 1) {
      return concat([
        this.modifiers(d.modifiers, "var"),
        this.singleDeclaration(this.type(d.type), d.declarators[0], ";"),
      ]);
    }
    return concat([
      this.modifiers(d.modifiers, "var"),
      this.type(d.type),
      " ",
      join(
        ", ",
        d.declarators.map(v => this.declarator(v)),
      ),
      ";",
    ]);
  }

  // A single-declarator `<type> <name> = <init><trailing>` with the gjf
  // break-before-name option: when `<type> <name> =` does not fit, break after
  // the type (name onto a +4 line) so `<name> = <init>` can stay together; the
  // initializer keeps its own break-after-`=`, indented +4 (or +8 when the name
  // also broke). The break-before-name's fit check excludes the initializer
  // (it sits in a sibling level), so a long initializer alone does not trigger it.
  private singleDeclaration(type: Doc, v: VariableDeclarator, trailing: Doc): Doc {
    const name = concat([this.raw(v.name), "[]".repeat(v.arrayRankAfterName)]);
    // No initializer, or an array/hugging initializer that owns its own braces:
    // keep the simple shape (the declarator handles the `=`).
    if (!v.initializer || v.initializer.kind === SyntaxKind.ArrayInitializer) {
      return concat([type, " ", this.declarator(v, trailing)]);
    }
    const nameTag = new BreakTag();
    return level(ZERO, [
      level(PLUS4, [type, brk("unified", " ", ZERO, nameTag), name, " ="]),
      level(indentIf(nameTag, indentConst(8), PLUS4), [
        line,
        this.statementTail(v.initializer, trailing),
      ]),
    ]);
  }

  private declarator(v: VariableDeclarator, trailing: Doc = ""): Doc {
    const name = concat([this.raw(v.name), "[]".repeat(v.arrayRankAfterName)]);
    if (!v.initializer) return concat([name, trailing]);
    // An array initializer hugs the `=` (`x = {` ... `}`); its own braces break,
    // so do not insert a break before it. (Other hugging RHS kinds - lambdas,
    // anonymous classes - are handled with the rest of assignment RHS in phase B.)
    if (v.initializer.kind === SyntaxKind.ArrayInitializer) {
      return concat([name, " = ", this.node(v.initializer), trailing]);
    }
    // gjf folds a long initializer onto a +4 continuation line after `=`; the
    // statement's `;` rides into the initializer's tail call (rest-of-line).
    return concat([name, " =", level(PLUS4, [line, this.statementTail(v.initializer, trailing)])]);
  }

  // A gjf-style parenthesized comma list (`(a, b, c)`). When it does not fit, a
  // UNIFIED break fires after `(` (continuation at +4) so the items always start
  // on the next line; a nested zero-indent level then keeps them on one
  // continuation line if they fit. If they do not, the inter-item fill mode
  // decides: UNIFIED puts one per line, INDEPENDENT *fills* as many per line as
  // fit. The closing `)` stays attached to the last item's line.
  private argsLike(
    open: string,
    items: Doc[],
    close: string,
    fillMode: FillMode,
    trailing: Doc = "",
  ): Doc {
    const innerParts: Doc[] = [];
    items.forEach((it, i) => {
      if (i > 0) innerParts.push(",", brk(fillMode, " ", ZERO));
      innerParts.push(it);
    });
    // The close delimiter (and any token trailing it on the same line, e.g. a
    // method signature's `;` or a routed-in separator) goes INSIDE the args level
    // so the args' own one-per-line fit check counts it: gjf breaks a delimited
    // list when the whole `(...)<close><trailing>` run overflows, not just the
    // bare args.
    innerParts.push(close);
    if (trailing !== "") innerParts.push(trailing);
    const inner = level(ZERO, innerParts);
    return concat([open, level(PLUS4, [brk("unified", "", ZERO), inner])]);
  }

  // gjf fills a delimited list (packs items per line) only when every item is
  // "short" - its source text is under MAX_ITEM_LENGTH_FOR_FILLING (10) chars;
  // otherwise items go one per line.
  private allShortItems(nodes: readonly Node[]): boolean {
    return nodes.every(n => n.end - this.start(n) < 10);
  }

  // gjf lays a delimited list one item per line (UNIFIED) when any item carries
  // a comment, else fills (INDEPENDENT) only when every item is short.
  private fillMode(anyComment: boolean, nodes: readonly Node[]): FillMode {
    return !anyComment && this.allShortItems(nodes) ? "independent" : "unified";
  }

  // Attach a same-line trailing block comment (`item /* note */`) after an item,
  // if the next pending comment is one. A line comment is left to the statement
  // boundary (it would comment out the following separator). Returns whether a
  // comment was consumed.
  private attachTrailingBlockComment(parts: Doc[], endPos: number): boolean {
    const t = this.comments[this.ci];
    if (
      t &&
      !t.line &&
      !t.ownLine &&
      t.pos >= endPos &&
      // The comment must immediately trail this item - stop at a separator or a
      // closing delimiter, so a comment past `)`/`,`/`:` is not mis-attached.
      !/[\n,):]/.test(this.text.slice(endPos, t.pos))
    ) {
      this.ci++;
      parts.push(" ", t.text);
      return true;
    }
    return false;
  }

  // Render a delimited-list item with the comments attached to it: leading
  // comments before the item (own-line ones get a forced break after, which also
  // forces the whole list to break; an inline block comment stays inline), and a
  // trailing block comment on the item's line before the separator. Returns
  // whether any comment was consumed (callers disable filling then, like gjf).
  private itemWithComments(
    node: Node,
    render: () => Doc,
    extractLeadTrailing = false,
  ): { doc: Doc; comment: boolean; leadTrailing?: string; leadStart: number } {
    const parts: Doc[] = [];
    let comment = false;
    let leadTrailing: string | undefined;
    // Where this item's rendered content begins (its first comment, else the
    // item) - used by callers to measure a source blank line before it.
    const firstPending = this.comments[this.ci];
    const leadStart =
      firstPending && firstPending.pos < this.start(node) ? firstPending.pos : this.start(node);
    this.commentsBefore(this.start(node)).forEach((c, i) => {
      comment = true;
      // A line comment on the same line as the previous element (`elem, // note`)
      // trails THAT element; the caller emits it before the inter-item break.
      if (extractLeadTrailing && i === 0 && c.line && !c.ownLine) {
        leadTrailing = c.text;
      } else if (c.ownLine) parts.push(reflow(c.text), hardline);
      else if (c.line) parts.push(c.text, hardline);
      else parts.push(reformatParamComment(c.text) ?? c.text, " ");
    });
    parts.push(render());
    if (this.attachTrailingBlockComment(parts, node.end)) comment = true;
    return { doc: parts.length === 1 ? parts[0] : concat(parts), comment, leadTrailing, leadStart };
  }

  // Build the items of a delimited list with their comments consumed, reporting
  // whether any item carried a comment. `leads[i]` is a line comment that trails
  // the PREVIOUS element on its line (`extractLeadTrailing`), for the caller to
  // place before the separator break.
  private listItems(
    nodes: readonly Node[],
    render: (n: Node) => Doc,
    extractLeadTrailing = false,
  ): { items: Doc[]; leads: (string | undefined)[]; leadStarts: number[]; anyComment: boolean } {
    let anyComment = false;
    const items: Doc[] = [];
    const leads: (string | undefined)[] = [];
    const leadStarts: number[] = [];
    nodes.forEach(n => {
      const r = this.itemWithComments(n, () => render(n), extractLeadTrailing);
      if (r.comment) anyComment = true;
      items.push(r.doc);
      leads.push(r.leadTrailing);
      leadStarts.push(r.leadStart);
    });
    return { items, leads, leadStarts, anyComment };
  }

  private parameters(params: NodeArray<Parameter>, trailing: Doc = ""): Doc {
    // Even with no parameters the trailing run (a `throws` clause + brace) may
    // carry a break, so it must sit in a +4 level to fold and indent correctly.
    if (params.length === 0) return level(PLUS4, ["()", trailing]);
    // Parameters are never filled (gjf uses a UNIFIED inter-parameter break).
    const { items } = this.listItems(params, p => this.parameter(p as Parameter));
    return this.argsLike("(", items, ")", "unified", trailing);
  }

  // The `(`, break-before-args, param list and `)` as flat children, for the
  // throws case where they must share the signature level with the throws break
  // (gjf's open(ZERO) around visitFormals + visitThrowsClause). The break before
  // the args is INDEPENDENT so the params stay inline when they fit and break to
  // the +4 line only when they overflow.
  private paramListChildren(params: NodeArray<Parameter>): Doc[] {
    if (params.length === 0) return ["()"];
    const { items } = this.listItems(params, p => this.parameter(p as Parameter));
    const innerParts: Doc[] = [];
    items.forEach((it, i) => {
      if (i > 0) innerParts.push(",", brk("unified", " ", ZERO));
      innerParts.push(it);
    });
    return ["(", brk("independent", "", ZERO), level(ZERO, innerParts), ")"];
  }

  private parameter(p: Parameter): Doc {
    const parts: Doc[] = [this.modifiers(p.modifiers), this.type(p.type)];
    if (p.isVarArgs) parts.push("...");
    if (p.name) parts.push(" ", this.raw(p.name));
    return concat(parts);
  }

  private methodLike(decl: {
    modifiers?: NodeArray<ModifierLike>;
    typeParameters?: NodeArray<TypeParameter>;
    returnType?: TypeNode;
    name: Node;
    parameters: NodeArray<Parameter>;
    throws?: NodeArray<TypeNode>;
    body?: Block;
  }): Doc {
    const tp = this.typeParameters(decl.typeParameters);
    const head: Doc[] = [this.modifiers(decl.modifiers, "own")];
    if (tp !== "") head.push(tp, " ");
    if (decl.returnType) head.push(this.type(decl.returnType), " ");
    const hasThrows = decl.throws !== undefined && decl.throws.length > 0;
    // The token trailing the parameter list on the same line (`;`, ` {}`, or
    // ` {`) and any `throws` clause go *inside* the param level so the whole
    // signature wraps as a unit when it overflows (gjf's rest-of-line rule): the
    // `throws` break is UNIFIED with the param-open break, so when the params go
    // one-per-line the `throws` clause and the brace fold onto their own lines.
    const emptyBody = decl.body !== undefined && this.blockIsEmpty(decl.body);
    const bodyToken = !decl.body ? ";" : emptyBody ? " {}" : " {";
    let sig: Doc;
    if (hasThrows) {
      const throwsParts: Doc[] = ["throws "];
      decl.throws!.forEach((t, i) => {
        if (i > 0) throwsParts.push(",", brk("unified", " ", ZERO));
        throwsParts.push(this.type(t));
      });
      // Throws-type continuation indents +4 beyond the `throws` line, which is
      // itself on sig's +4 continuation -> +8 from the method (col 10).
      const throwsClause = level(decl.throws!.length > 1 ? indentConst(4) : ZERO, throwsParts);
      // Mirror gjf's visitMethodDeclaration: `(`, a break-before-args, the param
      // list, `)`, the `throws` break, the throws clause, and the body token are
      // all direct children of ONE +4 level (the indent rides the level, breaks
      // sit at ZERO so they land at +4). Both breaks are INDEPENDENT (gjf's
      // breakToFill): params break to their own line only when they overflow, and
      // `) throws X {` GLUES whenever it fits after the params' rendered end
      // column. When the params explode one-per-line, the param split's flat
      // width overflows the +4 line, propagating mustBreak so the throws clause
      // also breaks - matching gjf with no engine change.
      sig = level(PLUS4, [
        ...this.paramListChildren(decl.parameters),
        brk("independent", " ", ZERO),
        throwsClause,
        bodyToken,
      ]);
    } else {
      // No throws clause: the body-open token rides inside the param level so the
      // list wraps when the whole `(...)<token>` run overflows (rest-of-line).
      sig = this.parameters(decl.parameters, bodyToken);
    }
    head.push(this.raw(decl.name), sig);
    // Emit the rest of the block when there is a real body, else the signature
    // (with its trailing `;`/` {}`) is complete.
    if (!decl.body || emptyBody) return concat(head);
    return concat([...head, this.blockRest(decl.body)]);
  }

  private initializerBlock(d: InitializerBlock): Doc {
    return concat([d.isStatic ? "static " : "", this.block(d.body)]);
  }

  // --- statements ----------------------------------------------------------

  private blockIsEmpty(b: Block): boolean {
    if (b.statements.length > 0) return false;
    // Only a comment *inside* the block (after its `{`) makes it non-empty; a
    // pending comment before the block (e.g. an unconsumed parameter comment)
    // must not be miscounted - blockIsEmpty can be queried before those are
    // consumed (methodLike computes the body shape before rendering params).
    return !(this.hasCommentBefore(b.end) && this.comments[this.ci].pos > this.start(b));
  }

  private block(b: Block, allowTrailingBlank = false): Doc {
    if (this.blockIsEmpty(b)) return "{}";
    return concat(["{", this.blockRest(b, allowTrailingBlank)]);
  }

  /** A block's body after the opening `{` (the `{` is emitted by the caller, so
   * it can be placed inside another level to count toward a wrap decision).
   * `allowTrailingBlank` preserves a source blank line before the closing `}`,
   * which gjf does only when a clause follows (`} else`, `} catch`, `} finally`). */
  private blockRest(b: Block, allowTrailingBlank = false): Doc {
    // A comment on the same source line as the opening `{` stays on that line
    // (gjf): `if (...) { // note`. It must be emitted before the indented body so
    // it rides the brace line, and consumed here so listDocs does not re-emit it
    // own-line.
    const braceComment =
      b.statements.length > 0 ? this.braceTrailingComment(b.statements[0].pos) : "";
    const lead =
      b.statements.length > 0
        ? this.braceLead(b.statements[0].pos, this.start(b.statements[0]))
        : hardline;
    const closeLead =
      allowTrailingBlank &&
      b.statements.length > 0 &&
      this.blankBeforePos(b.statements[b.statements.length - 1].end, b.end - 1)
        ? concat([hardline, hardline])
        : hardline;
    return concat([
      braceComment,
      indent(concat([lead, ...this.statementList(b.statements, b.end)])),
      closeLead,
      "}",
    ]);
  }

  // Consume and return a comment that trails the opening `{` on its own line
  // (` // note`), or "" when the next pending comment starts on a later line.
  // `afterBrace` is the offset just past the `{` (the body's leading-trivia start).
  private braceTrailingComment(afterBrace: number): Doc {
    const c = this.comments[this.ci];
    if (!c || c.pos < afterBrace || /\n/.test(this.text.slice(afterBrace, c.pos))) return "";
    this.ci++;
    return concat([" ", c.text]);
  }

  private statementList(list: NodeArray<Statement>, endPos: number): Doc[] {
    return this.listDocs(list, false, endPos);
  }

  private localVar(d: LocalVariableDeclarationStatement): Doc {
    if (d.declarators.length === 1) {
      return concat([
        this.modifiers(d.modifiers, "var"),
        this.singleDeclaration(this.type(d.type), d.declarators[0], ";"),
      ]);
    }
    const last = d.declarators.length - 1;
    const parts: Doc[] = [this.modifiers(d.modifiers, "var"), this.type(d.type), " "];
    d.declarators.forEach((v, i) => {
      if (i > 0) parts.push(", ");
      // The terminating `;` rides into the last declarator's initializer.
      parts.push(this.declarator(v, i === last ? ";" : ""));
    });
    return concat(parts);
  }

  private ifStatement(s: IfStatement): Doc {
    const parts: Doc[] = [
      group(concat(["if (", this.statementTail(s.condition, ")")])),
      // gjf preserves a source blank line before the then-block's `}` when an
      // `else` follows.
      this.clauseBody(s.thenStatement, s.elseStatement !== undefined),
    ];
    if (s.elseStatement) {
      const elseOnSameLine = s.thenStatement.kind === SyntaxKind.Block;
      parts.push(elseOnSameLine ? " else" : concat([hardline, "else"]));
      if (s.elseStatement.kind === SyntaxKind.IfStatement) {
        parts.push(" ", this.node(s.elseStatement));
      } else {
        parts.push(this.clauseBody(s.elseStatement));
      }
    }
    return concat(parts);
  }

  /**
   * The controlled statement of if/for/while, with its leading separator. A
   * block follows after a space; a single statement stays on the same line when
   * it fits (`if (c) break;`) and otherwise breaks onto an indented line.
   */
  private clauseBody(s: Statement, allowTrailingBlank = false): Doc {
    if (s.kind === SyntaxKind.Block) {
      return concat([" ", this.block(s as Block, allowTrailingBlank)]);
    }
    return group(indent(concat([line, this.node(s)])));
  }

  private whileStatement(s: WhileStatement): Doc {
    return concat([
      group(concat(["while (", this.statementTail(s.condition, ")")])),
      this.clauseBody(s.statement),
    ]);
  }

  private doStatement(s: DoStatement): Doc {
    // `do` always takes a block in practice; keep the body adjacent either way.
    const body =
      s.statement.kind === SyntaxKind.Block
        ? concat([" ", this.block(s.statement as Block)])
        : this.clauseBody(s.statement);
    return concat(["do", body, " while (", this.node(s.condition), ");"]);
  }

  private forStatement(s: ForStatement): Doc {
    const init = s.initializer
      ? this.forInit(s.initializer)
      : s.initializerExpressions
        ? join(
            ", ",
            s.initializerExpressions.map(e => this.node(e)),
          )
        : "";
    const cond = s.condition ? this.node(s.condition) : "";
    const upd = s.incrementors
      ? join(
          ", ",
          s.incrementors.map(e => this.node(e)),
        )
      : "";
    const header = group(concat(["for (", init, "; ", cond, "; ", upd, ")"]));
    return concat([header, this.clauseBody(s.statement)]);
  }

  private forInit(init: Node): Doc {
    // A local variable declaration used as a for-init has no trailing `;`.
    if (init.kind === SyntaxKind.LocalVariableDeclarationStatement) {
      const d = init as LocalVariableDeclarationStatement;
      return concat([
        this.modifiers(d.modifiers),
        this.type(d.type),
        " ",
        join(
          ", ",
          d.declarators.map(v => this.declarator(v)),
        ),
      ]);
    }
    return this.node(init);
  }

  private forEachStatement(s: ForEachStatement): Doc {
    // gjf visitEnhancedForLoop: "for (" open(+4) param " :" breakOp(" ") expr
    // close ")". The iterable breaks after the ":" at +4 when it overflows.
    return concat([
      concat([
        "for (",
        level(PLUS4, [this.parameter(s.parameter), " :", line, this.node(s.expression)]),
        ")",
      ]),
      this.clauseBody(s.statement),
    ]);
  }

  private tryStatement(s: TryStatement): Doc {
    const parts: Doc[] = ["try"];
    if (s.resources && s.resources.length > 0) {
      // The first resource stays on the `try (` line; subsequent ones break
      // before themselves at +4 (one per line), each `;`-terminated. A trailing
      // `;` after the last resource in source is preserved as `; )`.
      const last = s.resources[s.resources.length - 1];
      const trailingSemi = this.text[skipTrivia(this.text, last.end)] === ";";
      const close = trailingSemi ? "; )" : ")";
      if (s.resources.length === 1) {
        // A single resource stays on the `try (` line; its own initializer level
        // supplies the +4 continuation indent, so no extra resource-list level
        // (which would double-indent the broken initializer to +8).
        parts.push(" (", this.resource(s.resources[0]), close);
      } else {
        const inner: Doc[] = [];
        s.resources.forEach((r, i) => {
          if (i > 0) inner.push(";", brk("unified", " ", ZERO));
          inner.push(this.resource(r));
        });
        parts.push(" (", level(PLUS4, inner), close);
      }
    }
    // gjf preserves a source blank line before a block's `}` when another clause
    // (catch/finally) follows.
    const hasFinally = s.finallyBlock !== undefined;
    parts.push(" ", this.block(s.tryBlock, s.catchClauses.length > 0 || hasFinally));
    s.catchClauses.forEach((c, i) => {
      parts.push(
        " catch (",
        this.modifiers(c.modifiers, "inline"),
        join(
          " | ",
          c.catchTypes.map(t => this.type(t)),
        ),
        " ",
        this.raw(c.name),
        ") ",
        this.block(c.block, i < s.catchClauses.length - 1 || hasFinally),
      );
    });
    if (s.finallyBlock) parts.push(" finally ", this.block(s.finallyBlock));
    return concat(parts);
  }

  private resource(r: Resource): Doc {
    if (r.expression) return this.node(r.expression);
    const head = concat([
      this.modifiers(r.modifiers),
      r.type ? concat([this.type(r.type), " "]) : "",
      r.name ? this.raw(r.name) : "",
    ]);
    if (!r.initializer) return head;
    // Like a variable declarator, a long initializer folds onto a +4
    // continuation line after `=` (gjf), rather than breaking the RHS in place.
    if (r.initializer.kind === SyntaxKind.ArrayInitializer) {
      return concat([head, " = ", this.node(r.initializer)]);
    }
    return concat([head, " =", level(PLUS4, [line, this.node(r.initializer)])]);
  }

  private switchLike(expr: Expression, clauses: NodeArray<SwitchClause>, endPos: number): Doc {
    // Comments before a `case`/`default` label sit on their own line at the
    // clause indent (gjf), so consume them per clause like a member list does.
    // Entries (leading comments + each clause) are separated by one hardline,
    // but a single source blank line between clauses is preserved (gjf), so the
    // separator before a clause becomes a double hardline when the source left a
    // blank between the previous clause and this one's first rendered thing.
    const entries: { doc: Doc; blank: boolean }[] = [];
    let prevEnd = -1;
    for (const c of clauses) {
      let comments = this.commentsBefore(this.start(c));
      // A line comment on the previous clause's line (`case 'a': // fall through`)
      // trails THAT clause, not the next - append it to the previous entry.
      if (comments.length > 0 && !comments[0].ownLine && entries.length > 0) {
        const prev = entries[entries.length - 1];
        prev.doc = concat([prev.doc, " ", comments[0].text]);
        comments = comments.slice(1);
      }
      const start = comments.length > 0 ? comments[0].pos : this.start(c);
      const blank = prevEnd >= 0 && this.blankBeforePos(prevEnd, start);
      let leading = blank;
      for (const cm of comments) {
        entries.push({ doc: reflow(cm.text), blank: leading });
        leading = false;
      }
      entries.push({ doc: this.switchClause(c), blank: leading });
      prevEnd = c.end;
    }
    for (const cm of this.commentsBefore(endPos))
      entries.push({ doc: reflow(cm.text), blank: false });
    const body: Doc[] = [];
    entries.forEach((e, i) => {
      if (i > 0) body.push(e.blank ? concat([hardline, hardline]) : hardline);
      body.push(e.doc);
    });
    return concat([
      group(concat(["switch (", this.node(expr), ")"])),
      " {",
      indent(concat([hardline, ...body])),
      hardline,
      "}",
    ]);
  }

  private switchClause(c: SwitchClause): Doc {
    // gjf wraps a case label in a level (arrow cases at +4) so multiple labels
    // break one-per-line and a long `when` guard folds before `when`, when the
    // label run overflows.
    // gjf wraps the whole case (labels, `when` guard, `->` and the arrow body) in
    // one level (arrow cases at +4): the labels break one-per-line (UNIFIED), the
    // guard folds before `when` (INDEPENDENT), and the body folds onto its own
    // continuation line - all together when the run overflows.
    const parts: Doc[] = c.isDefault
      ? ["default"]
      : ["case ", level(ZERO, this.caseLabelParts(c.labels ?? []))];
    if (c.guard) parts.push(brk("independent", " ", ZERO), "when ", this.node(c.guard));

    if (c.isArrow) {
      const stmts = c.statements;
      parts.push(" ->");
      if (stmts.length === 1 && stmts[0].kind === SyntaxKind.Block) {
        // A block body stays on the `->` line; it owns its own braces.
        return concat([level(PLUS4, parts), " ", this.block(stmts[0] as Block)]);
      }
      const bodyStart = this.start(stmts[0]);
      // A comment before the body sits own-line on the +4 continuation, forcing
      // the break so `case X ->` keeps only the label. Consume the comments
      // BEFORE rendering the body (else the body would absorb them).
      if (this.hasCommentBefore(bodyStart)) {
        const cparts: Doc[] = [];
        for (const c2 of this.commentsBefore(bodyStart)) cparts.push(reflow(c2.text), hardline);
        cparts.push(join(" ", stmts.map(s => this.node(s))));
        parts.push(hardline, concat(cparts));
      } else {
        parts.push(
          brk("unified", " ", ZERO),
          join(
            " ",
            stmts.map(s => this.node(s)),
          ),
        );
      }
      return level(PLUS4, parts);
    }
    parts.push(":");
    // A comment trailing the `case X:` / `default:` label on its line stays there
    // (`case 'a': // fall through`) rather than moving onto the next line.
    const bound = c.statements.length > 0 ? this.start(c.statements[0]) : c.end;
    const t = this.comments[this.ci];
    const head: Doc[] = [level(ZERO, parts)];
    if (t !== undefined && !t.ownLine && t.pos < bound) {
      this.ci++;
      head.push(" ", t.text);
    }
    // A fall-through case with no body is just its label; the switch body's clause
    // separator supplies the newline to the next clause.
    if (c.statements.length === 0) return concat(head);
    return concat([
      concat(head),
      indent(concat([hardline, ...this.statementList(c.statements, c.end)])),
    ]);
  }

  private caseLabelParts(labels: readonly Node[]): Doc[] {
    const parts: Doc[] = [];
    labels.forEach((l, i) => {
      if (i > 0) parts.push(",", brk("unified", " ", ZERO));
      parts.push(this.node(l));
    });
    return parts;
  }

  // --- expressions ---------------------------------------------------------

  // A binary operator chain. gjf collects all operands at the same precedence
  // into one +4 level and breaks *before* each operator; the breaks fill when
  // every operand is short, else go one per line.
  private binary(e: BinaryExpression, trailing: Doc = ""): Doc {
    const prec = precedence(e.operatorToken);
    const operands: Expression[] = [];
    const operators: string[] = [];
    this.walkInfix(prec, e, operands, operators);
    const fillMode = this.fillMode(false, operands);
    const parts: Doc[] = [this.node(operands[0])];
    operators.forEach((op, i) => {
      parts.push(brk(fillMode, " ", ZERO));
      // A comment before the next operand sits on its own line before the
      // operator (gjf), not inside the operand - so consume it here.
      for (const c of this.commentsBefore(this.start(operands[i + 1]))) {
        parts.push(c.line ? c.text : reflow(c.text), hardline);
      }
      parts.push(op, " ", this.node(operands[i + 1]));
    });
    // A statement's trailing `;` rides inside the +4 level (gjf counts it in the
    // level width), so `a && b;` breaks when the `;` is what tips it past 100.
    if (trailing !== "") parts.push(trailing);
    return level(PLUS4, parts);
  }

  // Flatten a left-associative chain of same-precedence binary operators into a
  // flat operand/operator list (a + b - c -> [a,b,c], [+,-]).
  private walkInfix(prec: number, node: Node, operands: Expression[], operators: string[]): void {
    if (
      node.kind === SyntaxKind.BinaryExpression &&
      precedence((node as BinaryExpression).operatorToken) === prec
    ) {
      const b = node as BinaryExpression;
      this.walkInfix(prec, b.left, operands, operators);
      operators.push(tokenToString(b.operatorToken) ?? "?");
      this.walkInfix(prec, b.right, operands, operators);
    } else {
      operands.push(node as Expression);
    }
  }

  // An assignment expression (`a = b`, `a += b`): the RHS folds onto a +4
  // continuation line after the operator when it does not fit.
  private assignment(e: AssignmentExpression): Doc {
    const op = tokenToString(e.operatorToken) ?? "=";
    return concat([this.node(e.left), " ", op, level(PLUS4, [line, this.node(e.right)])]);
  }

  // A dotted dereference chain (`a.b().c().d`). gjf flattens the `.`-spine and,
  // when there are at least two method invocations (a "builder" chain), breaks
  // before every dot at +4 (one selector per line). A chain with at most one
  // invocation stays glued (its argument lists wrap instead). The first dot does
  // not break when the receiver is tiny (<= indentMultiplier*4 chars).
  // ponytail: type-name prefixes (`ImmutableList.builder()...`) and stream
  // chains are not yet treated as units; those over-break. Add when a corpus
  // fixture needs them.
  // A comment sitting before a `.link` in a dereference chain (between the prior
  // selector and this one) stays before the dereference, own-line at the chain
  // indent (gjf), not pushed into the link's argument list. Consumes such
  // comments (those before the link's name) and renders them with forced breaks.
  private dotLinkLead(namePos: number): Doc {
    if (!this.hasCommentBefore(namePos)) return "";
    const parts: Doc[] = [];
    for (const c of this.commentsBefore(namePos)) {
      parts.push(c.line ? c.text : reflow(c.text), hardline);
    }
    return concat(parts);
  }

  private dotChain(root: Expression, trailing: Doc = ""): Doc {
    // Collect the chain's links without rendering them yet: a link's argument
    // list consumes comments, and comments must be consumed in source order
    // (left to right). Rendering eagerly here would consume the OUTER call's
    // args before the receiver's, mis-attributing a receiver-arg comment (e.g.
    // `new Pretty(/*writer*/ null, /*sourceOutput*/ true).operatorName(tag)`).
    const links: { isCall: boolean; name: string; render: () => Doc }[] = [];
    let cur: Node = root;
    let trailingRouted = false;
    for (;;) {
      if (
        cur.kind === SyntaxKind.CallExpression &&
        (cur as CallExpression).expression.kind === SyntaxKind.PropertyAccessExpression
      ) {
        const callExpr = cur as CallExpression;
        const pa = callExpr.expression as PropertyAccessExpression;
        // The rightmost link (the last in source order) carries the statement's
        // trailing token inside its argument list, so the call's args wrap when
        // the whole `...(...);` run overflows (rest-of-line rule).
        const rightmost = links.length === 0;
        const name = this.raw(pa.name);
        links.unshift({
          isCall: true,
          name,
          // Explicit method type arguments go between the dot and the name:
          // `obj.<String>foo(x)`, not `obj.foo<String>(x)`.
          render: () =>
            concat([
              this.dotLinkLead(this.start(pa.name)),
              ".",
              this.typeArguments(callExpr.typeArguments),
              name,
              this.argList(callExpr.arguments, rightmost ? trailing : ""),
            ]),
        });
        if (rightmost && trailing !== "") trailingRouted = true;
        cur = pa.expression;
      } else if (cur.kind === SyntaxKind.PropertyAccessExpression) {
        const pa = cur as PropertyAccessExpression;
        const name = this.raw(pa.name);
        links.unshift({
          isCall: false,
          name,
          render: () => concat([this.dotLinkLead(this.start(pa.name)), ".", name]),
        });
        cur = pa.expression;
      } else {
        break;
      }
    }
    // Render the base (leftmost receiver) first, then each link in source order,
    // so comments are consumed left to right.
    const base = this.node(cur);
    const linkDocs = links.map(l => l.render());
    // A trailing token not routed into a rightmost call's args (e.g. the chain
    // ends in a field access) is appended after the whole chain.
    const finish = (doc: Doc): Doc =>
      trailing === "" || trailingRouted ? doc : concat([doc, trailing]);
    const callCount = links.filter(l => l.isCall).length;
    const baseIsCall = cur.kind === SyntaxKind.CallExpression;
    // gjf keeps a chain glued when its only dereference invocation comes after a
    // non-invocation prefix (`myField.foo()` stays on one line). But when the
    // receiver is itself a call (`when(x).thenReturn(y)`) or a primary expression
    // like `new X()` (gjf's `node != null` path), the dereference still breaks,
    // and two or more invocations are always a builder chain. A pure field-access
    // chain still breaks before its last selectors when it overflows (the break
    // path below, gated by the type-name prefix).
    const baseIsNew = cur.kind === SyntaxKind.ObjectCreationExpression;
    // An anonymous class receiver (`new X() {..}`) already spans multiple lines
    // and provides its own indentation; gjf glues a single dereference onto its
    // closing `}` (`}.scan(..)`) rather than starting a +4 chain that would
    // re-indent the class body.
    const baseIsAnonClass =
      baseIsNew && (cur as ObjectCreationExpression).classBody !== undefined;
    // A multi-line text-block receiver breaks before its dereference (gjf puts
    // `.replace(..)` on its own +4 line after the closing `"""`).
    const baseIsMultilineTextBlock =
      cur.kind === SyntaxKind.TextBlockLiteral && this.raw(cur).includes("\n");
    if (
      callCount === 1 &&
      !baseIsCall &&
      (!baseIsNew || baseIsAnonClass) &&
      !baseIsMultilineTextBlock
    ) {
      return finish(concat([base, ...linkDocs]));
    }
    // The leading links glued to the base (no break before them): a type-name
    // prefix (`ImmutableList.builder()` stays a unit), else just the first link
    // when the receiver is tiny.
    const baseLen = cur.end - this.start(cur);
    const minLength = this.mult * 4;
    let glue = baseLen <= minLength ? 1 : 0;
    if (cur.kind === SyntaxKind.Identifier) {
      const names = [this.raw(cur)];
      for (const l of links) {
        names.push(l.name);
        if (l.isCall) break; // the first method name ends the type-name prefix
      }
      const p = typePrefixLength(names);
      if (p >= 0) glue = p;
    }
    // gjf glues the receiver through a `.stream()`/`.parallelStream()`/
    // `.toBuilder()` call (its index becomes a chain-prefix boundary), so
    // `x.stream().map(..).collect(..)` keeps `x.stream()` on the first line and
    // breaks before the rest - rather than stranding the receiver on its own.
    links.forEach((l, i) => {
      if (l.isCall && STREAM_PREFIX_METHODS.has(l.name)) glue = Math.max(glue, i + 1);
    });
    const parts: Doc[] = [base];
    links.forEach((l, i) => {
      if (i >= glue) parts.push(brk("unified", "", ZERO));
      parts.push(linkDocs[i]);
    });
    return finish(level(PLUS4, parts));
  }

  // Emit an expression that a statement (or a declarator/assignment RHS)
  // terminates with `trailing` (a `;`), routing that token into the expression's
  // tail delimited level (a call/constructor argument list, including the last
  // call of a dereference chain) so the list wraps when the whole `(...);` run
  // overflows - gjf's rest-of-line rule. Other shapes just append it.
  private statementTail(e: Expression, trailing: Doc): Doc {
    if (e.kind === SyntaxKind.CallExpression) {
      const ce = e as CallExpression;
      // Mirror node()'s dispatch: a call on a `.`-access renders via dotChain.
      return ce.expression.kind === SyntaxKind.PropertyAccessExpression
        ? this.dotChain(ce, trailing)
        : this.call(ce, trailing);
    }
    if (e.kind === SyntaxKind.PropertyAccessExpression) {
      return this.dotChain(e, trailing);
    }
    if (e.kind === SyntaxKind.BinaryExpression) {
      return this.binary(e as BinaryExpression, trailing);
    }
    if (e.kind === SyntaxKind.ObjectCreationExpression) {
      const oc = e as ObjectCreationExpression;
      if (!oc.classBody) return this.objectCreation(oc, trailing);
    }
    if (e.kind === SyntaxKind.AssignmentExpression) {
      // `x = foo(...);` - the `;` rides into the assignment's RHS tail.
      const a = e as AssignmentExpression;
      const op = tokenToString(a.operatorToken) ?? "=";
      return concat([
        this.node(a.left),
        " ",
        op,
        level(PLUS4, [line, this.statementTail(a.right, trailing)]),
      ]);
    }
    return concat([this.node(e), trailing]);
  }

  private call(e: CallExpression, trailing: Doc = ""): Doc {
    return concat([
      this.node(e.expression),
      this.typeArguments(e.typeArguments),
      this.argList(e.arguments, trailing),
    ]);
  }

  private argList(args: NodeArray<Expression>, trailing: Doc = ""): Doc {
    if (args.length === 0) return concat(["()", trailing]);
    let anyComment = false;
    const lastI = args.length - 1;
    // Render each argument with the token that FOLLOWS it routed into the
    // argument's innermost delimited level: the inter-argument `,` for a non-last
    // arg, or the closing `)` (plus any outer trailing token) for the last. gjf
    // breaks a nested call/chain when the whole `(...)<close><sep>` run overflows,
    // so that trailing token must count in the nested level's own fit check
    // (rest-of-line rule); this also subsumes the lone dot-chain case. An argument
    // carrying a same-line trailing comment cannot route - the comment must sit
    // between the value and the separator - so it appends the token instead.
    const leads: (string | undefined)[] = [];
    const leadStarts: number[] = [];
    const items = args.map((a, i) => {
      const parts: Doc[] = [];
      const firstPending = this.comments[this.ci];
      leadStarts[i] =
        firstPending && firstPending.pos < this.start(a) ? firstPending.pos : this.start(a);
      // Leading comments on the argument: a block comment renders inline before
      // it (`/* a= */ 1`); a line comment forces a break after itself. A line
      // comment on the PREVIOUS argument's line (non-own-line) trails THAT
      // argument - the join emits it before the inter-argument break.
      this.commentsBefore(this.start(a)).forEach((c, ci) => {
        anyComment = true;
        if (ci === 0 && i > 0 && c.line && !c.ownLine) leads[i] = c.text;
        else if (c.line) parts.push(c.text, hardline);
        else parts.push(reformatParamComment(c.text) ?? c.text, " ");
      });
      const follow: Doc = i < lastI ? "," : concat([")", trailing]);
      const t = this.comments[this.ci];
      const hasTrailingComment =
        t !== undefined &&
        !t.line &&
        !t.ownLine &&
        t.pos >= a.end &&
        !/[\n,]/.test(this.text.slice(a.end, t.pos));
      if (hasTrailingComment) {
        parts.push(this.node(a));
        this.attachTrailingBlockComment(parts, a.end);
        anyComment = true;
        parts.push(follow);
      } else {
        parts.push(this.statementTail(a, follow));
      }
      return parts.length === 1 ? parts[0] : concat(parts);
    });
    const fill = this.fillMode(anyComment, args);
    // gjf's format-method layout (String.format / printf-style): when the first
    // arg is a string-literal concatenation carrying a format specifier, it sits
    // on its own line and the value args fill below it as a group - instead of
    // every arg going one-per-line just because the long format string is not a
    // "short item". Mirrors JavaInputAstVisitor.addArguments / isFormatMethod. The
    // `,` after the format string and the closing `)` are already in the items.
    if (!anyComment && this.isFormatMethod(args)) {
      const restFill = this.fillMode(false, args.slice(1));
      const restInner: Doc[] = [];
      items.slice(1).forEach((it, i) => {
        if (i > 0) restInner.push(brk(restFill, " ", ZERO));
        restInner.push(it);
      });
      return concat([
        "(",
        level(PLUS4, [
          brk("unified", "", ZERO),
          level(ZERO, [items[0], brk("unified", " ", ZERO), level(ZERO, restInner)]),
        ]),
      ]);
    }
    // The inter-argument `,` and the closing `)` are routed into the items, so the
    // inner level only joins them with fill breaks; its own fit check then counts
    // the `)` and any outer trailing token (rest-of-line). A line comment trailing
    // the previous argument stays on its line (before the break); gjf also
    // preserves one source blank line between arguments.
    const innerParts: Doc[] = [];
    // A source blank line between `(` and the first argument's leading comment is
    // preserved (gjf preserves a blank before an own-line comment, not a bare arg).
    if (leadStarts[0] < this.start(args[0]) && this.blankBeforePos(args[0].pos, leadStarts[0])) {
      innerParts.push(hardline);
    }
    items.forEach((it, i) => {
      if (i > 0) {
        const blank = this.blankBeforePos(args[i - 1].end, leadStarts[i]);
        if (leads[i] !== undefined) innerParts.push(" ", leads[i] as string, hardline);
        else innerParts.push(brk(fill, " ", ZERO));
        if (blank) innerParts.push(hardline);
      }
      innerParts.push(it);
    });
    return concat(["(", level(PLUS4, [brk("unified", "", ZERO), level(ZERO, innerParts)])]);
  }

  // gjf's isFormatMethod: a call whose first argument is a string-literal
  // concatenation containing a format specifier (`%` or `{n}`), with >= 2 args.
  private isFormatMethod(args: NodeArray<Expression>): boolean {
    return args.length >= 2 && this.isFormatStringConcat(args[0]);
  }

  // True when `node` is built only from string literals joined by `+` and at
  // least one literal carries a format specifier - gjf's isStringConcat.
  private isFormatStringConcat(node: Expression): boolean {
    let hasSpecifier = false;
    const walk = (n: Node): boolean => {
      if (n.kind === SyntaxKind.StringLiteral || n.kind === SyntaxKind.TextBlockLiteral) {
        if (/%|\{[0-9]\}/.test(this.raw(n))) hasSpecifier = true;
        return true;
      }
      if (
        n.kind === SyntaxKind.BinaryExpression &&
        (n as BinaryExpression).operatorToken === SyntaxKind.PlusToken
      ) {
        const b = n as BinaryExpression;
        return walk(b.left) && walk(b.right);
      }
      return false;
    };
    return walk(node) && hasSpecifier;
  }

  private objectCreation(e: ObjectCreationExpression, trailing: Doc = ""): Doc {
    const parts: Doc[] = [];
    if (e.qualifier) parts.push(this.node(e.qualifier), ".");
    // A trailing token only rides inside the argument list when there is no
    // anonymous class body (otherwise it belongs after the `}`).
    parts.push("new ", this.type(e.type), this.argList(e.arguments, e.classBody ? "" : trailing));
    if (e.classBody) parts.push(" ", this.body(e.classBody, e.end), trailing);
    return concat(parts);
  }

  private arrayCreation(e: ArrayCreationExpression): Doc {
    const dims = (e.dimensions ?? []).map(d => concat(["[", this.node(d), "]"]));
    const extra = "[]".repeat(e.additionalRank);
    const init = e.initializer ? concat([" ", this.arrayInitializer(e.initializer)]) : "";
    return concat(["new ", this.type(e.elementType), concat(dims), extra, init]);
  }

  private arrayInitializer(e: ArrayInitializer): Doc {
    if (e.elements.length === 0) return "{}";
    // gjf: contents indent +2; when broken, elements fill (INDEPENDENT) if all
    // short, else one per line (UNIFIED); the closing `}` goes on its own line
    // back at the parent indent (a -2 break cancels the +2).
    // A trailing comma in source is the author's "keep this vertical" signal:
    // gjf preserves the comma and FORCES the braces open (newline after `{` and
    // before `}`), but the elements themselves still fill (`{\n  1, 2, 3,\n}`).
    const trailingComma =
      this.text[skipTrivia(this.text, e.elements[e.elements.length - 1].end)] === ",";
    const { items, leads, leadStarts, anyComment } = this.listItems(
      e.elements,
      el => this.node(el),
      true,
    );
    // A comment forces one-per-line (gjf), else short items fill.
    const fillMode = this.fillMode(anyComment, e.elements);
    const innerParts: Doc[] = [];
    items.forEach((el, i) => {
      if (i > 0) {
        innerParts.push(",");
        // gjf preserves one source blank line between elements.
        const blank = this.blankBeforePos(e.elements[i - 1].end, leadStarts[i]);
        // A line comment trailing the previous element stays on its line, then
        // forces the break before this element.
        if (leads[i] !== undefined) innerParts.push(" ", leads[i] as string, hardline);
        else innerParts.push(brk(fillMode, " ", ZERO));
        if (blank) innerParts.push(hardline);
      }
      innerParts.push(el);
    });
    if (trailingComma) innerParts.push(",");
    // A line comment after the last element (before `}`) stays on its line.
    const lastEnd = e.elements[e.elements.length - 1].end;
    const t = this.comments[this.ci];
    let closingComment: string | undefined;
    if (t && t.line && !t.ownLine && t.pos > lastEnd && !/\n/.test(this.text.slice(lastEnd, t.pos))) {
      this.ci++;
      closingComment = t.text;
    }
    if (closingComment !== undefined) innerParts.push(" ", closingComment);
    const inner = level(ZERO, innerParts);
    const open: FillMode = trailingComma || closingComment !== undefined ? "forced" : "unified";
    return concat(["{", level(PLUS2, [brk(open, "", ZERO), inner, brk(open, "", MINUS2)]), "}"]);
  }

  private lambda(e: LambdaExpression): Doc {
    const params = e.parameters;
    let head: Doc;
    if (params.length === 1 && params[0].kind === SyntaxKind.Identifier) {
      head = this.raw(params[0]);
    } else {
      head = concat([
        "(",
        join(
          ", ",
          params.map(pp =>
            pp.kind === SyntaxKind.Parameter ? this.parameter(pp as Parameter) : this.raw(pp),
          ),
        ),
        ")",
      ]);
    }
    if (e.body.kind === SyntaxKind.Block) {
      return concat([head, " -> ", this.block(e.body as Block)]);
    }
    // A comment before an expression body sits own-line at a +8 continuation
    // indent (gjf), forcing `-> ` onto its own line; the comment forces the break.
    if (this.hasCommentBefore(this.start(e.body))) {
      const parts: Doc[] = [];
      for (const c of this.commentsBefore(this.start(e.body))) parts.push(reflow(c.text), hardline);
      parts.push(this.node(e.body));
      return concat([head, " ->", level(PLUS4, [hardline, concat(parts)])]);
    }
    // An expression body folds onto a +4 continuation line after `->` when it
    // does not fit (gjf), like the switch-arrow body above.
    return concat([head, " ->", level(PLUS4, [line, this.node(e.body)])]);
  }

  // A ternary. gjf keeps the condition on the line and breaks before `?` and `:`
  // (UNIFIED) onto +4 continuation lines.
  private conditional(e: ConditionalExpression): Doc {
    const parts: Doc[] = [
      this.node(e.condition),
      brk("unified", " ", ZERO),
      "? ",
      this.node(e.whenTrue),
    ];
    // A comment trailing the then-branch on its line stays there (gjf); a line
    // comment forces the `:` onto the next line (it would otherwise comment it out).
    const tc = this.trailingComment(e.whenTrue.end);
    if (tc) {
      parts.push(" ", tc.line ? tc.text : tc.text, tc.line ? hardline : brk("unified", " ", ZERO));
    } else {
      parts.push(brk("unified", " ", ZERO));
    }
    parts.push(": ");
    // A comment before the else-branch renders inline before it (`: /* a= */ x`).
    for (const c of this.commentsBefore(this.start(e.whenFalse))) {
      if (c.line) parts.push(c.text, hardline);
      else parts.push(c.text, " ");
    }
    parts.push(this.node(e.whenFalse));
    return level(PLUS4, parts);
  }

  // Consume and return a comment trailing `endPos` on the same source line (a
  // line or block comment), or undefined if the next pending comment is on a
  // later line. Used where a trailing comment must stay attached to a sub-part.
  private trailingComment(endPos: number): Comment | undefined {
    const t = this.comments[this.ci];
    // Only whitespace (no newline, no intervening token) may separate the part
    // from its trailing comment - else the comment belongs to something later.
    if (t && !t.ownLine && t.pos >= endPos && /^[ \t]*$/.test(this.text.slice(endPos, t.pos))) {
      this.ci++;
      return t;
    }
    return undefined;
  }

  private instanceOf(e: InstanceofExpression): Doc {
    const parts: Doc[] = [this.node(e.expression), " instanceof "];
    if (e.type) parts.push(this.type(e.type));
    if (e.name) parts.push(" ", this.raw(e.name));
    return concat(parts);
  }

  // --- dispatch ------------------------------------------------------------

  node(node: Node): Doc {
    switch (node.kind) {
      case SyntaxKind.ClassDeclaration:
        return this.classDeclaration(node as ClassDeclaration);
      case SyntaxKind.InterfaceDeclaration:
        return this.interfaceDeclaration(node as InterfaceDeclaration);
      case SyntaxKind.EnumDeclaration:
        return this.enumDeclaration(node as EnumDeclaration);
      case SyntaxKind.RecordDeclaration:
        return this.recordDeclaration(node as RecordDeclaration);
      case SyntaxKind.FieldDeclaration:
        return this.fieldDeclaration(node as FieldDeclaration);
      case SyntaxKind.MethodDeclaration:
        return this.methodLike(node as MethodDeclaration);
      case SyntaxKind.ConstructorDeclaration:
        return this.methodLike(node as ConstructorDeclaration);
      case SyntaxKind.InitializerBlock:
        return this.initializerBlock(node as InitializerBlock);

      case SyntaxKind.Block:
        return this.block(node as Block);
      case SyntaxKind.EmptyStatement:
        return ";";
      case SyntaxKind.LocalVariableDeclarationStatement:
        return this.localVar(node as LocalVariableDeclarationStatement);
      case SyntaxKind.ExpressionStatement:
        return this.statementTail((node as ExpressionStatement).expression, ";");
      case SyntaxKind.IfStatement:
        return this.ifStatement(node as IfStatement);
      case SyntaxKind.WhileStatement:
        return this.whileStatement(node as WhileStatement);
      case SyntaxKind.DoStatement:
        return this.doStatement(node as DoStatement);
      case SyntaxKind.ForStatement:
        return this.forStatement(node as ForStatement);
      case SyntaxKind.ForEachStatement:
        return this.forEachStatement(node as ForEachStatement);
      case SyntaxKind.ReturnStatement: {
        const r = node as ReturnStatement;
        return r.expression
          ? concat(["return ", this.statementTail(r.expression, ";")])
          : "return;";
      }
      case SyntaxKind.ThrowStatement:
        return concat(["throw ", this.statementTail((node as ThrowStatement).expression, ";")]);
      case SyntaxKind.BreakStatement: {
        const b = node as { label?: Node };
        return b.label ? concat(["break ", this.raw(b.label), ";"]) : "break;";
      }
      case SyntaxKind.ContinueStatement: {
        const c = node as { label?: Node };
        return c.label ? concat(["continue ", this.raw(c.label), ";"]) : "continue;";
      }
      case SyntaxKind.YieldStatement:
        return concat(["yield ", this.node((node as YieldStatement).expression), ";"]);
      case SyntaxKind.SynchronizedStatement: {
        const s = node as SynchronizedStatement;
        return concat(["synchronized (", this.node(s.expression), ") ", this.block(s.body)]);
      }
      case SyntaxKind.AssertStatement: {
        const s = node as AssertStatement;
        return s.message
          ? concat(["assert ", this.node(s.condition), " : ", this.node(s.message), ";"])
          : concat(["assert ", this.node(s.condition), ";"]);
      }
      case SyntaxKind.LabeledStatement: {
        const s = node as LabeledStatement;
        return concat([this.raw(s.label), ":", hardline, this.node(s.statement)]);
      }
      case SyntaxKind.TryStatement:
        return this.tryStatement(node as TryStatement);
      case SyntaxKind.SwitchStatement: {
        const s = node as SwitchStatement;
        return this.switchLike(s.expression, s.clauses, s.end);
      }

      case SyntaxKind.SwitchExpression: {
        const s = node as SwitchExpression;
        return this.switchLike(s.expression, s.clauses, s.end);
      }
      case SyntaxKind.BinaryExpression:
        return this.binary(node as BinaryExpression);
      case SyntaxKind.AssignmentExpression:
        return this.assignment(node as AssignmentExpression);
      case SyntaxKind.ConditionalExpression:
        return this.conditional(node as ConditionalExpression);
      case SyntaxKind.CallExpression: {
        const e = node as CallExpression;
        // A method call on a `.`-access is part of a dereference chain.
        if (e.expression.kind === SyntaxKind.PropertyAccessExpression) return this.dotChain(e);
        return this.call(e);
      }
      case SyntaxKind.PropertyAccessExpression: {
        const e = node as PropertyAccessExpression;
        // Route through the chain layout only when the receiver is itself a
        // call/access (a real chain); a plain `obj.field` stays inline.
        const k = e.expression.kind;
        if (
          k === SyntaxKind.CallExpression ||
          k === SyntaxKind.PropertyAccessExpression ||
          k === SyntaxKind.ElementAccessExpression
        ) {
          return this.dotChain(e);
        }
        return concat([this.node(e.expression), ".", this.raw(e.name)]);
      }
      case SyntaxKind.ElementAccessExpression: {
        const e = node as ElementAccessExpression;
        return concat([this.node(e.expression), "[", this.node(e.argumentExpression), "]"]);
      }
      case SyntaxKind.ObjectCreationExpression:
        return this.objectCreation(node as ObjectCreationExpression);
      case SyntaxKind.ArrayCreationExpression:
        return this.arrayCreation(node as ArrayCreationExpression);
      case SyntaxKind.ArrayInitializer:
        return this.arrayInitializer(node as ArrayInitializer);
      case SyntaxKind.ParenthesizedExpression:
        return concat(["(", this.node((node as ParenthesizedExpression).expression), ")"]);
      case SyntaxKind.PrefixUnaryExpression: {
        const e = node as PrefixUnaryExpression;
        const op = tokenToString(e.operator) ?? "";
        // A space goes between a +/- operator and an operand that itself starts
        // with +/- so the tokens do not merge into ++/-- (e.g. `- -1`, not `--1`).
        const operand = e.operand;
        const sep =
          (e.operator === SyntaxKind.PlusToken || e.operator === SyntaxKind.MinusToken) &&
          operand.kind === SyntaxKind.PrefixUnaryExpression &&
          ((operand as PrefixUnaryExpression).operator === SyntaxKind.PlusToken ||
            (operand as PrefixUnaryExpression).operator === SyntaxKind.MinusToken ||
            (operand as PrefixUnaryExpression).operator === SyntaxKind.PlusPlusToken ||
            (operand as PrefixUnaryExpression).operator === SyntaxKind.MinusMinusToken)
            ? " "
            : "";
        return concat([op, sep, this.node(operand)]);
      }
      case SyntaxKind.PostfixUnaryExpression: {
        const e = node as PostfixUnaryExpression;
        return concat([this.node(e.operand), tokenToString(e.operator) ?? ""]);
      }
      case SyntaxKind.CastExpression: {
        const e = node as CastExpression;
        const types = [this.type(e.type), ...(e.bounds ?? []).map(b => this.type(b))];
        // gjf visitTypeCast: open(+4); "(" type ")" breakOp(" ") expr; close.
        // The cast and its operand share a +4 level, so a multi-line operand
        // breaks after the ")" instead of gluing to it.
        return level(PLUS4, [
          "(",
          join(" & ", types),
          ")",
          brk("unified", " ", ZERO),
          this.node(e.expression),
        ]);
      }
      case SyntaxKind.InstanceofExpression:
        return this.instanceOf(node as InstanceofExpression);
      case SyntaxKind.LambdaExpression:
        return this.lambda(node as LambdaExpression);
      case SyntaxKind.MethodReferenceExpression: {
        const e = node as MethodReferenceExpression;
        return concat([
          this.node(e.expression),
          "::",
          e.isConstructorRef ? "new" : e.name ? this.raw(e.name) : "",
        ]);
      }
      case SyntaxKind.ThisExpression:
        return "this";
      case SyntaxKind.SuperExpression:
        return "super";
      case SyntaxKind.ClassLiteralExpression:
        return concat([this.type((node as ClassLiteralExpression).type), ".class"]);

      case SyntaxKind.TextBlockLiteral: {
        const raw = this.raw(node);
        // A multi-line text block is reflowed at write time: gjf strips the
        // block's common indentation (down to column 0) when a content line would
        // overflow at its current indent; otherwise it keeps the source indent.
        return raw.includes("\n") ? reflow(raw) : raw;
      }

      case SyntaxKind.Identifier:
      case SyntaxKind.NumericLiteral:
      case SyntaxKind.StringLiteral:
      case SyntaxKind.CharacterLiteral:
      case SyntaxKind.TrueKeyword:
      case SyntaxKind.FalseKeyword:
      case SyntaxKind.NullKeyword:
        return this.raw(node);

      case SyntaxKind.PrimitiveType:
      case SyntaxKind.TypeReference:
      case SyntaxKind.ArrayType:
      case SyntaxKind.WildcardType:
      case SyntaxKind.VarType:
        return this.type(node as TypeNode);

      case SyntaxKind.QualifiedName:
        return this.entityName(node as EntityName);
      case SyntaxKind.Annotation:
        return this.annotation(node as Annotation);

      default:
        // Degrade, do not crash: emit the verbatim source slice.
        return this.raw(node);
    }
  }
}

// Java binary-operator precedence groups (higher binds tighter). Operators in
// the same group flatten into one chain when wrapping, matching gjf's walkInfix.
const PRECEDENCE: Partial<Record<SyntaxKind, number>> = {
  [SyntaxKind.AsteriskToken]: 10,
  [SyntaxKind.SlashToken]: 10,
  [SyntaxKind.PercentToken]: 10,
  [SyntaxKind.PlusToken]: 9,
  [SyntaxKind.MinusToken]: 9,
  [SyntaxKind.LessThanLessThanToken]: 8,
  [SyntaxKind.GreaterThanGreaterThanToken]: 8,
  [SyntaxKind.GreaterThanGreaterThanGreaterThanToken]: 8,
  [SyntaxKind.LessThanToken]: 7,
  [SyntaxKind.GreaterThanToken]: 7,
  [SyntaxKind.LessThanEqualsToken]: 7,
  [SyntaxKind.GreaterThanEqualsToken]: 7,
  [SyntaxKind.EqualsEqualsToken]: 6,
  [SyntaxKind.ExclamationEqualsToken]: 6,
  [SyntaxKind.AmpersandToken]: 5,
  [SyntaxKind.CaretToken]: 4,
  [SyntaxKind.BarToken]: 3,
  [SyntaxKind.AmpersandAmpersandToken]: 2,
  [SyntaxKind.BarBarToken]: 1,
};

function precedence(op: SyntaxKind): number {
  return PRECEDENCE[op] ?? 0;
}

// google-java-format's TypeNameClassifier: the inclusive end index of the
// longest leading run of `nameParts` that looks like a type name (optionally
// with one trailing static member), or -1. Lets a chain keep a type prefix glued
// (`ImmutableList.builder()` stays a unit). Ported from TypeNameClassifier.java.
type CaseFormat = "upper" | "lower" | "upperCamel" | "lowerCamel";

function javaCaseFormat(name: string): CaseFormat {
  let firstUpper = false;
  let hasUpper = false;
  let hasLower = false;
  let first = true;
  for (const c of name) {
    if (!/[a-zA-Z]/.test(c)) continue;
    if (first) {
      firstUpper = c >= "A" && c <= "Z";
      first = false;
    }
    if (c >= "A" && c <= "Z") hasUpper = true;
    if (c >= "a" && c <= "z") hasLower = true;
  }
  if (firstUpper) return hasLower || name.length === 1 ? "upperCamel" : "upper";
  return hasUpper ? "lowerCamel" : "lower";
}

// State machine over case formats: START/TYPE/FIRST_STATIC_MEMBER/AMBIGUOUS/REJECT.
type TyState = "start" | "type" | "firstStatic" | "ambiguous" | "reject";
const SINGLE_UNIT: Record<TyState, boolean> = {
  start: false,
  type: true,
  firstStatic: true,
  ambiguous: false,
  reject: false,
};

function tyNext(state: TyState, n: CaseFormat): TyState {
  switch (state) {
    case "start":
      return n === "upper"
        ? "ambiguous"
        : n === "lowerCamel"
          ? "reject"
          : n === "lower"
            ? "start"
            : "type";
    case "type":
      return n === "upperCamel" ? "type" : "firstStatic";
    case "firstStatic":
      return "reject";
    case "ambiguous":
      return n === "upper" ? "ambiguous" : n === "upperCamel" ? "type" : "reject";
    case "reject":
      return "reject";
  }
}

function typePrefixLength(nameParts: string[]): number {
  let state: TyState = "start";
  let typeLength = -1;
  for (let i = 0; i < nameParts.length; i++) {
    state = tyNext(state, javaCaseFormat(nameParts[i]));
    if (state === "reject") break;
    if (SINGLE_UNIT[state]) typeLength = i;
  }
  return typeLength;
}

function rank(kind: SyntaxKind): number {
  const i = MODIFIER_ORDER.indexOf(kind);
  return i === -1 ? MODIFIER_ORDER.length : i;
}

// A blank line is forced between two members when either is a method,
// constructor, initializer or nested type (google-java-format); consecutive
// fields stay together unless the user separated them (a source blank line).
function forcedBlank(a: Node, b: Node): boolean {
  return (
    isBlankForcing(a.kind) ||
    isBlankForcing(b.kind) ||
    fieldSpansMultipleLines(a) ||
    fieldSpansMultipleLines(b)
  );
}

// google-java-format pads a field declaration with blank lines when it renders
// across multiple lines, which happens when an annotation lands on its own line
// (a "var"-mode annotation carrying arguments).
function fieldSpansMultipleLines(node: Node): boolean {
  if (node.kind !== SyntaxKind.FieldDeclaration) return false;
  const mods = (node as FieldDeclaration).modifiers;
  return (
    mods?.some(
      m => m.kind === SyntaxKind.Annotation && ((m as Annotation).args?.length ?? 0) > 0,
    ) ?? false
  );
}

function isBlankForcing(kind: SyntaxKind): boolean {
  switch (kind) {
    case SyntaxKind.MethodDeclaration:
    case SyntaxKind.ConstructorDeclaration:
    case SyntaxKind.InitializerBlock:
    case SyntaxKind.ClassDeclaration:
    case SyntaxKind.InterfaceDeclaration:
    case SyntaxKind.EnumDeclaration:
    case SyntaxKind.RecordDeclaration:
    case SyntaxKind.AnnotationTypeDeclaration:
      return true;
    default:
      return false;
  }
}
