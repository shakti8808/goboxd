// Package structure_test enforces the architecture spec §5 dependency
// graph at CI time (Property 32).
//
// The test parses `go list -f '{{...}}' ./...` output and fails if any
// package in cmd/goboxd or internal/ imports a sibling that is not on
// the declared adjacency list, or if the resulting graph contains a
// cycle.
//
// Phase 1 enforces only the subset of internal/ packages that exists at
// this stage (config, log, version, metrics, security, registry, api,
// api/handlers); later phases extend the allowed-edge map as packages
// land. The default-deny check (any unknown internal/ package counts as
// a violation) catches accidentally-added packages too.
package structure_test

import (
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/thesouldev/goboxd"

// allowedEdges encodes the adjacency map from architecture spec §5.
//
// The value slice for each key is the COMPLETE set of internal/ packages
// the key may import. No edge outside this set is permitted; that
// guarantees Property 32 acyclicity by construction.
var allowedEdges = map[string][]string{
	"cmd/goboxd": {
		"internal/api",
		"internal/api/handlers",
		"internal/config",
		"internal/log",
		"internal/metrics",
		"internal/registry",
		"internal/version",
		"internal/runner",
		"internal/security",
		"internal/worker",
	},
	"internal/version":  {},
	"internal/config":   {},
	"internal/metrics":  {},
	"internal/log":      {},
	"internal/security": {},
	"internal/registry": {
		"internal/security",
	},
	"internal/runner": {
		"internal/security",
	},
	"internal/worker": {
		"internal/runner",
	},
	"internal/api/handlers": {
		"internal/registry",
		"internal/runner",
		"internal/security",
		"internal/worker",
	},
	"internal/api":          {},
}

// goPackage is the slice of `go list -json ./...` output we care about.
type goPackage struct {
	ImportPath string
	Imports    []string
}

// TestPackageDependencies asserts that every package edge in this module
// is declared in allowedEdges.
func TestPackageDependencies(t *testing.T) {
	pkgs := listPackages(t)

	// Build a relative-path import graph so the assertions are
	// independent of the module path.
	graph := map[string][]string{}
	for _, p := range pkgs {
		rel := strings.TrimPrefix(p.ImportPath, modulePath+"/")
		// Skip test-only packages (the structure test itself, etc.).
		if rel == "tests" || strings.HasPrefix(rel, "tests/") {
			continue
		}
		var deps []string
		for _, imp := range p.Imports {
			if !strings.HasPrefix(imp, modulePath+"/") {
				continue
			}
			deps = append(deps, strings.TrimPrefix(imp, modulePath+"/"))
		}
		sort.Strings(deps)
		graph[rel] = deps
	}

	for pkg, deps := range graph {
		allowed, declared := allowedEdges[pkg]
		if !declared {
			t.Errorf("undeclared package %s — add it to allowedEdges or remove the package", pkg)
			continue
		}
		allowedSet := map[string]struct{}{}
		for _, a := range allowed {
			allowedSet[a] = struct{}{}
		}
		for _, dep := range deps {
			if _, ok := allowedSet[dep]; !ok {
				t.Errorf("forbidden edge: %s imports %s (not in allowedEdges)", pkg, dep)
			}
		}
	}

	// Catch declared packages that don't yet exist in the tree (e.g. a
	// stale entry in allowedEdges).
	for declared := range allowedEdges {
		if _, ok := graph[declared]; !ok && declared != "" {
			// cmd/goboxd lives at the root of cmd/, internal/ packages
			// always show up in graph; if one is absent it is acceptable
			// for now because Phase 1 ships a strict subset. Surface as
			// info, not a failure.
			t.Logf("note: package %s declared in allowedEdges but not present in build tree", declared)
		}
	}

	if cycles := findCycles(graph); len(cycles) > 0 {
		t.Errorf("dependency cycles detected: %v", cycles)
	}
}

// listPackages runs `go list -json ./...` and parses the JSON stream.
func listPackages(t *testing.T) []goPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = ".."
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("go list: %v\n%s", err, stderr)
	}

	dec := json.NewDecoder(strings.NewReader(string(out)))
	var pkgs []goPackage
	for {
		var p goPackage
		if err := dec.Decode(&p); err != nil {
			break
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// findCycles returns any cycles in graph using DFS.
//
// Returns a slice of slices where each inner slice is the cycle's path
// in visit order; the slice is empty for an acyclic graph.
func findCycles(graph map[string][]string) [][]string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	colour := map[string]int{}
	stack := []string{}
	var cycles [][]string

	var visit func(node string) bool
	visit = func(node string) bool {
		colour[node] = grey
		stack = append(stack, node)
		for _, dep := range graph[node] {
			switch colour[dep] {
			case white:
				if visit(dep) {
					return true
				}
			case grey:
				// Cycle detected from `dep` up to the current top of stack.
				start := 0
				for i, s := range stack {
					if s == dep {
						start = i
						break
					}
				}
				cycle := append([]string{}, stack[start:]...)
				cycle = append(cycle, dep)
				cycles = append(cycles, cycle)
			}
		}
		colour[node] = black
		stack = stack[:len(stack)-1]
		return false
	}

	keys := make([]string, 0, len(graph))
	for k := range graph {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, node := range keys {
		if colour[node] == white {
			_ = visit(node)
		}
	}
	return cycles
}
