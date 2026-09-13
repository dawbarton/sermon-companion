package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Probe performs read-only reconnaissance of a WordPress site's REST API. The
// sermon upload design has to be based on what a particular site exposes, not
// on what a plugin's documentation claims, and the two differ often enough
// that guessing is not worth the round trip. Nothing here writes to the site.
type Probe struct {
	Base   *url.URL
	User   string
	Pass   string
	OutDir string
	HTTP   *http.Client

	// pretty records whether the site serves the REST API at /wp-json/. A site
	// left on plain permalinks serves it at /?rest_route=/ instead, and a probe
	// that only knew the first form would report a perfectly good site as
	// having no REST API at all.
	pretty  bool
	steps   []step
	summary strings.Builder

	// TalkURL is an optional page on the site that the operator knows to be a
	// talk. Resolving it is far more direct than guessing which custom post
	// type holds talks, and any filter parameters on it name real taxonomy
	// terms, which is how a facet's opaque parameter is traced back to the
	// taxonomy the app would have to set.
	TalkURL *url.URL

	// indexHidden records that the REST index itself was refused while the
	// individual routes answered. Several security plugins do exactly this,
	// and treating it as "no REST API" would abandon a perfectly usable site.
	indexHidden bool
}

type step struct {
	Name   string
	Method string
	URL    string
	Status int
	File   string
	Note   string
	Err    string
	Body   string // the first part of a refusal, which says who refused
}

// sermonWords are the substrings that suggest a post type or taxonomy carries
// talks rather than ordinary site content. They are a hint for the summary
// only: every non-builtin type is reported whether it matches or not.
var sermonWords = []string{"sermon", "talk", "message", "teaching", "preach", "podcast", "series", "speaker", "address"}

func looksLikeSermon(names ...string) bool {
	for _, name := range names {
		lower := strings.ToLower(name)
		for _, word := range sermonWords {
			if strings.Contains(lower, word) {
				return true
			}
		}
	}
	return false
}

