// Package replicate syncs coordination state between machines using nothing but git.
//
// Each machine appends its pending events as a commit on a dedicated ref (default refs/wbi/sync) of an
// ordinary remote. A push to a ref is an atomic compare-and-swap, so the history of that ref is a single
// linear log with one agreed order. Replaying it in order gives every machine the same state, and when two
// machines race for the same task exactly one push wins; the other learns that it lost and drops its claim.
// No server, account, or extra infrastructure is involved.
package replicate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wbi/internal/gitx"
	"wbi/internal/state"
)

const (
	DefaultRef = "refs/wbi/sync"
	netTimeout = 45 * time.Second
	maxTries   = 6
)

type Config struct{ Remote, Ref string }

// BeforePush, when set, runs after the commit is built and right before it is pushed. Tests use it to let
// another machine publish inside the fetch→push window, which is the only place compare-and-swap matters.
var BeforePush func()

// ErrOffline means the remote could not be reached; local work continues and syncs later.
type ErrOffline struct{ Reason string }

func (e *ErrOffline) Error() string { return "offline: " + e.Reason }

type Report struct {
	Applied  int      // events from other machines applied locally
	Pushed   int      // local events published
	Attempts int      // fetch/push rounds (>1 means we raced someone)
	Rejected []string // local claims that lost a race
	Tip      string
}

func trackingRef(ref string) string {
	return "refs/wbi/tracking/" + strings.NewReplacer("/", "_", " ", "_").Replace(strings.TrimPrefix(ref, "refs/"))
}

var gitAuthor = []string{
	"GIT_AUTHOR_NAME=wbi", "GIT_AUTHOR_EMAIL=wbi@localhost", "GIT_COMMITTER_NAME=wbi", "GIT_COMMITTER_EMAIL=wbi@localhost",
}

func netErr(err error) error {
	var ge *gitx.Error
	if errors.As(err, &ge) {
		return &ErrOffline{Reason: firstLine(ge.Stderr)}
	}
	return &ErrOffline{Reason: err.Error()}
}

func firstLine(s string) string {
	if s == "" {
		return "git failed"
	}
	return strings.SplitN(s, "\n", 2)[0]
}

// fetch updates the tracking ref and returns the remote tip ("" if the remote has no log yet).
func fetch(root string, cfg Config) (string, error) {
	tr := trackingRef(cfg.Ref)
	_, err := gitx.Run(root, gitx.Opts{Timeout: netTimeout}, "fetch", "--quiet", "--no-tags", cfg.Remote, "+"+cfg.Ref+":"+tr)
	if err != nil {
		var ge *gitx.Error
		if errors.As(err, &ge) && strings.Contains(ge.Stderr, "couldn't find remote ref") {
			_, _ = gitx.Try(root, "update-ref", "-d", tr)
			return "", nil
		}
		return "", netErr(err)
	}
	tip, _ := gitx.Try(root, "rev-parse", "--verify", "--quiet", tr)
	return tip, nil
}

// newCommits lists log commits after lastSeen (oldest first); if history was rewritten it replays everything.
func newCommits(root, lastSeen, tip string) []string {
	if tip == "" {
		return nil
	}
	rng := tip
	if lastSeen != "" {
		if _, ok := gitx.Try(root, "merge-base", "--is-ancestor", lastSeen, tip); ok {
			if lastSeen == tip {
				return nil
			}
			rng = lastSeen + ".." + tip
		}
	}
	out, _ := gitx.Try(root, "rev-list", "--first-parent", "--reverse", rng)
	var cs []string
	for _, l := range strings.Split(out, "\n") {
		if l != "" {
			cs = append(cs, l)
		}
	}
	return cs
}

func readEvents(root string, commits []string) []state.Event {
	var evs []state.Event
	for _, c := range commits {
		names, _ := gitx.Try(root, "diff-tree", "--no-commit-id", "--name-only", "-r", "--root", c)
		for _, f := range strings.Split(names, "\n") {
			if !strings.HasPrefix(f, "log/") || !strings.HasSuffix(f, ".jsonl") {
				continue
			}
			body, err := gitx.Git(root, "show", c+":"+f)
			if err != nil {
				continue
			}
			sc := bufio.NewScanner(strings.NewReader(body))
			sc.Buffer(make([]byte, 1<<20), 16<<20)
			for sc.Scan() {
				var ev state.Event
				if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Tbl != "" {
					evs = append(evs, ev)
				}
			}
		}
	}
	return evs
}

