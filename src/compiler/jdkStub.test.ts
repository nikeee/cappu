import { test } from "node:test";

import { expect } from "expect";

import { createChecker } from "./checker.ts";
import { JDK_STUB_FILES, loadJdkStub } from "./jdkStub.ts";
import { getIdentifierAtPosition } from "../services/nodeAtPosition.ts";
import { createProgram } from "./program.ts";
import { lookupMember, Meaning, resolveIdentifier } from "./resolver.ts";
import { type Identifier, SymbolFlags } from "./types.ts";
import { type Uri } from "../workspace.ts";
import type { Fqn } from "./program.ts";

test("stub files parse without diagnostics", () => {
  const program = createProgram();
  loadJdkStub(program);
  for (const file of JDK_STUB_FILES) {
    expect(program.getSourceFile(file.uri)!.parseDiagnostics).toHaveLength(0);
  }
});

test("stub types are in the global index", () => {
  const program = createProgram();
  loadJdkStub(program);
  const index = program.getGlobalIndex();
  expect(index.getType("java.lang.String" as Fqn)?.flags).toBe(SymbolFlags.Class);
  expect(index.getType("java.lang.Object" as Fqn)?.flags).toBe(SymbolFlags.Class);
  expect(index.getType("java.util.List" as Fqn)?.flags).toBe(SymbolFlags.Interface);
});

test("a user type resolves String via implicit java.lang", () => {
  const program = createProgram();
  loadJdkStub(program);
  program.setOpenDocument("file:///C.java" as Uri, "class C { String name; }", 1);
  const sf = program.getSourceFile("file:///C.java" as Uri)!;
  const id = getIdentifierAtPosition(sf, sf.text.indexOf("String"));
  const sym = resolveIdentifier(id as Identifier, program);
  expect(sym).toBe(program.getGlobalIndex().getType("java.lang.String" as Fqn));
});

test("inherited member is found through the stub hierarchy (List -> Collection)", () => {
  const program = createProgram();
  loadJdkStub(program);
  const list = program.getGlobalIndex().getType("java.util.List" as Fqn)!;
  // size() is declared on Collection, inherited by List
  const size = lookupMember(list, "size", Meaning.Value, program);
  expect(size?.flags).toBe(SymbolFlags.Method);
});

test("the members from nikeee/cappu#1 resolve", () => {
  // One use per symbol the issue reported as unresolved.
  const source = `
import java.io.BufferedReader;
import java.io.BufferedWriter;
import java.io.File;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Arrays;
import java.util.Collections;
import java.util.Comparator;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.function.Function;
import java.util.stream.Collectors;

class Issue1 {
  void members(List<String> list, Map<String, Integer> map, Set<Map.Entry<String, Integer>> entries,
               Optional<String> opt, File file, Path path, Class<?> klass, String[] array) throws Exception {
    list.stream();
    entries.stream();
    list.sort(Comparator.comparing(Function.identity()));
    map.forEach((k, v) -> {});
    map.computeIfAbsent("k", k -> 1);
    map.merge("k", 1, (a, b) -> a);
    Map<String, String> em = Collections.emptyMap();
    Set<String> us = Collections.unmodifiableSet(Collections.emptySet());
    Map<String, Integer> um = Collections.unmodifiableMap(map);
    opt.map(v -> v);
    opt.ifPresent(v -> {});
    Arrays.stream(array);
    File abs = file.getAbsoluteFile();
    klass.getConstructor();
    klass.getPackage();
    BufferedReader r = Files.newBufferedReader(path);
    BufferedWriter w = Files.newBufferedWriter(path);
    list.stream().collect(Collectors.counting());
    list.stream().collect(Collectors.groupingBy(v -> v));
    list.stream().collect(Collectors.groupingBy(v -> v, Collectors.mapping(v -> v, Collectors.toList())));
    list.stream().collect(Collectors.toMap(v -> v, v -> v));
  }
}
`;
  const program = createProgram();
  loadJdkStub(program);
  program.addProjectFile("file:///Issue1.java" as Uri, source);
  const checker = createChecker(program);
  const sourceFile = program.getSourceFile("file:///Issue1.java" as Uri)!;
  expect(sourceFile.parseDiagnostics).toHaveLength(0);
  const unresolved = checker
    .getSemanticDiagnostics(sourceFile)
    .filter(d => d.messageText.includes("Cannot resolve symbol"));
  expect(unresolved.map(d => d.messageText)).toEqual([]);
});
