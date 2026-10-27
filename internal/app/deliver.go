package app

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/interaction"
	"orchestrator/internal/report"
	"orchestrator/internal/secretscan"
)

const (
	// maxMRTitleBytes is the longest merge request title (specs/delivery/SPEC.md §7.3).
	maxMRTitleBytes = 255
	// maxPushOptionBytes bounds one push option, the whole "merge_request.description=..." once
	// its line breaks are encoded: an option travels as one pkt-line of at most 65 516 bytes.
	maxPushOptionBytes = 64_000
	// maxMRRecordBytes bounds the text run.json keeps of a skip or an error.
	maxMRRecordBytes = 500

	descriptionOption = "merge_request.description="
)

// commitStop is why offerCommit made no commit: a person said no, or something kept it from
// committing (a warning under --mode auto, a refusal, an error). The first ends the chain with
// exit 0, the second needs a person (specs/delivery/SPEC.md §7.4).
type commitStop struct {
	why      string
	declined bool
}

// skipDelivery ends the chain of a completed worktree run that has no commit: it records
// mr.skipped and says that nothing was pushed (specs/delivery/SPEC.md §7.4). A no at the commit
// question is the person's decision and returns nil; anything else is a run that needs a person.
func skipDelivery(s *session) error {
	if s.noCommit.declined {
		recordMR(s, domain.RunMR{Skipped: "declined at commit"})
		return nil
	}
	why := firstNonEmpty(s.noCommit.why, "nothing was committed")
	recordMR(s, domain.RunMR{Skipped: oneLineText(why, maxMRRecordBytes)})
	s.reporter.NoDelivery(why)
	return needsPerson("nothing was delivered: " + why)
}

// deliver pushes the run's branch, which opens its merge request with the same push, after the
// commit and the conflict check and before the worktree is removed (specs/delivery/SPEC.md
// §7.1-§7.3). Any mode but auto asks first, for the push and then for the merge request, and a
// no stops there. It is called from the operator's repository, the branch being shared with the
// worktree: only after that repository's git fingerprint matched the one taken when the
// worktree was created, and the remote showed no branch of that name. The push is never
// repeated: run.json: mr says intent before it. Nothing here changes the run's status; what did
// not happen is recorded in run.mr and returned as a NeedsPersonError (exit code 6), unless a
// person said no.
func deliver(ctx context.Context, s *session, sha string) error {
	run, w := s.run, s.worktree
	remote := s.cfg.Delivery.RemoteName()
	target := firstNonEmpty(w.BaseBranch, s.cfg.Delivery.TargetBranch)
	out := s.reporter.PausingWriter()

	wantMR := true
	if run.Mode != domain.ModeAuto {
		ok, err := interaction.AskPush(s.prompter, out, w.Branch, remote)
		if err != nil {
			fmt.Fprintf(out, "No answer (%v); nothing pushed.\n", err)
		}
		if !ok {
			fmt.Fprintf(out, "Not pushed; the branch %s stays in your repository.\n", w.Branch)
			recordMR(s, domain.RunMR{Skipped: "declined at push"})
			return nil
		}
		if wantMR, err = interaction.AskMergeRequest(s.prompter, out, w.Branch, target); err != nil {
			fmt.Fprintf(out, "No answer (%v); no merge request.\n", err)
		}
	}

	// git push runs the operator's hooks, and ls-remote and push their credential helpers: they
	// must be the ones the worktree was created with.
	fingerprint, err := s.operator.GitFingerprint(ctx)
	switch {
	case err != nil:
		return stopBeforePush(s, "skipped", "cannot read the git config and hooks: "+err.Error())
	case fingerprint != s.operatorFingerprint:
		return stopBeforePush(s, "skipped", guardChanged)
	case target == "":
		return stopBeforePush(s, "error", "there is no target branch: HEAD was detached and delivery.target_branch is not set")
	}
	remoteURL, err := s.operator.RemoteURL(ctx, remote)
	if err != nil {
		return stopBeforePush(s, "error", fmt.Sprintf("cannot read the URL of the remote %s: %v", remote, err))
	}
	// A branch of that name on the remote is somebody else's, even an ancestor of this one.
	switch present, err := s.operator.LsRemoteHead(ctx, remote, w.Branch); {
	case err != nil:
		return stopBeforePush(s, "error", fmt.Sprintf("cannot ask %s whether it has %s: %v", remote, w.Branch, err))
	case present:
		return stopBeforePush(s, "error", fmt.Sprintf("the branch %s already exists on %s; it is not pushed over", w.Branch, remote))
	}

	var options []string
	if wantMR {
		options = mergeRequestOptions(ctx, s, sha, target)
	}
	mr := domain.RunMR{State: domain.MRIntent, Remote: remote, Source: w.Branch, Target: target}
	run.MR = &mr
	if err := s.store.SaveRun(run); err != nil {
		run.MR = nil
		return stopBeforePush(s, "error", "run.json could not record the push intent, so nothing was pushed: "+err.Error())
	}

	// A story child pushes without -u (specs/delivery/SPEC.md §7.3, D22): a sibling task of the same
	// repository may be running, and its guard compares the git config the upstream is written to.
	storyTask := run.Task.Source != nil && run.Task.Source.Kind == domain.TaskSourceStory
	push, byHand := s.operator.Push, "git push -u "
	if storyTask {
		push, byHand = s.operator.PushWithoutUpstream, "git push "
	}
	stderr, err := push(ctx, remote, w.Branch, options)
	if err != nil {
		mr.Error = pushFailure(stderr, err)
		recordMR(s, mr)
		s.reporter.DeliveryFailed(mr.Error, byHand+shellWord(remote)+" "+shellWord(w.Branch), "")
		return needsPerson("its branch was not pushed: " + mr.Error)
	}
	mr.State = domain.MRPushed
	if !wantMR {
		mr.Skipped = "declined at merge request"
		recordMR(s, mr)
		s.reporter.Pushed(w.Branch, remote)
		return nil
	}
	if found, iid, ok := mergeRequestURL(stderr, remoteURL); ok {
		mr.State, mr.IID, mr.URL = domain.MRCreated, iid, found
		recordMR(s, mr)
		s.reporter.MergeRequest(iid, found)
		return nil
	}
	mr.Error = "the remote reported no merge request"
	recordMR(s, mr)
	s.reporter.DeliveryFailed(mr.Error, "", createLink(stderr, remoteURL))
	return needsPerson("the branch " + w.Branch + " was pushed, but " + mr.Error)
}

