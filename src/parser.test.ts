import { test } from "node:test";
import { expect } from "expect";

import { forEachChild, parseSourceFile } from "./parser.ts";
import {
	type ArrayType,
	type ClassDeclaration,
	type ConstructorDeclaration,
	type EnumDeclaration,
	type FieldDeclaration,
	type Identifier,
	type InitializerBlock,
	type InterfaceDeclaration,
	type MethodDeclaration,
	type Node,
	type NodeArray,
	NodeFlags,
	type Parameter,
	type PrimitiveType,
	type QualifiedName,
	SyntaxKind,
	type TypeNode,
	type TypeReference,
	type WildcardType,
} from "./types.ts";

function parse(text: string) {
	return parseSourceFile("Test.java", text);
}

function expectNoErrors(text: string) {
	const sf = parse(text);
	expect(sf.parseDiagnostics).toHaveLength(0);
	return sf;
}

test("empty source produces an empty, error-free SourceFile", () => {
	const sf = parse("");
	expect(sf.kind).toBe(SyntaxKind.SourceFile);
	expect(sf.statements).toHaveLength(0);
	expect(sf.parseDiagnostics).toHaveLength(0);
	expect(sf.endOfFileToken.kind).toBe(SyntaxKind.EndOfFileToken);
});

test("whitespace and comments only -> still empty and error-free", () => {
	const sf = parse("  // hello\n  /* block */\n");
	expect(sf.statements).toHaveLength(0);
	expect(sf.parseDiagnostics).toHaveLength(0);
});

test("empty statements are parsed", () => {
	const sf = parse(";;;");
	expect(sf.statements).toHaveLength(3);
	expect(sf.statements.every((s) => s.kind === SyntaxKind.EmptyStatement)).toBe(true);
	expect(sf.parseDiagnostics).toHaveLength(0);
});

test("garbage produces a diagnostic and is recovered", () => {
	const sf = parse("foo");
	expect(sf.statements).toHaveLength(0);
	expect(sf.parseDiagnostics.length).toBeGreaterThanOrEqual(1);
	// The error happened right before the EOF token was finished, so it carries
	// the ThisNodeHasError flag - exercising finishNode's error stamping.
	expect(sf.endOfFileToken.flags & NodeFlags.ThisNodeHasError).toBeTruthy();
});

test("garbage interleaved with valid statements recovers and keeps the good ones", () => {
	const sf = parse("; bar ;");
	expect(sf.statements).toHaveLength(2);
	expect(sf.statements.every((s) => s.kind === SyntaxKind.EmptyStatement)).toBe(true);
	expect(sf.parseDiagnostics.length).toBeGreaterThanOrEqual(1);
});

test("a long run of garbage terminates (no infinite loop)", () => {
	const sf = parse("@ @ @ # # # < > < > & & |".repeat(20));
	expect(sf.endOfFileToken.kind).toBe(SyntaxKind.EndOfFileToken);
	expect(sf.parseDiagnostics.length).toBeGreaterThanOrEqual(1);
});

test("node positions are set and ordered", () => {
	const sf = parse("  ;  ;");
	expect(sf.pos).toBe(0);
	expect(sf.end).toBe(6);
	const [a, b] = sf.statements;
	expect(a!.end).toBeLessThanOrEqual(b!.pos);
});

test("forEachChild visits statements then the end-of-file token", () => {
	const sf = parse(";;");
	const visited: SyntaxKind[] = [];
	forEachChild(sf, (node: Node) => {
		visited.push(node.kind);
		return undefined;
	});
	expect(visited).toEqual([
		SyntaxKind.EmptyStatement,
		SyntaxKind.EmptyStatement,
		SyntaxKind.EndOfFileToken,
	]);
});

test("forEachChild short-circuits on the first truthy result", () => {
	const sf = parse(";;");
	const first = forEachChild(sf, (node: Node) => (node.kind === SyntaxKind.EmptyStatement ? node : undefined));
	expect(first).toBe(sf.statements[0]);
});

// M4: compilation unit and type declaration headers

test("package declaration with a qualified name", () => {
	const sf = expectNoErrors("package com.example.app;");
	expect(sf.packageDeclaration?.kind).toBe(SyntaxKind.PackageDeclaration);
	const name = sf.packageDeclaration!.name as QualifiedName;
	expect(name.kind).toBe(SyntaxKind.QualifiedName);
	expect((name.right as Identifier).text).toBe("app");
});

