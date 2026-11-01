// Tests for reading the books out of a passage as people actually type it:
// abbreviations, numbered books, British full stops, several books separated by
// commas, and the mistakes that must be refused rather than guessed at.
package publish

import (
	"reflect"
	"strings"
	"testing"
)

func TestPassageBooksReadsWhatPeopleType(t *testing.T) {
	for _, test := range []struct {
		passage string
		want    []string
	}{
		{"Matthew 6:13", []string{"Matthew"}},
		{"Matthew 6:9-13, Ezekiel 36:22-27", []string{"Matthew", "Ezekiel"}},
		{"Matthew 6.9-13", []string{"Matthew"}},
		{"Rom 5:1–11", []string{"Romans"}},
		{"Genesis 1:1-2:3", []string{"Genesis"}},
		{"1 Thess 4:1-12", []string{"1 Thessalonians"}},
		{"1 Thess. 4:13-5:11", []string{"1 Thessalonians"}},
		{"1Cor 13", []string{"1 Corinthians"}},
		{"II Kings 5:1-14", []string{"2 Kings"}},
		{"First Peter 1:3-9", []string{"1 Peter"}},
		{"John 3:16, 18; 1 John 4:7-12", []string{"John", "1 John"}},
		{"Jn 1:1-14, 3 Jn 2", []string{"John", "3 John"}},
		{"Psalm 23", []string{"Psalms"}},
		{"Ps. 121, Pss 122-124", []string{"Psalms"}},
		{"Song of Solomon 2:1-7", []string{"Song of Songs"}},
		{"Isaiah 53, Is 40:1-11", []string{"Isaiah"}},
		{"Jude 3", []string{"Jude"}},
		{"Phil 2:5-11, Philem 1-25", []string{"Philippians", "Philemon"}},
		{"Revelation", []string{"Revelation"}},
		{"Matthew 6:9-13, vv 16-18", []string{"Matthew"}},
		{"Isaiah 6:1-8, ", []string{"Isaiah"}},
		{"", nil},
	} {
		got, err := PassageBooks(test.passage)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("PassageBooks(%q) = %v, %v; want %v", test.passage, got, err, test.want)
		}
	}
}

func TestPassageBooksRefusesWhatItCannotFileCorrectly(t *testing.T) {
	for _, test := range []struct{ passage, says string }{
		{"Sirach 1:1-10", "not a book of the Bible this site lists"},
		{"3 Peter 1", "no 3 Peter"},
		{"Corinthians 13", "needs a number"},
		{"1 Genesis 1", "no 1 Genesis"},
		{"16-18", "starts without naming a book"},
		{"Genesis 50:26 - Exodus 1:7", "separate passages in different books with a comma"},
		{"Jud 5", "not a book"},
	} {
		_, err := PassageBooks(test.passage)
		if err == nil || !strings.Contains(err.Error(), test.says) {
			t.Errorf("PassageBooks(%q) error = %v, want one saying %q", test.passage, err, test.says)
		}
	}
}

func TestEveryBookNameMatchesTheSitesList(t *testing.T) {
	// The site's names, in its own order, as listed by the probe.
	site := []string{"Genesis", "Exodus", "Leviticus", "Numbers", "Deuteronomy", "Joshua", "Judges", "Ruth",
		"1 Samuel", "2 Samuel", "1 Kings", "2 Kings", "1 Chronicles", "2 Chronicles", "Ezra", "Nehemiah",
		"Esther", "Job", "Psalms", "Proverbs", "Ecclesiastes", "Song of Songs", "Isaiah", "Jeremiah",
		"Lamentations", "Ezekiel", "Daniel", "Hosea", "Joel", "Amos", "Obadiah", "Jonah", "Micah", "Nahum",
		"Habakkuk", "Zephaniah", "Haggai", "Zechariah", "Malachi", "Matthew", "Mark", "Luke", "John", "Acts",
		"Romans", "1 Corinthians", "2 Corinthians", "Galatians", "Ephesians", "Philippians", "Colossians",
		"1 Thessalonians", "2 Thessalonians", "1 Timothy", "2 Timothy", "Titus", "Philemon", "Hebrews",
		"James", "1 Peter", "2 Peter", "1 John", "2 John", "3 John", "Jude", "Revelation"}
	if len(site) != 66 {
		t.Fatalf("the test list has %d books", len(site))
	}
	terms := make([]Term, len(site))
	for i, name := range site {
		terms[i] = Term{ID: 200 + i, Name: name}
		books, err := PassageBooks(name + " 1")
		if err != nil || len(books) != 1 || books[0] != name {
			t.Errorf("PassageBooks(%q) = %v, %v", name+" 1", books, err)
		}
	}
	found, err := BookTerms([]string{"Matthew", "Ezekiel"}, terms)
	if err != nil || len(found) != 2 || found[0].ID != 239 || found[1].ID != 225 {
		t.Errorf("BookTerms = %v, %v; want terms 239 and 225 as on the site", found, err)
	}
	if _, err := BookTerms([]string{"Sirach"}, terms); err == nil {
		t.Error("a book missing from the site's list should be an error")
	}
}
