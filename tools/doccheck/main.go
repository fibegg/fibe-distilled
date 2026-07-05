package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// productionPackageRoots are the source roots covered by the Godoc contract.
var productionPackageRoots = []string{"cmd", "internal", "tools"}

// main exits non-zero when the local Godoc contract is violated.
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run checks package comments, declaration comments, exported-doc conventions,
// and the package-map document.
func run() error {
	rootPath, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	rootFS := root.FS()
	packages, err := productionPackages(rootFS)
	if err != nil {
		return err
	}
	failures := packageMapFailures(rootFS, packages)
	for _, pkg := range packages {
		failures = append(failures, checkPackage(rootFS, pkg)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return fmt.Errorf("godoc contract failed:\n%s", strings.Join(failures, "\n"))
	}
	return nil
}

// productionPackages returns command, internal, and tool package directories.
func productionPackages(root fs.FS) ([]string, error) {
	var dirs []string
	for _, base := range productionPackageRoots {
		found, err := productionPackagesUnder(root, base)
		if err != nil {
			return nil, err
		}
		dirs = append(dirs, found...)
	}
	sort.Strings(dirs)
	return dirs, nil
}

// productionPackagesUnder returns package directories below one source root.
func productionPackagesUnder(root fs.FS, base string) ([]string, error) {
	var dirs []string
	err := fs.WalkDir(root, base, collectProductionPackageDirs(root, &dirs))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return dirs, err
}

// collectProductionPackageDirs builds a WalkDir callback for package discovery.
func collectProductionPackageDirs(root fs.FS, dirs *[]string) fs.WalkDirFunc {
	return func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && hasProductionGoFile(root, path) {
			*dirs = append(*dirs, path)
		}
		return nil
	}
}

// checkPackage verifies the documentation rules for one package.
func checkPackage(root fs.FS, dir string) []string {
	pkg, err := parsePackage(root, dir)
	if err != nil {
		return []string{dir + ": " + err.Error()}
	}
	var failures []string
	if failure := docFilePackageCommentFailure(pkg.set, dir, pkg.name, pkg.files); failure != "" {
		failures = append(failures, failure)
	}
	for _, file := range pkg.files {
		failures = append(failures, checkFileDecls(pkg.set, file)...)
	}
	return failures
}

// parsedPackage holds an AST package and the file set for positions.
type parsedPackage struct {
	set   *token.FileSet
	name  string
	files []*ast.File
}

// parsePackage parses non-test Go files in a package directory.
func parsePackage(root fs.FS, dir string) (parsedPackage, error) {
	set := token.NewFileSet()
	names, err := productionGoFileNames(root, dir)
	if err != nil {
		return parsedPackage{}, err
	}
	files, err := parsePackageFiles(root, dir, set, names)
	if err != nil {
		return parsedPackage{}, err
	}
	if len(files) == 0 {
		return parsedPackage{}, errors.New("no package parsed")
	}
	sortParsedPackageFiles(set, files)
	return parsedPackage{set: set, name: files[0].Name.Name, files: files}, nil
}