test("import declarations: plain, static and on-demand", () => {
	const sf = expectNoErrors("import java.util.List;\nimport static org.Assert.assertTrue;\nimport java.util.*;");
	expect(sf.imports).toHaveLength(3);
	expect(sf.imports[0]!.isStatic).toBe(false);
	expect(sf.imports[0]!.isOnDemand).toBe(false);
	expect(sf.imports[1]!.isStatic).toBe(true);
	expect(sf.imports[2]!.isOnDemand).toBe(true);
});

test("class header with modifiers, type parameters, extends and implements", () => {
	const sf = expectNoErrors("public final class Foo<T extends Number, U> extends Bar implements A, B {}");
	const cls = sf.statements[0] as ClassDeclaration;
	expect(cls.kind).toBe(SyntaxKind.ClassDeclaration);
	expect(cls.name.text).toBe("Foo");
	expect(cls.modifiers).toHaveLength(2);
	expect(cls.typeParameters).toHaveLength(2);
	expect(cls.extendsType?.kind).toBe(SyntaxKind.TypeReference);
	expect(cls.implementsTypes).toHaveLength(2);
});

test("interface extends a list of interfaces", () => {
	const sf = expectNoErrors("interface I extends A, B, C {}");
	const iface = sf.statements[0] as InterfaceDeclaration;
	expect(iface.kind).toBe(SyntaxKind.InterfaceDeclaration);
	expect(iface.extendsTypes).toHaveLength(3);
});

test("enum and annotation type declarations", () => {
	const enumSf = expectNoErrors("enum Color implements Paintable {}");
	expect((enumSf.statements[0] as EnumDeclaration).kind).toBe(SyntaxKind.EnumDeclaration);
	const annSf = expectNoErrors("public @interface Marker {}");
	expect(annSf.statements[0]!.kind).toBe(SyntaxKind.AnnotationTypeDeclaration);
});

test("annotation used as a modifier is distinguished from @interface", () => {
	const sf = expectNoErrors("@Deprecated @SuppressWarnings public class C {}");
	const cls = sf.statements[0] as ClassDeclaration;
	expect(cls.modifiers).toHaveLength(3);
	expect(cls.modifiers![0]!.kind).toBe(SyntaxKind.Annotation);
	expect(cls.modifiers![2]!.kind).toBe(SyntaxKind.PublicKeyword);
});

test("nested generics in a heritage clause close cleanly", () => {
	expectNoErrors("class C extends java.util.HashMap<String, java.util.List<Integer>> {}");
});

test("class body with a field and a method parses without errors", () => {
	const sf = expectNoErrors("class C { private int x = 1; void m() { return; } }");
	const cls = sf.statements[0] as ClassDeclaration;
	expect(cls.members).toHaveLength(2);
});

test("multiple top-level type declarations", () => {
	const sf = expectNoErrors("class A {} interface B {} enum C {}");
	expect(sf.statements.map((s) => s.kind)).toEqual([
		SyntaxKind.ClassDeclaration,
		SyntaxKind.InterfaceDeclaration,
		SyntaxKind.EnumDeclaration,
	]);
});

test("package, imports and a class together", () => {
	const sf = expectNoErrors("package p;\nimport java.util.List;\npublic class Main {}");
	expect(sf.packageDeclaration).toBeDefined();
	expect(sf.imports).toHaveLength(1);
	expect(sf.statements).toHaveLength(1);
});

test("forEachChild walks a class header", () => {
	const sf = parse("class Foo<T> extends Bar {}");
	const cls = sf.statements[0] as ClassDeclaration;
	const kinds: SyntaxKind[] = [];
	forEachChild(cls, (n) => {
		kinds.push(n.kind);
		return undefined;
	});
	expect(kinds).toContain(SyntaxKind.Identifier); // name
	expect(kinds).toContain(SyntaxKind.TypeParameter);
	expect(kinds).toContain(SyntaxKind.TypeReference); // extends Bar
});

// M5: types parser (exercised through the extends/type-parameter positions)

function extendsType(typeText: string): { type: TypeNode; errors: number } {
	const sf = parse(`class C extends ${typeText} {}`);
	const cls = sf.statements[0] as ClassDeclaration;
	return { type: cls.extendsType!, errors: sf.parseDiagnostics.length };
}

