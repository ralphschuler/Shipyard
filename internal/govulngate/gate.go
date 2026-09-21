package govulngate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Class string

const (
	ClassCallPath Class = "call_path"
	ClassSymbol   Class = "symbol"
	ClassPackage  Class = "package"
	ClassModule   Class = "module"
)

type Mode string

const (
	ModeSource Mode = "source"
	ModeBinary Mode = "binary"
)

type Finding struct {
	OSV      string `json:"osv"`
	Class    Class  `json:"class"`
	Mode     Mode   `json:"mode"`
	Symbol   string `json:"symbol,omitempty"`
	Package  string `json:"package,omitempty"`
	Module   string `json:"module,omitempty"`
	TraceLen int    `json:"traceLen"`
}

type Summary struct {
	GoVersion     string    `json:"goVersion"`
	CallPath      []Finding `json:"callPath"`
	Symbol        []Finding `json:"symbol"`
	Package       []Finding `json:"package"`
	Module        []Finding `json:"module"`
	ExploitProven bool      `json:"exploitProven"`
}

type event struct {
	Finding *rawFinding `json:"finding"`
}

type rawFinding struct {
	OSV   string     `json:"osv"`
	Trace []rawFrame `json:"trace"`
}

type rawFrame struct {
	Module   string `json:"module"`
	Package  string `json:"package"`
	Function string `json:"function"`
	Symbol   string `json:"symbol"`
}

func ParseJSON(data []byte, mode Mode) ([]Finding, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var findings []Finding
	for {
		var item event
		if err := decoder.Decode(&item); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("parse govulncheck %s JSON: %w", mode, err)
		}
		if item.Finding == nil || strings.TrimSpace(item.Finding.OSV) == "" {
			continue
		}
		findings = append(findings, classify(mode, *item.Finding))
	}
	return findings, nil
}

func classify(mode Mode, raw rawFinding) Finding {
	finding := Finding{
		OSV:      raw.OSV,
		Mode:     mode,
		TraceLen: len(raw.Trace),
	}
	if len(raw.Trace) > 0 {
		finding.Symbol = firstNonEmpty(raw.Trace[0].Symbol, raw.Trace[0].Function)
		finding.Package = raw.Trace[0].Package
		finding.Module = raw.Trace[0].Module
	}
	switch {
	case mode == ModeBinary:
		if finding.Symbol != "" {
			finding.Class = ClassSymbol
		} else if finding.Package != "" {
			finding.Class = ClassPackage
		} else {
			finding.Class = ClassModule
		}
	case isCallPath(raw.Trace):
		finding.Class = ClassCallPath
	case finding.Symbol != "":
		finding.Class = ClassSymbol
	case finding.Package != "":
		finding.Class = ClassPackage
	default:
		finding.Class = ClassModule
	}
	return finding
}

func isCallPath(trace []rawFrame) bool {
	if len(trace) < 2 {
		return false
	}
	vulnerable := firstNonEmpty(trace[0].Function, trace[0].Symbol)
	caller := firstNonEmpty(trace[len(trace)-1].Function, trace[len(trace)-1].Symbol)
	return vulnerable != "" && caller != ""
}

func Summarize(goVersion string, source, binary []Finding) Summary {
	summary := Summary{GoVersion: goVersion, ExploitProven: false}
	for _, finding := range append(append([]Finding{}, source...), binary...) {
		switch finding.Class {
		case ClassCallPath:
			summary.CallPath = append(summary.CallPath, finding)
		case ClassSymbol:
			summary.Symbol = append(summary.Symbol, finding)
		case ClassPackage:
			summary.Package = append(summary.Package, finding)
		default:
			summary.Module = append(summary.Module, finding)
		}
	}
	return summary
}

func (s Summary) Blocking() []Finding {
	blocked := append([]Finding{}, s.CallPath...)
	return append(blocked, s.Symbol...)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
