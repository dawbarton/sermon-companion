// Checking a talk's details against the site's lists without sending
// anything: which names the site already has, which are near misses for one
// it has, which the operator has confirmed are new, and which books the
// passage files the talk under. The upload page shows this as the details are
// typed, from the copy of the lists kept on disk, and the upload repeats it
// against the site's current lists before changing anything.
package publish

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dawbarton/sermon-companion/internal/store"
)

// Talk is the operator's description of a talk, as the session keeps it.
type Talk = store.Talk

// Progress is what an upload has created on the site so far.
type Progress = store.PublishProgress

// Lists are the site's terms a talk is filed under.
type Lists struct {
	Churches []Term `json:"churches"`
	Types    []Term `json:"types"`
	Speakers []Term `json:"speakers"`
	Series   []Term `json:"series"`
	Books    []Term `json:"books"`
}

// NameCheck is what the site's list says about one name.
type NameCheck struct {
	Name string `json:"name"`
	// Known means the site has exactly this name, however it was typed.
	Known bool `json:"known"`
	// Site is the name as the site spells it, when Known.
	Site string `json:"site,omitempty"`
	// Near are the site's names this one might be a slip for.
	Near []string `json:"near,omitempty"`
	// New means the operator has said the name is new to the site.
	New  bool `json:"new"`
	term Term
}

// Review is the result of checking a talk against the lists. Problems stop
// the upload; Warnings are for the operator to read.
type Review struct {
	Speakers []NameCheck `json:"speakers"`
	Series   *NameCheck  `json:"series,omitempty"`
	Books    []string    `json:"books"`
	Problems []string    `json:"problems"`
	Warnings []string    `json:"warnings"`

	church Term
	kind   Term
	books  []Term
}

// ReviewTalk checks talk against lists. typeName is the kind of resource a
// talk is filed as, normally "Sermon".
func ReviewTalk(lists Lists, talk Talk, typeName string) Review {
	r := Review{Speakers: []NameCheck{}, Books: []string{}, Problems: []string{}, Warnings: []string{}}
	problem := func(format string, args ...any) { r.Problems = append(r.Problems, fmt.Sprintf(format, args...)) }

	if strings.TrimSpace(talk.Title) == "" {
		problem("The talk needs a title.")
	}
	if term, err := existing(lists.Churches, talk.Church, "church"); err != nil {
		problem("%s.", Capitalise(err.Error()))
	} else {
		r.church = term
	}
	if strings.TrimSpace(typeName) == "" {
		typeName = "Sermon"
	}
	if term, err := existing(lists.Types, typeName, "resource type"); err != nil {
		problem("%s.", Capitalise(err.Error()))
	} else {
		r.kind = term
	}

	confirmed := map[string]bool{}
	for _, name := range talk.NewSpeakers {
		confirmed[nameKey(name)] = true
	}
	if len(talk.Speakers) == 0 {
		problem("The talk needs a speaker.")
	}
	for _, name := range talk.Speakers {
		check := checkName(lists.Speakers, name, confirmed[nameKey(name)])
		r.Speakers = append(r.Speakers, check)
		r.note(check, "speaker", "they are")
	}
	if strings.TrimSpace(talk.Series) != "" {
		check := checkName(lists.Series, talk.Series, talk.NewSeries)
		r.Series = &check
		r.note(check, "series", "it is")
	}

	books, err := PassageBooks(talk.Passage)
	if err != nil {
		problem("%s.", Capitalise(strings.TrimSuffix(err.Error(), ".")))
	} else if terms, err := BookTerms(books, lists.Books); err != nil {
		problem("%s.", Capitalise(strings.TrimSuffix(err.Error(), ".")))
	} else {
		r.books = terms
		for _, term := range terms {
			r.Books = append(r.Books, term.Name)
		}
		if len(terms) == 0 {
			r.Warnings = append(r.Warnings, "There is no Bible passage, so the talk will not be filed under any book.")
		}
	}
	if !talk.Podcast {
		r.Warnings = append(r.Warnings, "The talk will be left out of the podcast feed.")
	}
	return r
}

func checkName(terms []Term, name string, confirmed bool) NameCheck {
	name = strings.Join(strings.Fields(name), " ")
	match := MatchTerm(terms, name)
	check := NameCheck{Name: name}
	if match.Exact != nil {
		check.Known, check.Site, check.term = true, match.Exact.Name, *match.Exact
		return check
	}
	for _, near := range match.Near {
		check.Near = append(check.Near, near.Name)
	}
	check.New = confirmed
	check.term = Term{Name: name}
	return check
}

// note records what a checked name means for the upload.
func (r *Review) note(check NameCheck, what, pronoun string) {
	switch {
	case check.Known:
	case !check.New:
		missing := fmt.Sprintf("The website has no %s called %q.", what, check.Name)
		if len(check.Near) > 0 {
			missing = fmt.Sprintf("The website has no %s called %q; did you mean %s?", what, check.Name, quotedList(check.Near))
		}
		r.Problems = append(r.Problems, fmt.Sprintf("%s If %s new to the website, mark %s as new.", missing, pronoun, objectPronoun(pronoun)))
	case len(check.Near) > 0:
		r.Warnings = append(r.Warnings, fmt.Sprintf("The %s %q will be added to the website, which already has %s. Check it is not one of those.",
			what, check.Name, quotedList(check.Near)))
	default:
		r.Warnings = append(r.Warnings, fmt.Sprintf("The %s %q will be added to the website.", what, check.Name))
	}
}

func objectPronoun(pronoun string) string {
	if pronoun == "they are" {
		return "them"
	}
	return "it"
}

func quotedList(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	if len(quoted) == 1 {
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

// Capitalise gives a message a capital first letter, for the start of a
// sentence shown to the operator. Messages can begin with a name, so the first
// letter is taken as a whole character rather than a byte.
func Capitalise(text string) string {
	first, size := utf8.DecodeRuneInString(text)
	if first == utf8.RuneError {
		return text
	}
	return string(unicode.ToUpper(first)) + text[size:]
}