test("simple and qualified type references", () => {
	const simple = extendsType("Foo");
	expect(simple.errors).toBe(0);
	expect(simple.type.kind).toBe(SyntaxKind.TypeReference);
	expect(((simple.type as TypeReference).typeName as Identifier).text).toBe("Foo");

	const qualified = extendsType("java.util.List");
	expect((qualified.type as TypeReference).typeName.kind).toBe(SyntaxKind.QualifiedName);
});

test("type arguments", () => {
	expect((extendsType("List<String>").type as TypeReference).typeArguments).toHaveLength(1);
	expect((extendsType("Map<K, V>").type as TypeReference).typeArguments).toHaveLength(2);
});

test("deeply nested generics close one '>' at a time", () => {
	const { type, errors } = extendsType("A<B<C<D>>>");
	expect(errors).toBe(0);
	const a = type as TypeReference;
	const b = a.typeArguments![0] as TypeReference;
	const c = b.typeArguments![0] as TypeReference;
	const d = c.typeArguments![0] as TypeReference;
	expect((d.typeName as Identifier).text).toBe("D");
});

test("wildcards: bounded and unbounded", () => {
	const { type, errors } = extendsType("Map<? extends Number, ? super Integer>");
	expect(errors).toBe(0);
	const args = (type as TypeReference).typeArguments!;
	expect((args[0] as WildcardType).hasExtends).toBe(true);
	expect((args[1] as WildcardType).hasSuper).toBe(true);

	const unbounded = (extendsType("List<?>").type as TypeReference).typeArguments![0] as WildcardType;
	expect(unbounded.kind).toBe(SyntaxKind.WildcardType);
	expect(unbounded.hasExtends).toBe(false);
	expect(unbounded.hasSuper).toBe(false);
});

test("diamond yields an empty type-argument list", () => {
	const { type, errors } = extendsType("List<>");
	expect(errors).toBe(0);
	expect((type as TypeReference).typeArguments).toHaveLength(0);
});

test("array types nest per '[]'", () => {
	const { type, errors } = extendsType("int[][]");
	expect(errors).toBe(0);
	const outer = type as ArrayType;
	expect(outer.kind).toBe(SyntaxKind.ArrayType);
	const inner = outer.elementType as ArrayType;
	expect(inner.kind).toBe(SyntaxKind.ArrayType);
	expect((inner.elementType as PrimitiveType).keyword).toBe(SyntaxKind.IntKeyword);
});

test("array inside a type argument", () => {
	const { type, errors } = extendsType("List<int[]>");
	expect(errors).toBe(0);
	expect((type as TypeReference).typeArguments![0]!.kind).toBe(SyntaxKind.ArrayType);
});

test("type parameter with multiple bounds", () => {
	const sf = expectNoErrors("class C<T extends A & B & java.io.Serializable> {}");
	const cls = sf.statements[0] as ClassDeclaration;
	expect(cls.typeParameters![0]!.constraint).toHaveLength(3);
});

// M6: members

function classMembers(text: string): NodeArray<Node> {
	const sf = expectNoErrors(text);
	return (sf.statements[0] as ClassDeclaration).members;
}

test("field declarations and multiple declarators", () => {
	const members = classMembers("class C { private int x; int a, b = 1, c[]; }");
	expect(members).toHaveLength(2);
	const f0 = members[0] as FieldDeclaration;
	expect(f0.kind).toBe(SyntaxKind.FieldDeclaration);
	expect(f0.declarators).toHaveLength(1);
	const f1 = members[1] as FieldDeclaration;
	expect(f1.declarators).toHaveLength(3);
	expect(f1.declarators[2]!.arrayRankAfterName).toBe(1);
});

test("method with parameters and throws", () => {
	const members = classMembers("class C { public void m(int a, String b) throws java.io.IOException {} }");
	const m = members[0] as MethodDeclaration;
	expect(m.kind).toBe(SyntaxKind.MethodDeclaration);
	expect(m.parameters).toHaveLength(2);
	expect(m.throws).toHaveLength(1);
	expect(m.body).toBeDefined();
});

test("generic method", () => {
	const members = classMembers("class C { <T> T id(T x) { return x; } }");
	const m = members[0] as MethodDeclaration;
	expect(m.typeParameters).toHaveLength(1);
	expect((m.returnType as TypeReference).typeName.kind).toBe(SyntaxKind.Identifier);
});

