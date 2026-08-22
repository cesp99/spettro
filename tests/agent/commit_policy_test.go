package agent_test

import (
	"strings"
	"testing"

	"spettro/internal/agent"
)

func TestEnforceCommitCoAuthor_InjectsOnSimpleCommit(t *testing.T) {
	cmd := `git commit -m "feat: add foo"`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	if !strings.Contains(got, "--trailer 'Co-Authored-By: Spettro <spettro@eyed.to>'") {
		t.Fatalf("expected trailer to be injected, got: %q", got)
	}
	if !strings.HasPrefix(got, `git commit -m "feat: add foo"`) {
		t.Fatalf("expected original command preserved, got: %q", got)
	}
}

func TestEnforceCommitCoAuthor_HandlesAmShortFlag(t *testing.T) {
	cmd := `git commit -am 'wip'`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	if !strings.Contains(got, agent.SpettroCoAuthorTrailerForTesting()) {
		t.Fatalf("expected trailer for -am, got: %q", got)
	}
}

func TestEnforceCommitCoAuthor_HandlesFileForm(t *testing.T) {
	cmd := `git commit -F /tmp/msg`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	if !strings.Contains(got, agent.SpettroCoAuthorTrailerForTesting()) {
		t.Fatalf("expected trailer for -F form, got: %q", got)
	}
}

func TestEnforceCommitCoAuthor_IdempotentWhenAlreadyPresent(t *testing.T) {
	cmd := `git commit -m "fix: x" --trailer 'Co-Authored-By: Spettro <spettro@eyed.to>'`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	if got != cmd {
		t.Fatalf("expected idempotent rewrite, got: %q (orig %q)", got, cmd)
	}
	if strings.Count(got, "Co-Authored-By: Spettro") != 1 {
		t.Fatalf("expected exactly one trailer copy, got %d in %q", strings.Count(got, "Co-Authored-By: Spettro"), got)
	}
}

func TestEnforceCommitCoAuthor_IdempotentWhenTrailerInMessage(t *testing.T) {
	cmd := `git commit -m "fix: x" -m "Co-Authored-By: Spettro <spettro@eyed.to>"`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	if got != cmd {
		t.Fatalf("expected idempotent rewrite when trailer is in body, got: %q", got)
	}
}

func TestEnforceCommitCoAuthor_OnlyTouchesCommitSegment(t *testing.T) {
	cmd := `git add . && git commit -m "x" && git push`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	if !strings.Contains(got, `git commit -m "x" --trailer '`+agent.SpettroCoAuthorTrailerForTesting()+`'`) {
		t.Fatalf("expected trailer attached only to commit segment, got: %q", got)
	}
	if !strings.Contains(got, "git add .") {
		t.Fatalf("expected git add segment preserved: %q", got)
	}
	if strings.Contains(got, `git add . --trailer`) {
		t.Fatalf("trailer must not be appended to non-commit segment: %q", got)
	}
}

func TestEnforceCommitCoAuthor_SkipsCommitTreeAndCommitGraph(t *testing.T) {
	for _, cmd := range []string{
		`git commit-tree HEAD`,
		`git commit-graph write`,
	} {
		got := agent.EnforceCommitCoAuthorForTesting(cmd)
		if got != cmd {
			t.Fatalf("plumbing command must NOT be rewritten: input=%q output=%q", cmd, got)
		}
	}
}

func TestEnforceCommitCoAuthor_HandlesGlobalOptions(t *testing.T) {
	cases := []string{
		`git -C /tmp/repo commit -m "x"`,
		`git --git-dir=/tmp/repo/.git --work-tree=/tmp/repo commit -m "x"`,
		`git -c user.name=foo commit -m "x"`,
	}
	for _, cmd := range cases {
		got := agent.EnforceCommitCoAuthorForTesting(cmd)
		if !strings.Contains(got, agent.SpettroCoAuthorTrailerForTesting()) {
			t.Fatalf("expected trailer for %q, got: %q", cmd, got)
		}
	}
}

