package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
)

// This gate runs before the behavioral test: all bot construction is confined
// to the existing user-configured factory, including both lottery branches.
func assertConfiguredBotConstruction(t *testing.T) {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "tgbot.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range f.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if fn.Name.Name == "SendReport" {
				if _, ok := n.(*ast.GoStmt); ok {
					t.Fatal("asynchronous external report restored")
				}
			}
			if branch, ok := n.(*ast.CaseClause); ok {
				for _, label := range branch.List {
					literal, ok := label.(*ast.BasicLit)
					if !ok || literal.Value != `"lottery_play"` {
						continue
					}
					for _, statement := range branch.Body {
						ast.Inspect(statement, func(child ast.Node) bool {
							if _, ok := child.(*ast.GoStmt); ok {
								t.Fatal("lottery external report goroutine restored")
							}
							return true
						})
					}
				}
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "telego" && sel.Sel.Name == "NewBot" && fn.Name.Name != "NewBot" {
				t.Fatal("unauthorized bot construction outside configured factory")
			}
			return true
		})
	}
}

// Do not embed a real or token-shaped credential in this regression test.
func TestTelegramSourceHasNoHardcodedCredential(t *testing.T) {
	assertConfiguredBotConstruction(t)
	source, err := os.ReadFile("tgbot.go")
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`[0-9]{6,}:[A-Za-z0-9_-]{20,}`).Match(source) {
		t.Fatal("Telegram credential found in source (value withheld)")
	}
	if strings.Contains(string(source), "REPORT_CHAT_IDS") {
		t.Fatal("foreign report destinations remain in source")
	}
}
