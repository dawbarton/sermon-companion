// What the upload page needs to know about the site before anything is
// uploaded: the lists of churches, speakers, and series to choose from, the
// talks recently filed, the image a series already uses, the image a link
// names, and whether a talk for the same service is already there. All of it
// is read; nothing here changes the site.
package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dawbarton/sermon-companion/internal/atomicfile"
)

// recentTalks is how many of the latest talks are read to put each church's
// recent speakers and series first, and to find a series' image. At one talk
// a week across three churches it covers about eight months.
const recentTalks = 100

// Catalogue is a copy of the site's lists, kept so that the upload page works
// from the last copy when the site cannot be reached.
type Catalogue struct {
	Site    string    `json:"site"`
	Fetched time.Time `json:"fetched"`
	// The lists are embedded, so that the file holds them at the top level.
	Lists
	Recent []RecentTalk `json:"recent"`
}

// RecentTalk is a talk already on the site, as far as the lists need it.
type RecentTalk struct {
	ID       int    `json:"id"`
	Date     string `json:"date"` // the post date, YYYY-MM-DD
	Title    string `json:"title"`
	Link     string `json:"link"`
	Churches []int  `json:"churches"`
	Speakers []int  `json:"speakers"`
	Series   []int  `json:"series"`
	Image    int    `json:"image"`
}

// FetchCatalogue reads the site's lists and its latest talks of the given
// type, normally "Sermon".
func FetchCatalogue(ctx context.Context, c *Client, typeName string) (*Catalogue, error) {
	lists, err := FetchLists(ctx, c)
	if err != nil {
		return nil, err
	}
	catalogue := &Catalogue{Site: c.base.String(), Fetched: time.Now().UTC(), Lists: lists}
	kind, err := existing(catalogue.Types, typeName, "resource type")
	if err != nil {
		return nil, err
	}
	query := url.Values{
		"per_page":   {strconv.Itoa(recentTalks)},
		"orderby":    {"date"},
		typeTaxonomy: {strconv.Itoa(kind.ID)},
		"_fields":    {"id,date,link,title,featured_media," + churchTaxonomy + "," + speakerTaxonomy + "," + seriesTaxonomy},
	}
	var talks []struct {
		ID    int    `json:"id"`
		Date  string `json:"date"`
		Link  string `json:"link"`
		Title struct {
			Rendered string `json:"rendered"`
		} `json:"title"`
		Image    int   `json:"featured_media"`
		Churches []int `json:"resource-church"`
		Speakers []int `json:"resource-contributor"`
		Series   []int `json:"resource-series"`
	}
	if _, err := c.Get(ctx, "reading the latest talks", "/wp/v2/resource", query, &talks); err != nil {
		return nil, err
	}
	for _, talk := range talks {
		catalogue.Recent = append(catalogue.Recent, RecentTalk{
			ID: talk.ID, Date: datePart(talk.Date), Title: html.UnescapeString(talk.Title.Rendered), Link: talk.Link,
			Churches: talk.Churches, Speakers: talk.Speakers, Series: talk.Series, Image: talk.Image,
		})
	}
	return catalogue, nil
}

func datePart(stamp string) string {
	if len(stamp) >= 10 {
		return stamp[:10]
	}
	return stamp
}