func TestEnforceCommitCoAuthor_RespectsQuotedCommitWord(t *testing.T) {
	// "git commit" embedded in a quoted echo arg must NOT trigger rewrite.
	cmd := `echo "git commit -m hello"`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	if got != cmd {
		t.Fatalf("quoted git commit text must not be rewritten: %q -> %q", cmd, got)
	}
}

func TestEnforceCommitCoAuthor_RespectsSubshell(t *testing.T) {
	// `git commit` inside $(...) is opaque — we conservatively skip it.
	cmd := `echo $(git status); git commit -m "x"`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	if !strings.Contains(got, `git commit -m "x" --trailer '`+agent.SpettroCoAuthorTrailerForTesting()+`'`) {
		t.Fatalf("expected trailer on outer commit: %q", got)
	}
}

func TestEnforceCommitCoAuthor_NoCommitNoChange(t *testing.T) {
	cmd := `git status --porcelain && git diff HEAD`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	if got != cmd {
		t.Fatalf("non-commit pipeline must be unchanged: %q -> %q", cmd, got)
	}
}

func TestEnforceCommitCoAuthor_EmptyString(t *testing.T) {
	if got := agent.EnforceCommitCoAuthorForTesting(""); got != "" {
		t.Fatalf("expected empty passthrough, got %q", got)
	}
}

// Regression test for the corrupted-commit bug: a multi-line message passed
// via the `-m "$(cat <<'EOF' ... EOF)"` idiom, where the message text itself
// contains a double quote. The old splitter had no heredoc awareness, so the
// body's `"` flipped its quote state, the next newline was taken as a command
// separator, and the trailer flag was spliced into the middle of the commit
// message (see commit d842429's original message for the wild specimen).
func TestEnforceCommitCoAuthor_HeredocSubshellMessage(t *testing.T) {
	cmd := `git commit -m "$(cat <<'EOF'
goal: cap LLM steps per iteration

The goal preamble ("only
goal-complete finishes the run") previously kept the loop alive.
EOF
)"`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	want := cmd + ` --trailer '` + agent.SpettroCoAuthorTrailerForTesting() + `'`
	if got != want {
		t.Fatalf("expected trailer appended after the closing quote, untouched body.\ngot:  %q\nwant: %q", got, want)
	}
}

func TestEnforceCommitCoAuthor_HeredocTopLevelStdin(t *testing.T) {
	// `git commit -F - <<'MSG'`: the trailer flag must land on the command
	// line, never after (inside) the heredoc body.
	cmd := "git commit -F - <<'MSG'\nsubject line\n\nbody with a \" quote\nMSG"
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	want := "git commit -F - <<'MSG' --trailer '" + agent.SpettroCoAuthorTrailerForTesting() + "'\nsubject line\n\nbody with a \" quote\nMSG"
	if got != want {
		t.Fatalf("expected trailer injected before the heredoc body.\ngot:  %q\nwant: %q", got, want)
	}
}

func TestEnforceCommitCoAuthor_HeredocFollowedByCommand(t *testing.T) {
	cmd := "git commit -F - <<EOF\nmsg (\"quoted\")\nEOF\ngit push"
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	if !strings.Contains(got, "git commit -F - <<EOF --trailer '"+agent.SpettroCoAuthorTrailerForTesting()+"'\nmsg") {
		t.Fatalf("expected trailer on the commit line: %q", got)
	}
	if !strings.HasSuffix(got, "\ngit push") {
		t.Fatalf("expected trailing git push untouched: %q", got)
	}
	if strings.Count(got, "Co-Authored-By: Spettro") != 1 {
		t.Fatalf("expected exactly one trailer, got: %q", got)
	}
}

func TestEnforceCommitCoAuthor_HeredocIdempotentWhenTrailerInBody(t *testing.T) {
	cmd := `git commit -m "$(cat <<'EOF'
fix: x

Co-Authored-By: Spettro <spettro@eyed.to>
EOF
)"`
	if got := agent.EnforceCommitCoAuthorForTesting(cmd); got != cmd {
		t.Fatalf("heredoc body already carrying the trailer must be untouched: %q", got)
	}
}

