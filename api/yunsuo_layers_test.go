package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// 多平台分发版式分两层（红线 43）：通用层写各平台都一样的规则，平台之间的差别只进平台层
// yunsuo_platform_profiles.go。通用层代码里出现平台名，就是有人写了「如果是某平台就…」，
// 平台一多就乱。注释里提平台不算。
func TestDistributionGeneralLayerNamesNoPlatform(t *testing.T) {
	var terms []string
	for key, profile := range yunsuoPlatformProfiles {
		terms = append(terms, key, profile.Name, strings.TrimSuffix(profile.Name, "号"))
	}
	for _, file := range []string{"yunsuo_distribution_rules.go", "yunsuo_render.go"} {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			var code string
			switch n := node.(type) {
			case *ast.Ident:
				code = n.Name
			case *ast.BasicLit:
				code = n.Value
			default:
				return true
			}
			for _, term := range terms {
				if strings.Contains(strings.ToLower(code), strings.ToLower(term)) {
					t.Errorf("%s 是通用层，出现了平台名「%s」（%s）：平台差别写进 yunsuo_platform_profiles.go 这个平台的 profile",
						fset.Position(node.Pos()), term, code)
				}
			}
			return true
		})
	}
}
