package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageCommentFailure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pkgName string
		doc     string
		want    string
	}{
		{
			name:    "library package comment",
			pkgName: "composefile",
			doc:     "// Package composefile parses Compose YAML.",
		},
		{
			name:    "command package comment",
			pkgName: "main",
			doc:     "// Command fibe-distilled runs the server.",
		},
		{
			name:    "missing comment",
			pkgName: "storage",
			want:    "missing package comment",
		},
		{
			name:    "wrong library prefix",
			pkgName: "runtime",
			doc:     "// Runtime executes remote commands.",
			want:    `package comment must start with "Package runtime"`,
		},
		{
			name:    "wrong command prefix",
			pkgName: "main",
			doc:     "// Package main runs a tool.",
			want:    `command package comment must start with "Command "`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := packageCommentFailure("internal/pkg/doc.go", tc.pkgName, packageDocGroup(tc.doc))
			if tc.want == "" && got != "" {
				t.Fatalf("expected no failure, got %q", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("expected failure containing %q, got %q", tc.want, got)
			}
		})
	}
}

func packageDocGroup(doc string) *ast.CommentGroup {
	if doc != "" {
		return &ast.CommentGroup{List: []*ast.Comment{{Text: doc}}}
	}
	return nil
}

func TestPackageMapFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dirs := []string{"internal/api", "internal/storage"}
	partialRows := packageRows("internal/api")
	partialFiles := packageFileRows("internal/api")
	if err := os.WriteFile(filepath.Join(root, packageMapFilename), []byte(packageMapFixture(partialRows, partialRows, partialFiles, "")), 0o600); err != nil {
		t.Fatalf("write package map: %v", err)
	}
	writeTestModule(t, root)
	writeTestPackage(t, root, "internal/api", "package api\n")
	writeTestPackage(t, root, "internal/storage", "package storage\n")

	failures := packageMapFailures(rootedFS(t, root), dirs)
	if !failureContains(failures, "missing responsibilities row for `internal/storage`") {
		t.Fatalf("expected missing storage package, got %#v", failures)
	}

	completeRows := packageRows("internal/api", "internal/storage")
	completeFiles := packageFileRows("internal/api", "internal/storage")
	if err := os.WriteFile(filepath.Join(root, packageMapFilename), []byte(packageMapFixture(completeRows, completeRows, completeFiles, "")), 0o600); err != nil {
		t.Fatalf("write complete package map: %v", err)
	}
	if failures := packageMapFailures(rootedFS(t, root), dirs); len(failures) != 0 {
		t.Fatalf("expected complete package map, got %#v", failures)
	}
}

func TestPackageMapDependencyFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dirs := []string{"internal/api", "internal/storage"}
	writeTestModule(t, root)
	writeTestPackage(t, root, "internal/api", `package api

import _ "example.test/mini/internal/storage"
`)
	writeTestPackage(t, root, "internal/storage", "package storage\n")
	rows := packageRows("internal/api", "internal/storage")
	files := packageFileRows("internal/api", "internal/storage")
	body := packageMapFixture(rows, rows, files, "")
	if err := os.WriteFile(filepath.Join(root, packageMapFilename), []byte(body), 0o600); err != nil {
		t.Fatalf("write package map: %v", err)
	}

	failures := packageMapFailures(rootedFS(t, root), dirs)
	if len(failures) != 1 || !strings.Contains(failures[0], "`internal/api` -> `internal/storage`") {
		t.Fatalf("expected missing api-storage edge, got %#v", failures)
	}

	body = packageMapFixture(rows, rows, files, "- `internal/api` -> `internal/storage`\n")
	if err := os.WriteFile(filepath.Join(root, packageMapFilename), []byte(body), 0o600); err != nil {
		t.Fatalf("write package map with edge: %v", err)
	}
	if failures := packageMapFailures(rootedFS(t, root), dirs); len(failures) != 0 {
		t.Fatalf("expected complete dependency map, got %#v", failures)
	}
}

