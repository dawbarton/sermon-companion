package programme

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// workbook builds a minimal .xlsx file in the shape Excel writes, so that no
// binary file needs to be committed for the tests.
type workbook struct {
	sheets   []testSheet
	shared   []string // shared-string items, as raw XML inside <si>
	date1904 bool
	// absoluteTargets writes relationship targets from the archive root, as
	// some programs other than Excel do.
	absoluteTargets bool
}

type testSheet struct {
	name string
	rows string // the XML inside <sheetData>
}

func (w workbook) write(t *testing.T) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "Programme.xlsx")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	add := func(name, content string) {
		part, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	const relNS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	add("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="`+relNS+`/officeDocument" Target="xl/workbook.xml"/></Relationships>`)
	properties := ""
	if w.date1904 {
		properties = `<workbookPr date1904="1"/>`
	}
	var sheets, rels strings.Builder
	prefix := ""
	if w.absoluteTargets {
		prefix = "/xl/"
	}
	for i, s := range w.sheets {
		fmt.Fprintf(&sheets, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, s.name, i+1, i+1)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="%s/worksheet" Target="%sworksheets/sheet%d.xml"/>`, i+1, relNS, prefix, i+1)
		add(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="`+relNS+`"><dimension ref="A1:H9"/><sheetData>`+s.rows+`</sheetData></worksheet>`)
	}
	fmt.Fprintf(&rels, `<Relationship Id="rId99" Type="%s/sharedStrings" Target="%ssharedStrings.xml"/>`, relNS, prefix)
	add("xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="`+relNS+`">`+properties+`<sheets>`+sheets.String()+`</sheets></workbook>`)
	add("xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+rels.String()+`</Relationships>`)
	var shared strings.Builder
	for _, item := range w.shared {
		shared.WriteString("<si>" + item + "</si>")
	}
	add("xl/sharedStrings.xml", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">%s</sst>`, len(w.shared), len(w.shared), shared.String()))
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return filename
}

func s(column string, index int) string {
	return fmt.Sprintf(`<c r="%s" t="s"><v>%d</v></c>`, column, index)
}

func n(column, value string) string {
	return fmt.Sprintf(`<c r="%s" s="1"><v>%s</v></c>`, column, value)
}

func inline(column, text string) string {
	return fmt.Sprintf(`<c r="%s" t="inlineStr"><is><t>%s</t></is></c>`, column, text)
}

func TestReadsAnExcelProgramme(t *testing.T) {
	shared := []string{
		"<t>Date</t>", "<t>Service</t>", "<t>Talk title</t>", "<t>Preacher</t>", "<t>Series</t>",
		"<t>Bible passage</t>", "<t>Series image</t>", "<t>Notes</t>",
		// A title typed with part of it in italics is stored as runs, and a
		// phonetic guide is not part of the text.
		`<r><t>What's wrong </t></r><r><rPr><i/></rPr><t>with us?</t></r><rPh sb="0" eb="1"><t>ignored</t></rPh>`,
		"<t>Zoë Smith, Ann Other</t>", "<t>Life Together</t>", "<t>Romans 3:9-20</t>",
		"<t>https://www.emmanuelbristol.org.uk/in-the-beginning-11/</t>",
	}
	rows := `<row r="1">` + inline("A1", "Autumn programme") + `</row>` +
		// The heading row is the third, below a title and a blank row.
		`<row r="3">` + s("A3", 0) + s("B3", 1) + s("C3", 2) + s("D3", 3) + s("E3", 4) + s("F3", 5) + s("G3", 6) + s("H3", 7) + `</row>` +
		// 1 November 2026 is serial 46327; 10:30 is 0.4375 of a day.
		`<row r="4">` + n("A4", "46327") + n("B4", "0.4375") + s("C4", 8) + s("D4", 9) + s("E4", 10) + s("F4", 11) + s("G4", 12) + `</row>` +
		// A date typed as text, and an evening service the same day.
		`<row r="5">` + inline("A5", "1st November 2026") + inline("B5", "6.30pm") + inline("C5", "Evening talk") + `</row>` +
		`<row r="6">` + inline("A6", "TBC") + inline("C6", "Not yet dated") + `</row>` +
		`<row r="8">` + inline("A8", "08/11/2026") + inline("D8", "Jo Bloggs") + `</row>`
	filename := workbook{
		sheets: []testSheet{{name: "Summer", rows: `<row r="1"><c r="A1" t="inlineStr"><is><t>Date</t></is></c></row>`}, {name: "Autumn", rows: rows}},
		shared: shared,
	}.write(t)

	p, err := Read(filename, "autumn")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 3 {
		t.Fatalf("rows = %#v", p.Rows)
	}
	first := p.Rows[0]
	if first.Line != 4 || first.Date != "2026-11-01" || first.Service != 630 || first.Title != "What's wrong with us?" ||
		strings.Join(first.Speakers, "|") != "Zoë Smith|Ann Other" || first.Series != "Life Together" ||
		first.Passage != "Romans 3:9-20" || !strings.HasSuffix(first.SeriesImage, "in-the-beginning-11/") {
		t.Fatalf("first row = %#v", first)
	}
	if second := p.Rows[1]; second.Date != "2026-11-01" || second.Service != 18*60+30 {
		t.Fatalf("second row = %#v", second)
	}
	if third := p.Rows[2]; third.Date != "2026-11-08" || third.Service != -1 || third.Speakers[0] != "Jo Bloggs" {
		t.Fatalf("third row = %#v", third)
	}
	if len(p.Problems) != 1 || !strings.Contains(p.Problems[0], "Row 6") || !strings.Contains(p.Problems[0], "TBC") {
		t.Fatalf("problems = %q", p.Problems)
	}

	// The first worksheet is read when none is named, and an unknown name is
	// reported with the names that do exist.
	if p, err := Read(filename, ""); err != nil || len(p.Rows) != 0 {
		t.Fatalf("first sheet = %#v, %v", p, err)
	}
	if _, err := Read(filename, "Spring"); err == nil || !strings.Contains(err.Error(), `"Autumn"`) {
		t.Fatalf("unknown sheet gave %v", err)
	}
}

func TestChoosesTheRowForTheService(t *testing.T) {
	p := &Programme{Rows: []Row{
		{Line: 2, Date: "2026-11-01", Service: 630, Title: "Morning"},
		{Line: 3, Date: "2026-11-01", Service: 18*60 + 30, Title: "Evening"},
		{Line: 4, Date: "2026-11-01", Service: 630, Church: "Emmanuel City Centre", Title: "City Centre"},
		{Line: 5, Date: "2026-11-08", Service: -1, Title: "Next week"},
	}}
	at := func(hour, minute int) time.Time { return time.Date(2026, 11, 1, hour, minute, 0, 0, time.Local) }

	if m := p.Find("Emmanuel Bishopston", at(10, 25)); m.Row == nil || m.Row.Title != "Morning" || !strings.Contains(m.Note, "10:30") {
		t.Fatalf("morning = %#v", m)
	}
	if m := p.Find("Emmanuel Bishopston", at(18, 20)); m.Row == nil || m.Row.Title != "Evening" {
		t.Fatalf("evening = %#v", m)
	}
	// A row naming the church is preferred to the rows shared by every church.
	if m := p.Find("emmanuel city centre", at(10, 31)); m.Row == nil || m.Row.Title != "City Centre" {
		t.Fatalf("city centre = %#v", m)
	}
	if m := p.Find("Emmanuel Bishopston", time.Date(2026, 11, 8, 10, 30, 0, 0, time.Local)); m.Row == nil || m.Row.Line != 5 || !strings.Contains(m.Note, "row 5") {
		t.Fatalf("next week = %#v", m)
	}
	if m := p.Find("Emmanuel Bishopston", time.Date(2026, 11, 15, 10, 30, 0, 0, time.Local)); m.Row != nil || !strings.Contains(m.Note, "nothing for Sunday 15 November") {
		t.Fatalf("missing week = %#v", m)
	}
}

func TestReadsDatesAndTimesAsTheyMightBeTyped(t *testing.T) {
	number := func(v float64) cell { return cell{Text: fmt.Sprint(v), Number: &v} }
	for _, test := range []struct {
		cell     cell
		date1904 bool
		want     string
	}{
		{number(46327), false, "2026-11-01"},
		{number(46327.75), false, "2026-11-01"},
		{number(44865), true, "2026-11-01"},
		{cell{Text: "2026-11-01"}, false, "2026-11-01"},
		{cell{Text: "01/11/2026"}, false, "2026-11-01"},
		{cell{Text: "1/11/26"}, false, "2026-11-01"},
		{cell{Text: "Sunday 1 November 2026"}, false, "2026-11-01"},
		{cell{Text: "Sun 1st Nov 2026"}, false, "2026-11-01"},
		{cell{Text: "1-Nov-26"}, false, "2026-11-01"},
	} {
		got, err := readDate(test.cell, test.date1904)
		if err != nil || got != test.want {
			t.Errorf("readDate(%q, 1904=%v) = %q, %v", test.cell.Text, test.date1904, got, err)
		}
	}
	for text, want := range map[string]int{
		"10:30": 630, "10.30": 630, "10:30am": 630, "10:30 a.m.": 630, "6pm": 18 * 60, "6.30 pm": 18*60 + 30,
		"12pm": 12 * 60, "12:15am": 15, "18:30": 18*60 + 30, "10": -1, "Morning": -1, "25:00": -1, "13pm": -1, "": -1,
	} {
		if got := readTime(cell{Text: text}); got != want {
			t.Errorf("readTime(%q) = %d, want %d", text, got, want)
		}
	}
	if got := readTime(number(0.4375)); got != 630 {
		t.Errorf("readTime(0.4375) = %d", got)
	}
	if got := readTime(number(10)); got != -1 {
		t.Errorf("a whole number was read as a time: %d", got)
	}
}

func TestReadsCSVInEitherOfExcelsEncodings(t *testing.T) {
	dir := t.TempDir()
	utf8File := filepath.Join(dir, "utf8.csv")
	if err := os.WriteFile(utf8File, []byte("\xef\xbb\xbfDate,Speakers,Title\r\n01/11/2026,Zoë Smith,\"Rest, for the weary\"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The same in the Windows code page, where ë is the single byte 0xEB and
	// a curly apostrophe is 0x92.
	windowsFile := filepath.Join(dir, "windows.csv")
	if err := os.WriteFile(windowsFile, []byte("Date,Speakers,Title\r\n01/11/2026,Zo\xeb Smith,God\x92s rest\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{utf8File: "Rest, for the weary", windowsFile: "God’s rest"} {
		p, err := Read(name, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Rows) != 1 || p.Rows[0].Speakers[0] != "Zoë Smith" || p.Rows[0].Title != want || p.Rows[0].Date != "2026-11-01" {
			t.Fatalf("%s: rows = %#v", filepath.Base(name), p.Rows)
		}
	}
}

func TestReportsAProgrammeItCannotUse(t *testing.T) {
	dir := t.TempDir()
	noDate := filepath.Join(dir, "nodate.csv")
	if err := os.WriteFile(noDate, []byte("Sunday morning,Speaker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(noDate, ""); err == nil || !strings.Contains(err.Error(), "Date column") {
		t.Fatalf("no date column gave %v", err)
	}
	notZip := filepath.Join(dir, "broken.xlsx")
	if err := os.WriteFile(notZip, []byte("not a workbook"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(notZip, ""); err == nil || !strings.Contains(err.Error(), "not an Excel workbook") {
		t.Fatalf("a broken workbook gave %v", err)
	}
	if _, err := Read(filepath.Join(dir, "missing.xlsx"), ""); err == nil {
		t.Fatal("a missing file was read")
	}
}

func TestReadsWorkbooksWithAbsoluteTargetsAnd1904Dates(t *testing.T) {
	filename := workbook{
		sheets:          []testSheet{{name: "Sheet1", rows: `<row r="1">` + inline("A1", "Date") + inline("B1", "Title") + `</row><row r="2">` + n("A2", "44865") + inline("B2", "Mac") + `</row>`}},
		date1904:        true,
		absoluteTargets: true,
	}.write(t)
	p, err := Read(filename, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 1 || p.Rows[0].Date != "2026-11-01" || p.Rows[0].Title != "Mac" {
		t.Fatalf("rows = %#v", p.Rows)
	}
}

func TestColumnReferences(t *testing.T) {
	for reference, want := range map[string][2]int{"A1": {0, 0}, "Z9": {25, 8}, "AA10": {26, 9}, "AB2": {27, 1}} {
		column, row, ok := parseReference(reference)
		if !ok || column != want[0] || row != want[1] {
			t.Errorf("parseReference(%q) = %d, %d, %v", reference, column, row, ok)
		}
		if columnName(want[0])+fmt.Sprint(want[1]+1) != reference {
			t.Errorf("columnName(%d) = %q", want[0], columnName(want[0]))
		}
	}
	for _, bad := range []string{"", "1A", "A0", "a1"} {
		if _, _, ok := parseReference(bad); ok {
			t.Errorf("parseReference(%q) succeeded", bad)
		}
	}
}
