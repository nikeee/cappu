package compiler

// Port of src/compiler/narrowing.ts: flow-aware nullness narrowing (nikeee/cappu#25).
// A syntactic dominator walk - not a full control-flow graph - that figures out what
// the code has proven about a local/parameter at a use site, the analog of
// TypeScript's getTypeOfSymbolAtLocation. Covers if/else guards, early-exit
// (if (x==null) return), && / || short-circuit, ternary, instanceof,
// Objects.requireNonNull / assert, and reassignment. Loops' back-edges and
// branch-merge of reassignments are not modeled.

// resolveRef resolves a bare reference node to its symbol (the checker's ResolveName).
type resolveRef func(node *Node) *Symbol

// exprNullnessFn is the provable nullness of a value expression (null literal, `new`,
// @NonNull, ...).
type exprNullnessFn func(node *Node) Nullness

// facts is what a boolean condition proves about a symbol when true / when false.
type facts struct {
	whenTrue  Nullness
	whenFalse Nullness
}

func isNullLiteral(n *Node) bool { return n.Kind == NullKeyword }

// refersToSymbol reports whether node is a bare reference (`x`) to symbol. Locals and
// params are referenced by a plain Identifier, so a qualified `this.x` is not matched.
func refersToSymbol(node *Node, symbol *Symbol, resolve resolveRef) bool {
	return node.Kind == Identifier && resolve(node) == symbol
}

// calleeName is the simple (unqualified) name of a call's callee.
func calleeName(call *CallExpressionData) string {
	switch call.Expression.Kind {
	case Identifier:
		return call.Expression.AsIdentifier().Text
	case PropertyAccessExpression:
		return call.Expression.AsPropertyAccessExpression().Name.AsIdentifier().Text
	}
	return ""
}

func conditionImplies(cond *Node, symbol *Symbol, resolve resolveRef) facts {
	switch cond.Kind {
	case ParenthesizedExpression:
		return conditionImplies(cond.AsParenthesizedExpression().Expression, symbol, resolve)
	case PrefixUnaryExpression:
		u := cond.AsPrefixUnaryExpression()
		if u.Operator != ExclamationToken {
			return facts{}
		}
		f := conditionImplies(u.Operand, symbol, resolve)
		return facts{whenTrue: f.whenFalse, whenFalse: f.whenTrue}
	case BinaryExpression:
		b := cond.AsBinaryExpression()
		switch b.OperatorToken {
		case EqualsEqualsToken, ExclamationEqualsToken:
			isNullCheck := (refersToSymbol(b.Left, symbol, resolve) && isNullLiteral(b.Right)) ||
				(refersToSymbol(b.Right, symbol, resolve) && isNullLiteral(b.Left))
			if !isNullCheck {
				return facts{}
			}
			// `x == null`: true => null, false => non-null. `x != null`: the inverse.
			if b.OperatorToken == EqualsEqualsToken {
				return facts{whenTrue: NullnessNullable, whenFalse: NullnessNonNull}
			}
			return facts{whenTrue: NullnessNonNull, whenFalse: NullnessNullable}
		case AmpersandAmpersandToken:
			l := conditionImplies(b.Left, symbol, resolve)
			r := conditionImplies(b.Right, symbol, resolve)
			return facts{whenTrue: orNullness(l.whenTrue, r.whenTrue)}
		case BarBarToken:
			l := conditionImplies(b.Left, symbol, resolve)
			r := conditionImplies(b.Right, symbol, resolve)
			return facts{whenFalse: orNullness(l.whenFalse, r.whenFalse)}
		}
		return facts{}
	case InstanceofExpression:
		// `x instanceof T` is false for null, so true proves x non-null.
		if refersToSymbol(cond.AsInstanceofExpression().Expression, symbol, resolve) {
			return facts{whenTrue: NullnessNonNull}
		}
		return facts{}
	case CallExpression:
		call := cond.AsCallExpression()
		arg0 := firstArg(call)
		if arg0 == nil || !refersToSymbol(arg0, symbol, resolve) {
			return facts{}
		}
		switch calleeName(call) {
		case "nonNull": // Objects.nonNull(x)
			return facts{whenTrue: NullnessNonNull}
		case "isNull": // Objects.isNull(x)
			return facts{whenTrue: NullnessNullable, whenFalse: NullnessNonNull}
		}
		return facts{}
	}
	return facts{}
}