// stopBeforePush ends the chain before anything was pushed: kind "skipped" records mr.skipped
// (the person has nothing to fix), "error" mr.error (the remote or the repository is not as the
// push needs it).
func stopBeforePush(s *session, kind, why string) error {
	why = oneLineText(why, maxMRRecordBytes)
	if kind == "skipped" {
		recordMR(s, domain.RunMR{Skipped: why})
	} else {
		recordMR(s, domain.RunMR{Error: why})
	}
	s.reporter.DeliveryFailed(why, "", "")
	return needsPerson("its branch was not pushed: " + why)
}

// guardChanged is the reason a changed git config or hook set stops the push.
const guardChanged = "the git config or hooks changed since the worktree was created; nothing was pushed"

// recordMR saves run.mr. The run completed: a failure to save it is shown, not returned.
func recordMR(s *session, mr domain.RunMR) {
	s.run.MR = &mr
	if err := s.store.SaveRun(s.run); err != nil {
		fmt.Fprintf(s.reporter.PausingWriter(), "run.json could not record the merge request state: %v\n", err)
	}
}

func needsPerson(why string) error {
	return &apperr.NeedsPersonError{Msg: "the run completed, but " + why}
}

// mergeRequestOptions are the push options that open the merge request (specs/delivery/SPEC.md
// §7.3): the commit header as the title and report.md as the description, rendered after the
// commit, with line breaks written as \n. The report is rendered again with less in it until
// the whole description option fits maxPushOptionBytes.
func mergeRequestOptions(ctx context.Context, s *session, sha, target string) []string {
	subject, err := s.operator.CommitSubject(ctx, sha)
	if err != nil || strings.TrimSpace(subject) == "" {
		subject = s.worktree.Branch
	}
	title := cutBytes(pushOptionText(strings.Join(strings.Fields(subject), " ")), maxMRTitleBytes)
	return []string{
		"merge_request.create",
		"merge_request.target=" + target,
		"merge_request.title=" + title,
		descriptionOption + mergeRequestDescription(*s.run, loadTrace(s.store, s.run.ID), maxPushOptionBytes),
	}
}

// mergeRequestDescription is report.md as the value of the description option: the report
// rendered for run as it is now, with line breaks encoded (pushOptionText), and so short that
// the whole option, "merge_request.description=" included, is at most maxOption bytes. When the
// report is longer it is rendered again with smaller lists, then a shorter task quote, and as a
// last resort cut at a line with "…(cut)" (report.RunReportFit).
func mergeRequestDescription(run domain.Run, trace *domain.DecisionTrace, maxOption int) string {
	text := report.RunReportFit(run, trace, func(text string) bool {
		return len(descriptionOption)+len(pushOptionText(text)) <= maxOption
	})
	return pushOptionText(text)
}

// pushOptionText is s as the text of a push option: git refuses an option with a line feed or a
// NUL, so a carriage return is dropped, a NUL dropped and every line feed written as the two
// characters \n, which GitLab turns back into a line break (specs/delivery/SPEC.md §7.3, A17).
func pushOptionText(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\x00", "")
	return strings.ReplaceAll(s, "\n", `\n`)
}

