package dapserver

import (
	"slices"
	"testing"
)

func TestDebuggeeJavaArgsOrdering(t *testing.T) {
	got := DebuggeeJavaArgs("/cp", "example.App", LaunchOptions{
		VMArgs:      []string{"-Xmx64m", "-Dk=v"},
		ProgramArgs: []string{"a", "b"},
	})
	want := []string{jdwpAgentArg, "-Xmx64m", "-Dk=v", "-cp", "/cp", "example.App", "a", "b"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestDebuggeeJavaArgsMinimal(t *testing.T) {
	got := DebuggeeJavaArgs("/cp", "M", LaunchOptions{})
	want := []string{jdwpAgentArg, "-cp", "/cp", "M"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestDebuggeeJavaArgsVMArgsBeforeMainClass(t *testing.T) {
	got := DebuggeeJavaArgs("/cp", "M", LaunchOptions{VMArgs: []string{"-ea"}, ProgramArgs: []string{"-ea"}})
	// Both literally "-ea": the JVM flag precedes the main class, the program
	// arg (the last element) follows it.
	if slices.Index(got, "-ea") >= slices.Index(got, "M") {
		t.Fatalf("vm arg not before main class: %v", got)
	}
	if got[len(got)-1] != "-ea" {
		t.Fatalf("program arg not after main class: %v", got)
	}
}