// LoadCatalogue reads the copy kept on disk. A missing copy is not an error.
func LoadCatalogue(filename string) (*Catalogue, error) {
	data, err := os.ReadFile(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var catalogue Catalogue
	if err := json.Unmarshal(data, &catalogue); err != nil {
		return nil, fmt.Errorf("the copy of the website's lists in %s cannot be read: %w", filename, err)
	}
	return &catalogue, nil
}

// Save keeps the copy on disk, replacing the previous one in a single step.
func (c *Catalogue) Save(filename string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(filename, append(data, '\n'), 0o644)
}

// Church finds a church by name.
func (c *Catalogue) Church(name string) *Term {
	if match := MatchTerm(c.Churches, name); match.Exact != nil {
		return match.Exact
	}
	return nil
}

// names returns the names of terms, those with the given IDs first in the
// order given, then the rest alphabetically.
func names(terms []Term, first []int) []string {
	byID := make(map[int]Term, len(terms))
	for _, term := range terms {
		byID[term.ID] = term
	}
	out := make([]string, 0, len(terms))
	used := map[int]bool{}
	for _, id := range first {
		if term, ok := byID[id]; ok && !used[id] {
			used[id] = true
			out = append(out, term.Name)
		}
	}
	rest := make([]string, 0, len(terms))
	for _, term := range terms {
		if !used[term.ID] {
			rest = append(rest, term.Name)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return strings.ToLower(rest[i]) < strings.ToLower(rest[j]) })
	return append(out, rest...)
}

// churchTalks returns the recent talks filed under the church, newest first.
func (c *Catalogue) churchTalks(church string) []RecentTalk {
	term := c.Church(church)
	if term == nil {
		return nil
	}
	var talks []RecentTalk
	for _, talk := range c.Recent {
		for _, id := range talk.Churches {
			if id == term.ID {
				talks = append(talks, talk)
				break
			}
		}
	}
	return talks
}

// SpeakerNames lists every speaker, those the church has heard most recently
// first, so that the usual preachers are at the top of the list.
func (c *Catalogue) SpeakerNames(church string) []string {
	var recent []int
	for _, talk := range c.churchTalks(church) {
		recent = append(recent, talk.Speakers...)
	}
	return names(c.Speakers, recent)
}

// SeriesNames lists every series, the church's most recent first.
func (c *Catalogue) SeriesNames(church string) []string {
	var recent []int
	for _, talk := range c.churchTalks(church) {
		recent = append(recent, talk.Series...)
	}
	return names(c.Series, recent)
}

// LatestSeries is the series of the church's latest talk, the usual answer
// for this week's when the programme does not say.
func (c *Catalogue) LatestSeries(church string) string {
	for _, talk := range c.churchTalks(church) {
		if len(talk.Series) == 0 {
			continue
		}
		for _, term := range c.Series {
			if term.ID == talk.Series[0] {
				return term.Name
			}
		}
	}
	return ""
}

// SeriesImage is the image of the latest recent talk in the series, or zero.
func (c *Catalogue) SeriesImage(seriesID int) int {
	for _, talk := range c.Recent {
		for _, id := range talk.Series {
			if id == seriesID && talk.Image != 0 {
				return talk.Image
			}
		}
	}
	return 0
}

// Image is an image in the site's media library.
type Image struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Link   string `json:"link"`
	Source string `json:"source"`
}

type mediaItem struct {
	ID    int `json:"id"`
	Title struct {
		Rendered string `json:"rendered"`
	} `json:"title"`
	Link      string `json:"link"`
	Source    string `json:"source_url"`
	MediaType string `json:"media_type"`
}

func (m mediaItem) image() Image {
	return Image{ID: m.ID, Title: html.UnescapeString(m.Title.Rendered), Link: m.Link, Source: m.Source}
}

const mediaFields = "id,title,link,source_url,media_type"

// SeriesImage finds the image used by the latest talk in a series that has
// one, asking the site when the catalogue's recent talks do not include one.
func (c *Client) SeriesImage(ctx context.Context, seriesID, typeID int) (int, error) {
	query := url.Values{
		"per_page":     {"10"},
		"orderby":      {"date"},
		seriesTaxonomy: {strconv.Itoa(seriesID)},
		"_fields":      {"id,featured_media"},
		typeTaxonomy:   {strconv.Itoa(typeID)},
	}
	var talks []struct {
		Image int `json:"featured_media"`
	}
	if _, err := c.Get(ctx, "finding the series' image", "/wp/v2/resource", query, &talks); err != nil {
		return 0, err
	}
	for _, talk := range talks {
		if talk.Image != 0 {
			return talk.Image, nil
		}
	}
	return 0, nil
}

// MediaImage reads an image by its ID.
func (c *Client) MediaImage(ctx context.Context, id int) (Image, error) {
	var item mediaItem
	if _, err := c.Get(ctx, "reading the image", "/wp/v2/media/"+strconv.Itoa(id), url.Values{"_fields": {mediaFields}}, &item); err != nil {
		return Image{}, err
	}
	if item.MediaType != "image" {
		return Image{}, fmt.Errorf("item %d in the website's media library is not an image", id)
	}
	return item.image(), nil
}

