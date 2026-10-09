package parse

import "testing"

func TestClassifyCommand(t *testing.T) {
	tests := map[string]string{
		"kata show abcd --json":                         ActivityPlanning,
		"owcli search 'parser'":                         ActivityDocumentation,
		"go test ./...":                                 ActivityTesting,
		"make build && go test ./...":                   ActivityTesting,
		"gofmt -w internal/a.go":                        ActivityImplementation,
		"go build ./cmd/bossman":                        ActivityImplementation,
		"git status --short":                            ActivitySourceControl,
		"rg -n 'ToolStat' internal":                     ActivityInspection,
		"npm install":                                   ActivityEnvironment,
		"/bin/bash -lc \"owcli read quickstart#usage\"": ActivityDocumentation,
		"./custom-script":                               ActivityOther,
	}
	for command, want := range tests {
		if got := ClassifyCommand(command); got != want {
			t.Errorf("ClassifyCommand(%q) = %q, want %q", command, got, want)
		}
	}
}
