package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/shriyashish-mishra/who-broke-it/internal/engine"
)

// Documentation is part of the product. These tests read the README and docs and fail when they mention a
// command, adapter, or Action input that does not exist, so the docs cannot silently drift from the code.

// help() puts several commands on one line ("wbi inbox   wbi ack <id>"), so take every `wbi <word>`.
var helpCmdRe = regexp.MustCompile(`\bwbi ([a-z][a-z-]*)`)

func knownCommands(t *testing.T) map[string]bool {
	known := map[string]bool{"help": true, "version": true}
	for _, m := range helpCmdRe.FindAllStringSubmatch(help(), -1) {
		known[m[1]] = true
	}
	if len(known) < 20 {
		t.Fatalf("could not extract the command list from help(): %v", known)
	}
	return known
}

func docFiles(t *testing.T) []string {
	files := []string{"../../README.md", "../../CONTRIBUTING.md", "../../SECURITY.md"}
	more, _ := filepath.Glob("../../docs/*.md")
	return append(files, more...)
}

var fenceCmdRe = regexp.MustCompile("(?m)^\\s*(?:\\$ )?wbi ([a-z][a-z-]*)")

func TestDocumentedCommandsExist(t *testing.T) {
	known := knownCommands(t)
	for _, f := range docFiles(t) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		inFence := false
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			if !inFence {
				continue
			}
			m := fenceCmdRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if !known[m[1]] {
				t.Errorf("%s:%d documents `wbi %s`, which does not exist", filepath.Base(f), i+1, m[1])
			}
		}
	}
}

var inlineCmdRe = regexp.MustCompile("`wbi ([a-z][a-z-]*)[^`]*`")

func TestInlineCommandMentionsExist(t *testing.T) {
	known := knownCommands(t)
	for _, f := range docFiles(t) {
		b, _ := os.ReadFile(f)
		for _, m := range inlineCmdRe.FindAllStringSubmatch(string(b), -1) {
			if !known[m[1]] {
				t.Errorf("%s mentions `wbi %s`, which does not exist", filepath.Base(f), m[1])
			}
		}
	}
}

func TestDocumentedAdaptersAndActionInputsExist(t *testing.T) {
	adapters := map[string]bool{"all": true}
	for _, a := range engine.Adapters() {
		adapters[a.Name] = true
	}
	re := regexp.MustCompile("wbi adapters (?:install|mcp) ([a-z|]+)")
	for _, f := range docFiles(t) {
		b, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			for _, name := range strings.Split(m[1], "|") {
				if name != "" && !adapters[name] && name != "myagent" { // "myagent" is the worked example in ADAPTERS.md
					t.Errorf("%s documents adapter %q, which is not in the registry", filepath.Base(f), name)
				}
			}
		}
	}
	action, err := os.ReadFile("../../action.yml")
	if err != nil {
		t.Fatal(err)
	}
	readme, _ := os.ReadFile("../../README.md")
	for _, in := range regexp.MustCompile(`(?m)^          (require-[a-z]+):`).FindAllStringSubmatch(string(readme), -1) {
		if !strings.Contains(string(action), "\n  "+in[1]+":") {
			t.Errorf("README uses Action input %q that action.yml does not define", in[1])
		}
	}
}

// The Action example in the README must pin a release that actually exists in the changelog.
func TestReadmeActionVersionIsInTheChangelog(t *testing.T) {
	readme, _ := os.ReadFile("../../README.md")
	log, _ := os.ReadFile("../../CHANGELOG.md")
	m := regexp.MustCompile(`who-broke-it@(v\d+\.\d+\.\d+)`).FindStringSubmatch(string(readme))
	if m == nil {
		t.Skip("README has no pinned Action example")
	}
	if !strings.Contains(string(log), "## ["+strings.TrimPrefix(m[1], "v")+"]") {
		t.Errorf("README pins %s but CHANGELOG has no such release", m[1])
	}
}
