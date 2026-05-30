import { test } from "node:test";
import { expect } from "expect";

import { forEachChild, parseSourceFile } from "./parser.ts";
import { type Node, NodeFlags, SyntaxKind } from "./types.ts";

function parse(text: string) {
	return parseSourceFile("Test.java", text);
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
