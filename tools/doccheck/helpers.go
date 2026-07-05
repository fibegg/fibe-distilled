package main

import (
	"cmp"
	"errors"
	"go/ast"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
)

// packageMapFilename is the architecture document kept in sync with packages.
const packageMapFilename = "PACKAGES.md"

// requiredPackageMapSections are the human-readable architecture sections the
// package map must keep.
var requiredPackageMapSections = []string{
	"## Package Responsibilities",
	"## Package Interactions",
	"## Goal Contribution",
	"## Godoc And Package Files",
	"## Dependency Graph",
	"## Runtime Flow",
	"## Helper Policy",
	"## Godoc Expectations",
}

// requiredPackageMapMarkers are non-heading markers that prove the package map
// can be used as a generated-doc companion.
var requiredPackageMapMarkers = []string{
	"https://go.dev/blog/godoc",
	"```mermaid",
	"graph TD",
	"every production function",
	"Packages only use `helpers.go` when there is real local glue.",
}

// hasProductionGoFile reports whether a directory has non-test Go files.
func hasProductionGoFile(root fs.FS, dir string) bool {
	entries, err := fs.ReadDir(root, dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			return true
		}
	}
	return false
}

// docFilePackageCommentFailure returns a failure when doc.go is missing or does
// not own the package comment.
func docFilePackageCommentFailure(set *token.FileSet, dir string, name string, files []*ast.File) string {
	docFile := packageDocFile(set, files)
	if docFile == nil {
		return dir + ": missing doc.go package documentation file"
	}
	return packageCommentFailure(dir+"/doc.go", name, docFile.Doc)
}

// packageCommentFailure returns the package-comment failure for one package doc.
func packageCommentFailure(location string, name string, docGroup *ast.CommentGroup) string {
	doc := packageComment(docGroup)
	if strings.TrimSpace(doc) == "" {
		return location + ": missing package comment"
	}
	if name == "main" {
		if !strings.HasPrefix(doc, "Command ") {
			return location + ": command package comment must start with \"Command \""
		}
		return ""
	}
	expected := "Package " + name
	if !strings.HasPrefix(doc, expected) {
		return location + ": package comment must start with " + strconv.Quote(expected)
	}
	return ""
}

// packageDocFile returns the parsed doc.go source file for a package.
func packageDocFile(set *token.FileSet, files []*ast.File) *ast.File {
	for _, file := range files {
		if path.Base(set.Position(file.FileStart).Filename) == "doc.go" {
			return file
		}
	}
	return nil
}

// packageComment returns the package Doc comment text from one file.
func packageComment(doc *ast.CommentGroup) string {
	if missingDoc(doc) {
		return ""
	}
	return strings.TrimSpace(doc.Text())
}

// packageMapFailures returns package directories missing from PACKAGES.md.
func packageMapFailures(root fs.FS, packageDirs []string) []string {
	body, err := readRootFile(root, packageMapFilename)
	if err != nil {
		return []string{packageMapFilename + ": " + err.Error()}
	}
	text := string(body)
	failures := packageMapStructureFailures(text)
	sections := packageMapSections(text)
	failures = append(failures, packageMapCoverageFailures(sections, packageDirs)...)
	failures = append(failures, packageDependencyFailures(root, packageDirs, text)...)
	return failures
}

// packageMapSectionSet holds the Markdown regions checked for package coverage.
type packageMapSectionSet struct {
	responsibilities string
	interactions     string
	goals            string
	files            string
}

// packageMapSections extracts the table sections used for package coverage.
func packageMapSections(packageMap string) packageMapSectionSet {
	return packageMapSectionSet{
		responsibilities: markdownSection(packageMap, "## Package Responsibilities"),
		interactions:     markdownSection(packageMap, "## Package Interactions"),
		goals:            markdownSection(packageMap, "## Goal Contribution"),
		files:            markdownSection(packageMap, "## Godoc And Package Files"),
	}
}

// packageMapCoverageFailures returns missing table rows for package docs.
func packageMapCoverageFailures(sections packageMapSectionSet, packageDirs []string) []string {
	failures := make([]string, 0, len(packageDirs))
	for _, dir := range packageDirs {
		failures = append(failures, packageMapPackageFailures(sections, dir)...)
	}
	return failures
}