// restURL builds the address of a REST route, in whichever of the two forms
// this site serves.
func (p *Probe) restURL(route string, query url.Values) string {
	u := *p.Base
	if query == nil {
		query = url.Values{}
	}
	if p.pretty {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/wp-json" + route
		u.RawQuery = query.Encode()
		return u.String()
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/"
	query.Set("rest_route", route)
	u.RawQuery = query.Encode()
	return u.String()
}

func (p *Probe) authenticated() bool { return p.User != "" && p.Pass != "" }

// request performs one call and, when the body is JSON, saves it under file.
// The saved body is the site's own response: it is written verbatim so that a
// field missed by this tool's summary can still be read afterwards.
func (p *Probe) request(name, method, address, file string) ([]byte, int, error) {
	req, err := http.NewRequest(method, address, nil)
	if err != nil {
		p.steps = append(p.steps, step{Name: name, Method: method, URL: address, Err: err.Error()})
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "sermon-companion-wp-probe/1")
	if p.authenticated() {
		credential := base64.StdEncoding.EncodeToString([]byte(p.User + ":" + p.Pass))
		req.Header.Set("Authorization", "Basic "+credential)
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		p.steps = append(p.steps, step{Name: name, Method: method, URL: address, Err: err.Error()})
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		p.steps = append(p.steps, step{Name: name, Method: method, URL: address, Status: resp.StatusCode, Err: err.Error()})
		return nil, resp.StatusCode, err
	}
	record := step{Name: name, Method: method, URL: address, Status: resp.StatusCode}
	if resp.StatusCode >= 400 {
		// A WordPress refusal names itself (`rest_no_route`, `rest_forbidden`);
		// a web application firewall or a host block looks nothing like one.
		// Keeping the first part of the body is what tells the two apart.
		record.Body = compact(string(body))
	}
	if file != "" && len(body) > 0 {
		if saved, err := p.save(file, body); err == nil {
			record.File = saved
		} else {
			record.Note = "could not save: " + err.Error()
		}
	}
	p.steps = append(p.steps, record)
	return body, resp.StatusCode, nil
}

// save writes a response, indented when it is JSON so that it can be read and
// diffed by a person.
func (p *Probe) save(file string, body []byte) (string, error) {
	if err := os.MkdirAll(p.OutDir, 0o755); err != nil {
		return "", err
	}
	var pretty bytes.Buffer
	content := body
	if json.Indent(&pretty, body, "", "  ") == nil {
		content = pretty.Bytes()
	}
	path := filepath.Join(p.OutDir, file)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func decode[T any](body []byte) (T, bool) {
	var value T
	if err := json.Unmarshal(body, &value); err != nil {
		return value, false
	}
	return value, true
}

type restIndex struct {
	Name           string                     `json:"name"`
	Description    string                     `json:"description"`
	URL            string                     `json:"url"`
	Home           string                     `json:"home"`
	Namespaces     []string                   `json:"namespaces"`
	Authentication map[string]json.RawMessage `json:"authentication"`
	Routes         map[string]json.RawMessage `json:"routes"`
	TimezoneString string                     `json:"timezone_string"`
	GMTOffset      json.Number                `json:"gmt_offset"`
}

type postType struct {
	Name       string   `json:"name"`
	Slug       string   `json:"slug"`
	RestBase   string   `json:"rest_base"`
	Taxonomies []string `json:"taxonomies"`
	Viewable   bool     `json:"viewable"`
	Hierarchic bool     `json:"hierarchical"`
}

type taxonomy struct {
	Name     string   `json:"name"`
	Slug     string   `json:"slug"`
	RestBase string   `json:"rest_base"`
	Types    []string `json:"types"`
}

var builtinTypes = map[string]bool{
	"post": true, "page": true, "attachment": true, "nav_menu_item": true,
	"wp_block": true, "wp_template": true, "wp_template_part": true,
	"wp_navigation": true, "wp_font_family": true, "wp_font_face": true,
	"wp_global_styles": true,
}

func (p *Probe) line(format string, args ...any) {
	fmt.Fprintf(&p.summary, format+"\n", args...)
}

// Run carries out the whole reconnaissance and returns the Markdown summary.
func (p *Probe) Run() string {
	p.line("# WordPress REST reconnaissance")
	p.line("")
	p.line("- Site: `%s`", p.Base.String())
	p.line("- Probed: %s", time.Now().Format(time.RFC3339))
	if p.authenticated() && strings.Contains(p.User, "@") {
		// WordPress accepts an account's email address in place of its login, and
		// an address has no business in a summary that may be pasted into a
		// design discussion.
		p.line("- Credentials: supplied for an account named by its email address, not repeated here")
	} else if p.authenticated() {
		p.line("- Credentials: supplied for user `%s`", p.User)
	} else {
		p.line("- Credentials: none; only publicly readable routes were seen")
	}
	p.line("")

	index, ok := p.findREST()
	if !ok {
		p.line("**No REST API was found.** Neither `/wp-json/` nor `?rest_route=/` returned")
		p.line("a REST index, and `/wp/v2/types` was refused as well. The site may not be")
		p.line("WordPress, the REST API may be blocked by a security plugin, a firewall, or the")
		p.line("host, or the address may need a different path. The refusals below say which:")
		p.line("a WordPress refusal names itself, whereas a firewall's does not.")
		p.line("")
		for _, s := range p.steps {
			if s.Body != "" {
				p.line("- `%s` returned %d: %s", s.URL, s.Status, s.Body)
			} else if s.Err != "" {
				p.line("- `%s` failed: %s", s.URL, s.Err)
			}
		}
		p.appendSteps()
		return p.summary.String()
	}

	p.describeSite(index)
	types := p.describeTypes()
	taxa := p.describeTaxonomies(types)
	p.describeSuppliedURL(types, taxa)
	p.describeCandidates(types)
	p.describeMedia()
	p.describeAuth(types, taxa)
	p.describeGenerator()
	p.appendSteps()
	return p.summary.String()
}

// findREST locates the REST index in whichever permalink form the site uses,
// and falls back to a single known route when the index alone is refused.
func (p *Probe) findREST() (restIndex, bool) {
	for _, pretty := range []bool{true, false} {
		p.pretty = pretty
		name := "REST index (/wp-json/)"
		file := "01-index.json"
		if !pretty {
			name = "REST index (?rest_route=/)"
			file = "01-index-rest-route.json"
		}
		body, status, err := p.request(name, http.MethodGet, p.restURL("/", nil), file)
		if err != nil || status != http.StatusOK {
			continue
		}
		if index, ok := decode[restIndex](body); ok && len(index.Namespaces) > 0 {
			return index, true
		}
	}
	// Hiding the index while leaving the routes open is a common hardening
	// measure and is not the same thing as having no REST API.
	for _, pretty := range []bool{true, false} {
		p.pretty = pretty
		body, status, err := p.request("post types (index refused)", http.MethodGet, p.restURL("/wp/v2/types", nil), "01-types-fallback.json")
		if err != nil || status != http.StatusOK {
			continue
		}
		if types, ok := decode[map[string]postType](body); ok && len(types) > 0 {
			p.indexHidden = true
			return restIndex{}, true
		}
	}
	return restIndex{}, false
}

func (p *Probe) describeSite(index restIndex) {
	p.line("## Site")
	p.line("")
	if p.indexHidden {
		p.line("The REST index was refused, but `/wp/v2/types` answered, so the API is present")
		p.line("and something is hiding the index alone. Namespaces and the plugin routes they")
		p.line("would have named are therefore unknown; everything below still applies.")
		p.line("")
		return
	}
	p.line("- Name: %s", strings.TrimSpace(index.Name))
	if strings.TrimSpace(index.Description) != "" {
		p.line("- Description: %s", strings.TrimSpace(index.Description))
	}
	form := "`/wp-json/` (pretty permalinks)"
	if !p.pretty {
		form = "`?rest_route=` (plain permalinks)"
	}
	p.line("- REST root: %s", form)
	if index.TimezoneString != "" {
		p.line("- Site timezone: `%s` (dates sent without a zone are read in it)", index.TimezoneString)
	} else if index.GMTOffset != "" {
		p.line("- Site timezone: UTC%+v (no named zone set)", index.GMTOffset)
	}
	if _, ok := index.Authentication["application-passwords"]; ok {
		p.line("- Application passwords: advertised by the REST index")
	} else if len(index.Authentication) == 0 {
		p.line("- Application passwords: not advertised. They still work on WordPress 5.6")
		p.line("  and later over HTTPS, but the index not naming them can also mean a plugin")
		p.line("  has switched them off.")
	}
	p.line("")
	p.line("### Namespaces")
	p.line("")
	namespaces := append([]string(nil), index.Namespaces...)
	sort.Strings(namespaces)
	for _, ns := range namespaces {
		note := ""
		if looksLikeSermon(ns) {
			note = "  <- worth reading: a plugin route that may be the intended way in"
		}
		p.line("- `%s`%s", ns, note)
	}
	p.line("")
	interesting := make([]string, 0, 8)
	for route := range index.Routes {
		if looksLikeSermon(route) {
			interesting = append(interesting, route)
		}
	}
	if len(interesting) > 0 {
		sort.Strings(interesting)
		p.line("### Routes whose names suggest talks")
		p.line("")
		for _, route := range interesting {
			p.line("- `%s`", route)
		}
		p.line("")
	}
}

func (p *Probe) describeTypes() map[string]postType {
	body, status, err := p.request("post types", http.MethodGet, p.restURL("/wp/v2/types", nil), "02-types.json")
	if err != nil || status != http.StatusOK {
		p.line("Post types could not be read (status %d).", status)
		p.line("")
		return nil
	}
	types, ok := decode[map[string]postType](body)
	if !ok {
		return nil
	}
	slugs := make([]string, 0, len(types))
	for slug := range types {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	p.line("## Post types")
	p.line("")
	p.line("| Slug | Name | REST base | Taxonomies |")
	p.line("| --- | --- | --- | --- |")
	for _, slug := range slugs {
		t := types[slug]
		if builtinTypes[slug] && !looksLikeSermon(slug, t.Name) {
			continue
		}
		base := t.RestBase
		if base == "" {
			base = "_(none: not exposed over REST)_"
		} else {
			base = "`" + base + "`"
		}
		taxa := strings.Join(t.Taxonomies, ", ")
		if taxa == "" {
			taxa = "_(none)_"
		}
		p.line("| `%s` | %s | %s | %s |", slug, t.Name, base, taxa)
	}
	p.line("")
	p.line("Builtin types are omitted unless their name suggests talks. The full list is in")
	p.line("`02-types.json`.")
	p.line("")
	return types
}

func (p *Probe) describeTaxonomies(types map[string]postType) map[string]taxonomy {
	body, status, err := p.request("taxonomies", http.MethodGet, p.restURL("/wp/v2/taxonomies", nil), "03-taxonomies.json")
	if err != nil || status != http.StatusOK {
		return nil
	}
	taxa, ok := decode[map[string]taxonomy](body)
	if !ok {
		return nil
	}
	slugs := make([]string, 0, len(taxa))
	for slug := range taxa {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	p.line("## Taxonomies")
	p.line("")
	p.line("These are where a speaker, a series, a book of the Bible, or a church is most")
	p.line("likely to live. Each one that carries a talk type is listed with its current")
	p.line("terms, so the app can offer the real values rather than free text.")
	p.line("")
	p.line("| Slug | Name | REST base | Applies to |")
	p.line("| --- | --- | --- | --- |")
	for _, slug := range slugs {
		t := taxa[slug]
		base := t.RestBase
		if base == "" {
			base = "_(none)_"
		} else {
			base = "`" + base + "`"
		}
		p.line("| `%s` | %s | %s | %s |", slug, t.Name, base, strings.Join(t.Types, ", "))
	}
	p.line("")

	for _, slug := range slugs {
		t := taxa[slug]
		if t.RestBase == "" || !p.taxonomyTouchesCandidate(t, types) {
			continue
		}
		query := url.Values{"per_page": {"100"}, "orderby": {"name"}, "hide_empty": {"false"}}
		terms, status, err := p.request("terms: "+slug, http.MethodGet, p.restURL("/wp/v2/"+t.RestBase, query), "20-terms-"+safeName(slug)+".json")
		if err != nil || status != http.StatusOK {
			continue
		}
		listed, ok := decode[[]struct {
			ID    int    `json:"id"`
			Name  string `json:"name"`
			Slug  string `json:"slug"`
			Count int    `json:"count"`
		}](terms)
		if !ok {
			continue
		}
		p.line("### `%s` terms (%d)", slug, len(listed))
		p.line("")
		shown := listed
		if len(shown) > 25 {
			shown = shown[:25]
		}
		for _, term := range shown {
			p.line("- %d `%s` — %s (%d posts)", term.ID, term.Slug, term.Name, term.Count)
		}
		if len(listed) > len(shown) {
			p.line("- _(%d more in `20-terms-%s.json`)_", len(listed)-len(shown), safeName(slug))
		}
		p.line("")
	}
	return taxa
}

func (p *Probe) taxonomyTouchesCandidate(t taxonomy, types map[string]postType) bool {
	if looksLikeSermon(t.Slug, t.Name) {
		return true
	}
	for _, typeSlug := range t.Types {
		if candidate, ok := types[typeSlug]; ok && isCandidate(typeSlug, candidate) {
			return true
		}
	}
	return false
}

// isCandidate reports whether a post type might be the one talks are stored in.
// Every non-builtin public type qualifies: a site can perfectly well keep its
// sermons in a type called "resources".
func isCandidate(slug string, t postType) bool {
	if t.RestBase == "" {
		return false
	}
	if looksLikeSermon(slug, t.Name) {
		return true
	}
	return !builtinTypes[slug]
}

func (p *Probe) describeCandidates(types map[string]postType) {
	slugs := make([]string, 0, len(types))
	for slug, t := range types {
		if isCandidate(slug, t) || slug == "post" {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	if len(slugs) == 0 {
		return
	}
	p.line("## Candidate talk types, newest entry and write schema")
	p.line("")
	p.line("For each type below, the newest three entries and the schema of its create")
	p.line("route were fetched. The schema names every field the site will accept, which")
	p.line("is the list the app has to be able to fill in.")
	p.line("")
	for _, slug := range slugs {
		t := types[slug]
		query := url.Values{"per_page": {"3"}, "orderby": {"date"}, "order": {"desc"}, "_embed": {"1"}}
		body, status, err := p.request("newest: "+slug, http.MethodGet, p.restURL("/wp/v2/"+t.RestBase, query), "10-newest-"+safeName(slug)+".json")
		p.line("### `%s` (`/wp/v2/%s`)", slug, t.RestBase)
		p.line("")
		if err != nil || status != http.StatusOK {
			p.line("Listing returned status %d. A talk type can be readable only when signed in,", status)
			p.line("so this is not necessarily a dead end.")
			p.line("")
			continue
		}
		if entries, ok := decode[[]map[string]json.RawMessage](body); ok && len(entries) > 0 {
			keys := make([]string, 0, len(entries[0]))
			for key := range entries[0] {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			p.line("Newest entry has fields: `%s`", strings.Join(keys, "`, `"))
			if title, ok := entries[0]["title"]; ok {
				p.line("")
				p.line("Newest title: `%s`", compact(string(title)))
			}
			if date, ok := entries[0]["date"]; ok {
				p.line("Newest date: `%s`", compact(string(date)))
			}
			if meta, ok := entries[0]["meta"]; ok {
				p.line("")
				p.line("`meta` on the newest entry (custom fields exposed to REST):")
				p.line("")
				p.line("```json")
				p.line("%s", compactLong(string(meta)))
				p.line("```")
			}
		} else {
			p.line("No entries were returned, so there is no previous talk to copy defaults from")
			p.line("yet.")
		}
		p.line("")
		schema, status, err := p.request("schema: "+slug, http.MethodOptions, p.restURL("/wp/v2/"+t.RestBase, nil), "11-schema-"+safeName(slug)+".json")
		if err == nil && status == http.StatusOK {
			p.line("Create-route arguments: %s", describeArgs(schema))
			p.line("")
		}
	}
}

// describeArgs summarises the POST arguments an OPTIONS response advertises.
func describeArgs(body []byte) string {
	var options struct {
		Endpoints []struct {
			Methods []string                   `json:"methods"`
			Args    map[string]json.RawMessage `json:"args"`
		} `json:"endpoints"`
	}
	if err := json.Unmarshal(body, &options); err != nil {
		return "_(could not be read)_"
	}
	for _, endpoint := range options.Endpoints {
		post := false
		for _, method := range endpoint.Methods {
			if method == http.MethodPost {
				post = true
			}
		}
		if !post || len(endpoint.Args) == 0 {
			continue
		}
		names := make([]string, 0, len(endpoint.Args))
		for name := range endpoint.Args {
			names = append(names, name)
		}
		sort.Strings(names)
		return "`" + strings.Join(names, "`, `") + "`"
	}
	return "_(no POST route advertised; this user may not be able to create one)_"
}

func (p *Probe) describeMedia() {
	p.line("## Media")
	p.line("")
	query := url.Values{"per_page": {"1"}, "orderby": {"date"}, "order": {"desc"}}
	body, status, err := p.request("newest media", http.MethodGet, p.restURL("/wp/v2/media", query), "12-newest-media.json")
	if err == nil && status == http.StatusOK {
		if entries, ok := decode[[]map[string]json.RawMessage](body); ok && len(entries) > 0 {
			if link, ok := entries[0]["source_url"]; ok {
				p.line("- Newest attachment: `%s`", compact(string(link)))
			}
			if mime, ok := entries[0]["mime_type"]; ok {
				p.line("- Its type: `%s`", compact(string(mime)))
			}
		}
	} else {
		p.line("- The media listing returned status %d.", status)
	}
	schema, status, err := p.request("media schema", http.MethodOptions, p.restURL("/wp/v2/media", nil), "13-schema-media.json")
	if err == nil && status == http.StatusOK {
		p.line("- Upload arguments: %s", describeArgs(schema))
	}
	p.line("")
	p.line("An MP3 of a service is roughly 30-60 MB. Neither the maximum upload size nor")
	p.line("the request timeout is reported over REST: both come from PHP and the web")
	p.line("server, and the only way to learn them is to try. The prototype should do that")
	p.line("deliberately rather than discovering it during a Sunday.")
	p.line("")
}

func (p *Probe) describeAuth(types map[string]postType, taxa map[string]taxonomy) {
	p.line("## Authentication")
	p.line("")
	if !p.authenticated() {
		p.line("No credentials were supplied, so everything above is what an anonymous visitor")
		p.line("can see. Re-run with an application password to learn what may be written.")
		p.line("")
		return
	}
	body, status, err := p.request("current user", http.MethodGet, p.restURL("/wp/v2/users/me", url.Values{"context": {"edit"}}), "")
	if err != nil {
		p.line("The signed-in check failed: %v", err)
		p.line("")
		return
	}
	if status != http.StatusOK {
		p.line("Signing in returned status %d. The response body is below; a `401` with", status)
		p.line("`rest_cannot_access` usually means the site is behind HTTP rather than HTTPS,")
		p.line("or that a security plugin strips the `Authorization` header before WordPress")
		p.line("sees it.")
		p.line("")
		p.line("```json")
		p.line("%s", compactLong(string(body)))
		p.line("```")
		p.line("")
		return
	}
	var me struct {
		ID           int             `json:"id"`
		Name         string          `json:"name"`
		Slug         string          `json:"slug"`
		Roles        []string        `json:"roles"`
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		p.line("Signed in, but the response could not be read: %v", err)
		p.line("")
		return
	}
	// Only the fields that bear on publishing are recorded. The full response
	// carries the account's email address, which has no business in a file that
	// will be pasted into a design discussion.
	redacted, _ := json.Marshal(map[string]any{"id": me.ID, "name": me.Name, "slug": me.Slug, "roles": me.Roles})
	if _, err := p.save("30-me-redacted.json", redacted); err != nil {
		p.line("(Could not save the redacted account summary: %v)", err)
	}
	p.line("- Signed in as `%s` (id %d), roles: %s", me.Slug, me.ID, strings.Join(me.Roles, ", "))
	p.line("- Relevant capabilities:")
	for _, capability := range []string{"upload_files", "edit_posts", "publish_posts", "edit_published_posts", "manage_categories", "edit_others_posts"} {
		p.line("  - `%s`: %t", capability, me.Capabilities[capability])
	}
	p.line("")
	p.line("The account's email address is deliberately not saved; only the fields above are")
	p.line("in `30-me-redacted.json`.")
	p.line("")
	p.describeTermRights(types, taxa, me.Capabilities)
}

// editTaxonomy is a taxonomy as the edit context reports it, which is the only
// context that names the capabilities WordPress checks for its terms.
type editTaxonomy struct {
	Hierarchical bool              `json:"hierarchical"`
	Capabilities map[string]string `json:"capabilities"`
}

// describeTermRights reports whether the account may assign and create terms in
// each taxonomy a talk would use. A visiting speaker or a new series has to be
// created from the app, and whether that works turns on the capabilities each
// taxonomy was registered with rather than on the name of the account's role.
func (p *Probe) describeTermRights(types map[string]postType, taxa map[string]taxonomy, held map[string]bool) {
	p.line("### Terms this account may assign and create")
	p.line("")
	body, status, err := p.request("taxonomies (edit context)", http.MethodGet,
		p.restURL("/wp/v2/taxonomies", url.Values{"context": {"edit"}}), "31-taxonomies-edit.json")
	if err != nil || status != http.StatusOK {
		// WordPress refuses the whole listing when the account may assign terms
		// in no taxonomy at all, so a refusal here is itself an answer.
		p.line("The edit-context taxonomy listing returned status %d, which WordPress does", status)
		p.line("when the account may assign terms in no taxonomy at all.")
		p.line("")
		return
	}
	listed, ok := decode[map[string]editTaxonomy](body)
	if !ok {
		p.line("The edit-context taxonomy listing could not be read.")
		p.line("")
		return
	}
	slugs := make([]string, 0, len(taxa))
	for slug, t := range taxa {
		if t.RestBase != "" && p.taxonomyTouchesCandidate(t, types) {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)

	p.line("WordPress lists a taxonomy in the edit context only when the account holds its")
	p.line("`assign_terms` capability. Over REST it lets a term be created with `assign_terms`")
	p.line("in a flat, tag-like taxonomy, and with `edit_terms` in a hierarchical one.")
	p.line("")
	p.line("| Taxonomy | Hierarchical | Assign terms | Create terms |")
	p.line("| --- | --- | --- | --- |")
	hierarchical := false
	for _, slug := range slugs {
		t, ok := listed[slug]
		if !ok {
			p.line("| `%s` | ? | no | no |", slug)
			continue
		}
		assign := t.Capabilities["assign_terms"]
		if !t.Hierarchical {
			p.line("| `%s` | no | yes (`%s`) | yes (`assign_terms`: `%s`) |", slug, assign, assign)
			continue
		}
		hierarchical = true
		edit := t.Capabilities["edit_terms"]
		p.line("| `%s` | yes | yes (`%s`) | %s (`edit_terms`: `%s`) |", slug, assign, yesNo(held[edit]), edit)
	}
	p.line("")
	if hierarchical {
		p.line("For a hierarchical taxonomy, creating a term is read from the account's listed")
		p.line("capabilities, which leave out any that a plugin grants only when asked.")
		p.line("")
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

var generatorPattern = regexp.MustCompile(`(?i)<meta[^>]+name=["']generator["'][^>]+content=["']([^"']+)["']`)

func (p *Probe) describeGenerator() {
	body, status, err := p.request("home page", http.MethodGet, p.Base.String(), "")
	if err != nil || status != http.StatusOK {
		return
	}
	matches := generatorPattern.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		return
	}
	p.line("## Versions advertised by the home page")
	p.line("")
	for _, match := range matches {
		p.line("- %s", match[1])
	}
	p.line("")
	p.line("Many sites remove these tags, so their absence means nothing.")
	p.line("")
}

func (p *Probe) appendSteps() {
	p.line("## Requests made")
	p.line("")
	p.line("| Request | Method | Status | Saved as |")
	p.line("| --- | --- | --- | --- |")
	for _, s := range p.steps {
		status := fmt.Sprintf("%d", s.Status)
		if s.Err != "" {
			status = "error: " + s.Err
		} else if s.Body != "" {
			status = fmt.Sprintf("%d — %s", s.Status, s.Body)
		}
		file := filepath.Base(s.File)
		if s.File == "" {
			file = "_(not saved)_"
		}
		p.line("| %s | %s | %s | %s |", s.Name, s.Method, status, file)
	}
	p.line("")
	p.line("Every request was a read. Nothing on the site was created, changed, or deleted.")
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeName(s string) string {
	cleaned := unsafeName.ReplaceAllString(s, "-")
	return strings.Trim(cleaned, "-")
}

// compact reduces a JSON fragment to a single short line for a summary table.
func compact(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return strings.ReplaceAll(s, "`", "'")
}

// compactLong keeps more of a fragment, for a fenced block rather than a table.
func compactLong(s string) string {
	if len(s) > 4000 {
		return s[:4000] + "\n... truncated; see the saved file"
	}
	return s
}

// describeSuppliedURL works backwards from a page the operator already knows
// about. Two things are wanted from it. The first is the REST object behind a
// talk's permalink, which settles what post type talks are in and what fields
// one actually carries, without guessing from names. The second is the meaning
// of any filter parameters on the address: a facet plugin puts an opaque
// parameter such as `_resource_bible_book_2_2` in the query string, and its
// value is a taxonomy term slug, so looking the slug up across the taxonomies
// names the taxonomy the app would have to set.
func (p *Probe) describeSuppliedURL(types map[string]postType, taxa map[string]taxonomy) {
	if p.TalkURL == nil {
		return
	}
	p.line("## The supplied page")
	p.line("")
	p.line("- Address: `%s`", p.TalkURL.String())
	p.line("")

	p.resolveFilterParameters(taxa)
	p.resolvePermalink(types)
}

// resolveFilterParameters looks up each filter value as a term slug in every
// taxonomy, so an opaque facet parameter is traced to the taxonomy behind it.
func (p *Probe) resolveFilterParameters(taxa map[string]taxonomy) {
	query := p.TalkURL.Query()
	if len(query) == 0 {
		return
	}
	names := make([]string, 0, len(query))
	for name := range query {
		names = append(names, name)
	}
	sort.Strings(names)

	p.line("### Filter parameters")
	p.line("")
	p.line("A facet plugin names its parameters after the facet rather than the taxonomy, so")
	p.line("the parameter name proves nothing. Each value is looked up as a term slug in")
	p.line("every taxonomy below; the taxonomy it is found in is the one the app must set.")
	p.line("")
	for _, name := range names {
		for _, value := range query[name] {
			if strings.TrimSpace(value) == "" {
				continue
			}
			p.line("- `%s` = `%s`", name, value)
			found := false
			for slug, t := range taxa {
				if t.RestBase == "" {
					continue
				}
				body, status, err := p.request("term lookup: "+slug+"/"+value, http.MethodGet,
					p.restURL("/wp/v2/"+t.RestBase, url.Values{"slug": {value}}), "")
				if err != nil || status != http.StatusOK {
					continue
				}
				terms, ok := decode[[]struct {
					ID   int    `json:"id"`
					Name string `json:"name"`
					Slug string `json:"slug"`
				}](body)
				if !ok || len(terms) == 0 {
					continue
				}
				found = true
				p.line("  - found in taxonomy `%s` as term %d, %q", slug, terms[0].ID, terms[0].Name)
			}
			if !found {
				p.line("  - not a term in any taxonomy readable here. It may be a custom field, a")
				p.line("    search string, or a taxonomy this account cannot see.")
			}
		}
	}
	p.line("")
}

// resolvePermalink finds the REST object behind the supplied address.
func (p *Probe) resolvePermalink(types map[string]postType) {
	slug := ""
	for _, part := range strings.Split(strings.Trim(p.TalkURL.Path, "/"), "/") {
		if part != "" {
			slug = part
		}
	}
	if slug == "" {
		return
	}
	p.line("### The page itself")
	p.line("")
	p.line("Last path segment, used as the slug: `%s`", slug)
	p.line("")

	// The core search route reports the post type of anything searchable, which
	// is the quickest way to learn where a talk lives.
	query := url.Values{"search": {slug}, "per_page": {"5"}}
	if body, status, err := p.request("search for the slug", http.MethodGet, p.restURL("/wp/v2/search", query), "14-search.json"); err == nil && status == http.StatusOK {
		results, ok := decode[[]struct {
			ID      int    `json:"id"`
			Title   string `json:"title"`
			URL     string `json:"url"`
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
		}](body)
		if ok && len(results) > 0 {
			p.line("The search route matched:")
			p.line("")
			for _, result := range results {
				marker := ""
				if sameAddress(result.URL, p.TalkURL) {
					marker = "  <- this is the supplied page"
				}
				p.line("- %d `%s` (%s/%s): %s%s", result.ID, result.Subtype, result.Type, result.Subtype, result.Title, marker)
			}
			p.line("")
		}
	}

	// Whether or not search answered, ask each candidate type for the slug
	// directly: search can be narrowed or switched off, and a direct hit also
	// brings back the full object with its fields.
	for typeSlug, t := range types {
		if !isCandidate(typeSlug, t) && typeSlug != "post" && typeSlug != "page" {
			continue
		}
		body, status, err := p.request("slug in "+typeSlug, http.MethodGet,
			p.restURL("/wp/v2/"+t.RestBase, url.Values{"slug": {slug}, "_embed": {"1"}}), "")
		if err != nil || status != http.StatusOK {
			continue
		}
		entries, ok := decode[[]map[string]json.RawMessage](body)
		if !ok || len(entries) == 0 {
			continue
		}
		if _, err := p.save("15-page-"+safeName(typeSlug)+".json", body); err != nil {
			p.line("(Could not save the matched object: %v)", err)
		}
		keys := make([]string, 0, len(entries[0]))
		for key := range entries[0] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		p.line("Found in post type `%s`. Its fields: `%s`", typeSlug, strings.Join(keys, "`, `"))
		p.line("")
		p.line("Saved in full as `15-page-%s.json`. This is the object an upload would have to", safeName(typeSlug))
		p.line("produce, so it is the field list the app has to be able to fill in.")
		p.line("")
		return
	}
	p.line("No post type readable here has that slug. If the address is an archive or a")
	p.line("filtered listing rather than one talk, supply the address of a single talk")
	p.line("instead: that is the object the upload has to create.")
	p.line("")
}

// sameAddress compares two addresses ignoring scheme, a leading www, a trailing
// slash, and any query, so that a permalink reported by the site matches the one
// the operator pasted.
func sameAddress(reported string, supplied *url.URL) bool {
	parsed, err := url.Parse(reported)
	if err != nil {
		return false
	}
	normalise := func(u *url.URL) string {
		host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
		return host + "/" + strings.Trim(u.Path, "/")
	}
	return normalise(parsed) == normalise(supplied)
}
