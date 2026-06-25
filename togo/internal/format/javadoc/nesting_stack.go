// Port of src/format/javadoc/nesting-stack.ts.

package javadoc

// nestingStack is a generic nesting stack (lexer HTML/code/table contexts).
type nestingStack[E comparable] struct {
	stack []E
}

func (s *nestingStack[E]) push(value E) { s.stack = append(s.stack, value) }

func (s *nestingStack[E]) popIfIn(values []E) (E, bool) {
	var zero E
	if len(s.stack) == 0 || !contains(values, s.stack[len(s.stack)-1]) {
		return zero, false
	}
	v := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	return v, true
}

// popUntil: if the stack contains value, pop it and everything above it.
func (s *nestingStack[E]) popUntil(value E) {
	if !contains(s.stack, value) {
		return
	}
	for {
		v := s.stack[len(s.stack)-1]
		s.stack = s.stack[:len(s.stack)-1]
		if v == value {
			return
		}
	}
}

func (s *nestingStack[E]) containsAny(values []E) bool {
	for _, e := range s.stack {
		if contains(values, e) {
			return true
		}
	}
	return false
}

func (s *nestingStack[E]) isEmpty() bool { return len(s.stack) == 0 }

func contains[E comparable](xs []E, v E) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// intNestingStack tracks a running total (writer list/footer indent levels).
type intNestingStack struct {
	stack []int
	total int
}

func (s *intNestingStack) push(value int) {
	s.stack = append(s.stack, value)
	s.total += value
}

func (s *intNestingStack) popIfNotEmpty() {
	if n := len(s.stack); n > 0 {
		s.total -= s.stack[n-1]
		s.stack = s.stack[:n-1]
	}
}

func (s *intNestingStack) isEmpty() bool { return len(s.stack) == 0 }

func (s *intNestingStack) reset() {
	s.stack = s.stack[:0]
	s.total = 0
}
