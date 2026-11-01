// Term lists for the taxonomies a talk is filed under: speaker, series, book of
// the Bible, church, and type. The operator picks from the site's real terms
// rather than typing, because a near-duplicate ("James Maurice" beside "James
// Morris") splits a speaker's talks across two names and is only ever noticed
// by someone browsing the site. This file fetches the complete lists, matches a
// name against them, finds near misses worth a warning, and creates a term when
// the operator has decided a new one is wanted.
package publish

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Term is one term of a taxonomy, as far as filing a talk needs.
type Term struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Count int    `json:"count"`
}

// maxTermPages stops a site that misreports its page count from keeping the
// client looping. At 100 terms a page it allows 10,000 terms.
const maxTermPages = 100

// Terms fetches every term of the taxonomy whose REST base is restBase,
// following WordPress's paging. Only the fields used here are asked for: the
// site's SEO plugin otherwise adds several kilobytes to every term.
func (c *Client) Terms(ctx context.Context, restBase string) ([]Term, error) {
	op := "reading the " + restBase + " list"
	var all []Term
	for page := 1; page <= maxTermPages; page++ {
		query := url.Values{
			"per_page":   {"100"},
			"page":       {strconv.Itoa(page)},
			"orderby":    {"name"},
			"hide_empty": {"false"},
			"_fields":    {"id,name,slug,count"},
		}
		var batch []Term
		header, err := c.Get(ctx, op, "/wp/v2/"+restBase, query, &batch)
		if err != nil {
			return nil, err
		}
		for i := range batch {
			batch[i].Name = html.UnescapeString(batch[i].Name)
		}
		all = append(all, batch...)
		if len(batch) == 0 || page >= totalPages(header) {
			return all, nil
		}
	}
	return nil, &Error{Op: op, Kind: KindSite, Message: fmt.Sprintf("the site reported more than %d pages of terms", maxTermPages)}
}

// totalPages reads WordPress's page count, treating a missing or unreadable
// header as a single page.
func totalPages(header http.Header) int {
	pages, err := strconv.Atoi(header.Get("X-WP-TotalPages"))
	if err != nil || pages < 1 {
		return 1
	}
	return pages
}

// CreateTerm adds a term to the taxonomy whose REST base is restBase. It is
// called only once the operator has chosen to create one, after MatchTerm has
// found no exact match.
func (c *Client) CreateTerm(ctx context.Context, restBase, name string) (Term, error) {
	var created Term
	err := c.SendJSON(ctx, fmt.Sprintf("adding %q to the %s list", name, restBase), http.MethodPost,
		"/wp/v2/"+restBase, nil, map[string]string{"name": strings.TrimSpace(name)}, &created)
	created.Name = html.UnescapeString(created.Name)
	return created, err
}

// Match is the outcome of looking a name up in a term list.
type Match struct {
	// Exact is the term with the same name, ignoring case, spacing, and the
	// difference between curly and straight quotes; nil when there is none.
	Exact *Term
	// Near are up to three other terms close enough to be a likely misspelling,
	// closest first. They are offered as a warning, never chosen automatically.
	Near []Term
}

// MatchTerm looks name up in terms.
func MatchTerm(terms []Term, name string) Match {
	key := nameKey(name)
	var match Match
	if key == "" {
		return match
	}
	type candidate struct {
		term     Term
		distance int
	}
	var near []candidate
	for _, term := range terms {
		other := nameKey(term.Name)
		if other == key {
			found := term
			match.Exact = &found
			continue
		}
		if distance, ok := nearness(key, other); ok {
			near = append(near, candidate{term, distance})
		}
	}
	sort.SliceStable(near, func(i, j int) bool { return near[i].distance < near[j].distance })
	for i := 0; i < len(near) && i < 3; i++ {
		match.Near = append(match.Near, near[i].term)
	}
	return match
}

// nameKey reduces a name to what decides whether two names are the same: lower
// case, single spaces, straight quotes, and no HTML entities.
func nameKey(name string) string {
	name = html.UnescapeString(name)
	name = strings.NewReplacer("’", "'", "‘", "'", "“", `"`, "”", `"`).Replace(name)
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

// nearness reports whether two different keys are close enough to warn about,
// and how close. One containing the other ("mike cain" and "mike cain (guest)")
// counts, as does an edit distance of at most a third of the longer name. The
// third is set by the site's own pair "james maurice" and "james morris"
// (distance 4 over 13 characters), which must be caught, against "jim walford"
// and "claire walford" (distance 6), which are different people.
func nearness(a, b string) (int, bool) {
	if a == "" || b == "" {
		return 0, false
	}
	distance := editDistance(a, b)
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return distance, true
	}
	longer := max(len([]rune(a)), len([]rune(b)))
	return distance, distance*3 <= longer
}

// editDistance is the Levenshtein distance between two strings, counted in
// characters rather than bytes so that an accented letter is one edit.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	previous := make([]int, len(rb)+1)
	current := make([]int, len(rb)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] || (unicode.IsSpace(ra[i-1]) && unicode.IsSpace(rb[j-1])) {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(rb)]
}
