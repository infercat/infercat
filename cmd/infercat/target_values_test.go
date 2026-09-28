package main

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/infercat/infercat/internal/machine"
)

func TestTargetFlagValuesStayLocal(t *testing.T) {
	for _, args := range [][]string{
		{"serve", "--upstream", "--host", "--help"},
		{"serve", "--upstream", "http://127.0.0.1:8000/--host", "--help"},
		{"connect", "--listen", "--host-file", "--help"},
		{"connect", "--configure", "--host", "--help"},
		{"setup", "--custom", "--host-file", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, errw bytes.Buffer
			args = append([]string{"--data-dir", t.TempDir()}, args...)
			code := run(context.Background(), args, &out, &errw, nil, false, newPlatform())
			if code != 0 || !strings.Contains(out.String(), "Usage:") || strings.Contains(errw.String(), "not available remotely") {
				t.Fatal(code, out.String(), errw.String())
			}
		})
	}
	for _, args := range [][]string{{"connect", "--", "--host", "127.0.0.1"}, {"keys", "add", "--", "--host-file"}} {
		clean, target, err := machine.ParseTarget(args, targetValueFlags())
		if err != nil || target.Remote() || strings.Join(clean, "\x00") != strings.Join(args, "\x00") {
			t.Fatal(clean, err)
		}
	}
}

// Guard the selector's literal-value inventory against additions to any public flag set.
func TestTargetCoversRegisteredValueFlags(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	values := targetValueFlags()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			receiver, ok := sel.X.(*ast.Ident)
			if !ok || receiver.Name != "fs" {
				return true
			}
			method := sel.Sel.Name
			at := 0
			switch method {
			case "String", "Int", "Int64", "Uint", "Uint64", "Float64", "Duration", "Func":
			case "StringVar", "IntVar", "Int64Var", "UintVar", "Uint64Var", "Float64Var", "DurationVar", "Var":
				at = 1
			default:
				return true
			}
			literal, ok := call.Args[at].(*ast.BasicLit)
			if !ok {
				t.Errorf("%s: nonliteral value flag needs explicit coverage", path)
				return true
			}
			name, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			if !values["--"+name] || !values["-"+name] {
				t.Errorf("%s: value flag %s missing from selector inventory", path, name)
			}
			return true
		})
	}
}
