package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// 第三方平台的写法与限制只在分发服务的平台文件里（分发框架边界 2）。排版工具外发这一侧的代码里出现平台名，
// 就是有人写了「如果是某平台就…」，平台一多就乱：平台差别写进分发服务 service/platforms/<平台>.mjs，
// 排版工具只认 GET /v1/platforms 给的数。注释里提平台不算。
// 例外 publish_external_links.go：头条号文章链接自动查找还在排版工具（要 A 机的 TikHub 密钥），以后搬去分发服务。
func TestDistributionCodeNamesNoPlatform(t *testing.T) {
	platforms, err := readTestDistributionPlatforms()
	if err != nil {
		t.Fatal(err)
	}
	var terms []string
	for _, p := range platforms {
		terms = append(terms, p.ID, p.Name, strings.TrimSuffix(p.Name, "号"))
	}
	var files []string
	for _, pattern := range []string{"distribution_*.go", "publish_external_*.go"} {
		matched, _ := filepath.Glob(pattern)
		files = append(files, matched...)
	}
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || file == "publish_external_links.go" {
			continue
		}
		checked++
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
					t.Errorf("%s 出现了平台名「%s」（%s）：平台差别写进分发服务这个平台的文件", fset.Position(node.Pos()), term, code)
				}
			}
			return true
		})
	}
	if checked < 10 {
		t.Fatalf("只查了 %d 个文件，文件名规则变了？", checked)
	}
}
