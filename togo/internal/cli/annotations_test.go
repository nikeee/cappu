package cli

import "testing"

// Port of src/cli/annotations.test.ts.

func TestAnnotationsEnabled(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"github", map[string]string{"GITHUB_ACTIONS": "true"}, true},
		{"forgejo", map[string]string{"FORGEJO_ACTIONS": "true"}, true},
		{"gitea", map[string]string{"GITEA_ACTIONS": "true"}, true},
		{"unset", map[string]string{}, false},
		{"empty", map[string]string{"GITHUB_ACTIONS": ""}, false},
		{"false", map[string]string{"GITHUB_ACTIONS": "false"}, false},
		{"one", map[string]string{"GITHUB_ACTIONS": "1"}, false},
		// bare CI=true is not a trigger: no generic-CI annotation format exists
		{"ci", map[string]string{"CI": "true"}, false},
	}
	for _, c := range cases {
		env := func(k string) string { return c.env[k] }
		if got := AnnotationsEnabled(env); got != c.want {
			t.Errorf("%s: AnnotationsEnabled = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFormatAnnotation(t *testing.T) {
	cases := []struct {
		severity, message string
		loc               AnnotationLocation
		want              string
	}{
		{"error", "cannot find symbol", AnnotationLocation{File: "Foo.java", Line: 3, Column: 5},
			"::error file=Foo.java,line=3,col=5::cannot find symbol"},
		{"warning", "deprecated API", AnnotationLocation{},
			"::warning::deprecated API"},
		// partial location: only the present properties are emitted
		{"error", "boom", AnnotationLocation{File: "A.java", Line: 2},
			"::error file=A.java,line=2::boom"},
		// message (data) escaping: % \r \n
		{"error", "100% done\nnext\r", AnnotationLocation{},
			"::error::100%25 done%0Anext%0D"},
		// property value escaping: additionally : and ,
		{"error", "x", AnnotationLocation{File: "a:b,c.java", Line: 1},
			"::error file=a%3Ab%2Cc.java,line=1::x"},
	}
	for _, c := range cases {
		if got := FormatAnnotation(c.severity, c.message, c.loc); got != c.want {
			t.Errorf("FormatAnnotation(%q, %q, %+v) = %q, want %q", c.severity, c.message, c.loc, got, c.want)
		}
	}
}
