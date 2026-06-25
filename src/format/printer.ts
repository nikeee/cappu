// Lower a parsed Java source file to the Doc IR (doc.ts), which is then printed
// at the configured width. The visitor regenerates all layout from the AST -
// google-java-format does the same, discarding original whitespace - so cappu's
// trivia-free AST is sufficient. The only thing recovered from source is whether
// the user left a blank line between two members/statements (g-j-f preserves one).
//
// Node kinds not yet handled fall back to the verbatim source slice (degrade,
// never crash), matching the emitter's discipline.

import { skipTrivia, tokenToString } from "../compiler/utilities.ts";
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
  concat,
  type Doc,
  type FillMode,
  group,
  hardline,
  indent,
  indentConst,
  join,
  level,
  line,
  printDoc,
  ZERO,
} from "./doc.ts";

// google-java-format continuation indents (columns at google scale; the printer
// is built once and the style multiplier is applied at print time):
//   +2 = one indent level (block body, array-initializer continuation)
//   +4 = a continuation (broken argument/parameter/type lists, operator chains)
const PLUS2 = indentConst(2);
const PLUS4 = indentConst(4);
const MINUS2 = indentConst(-2);

export interface FormatOptions {
  style: "google" | "aosp";
}

const WIDTH = 100;

/** Thrown when the formatter cannot format the input without losing information. */
export class UnsupportedSyntaxError extends Error {}

