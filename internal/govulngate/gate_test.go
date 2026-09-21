package govulngate

import (
	"testing"
)

func TestParseJSONClassifiesCallPathAndBinarySymbolHits(t *testing.T) {
	source := []byte(`
{"config":{"scanner_name":"govulncheck"}}
{"finding":{"osv":"GO-2026-6218","trace":[{"module":"stdlib","package":"net/url","function":"URL.Parse"},{"module":"taskboard","package":"taskboard/internal/validate","function":"Parse"}]}}
{"finding":{"osv":"GO-2026-6089","trace":[{"module":"stdlib","package":"html/template"}]}}
{"finding":{"osv":"GO-2024-0001","trace":[{"module":"example.com/mod"}]}}
`)
	binary := []byte(`
{"finding":{"osv":"GO-2026-6218","trace":[{"module":"stdlib","package":"net/url","function":"URL.Parse"}]}}
`)
	sourceFindings, err := ParseJSON(source, ModeSource)
	if err != nil {
		t.Fatal(err)
	}
	binaryFindings, err := ParseJSON(binary, ModeBinary)
	if err != nil {
		t.Fatal(err)
	}
	summary := Summarize("go1.26.0", sourceFindings, binaryFindings)
	if summary.ExploitProven {
		t.Fatal("static findings must not be reported as proven exploits")
	}
	if len(summary.CallPath) != 1 || summary.CallPath[0].OSV != "GO-2026-6218" {
		t.Fatalf("call-path findings = %#v", summary.CallPath)
	}
	if len(summary.Symbol) != 1 || summary.Symbol[0].Mode != ModeBinary {
		t.Fatalf("symbol findings = %#v", summary.Symbol)
	}
	if len(summary.Package) != 1 || summary.Package[0].OSV != "GO-2026-6089" {
		t.Fatalf("package findings = %#v", summary.Package)
	}
	if len(summary.Module) != 1 || summary.Module[0].OSV != "GO-2024-0001" {
		t.Fatalf("module findings = %#v", summary.Module)
	}
	if got := summary.Blocking(); len(got) != 2 {
		t.Fatalf("blocking findings = %#v, want call-path and symbol hits", got)
	}
}

func TestParseJSONIgnoresEmptyProgressEvents(t *testing.T) {
	findings, err := ParseJSON([]byte(`{"progress":{"message":"scanning"}}{"osv":{"id":"GO-2026-6218","summary":"advisory"}}`), ModeSource)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %#v, want none", findings)
	}
}

func TestModuleLevelFindingIsNotBlocking(t *testing.T) {
	findings, err := ParseJSON([]byte(`{"finding":{"osv":"GO-2026-5932","trace":[{"module":"golang.org/x/crypto","version":"v0.57.0"}]}}`), ModeSource)
	if err != nil {
		t.Fatal(err)
	}
	summary := Summarize("go1.26.8", findings, nil)
	if len(summary.Module) != 1 || len(summary.Blocking()) != 0 {
		t.Fatalf("module-only advisory must not fail the gate: %#v", summary)
	}
}

func TestIsPatchedRejectsUnpatchedGo126(t *testing.T) {
	if err := IsPatched("go1.26.0", "go1.26.8"); err == nil {
		t.Fatal("go1.26.0 must be rejected against go1.26.8")
	}
	if err := IsPatched("go1.26.8", "go1.26.8"); err != nil {
		t.Fatal(err)
	}
	if err := IsPatched("go1.27.0 linux/amd64", "1.26.8"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseGoVersion(""); err == nil {
		t.Fatal("empty version must be rejected")
	}
	if _, err := ParseGoVersion("devel"); err == nil {
		t.Fatal("devel version must be rejected")
	}
}
