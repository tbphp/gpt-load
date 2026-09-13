package webui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDatabaseShardsCoverEveryExternalTestExactlyOnce(t *testing.T) {
	script := readRepositoryFile(t, ".github/scripts/ci-database-contract.sh")
	dir := t.TempDir()
	path := filepath.Join(dir, "contract.sh")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	type selection struct{ run, skip *regexp.Regexp }
	filters := make(map[string]selection)
	for _, shard := range []string{"all", "mysql-migrations", "mysql-rest"} {
		command := exec.Command("bash", path, shard)
		command.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", shard, err, output)
		}
		args := strings.Split(strings.TrimSpace(string(output)), "\n")
		for _, pkg := range []string{"storage", "control", "requestlog"} {
			if !strings.Contains(string(output), "./internal/"+pkg+"\n") {
				t.Fatalf("%s omits the %s package", shard, pkg)
			}
		}
		var run, skip string
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-run" {
				run = args[i+1]
			}
			if args[i] == "-skip" {
				skip = args[i+1]
			}
		}
		if run == "" || skip == "" || !strings.Contains(string(output), "-count=1\n") {
			t.Fatalf("%s has incomplete test arguments: %s", shard, output)
		}
		filters[shard] = selection{regexp.MustCompile(run), regexp.MustCompile(skip)}
	}
	// 从源码发现测试，新增外部数据库测试也必须被分组覆盖。
	names := []string{"TestExternalFutureContract"}
	for _, pkg := range []string{"storage", "control", "requestlog"} {
		files, err := filepath.Glob(filepath.Join("..", pkg, "*_test.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && strings.HasPrefix(fn.Name.Name, "TestExternal") {
					names = append(names, fn.Name.Name)
				}
			}
		}
	}
	for _, name := range names {
		count := 0
		for _, shard := range []string{"mysql-migrations", "mysql-rest"} {
			filter := filters[shard]
			if filter.run.MatchString(name) && !filter.skip.MatchString(name) {
				count++
			}
		}
		if count != 1 || !filters["all"].run.MatchString(name) || filters["all"].skip.MatchString(name) {
			t.Errorf("%s is not covered exactly once for each driver", name)
		}
	}
}

func TestMySQLShardsRunConcurrentlyAndPropagateFailures(t *testing.T) {
	for _, fail := range []string{"", "migrations", "rest"} {
		t.Run("failure="+fail, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "contract.sh")
			if err := os.WriteFile(script, []byte(readRepositoryFile(t, ".github/scripts/ci-database-contract.sh")), 0o600); err != nil {
				t.Fatal(err)
			}
			fakeGo := `#!/bin/bash
case "$*" in
  *"-skip ^TestExternal("*) shard=rest ;;
  *) shard=migrations ;;
esac
printf '%s' "$GPT_LOAD_DATABASE_TEST_DSN" > "$MARKERS/$shard"
for ((i=0; i<100; i++)); do
  if [[ -f "$MARKERS/rest" && -f "$MARKERS/migrations" ]]; then
    [[ "$FAIL_SHARD" != "$shard" ]]
    exit $?
  fi
  sleep 0.01
done
exit 91
`
			if err := os.WriteFile(filepath.Join(dir, "go"), []byte(fakeGo), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("bash", script, "mysql")
			command.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"MARKERS="+dir, "FAIL_SHARD="+fail, "RUNNER_TEMP="+dir,
				"GPT_LOAD_DATABASE_TEST_DSN=primary", "GPT_LOAD_DATABASE_SECONDARY_DSN=secondary")
			output, err := command.CombinedOutput()
			if (err != nil) != (fail != "") {
				t.Fatalf("mysql result = %v, failing shard = %q: %s", err, fail, output)
			}
			for shard, want := range map[string]string{"migrations": "primary", "rest": "secondary"} {
				got, err := os.ReadFile(filepath.Join(dir, shard))
				if err != nil || string(got) != want {
					t.Fatalf("%s did not use its isolated database: %s, %v", shard, got, err)
				}
			}
		})
	}
}

func TestDatabaseMatrixStartsOnlyRequiredServers(t *testing.T) {
	for _, workflow := range []string{"ci.yml", "release.yml"} {
		job := workflowJobBlock(t, readRepositoryFile(t, ".github/workflows/"+workflow), "database-contract")
		for _, required := range []string{
			"max-parallel: 2", "secondary-mysql:", "matrix.driver == 'mysql'",
			"image: ${{ matrix.image }}", ".github/scripts/ci-database-contract.sh", "GPT_LOAD_DATABASE_SECONDARY_DSN",
			"data-dir: /var/lib/mysql", "data-dir: /var/lib/postgresql",
			"--tmpfs ${{ matrix.data-dir }}:rw,nosuid,nodev,size=2g",
			"--tmpfs /var/lib/mysql:rw,nosuid,nodev,size=2g",
		} {
			if !strings.Contains(job, required) {
				t.Errorf("%s database matrix is missing %s", workflow, required)
			}
		}
		if strings.Contains(job, "      mysql:") || strings.Contains(job, "      postgres:") {
			t.Errorf("%s still unconditionally starts both database types", workflow)
		}
	}
}