export function formatSourceFile(sf: SourceFile, options: FormatOptions): string {
  const mult = options.style === "aosp" ? 2 : 1;
  const p = new Printer(sf, mult);
  const doc = p.sourceFile(sf);
  const text = printDoc(doc, { width: WIDTH, indentMultiplier: mult });
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
  constructor(
    private readonly sf: SourceFile,
    private readonly mult: number,
  ) {
    this.text = sf.text;
    this.comments = collectComments(sf.text);
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
      const firstPos = this.hasCommentBefore(itemStart) ? this.comments[this.ci].pos : itemStart;
      const entryBlank =
        i > 0 &&
        (this.blankBeforePos(prevEnd, firstPos) || (forced && forcedBlank(list[i - 1], item)));
      let pushedInEntry = false;
      const pushEntry = (doc: Doc, srcBlank: boolean) => {
        push(doc, pushedInEntry ? srcBlank : entryBlank || srcBlank);
        pushedInEntry = true;
      };

      for (const c of this.commentsBefore(itemStart)) {
        if (!c.ownLine && !pushedInEntry && i > 0) {
          // A comment after code on the same line: attach to the previous entry.
          out[out.length - 1] = concat([out[out.length - 1], " ", c.text]);
        } else {
          pushEntry(c.text, this.blankBeforePos(prevEnd, c.pos));
        }
        prevEnd = c.end;
      }

      let itemDoc = this.node(item);
      const trailing = this.trailingCommentAfter(item);
      if (trailing) {
        itemDoc = concat([itemDoc, " ", trailing.text]);
        prevEnd = trailing.end;
      } else {
        prevEnd = item.end;
      }
      pushEntry(itemDoc, false);
    });

    for (const c of this.commentsBefore(endPos)) {
      push(c.text, this.blankBeforePos(prevEnd, c.pos));
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
    if (header.length > 0)
      blocks.push(
        join(
          hardline,
          header.map(c => c.text),
        ),
      );
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
    const sorted = [...imports].sort((a, b) => {
      const an = this.entityName(a.name);
      const bn = this.entityName(b.name);
      return an < bn ? -1 : an > bn ? 1 : 0;
    });
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
    const annotations = mods.filter(m => m.kind === SyntaxKind.Annotation) as Annotation[];
    const keywords = mods.filter(m => m.kind !== SyntaxKind.Annotation);
    keywords.sort((a, b) => rank(a.kind) - rank(b.kind));
    const parts: Doc[] = [];
    for (const a of annotations) {
      const ownLine =
        annoMode === "own" || (annoMode === "var" && a.args !== undefined && a.args.length > 0);
      parts.push(this.annotation(a), ownLine ? hardline : " ");
    }
    for (const k of keywords) parts.push(concat([this.modifierText(k), " "]));
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
    return concat([name, "(", join(", ", args), ")"]);
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
      case SyntaxKind.PrimitiveType:
        return tokenToString((t as { keyword: SyntaxKind }).keyword) ?? this.raw(t);
      case SyntaxKind.VarType:
        return "var";
      case SyntaxKind.ArrayType:
        return concat([this.type((t as ArrayType).elementType), "[]"]);
      case SyntaxKind.TypeReference: {
        const tr = t as TypeReference;
        return concat([this.entityName(tr.typeName), this.typeArguments(tr.typeArguments)]);
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
    return concat([
      "{",
      indent(concat([hardline, ...this.members(members, endPos)])),
      hardline,
      "}",
    ]);
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
    const constants = d.enumConstants.map(c => this.enumConstant(c));
    // google-java-format always lays enum constants one per line.
    const constantsDoc = join(concat([",", hardline]), constants);
    const bodyParts: Doc[] = [hardline, constantsDoc];
    if (d.members.length > 0) {
      // A blank line separates the constant list from the member declarations.
      bodyParts.push(";", hardline, hardline, ...this.members(d.members, d.end));
    } else if (constants.length > 0) {
      // A trailing `;` after the last constant is preserved from the source.
      const last = d.enumConstants[d.enumConstants.length - 1];
      if (this.text[skipTrivia(this.text, last.end)] === ";") bodyParts.push(";");
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
    const comps = d.recordComponents.map((rc: RecordComponent) =>
      concat([
        this.annotations(rc.annotations),
        this.type(rc.type),
        rc.isVarArgs ? "... " : " ",
        this.raw(rc.name),
      ]),
    );
    const after: Doc[] = [concat(["(", join(", ", comps), ")"])];
    if (d.implementsTypes && d.implementsTypes.length > 0)
      after.push(
        concat([
          " implements ",
          join(
            ", ",
            d.implementsTypes.map(t => this.type(t)),
          ),
        ]),
      );
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

  private declarator(v: VariableDeclarator): Doc {
    const name = concat([this.raw(v.name), "[]".repeat(v.arrayRankAfterName)]);
    if (!v.initializer) return name;
    // An array initializer hugs the `=` (`x = {` ... `}`); its own braces break,
    // so do not insert a break before it. (Other hugging RHS kinds - lambdas,
    // anonymous classes - are handled with the rest of assignment RHS in phase B.)
    if (v.initializer.kind === SyntaxKind.ArrayInitializer) {
      return concat([name, " = ", this.node(v.initializer)]);
    }
    // gjf folds a long initializer onto a +4 continuation line after `=`.
    return concat([name, " =", level(PLUS4, [line, this.node(v.initializer)])]);
  }

  // A gjf-style parenthesized comma list (`(a, b, c)`). When it does not fit, a
  // UNIFIED break fires after `(` (continuation at +4) so the items always start
  // on the next line; a nested zero-indent level then keeps them on one
  // continuation line if they fit. If they do not, the inter-item fill mode
  // decides: UNIFIED puts one per line, INDEPENDENT *fills* as many per line as
  // fit. The closing `)` stays attached to the last item's line.
  private argsLike(open: string, items: Doc[], close: string, fillMode: FillMode): Doc {
    const innerParts: Doc[] = [];
    items.forEach((it, i) => {
      if (i > 0) innerParts.push(",", brk(fillMode, " ", ZERO));
      innerParts.push(it);
    });
    const inner = level(ZERO, innerParts);
    return concat([open, level(PLUS4, [brk("unified", "", ZERO), inner]), close]);
  }

  // gjf fills a delimited list (packs items per line) only when every item is
  // "short" - its source text is under MAX_ITEM_LENGTH_FOR_FILLING (10) chars;
  // otherwise items go one per line.
  private allShortItems(nodes: readonly Node[]): boolean {
    return nodes.every(n => n.end - this.start(n) < 10);
  }

  private parameters(params: NodeArray<Parameter>): Doc {
    if (params.length === 0) return "()";
    // Parameters are never filled (gjf uses a UNIFIED inter-parameter break).
    return this.argsLike(
      "(",
      params.map(p => this.parameter(p)),
      ")",
      "unified",
    );
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
    head.push(this.raw(decl.name), this.parameters(decl.parameters));
    if (decl.throws && decl.throws.length > 0) {
      // Same shape as a class header type list, wrapped in a +4 level so the
      // `throws` keyword folds onto a continuation line (the class header gets
      // that +4 from its own enclosing level; a method head has none).
      head.push(level(PLUS4, [this.typeListClause("throws", [...decl.throws])]));
    }
    if (!decl.body) return concat([...head, ";"]);
    return concat([...head, " ", this.block(decl.body)]);
  }

  private initializerBlock(d: InitializerBlock): Doc {
    return concat([d.isStatic ? "static " : "", this.block(d.body)]);
  }

  // --- statements ----------------------------------------------------------

  private block(b: Block): Doc {
    if (b.statements.length === 0 && !this.hasCommentBefore(b.end)) return "{}";
    return concat([
      "{",
      indent(concat([hardline, ...this.statementList(b.statements, b.end)])),
      hardline,
      "}",
    ]);
  }

  private statementList(list: NodeArray<Statement>, endPos: number): Doc[] {
    return this.listDocs(list, false, endPos);
  }

  private localVar(d: LocalVariableDeclarationStatement): Doc {
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

  private ifStatement(s: IfStatement): Doc {
    const parts: Doc[] = [
      group(concat(["if (", this.node(s.condition), ")"])),
      this.clauseBody(s.thenStatement),
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
  private clauseBody(s: Statement): Doc {
    if (s.kind === SyntaxKind.Block) return concat([" ", this.block(s as Block)]);
    return group(indent(concat([line, this.node(s)])));
  }

  private whileStatement(s: WhileStatement): Doc {
    return concat([
      group(concat(["while (", this.node(s.condition), ")"])),
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
    return concat([
      group(concat(["for (", this.parameter(s.parameter), " : ", this.node(s.expression), ")"])),
      this.clauseBody(s.statement),
    ]);
  }

  private tryStatement(s: TryStatement): Doc {
    const parts: Doc[] = ["try"];
    if (s.resources && s.resources.length > 0) {
      parts.push(
        " (",
        join(
          concat(["; "]),
          s.resources.map(r => this.resource(r)),
        ),
        ")",
      );
    }
    parts.push(" ", this.block(s.tryBlock));
    for (const c of s.catchClauses) {
      parts.push(
        " catch (",
        join(
          " | ",
          c.catchTypes.map(t => this.type(t)),
        ),
        " ",
        this.raw(c.name),
        ") ",
        this.block(c.block),
      );
    }
    if (s.finallyBlock) parts.push(" finally ", this.block(s.finallyBlock));
    return concat(parts);
  }

  private resource(r: Resource): Doc {
    if (r.expression) return this.node(r.expression);
    return concat([
      this.modifiers(r.modifiers),
      r.type ? concat([this.type(r.type), " "]) : "",
      r.name ? this.raw(r.name) : "",
      r.initializer ? concat([" = ", this.node(r.initializer)]) : "",
    ]);
  }

  private switchLike(expr: Expression, clauses: NodeArray<SwitchClause>): Doc {
    const body = clauses.map(c => this.switchClause(c));
    return concat([
      group(concat(["switch (", this.node(expr), ")"])),
      " {",
      indent(concat([hardline, join(hardline, body)])),
      hardline,
      "}",
    ]);
  }

  private switchClause(c: SwitchClause): Doc {
    const label = c.isDefault
      ? "default"
      : concat([
          "case ",
          join(
            ", ",
            (c.labels ?? []).map(l => this.node(l)),
          ),
        ]);
    const guard = c.guard ? concat([" when ", this.node(c.guard)]) : "";
    if (c.isArrow) {
      const stmts = c.statements;
      if (stmts.length === 1 && stmts[0].kind === SyntaxKind.Block) {
        return concat([label, guard, " -> ", this.block(stmts[0] as Block)]);
      }
      return concat([
        label,
        guard,
        " -> ",
        join(
          " ",
          stmts.map(s => this.node(s)),
        ),
      ]);
    }
    return concat([
      label,
      guard,
      ":",
      indent(concat([hardline, ...this.statementList(c.statements, c.end)])),
    ]);
  }

  // --- expressions ---------------------------------------------------------

  // A binary operator chain. gjf collects all operands at the same precedence
  // into one +4 level and breaks *before* each operator; the breaks fill when
  // every operand is short, else go one per line.
  private binary(e: BinaryExpression): Doc {
    const prec = precedence(e.operatorToken);
    const operands: Expression[] = [];
    const operators: string[] = [];
    this.walkInfix(prec, e, operands, operators);
    const fillMode: FillMode = this.allShortItems(operands) ? "independent" : "unified";
    const parts: Doc[] = [this.node(operands[0])];
    operators.forEach((op, i) => {
      parts.push(brk(fillMode, " ", ZERO), op, " ", this.node(operands[i + 1]));
    });
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
  private dotChain(root: Expression): Doc {
    const links: { doc: Doc; isCall: boolean; name: string }[] = [];
    let cur: Node = root;
    for (;;) {
      if (
        cur.kind === SyntaxKind.CallExpression &&
        (cur as CallExpression).expression.kind === SyntaxKind.PropertyAccessExpression
      ) {
        const callExpr = cur as CallExpression;
        const pa = callExpr.expression as PropertyAccessExpression;
        links.unshift({
          doc: concat([
            ".",
            this.raw(pa.name),
            this.typeArguments(callExpr.typeArguments),
            this.argList(callExpr.arguments),
          ]),
          isCall: true,
          name: this.raw(pa.name),
        });
        cur = pa.expression;
      } else if (cur.kind === SyntaxKind.PropertyAccessExpression) {
        const pa = cur as PropertyAccessExpression;
        links.unshift({
          doc: concat([".", this.raw(pa.name)]),
          isCall: false,
          name: this.raw(pa.name),
        });
        cur = pa.expression;
      } else {
        break;
      }
    }
    const base = this.node(cur);
    const callCount = links.filter(l => l.isCall).length;
    const baseIsCall = cur.kind === SyntaxKind.CallExpression;
    // gjf keeps a chain glued when its only dereference invocation comes after a
    // non-invocation prefix (`myField.foo()` stays on one line). But when the
    // receiver is itself a call (`when(x).thenReturn(y)`) the dereference still
    // breaks, and two or more invocations are always a builder chain.
    if (callCount === 0 || (callCount === 1 && !baseIsCall)) {
      return concat([base, ...links.map(l => l.doc)]);
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
    const parts: Doc[] = [base];
    links.forEach((l, i) => {
      if (i >= glue) parts.push(brk("unified", "", ZERO));
      parts.push(l.doc);
    });
    return level(PLUS4, parts);
  }

  private call(e: CallExpression): Doc {
    return concat([
      this.node(e.expression),
      this.typeArguments(e.typeArguments),
      this.argList(e.arguments),
    ]);
  }

  private argList(args: NodeArray<Expression>): Doc {
    if (args.length === 0) return "()";
    return this.argsLike(
      "(",
      args.map(a => this.node(a)),
      ")",
      this.allShortItems(args) ? "independent" : "unified",
    );
  }

  private objectCreation(e: ObjectCreationExpression): Doc {
    const parts: Doc[] = [];
    if (e.qualifier) parts.push(this.node(e.qualifier), ".");
    parts.push("new ", this.type(e.type), this.argList(e.arguments));
    if (e.classBody) parts.push(" ", this.body(e.classBody, e.end));
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
    // ponytail: trailing-comma -> FORCED after-open break is not modeled (the
    // parser drops the trailing comma); add when a fixture needs it.
    const fillMode: FillMode = this.allShortItems(e.elements) ? "independent" : "unified";
    const innerParts: Doc[] = [];
    e.elements.forEach((el, i) => {
      if (i > 0) innerParts.push(",", brk(fillMode, " ", ZERO));
      innerParts.push(this.node(el));
    });
    const inner = level(ZERO, innerParts);
    return concat([
      "{",
      level(PLUS2, [brk("unified", "", ZERO), inner, brk("unified", "", MINUS2)]),
      "}",
    ]);
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
    const body = e.body.kind === SyntaxKind.Block ? this.block(e.body as Block) : this.node(e.body);
    return concat([head, " -> ", body]);
  }

  // A ternary. gjf keeps the condition on the line and breaks before `?` and `:`
  // (UNIFIED) onto +4 continuation lines.
  private conditional(e: ConditionalExpression): Doc {
    return level(PLUS4, [
      this.node(e.condition),
      brk("unified", " ", ZERO),
      "? ",
      this.node(e.whenTrue),
      brk("unified", " ", ZERO),
      ": ",
      this.node(e.whenFalse),
    ]);
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
        return concat([this.node((node as ExpressionStatement).expression), ";"]);
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
        return r.expression ? concat(["return ", this.node(r.expression), ";"]) : "return;";
      }
      case SyntaxKind.ThrowStatement:
        return concat(["throw ", this.node((node as ThrowStatement).expression), ";"]);
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
        return this.switchLike(s.expression, s.clauses);
      }

      case SyntaxKind.SwitchExpression: {
        const s = node as SwitchExpression;
        return this.switchLike(s.expression, s.clauses);
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
        return concat(["(", join(" & ", types), ") ", this.node(e.expression)]);
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

      case SyntaxKind.Identifier:
      case SyntaxKind.NumericLiteral:
      case SyntaxKind.StringLiteral:
      case SyntaxKind.CharacterLiteral:
      case SyntaxKind.TextBlockLiteral:
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
