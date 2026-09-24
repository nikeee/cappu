package compiler

import "testing"

func TestClassSignatureTypes(t *testing.T) {
	for _, one := range []struct {
		signature  string
		super      string
		interfaces []string
		ok         bool
	}{
		{"Ljava/lang/Object;Ljava/util/function/Function<Ljava/lang/String;Ljava/lang/Integer;>;",
			"java.lang.Object", []string{"java.util.function.Function<java.lang.String, java.lang.Integer>"}, true},
		{"Ljava/lang/Object;Ljava/util/Comparator<Ljava/lang/String;>;",
			"java.lang.Object", []string{"java.util.Comparator<java.lang.String>"}, true},
		{"Ljava/util/ArrayList<Ljava/lang/String;>;", "java.util.ArrayList<java.lang.String>", nil, true},
		{"Ljava/lang/Object;Ljava/util/List<*>;",
			"java.lang.Object", []string{"java.util.List<?>"}, true},
		{"Ljava/lang/Object;Ljava/util/List<+Ljava/lang/Number;>;",
			"java.lang.Object", []string{"java.util.List<? extends java.lang.Number>"}, true},
		{"Ljava/lang/Object;Ljava/util/List<-Ljava/lang/Number;>;",
			"java.lang.Object", []string{"java.util.List<? super java.lang.Number>"}, true},
		// A type variable is declared by the class or method around it, and the
		// standalone rendering writes neither's parameters.
		{"Ljava/lang/Object;Ljava/util/function/Function<TT;Ljava/lang/String;>;", "", nil, false},
		// Nothing but a real descriptor letter is a type argument.
		{"Ljava/lang/Object;Ljava/util/function/Function<VLjava/lang/Integer;>;", "", nil, false},
		{"Ljava/lang/Object;Ljava/util/function/Function<<<Ljava/lang/String;>;", "", nil, false},
		{"Ljava/lang/Object;Ljava/util/function/Function<T;Ljava/lang/Integer;>;", "", nil, false},
		{"Ljava/lang/Object;Ljava/util/List<[Ljava/lang/String;>;",
			"java.lang.Object", []string{"java.util.List<java.lang.String[]>"}, true},
		{"Ljava/lang/Object;Ljava/util/Map<Ljava/lang/String;Ljava/util/List<Ljava/lang/Integer;>;>;",
			"java.lang.Object", []string{"java.util.Map<java.lang.String, java.util.List<java.lang.Integer>>"}, true},
		{"Ljava/lang/Object;Lp/Outer<Ljava/lang/String;>.Inner;",
			"java.lang.Object", []string{"p.Outer<java.lang.String>.Inner"}, true},
		// A class with type parameters of its own is not one of these.
		{"<T:Ljava/lang/Object;>Ljava/lang/Object;", "", nil, false},
		{"", "", nil, false},
		{"Ljava/util/List<Ljava/lang/String;", "", nil, false},
	} {
		super, interfaces, ok := classSignatureTypes(one.signature, "")
		if ok != one.ok || super != one.super || len(interfaces) != len(one.interfaces) {
			t.Errorf("classSignatureTypes(%q) = %q, %q, %v; want %q, %q, %v",
				one.signature, super, interfaces, ok, one.super, one.interfaces, one.ok)
			continue
		}
		for i := range interfaces {
			if interfaces[i] != one.interfaces[i] {
				t.Errorf("classSignatureTypes(%q) interface %d = %q, want %q",
					one.signature, i, interfaces[i], one.interfaces[i])
			}
		}
	}
}