// mergeRequestURL is the merge request a GitLab remote reports on the stderr of the push that
// opened it (specs/delivery/SPEC.md §7.3): the first line after "View merge request for" that is
// exactly https://<host>/<path>/-/merge_requests/<n>, <host> being the remote's host. The "To
// create a merge request" link (.../merge_requests/new?...), another host and a URL with
// userinfo, a query or a fragment never match.
func mergeRequestURL(stderr, remoteURL string) (found string, iid int, ok bool) {
	host := remoteHost(remoteURL)
	if host == "" {
		return "", 0, false
	}
	seen := false
	for _, line := range remoteLines(stderr) {
		if strings.HasPrefix(line, "View merge request for") {
			seen = true
			continue
		}
		if !seen {
			continue
		}
		if n, ok := mergeRequestNumber(line, host); ok {
			return line, n, true
		}
	}
	return "", 0, false
}

// createLink is the address GitLab prints for opening a merge request by hand ("To create a
// merge request for <branch>, visit:"), when it is on the remote's host; "" otherwise.
func createLink(stderr, remoteURL string) string {
	host := remoteHost(remoteURL)
	if host == "" {
		return ""
	}
	seen := false
	for _, line := range remoteLines(stderr) {
		if strings.HasPrefix(line, "To create a merge request") {
			seen = true
			continue
		}
		if !seen {
			continue
		}
		if u, ok := httpsURLOn(line, host); ok && u.RawQuery != "" && strings.HasSuffix(u.Path, "/-/merge_requests/new") {
			return line
		}
	}
	return ""
}

// remoteLines are the lines of a push's stderr without the "remote:" git puts before what the
// remote printed, trimmed.
func remoteLines(stderr string) []string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "remote:"))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// mergeRequestNumber is the number of the merge request line names, when line is exactly its
// URL on host.
func mergeRequestNumber(line, host string) (int, bool) {
	u, ok := plainHTTPSURL(line, host)
	if !ok {
		return 0, false
	}
	const marker = "/-/merge_requests/"
	i := strings.LastIndex(u.Path, marker)
	if i < 1 {
		return 0, false
	}
	digits := u.Path[i+len(marker):]
	if digits == "" || digits[0] == '0' || strings.Trim(digits, "0123456789") != "" || len(digits) > 9 {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	return n, err == nil
}

// plainHTTPSURL parses line as an https URL on host, without userinfo, query or fragment.
func plainHTTPSURL(line, host string) (*url.URL, bool) {
	u, ok := httpsURLOn(line, host)
	if !ok || u.RawQuery != "" {
		return nil, false
	}
	return u, true
}

// httpsURLOn parses line as an https URL on host, without userinfo or fragment.
func httpsURLOn(line, host string) (*url.URL, bool) {
	if strings.ContainsAny(line, " \t") {
		return nil, false
	}
	u, err := url.Parse(line)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" ||
		!strings.EqualFold(u.Hostname(), host) {
		return nil, false
	}
	return u, true
}

// remoteHost is the host of a remote's URL, userinfo and port dropped, lower case: "" for one
// without a host, such as a local path. It reads both a URL (https://user:pw@host/p.git,
// ssh://git@host:22/p.git) and the scp form (git@host:p.git).
func remoteHost(remoteURL string) string {
	raw := strings.TrimSpace(remoteURL)
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		return strings.ToLower(u.Hostname())
	}
	if i := strings.IndexByte(raw, ':'); i > 0 && !strings.Contains(raw[:i], "/") {
		host := raw[:i]
		if j := strings.LastIndexByte(host, '@'); j >= 0 {
			host = host[j+1:]
		}
		return strings.ToLower(strings.Trim(host, "[]"))
	}
	return ""
}

// urlUserinfo matches the user[:password]@ of a URL inside a message.
var urlUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s'"]*@`)

// pushFailure is the line of a failed push's stderr that says what went wrong: git's last line,
// not the "failed to push some refs", the hints or "the remote end hung up" that follow the
// cause. It is one redacted line with no credential in a URL; without any output, the error
// itself.
func pushFailure(stderr string, err error) string {
	var lines []string
	for _, l := range strings.Split(stderr, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	pick := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if l := lines[i]; !strings.HasPrefix(l, "error: failed to push some refs") && !strings.HasPrefix(l, "hint:") &&
			l != "fatal: the remote end hung up unexpectedly" {
			pick = l
			break
		}
	}
	if pick == "" && len(lines) > 0 {
		pick = lines[len(lines)-1]
	}
	if pick == "" {
		pick = err.Error()
	}
	return oneLineText(pick, maxMRRecordBytes)
}

// oneLineText is s on one line, redacted, with no credential in a URL, cut to max bytes.
func oneLineText(s string, max int) string {
	s = urlUserinfo.ReplaceAllString(strings.Join(strings.Fields(s), " "), "$1")
	return cutBytes(secretscan.Redact(s), max)
}

// cutBytes is s cut to at most max bytes at a rune boundary.
func cutBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !isRuneStart(s[max]) {
		max--
	}
	return s[:max]
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// shellWord is s as one word of a command a person pastes into a shell.
func shellWord(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._/@%+=:,-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
