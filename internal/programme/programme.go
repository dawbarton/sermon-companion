// Package programme reads the preaching programme: a spreadsheet, kept by the
// church's administrators, of the talks planned for each Sunday. The upload
// page fills a talk's details in from the programme's row for the day of the
// service, and the operator corrects anything that changed late.
//
// The spreadsheet is an .xlsx workbook, or a .csv file, with a heading row.
// Columns are found by their headings, so they can be in any order and other
// columns are ignored. Only Date is required:
//
//	Date | Service | Church | Title | Speakers | Series | Passage | Series image | Podcast
//
// Service is the start time, such as 10:30, used only to choose between two
// services on the same day. Church restricts a row to one congregation when
// several share the spreadsheet. Several speakers are separated by commas.
// Series image is a link to an image already on the website, needed only for
// the first talk of a new series. Podcast is Yes or No, to keep one talk out
// of the podcast feed; left empty, the configured default applies.
package programme

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Row is one planned talk.
type Row struct {
	// Line is the spreadsheet's own row number, for telling the operator
	// where a value came from.
	Line int
	// Date is the day of the service, YYYY-MM-DD.
	Date string
	// Service is the start time in minutes after midnight, or -1 if none was
	// given or it could not be read; ServiceText is the cell as written.
	Service     int
	ServiceText string
	Church      string
	Title       string
	Speakers    []string
	Series      string
	Passage     string
	SeriesImage string
	// Podcast is whether the talk goes into the podcast feed, or nil when the
	// programme does not say.
	Podcast *bool
}

// Programme is what was read from the spreadsheet, including the rows that
// could not be, so that a mistake in it is reported rather than silently
// leaving a Sunday blank.
type Programme struct {
	Rows     []Row
	Problems []string
}

// Read reads the programme from an .xlsx or .csv file. sheetName chooses a
// worksheet in a workbook; the first is used if it is empty.
func Read(filename, sheetName string) (*Programme, error) {
	var (
		grid     [][]cell
		date1904 bool
	)
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".xlsx":
		s, err := readXLSX(filename, sheetName)
		if err != nil {
			return nil, err
		}
		grid, date1904 = s.Rows, s.Date1904
	case ".csv":
		rows, err := readCSV(filename)
		if err != nil {
			return nil, err
		}
		grid = rows
	default:
		return nil, errors.New("the programme must be an .xlsx or .csv file")
	}
	return fromGrid(grid, date1904)
}

// The headings each column may be given, compared without case, spaces, or
// punctuation.
var headings = map[string][]string{
	"date":     {"date", "servicedate", "sunday"},
	"service":  {"service", "time", "servicetime", "starttime"},
	"church":   {"church", "congregation"},
	"title":    {"title", "talktitle", "sermontitle", "talk", "sermon"},
	"speakers": {"speakers", "speaker", "preacher", "preachers", "contributors", "contributor"},
	"series":   {"series", "sermonseries"},
	"passage":  {"passage", "passages", "biblepassage", "biblepassages", "reading", "bible"},
	"image":    {"seriesimage", "image", "featuredimage", "picture"},
	"podcast":  {"podcast", "inpodcast", "podcastfeed", "includeinpodcast", "includeinpodcastfeed"},
}

// headingRowsSearched is how far down the heading row is looked for, so that
// a title or a note above the table does not stop the programme being read.
const headingRowsSearched = 20