// packageMapPackageFailures returns package-map misses for one package.
func packageMapPackageFailures(sections packageMapSectionSet, name string) []string {
	var failures []string
	failures = appendIfMissingPackageRow(failures, sections.responsibilities, name, "responsibilities row")
	failures = appendIfMissingPackageRow(failures, sections.interactions, name, "interactions row")
	failures = appendIfMissingPackageRow(failures, sections.goals, name, "goal row")
	doc := path.Join(name, "doc.go")
	if !hasPackageFileRow(sections.files, name, doc) {
		failures = append(failures, packageMapFilename+": missing package-file row for `"+name+"` with `"+doc+"`")
	}
	return failures
}

// appendIfMissingPackageRow appends a package-map row failure when absent.
func appendIfMissingPackageRow(failures []string, section string, name string, label string) []string {
	if hasPackageTableRow(section, name) {
		return failures
	}
	return append(failures, packageMapFilename+": missing "+label+" for `"+name+"`")
}

// packageMapStructureFailures returns failures for missing architecture sections.
func packageMapStructureFailures(packageMap string) []string {
	var failures []string
	for _, heading := range requiredPackageMapSections {
		if !strings.Contains(packageMap, heading) {
			failures = append(failures, packageMapFilename+": missing section "+strconv.Quote(heading))
		}
	}
	for _, marker := range requiredPackageMapMarkers {
		if !strings.Contains(packageMap, marker) {
			failures = append(failures, packageMapFilename+": missing marker "+strconv.Quote(marker))
		}
	}
	return failures
}

// markdownSection returns the body of one Markdown heading section.
func markdownSection(markdown string, heading string) string {
	_, body, found := strings.Cut(markdown, heading)
	if !found {
		return ""
	}
	section, _, found := strings.Cut(body, "\n## ")
	if !found {
		return body
	}
	return section
}

// hasPackageTableRow reports whether a Markdown table section documents a package.
func hasPackageTableRow(section string, packageName string) bool {
	return strings.Contains(section, "| `"+packageName+"` |")
}

// hasPackageFileRow reports whether a package-file table row names one package file.
func hasPackageFileRow(section string, packageName string, file string) bool {
	rowPrefix := "| `" + packageName + "` |"
	fileMarker := "`" + file + "`"
	for line := range strings.SplitSeq(section, "\n") {
		if strings.Contains(line, rowPrefix) && strings.Contains(line, fileMarker) {
			return true
		}
	}
	return false
}

// dependencyEdge describes one direct import between fibe-distilled packages.
type dependencyEdge struct {
	from string
	to   string
}

// packageDependencyFailures returns PACKAGES.md drift for direct import edges.
func packageDependencyFailures(root fs.FS, packageDirs []string, packageMap string) []string {
	edges, err := packageDependencyEdges(root, packageDirs)
	if err != nil {
		return []string{packageMapFilename + ": " + err.Error()}
	}
	var failures []string
	for _, edge := range edges {
		marker := "`" + edge.from + "` -> `" + edge.to + "`"
		if !strings.Contains(packageMap, marker) {
			failures = append(failures, packageMapFilename+": missing dependency edge "+marker)
		}
	}
	return failures
}

// packageDependencyEdges discovers direct internal imports from production files.
func packageDependencyEdges(root fs.FS, packageDirs []string) ([]dependencyEdge, error) {
	module, err := modulePath(root)
	if err != nil {
		return nil, err
	}
	localPackages := localPackageImportMap(packageDirs, module)
	seen := map[dependencyEdge]bool{}
	var edges []dependencyEdge
	for _, dir := range packageDirs {
		if err := appendPackageDependencyEdges(root, dir, localPackages, seen, &edges); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(edges, func(left, right dependencyEdge) int {
		return cmp.Or(cmp.Compare(left.from, right.from), cmp.Compare(left.to, right.to))
	})
	return edges, nil
}

// localPackageImportMap maps full import paths to repository-relative package dirs.
func localPackageImportMap(packageDirs []string, module string) map[string]string {
	localPackages := map[string]string{}
	for _, dir := range packageDirs {
		localPackages[module+"/"+dir] = dir
	}
	return localPackages
}

// appendPackageDependencyEdges appends direct local imports from one package.
func appendPackageDependencyEdges(root fs.FS, dir string, localPackages map[string]string, seen map[dependencyEdge]bool, edges *[]dependencyEdge) error {
	pkg, err := parsePackage(root, dir)
	if err != nil {
		return err
	}
	from := dir
	for _, file := range pkg.files {
		for _, spec := range file.Imports {
			if err := appendImportDependencyEdge(spec.Path.Value, from, localPackages, seen, edges); err != nil {
				return err
			}
		}
	}
	return nil
}

// appendImportDependencyEdge appends one local import edge when it has not appeared.
func appendImportDependencyEdge(quotedPath string, from string, localPackages map[string]string, seen map[dependencyEdge]bool, edges *[]dependencyEdge) error {
	path, err := strconv.Unquote(quotedPath)
	if err != nil {
		return err
	}
	to, ok := localPackages[path]
	if !ok {
		return nil
	}
	edge := dependencyEdge{from: from, to: to}
	if seen[edge] {
		return nil
	}
	seen[edge] = true
	*edges = append(*edges, edge)
	return nil
}

// modulePath reads the module path from go.mod.
func modulePath(root fs.FS) (string, error) {
	body, err := readRootFile(root, "go.mod")
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(body), "\n") {
		line = strings.TrimSpace(line)
		if module, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(module), nil
		}
	}
	return "", os.ErrNotExist
}

