package gate

import (
	"strings"

	"github.com/alesierraalta/tsp/internal/plan"
)

// BuildAuditReason is what the Stop says when the discipline ran. Covering a diff and reporting
// as though the surface were covered is the failure this catches: the run looks complete because
// every step it did take succeeded.
func BuildAuditReason(planPath, gaps string, offerFeedback ...bool) string {
	return joinReason([]string{
		"This session ran the testing discipline and left breadth owed. " + planPath + " says:",
		"",
		strings.TrimRight(gaps, "\n"),
		"",
		"A layer assigned and never invoked is an open gap, not a silence. Before reporting this",
		"done, either invoke the owners above, or say plainly in one line which surfaces went",
		"unexamined, so nobody reads depth on a diff as coverage of the whole.",
	}, offerFeedback)
}

// BuildCompleteReason is what the Stop says when the plan owes nothing. A run with no gaps is
// still worth grading: the question is what it would have missed, not whether it finished.
func BuildCompleteReason(planPath string, offerFeedback ...bool) string {
	return joinReason([]string{
		"This session ran the testing discipline and " + planPath + " owes nothing: every layer",
		"the plan assigned was swept and every ranked target is done.",
	}, offerFeedback)
}

// BuildMicroCompleteReason is what the Stop says when a micro plan owes nothing. A micro plan owes no layer
// sweep, so the sentence says that instead of claiming the layers were swept.
func BuildMicroCompleteReason(planPath string, offerFeedback ...bool) string {
	return joinReason([]string{
		"This session ran the testing discipline and " + planPath + " owes nothing: it is a micro plan for",
		"one small function, which owes no layer sweep, so no other surface was examined.",
	}, offerFeedback)
}

// BuildDeclarationProblem is what the Stop says when the plan declaration cannot be read. It replaces
// the silence: the gate cannot audit a plan it cannot resolve, and the operator is the one who can
// repair the file. Removing the declaration falls back to the default plan, which is a decision.
func BuildDeclarationProblem(err error) string {
	return strings.Join([]string{
		"The plan declaration could not be read, so this run's breadth was not audited:",
		"",
		"  " + err.Error(),
		"",
		"Fix " + plan.ConfigName + " at the worktree root, or remove it to fall back to " + plan.DefaultPath + ".",
	}, "\n")
}

func joinReason(lines []string, offerFeedback []bool) string {
	includeOffer := feedbackOfferEnabled()
	if len(offerFeedback) > 0 {
		includeOffer = offerFeedback[0]
	}
	if includeOffer {
		lines = append(lines, "", feedbackOffer)
	}
	return strings.Join(lines, "\n")
}

const feedbackOffer = "Want the run graded? Offer the operator feedback on the testing itself with " +
	"`tpp feedback --template`, in one short pass: what was executed, what was skipped, which " +
	"findings a different order would have surfaced first, and what you would still not know if the " +
	"suite were green. This is a reminder, not an approval gate. Silence it for this repository with " +
	"a .no-testing-gate file."