test("varargs parameter", () => {
	const members = classMembers("class C { void f(int... xs) {} }");
	const m = members[0] as MethodDeclaration;
	expect((m.parameters[0] as Parameter).isVarArgs).toBe(true);
});

test("abstract / interface method has no body", () => {
	const sf = expectNoErrors("interface I { int compute(int x); }");
	const m = (sf.statements[0] as InterfaceDeclaration).members[0] as MethodDeclaration;
	expect(m.body).toBeUndefined();
});

test("constructors, including generic constructors", () => {
	const plain = classMembers("class C { C(int x) {} }");
	expect((plain[0] as ConstructorDeclaration).kind).toBe(SyntaxKind.ConstructorDeclaration);
	const generic = classMembers("class C { <T> C(T x) {} }");
	const ctor = generic[0] as ConstructorDeclaration;
	expect(ctor.kind).toBe(SyntaxKind.ConstructorDeclaration);
	expect(ctor.typeParameters).toHaveLength(1);
});

test("static and instance initializer blocks", () => {
	const members = classMembers("class C { static {} {} }");
	expect((members[0] as InitializerBlock).isStatic).toBe(true);
	expect((members[1] as InitializerBlock).isStatic).toBe(false);
});

test("nested type declarations as members", () => {
	const members = classMembers("class C { class Inner {} static interface N {} }");
	expect(members.map((m) => m.kind)).toEqual([
		SyntaxKind.ClassDeclaration,
		SyntaxKind.InterfaceDeclaration,
	]);
});

test("enum with constants then a body", () => {
	const sf = expectNoErrors("enum E { A, B, C; int code; void m() {} }");
	const e = sf.statements[0] as EnumDeclaration;
	expect(e.enumConstants).toHaveLength(3);
	expect(e.members).toHaveLength(2);
});

test("enum constant with arguments and a class body", () => {
	const sf = expectNoErrors("enum E { A(1) { void m() {} }, B(2); E(int x) {} }");
	const e = sf.statements[0] as EnumDeclaration;
	expect(e.enumConstants).toHaveLength(2);
	expect(e.enumConstants[0]!.classBody).toBeDefined();
	expect(e.members).toHaveLength(1); // the constructor
});

test("annotation type element with default", () => {
	expectNoErrors("@interface Config { int timeout() default 30; String name(); }");
});

test("trailing comma in enum constants", () => {
	const sf = expectNoErrors("enum E { A, B, }");
	expect((sf.statements[0] as EnumDeclaration).enumConstants).toHaveLength(2);
});

test("forEachChild walks a method declaration", () => {
	const members = classMembers("class C { int add(int a, int b) { return a; } }");
	const kinds: SyntaxKind[] = [];
	forEachChild(members[0]!, (n) => {
		kinds.push(n.kind);
		return undefined;
	});
	expect(kinds).toContain(SyntaxKind.Parameter);
	expect(kinds).toContain(SyntaxKind.Block);
});

// M7 + M8: statements and expressions (parsed inside real method bodies)

function methodBody(stmts: string) {
	const sf = expectNoErrors(`class C { void m() { ${stmts} } }`);
	const method = (sf.statements[0] as ClassDeclaration).members[0] as MethodDeclaration;
	return method.body!.statements;
}

function firstStatement(stmts: string) {
	return methodBody(stmts)[0]!;
}

function expr(text: string) {
	// an expression in statement position
	const s = firstStatement(`${text};`) as import("./types.ts").ExpressionStatement;
	return s.expression;
}

test("local variable declarations with initializers", () => {
	const stmts = methodBody("int x = 1, y; final String s = \"a\";");
	expect(stmts).toHaveLength(2);
	expect(stmts[0]!.kind).toBe(SyntaxKind.LocalVariableDeclarationStatement);
});

test("expression statements: calls, assignment, increment", () => {
	expect(expr("foo()").kind).toBe(SyntaxKind.CallExpression);
	expect(expr("a = b").kind).toBe(SyntaxKind.AssignmentExpression);
	expect(expr("i++").kind).toBe(SyntaxKind.PostfixUnaryExpression);
	expect(expr("a.b.c").kind).toBe(SyntaxKind.PropertyAccessExpression);
	expect(expr("a.b().c[0].d").kind).toBe(SyntaxKind.PropertyAccessExpression);
});

