package publish

import (
	"strings"
	"testing"
)

func TestReviewTalkSaysWhatEachNameIs(t *testing.T) {
	lists := Lists{
		Churches: []Term{{ID: 128, Name: "Emmanuel Bishopston"}},
		Types:    []Term{{ID: 270, Name: "Sermon"}},
		Speakers: siteSpeakers,
		Series:   siteSeries,
		Books:    []Term{{ID: 239, Name: "Matthew"}},
	}
	talk := Talk{
		Title: "A talk", Church: "emmanuel bishopston", Podcast: true,
		Speakers:    []string{"jim walford", "James Morris", "Zed Newcomer"},
		NewSpeakers: []string{"Zed Newcomer"},
		Series:      "the Lord’s Prayer (EB)", Passage: "Matt 6:9-13",
	}
	review := ReviewTalk(lists, talk, "")
	if len(review.Speakers) != 3 {
		t.Fatalf("speakers = %+v", review.Speakers)
	}
	if first := review.Speakers[0]; !first.Known || first.Site != "Jim Walford" {
		t.Errorf("a known speaker typed in lower case = %+v", first)
	}
	if second := review.Speakers[1]; second.Known || second.New || len(second.Near) == 0 || second.Near[0] != "James Maurice" {
		t.Errorf("a near miss = %+v", second)
	}
	if third := review.Speakers[2]; third.Known || !third.New {
		t.Errorf("a confirmed new speaker = %+v", third)
	}
	if review.Series == nil || !review.Series.Known {
		t.Errorf("series = %+v", review.Series)
	}
	if len(review.Books) != 1 || review.Books[0] != "Matthew" {
		t.Errorf("books = %q", review.Books)
	}
	if len(review.Problems) != 1 || review.Problems[0] != `The website has no speaker called "James Morris"; did you mean "James Maurice"? If they are new to the website, mark them as new.` {
		t.Errorf("problems = %q", review.Problems)
	}
	if len(review.Warnings) != 1 || !strings.Contains(review.Warnings[0], `"Zed Newcomer" will be added`) {
		t.Errorf("warnings = %q", review.Warnings)
	}

	empty := ReviewTalk(lists, Talk{Church: "Emmanuel Westbury"}, "Sermon")
	joined := strings.Join(empty.Problems, " | ")
	for _, want := range []string{"needs a title", `The website has no church called "Emmanuel Westbury"`, "needs a speaker"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems %q lack %q", joined, want)
		}
	}
}

func TestCapitaliseTakesTheWholeFirstLetter(t *testing.T) {
	for in, want := range map[string]string{"": "", "the site": "The site", "émile was not found": "Émile was not found", "42 talks": "42 talks"} {
		if got := Capitalise(in); got != want {
			t.Errorf("Capitalise(%q) = %q, want %q", in, got, want)
		}
	}
}