func orNullness(a, b Nullness) Nullness {
	if a != "" {
		return a
	}
	return b
}

func firstArg(call *CallExpressionData) *Node {
	if call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
		return nil
	}
	return call.Arguments.Nodes[0]
}

// definitelyExits reports whether a statement always completes abruptly (so code
// after it is unreachable): return/throw/break/continue, or a block ending in one.
func definitelyExits(stmt *Node) bool {
	if stmt == nil {
		return false
	}
	switch stmt.Kind {
	case ReturnStatement, ThrowStatement, BreakStatement, ContinueStatement:
		return true
	case Block:
		s := stmt.AsBlock().Statements
		return s != nil && len(s.Nodes) > 0 && definitelyExits(s.Nodes[len(s.Nodes)-1])
	}
	return false
}

// stmtFactKind classifies a preceding statement's effect on a symbol.
type stmtFactKind int

const (
	factNone   stmtFactKind = iota // not relevant; keep looking
	factNarrow                     // proves a nullness
	factReset                      // an assignment whose value is unprovable; stop
)

type stmtFact struct {
	kind     stmtFactKind
	nullness Nullness
}

func precedingStatementFact(stmt *Node, symbol *Symbol, resolve resolveRef, exprNullness exprNullnessFn) stmtFact {
	switch stmt.Kind {
	case ExpressionStatement:
		expr := stmt.AsExpressionStatement().Expression
		switch expr.Kind {
		case AssignmentExpression:
			a := expr.AsAssignmentExpression()
			if a.OperatorToken == EqualsToken && refersToSymbol(a.Left, symbol, resolve) {
				if n := exprNullness(a.Right); n != "" {
					return stmtFact{kind: factNarrow, nullness: n}
				}
				return stmtFact{kind: factReset}
			}
		case CallExpression:
			call := expr.AsCallExpression()
			arg0 := firstArg(call)
			if calleeName(call) == "requireNonNull" && arg0 != nil && refersToSymbol(arg0, symbol, resolve) {
				return stmtFact{kind: factNarrow, nullness: NullnessNonNull}
			}
		}
	case AssertStatement:
		if conditionImplies(stmt.AsAssertStatement().Condition, symbol, resolve).whenTrue == NullnessNonNull {
			return stmtFact{kind: factNarrow, nullness: NullnessNonNull}
		}
	case IfStatement:
		ifs := stmt.AsIfStatement()
		facts := conditionImplies(ifs.Condition, symbol, resolve)
		// Early-exit: if (COND) <abrupt>;  -> after the if, only the fall-through
		// path remains (e.g. if (x==null) return; -> non-null).
		if ifs.ElseStatement == nil && definitelyExits(ifs.ThenStatement) && facts.whenFalse != "" {
			return stmtFact{kind: factNarrow, nullness: facts.whenFalse}
		}
		// Branch-merge: if (x == null) x = <non-null>;  -> both the then-branch (just
		// assigned) and the fall-through (COND false, non-null) agree x is non-null.
		if ifs.ElseStatement == nil && facts.whenTrue == NullnessNullable &&
			!definitelyExits(ifs.ThenStatement) &&
			branchTrailingNullness(ifs.ThenStatement, symbol, resolve, exprNullness) == NullnessNonNull {
			return stmtFact{kind: factNarrow, nullness: NullnessNonNull}
		}
		// Soundness: any other assignment to x inside a branch leaves its state
		// unprovable here, so earlier facts must not leak past this if.
		if branchAssignsSymbol(ifs.ThenStatement, symbol, resolve) ||
			(ifs.ElseStatement != nil && branchAssignsSymbol(ifs.ElseStatement, symbol, resolve)) {
			return stmtFact{kind: factReset}
		}
	}
	return stmtFact{kind: factNone}
}

