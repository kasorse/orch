package logging

import "fmt"

// This file prints the end of a worktree run after its commit: the push of its branch and the
// merge request that push opened (specs/delivery/SPEC.md §7.3).

// NoDelivery says why a completed worktree run neither pushed nor opened a merge request: nothing
// was committed, or somebody said no.
func (r *ConsoleReporter) NoDelivery(why string) {
	r.stopSpin()
	text := "No push and no merge request: " + firstLine(why)
	if r.Pretty {
		fmt.Fprintln(r.Out, r.st().yellow("⚠ "+text))
		return
	}
	fmt.Fprintln(r.Out, text)
}

// Pushed reports a branch pushed without a merge request.
func (r *ConsoleReporter) Pushed(branch, remote string) {
	r.stopSpin()
	if r.Pretty {
		fmt.Fprintf(r.Out, "%s %s %s %s\n", r.st().green("✓"), "Pushed", branch, r.st().gray("to "+remote+"."))
		return
	}
	fmt.Fprintf(r.Out, "Pushed %s to %s.\n", branch, remote)
}

// MergeRequest reports the merge request the push opened.
func (r *ConsoleReporter) MergeRequest(iid int, url string) {
	r.stopSpin()
	if r.Pretty {
		fmt.Fprintf(r.Out, "%s Merge request !%d: %s\n", r.st().green("✓"), iid, url)
		return
	}
	fmt.Fprintf(r.Out, "Merge request !%d: %s\n", iid, url)
}

// DeliveryFailed reports a push or merge request that did not happen, with what the operator can
// do by hand: manual is the command that pushes the branch, create the address that opens the
// merge request; either may be "".
func (r *ConsoleReporter) DeliveryFailed(msg, manual, create string) {
	r.stopSpin()
	if r.Pretty {
		st := r.st()
		fmt.Fprintln(r.Out, st.red("✗ "+firstLine(msg)))
		if manual != "" {
			fmt.Fprintf(r.Out, "  %s\n  %s\n", st.gray("To push it yourself:"), manual)
		}
		if create != "" {
			fmt.Fprintf(r.Out, "  %s\n  %s\n", st.gray("To create the merge request:"), create)
		}
		return
	}
	fmt.Fprintf(r.Out, "Error: %s\n", firstLine(msg))
	if manual != "" {
		fmt.Fprintf(r.Out, "To push it yourself: %s\n", manual)
	}
	if create != "" {
		fmt.Fprintf(r.Out, "To create the merge request: %s\n", create)
	}
}