func fromGrid(grid [][]cell, date1904 bool) (*Programme, error) {
	headingRow, columns := -1, map[string]int{}
	for r := 0; r < len(grid) && r < headingRowsSearched && headingRow < 0; r++ {
		found := map[string]int{}
		for c, value := range grid[r] {
			key := headingKey(value.Text)
			for field, names := range headings {
				for _, name := range names {
					if key == name {
						if _, taken := found[field]; !taken {
							found[field] = c
						}
					}
				}
			}
		}
		if _, ok := found["date"]; ok {
			headingRow, columns = r, found
		}
	}
	if headingRow < 0 {
		return nil, errors.New("the programme has no heading row with a Date column")
	}
	get := func(row []cell, field string) cell {
		c, ok := columns[field]
		if !ok || c >= len(row) {
			return cell{}
		}
		return row[c]
	}
	text := func(row []cell, field string) string {
		return strings.Join(strings.Fields(get(row, field).Text), " ")
	}
	p := &Programme{}
	for r := headingRow + 1; r < len(grid); r++ {
		row := grid[r]
		dateCell := get(row, "date")
		if strings.TrimSpace(dateCell.Text) == "" && dateCell.Number == nil {
			continue
		}
		line := r + 1
		day, err := readDate(dateCell, date1904)
		if err != nil {
			p.Problems = append(p.Problems, fmt.Sprintf("Row %d: %v.", line, err))
			continue
		}
		service := get(row, "service")
		minutes := readTime(service)
		podcast, ok := readYesNo(get(row, "podcast").Text)
		if !ok {
			// The row is still used, with the configured podcast choice, so
			// that one unclear cell does not lose the rest of the talk.
			p.Problems = append(p.Problems, fmt.Sprintf("Row %d: could not read %q as Yes or No for the podcast.", line, strings.TrimSpace(get(row, "podcast").Text)))
		}
		p.Rows = append(p.Rows, Row{
			Line:        line,
			Date:        day,
			Service:     minutes,
			ServiceText: strings.TrimSpace(service.Text),
			Church:      text(row, "church"),
			Title:       text(row, "title"),
			Speakers:    splitNames(get(row, "speakers").Text),
			Series:      text(row, "series"),
			Passage:     text(row, "passage"),
			SeriesImage: strings.TrimSpace(get(row, "image").Text),
			Podcast:     podcast,
		})
	}
	return p, nil
}

// readYesNo reads a yes-or-no cell. An empty cell says nothing, and is not
// a problem; anything it cannot read is.
func readYesNo(text string) (*bool, bool) {
	yes, no := true, false
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "":
		return nil, true
	case "yes", "y", "true", "1", "include", "included":
		return &yes, true
	case "no", "n", "false", "0", "exclude", "excluded":
		return &no, true
	}
	return nil, false
}

func headingKey(text string) string {
	var key strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			key.WriteRune(r)
		}
	}
	return key.String()
}

