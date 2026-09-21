package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"taskboard/internal/govulngate"
)

func main() {
	check := flag.String("check-toolchain", "", "Go version to compare against the required minimum")
	minimum := flag.String("minimum", "", "minimum patched toolchain, for example go1.26.8")
	sourceJSON := flag.String("source-json", "", "govulncheck source-mode JSON file")
	binaryJSON := flag.String("binary-json", "", "govulncheck binary-mode JSON file")
	goVersion := flag.String("go-version", "", "toolchain embedded in the scanned artifact")
	flag.Parse()

	if *check != "" {
		if *minimum == "" {
			fatalf("missing -minimum toolchain")
		}
		if err := govulngate.IsPatched(*check, *minimum); err != nil {
			fatalf("%s", err)
		}
		fmt.Printf("patched toolchain: %s (minimum %s)\n", *check, *minimum)
		return
	}
	if *sourceJSON == "" && *binaryJSON == "" {
		fatalf("specify -check-toolchain or govulncheck JSON inputs")
	}
	source, err := readFindings(*sourceJSON, govulngate.ModeSource)
	if err != nil {
		fatalf("%s", err)
	}
	binary, err := readFindings(*binaryJSON, govulngate.ModeBinary)
	if err != nil {
		fatalf("%s", err)
	}
	summary := govulngate.Summarize(*goVersion, source, binary)
	printSummary(summary)
	if err := json.NewEncoder(os.Stdout).Encode(summary); err != nil {
		fatalf("encode summary: %s", err)
	}
	if blocked := summary.Blocking(); len(blocked) > 0 {
		os.Exit(3)
	}
}

func readFindings(path string, mode govulngate.Mode) ([]govulngate.Finding, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return govulngate.ParseJSON(data, mode)
}

func printSummary(summary govulngate.Summary) {
	fmt.Fprintf(os.Stderr, "artifact toolchain: %s\n", empty(summary.GoVersion, "unknown"))
	fmt.Fprintf(os.Stderr, "call-path hits (static, not proven exploits): %d\n", len(summary.CallPath))
	for _, finding := range summary.CallPath {
		fmt.Fprintf(os.Stderr, "  - %s %s %s [call_path/%s]\n", finding.OSV, finding.Package, finding.Symbol, finding.Mode)
	}
	fmt.Fprintf(os.Stderr, "symbol hits (binary/symbol table, less precise): %d\n", len(summary.Symbol))
	for _, finding := range summary.Symbol {
		fmt.Fprintf(os.Stderr, "  - %s %s %s [symbol/%s]\n", finding.OSV, finding.Package, finding.Symbol, finding.Mode)
	}
	fmt.Fprintf(os.Stderr, "package/module hints (not called): %d\n", len(summary.Package)+len(summary.Module))
	fmt.Fprintf(os.Stderr, "proven exploitability: no (static advisory matches only)\n")
}

func empty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