// assignedValue returns the RHS of `x = <expr>` as a statement, or nil.
func assignedValue(stmt *Node, symbol *Symbol, resolve resolveRef) *Node {
	if stmt.Kind != ExpressionStatement {
		return nil
	}
	expr := stmt.AsExpressionStatement().Expression
	if expr.Kind != AssignmentExpression {
		return nil
	}
	a := expr.AsAssignmentExpression()
	if a.OperatorToken != EqualsToken || !refersToSymbol(a.Left, symbol, resolve) {
		return nil
	}
	return a.Right
}

// branchStatements is the statements of a branch (a block's, or the statement itself).
func branchStatements(branch *Node) []*Node {
	if branch.Kind == Block {
		return branch.AsBlock().Statements.Nodes
	}
	return []*Node{branch}
}

func branchAssignsSymbol(branch *Node, symbol *Symbol, resolve resolveRef) bool {
	for _, s := range branchStatements(branch) {
		if assignedValue(s, symbol, resolve) != nil {
			return true
		}
	}
	return false
}

// branchTrailingNullness is the nullness a branch leaves the symbol with when its
// last statement assigns it, else "" (we only reason about a trailing assignment).
func branchTrailingNullness(branch *Node, symbol *Symbol, resolve resolveRef, exprNullness exprNullnessFn) Nullness {
	stmts := branchStatements(branch)
	if len(stmts) == 0 {
		return ""
	}
	if value := assignedValue(stmts[len(stmts)-1], symbol, resolve); value != nil {
		return exprNullness(value)
	}
	return ""
}

// narrowNullnessAt returns the narrowed nullness of symbol at use site `use`, or ""
// when nothing is proven (the declared nullness then applies). Caller must only pass
// locals/params.
func narrowNullnessAt(use *Node, symbol *Symbol, resolve resolveRef, exprNullness exprNullnessFn) Nullness {
	node := use
	for parent := node.Parent; parent != nil; node, parent = parent, parent.Parent {
		// Preceding statements in an enclosing block (nearest first). An assignment
		// or guard here is checked before the enclosing condition, so a write between
		// a guard and the use correctly invalidates the guard.
		if parent.Kind == Block {
			stmts := parent.AsBlock().Statements
			idx := indexOfNode(stmts, node)
			for i := idx - 1; i >= 0; i-- {
				fact := precedingStatementFact(stmts.Nodes[i], symbol, resolve, exprNullness)
				if fact.kind == factNarrow {
					return fact.nullness
				}
				if fact.kind == factReset {
					return ""
				}
			}
			continue
		}
		// Conditional branch position: inside a branch whose guard proves a fact.
		switch parent.Kind {
		case IfStatement:
			ifs := parent.AsIfStatement()
			f := conditionImplies(ifs.Condition, symbol, resolve)
			if node == ifs.ThenStatement && f.whenTrue != "" {
				return f.whenTrue
			}
			if node == ifs.ElseStatement && f.whenFalse != "" {
				return f.whenFalse
			}
		case ConditionalExpression:
			c := parent.AsConditionalExpression()
			f := conditionImplies(c.Condition, symbol, resolve)
			if node == c.WhenTrue && f.whenTrue != "" {
				return f.whenTrue
			}
			if node == c.WhenFalse && f.whenFalse != "" {
				return f.whenFalse
			}
		case BinaryExpression:
			b := parent.AsBinaryExpression()
			if node == b.Right {
				switch b.OperatorToken {
				case AmpersandAmpersandToken:
					if f := conditionImplies(b.Left, symbol, resolve).whenTrue; f != "" {
						return f
					}
				case BarBarToken:
					if f := conditionImplies(b.Left, symbol, resolve).whenFalse; f != "" {
						return f
					}
				}
			}
		case WhileStatement:
			// The body runs only when the condition held. (A do-while body runs once
			// before the test, so its condition is intentionally not consulted.)
			w := parent.AsWhileStatement()
			if node == w.Statement {
				if f := conditionImplies(w.Condition, symbol, resolve).whenTrue; f != "" {
					return f
				}
			}
		case ForStatement:
			fs := parent.AsForStatement()
			if node == fs.Statement && fs.Condition != nil {
				if f := conditionImplies(fs.Condition, symbol, resolve).whenTrue; f != "" {
					return f
				}
			}
		}
	}
	return ""
}
