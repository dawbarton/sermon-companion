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

	"github.com/dawbarton/sermon-companion/internal/master"
)

// The taxonomies a talk is filed under, by their REST bases on the site.
const (
	churchTaxonomy  = "resource-church"
	typeTaxonomy    = "resource-type"
	speakerTaxonomy = "resource-contributor"
	seriesTaxonomy  = "resource-series"
	bookTaxonomy    = "bible_book"
)

// Options are the choices made for this upload rather than for this talk.
type Options struct {
	Status string // "publish", "private", or "draft"
	Type   string // the resource-type term, normally "Sermon"
}

// Plan is an upload with every term found and every value worked out, so that
// carrying it out is a fixed sequence of requests.
type Plan struct {
	Title   string
	Passage string
	// Date is the day of the service, YYYY-MM-DD, and LocalDate the same as
	// the site's Date field takes it. PostDate is when the talk is dated on
	// the site, which is what its page shows.
	Date      string
	LocalDate string
	PostDate  time.Time
	Church    Term
	Type      Term
	Speakers  []Term // an ID of zero means the speaker is to be created
	Series    *Term  // nil when there is none; ID zero means it is to be created
	Books     []Term
	Image     Image // ID zero when the talk has no image
	VideoURL  string
	File      string
	FileName  string
	Size      int64
	Duration  time.Duration
	Status    string
	Podcast   bool
	// Duplicates are talks already filed for the same church and service.
	Duplicates []ExistingTalk
}

var statuses = map[string]bool{"publish": true, "private": true, "draft": true}

// FetchLists reads the site's current lists.
func FetchLists(ctx context.Context, c *Client) (Lists, error) {
	var lists Lists
	for _, list := range []struct {
		taxonomy string
		into     *[]Term
	}{
		{churchTaxonomy, &lists.Churches},
		{typeTaxonomy, &lists.Types},
		{speakerTaxonomy, &lists.Speakers},
		{seriesTaxonomy, &lists.Series},
		{bookTaxonomy, &lists.Books},
	} {
		terms, err := c.Terms(ctx, list.taxonomy)
		if err != nil {
			return Lists{}, err
		}
		*list.into = terms
	}
	return lists, nil
}

// Prepare looks the talk up on the site and returns the plan, with any
// warnings the operator should read before it is carried out. It only reads.
// An empty date or church in the talk is taken from the session.
func Prepare(ctx context.Context, c *Client, export *Export, talk Talk, opts Options, duration time.Duration) (*Plan, []string, error) {
	if !statuses[opts.Status] {
		return nil, nil, fmt.Errorf("the status must be publish, private, or draft, not %q", opts.Status)
	}
	started := export.Session.StartedAt
	if strings.TrimSpace(talk.Date) == "" {
		talk.Date = started.In(time.Local).Format("2006-01-02")
	}
	day, err := time.ParseInLocation("2006-01-02", talk.Date, time.Local)
	if err != nil {
		return nil, nil, fmt.Errorf("the talk's date %q is not a day such as 2026-11-01", talk.Date)
	}
	if strings.TrimSpace(talk.Church) == "" {
		talk.Church = export.Session.Church
	}

	lists, err := FetchLists(ctx, c)
	if err != nil {
		return nil, nil, err
	}
	review := ReviewTalk(lists, talk, opts.Type)
	if len(review.Problems) > 0 {
		return nil, nil, errors.New(strings.Join(review.Problems, " "))
	}
	warnings := review.Warnings

	plan := &Plan{
		Title:     strings.TrimSpace(talk.Title),
		Passage:   strings.TrimSpace(talk.Passage),
		Date:      talk.Date,
		LocalDate: day.Format("20060102"),
		PostDate:  postDate(day, started),
		Church:    review.church,
		Type:      review.kind,
		Books:     review.books,
		VideoURL:  strings.TrimSpace(talk.VideoURL),
		File:      export.Path,
		// The file is named on the site from the talk's own date and church,
		// which may have been corrected after the MP3 was made.
		FileName: master.MP3Name(talk.Date, review.church.Name),
		Size:     export.Size,
		Duration: duration,
		Status:   opts.Status,
		Podcast:  talk.Podcast,
	}
	for _, speaker := range review.Speakers {
		plan.Speakers = append(plan.Speakers, speaker.term)
	}
	if review.Series != nil {
		series := review.Series.term
		plan.Series = &series
	}

	if strings.TrimSpace(talk.Image) != "" {
		if plan.Image, err = c.FindImage(ctx, talk.Image); err != nil {
			return nil, nil, err
		}
	} else if plan.Series != nil && plan.Series.ID != 0 {
		id, err := c.SeriesImage(ctx, plan.Series.ID, plan.Type.ID)
		if err != nil {
			return nil, nil, err
		}
		if id != 0 {
			if plan.Image, err = c.MediaImage(ctx, id); err != nil {
				return nil, nil, err
			}
		}
	}
	if plan.Image.ID == 0 {
		warnings = append(warnings, "The talk will have no image.")
	}

	if plan.Duplicates, err = c.TalksOn(ctx, plan.Church.ID, plan.Type.ID, plan.Date); err != nil {
		return nil, nil, err
	}
	for _, duplicate := range plan.Duplicates {
		warnings = append(warnings, fmt.Sprintf("The website already has a talk for %s on this day: %q, %s.", plan.Church.Name, duplicate.Title, duplicate.Link))
	}
	return plan, warnings, nil
}

// postDate is when the talk is dated on the site. It is the start of the
// recording when the talk is for that day; a corrected date keeps the
// recording's time of day.
func postDate(day, started time.Time) time.Time {
	local := started.In(time.Local)
	return time.Date(day.Year(), day.Month(), day.Day(), local.Hour(), local.Minute(), local.Second(), 0, time.Local)
}

// existing finds a term that must already be on the site.
func existing(terms []Term, name, what string) (Term, error) {
	match := MatchTerm(terms, name)
	if match.Exact == nil {
		names := make([]string, len(match.Near))
		for i, term := range match.Near {
			names[i] = term.Name
		}
		if len(names) > 0 {
			return Term{}, fmt.Errorf("the website has no %s called %q; did you mean %s?", what, strings.TrimSpace(name), quotedList(names))
		}
		return Term{}, fmt.Errorf("the website has no %s called %q", what, strings.TrimSpace(name))
	}
	return *match.Exact, nil
}
