// Preparing an upload: everything the talk needs is looked up on the site and
// worked out before any request that changes anything is made. A name that is
// not on the site stops the upload with the near matches listed, unless the
// operator has said it is new, so that a typing slip cannot create a duplicate
// speaker or series.
package publish

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The taxonomies a talk is filed under, by their REST bases on the site.
const (
	churchTaxonomy  = "resource-church"
	typeTaxonomy    = "resource-type"
	speakerTaxonomy = "resource-contributor"
	seriesTaxonomy  = "resource-series"
	bookTaxonomy    = "bible_book"
)

// Talk is what the operator says about a talk. Everything else comes from the
// session or from the site.
type Talk struct {
	Title string `json:"title"`
	// Speaker and Series are names, matched against the site's lists.
	Speaker string `json:"speaker"`
	Series  string `json:"series,omitempty"`
	// NewSpeaker and NewSeries allow a name that is not on the site yet.
	NewSpeaker bool `json:"newSpeaker,omitempty"`
	NewSeries  bool `json:"newSeries,omitempty"`
	// Passage is shown on the site as typed; the books are read from it.
	Passage string `json:"passage,omitempty"`
}

// Options are the choices made for this upload rather than for this talk.
type Options struct {
	Status  string // "publish", "private", or "draft"
	Podcast bool   // whether the talk goes into the site's podcast feed
	Type    string // the resource-type term, normally "Sermon"
}

// Plan is an upload with every term found and every value worked out, so that
// carrying it out is a fixed sequence of requests.
type Plan struct {
	Title     string
	Passage   string
	Started   time.Time // when the service started; the talk is dated by it
	LocalDate string    // the service's date as the site's Date field takes it
	Church    Term
	Type      Term
	Speaker   Term  // ID zero means it is to be created
	Series    *Term // nil when there is none; ID zero means it is to be created
	Books     []Term
	File      string
	FileName  string
	Size      int64
	Duration  time.Duration
	Status    string
	Podcast   bool
}

var statuses = map[string]bool{"publish": true, "private": true, "draft": true}

// Prepare looks the talk's terms up on the site and returns the plan, with any
// warnings the operator should read before it is carried out. It only reads.
func Prepare(ctx context.Context, c *Client, export *Export, talk Talk, opts Options, duration time.Duration) (*Plan, []string, error) {
	if !statuses[opts.Status] {
		return nil, nil, fmt.Errorf("the status must be publish, private, or draft, not %q", opts.Status)
	}
	if strings.TrimSpace(talk.Title) == "" {
		return nil, nil, errors.New("the talk needs a title")
	}
	if strings.TrimSpace(talk.Speaker) == "" {
		return nil, nil, errors.New("the talk needs a speaker")
	}
	if strings.TrimSpace(opts.Type) == "" {
		opts.Type = "Sermon"
	}
	var warnings []string

	books, err := PassageBooks(talk.Passage)
	if err != nil {
		return nil, nil, err
	}
	if len(books) == 0 {
		warnings = append(warnings, "There is no Bible passage, so the talk will not be filed under any book.")
	}

	lists := make(map[string][]Term)
	for _, taxonomy := range []string{churchTaxonomy, typeTaxonomy, speakerTaxonomy, seriesTaxonomy, bookTaxonomy} {
		if lists[taxonomy], err = c.Terms(ctx, taxonomy); err != nil {
			return nil, nil, err
		}
	}

	plan := &Plan{
		Title:     strings.TrimSpace(talk.Title),
		Passage:   strings.TrimSpace(talk.Passage),
		Started:   export.Session.StartedAt,
		LocalDate: export.Session.StartedAt.In(time.Local).Format("20060102"),
		File:      export.Path,
		FileName:  export.Name,
		Size:      export.Size,
		Duration:  duration,
		Status:    opts.Status,
		Podcast:   opts.Podcast,
	}
	if plan.Church, err = existing(lists[churchTaxonomy], export.Session.Church, "church"); err != nil {
		return nil, nil, err
	}
	if plan.Type, err = existing(lists[typeTaxonomy], opts.Type, "resource type"); err != nil {
		return nil, nil, err
	}
	var warning string
	if plan.Speaker, warning, err = choose(lists[speakerTaxonomy], talk.Speaker, talk.NewSpeaker, "speaker", "newSpeaker"); err != nil {
		return nil, nil, err
	}
	warnings = appendNonEmpty(warnings, warning)
	if strings.TrimSpace(talk.Series) != "" {
		series, warning, err := choose(lists[seriesTaxonomy], talk.Series, talk.NewSeries, "series", "newSeries")
		if err != nil {
			return nil, nil, err
		}
		plan.Series = &series
		warnings = appendNonEmpty(warnings, warning)
	}
	if plan.Books, err = BookTerms(books, lists[bookTaxonomy]); err != nil {
		return nil, nil, err
	}
	if !plan.Podcast {
		warnings = append(warnings, "The talk will be left out of the podcast feed.")
	}
	return plan, warnings, nil
}

// existing finds a term that must already be on the site.
func existing(terms []Term, name, what string) (Term, error) {
	match := MatchTerm(terms, name)
	if match.Exact == nil {
		return Term{}, fmt.Errorf("the site has no %s called %q%s", what, name, suggest(match.Near))
	}
	return *match.Exact, nil
}

// choose finds a speaker or series, or accepts a new one when the operator has
// said it is new. A new name close to an existing one is allowed, since two
// people can have similar names, but it is reported.
func choose(terms []Term, name string, allowNew bool, what, flag string) (Term, string, error) {
	match := MatchTerm(terms, name)
	if match.Exact != nil {
		return *match.Exact, "", nil
	}
	if !allowNew {
		return Term{}, "", fmt.Errorf("the site has no %s called %q%s. If it is new, set %q to true in the talk's details",
			what, strings.TrimSpace(name), suggest(match.Near), flag)
	}
	created := Term{Name: strings.TrimSpace(name)}
	if len(match.Near) == 0 {
		return created, fmt.Sprintf("The %s %q is new and will be added to the site.", what, created.Name), nil
	}
	return created, fmt.Sprintf("The %s %q is new and will be added to the site, which already has %s. Check it is not one of those.",
		what, created.Name, quotedNames(match.Near)), nil
}

func suggest(near []Term) string {
	if len(near) == 0 {
		return ""
	}
	return "; did you mean " + quotedNames(near) + "?"
}

func quotedNames(terms []Term) string {
	names := make([]string, len(terms))
	for i, term := range terms {
		names[i] = fmt.Sprintf("%q", term.Name)
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

func appendNonEmpty(list []string, item string) []string {
	if item == "" {
		return list
	}
	return append(list, item)
}
