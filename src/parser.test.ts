import { test } from "node:test";
import { expect } from "expect";

import { forEachChild, parseSourceFile } from "./parser.ts";
import {
	type ClassDeclaration,
	type EnumDeclaration,
	type Identifier,
	type InterfaceDeclaration,
	type Node,
	NodeFlags,
	type QualifiedName,
	SyntaxKind,
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

test("class body is skipped without errors (members come in M6)", () => {
	const sf = expectNoErrors("class C { private int x = 1; void m() { return; } }");
	const cls = sf.statements[0] as ClassDeclaration;
	expect(cls.members).toHaveLength(0);
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
