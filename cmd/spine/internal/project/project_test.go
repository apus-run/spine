package project

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/apus-run/spine/cmd/spine/v3/internal/base"
)

// TestCmdNew tests the `kratos new` command.
func TestCmdNew(t *testing.T) {
	cwd := changeCurrentDir(t)
	template := projectFixture(t)
	projectName := "helloworld"

	// create a new project
	CmdNew.SetArgs([]string{projectName, "--repo", template})
	if err := CmdNew.Execute(); err != nil {
		t.Fatalf("executing command: %v", err)
	}

	// check that the expected files were created
	for _, file := range []string{
		goModFileName,
		goSumFileName,
		readmeFileName,
		"cmd/helloworld/main.go",
	} {
		if _, err := os.Stat(filepath.Join(cwd, projectName, file)); err != nil {
			t.Errorf("expected file %s to exist", file)
		}
	}

	// check that the go.mod file contains the expected module name
	assertGoMod(t, filepath.Join(cwd, projectName, goModFileName), projectName)

	assertImportsInclude(t, filepath.Join(cwd, projectName, "cmd", projectName, "wire.go"), fmt.Sprintf(`"%s/internal/biz"`, projectName))
}

// TestCmdNewNoMod tests the `kratos new` command with the --nomod flag.
func TestCmdNewNoMod(t *testing.T) {
	cwd := changeCurrentDir(t)
	template := projectFixture(t)

	// create a new project
	CmdNew.SetArgs([]string{"project", "--repo", template})
	if err := CmdNew.Execute(); err != nil {
		t.Fatalf("executing command: %v", err)
	}

	// add new app with --nomod flag
	CmdNew.SetArgs([]string{"--nomod", "project/app/user", "--repo", template})
	if err := CmdNew.Execute(); err != nil {
		t.Fatalf("executing command: %v", err)
	}

	// check that the expected files were created
	for _, file := range []string{
		goModFileName,
		goSumFileName,
		readmeFileName,
		"cmd/project/main.go",
		"app/user/cmd/user/main.go",
	} {
		if _, err := os.Stat(filepath.Join(cwd, "project", file)); err != nil {
			t.Errorf("expected file %s to exist", file)
		}
	}

	assertImportsInclude(t, filepath.Join(cwd, "project/app/user/cmd/user/wire.go"), `"project/app/user/internal/biz"`)
}

// assertImportsInclude checks that the file at path contains the expected import.
func assertImportsInclude(t *testing.T, path, expected string) {
	t.Helper()

	got, err := imports(path)
	if err != nil {
		t.Fatalf("getting imports: %v", err)
	}

	for _, imp := range got {
		if imp == expected {
			return
		}
	}

	t.Errorf("expected imports to include %s, got %v", expected, got)
}

// imports returns the imports in the file at path.
func imports(path string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}

	imports := make([]string, 0, len(f.Imports))
	for _, s := range f.Imports {
		imports = append(imports, s.Path.Value)
	}

	return imports, nil
}

// assertGoMod checks that the go.mod file contains the expected module name.
func assertGoMod(t *testing.T, path, expected string) {
	t.Helper()

	got, err := base.ModulePath(path)
	if err != nil {
		t.Fatalf("getting module path: %v", err)
	}

	if got != expected {
		t.Errorf("expected module name %s, got %s", expected, got)
	}
}

// change the working directory to the tempdir
func changeCurrentDir(t *testing.T) string {
	t.Helper()

	tmp := t.TempDir()

	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}

	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("changing working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldCWD); err != nil {
			t.Fatalf("restoring working directory: %v", err)
		}
	})

	return tmp
}

// Use a local template so command tests require neither a TTY nor network.
func projectFixture(t *testing.T) string {
	t.Helper()
	oldRepo, oldBranch, oldTimeout, oldNoMod := repo, branch, timeout, nomod
	repo, branch, timeout, nomod = "", "", "60s", false
	t.Cleanup(func() { repo, branch, timeout, nomod = oldRepo, oldBranch, oldTimeout, oldNoMod; CmdNew.SetArgs(nil) })
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":             "module example.com/template\n\ngo 1.27.3\n",
		"go.sum":             "",
		"README.md":          "test template\n",
		"cmd/server/main.go": "package main\nfunc main() {}\n",
		"cmd/server/wire.go": `package main
import _ "example.com/template/internal/biz"
`,
		"internal/biz/biz.go": "package biz\n",
	}
	for name, content := range files {
		file := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "."},
		{"-c", "user.name=Spine Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	cache := base.NewRepo(dir, "").Path()
	t.Cleanup(func() {
		if err := os.RemoveAll(cache); err != nil {
			t.Error(err)
		}
	})
	return dir
}

func TestCmdNewReturnsErrors(t *testing.T) {
	changeCurrentDir(t)
	template := projectFixture(t)
	CmdNew.SetArgs([]string{"example", "--repo", template, "--timeout", "invalid"})
	if err := CmdNew.Execute(); err == nil {
		t.Fatal("invalid timeout must return an error")
	}
	CmdNew.SetArgs([]string{"example", "--repo", filepath.Join(t.TempDir(), "missing"), "--timeout", "5s"})
	if err := CmdNew.Execute(); err == nil {
		t.Fatal("template failure must return an error")
	}
}
