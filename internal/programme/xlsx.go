// Reading the cell values of one worksheet from an Excel .xlsx file. An .xlsx
// file is a zip of XML parts, so the standard library is enough for what the
// programme needs: the text and numbers in the cells. Formatting, formulae,
// and everything else are ignored. The number formats that would say a number
// is a date are ignored too; the programme knows which of its columns hold
// dates and reads those numbers as dates itself.
package programme

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// cell is one value from a worksheet. Number is set when Excel stored a
// number, which is how it stores a date or a time typed into a cell.
type cell struct {
	Text   string
	Number *float64
}

// sheet is a worksheet's values by row and then column, both from zero, and
// whether the workbook counts dates from 1904 rather than 1900.
type sheet struct {
	Rows     [][]cell
	Date1904 bool
}

// Limits that keep a damaged or hostile file from exhausting memory. A
// preaching programme is a few hundred rows of short text.
const (
	maximumPartBytes = 64 << 20
	maximumRows      = 20_000
	maximumColumns   = 200
)

const officeDocumentType = "/officeDocument"

func readXLSX(filename, sheetName string) (*sheet, error) {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return nil, fmt.Errorf("this is not an Excel workbook that can be read: %w", err)
	}
	defer archive.Close()
	parts := map[string]*zip.File{}
	for _, file := range archive.File {
		parts[strings.TrimPrefix(file.Name, "/")] = file
	}

	workbookPath := "xl/workbook.xml"
	if rels, err := readRelationships(parts, "_rels/.rels", ""); err == nil {
		for _, rel := range rels {
			if strings.HasSuffix(rel.Type, officeDocumentType) {
				workbookPath = rel.Target
			}
		}
	}
	var workbook struct {
		Properties struct {
			Date1904 string `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Sheets []struct {
			Name  string     `xml:"name,attr"`
			Attrs []xml.Attr `xml:",any,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := decodePart(parts, workbookPath, &workbook); err != nil {
		return nil, err
	}
	if len(workbook.Sheets) == 0 {
		return nil, errors.New("the workbook has no worksheets")
	}
	chosen := -1
	names := make([]string, len(workbook.Sheets))
	for i, candidate := range workbook.Sheets {
		names[i] = candidate.Name
		if sheetName == "" && chosen < 0 || sheetName != "" && strings.EqualFold(strings.TrimSpace(candidate.Name), strings.TrimSpace(sheetName)) {
			chosen = i
		}
	}
	if chosen < 0 {
		return nil, fmt.Errorf("the workbook has no worksheet called %q; it has %s", sheetName, strings.Join(quoteAll(names), ", "))
	}
	relationshipID := ""
	for _, attr := range workbook.Sheets[chosen].Attrs {
		// The relationship ID's namespace differs between the transitional
		// and strict forms of the format, so only its local name is checked.
		if attr.Name.Local == "id" && strings.Contains(attr.Name.Space, "relationships") {
			relationshipID = attr.Value
		}
	}
	workbookRels, err := readRelationships(parts, path.Join(path.Dir(workbookPath), "_rels", path.Base(workbookPath)+".rels"), path.Dir(workbookPath))
	if err != nil {
		return nil, err
	}
	sheetPath, sharedPath := "", ""
	for _, rel := range workbookRels {
		if rel.ID == relationshipID {
			sheetPath = rel.Target
		}
		if strings.HasSuffix(rel.Type, "/sharedStrings") {
			sharedPath = rel.Target
		}
	}
	if sheetPath == "" {
		return nil, fmt.Errorf("the workbook does not say where the worksheet %q is", workbook.Sheets[chosen].Name)
	}
	var shared []string
	if sharedPath != "" {
		if shared, err = readSharedStrings(parts, sharedPath); err != nil {
			return nil, err
		}
	}
	rows, err := readWorksheet(parts, sheetPath, shared)
	if err != nil {
		return nil, err
	}
	date1904 := workbook.Properties.Date1904 == "1" || strings.EqualFold(workbook.Properties.Date1904, "true")
	return &sheet{Rows: rows, Date1904: date1904}, nil
}

type relationship struct {
	ID     string `xml:"Id,attr"`
	Type   string `xml:"Type,attr"`
	Target string `xml:"Target,attr"`
}