func TestPackageMapStructureFailures(t *testing.T) {
	t.Parallel()

	failures := packageMapStructureFailures("package list only")
	for _, want := range []string{
		`missing section "## Package Responsibilities"`,
		`missing section "## Dependency Graph"`,
		`missing marker "https://go.dev/blog/godoc"`,
		"missing marker \"```mermaid\"",
		`missing marker "graph TD"`,
		`missing marker "every production function"`,
		"missing marker \"Packages only use `helpers.go` when there is real local glue.\"",
	} {
		if !failureContains(failures, want) {
			t.Fatalf("expected failure containing %q, got %#v", want, failures)
		}
	}

	if failures := packageMapStructureFailures(packageMapFixture("", "", "", "")); len(failures) != 0 {
		t.Fatalf("expected complete package-map structure, got %#v", failures)
	}
}

func TestCheckFileDeclsRequiresFunctionMethodTypeConstAndVarComments(t *testing.T) {
	t.Parallel()

	source := `package sample

// documentedConst is documented.
const documentedConst = "ok"

const undocumentedConst = "bad"

// documentedVar is documented.
var documentedVar = "ok"

var undocumentedVar = "bad"

// documentedType is documented.
type documentedType struct{}

type undocumentedType struct{}

// documentedFunc is documented.
func documentedFunc() {}

func undocumentedFunc() {}

// receiver is documented.
type receiver struct{}

// documentedMethod is documented.
func (receiver) documentedMethod() {}

func (receiver) undocumentedMethod() {}
`
	set, file := parseTestFile(t, source)

	failures := checkFileDecls(set, file)
	for _, want := range []string{
		"missing comment for undocumentedConst",
		"missing comment for undocumentedVar",
		"missing comment for undocumentedType",
		"missing comment for undocumentedFunc",
		"missing comment for undocumentedMethod",
	} {
		if !failureContains(failures, want) {
			t.Fatalf("expected failure containing %q, got %#v", want, failures)
		}
	}
	if failureContains(failures, "missing comment for documentedFunc") {
		t.Fatalf("documented declarations should not fail, got %#v", failures)
	}
}

func TestCheckFileDeclsRequiresExportedStructAndInterfaceMemberComments(t *testing.T) {
	t.Parallel()

	source := `package sample

// ExportedStruct is documented.
type ExportedStruct struct {
	// Documented is documented.
	Documented string
	Undocumented string
	private string
}

// privateStruct is documented.
type privateStruct struct {
	ExportedButPrivateType string
}

// ExportedInterface is documented.
type ExportedInterface interface {
	// DocumentedMethod is documented.
	DocumentedMethod()
	UndocumentedMethod()
	privateMethod()
}
`
	set, file := parseTestFile(t, source)

	failures := checkFileDecls(set, file)
	for _, want := range []string{
		"missing comment for ExportedStruct.Undocumented",
		"missing comment for ExportedInterface.UndocumentedMethod",
	} {
		if !failureContains(failures, want) {
			t.Fatalf("expected failure containing %q, got %#v", want, failures)
		}
	}
	for _, unwanted := range []string{
		"Documented",
		"private",
		"ExportedButPrivateType",
		"privateMethod",
	} {
		if failureContains(failures, unwanted) {
			t.Fatalf("unexpected failure containing %q in %#v", unwanted, failures)
		}
	}
}

func TestCheckFileDeclsRequiresExportedCommentPrefixes(t *testing.T) {
	t.Parallel()

	source := `package sample

// Something else documents the const.
const ExportedConst = "bad"

// Something else documents the var.
var ExportedVar = "bad"

// Exported values share a group-level description.
const (
	GroupedExported = "ok"
)

const (
	// Something else documents the grouped const.
	DirectGroupedExported = "bad"
)

// Something else documents the type.
type ExportedType struct {
	// Wrong documents the field.
	ExportedField string
}

// Something else documents the function.
func ExportedFunc() {}

// ExportedInterface is documented.
type ExportedInterface interface {
	// Wrong documents the method.
	ExportedMethod()
}
`
	set, file := parseTestFile(t, source)

	failures := checkFileDecls(set, file)
	for _, want := range []string{
		`comment for ExportedConst must start with "ExportedConst"`,
		`comment for ExportedVar must start with "ExportedVar"`,
		`comment for DirectGroupedExported must start with "DirectGroupedExported"`,
		`comment for ExportedType must start with "ExportedType"`,
		`comment for ExportedType.ExportedField must start with "ExportedField"`,
		`comment for ExportedFunc must start with "ExportedFunc"`,
		`comment for ExportedInterface.ExportedMethod must start with "ExportedMethod"`,
	} {
		if !failureContains(failures, want) {
			t.Fatalf("expected failure containing %q, got %#v", want, failures)
		}
	}
	if failureContains(failures, `comment for GroupedExported must start with "GroupedExported"`) {
		t.Fatalf("group-level constant docs should not need a per-name prefix, got %#v", failures)
	}
}