// productionGoFileNames returns non-test Go source names in one package.
func productionGoFileNames(root fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(root, dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// parsePackageFiles parses named package source files with comments.
func parsePackageFiles(root fs.FS, dir string, set *token.FileSet, names []string) ([]*ast.File, error) {
	files := make([]*ast.File, 0, len(names))
	for _, name := range names {
		file, err := parsePackageFile(root, dir, set, name)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

// parsePackageFile parses one source file with comments.
func parsePackageFile(root fs.FS, dir string, set *token.FileSet, name string) (*ast.File, error) {
	filename := path.Join(dir, name)
	body, err := fs.ReadFile(root, filename)
	if err != nil {
		return nil, err
	}
	return parser.ParseFile(set, filename, body, parser.ParseComments)
}

// sortParsedPackageFiles orders parsed files by their source filename.
func sortParsedPackageFiles(set *token.FileSet, files []*ast.File) {
	sort.Slice(files, func(i, j int) bool {
		return set.Position(files[i].FileStart).Filename < set.Position(files[j].FileStart).Filename
	})
}

// checkFileDecls returns comment failures for declarations in one file.
func checkFileDecls(set *token.FileSet, file *ast.File) []string {
	failures := make([]string, 0, len(file.Decls))
	for _, decl := range file.Decls {
		failures = append(failures, checkDeclDoc(set, decl)...)
	}
	return failures
}

// checkDeclDoc routes one declaration to the matching documentation check.
func checkDeclDoc(set *token.FileSet, decl ast.Decl) []string {
	switch item := decl.(type) {
	case *ast.FuncDecl:
		return checkFuncDeclDoc(set, item)
	case *ast.GenDecl:
		return checkGenDeclDocs(set, item)
	default:
		return nil
	}
}

// checkFuncDeclDoc verifies one function or method declaration comment.
func checkFuncDeclDoc(set *token.FileSet, decl *ast.FuncDecl) []string {
	if declarationNeedsComment(decl.Name.Name) && missingDoc(decl.Doc) {
		return []string{missingComment(set, decl.Pos(), decl.Name.Name)}
	}
	if decl.Name.IsExported() && !commentStartsWith(decl.Doc, decl.Name.Name) {
		return []string{prefixComment(set, decl.Pos(), decl.Name.Name)}
	}
	return nil
}

// checkGenDeclDocs verifies type, const, and var spec comments.
func checkGenDeclDocs(set *token.FileSet, decl *ast.GenDecl) []string {
	if decl.Tok == token.IMPORT {
		return nil
	}
	var failures []string
	for _, spec := range decl.Specs {
		if failure := checkSpecDoc(set, decl, spec); failure != "" {
			failures = append(failures, failure)
		}
		if typeSpec, ok := spec.(*ast.TypeSpec); ok {
			failures = append(failures, checkTypeMemberDocs(set, typeSpec)...)
		}
	}
	return failures
}

// checkSpecDoc verifies one type, const, or var spec comment.
func checkSpecDoc(set *token.FileSet, decl *ast.GenDecl, spec ast.Spec) string {
	name := specName(spec)
	doc := effectiveSpecComment(decl, spec)
	if declarationNeedsComment(name) && missingDoc(doc) {
		return missingComment(set, spec.Pos(), name)
	}
	if failure := checkTypeSpecPrefix(set, spec, doc); failure != "" {
		return failure
	}
	if failure := checkValueSpecPrefix(set, decl, spec, doc); failure != "" {
		return failure
	}
	return ""
}

// checkTypeSpecPrefix verifies the exported identifier prefix for type docs.
func checkTypeSpecPrefix(set *token.FileSet, spec ast.Spec, doc *ast.CommentGroup) string {
	typeSpec, ok := spec.(*ast.TypeSpec)
	if !ok || !typeSpec.Name.IsExported() || commentStartsWith(doc, typeSpec.Name.Name) {
		return ""
	}
	return prefixComment(set, spec.Pos(), typeSpec.Name.Name)
}

// checkValueSpecPrefix verifies the exported identifier prefix for const/var docs.
func checkValueSpecPrefix(set *token.FileSet, decl *ast.GenDecl, spec ast.Spec, doc *ast.CommentGroup) string {
	valueSpec, ok := spec.(*ast.ValueSpec)
	if !ok {
		return ""
	}
	exportedName, ok := singleExportedValueName(valueSpec)
	if !ok || !valueSpecNeedsPrefix(decl, valueSpec) || commentStartsWith(doc, exportedName) {
		return ""
	}
	return prefixComment(set, spec.Pos(), exportedName)
}

// checkTypeMemberDocs verifies exported members that appear in generated docs.
func checkTypeMemberDocs(set *token.FileSet, spec *ast.TypeSpec) []string {
	if spec == nil || !spec.Name.IsExported() {
		return nil
	}
	switch expr := spec.Type.(type) {
	case *ast.StructType:
		return checkNamedFieldDocs(set, spec.Name.Name, expr.Fields.List)
	case *ast.InterfaceType:
		return checkNamedFieldDocs(set, spec.Name.Name, expr.Methods.List)
	default:
		return nil
	}
}

// checkNamedFieldDocs verifies exported named fields or interface methods.
func checkNamedFieldDocs(set *token.FileSet, typeName string, fields []*ast.Field) []string {
	var failures []string
	for _, field := range fields {
		for _, name := range field.Names {
			failures = append(failures, checkNamedFieldDoc(set, typeName, field, name)...)
		}
	}
	return failures
}

// checkNamedFieldDoc verifies one exported struct field or interface method.
func checkNamedFieldDoc(set *token.FileSet, typeName string, field *ast.Field, name *ast.Ident) []string {
	if name == nil || !name.IsExported() {
		return nil
	}
	fullName := typeName + "." + name.Name
	var failures []string
	if missingFieldDoc(field) {
		failures = append(failures, missingComment(set, name.Pos(), fullName))
	}
	if !fieldCommentStartsWith(field, name.Name) {
		failures = append(failures, prefixComment(set, name.Pos(), fullName))
	}
	return failures
}

// missingComment formats a single declaration comment failure.
func missingComment(set *token.FileSet, pos token.Pos, name string) string {
	location := set.Position(pos)
	return fmt.Sprintf("%s:%d: missing comment for %s", location.Filename, location.Line, name)
}

// prefixComment formats a declaration comment that does not start with the name
// Go documentation convention expects.
func prefixComment(set *token.FileSet, pos token.Pos, name string) string {
	location := set.Position(pos)
	return fmt.Sprintf("%s:%d: comment for %s must start with %q", location.Filename, location.Line, name, expectedCommentPrefix(name))
}
