package eval

// Fact records a computed or adjudicated fact with an explicit unknown value.
type Fact int

const (
	FactUnknown Fact = iota
	FactTrue
	FactFalse
	FactNA
)

// Facts holds the six instrument facts except the derived non-repetition fact.
type Facts struct {
	C1      Fact `json:"c1"`
	C2      Fact `json:"c2"`
	C3      Fact `json:"c3"`
	C4Cited Fact `json:"c4_cited"`
	C4Shows Fact `json:"c4_shows"`
	C5      Fact `json:"c5"`
}

// MatchLevel describes how completely a finding detects an Issue.
type MatchLevel int

const (
	LevelNone MatchLevel = iota
	LevelPartial
	LevelFull
)

// Level derives the match level; unknown facts never count as true.
func (f Facts) Level() MatchLevel {
	c1 := f.C1 == FactTrue
	c2 := f.C2 == FactTrue
	c3 := f.C3 == FactTrue
	c4 := f.C4Cited == FactTrue && f.C4Shows == FactTrue
	c5 := f.C5 == FactTrue || f.C5 == FactNA
	if !c2 || (!c1 && !c3) {
		return LevelNone
	}
	if c1 && c2 && c3 && c4 && c5 {
		return LevelFull
	}
	return LevelPartial
}
