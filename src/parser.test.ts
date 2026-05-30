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
