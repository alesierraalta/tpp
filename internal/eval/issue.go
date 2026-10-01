package eval

// Issue is one ground-truth defect in a case.
type Issue struct {
	Case         string       `json:"case"`
	ID           string       `json:"id"`
	Domain       Domain       `json:"domain"`
	Severity     Severity     `json:"severity"`
	IssueType    string       `json:"issue_type"`
	Reproduction Reproduction `json:"reproduction"`
}

// Reproduction describes how an Issue is reproduced by its oracle.
type Reproduction struct {
	Applies          bool   `json:"applies"`
	Oracle           string `json:"oracle"`
	Nondeterministic bool   `json:"nondeterministic"`
	Attempts         int    `json:"attempts"`
}

// Key returns the case-qualified Issue identifier.
func (i Issue) Key() string {
	return i.Case + "/" + i.ID
}