test("binary operator precedence: a + b * c", () => {
	const e = expr("a + b * c") as import("./types.ts").BinaryExpression;
	expect(e.kind).toBe(SyntaxKind.BinaryExpression);
	expect(e.operatorToken).toBe(SyntaxKind.PlusToken);
	const right = e.right as import("./types.ts").BinaryExpression;
	expect(right.operatorToken).toBe(SyntaxKind.AsteriskToken);
});

test("logical precedence: a || b && c groups && tighter", () => {
	const e = expr("a || b && c") as import("./types.ts").BinaryExpression;
	expect(e.operatorToken).toBe(SyntaxKind.BarBarToken);
	expect((e.right as import("./types.ts").BinaryExpression).operatorToken).toBe(
		SyntaxKind.AmpersandAmpersandToken,
	);
});

test("shift vs relational: a << b < c", () => {
	const e = expr("a << b < c") as import("./types.ts").BinaryExpression;
	// '<' is lower precedence than '<<', so the top operator is '<'
	expect(e.operatorToken).toBe(SyntaxKind.LessThanToken);
	expect((e.left as import("./types.ts").BinaryExpression).operatorToken).toBe(
		SyntaxKind.LessThanLessThanToken,
	);
});

test("ternary and assignment are right-associative", () => {
	const ternary = expr("a ? b : c ? d : e") as import("./types.ts").ConditionalExpression;
	expect(ternary.kind).toBe(SyntaxKind.ConditionalExpression);
	expect((ternary.whenFalse as import("./types.ts").ConditionalExpression).kind).toBe(
		SyntaxKind.ConditionalExpression,
	);
	const assign = expr("a = b = c") as import("./types.ts").AssignmentExpression;
	expect((assign.right as import("./types.ts").AssignmentExpression).kind).toBe(
		SyntaxKind.AssignmentExpression,
	);
});

test("compound shift assignment a >>= b", () => {
	const e = expr("a >>= b") as import("./types.ts").AssignmentExpression;
	expect(e.kind).toBe(SyntaxKind.AssignmentExpression);
	expect(e.operatorToken).toBe(SyntaxKind.GreaterThanGreaterThanEqualsToken);
});

test("instanceof", () => {
	const e = expr("o instanceof String") as import("./types.ts").InstanceofExpression;
	expect(e.kind).toBe(SyntaxKind.InstanceofExpression);
	expect(e.type.kind).toBe(SyntaxKind.TypeReference);
});

test("cast vs parenthesized vs subtraction", () => {
	expect(expr("(int) x").kind).toBe(SyntaxKind.CastExpression);
	expect(expr("(Foo) bar").kind).toBe(SyntaxKind.CastExpression);
	expect(expr("(a)").kind).toBe(SyntaxKind.ParenthesizedExpression);
	// (a) - b is a subtraction, not a cast
	expect(expr("(a) - b").kind).toBe(SyntaxKind.BinaryExpression);
});

test("object, array and anonymous-class creation", () => {
	expect(expr("new Foo(1, 2)").kind).toBe(SyntaxKind.ObjectCreationExpression);
	expect(expr("new int[3][]").kind).toBe(SyntaxKind.ArrayCreationExpression);
	const arr = expr("new int[]{1, 2, 3}") as import("./types.ts").ArrayCreationExpression;
	expect(arr.initializer).toBeDefined();
	const anon = expr("new Runnable() { public void run() {} }") as import("./types.ts").ObjectCreationExpression;
	expect(anon.classBody).toBeDefined();
});

test("class literals and generic method calls", () => {
	expect(expr("String.class").kind).toBe(SyntaxKind.ClassLiteralExpression);
	expect(expr("int.class").kind).toBe(SyntaxKind.ClassLiteralExpression);
	expect(expr("this.<String>doIt()").kind).toBe(SyntaxKind.CallExpression);
});

test("control-flow statements", () => {
	expect(firstStatement("if (a) b(); else c();").kind).toBe(SyntaxKind.IfStatement);
	expect(firstStatement("while (a) b();").kind).toBe(SyntaxKind.WhileStatement);
	expect(firstStatement("do b(); while (a);").kind).toBe(SyntaxKind.DoStatement);
	expect(firstStatement("for (int i = 0; i < n; i++) b();").kind).toBe(SyntaxKind.ForStatement);
	expect(firstStatement("for (String s : list) b();").kind).toBe(SyntaxKind.ForEachStatement);
	expect(firstStatement("return x;").kind).toBe(SyntaxKind.ReturnStatement);
	expect(firstStatement("throw e;").kind).toBe(SyntaxKind.ThrowStatement);
	expect(firstStatement("synchronized (lock) {}").kind).toBe(SyntaxKind.SynchronizedStatement);
	expect(firstStatement("assert x > 0 : \"bad\";").kind).toBe(SyntaxKind.AssertStatement);
});

