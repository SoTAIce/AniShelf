package catalog

import (
	"math"
	"testing"
)

func TestComposite(t *testing.T) {
	a := Anime{Sources: []Source{{Name: "Bangumi", Score: 8, Scale: 10, Votes: 1000}, {Name: "AniList", Score: 90, Scale: 100, Votes: 1000}}}
	Calculate(&a)
	if a.Composite == nil || *a.Composite != 8.5 {
		t.Fatalf("normalization: %v", a.Composite)
	}
	a.Sources = a.Sources[:1]
	Calculate(&a)
	if a.Composite != nil {
		t.Fatal("single source must not claim composite")
	}
	a.Sources = append(a.Sources, Source{Score: 9, Scale: 10, Votes: 10})
	Calculate(&a)
	if a.Composite == nil || *a.Composite >= 8.1 {
		t.Fatal("small sample must not dominate")
	}
	a.Sources = append(a.Sources, Source{Score: math.NaN(), Scale: 10, Votes: 100})
	Calculate(&a)
	if math.IsNaN(*a.Composite) {
		t.Fatal("invalid score contaminated composite")
	}
}
func TestMissingVotesAndOutOfRange(t *testing.T) {
	a := Anime{Sources: []Source{{Score: 8, Scale: 10, Votes: 0}, {Score: 15, Scale: 10, Votes: 100}, {Score: 8, Scale: 10, Votes: 100}}}
	Calculate(&a)
	if a.Composite != nil {
		t.Fatal("invalid sources counted")
	}
}
