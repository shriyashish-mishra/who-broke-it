// Package gitx is a thin wrapper over the git CLI. Shelling out keeps behavior identical to what users see.
package gitx

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Opts tunes a git invocation.
type Opts struct {
	Env     []string      // extra KEY=VALUE entries
	Stdin   string        // fed to git's stdin
	Timeout time.Duration // 0 = no timeout
}

// Run runs git in cwd with options and returns stdout with trailing newlines trimmed.
func Run(cwd string, o Opts, args ...string) (string, error) {
	ctx := context.Background()
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), append([]string{"GIT_TERMINAL_PROMPT=0"}, o.Env...)...)
	if o.Stdin != "" {
		cmd.Stdin = strings.NewReader(o.Stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", &Error{Args: args, Stderr: strings.TrimSpace(errb.String()), Err: err}
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// Git runs git in cwd and returns stdout with trailing newlines trimmed.
func Git(cwd string, args ...string) (string, error) { return Run(cwd, Opts{}, args...) }

type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string { return "git " + strings.Join(e.Args, " ") + ": " + e.Stderr }

// Try returns the output and whether git succeeded.
func Try(cwd string, args ...string) (string, bool) {
	s, err := Git(cwd, args...)
	return s, err == nil
}

func IsRepo(cwd string) bool {
	s, ok := Try(cwd, "rev-parse", "--is-inside-work-tree")
	return ok && s == "true"
}

func Toplevel(cwd string) string { s, _ := Try(cwd, "rev-parse", "--show-toplevel"); return s }

// CommonDir is the git dir shared by all worktrees; runtime state lives here.
func CommonDir(cwd string) string {
	s, _ := Try(cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	return s
}

func CurrentBranch(cwd string) string {
	if s, ok := Try(cwd, "rev-parse", "--abbrev-ref", "HEAD"); ok {
		return s
	}
	return "HEAD"
}

func UserName(cwd string) string { s, _ := Try(cwd, "config", "user.name"); return s }

var wtRe = regexp.MustCompile(`(?m)^worktree (.+)$`)

// MainWorktree is the first entry of `git worktree list`.
func MainWorktree(cwd string) string {
	out, ok := Try(cwd, "worktree", "list", "--porcelain")
	if !ok {
		return ""
	}
	if m := wtRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

func RefExists(cwd, ref string) bool {
	_, ok := Try(cwd, "rev-parse", "--verify", "--quiet", ref)
	return ok
}

func DefaultBranch(cwd string) string {
	if s, ok := Try(cwd, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); ok {
		return strings.TrimPrefix(s, "origin/")
	}
	for _, b := range []string{"main", "master"} {
		if RefExists(cwd, b) {
			return b
		}
	}
	return CurrentBranch(cwd)
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ChangedFiles lists files changed on head since it diverged from base.
func ChangedFiles(cwd, base, head string) []string {
	s, _ := Try(cwd, "diff", "--name-only", base+"..."+head)
	return lines(s)
}

type Commit struct{ SHA, Subject string }

func CommitsAhead(cwd, base, head string) []Commit {
	s, _ := Try(cwd, "log", base+".."+head, "--format=%h%x1f%s")
	var out []Commit
	for _, l := range lines(s) {
		p := strings.SplitN(l, "\x1f", 2)
		if len(p) == 2 {
			out = append(out, Commit{p[0], p[1]})
		}
	}
	return out
}

// FileLines holds the added (or all) lines of one file. A slice keeps iteration order deterministic.
type FileLines struct {
	File  string
	Lines []string
}

// AddedLines returns the lines added per file between base and head.
func AddedLines(cwd, base, head string) []FileLines {
	s, _ := Try(cwd, "diff", "-U0", "--no-color", base+"..."+head)
	var out []FileLines
	cur := -1
	for _, l := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ "):
			cur = -1
			if strings.HasPrefix(l, "+++ b/") {
				out = append(out, FileLines{File: l[6:]})
				cur = len(out) - 1
			}
		case cur >= 0 && strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			out[cur].Lines = append(out[cur].Lines, l[1:])
		}
	}
	return out
}

func LsFiles(cwd string) []string {
	s, _ := Try(cwd, "ls-files", "--cached", "--others", "--exclude-standard")
	return lines(s)
}

type CommitInfo struct {
	SHA, Author, Subject, Body string
	TS                         int64 // unix millis
	// Ref is the branch the commit was found on (only when searching all branches).
	Ref string
}

// LogFor lists commits touching path. With branches=true it searches all local branches
// (so unmerged agent work is included) and records which branch each commit was found on.
func LogFor(cwd, path string, limit int, extra []string, branches bool) []CommitInfo {
	args := []string{"log"}
	if branches {
		args = append(args, "--branches", "--source")
	}
	args = append(args, extra...)
	args = append(args, "-n"+strconv.Itoa(limit), "--no-merges", "--format=%H%x1f%an%x1f%ct%x1f%s%x1f%S%x1f%b%x1e", "--", path)
	s, _ := Try(cwd, args...)
	var out []CommitInfo
	for _, rec := range strings.Split(s, "\x1e") {
		rec = strings.TrimPrefix(rec, "\n")
		if rec == "" {
			continue
		}
		p := strings.SplitN(rec, "\x1f", 6)
		if len(p) < 6 {
			continue
		}
		ts, _ := strconv.ParseInt(p[2], 10, 64)
		out = append(out, CommitInfo{SHA: p[0], Author: p[1], TS: ts * 1000, Subject: p[3], Ref: strings.TrimPrefix(p[4], "refs/heads/"), Body: p[5]})
	}
	return out
}
