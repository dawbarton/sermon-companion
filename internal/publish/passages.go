// Bible passages and the book terms they file a talk under. The operator types
// the passage as it should read on the site, such as "Matthew 6:9-13, Ezekiel
// 36:22-27", and that text goes into the passage field unchanged. The books it
// names are worked out here, so that the bible_book terms always agree with the
// passage rather than being chosen separately and drifting from it. A talk may
// draw on several books, and every one of them is filed.
package publish

import (
	"fmt"
	"regexp"
	"strings"
)

// book is one book of the Bible as the site names it. A numbered book lists the
// numbers it takes; its name is then the number, a space, and base.
type book struct {
	base    string
	numbers []int
	aliases []string
}

// bibleBooks are the 66 books the site's bible_book taxonomy holds, with the
// abbreviations people commonly type. Ambiguous ones are left out on purpose:
// "Jud" could be Jude or Judges, and "Jo" could be Job, Joel, or John.
var bibleBooks = []book{
	{base: "Genesis", aliases: []string{"gen", "ge", "gn"}},
	{base: "Exodus", aliases: []string{"exod", "exo", "ex"}},
	{base: "Leviticus", aliases: []string{"lev", "le", "lv"}},
	{base: "Numbers", aliases: []string{"num", "nu", "nm", "nb"}},
	{base: "Deuteronomy", aliases: []string{"deut", "de", "dt"}},
	{base: "Joshua", aliases: []string{"josh", "jos", "jsh"}},
	{base: "Judges", aliases: []string{"judg", "jdg", "jdgs", "jg"}},
	{base: "Ruth", aliases: []string{"rth", "ru"}},
	{base: "Samuel", numbers: []int{1, 2}, aliases: []string{"sam", "sa", "sm"}},
	{base: "Kings", numbers: []int{1, 2}, aliases: []string{"kgs", "kin", "ki"}},
	{base: "Chronicles", numbers: []int{1, 2}, aliases: []string{"chron", "chr", "ch"}},
	{base: "Ezra", aliases: []string{"ezr"}},
	{base: "Nehemiah", aliases: []string{"neh", "ne"}},
	{base: "Esther", aliases: []string{"esth", "est", "es"}},
	{base: "Job", aliases: []string{"jb"}},
	{base: "Psalms", aliases: []string{"psalm", "pss", "psa", "psm", "pslm", "ps"}},
	{base: "Proverbs", aliases: []string{"prov", "pro", "prv", "pr"}},
	{base: "Ecclesiastes", aliases: []string{"eccles", "eccle", "eccl", "ecc", "ec", "qoheleth", "qoh"}},
	{base: "Song of Songs", aliases: []string{"song of solomon", "song of sol", "song", "canticles", "cant", "sos", "sg"}},
	{base: "Isaiah", aliases: []string{"isa", "is"}},
	{base: "Jeremiah", aliases: []string{"jer", "je", "jr"}},
	{base: "Lamentations", aliases: []string{"lam", "la"}},
	{base: "Ezekiel", aliases: []string{"ezek", "eze", "ezk"}},
	{base: "Daniel", aliases: []string{"dan", "da", "dn"}},
	{base: "Hosea", aliases: []string{"hos", "ho"}},
	{base: "Joel", aliases: []string{"jl"}},
	{base: "Amos", aliases: []string{"am"}},
	{base: "Obadiah", aliases: []string{"obad", "ob"}},
	{base: "Jonah", aliases: []string{"jnh", "jon"}},
	{base: "Micah", aliases: []string{"mic", "mc"}},
	{base: "Nahum", aliases: []string{"nah", "na"}},
	{base: "Habakkuk", aliases: []string{"hab", "hb"}},
	{base: "Zephaniah", aliases: []string{"zeph", "zep", "zp"}},
	{base: "Haggai", aliases: []string{"hag", "hg"}},
	{base: "Zechariah", aliases: []string{"zech", "zec", "zc"}},
	{base: "Malachi", aliases: []string{"mal", "ml"}},
	{base: "Matthew", aliases: []string{"matt", "mat", "mt"}},
	{base: "Mark", aliases: []string{"mrk", "mar", "mk", "mr"}},
	{base: "Luke", aliases: []string{"luk", "lk"}},
	// Unnumbered, John is the Gospel; numbered, it is one of the letters.
	{base: "John", numbers: []int{0, 1, 2, 3}, aliases: []string{"jhn", "joh", "jn"}},
	{base: "Acts", aliases: []string{"act", "ac"}},
	{base: "Romans", aliases: []string{"rom", "ro", "rm"}},
	{base: "Corinthians", numbers: []int{1, 2}, aliases: []string{"cor", "co"}},
	{base: "Galatians", aliases: []string{"gal", "ga"}},
	{base: "Ephesians", aliases: []string{"ephes", "eph"}},
	{base: "Philippians", aliases: []string{"phil", "php", "pp"}},
	{base: "Colossians", aliases: []string{"col"}},
	{base: "Thessalonians", numbers: []int{1, 2}, aliases: []string{"thess", "thes", "th"}},
	{base: "Timothy", numbers: []int{1, 2}, aliases: []string{"tim", "ti"}},
	{base: "Titus", aliases: []string{"tit"}},
	{base: "Philemon", aliases: []string{"philem", "phlm", "phm"}},
	{base: "Hebrews", aliases: []string{"heb"}},
	{base: "James", aliases: []string{"jas", "jm"}},
	{base: "Peter", numbers: []int{1, 2}, aliases: []string{"pet", "pe", "pt"}},
	{base: "Jude", aliases: []string{"jde"}},
	{base: "Revelation", aliases: []string{"revelations", "rev", "re"}},
}

