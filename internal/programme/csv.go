// Reading the programme from a .csv file. Excel saves CSV either as UTF-8,
// with a byte-order mark, or in the Windows code page, depending on which of
// its CSV types was chosen; both are accepted, so that a name with an accent
// arrives intact whichever was used.
package programme

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

func readCSV(filename string) ([][]cell, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumPartBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maximumPartBytes {
		return nil, errors.New("the programme is larger than a preaching programme would be")
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	text := string(data)
	if !utf8.Valid(data) {
		text = fromWindows1252(data)
	}
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	var rows [][]cell
	for {
		record, err := reader.Read()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading the programme: %w", err)
		}
		if len(rows) >= maximumRows {
			return nil, errors.New("the programme is larger than a preaching programme would be")
		}
		row := make([]cell, len(record))
		for i, value := range record {
			row[i] = cell{Text: value}
		}
		rows = append(rows, row)
	}
}

// windows1252 gives the characters Windows puts in bytes 0x80 to 0x9F, where
// ISO 8859-1 has control codes. The rest of the code page matches Unicode.
var windows1252 = [32]rune{
	'€', '\u0081', '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', '\u008d', 'Ž', '\u008f',
	'\u0090', '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', '\u009d', 'ž', 'Ÿ',
}

func fromWindows1252(data []byte) string {
	var out strings.Builder
	for _, b := range data {
		switch {
		case b >= 0x80 && b <= 0x9f:
			out.WriteRune(windows1252[b-0x80])
		default:
			out.WriteRune(rune(b))
		}
	}
	return out.String()
}
