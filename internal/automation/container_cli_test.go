package automation

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type hostCodexInstall struct {
	root   string
	bin    string
	script string
	scope  string
	cache  string
	npmDir string
	link   string
}

func writeHostCodexInstall(t *testing.T) hostCodexInstall {
	t.Helper()
	root := t.TempDir()
	scope := filepath.Join(root, "lib", "node_modules", "@openai")
	script := filepath.Join(scope, "codex", "bin", "codex.js")
	cache := filepath.Join(root, "cache", "codex-linux-x64")
	npmDir := filepath.Join(root, "lib", "node_modules", "npm")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{filepath.Dir(script), cache, npmDir, bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scriptBody := `#!/usr/bin/env node
const fs = require("fs");
const path = require("path");
const pkg = path.join(__dirname, "..", "..", "codex-linux-x64", "package.json");
if (!fs.existsSync(pkg)) {
  process.stderr.write("missing " + pkg + "\n");
  process.exit(1);
}
process.stdout.write("codex-ok\n");
`
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scope, "codex", "package.json"), []byte("{\"name\":\"@openai/codex\",\"optionalDependencies\":{\"@openai/codex-linux-x64\":\"0.0.0\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "package.json"), []byte("{\"name\":\"@openai/codex-linux-x64\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cache, filepath.Join(scope, "codex-linux-x64")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(npmDir, "package.json"), []byte("{\"name\":\"npm\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, "codex")
	if err := os.Symlink("../lib/node_modules/@openai/codex/bin/codex.js", link); err != nil {
		t.Fatal(err)
	}
	return hostCodexInstall{root: root, bin: bin, script: script, scope: scope, cache: cache, npmDir: npmDir, link: link}
}

func useHostCodexOnPATH(t *testing.T, install hostCodexInstall) {
	t.Helper()
	t.Setenv("PATH", install.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestResolveContainerCLIUsesImageProviderCLIs(t *testing.T) {
	install := writeHostCodexInstall(t)
	useHostCodexOnPATH(t, install)
	for _, name := range []string{"codex", "claude", "grok", install.link} {
		cli, err := resolveContainerCLI(name)
		if err != nil {
			t.Fatal(err)
		}
		want := "/usr/local/bin/" + filepath.Base(name)
		if cli.NeedsNode || len(cli.Binds) != 0 || !reflect.DeepEqual(cli.Argv, []string{want}) {
			t.Fatalf("%s plan = %#v", name, cli)
		}
	}
	for _, blocked := range []string{install.script, install.scope, install.cache, install.bin} {
		cli, err := resolveContainerCLI("codex")
		if err != nil {
			t.Fatal(err)
		}
		if pathListed(cli.Binds, blocked) || strings.Contains(strings.Join(cli.Argv, " "), blocked) {
			t.Fatalf("image Codex exec used host path %s: %#v", blocked, cli)
		}
	}
}

func TestResolveContainerCLIMountsHoistedSiblingDependency(t *testing.T) {
	root := t.TempDir()
	modules := filepath.Join(root, "lib", "node_modules")
	script := filepath.Join(modules, "acme-cli", "bin", "cli.js")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(modules, "left-pad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(modules, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/usr/bin/env node\nprocess.stdout.write('acme')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modules, "acme-cli", "package.json"), []byte("{\"dependencies\":{\"left-pad\":\"1.0.0\"}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modules, "left-pad", "package.json"), []byte("{\"name\":\"left-pad\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modules, "npm", "package.json"), []byte("{\"name\":\"npm\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../lib/node_modules/acme-cli/bin/cli.js", filepath.Join(bin, "acme")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cli, err := resolveContainerCLI("acme")
	if err != nil {
		t.Fatal(err)
	}
	assertPathSet(t, cli.Binds, filepath.Join(modules, "acme-cli"), filepath.Join(modules, "left-pad"))
	if pathListed(cli.Binds, filepath.Join(modules, "npm")) || pathListed(cli.Binds, modules) {
		t.Fatalf("unrelated global modules were mounted: %#v", cli.Binds)
	}
}

func TestResolveContainerCLIMountsNativePackageWithoutConfigHome(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "libexec", "helper")
	binDir := filepath.Join(pkg, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(binDir, "helper")
	if err := os.WriteFile(real, []byte("#!/bin/sh\necho helper\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "share.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../libexec/helper/bin/helper", filepath.Join(linkDir, "helper")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", linkDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cli, err := resolveContainerCLI("helper")
	if err != nil {
		t.Fatal(err)
	}
	if cli.NeedsNode || !reflect.DeepEqual(cli.Argv, []string{real}) {
		t.Fatalf("argv = %#v needsNode=%v", cli.Argv, cli.NeedsNode)
	}
	if cli.ExtraPATH != binDir {
		t.Fatalf("extra PATH = %q", cli.ExtraPATH)
	}
	assertPathSet(t, cli.Binds, pkg)
	if pathListed(cli.Binds, linkDir) || pathListed(cli.Binds, root) {
		t.Fatalf("native mount is too broad: %#v", cli.Binds)
	}

	home := t.TempDir()
	config := filepath.Join(home, ".codex")
	configBin := filepath.Join(config, "bin")
	if err := os.MkdirAll(configBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "auth.json"), []byte("{\"token\":\"secret\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(configBin, "helper")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho native\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", configBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cli, err = resolveContainerCLI("helper")
	if err != nil {
		t.Fatal(err)
	}
	if pathListed(cli.Binds, config) || pathListed(cli.Binds, filepath.Join(config, "auth.json")) {
		t.Fatalf("CLI home or auth.json was mounted: %#v", cli.Binds)
	}
	assertPathSet(t, cli.Binds, configBin)
	if !reflect.DeepEqual(cli.Argv, []string{binary}) {
		t.Fatalf("argv = %#v", cli.Argv)
	}
}

func TestNodePackageBindsLeaveImageToolchainInPlace(t *testing.T) {
	binds, err := nodePackageBinds("/usr/local/lib/node_modules/@openai/codex/bin/codex.js")
	if err != nil {
		t.Fatal(err)
	}
	assertPathSet(t, binds, "/usr/local/lib/node_modules/@openai")
	for _, blocked := range []string{"/usr/local", "/usr/local/bin", "/usr/local/lib", "/usr/local/lib/node_modules", "/usr"} {
		if pathListed(binds, blocked) {
			t.Fatalf("image path %s was selected: %#v", blocked, binds)
		}
	}
	if got := nativeCLIMountRoot("/usr/local/bin/grok"); got != "/usr/local/bin/grok" {
		t.Fatalf("native mount root = %s", got)
	}
}

func TestResolveContainerCLIUsesImageToolsAsIs(t *testing.T) {
	cli, err := resolveContainerCLI("sh")
	if err != nil {
		t.Fatal(err)
	}
	if len(cli.Binds) != 0 || cli.NeedsNode {
		t.Fatalf("image tool plan = %#v", cli)
	}
	if len(cli.Argv) != 1 || filepath.Base(cli.Argv[0]) != "sh" {
		t.Fatalf("argv = %#v", cli.Argv)
	}
	if _, err := resolveContainerCLI("/no/such/helper"); err == nil {
		t.Fatal("missing absolute CLI was accepted")
	}
}

func assertPathSet(t *testing.T, got []string, want ...string) {
	t.Helper()
	for _, path := range want {
		if !pathListed(got, path) {
			t.Fatalf("missing %s in %#v", path, got)
		}
	}
}

func pathListed(paths []string, want string) bool {
	want = filepath.Clean(want)
	for _, path := range paths {
		if filepath.Clean(path) == want {
			return true
		}
	}
	return false
}