// sizeSuffix is the part WordPress adds to the name of a resized copy of an
// image, such as -300x200, or -scaled for a large original it reduced.
var sizeSuffix = regexp.MustCompile(`(-\d+x\d+|-scaled|-rotated)+$`)

// FindImage finds the image a reference names: its ID, the link to its page
// on the site (what the media library's "View attachment page" opens), or the
// address of the file itself (what its "Copy URL" button copies).
func (c *Client) FindImage(ctx context.Context, reference string) (Image, error) {
	reference = strings.TrimSpace(reference)
	if id, err := strconv.Atoi(reference); err == nil && id > 0 {
		return c.MediaImage(ctx, id)
	}
	address, err := url.Parse(reference)
	if err != nil || address.Host == "" {
		return Image{}, fmt.Errorf("%q is not a link to an image", reference)
	}
	base := path.Base(strings.TrimSuffix(address.Path, "/"))
	if base == "." || base == "/" || base == "" {
		return Image{}, fmt.Errorf("%q is not a link to an image", reference)
	}
	extension := path.Ext(base)
	if extension == "" {
		// An attachment page is named by the image's slug.
		var items []mediaItem
		query := url.Values{"slug": {base}, "_fields": {mediaFields}}
		if _, err := c.Get(ctx, "finding the image", "/wp/v2/media", query, &items); err != nil {
			return Image{}, err
		}
		for _, item := range items {
			if item.MediaType == "image" {
				return item.image(), nil
			}
		}
		return Image{}, fmt.Errorf("the website has no image at %s", reference)
	}
	// A file is found by searching for its name and comparing addresses,
	// allowing for a resized copy of the image having been linked.
	stem := sizeSuffix.ReplaceAllString(strings.TrimSuffix(base, extension), "")
	var items []mediaItem
	query := url.Values{"search": {stem}, "media_type": {"image"}, "per_page": {"20"}, "_fields": {mediaFields}}
	if _, err := c.Get(ctx, "finding the image", "/wp/v2/media", query, &items); err != nil {
		return Image{}, err
	}
	want := strings.ToLower(stem + extension)
	for _, item := range items {
		source, err := url.Parse(item.Source)
		if err != nil {
			continue
		}
		name := path.Base(source.Path)
		candidate := strings.ToLower(sizeSuffix.ReplaceAllString(strings.TrimSuffix(name, path.Ext(name)), "") + path.Ext(name))
		if candidate == want {
			return item.image(), nil
		}
	}
	return Image{}, fmt.Errorf("the website's media library has no image called %s", base)
}

// ExistingTalk is a talk already on the site for the same church and day.
type ExistingTalk struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Link  string `json:"link"`
}

// TalksOn finds talks of the type filed under the church for the service on
// day, YYYY-MM-DD, by the site's Date field rather than the day they were
// posted, which can be a week later. Reading the Date field needs the
// account to be signed in.
func (c *Client) TalksOn(ctx context.Context, churchID, typeID int, day string) ([]ExistingTalk, error) {
	query := url.Values{
		"per_page":     {"30"},
		"orderby":      {"date"},
		churchTaxonomy: {strconv.Itoa(churchID)},
		typeTaxonomy:   {strconv.Itoa(typeID)},
		"_fields":      {"id,date,link,title,acf." + fieldDate},
	}
	var talks []struct {
		ID    int    `json:"id"`
		Date  string `json:"date"`
		Link  string `json:"link"`
		Title struct {
			Rendered string `json:"rendered"`
		} `json:"title"`
		// ACF sends an empty array rather than an object when no field is set.
		ACF json.RawMessage `json:"acf"`
	}
	if _, err := c.Get(ctx, "looking for a talk already on the website", "/wp/v2/resource", query, &talks); err != nil {
		return nil, err
	}
	compact := strings.ReplaceAll(day, "-", "")
	var found []ExistingTalk
	for _, talk := range talks {
		var fields struct {
			Date string `json:"resource_date"`
		}
		_ = json.Unmarshal(talk.ACF, &fields)
		date := fields.Date
		if date == compact || date == "" && datePart(talk.Date) == day {
			found = append(found, ExistingTalk{ID: talk.ID, Title: html.UnescapeString(talk.Title.Rendered), Link: talk.Link})
		}
	}
	return found, nil
}