// readRelationships reads a .rels part, resolving each target against base.
func readRelationships(parts map[string]*zip.File, name, base string) ([]relationship, error) {
	var document struct {
		Relationships []relationship `xml:"Relationship"`
	}
	if err := decodePart(parts, name, &document); err != nil {
		return nil, err
	}
	for i, rel := range document.Relationships {
		target := strings.ReplaceAll(rel.Target, `\`, "/")
		if strings.HasPrefix(target, "/") {
			target = strings.TrimPrefix(target, "/")
		} else {
			target = path.Join(base, target)
		}
		document.Relationships[i].Target = path.Clean(target)
	}
	return document.Relationships, nil
}

func openPart(parts map[string]*zip.File, name string) (io.ReadCloser, error) {
	file, ok := parts[name]
	if !ok {
		return nil, fmt.Errorf("the workbook is missing its part %s", name)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("reading %s from the workbook: %w", name, err)
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(reader, maximumPartBytes), reader}, nil
}

func decodePart(parts map[string]*zip.File, name string, into any) error {
	reader, err := openPart(parts, name)
	if err != nil {
		return err
	}
	defer reader.Close()
	if err := xml.NewDecoder(reader).Decode(into); err != nil {
		return fmt.Errorf("reading %s from the workbook: %w", name, err)
	}
	return nil
}

// readSharedStrings reads the table that most text cells refer to by index.
// An entry is either plain text or runs of differently formatted text, which
// are joined; phonetic guides are not part of the text and are skipped.
func readSharedStrings(parts map[string]*zip.File, name string) ([]string, error) {
	reader, err := openPart(parts, name)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	decoder := xml.NewDecoder(reader)
	var (
		items    []string
		current  strings.Builder
		inItem   bool
		inText   bool
		phonetic int
	)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return items, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s from the workbook: %w", name, err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inItem = true
				current.Reset()
			case "rPh":
				phonetic++
			case "t":
				inText = inItem && phonetic == 0
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				inItem = false
				items = append(items, current.String())
				if len(items) > maximumRows*maximumColumns {
					return nil, errors.New("the workbook has more text than a preaching programme would")
				}
			case "rPh":
				phonetic--
			case "t":
				inText = false
			}
		case xml.CharData:
			if inText {
				current.Write(t)
			}
		}
	}
}

// readWorksheet reads every cell's value. Cells are placed by their own
// references, such as C7, because empty cells are usually left out.
func readWorksheet(parts map[string]*zip.File, name string, shared []string) ([][]cell, error) {
	reader, err := openPart(parts, name)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	decoder := xml.NewDecoder(reader)
	var (
		rows                [][]cell
		row                 = -1
		column              = -1
		kind                string
		value, inline       strings.Builder
		inValue, inInline   bool
		inInlineText        bool
		phonetic            int
		cellOpen, inSheet   bool
		nextRow, nextColumn int
	)
	put := func() error {
		if row < 0 || column < 0 {
			return nil
		}
		if row >= maximumRows || column >= maximumColumns {
			return errors.New("the worksheet is larger than a preaching programme would be")
		}
		c, err := cellValue(kind, value.String(), inline.String(), shared)
		if err != nil {
			return fmt.Errorf("cell %s%d: %w", columnName(column), row+1, err)
		}
		if c.Text == "" && c.Number == nil {
			return nil
		}
		for len(rows) <= row {
			rows = append(rows, nil)
		}
		for len(rows[row]) <= column {
			rows[row] = append(rows[row], cell{})
		}
		rows[row][column] = c
		return nil
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading the worksheet: %w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "sheetData":
				inSheet = true
			case "row":
				if !inSheet {
					continue
				}
				row = nextRow
				if r := attribute(t, "r"); r != "" {
					if n, err := strconv.Atoi(r); err == nil && n >= 1 {
						row = n - 1
					}
				}
				nextRow, nextColumn = row+1, 0
			case "c":
				if !inSheet {
					continue
				}
				cellOpen = true
				column = nextColumn
				if reference := attribute(t, "r"); reference != "" {
					if c, r, ok := parseReference(reference); ok {
						column, row = c, r
					}
				}
				nextColumn = column + 1
				kind = attribute(t, "t")
				value.Reset()
				inline.Reset()
			case "v":
				inValue = cellOpen
			case "is":
				inInline = cellOpen
			case "rPh":
				phonetic++
			case "t":
				inInlineText = inInline && phonetic == 0
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "sheetData":
				inSheet = false
			case "c":
				if cellOpen {
					if err := put(); err != nil {
						return nil, err
					}
				}
				cellOpen = false
			case "v":
				inValue = false
			case "is":
				inInline = false
			case "rPh":
				phonetic--
			case "t":
				inInlineText = false
			}
		case xml.CharData:
			if inValue {
				value.Write(t)
			} else if inInlineText {
				inline.Write(t)
			}
		}
	}
}

func cellValue(kind, value, inline string, shared []string) (cell, error) {
	switch kind {
	case "s":
		index, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || index < 0 || index >= len(shared) {
			return cell{}, errors.New("refers to text the workbook does not have")
		}
		return cell{Text: shared[index]}, nil
	case "inlineStr":
		return cell{Text: inline}, nil
	case "str", "d":
		// A formula's text result, or a date written out in ISO 8601.
		return cell{Text: value}, nil
	case "b":
		if strings.TrimSpace(value) == "1" {
			return cell{Text: "TRUE"}, nil
		}
		return cell{Text: "FALSE"}, nil
	case "e":
		return cell{Text: value}, nil
	default:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return cell{}, nil
		}
		number, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return cell{Text: trimmed}, nil
		}
		return cell{Text: trimmed, Number: &number}, nil
	}
}

func attribute(element xml.StartElement, name string) string {
	for _, attr := range element.Attr {
		if attr.Name.Local == name && attr.Name.Space == "" {
			return attr.Value
		}
	}
	return ""
}

// parseReference reads a cell reference such as AB12 as a column and a row,
// both counted from zero.
func parseReference(reference string) (column, row int, ok bool) {
	letters := 0
	for letters < len(reference) && reference[letters] >= 'A' && reference[letters] <= 'Z' {
		column = column*26 + int(reference[letters]-'A'+1)
		letters++
		if column > maximumColumns*26 {
			return 0, 0, false
		}
	}
	if letters == 0 {
		return 0, 0, false
	}
	n, err := strconv.Atoi(reference[letters:])
	if err != nil || n < 1 {
		return 0, 0, false
	}
	return column - 1, n - 1, true
}

func columnName(column int) string {
	name := ""
	for column++; column > 0; column = (column - 1) / 26 {
		name = string(rune('A'+(column-1)%26)) + name
	}
	return name
}

func quoteAll(names []string) []string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = strconv.Quote(name)
	}
	return quoted
}