// splitNames separates several speakers written in one cell.
func splitNames(text string) []string {
	var names []string
	for _, name := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '&' || r == '\n' || r == '/' }) {
		if name = strings.Join(strings.Fields(name), " "); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// Excel counts days from the end of 1899, or from 1904 in workbooks set to
// do so. Its 1900 calendar has a 29 February that never existed, so serial
// numbers from 61 onwards are counted from 30 December 1899.
var (
	excelEpoch1900 = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	excelEpoch1904 = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
)

var ordinalSuffix = regexp.MustCompile(`(?i)\b(\d{1,2})(st|nd|rd|th)\b`)

// dateLayouts are the ways a date might be typed as text. Dates written with
// numbers alone are read day first, as they are in Britain.
var dateLayouts = []string{
	"2006-01-02", "2006-01-02T15:04:05", "2006-01-02T15:04:05Z07:00",
	"2/1/2006", "2/1/06", "2.1.2006", "2-1-2006",
	"2 January 2006", "2 Jan 2006", "2 January 06", "2 Jan 06",
	"Monday 2 January 2006", "Mon 2 January 2006", "Monday 2 Jan 2006", "Mon 2 Jan 2006",
	"Monday, 2 January 2006", "Mon, 2 Jan 2006",
	"2-Jan-2006", "2-Jan-06", "January 2, 2006", "Jan 2, 2006",
}

func readDate(c cell, date1904 bool) (string, error) {
	if c.Number != nil {
		serial := math.Floor(*c.Number)
		if serial < 1 || serial > 2_958_465 {
			return "", fmt.Errorf("%q is not a date", c.Text)
		}
		epoch := excelEpoch1900
		if date1904 {
			epoch = excelEpoch1904
		} else if serial < 61 {
			epoch = epoch.AddDate(0, 0, 1)
		}
		return epoch.AddDate(0, 0, int(serial)).Format("2006-01-02"), nil
	}
	text := strings.Join(strings.Fields(c.Text), " ")
	text = ordinalSuffix.ReplaceAllString(text, "$1")
	for _, layout := range dateLayouts {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("could not read %q as a date", strings.TrimSpace(c.Text))
}

var clockTime = regexp.MustCompile(`(?i)^(\d{1,2})(?:[:.](\d{2}))?\s*(am|pm|a\.m\.|p\.m\.)?$`)

// readTime reads a service's start time as minutes after midnight, or -1.
// Excel stores a time typed into a cell as a fraction of a day.
func readTime(c cell) int {
	if c.Number != nil {
		fraction := *c.Number - math.Floor(*c.Number)
		if *c.Number < 0 || fraction == 0 && *c.Number != 0 {
			return -1
		}
		return int(math.Round(fraction*24*60)) % (24 * 60)
	}
	match := clockTime.FindStringSubmatch(strings.TrimSpace(c.Text))
	if match == nil {
		return -1
	}
	hour, _ := strconv.Atoi(match[1])
	minute := 0
	if match[2] != "" {
		minute, _ = strconv.Atoi(match[2])
	}
	if minute > 59 {
		return -1
	}
	switch strings.ToLower(strings.ReplaceAll(match[3], ".", "")) {
	case "":
		// A bare number such as "10" is too ambiguous to be a time.
		if match[2] == "" || hour > 23 {
			return -1
		}
	case "am":
		if hour < 1 || hour > 12 {
			return -1
		}
		hour %= 12
	case "pm":
		if hour < 1 || hour > 12 {
			return -1
		}
		hour = hour%12 + 12
	}
	return hour*60 + minute
}

// Match is the programme's row for a service, if it has one, and a sentence
// for the operator saying how it was chosen.
type Match struct {
	Row  *Row
	Note string
}

// Find chooses the programme's row for a service of church starting at
// started. Rows for another church are ignored, and rows for this church are
// preferred to rows for none. When a day has more than one
// row, the one whose service time is nearest the start of the recording is
// chosen.
func (p *Programme) Find(church string, started time.Time) Match {
	local := started.In(time.Local)
	day := local.Format("2006-01-02")
	// Rows naming this church take precedence over rows naming none, which
	// are for every church sharing the spreadsheet.
	var named, shared []*Row
	for i := range p.Rows {
		row := &p.Rows[i]
		switch {
		case row.Date != day:
		case row.Church == "":
			shared = append(shared, row)
		case strings.EqualFold(row.Church, strings.TrimSpace(church)):
			named = append(named, row)
		}
	}
	candidates := named
	if len(candidates) == 0 {
		candidates = shared
	}
	when := local.Format("Monday 2 January")
	switch len(candidates) {
	case 0:
		return Match{Note: fmt.Sprintf("The programme has nothing for %s.", when)}
	case 1:
		return Match{Row: candidates[0], Note: fmt.Sprintf("Filled in from row %d of the programme.", candidates[0].Line)}
	}
	minutes := local.Hour()*60 + local.Minute()
	best := -1
	for i, row := range candidates {
		if row.Service < 0 {
			continue
		}
		if best < 0 || distance(row.Service, minutes) < distance(candidates[best].Service, minutes) {
			best = i
		}
	}
	if best < 0 {
		return Match{Row: candidates[0], Note: fmt.Sprintf("The programme has %d rows for %s and no service times to choose between them, so row %d was used. Check it is the right one.",
			len(candidates), when, candidates[0].Line)}
	}
	chosen := candidates[best]
	return Match{Row: chosen, Note: fmt.Sprintf("Filled in from row %d of the programme, the %s service; it has %d services that day.",
		chosen.Line, formatMinutes(chosen.Service), len(candidates))}
}

func distance(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

func formatMinutes(minutes int) string {
	return fmt.Sprintf("%d:%02d", minutes/60, minutes%60)
}