// bookByStem finds a book from what is typed after any number.
var bookByStem = func() map[string]*book {
	stems := make(map[string]*book)
	for i := range bibleBooks {
		b := &bibleBooks[i]
		for _, stem := range append([]string{strings.ToLower(b.base)}, b.aliases...) {
			if _, taken := stems[stem]; taken {
				panic("publish: duplicate book abbreviation " + stem)
			}
			stems[stem] = b
		}
	}
	return stems
}()

// ordinals are the ways a book's number is written. A digit may run straight
// into the name ("1Cor"); the others need a space, or "isaiah" would read as
// "i saiah".
var ordinals = map[string]int{
	"1": 1, "2": 2, "3": 3,
	"i": 1, "ii": 2, "iii": 3,
	"1st": 1, "2nd": 2, "3rd": 3,
	"first": 1, "second": 2, "third": 3,
}

var (
	digitFirst = regexp.MustCompile(`^([123])\s*([a-z][a-z' ]*?)\s*([0-9].*)?$`)
	wordFirst  = regexp.MustCompile(`^(i|ii|iii|1st|2nd|3rd|first|second|third)\s+([a-z][a-z' ]*?)\s*([0-9].*)?$`)
	nameFirst  = regexp.MustCompile(`^([a-z][a-z' ]*?)\s*([0-9].*)?$`)
	reference  = regexp.MustCompile(`^[0-9][0-9:. \-–—abcf]*$`)
	verseWords = map[string]bool{"v": true, "vv": true, "ver": true, "vers": true, "verse": true, "verses": true}
)

// PassageBooks returns the books a passage names, as the site names them, in
// the order they first appear and without repeats. Passages are separated by
// commas or semicolons; a part with no book of its own, such as the "18" in
// "John 3:16, 18", continues the book before it. An empty passage names no
// books.
func PassageBooks(passage string) ([]string, error) {
	var books []string
	seen := make(map[string]bool)
	current := ""
	for _, part := range strings.FieldsFunc(passage, func(r rune) bool { return r == ',' || r == ';' }) {
		normal := normalisePassage(part)
		if normal == "" {
			continue
		}
		name, err := partBook(normal)
		if err != nil {
			return nil, fmt.Errorf("in the passage %q, %q: %w", passage, strings.TrimSpace(part), err)
		}
		if name == "" {
			if current == "" {
				return nil, fmt.Errorf("the passage %q starts without naming a book", passage)
			}
			continue
		}
		current = name
		if !seen[name] {
			seen[name] = true
			books = append(books, name)
		}
	}
	return books, nil
}

// normalisePassage lower-cases one part of a passage and turns full stops after
// abbreviations into spaces. A full stop between numbers, as in the British
// "6.9", is left alone.
func normalisePassage(part string) string {
	var out strings.Builder
	runes := []rune(strings.ToLower(part))
	for i, r := range runes {
		if r == '.' && i > 0 && runes[i-1] >= 'a' && runes[i-1] <= 'z' {
			out.WriteRune(' ')
			continue
		}
		out.WriteRune(r)
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// partBook reads the book at the start of one part of a passage. It returns ""
// for a part that is only a reference, which continues the previous book.
func partBook(part string) (string, error) {
	if reference.MatchString(part) {
		return "", nil
	}
	if m := digitFirst.FindStringSubmatch(part); m != nil {
		return numberedBook(ordinals[m[1]], m[2], m[3])
	}
	if m := wordFirst.FindStringSubmatch(part); m != nil {
		if b := bookByStem[m[2]]; b != nil && b.takes(ordinals[m[1]]) {
			return numberedBook(ordinals[m[1]], m[2], m[3])
		}
	}
	if m := nameFirst.FindStringSubmatch(part); m != nil {
		if verseWords[m[1]] && m[2] != "" {
			return "", nil
		}
		return numberedBook(0, m[1], m[2])
	}
	return "", fmt.Errorf("could not read it as a book and a reference; separate passages in different books with a comma")
}

func numberedBook(number int, stem, ref string) (string, error) {
	b := bookByStem[strings.TrimSpace(stem)]
	if b == nil {
		return "", fmt.Errorf("%q is not a book of the Bible this site lists", strings.TrimSpace(stem))
	}
	if ref != "" && !reference.MatchString(ref) {
		return "", fmt.Errorf("could not read %q as a chapter and verse; separate passages in different books with a comma", ref)
	}
	if !b.takes(number) {
		if number == 0 {
			return "", fmt.Errorf("%s needs a number, such as 1 %s", b.base, b.base)
		}
		return "", fmt.Errorf("there is no %d %s", number, b.base)
	}
	if number == 0 {
		return b.base, nil
	}
	return fmt.Sprintf("%d %s", number, b.base), nil
}

// takes reports whether the book can be written with that number, zero meaning
// none.
func (b *book) takes(number int) bool {
	if len(b.numbers) == 0 {
		return number == 0
	}
	for _, n := range b.numbers {
		if n == number {
			return true
		}
	}
	return false
}

// BookTerms finds the site's term for each book. A book with no term is an
// error rather than silently left out: the site's list should hold all 66, so
// a gap means the list has changed.
func BookTerms(books []string, terms []Term) ([]Term, error) {
	found := make([]Term, 0, len(books))
	for _, name := range books {
		match := MatchTerm(terms, name)
		if match.Exact == nil {
			return nil, fmt.Errorf("the site's list of Bible books has no %q", name)
		}
		found = append(found, *match.Exact)
	}
	return found, nil
}
