// Tests for the term lists: complete paging, and matching that finds the same
// name however it is typed and warns about near misses without crying wolf over
// different people who share a surname.
package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func TestTermsFollowsEveryPageAndAsksOnlyForWhatItUses(t *testing.T) {
	const total = 286 // the site's series count when it was probed
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if r.URL.Path != "/wp-json/wp/v2/resource-series" || query.Get("per_page") != "100" ||
			query.Get("hide_empty") != "false" || query.Get("_fields") != "id,name,slug,count" {
			t.Errorf("request = %s", r.URL)
		}
		page, _ := strconv.Atoi(query.Get("page"))
		var batch []Term
		for id := (page-1)*100 + 1; id <= min(page*100, total); id++ {
			batch = append(batch, Term{ID: id, Name: fmt.Sprintf("Series %d &amp; more", id)})
		}
		w.Header().Set("X-WP-Total", strconv.Itoa(total))
		w.Header().Set("X-WP-TotalPages", "3")
		json.NewEncoder(w).Encode(batch)
	})
	terms, err := client.Terms(context.Background(), "resource-series")
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != total || terms[total-1].ID != total {
		t.Fatalf("got %d terms, want all %d", len(terms), total)
	}
	if terms[0].Name != "Series 1 & more" {
		t.Errorf("name = %q; HTML entities should be decoded", terms[0].Name)
	}
}

func TestTermsStopsAtAnEmptyPageEvenIfThePageCountIsWrong(t *testing.T) {
	requests := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("X-WP-TotalPages", "50")
		if r.URL.Query().Get("page") == "1" {
			w.Write([]byte(`[{"id":1,"name":"Acts"}]`))
			return
		}
		w.Write([]byte(`[]`))
	})
	terms, err := client.Terms(context.Background(), "resource-series")
	if err != nil || len(terms) != 1 || requests != 2 {
		t.Errorf("terms %v, err %v, requests %d", terms, err, requests)
	}
}

func TestCreateTermPostsTheTrimmedName(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if r.Method != http.MethodPost || r.URL.Path != "/wp-json/wp/v2/resource-contributor" || body["name"] != "Ruth Visiting" {
			t.Errorf("%s %s %v", r.Method, r.URL.Path, body)
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":740,"name":"Ruth Visiting","slug":"ruth-visiting"}`))
	})
	term, err := client.CreateTerm(context.Background(), "resource-contributor", "  Ruth Visiting ")
	if err != nil || term.ID != 740 {
		t.Errorf("term %+v, err %v", term, err)
	}
}

// siteSpeakers and siteSeries are drawn from the real lists.
var (
	siteSpeakers = []Term{
		{ID: 296, Name: "Jim Walford"},
		{ID: 709, Name: "Claire Walford"},
		{ID: 299, Name: "James Maurice"},
		{ID: 680, Name: "Angus Macleay"},
		{ID: 330, Name: "Mike Cain"},
	}
	siteSeries = []Term{
		{ID: 736, Name: "The Lord's Prayer (EB)"},
		{ID: 732, Name: "Amazing Grace (2 Kings; EB)"},
		{ID: 731, Name: "Amazing Grace (2 Kings; ECC)"},
	}
)

func TestMatchTermFindsTheSameNameHoweverItIsTyped(t *testing.T) {
	for _, test := range []struct {
		terms []Term
		name  string
		want  int
	}{
		{siteSpeakers, "Jim Walford", 296},
		{siteSpeakers, "  jim   WALFORD ", 296},
		{siteSeries, "The Lord’s Prayer (EB)", 736},       // curly apostrophe
		{siteSeries, "The Lord&#8217;s Prayer (EB)", 736}, // as WordPress renders titles
	} {
		match := MatchTerm(test.terms, test.name)
		if match.Exact == nil || match.Exact.ID != test.want {
			t.Errorf("MatchTerm(%q).Exact = %v, want term %d", test.name, match.Exact, test.want)
		}
	}
}

func TestMatchTermWarnsAboutLikelyMisspellingsOnly(t *testing.T) {
	match := MatchTerm(siteSpeakers, "James Morris")
	if match.Exact != nil || len(match.Near) == 0 || match.Near[0].ID != 299 {
		t.Errorf("James Morris should be flagged as near James Maurice: %+v", match)
	}

	match = MatchTerm(siteSpeakers, "Mike Cain (guest)")
	if len(match.Near) == 0 || match.Near[0].ID != 330 {
		t.Errorf("a name containing an existing one should be flagged: %+v", match)
	}

	match = MatchTerm(siteSpeakers, "Jim Walford")
	for _, near := range match.Near {
		if near.ID == 709 {
			t.Error("Claire Walford is a different person, not a misspelling of Jim Walford")
		}
	}

	match = MatchTerm(siteSeries, "Amazing Grace (2 Kings; EB)")
	if match.Exact == nil || match.Exact.ID != 732 {
		t.Fatalf("exact = %v", match.Exact)
	}
	if len(match.Near) != 1 || match.Near[0].ID != 731 {
		t.Errorf("the other congregation's series is worth a warning: %+v", match.Near)
	}

	if match := MatchTerm(siteSpeakers, "   "); match.Exact != nil || len(match.Near) != 0 {
		t.Errorf("an empty name should match nothing: %+v", match)
	}
}

func TestEditDistanceCountsCharactersNotBytes(t *testing.T) {
	for _, test := range []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"maurice", "morris", 4},
		{"étienne", "etienne", 1},
		{"kitten", "sitting", 3},
	} {
		if got := editDistance(test.a, test.b); got != test.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", test.a, test.b, got, test.want)
		}
	}
}