// commitEvents writes the events as one new file on top of tip, using plumbing only: no checkout, no
// index, and no effect on the user's branches or working tree.
func commitEvents(root, actor, tip string, evs []state.Event) (string, error) {
	var b strings.Builder
	for _, ev := range evs {
		ev.Actor = actor
		line, err := json.Marshal(ev)
		if err != nil {
			return "", err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	blob, err := gitx.Run(root, gitx.Opts{Stdin: b.String()}, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "wbi-index-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	env := append([]string{"GIT_INDEX_FILE=" + filepath.Join(dir, "index")}, gitAuthor...)
	if tip != "" {
		if _, err := gitx.Run(root, gitx.Opts{Env: env}, "read-tree", tip); err != nil {
			return "", err
		}
	}
	path := fmt.Sprintf("log/%s/%013d-%04d.jsonl", actor, time.Now().UnixMilli(), rand.Intn(10000))
	if _, err := gitx.Run(root, gitx.Opts{Env: env}, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path); err != nil {
		return "", err
	}
	tree, err := gitx.Run(root, gitx.Opts{Env: env}, "write-tree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", tree, "-m", fmt.Sprintf("wbi sync: %s +%d events", actor, len(evs))}
	if tip != "" {
		args = append(args, "-p", tip)
	}
	return gitx.Run(root, gitx.Opts{Env: env}, args...)
}

func push(root string, cfg Config, commit string) (rejected bool, err error) {
	_, err = gitx.Run(root, gitx.Opts{Timeout: netTimeout}, "push", "--quiet", cfg.Remote, commit+":"+cfg.Ref)
	if err == nil {
		return false, nil
	}
	var ge *gitx.Error
	if errors.As(err, &ge) {
		low := strings.ToLower(ge.Stderr)
		if strings.Contains(low, "rejected") || strings.Contains(low, "non-fast-forward") || strings.Contains(low, "fetch first") || strings.Contains(low, "stale info") {
			return true, nil
		}
	}
	return false, netErr(err)
}

// Sync pulls other machines' events, replays local pending ones on top, and publishes them.
// It is safe to call repeatedly and from several clones at once.
func Sync(db *state.DB, root string, cfg Config) (Report, error) {
	var rep Report
	if cfg.Ref == "" {
		cfg.Ref = DefaultRef
	}
	if _, ok := gitx.Try(root, "remote", "get-url", cfg.Remote); !ok {
		return rep, fmt.Errorf("git remote %q does not exist (configure it, or set WBI_SYNC_REMOTE)", cfg.Remote)
	}
	actor := db.Actor()
	for rep.Attempts < maxTries {
		rep.Attempts++
		tip, err := fetch(root, cfg)
		if err != nil {
			return rep, err
		}
		commits := newCommits(root, db.KV("sync.last_seen"), tip)
		remote := readEvents(root, commits)
		// our own events come back to us only after a history rewrite; applying them is harmless (log order)
		applied, rejected, err := db.Merge(remote, tip)
		if err != nil {
			return rep, err
		}
		rep.Applied += applied
		rep.Rejected = append(rep.Rejected, rejected...)
		rep.Tip = tip

		pending, seqs := db.Pending()
		if len(pending) == 0 {
			return rep, nil
		}
		commit, err := commitEvents(root, actor, tip, pending)
		if err != nil {
			return rep, err
		}
		if BeforePush != nil {
			BeforePush()
		}
		lost, err := push(root, cfg, commit)
		if err != nil {
			return rep, err
		}
		if lost { // someone else pushed first: refetch, replay, retry
			time.Sleep(time.Duration(40+rand.Intn(160)) * time.Millisecond)
			continue
		}
		_, _ = gitx.Try(root, "update-ref", trackingRef(cfg.Ref), commit)
		if err := db.MarkSent(seqs, commit); err != nil {
			return rep, err
		}
		rep.Pushed, rep.Tip = len(pending), commit
		return rep, nil
	}
	return rep, fmt.Errorf("sync gave up after %d attempts: the remote kept changing", maxTries)
}
