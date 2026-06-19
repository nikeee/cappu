package compiler

import (
	"strings"
	"testing"
)

// Port of src/compiler/jdkStub.test.ts.

func TestStubFilesParseClean(t *testing.T) {
	program := NewProgram()
	LoadJdkStub(program)
	for _, file := range JDKStubFiles {
		sf := program.GetSourceFile(file.uri)
		if sf == nil {
			t.Fatalf("stub %s not loaded", file.uri)
		}
		if d := sf.AsSourceFile().ParseDiagnostics; len(d) != 0 {
			t.Errorf("stub %s has parse diagnostics: %v", file.uri, d)
		}
	}
}

func TestStubTypesInIndex(t *testing.T) {
	program := NewProgram()
	LoadJdkStub(program)
	index := program.GetGlobalIndex()
	if index.GetType("java.lang.String").Flags != SymbolFlagsClass {
		t.Error("java.lang.String should be a Class")
	}
	if index.GetType("java.lang.Object").Flags != SymbolFlagsClass {
		t.Error("java.lang.Object should be a Class")
	}
	if index.GetType("java.util.List").Flags != SymbolFlagsInterface {
		t.Error("java.util.List should be an Interface")
	}
}

func TestUserTypeResolvesStringViaJavaLang(t *testing.T) {
	program := NewProgram()
	LoadJdkStub(program)
	program.SetOpenDocument("file:///C.java", "class C { String name; }", 1)
	sf := program.GetSourceFile("file:///C.java")
	id := GetIdentifierAtPosition(sf, strings.Index(sf.AsSourceFile().Text, "String"))
	sym := ResolveIdentifier(id, program)
	if sym != program.GetGlobalIndex().GetType("java.lang.String") {
		t.Error("String should resolve to java.lang.String via implicit java.lang")
	}
}

func TestInheritedMemberThroughStubHierarchy(t *testing.T) {
	program := NewProgram()
	LoadJdkStub(program)
	list := program.GetGlobalIndex().GetType("java.util.List")
	size := LookupMember(list, "size", MeaningValue, program)
	if size == nil || size.Flags&SymbolFlagsMethod == 0 {
		t.Error("size() should be inherited from Collection by List")
	}
}

func TestIssue1MembersResolve(t *testing.T) {
	source := `
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
`
	program := NewProgram()
	LoadJdkStub(program)
	program.AddProjectFile("file:///Issue1.java", source)
	checker := NewChecker(program)
	sourceFile := program.GetSourceFile("file:///Issue1.java")
	if d := sourceFile.AsSourceFile().ParseDiagnostics; len(d) != 0 {
		t.Fatalf("parse diagnostics: %v", d)
	}
	for _, d := range checker.GetSemanticDiagnostics(sourceFile) {
		if strings.Contains(d.MessageText, "Cannot resolve symbol") {
			t.Errorf("unresolved member: %s", d.MessageText)
		}
	}
}

func TestIssue9MembersResolveAndType(t *testing.T) {
	source := `
import java.util.Map;
import java.util.Queue;

class Issue9 {
  String take(Queue<String> q) {
    q.element();
    return q.remove();
  }
  int count(Map<Object, String> nodeIdMap) {
    return nodeIdMap.values().stream()
      .map(s -> s)
      .mapToInt(i -> 1).max().orElse(-1);
  }
}
`
	program := NewProgram()
	LoadJdkStub(program)
	program.AddProjectFile("file:///Issue9.java", source)
	checker := NewChecker(program)
	sourceFile := program.GetSourceFile("file:///Issue9.java")
	if d := sourceFile.AsSourceFile().ParseDiagnostics; len(d) != 0 {
		t.Fatalf("parse diagnostics: %v", d)
	}
	if d := checker.GetSemanticDiagnostics(sourceFile); len(d) != 0 {
		t.Errorf("unexpected diagnostics: %v", d)
	}
}