func TestEnforceCommitCoAuthor_UnterminatedHeredocLeftAlone(t *testing.T) {
	for _, cmd := range []string{
		"git commit -F - <<'EOF'\nnever closed",
		"git commit -F - <<'EOF'",
		"git commit -F - <<''\nEOF",
	} {
		if got := agent.EnforceCommitCoAuthorForTesting(cmd); got != cmd {
			t.Fatalf("ambiguous heredoc must be left as-is: %q -> %q", cmd, got)
		}
	}
}

func TestEnforceCommitCoAuthor_SubshellInsideDoubleQuotes(t *testing.T) {
	// `$(...)` inside "..." opens a fresh quoting context in bash; the inner
	// double quotes must not corrupt the outer state.
	cmd := `git commit -m "$(printf "%s" "fix: x")"; git push`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	want := `git commit -m "$(printf "%s" "fix: x")" --trailer '` + agent.SpettroCoAuthorTrailerForTesting() + `'; git push`
	if got != want {
		t.Fatalf("expected trailer before the separator.\ngot:  %q\nwant: %q", got, want)
	}
}

// Regression: a double-quoted -m message whose TEXT talks about the
// `\$(cat <<'EOF' ... EOF)` idiom (e.g. a commit message describing this very
// feature). The escaped `\$` must not open a phantom subshell — doing so
// resets the quote state, makes the literal `<<'EOF'` register as a live
// heredoc that never terminates, and aborts the parse, silently skipping
// trailer injection (observed in the wild on commit 0b9dcf6).
func TestEnforceCommitCoAuthor_EscapedDollarAndHeredocTextInMessage(t *testing.T) {
	cmd := `git commit -m "fix: make trailer injection heredoc-aware` + "\n\n" +
		`Previously a multi-line -m \"\$(cat <<'EOF' ... EOF)\" message whose` + "\n" +
		`body contained a double quote (git commit -F - <<'EOF' ...) corrupted` + "\n" +
		`the closing )\" tracking." && git status`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	if !strings.Contains(got, `tracking." --trailer '`+agent.SpettroCoAuthorTrailerForTesting()+`' && git status`) {
		t.Fatalf("expected trailer before && separator, got: %q", got)
	}
	if strings.Count(got, "Co-Authored-By: Spettro") != 1 {
		t.Fatalf("expected exactly one trailer, got: %q", got)
	}
}

func TestEnforceCommitCoAuthor_HereStringNotAHeredoc(t *testing.T) {
	cmd := `git commit -F - <<<"fix: x"`
	got := agent.EnforceCommitCoAuthorForTesting(cmd)
	if !strings.Contains(got, agent.SpettroCoAuthorTrailerForTesting()) {
		t.Fatalf("here-string commit should still get the trailer: %q", got)
	}
}

func TestIsGitCommitInvocation(t *testing.T) {
	yes := []string{
		`git commit`,
		`git commit -m 'x'`,
		`git  commit  --amend`,
		`/usr/bin/git commit -m x`,
		`git -C dir commit -m x`,
		`git -c key=val commit`,
	}
	for _, c := range yes {
		if !agent.IsGitCommitInvocationForTesting(c) {
			t.Errorf("expected %q to be a git commit invocation", c)
		}
	}
	no := []string{
		`git status`,
		`git commit-tree`,
		`git commit-graph write`,
		`gitcommit -m x`,
		`echo git commit`,
		``,
	}
	for _, c := range no {
		if agent.IsGitCommitInvocationForTesting(c) {
			t.Errorf("expected %q NOT to match", c)
		}
	}
}

func TestLexShellTokens_HandlesQuotesAndEscapes(t *testing.T) {
	got := agent.LexShellTokensForTesting(`git commit -m "feat: with spaces" --trailer 'a b'`)
	want := []string{"git", "commit", "-m", "feat: with spaces", "--trailer", "a b"}
	if len(got) != len(want) {
		t.Fatalf("token count mismatch: got %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token %d: got %q want %q", i, got[i], want[i])
		}
	}
}
