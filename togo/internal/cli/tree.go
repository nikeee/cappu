package cli

// Port of src/cli/tree.ts.
//
// `cappu tree`: resolve each dependency configuration's transitive graph and
// print it as an indented tree (npm-ls / cargo-tree style), one section per
// configuration (api, implementation, annotationProcessor, testImplementation).
// --json emits the same forest machine-readable. Builds purely on the
// RequestedBy edges that ResolveTransitive records - no new graph code.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/packages"
	"github.com/nikeee/cappu/internal/sources"
)

type treeNode struct {
	Coordinate   string     `json:"coordinate"`
	Dependencies []treeNode `json:"dependencies"`
	// Unresolved marks a declared dependency no source could provide.
	Unresolved bool `json:"unresolved,omitempty"`
}

type treeSection struct {
	Configuration string     `json:"configuration"`
	Tree          []treeNode `json:"tree"`
}

type paintFunc = func(format, text string) string

func plainPaint(_, text string) string { return text }

// configRoots is the declared roots of one named configuration, as coordinates.
func configRoots(cfg *config.Config, configuration string) []packages.Coordinates {
	switch configuration {
	case "api":
		return sources.RootsOf(cfg.Dependencies.API)
	case "implementation":
		return sources.RootsOf(cfg.Dependencies.Implementation)
	case "annotationProcessor":
		return sources.RootsOf(cfg.Dependencies.AnnotationProcessor)
	case "testImplementation":
		return sources.RootsOf(cfg.Dependencies.TestImplementation)
	}
	return nil
}

// buildForest turns one configuration's resolution into a forest by following
// each package's single RequestedBy parent. Nearest-wins dedup means every
// package appears exactly once, so no shared-subtree markers are needed; the
// cycle guard is belt-and-suspenders against a pathological RequestedBy loop.
func buildForest(res packages.Resolution) []treeNode {
	childrenByParent := map[packages.PackageKey][]packages.ResolvedPackage{}
	var roots []packages.ResolvedPackage
	for _, p := range res.Packages {
		if p.RequestedBy == (packages.Coordinates{}) {
			roots = append(roots, p)
			continue
		}
		key := p.RequestedBy.Key()
		childrenByParent[key] = append(childrenByParent[key], p)
	}

	seen := map[packages.PackageKey]bool{}
	var toNode func(p packages.ResolvedPackage) treeNode
	toNode = func(p packages.ResolvedPackage) treeNode {
		key := p.Coordinates.Key()
		node := treeNode{Coordinate: string(p.Coordinates.String()), Dependencies: []treeNode{}}
		if seen[key] {
			return node
		}
		seen[key] = true
		for _, c := range childrenByParent[key] {
			node.Dependencies = append(node.Dependencies, toNode(c))
		}
		return node
	}

	forest := []treeNode{}
	for _, r := range roots {
		forest = append(forest, toNode(r))
	}
	// Surface declared roots that nothing could resolve - otherwise they vanish.
	for _, m := range res.Missing {
		if m.RequestedBy == (packages.Coordinates{}) {
			forest = append(forest, treeNode{Coordinate: string(m.Coordinates.String()), Dependencies: []treeNode{}, Unresolved: true})
		}
	}
	return forest
}

func renderNodes(nodes []treeNode, prefix string, paint paintFunc) []string {
	var lines []string
	for i, node := range nodes {
		last := i == len(nodes)-1
		label := node.Coordinate
		if node.Unresolved {
			label = node.Coordinate + " " + paint("yellow", "(unresolved)")
		}
		branch, cont := "├── ", "│   "
		if last {
			branch, cont = "└── ", "    "
		}
		lines = append(lines, prefix+branch+label)
		lines = append(lines, renderNodes(node.Dependencies, prefix+cont, paint)...)
	}
	return lines
}

// FormatTree renders the per-configuration forests as indented trees (pure).
func FormatTree(sections []treeSection, paint paintFunc) string {
	var blocks []string
	for _, s := range sections {
		if len(s.Tree) == 0 {
			continue
		}
		lines := []string{paint("bold", paint("cyan", s.Configuration))}
		lines = append(lines, renderNodes(s.Tree, "", paint)...)
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	if len(blocks) == 0 {
		return "no dependencies declared\n"
	}
	return strings.Join(blocks, "\n") + "\n"
}

// RunTree handles `cappu tree`. Port of runTree in src/cli/tree.ts.
func RunTree(cfg *config.Config, jsonOut bool) int {
	resolving := 0
	showProgress := ColorEnabled(isTTY(os.Stderr), os.Getenv)
	onResolve := func(packages.Coordinates) {
		if showProgress {
			resolving++
			fmt.Fprintf(os.Stderr, "\r\x1b[2Kresolving dependency graph (%d)...", resolving)
		}
	}

	sections := make([]treeSection, 0, len(config.Configurations))
	for _, configuration := range config.Configurations {
		res, err := packages.ResolveTransitive(configRoots(cfg, configuration), sources.Configured(cfg), onResolve)
		if err != nil {
			if resolving > 0 {
				fmt.Fprint(os.Stderr, "\r\x1b[2K")
			}
			fmt.Fprintf(os.Stderr, "cappu: %s\n", err)
			return 1
		}
		sections = append(sections, treeSection{Configuration: configuration, Tree: buildForest(res)})
	}
	if resolving > 0 {
		fmt.Fprint(os.Stderr, "\r\x1b[2K")
	}

	if jsonOut {
		out := make([]treeSection, 0, len(sections))
		for _, s := range sections {
			if len(s.Tree) > 0 {
				out = append(out, s)
			}
		}
		buf, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintf(os.Stdout, "%s\n", buf)
		return 0
	}

	fmt.Fprint(os.Stdout, FormatTree(sections, painter(os.Stdout)))
	return 0
}