test("labeled break and continue", () => {
	const labeled = firstStatement("outer: for (;;) break outer;") as import("./types.ts").LabeledStatement;
	expect(labeled.kind).toBe(SyntaxKind.LabeledStatement);
	expect(labeled.label.text).toBe("outer");
});

test("try with resources, multi-catch and finally", () => {
	const t = firstStatement(
		"try (Reader r = open(); Reader q = open()) { use(); } catch (IOException | RuntimeException e) { log(e); } finally { close(); }",
	) as import("./types.ts").TryStatement;
	expect(t.kind).toBe(SyntaxKind.TryStatement);
	expect(t.resources).toHaveLength(2);
	expect(t.catchClauses).toHaveLength(1);
	expect(t.catchClauses[0]!.catchTypes).toHaveLength(2);
	expect(t.finallyBlock).toBeDefined();
});

test("switch statement with cases and default", () => {
	const sw = firstStatement("switch (x) { case 1: a(); break; case 2: b(); default: c(); }") as
		import("./types.ts").SwitchStatement;
	expect(sw.kind).toBe(SyntaxKind.SwitchStatement);
	expect(sw.clauses).toHaveLength(3);
	expect(sw.clauses[2]!.isDefault).toBe(true);
});

test("string switch (SE7)", () => {
	expectNoErrors("class C { void m(String s) { switch (s) { case \"a\": break; default: } } }");
});

test("local class declaration inside a method", () => {
	expect(firstStatement("class Local {} ").kind).toBe(SyntaxKind.ClassDeclaration);
});

test("nested blocks", () => {
	const block = firstStatement("{ int x = 1; { int y = 2; } }");
	expect(block.kind).toBe(SyntaxKind.Block);
});

test("field initializers are now parsed as expressions", () => {
	const sf = expectNoErrors("class C { int x = 1 + 2; int[] a = {1, 2, 3}; }");
	const field = (sf.statements[0] as ClassDeclaration).members[0] as FieldDeclaration;
	expect(field.declarators[0]!.initializer?.kind).toBe(SyntaxKind.BinaryExpression);
});

// M10: SE8 lambdas, method references, default methods, type annotations

test("lambda expressions: concise, parenthesized, typed, block body", () => {
	expect(expr("x -> x + 1").kind).toBe(SyntaxKind.LambdaExpression);
	expect(expr("() -> 42").kind).toBe(SyntaxKind.LambdaExpression);
	const two = expr("(a, b) -> a + b") as import("./types.ts").LambdaExpression;
	expect(two.parameters).toHaveLength(2);
	const typed = expr("(int a, String b) -> { return a; }") as import("./types.ts").LambdaExpression;
	expect(typed.parameters).toHaveLength(2);
	expect(typed.body.kind).toBe(SyntaxKind.Block);
});

test("a parenthesized expression is not mistaken for a lambda", () => {
	expect(expr("(a + b) * c").kind).toBe(SyntaxKind.BinaryExpression);
});

test("method references", () => {
	const m = expr("Foo::bar") as import("./types.ts").MethodReferenceExpression;
	expect(m.kind).toBe(SyntaxKind.MethodReferenceExpression);
	expect(m.isConstructorRef).toBe(false);
	const ctor = expr("ArrayList::new") as import("./types.ts").MethodReferenceExpression;
	expect(ctor.isConstructorRef).toBe(true);
	expect(expr("this::handle").kind).toBe(SyntaxKind.MethodReferenceExpression);
	expect(expr("java.util.Objects::requireNonNull").kind).toBe(SyntaxKind.MethodReferenceExpression);
});

test("default and static interface methods", () => {
	expectNoErrors("interface I { default int x() { return 1; } static int y() { return 2; } }");
});

test("type-use annotations are accepted", () => {
	expectNoErrors("class C { java.util.List<@NonNull String> xs; }");
});

test("lambda as a field initializer parses cleanly", () => {
	expectNoErrors("class C { Runnable r = (int a) -> { int b = a; }; }");
});
