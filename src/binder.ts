// Binder. Mirrors the TypeScript compiler binder: a single walk of the tree
// (via forEachChild) that sets parent pointers, tracks the current container and
// block scope, and builds symbol tables. Declarations are attached to nodes
// (node.symbol) and recorded in the enclosing container's table (a type's
// symbol.members, or a container node's locals). Duplicate declarations are
// reported as bind diagnostics.
//
// This is the foundation an LSP needs for goto-definition, find-references and
// completion. Scoping is deliberately simple for the SE7 baseline: parameters
// and the method body share separate tables (a param redeclared as a top-level
// body local is not flagged), and try-with-resources variables are not yet
// scoped. These are refined in later milestones.

import { createDiagnostic } from "./diagnostics.ts";
import { Diagnostics } from "./diagnostics.ts";
import { forEachChild } from "./parser.ts";
import {
	type Diagnostic,
	type Identifier,
	type Node,
	type SourceFile,
	type Symbol,
	SymbolFlags,
	type SymbolTable,
	SyntaxKind,
} from "./types.ts";

let file: SourceFile;
let parent: Node | undefined;
let container: Node;
let bindDiagnostics: Diagnostic[] = [];

function createSymbolTable(): SymbolTable {
	return new Map<string, Symbol>();
}

function createSymbol(flags: SymbolFlags, name: string): Symbol {
	return { flags, escapedName: name, declarations: [] };
}

export function bindSourceFile(f: SourceFile): void {
	file = f;
	container = f;
	parent = f; // children of the file have the file as their parent
	bindDiagnostics = [];
	f.locals = createSymbolTable();
	f.symbol = createSymbol(SymbolFlags.Module, f.fileName);

	bindChildren(f);

	f.bindDiagnostics = bindDiagnostics;
}

function isTypeDeclaration(node: Node): boolean {
	switch (node.kind) {
		case SyntaxKind.ClassDeclaration:
		case SyntaxKind.InterfaceDeclaration:
		case SyntaxKind.EnumDeclaration:
		case SyntaxKind.AnnotationTypeDeclaration:
		case SyntaxKind.RecordDeclaration:
			return true;
		default:
			return false;
	}
}

// A node that owns a symbol table (type members, or locals).
function isContainer(node: Node): boolean {
	switch (node.kind) {
		case SyntaxKind.SourceFile:
		case SyntaxKind.MethodDeclaration:
		case SyntaxKind.ConstructorDeclaration:
		case SyntaxKind.Block:
		case SyntaxKind.ForStatement:
		case SyntaxKind.ForEachStatement:
		case SyntaxKind.CatchClause:
		case SyntaxKind.LambdaExpression:
			return true;
		default:
			return isTypeDeclaration(node);
	}
}

// The table that declarations in this container are recorded into.
function containerTable(node: Node): SymbolTable {
	if (node.kind === SyntaxKind.SourceFile) {
		return file.locals!;
	}
	if (isTypeDeclaration(node)) {
		const symbol = node.symbol!;
		symbol.members ??= createSymbolTable();
		return symbol.members;
	}
	node.locals ??= createSymbolTable();
	return node.locals;
}

function declareSymbol(
	table: SymbolTable,
	name: string,
	node: Node,
	locationNode: Node,
	flags: SymbolFlags,
	excludes: SymbolFlags,
): Symbol {
	let symbol = table.get(name);
	if (!symbol) {
		symbol = createSymbol(flags, name);
		table.set(name, symbol);
	} else {
		if (symbol.flags & excludes) {
			bindDiagnostics.push(
				createDiagnostic(locationNode.pos, locationNode.end - locationNode.pos, Diagnostics.Duplicate_declaration_0, name),
			);
		}
		symbol.flags |= flags;
	}
	symbol.declarations ??= [];
	symbol.declarations.push(node);
	node.symbol = symbol;
	return symbol;
}

function declareIntoContainer(name: Identifier | undefined, node: Node, flags: SymbolFlags, excludes: SymbolFlags): void {
	if (!name || name.text === "") {
		return; // missing name (parse error); nothing to declare
	}
	declareSymbol(containerTable(container), name.text, node, name, flags, excludes);
}

// Declare the node (if it is a declaration) into the current container.
function bindDeclaration(node: Node): void {
	switch (node.kind) {
		case SyntaxKind.ClassDeclaration:
			declareIntoContainer(named(node), node, SymbolFlags.Class, SymbolFlags.Type);
			break;
		case SyntaxKind.InterfaceDeclaration:
			declareIntoContainer(named(node), node, SymbolFlags.Interface, SymbolFlags.Type);
			break;
		case SyntaxKind.EnumDeclaration:
			declareIntoContainer(named(node), node, SymbolFlags.Enum, SymbolFlags.Type);
			break;
		case SyntaxKind.AnnotationTypeDeclaration:
			declareIntoContainer(named(node), node, SymbolFlags.Annotation, SymbolFlags.Type);
			break;
		case SyntaxKind.RecordDeclaration:
			declareIntoContainer(named(node), node, SymbolFlags.Record, SymbolFlags.Type);
			break;
		case SyntaxKind.RecordComponent:
			declareIntoContainer(named(node), node, SymbolFlags.Field, SymbolFlags.Field);
			break;
		case SyntaxKind.CompactConstructorDeclaration:
			declareIntoContainer(named(node), node, SymbolFlags.Constructor, SymbolFlags.None);
			break;
		case SyntaxKind.MethodDeclaration:
			// Overloading is allowed: methods do not exclude each other.
			declareIntoContainer(named(node), node, SymbolFlags.Method, SymbolFlags.None);
			break;
		case SyntaxKind.ConstructorDeclaration:
			declareIntoContainer(named(node), node, SymbolFlags.Constructor, SymbolFlags.None);
			break;
		case SyntaxKind.EnumConstantDeclaration:
			declareIntoContainer(named(node), node, SymbolFlags.EnumConstant, SymbolFlags.EnumConstant);
			break;
		case SyntaxKind.Parameter:
			declareIntoContainer(named(node), node, SymbolFlags.Parameter, SymbolFlags.Parameter);
			break;
		case SyntaxKind.TypeParameter:
			declareIntoContainer(named(node), node, SymbolFlags.TypeParameter, SymbolFlags.TypeParameter);
			break;
		case SyntaxKind.VariableDeclarator: {
			const isField = node.parent.kind === SyntaxKind.FieldDeclaration;
			const flags = isField ? SymbolFlags.Field : SymbolFlags.LocalVariable;
			declareIntoContainer(named(node), node, flags, flags);
			break;
		}
		default:
			break;
	}
}

// Pull the `name` Identifier off a declaration node, if present.
function named(node: Node): Identifier | undefined {
	return (node as { name?: Identifier }).name;
}

function bind(node: Node | undefined): void {
	if (!node) return;
	node.parent = parent!;
	bindDeclaration(node);

	const savedParent = parent;
	const savedContainer = container;

	parent = node;
	if (isContainer(node)) {
		container = node;
		containerTable(node); // ensure the table exists
	}

	bindChildren(node);

	parent = savedParent;
	container = savedContainer;
}

function bindChildren(node: Node): void {
	forEachChild(node, bind);
}