func TestDocFilePackageCommentFailureRequiresDocGoComment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	pkgDir := filepath.Join(root, "internal", "sample")
	if err := os.MkdirAll(pkgDir, 0o700); err != nil {
		t.Fatalf("mkdir package: %v", err)
	}
	writeTestModule(t, root)
	writeDoccheckSampleFile(t, pkgDir, "sample.go", "// Package sample is in the wrong file.\npackage sample\n")
	assertDocFilePackageCommentFailure(t, root, "missing doc.go")
	writeDoccheckSampleFile(t, pkgDir, "doc.go", "package sample\n")
	assertDocFilePackageCommentFailure(t, root, "missing package comment")
	writeDoccheckSampleFile(t, pkgDir, "doc.go", "// Package sample is documented.\npackage sample\n")
	assertDocFilePackageCommentOK(t, root)
}

func writeDoccheckSampleFile(t *testing.T, pkgDir string, name string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(pkgDir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func assertDocFilePackageCommentFailure(t *testing.T, root string, want string) {
	t.Helper()
	pkg, err := parsePackage(rootedFS(t, root), "internal/sample")
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	if got := docFilePackageCommentFailure(pkg.set, "internal/sample", pkg.name, pkg.files); !strings.Contains(got, want) {
		t.Fatalf("expected doc.go failure containing %q, got %q", want, got)
	}
}

func assertDocFilePackageCommentOK(t *testing.T, root string) {
	t.Helper()
	pkg, err := parsePackage(rootedFS(t, root), "internal/sample")
	if err != nil {
		t.Fatalf("parse package with documented doc.go: %v", err)
	}
	if got := docFilePackageCommentFailure(pkg.set, "internal/sample", pkg.name, pkg.files); got != "" {
		t.Fatalf("expected no doc.go failure, got %q", got)
	}
}

func rootedFS(t *testing.T, root string) fs.FS {
	t.Helper()
	handle, err := os.OpenRoot(root)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle.FS()
}

func parseTestFile(t *testing.T, source string) (*token.FileSet, *ast.File) {
	t.Helper()
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "/repo/sample/sample.go", source, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse test source: %v", err)
	}
	return set, file
}

func failureContains(failures []string, want string) bool {
	for _, failure := range failures {
		if strings.Contains(failure, want) {
			return true
		}
	}
	return false
}

func writeTestModule(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/mini\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

func writeTestPackage(t *testing.T, root string, name string, source string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "doc.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("write package %s: %v", name, err)
	}
}

func packageRows(packages ...string) string {
	var rows []string
	for _, pkg := range packages {
		rows = append(rows, "| `"+pkg+"` | documented | documented |")
	}
	return strings.Join(rows, "\n")
}

func packageFileRows(packages ...string) string {
	var rows []string
	for _, pkg := range packages {
		rows = append(rows, "| `"+pkg+"` | `"+pkg+"/doc.go` | documented |")
	}
	return strings.Join(rows, "\n")
}

func packageMapFixture(responsibilities string, goals string, files string, edges string) string {
	return strings.Join([]string{
		"# fibe-distilled Package Map And Godoc Contract",
		"https://go.dev/blog/godoc",
		"## Package Responsibilities",
		responsibilities,
		"## Package Interactions",
		responsibilities,
		"## Goal Contribution",
		goals,
		"## Godoc And Package Files",
		"every production function is documented",
		"Packages only use `helpers.go` when there is real local glue.",
		files,
		"## Dependency Graph",
		"```mermaid",
		"graph TD",
		"```",
		edges,
		"## Runtime Flow",
		"## Helper Policy",
		"## Godoc Expectations",
	}, "\n")
}