// readRootFile reads one repository-rooted file without allowing traversal out
// of the checked tree.
func readRootFile(root fs.FS, name string) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return body, nil
}

// declarationNeedsComment reports whether a named package-level declaration
// needs docs.
func declarationNeedsComment(name string) bool {
	return name != "" && name != "_"
}

// missingDoc reports whether a comment group is absent or blank.
func missingDoc(doc *ast.CommentGroup) bool {
	return doc == nil || strings.TrimSpace(doc.Text()) == ""
}

// missingFieldDoc reports whether a struct field or interface method lacks docs.
func missingFieldDoc(field *ast.Field) bool {
	if field == nil {
		return true
	}
	return missingDoc(field.Doc) && missingDoc(field.Comment)
}

// commentStartsWith reports whether a declaration doc starts with the expected
// Godoc identifier prefix.
func commentStartsWith(doc *ast.CommentGroup, name string) bool {
	return strings.HasPrefix(commentText(doc), expectedCommentPrefix(name))
}

// fieldCommentStartsWith reports whether a field or interface method doc starts
// with the expected Godoc identifier prefix.
func fieldCommentStartsWith(field *ast.Field, name string) bool {
	if field == nil {
		return false
	}
	text := commentText(field.Doc)
	if text == "" {
		text = commentText(field.Comment)
	}
	return strings.HasPrefix(text, expectedCommentPrefix(name))
}

// commentText returns normalized documentation text from an AST comment group.
func commentText(doc *ast.CommentGroup) string {
	if doc == nil {
		return ""
	}
	return strings.TrimSpace(doc.Text())
}

// expectedCommentPrefix returns the identifier prefix expected by Godoc
// convention for a declaration comment.
func expectedCommentPrefix(name string) string {
	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		return parts[len(parts)-1]
	}
	return name
}

// specComment returns the direct comment attached to a type, const, or var spec.
func specComment(spec ast.Spec) *ast.CommentGroup {
	switch item := spec.(type) {
	case *ast.TypeSpec:
		return item.Doc
	case *ast.ValueSpec:
		return item.Doc
	default:
		return nil
	}
}

// effectiveSpecComment returns the spec comment or the enclosing declaration
// comment.
func effectiveSpecComment(decl *ast.GenDecl, spec ast.Spec) *ast.CommentGroup {
	if doc := specComment(spec); doc != nil {
		return doc
	}
	if decl == nil {
		return nil
	}
	return decl.Doc
}

// specName returns a stable name for a type, const, or var spec.
func specName(spec ast.Spec) string {
	switch item := spec.(type) {
	case *ast.TypeSpec:
		return item.Name.Name
	case *ast.ValueSpec:
		names := make([]string, 0, len(item.Names))
		for _, name := range item.Names {
			names = append(names, name.Name)
		}
		return strings.Join(names, ",")
	default:
		return ""
	}
}

// singleExportedValueName returns the exported name for a one-name const or var
// declaration.
func singleExportedValueName(spec *ast.ValueSpec) (string, bool) {
	if spec == nil || len(spec.Names) != 1 {
		return "", false
	}
	name := spec.Names[0]
	if !name.IsExported() {
		return "", false
	}
	return name.Name, true
}

// valueSpecNeedsPrefix reports whether a const or var spec should follow the
// exported identifier-prefix rule.
func valueSpecNeedsPrefix(decl *ast.GenDecl, spec *ast.ValueSpec) bool {
	if decl == nil || spec == nil {
		return false
	}
	return decl.Lparen == token.NoPos || !missingDoc(spec.Doc)
}
