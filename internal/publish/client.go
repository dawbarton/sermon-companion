// Package publish uploads a finished talk to a church's WordPress site through
// the site's REST API: the talk itself, its terms and custom fields, and the
// MP3.
//
// This file holds the HTTP client and the classification of the site's
// refusals. A failed upload is reported to someone who may upload only once
// every month or two, so a refusal is turned into the one of a few likely
// problems it represents, rather than shown as a bare status code.
package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const (
	userAgent = "sermon-companion-publish/1"

	// maxErrorBody bounds what is read of a refusal. WordPress's own refusals
	// are a few hundred bytes; anything longer is a web server's error page.
	maxErrorBody = 64 << 10

	// maxReplyBody bounds a successful reply. A page of 100 terms with their
	// SEO metadata runs to about a megabyte.
	maxReplyBody = 32 << 20
)

// Client talks to one WordPress site as one account.
type Client struct {
	base *url.URL
	user string
	pass string
	http *http.Client
}

// NewClient returns a client for the site at address, signing in as user with
// an application password. The address must use HTTPS: WordPress refuses
// application passwords over plain HTTP, so sending one would expose it for
// nothing. httpClient may be nil; it is copied rather than modified.
func NewClient(address, user, password string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("could not read %q as a website address", address)
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("the website address must start with https://, not %s://", parsed.Scheme)
	}
	if strings.TrimSpace(user) == "" || strings.TrimSpace(password) == "" {
		return nil, errors.New("a WordPress user and an application password are both required")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawQuery, parsed.Fragment = "", ""

	if httpClient == nil {
		httpClient = &http.Client{}
	}
	copied := *httpClient
	copied.CheckRedirect = refuseInsecureRedirect
	return &Client{base: parsed, user: user, pass: password, http: &copied}, nil
}

// refuseInsecureRedirect stops a redirect from carrying a request, and with it
// the credentials, anywhere but HTTPS. Go already drops the Authorization
// header when a redirect leaves the original host.
func refuseInsecureRedirect(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("the website redirected to %s, which is not HTTPS", req.URL.Redacted())
	}
	if len(via) >= 10 {
		return errors.New("the website redirected too many times")
	}
	return nil
}

// Account is the signed-in user, as far as publishing is concerned.
type Account struct {
	ID           int             `json:"id"`
	Name         string          `json:"name"`
	Slug         string          `json:"slug"`
	Roles        []string        `json:"roles"`
	Capabilities map[string]bool `json:"capabilities"`
}

// Me returns the account the application password belongs to. It is the
// cheapest request that proves the sign-in works.
func (c *Client) Me(ctx context.Context) (Account, error) {
	var account Account
	_, err := c.Get(ctx, "checking the sign-in", "/wp/v2/users/me", url.Values{"context": {"edit"}}, &account)
	return account, err
}

// Get fetches a route and decodes its JSON reply into out. The reply's headers
// are returned because WordPress reports paging in them.
func (c *Client) Get(ctx context.Context, op, route string, query url.Values, out any) (http.Header, error) {
	return c.do(ctx, op, http.MethodGet, route, query, outgoing{}, out)
}

// SendJSON sends payload as JSON with the given method and decodes the reply.
func (c *Client) SendJSON(ctx context.Context, op, method, route string, query url.Values, payload, out any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return &Error{Op: op, Kind: KindSite, Message: "could not encode the request: " + err.Error(), Err: err}
	}
	body := outgoing{
		body:   strings.NewReader(string(encoded)),
		length: int64(len(encoded)),
		header: http.Header{"Content-Type": {"application/json"}},
	}
	_, err = c.do(ctx, op, method, route, query, body, out)
	return err
}

// UploadFile sends content as the raw body of a POST, which is how WordPress's
// media route takes a file. The length is declared rather than left to chunked
// encoding, which some PHP set-ups do not accept, and the file is streamed
// rather than held in memory.
func (c *Client) UploadFile(ctx context.Context, op, route string, query url.Values, filename, contentType string, content io.Reader, size int64, out any) error {
	disposition, err := attachmentDisposition(filename)
	if err != nil {
		return &Error{Op: op, Kind: KindSite, Message: err.Error(), Err: err}
	}
	body := outgoing{
		body:   content,
		length: size,
		header: http.Header{
			"Content-Type":        {contentType},
			"Content-Disposition": {disposition},
		},
	}
	_, err = c.do(ctx, op, http.MethodPost, route, query, body, out)
	return err
}

// attachmentDisposition writes the Content-Disposition header WordPress's media
// route reads the file name from. Its parser splits on semicolons, strips one
// pair of quotes, and does not understand RFC 2231's filename*, so the name is
// reduced to ASCII letters, digits, dots, hyphens, and underscores before it is
// quoted. Exports already fit, except where a church name has accented
// letters; those become hyphens in the name the site gives the file.
func attachmentDisposition(filename string) (string, error) {
	var safe strings.Builder
	hyphen := false
	for _, r := range filename {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
			safe.WriteRune(r)
			hyphen = false
		default:
			if !hyphen && safe.Len() > 0 {
				safe.WriteByte('-')
			}
			hyphen = true
		}
	}
	name := strings.Trim(strings.ReplaceAll(safe.String(), "-.", "."), "-.")
	if name == "" || !strings.Contains(name, ".") {
		return "", fmt.Errorf("%q cannot be given to WordPress as a file name", filename)
	}
	return `attachment; filename="` + name + `"`, nil
}

// outgoing is a request body together with what has to be said about it.
type outgoing struct {
	body   io.Reader
	length int64
	header http.Header
}

func (c *Client) endpoint(route string, query url.Values) string {
	u := *c.base
	u.Path = c.base.Path + "/wp-json" + route
	u.RawQuery = query.Encode()
	return u.String()
}

func (c *Client) do(ctx context.Context, op, method, route string, query url.Values, send outgoing, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(route, query), send.body)
	if err != nil {
		return nil, &Error{Op: op, Kind: KindSite, Message: err.Error(), Err: err}
	}
	if send.body != nil {
		req.ContentLength = send.length
	}
	for name, values := range send.header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.SetBasicAuth(c.user, c.pass)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(op, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		refusal, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return resp.Header, classify(op, resp.StatusCode, refusal)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxReplyBody))
		return resp.Header, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReplyBody)).Decode(out); err != nil {
		return resp.Header, &Error{
			Op: op, Kind: KindSite, Status: resp.StatusCode,
			Message: "the reply was not the JSON WordPress sends: " + err.Error(), Err: err,
		}
	}
	return resp.Header, nil
}

// Kind classifies a refusal by what the operator, or whoever looks after the
// site, would have to do about it.
type Kind int

const (
	// KindSite is any other refusal; WordPress's own message is shown.
	KindSite Kind = iota
	// KindSignInIgnored means WordPress treated the request as anonymous, so
	// the application password was never checked.
	KindSignInIgnored
	// KindBadCredentials means WordPress checked the user and application
	// password and rejected them.
	KindBadCredentials
	// KindForbidden means the account signed in but may not do this.
	KindForbidden
	// KindNotFound means the post, attachment, or term no longer exists.
	KindNotFound
	// KindTooLarge means the file is over what the site accepts.
	KindTooLarge
	// KindTimeout means the request did not finish in time.
	KindTimeout
	// KindNetwork means the site could not be reached at all.
	KindNetwork
)

func (k Kind) advice() string {
	switch k {
	case KindSignInIgnored:
		return "the website treated the request as anonymous, so it never checked the application password. " +
			"Something on the site has stopped application passwords working; on a site running Defender, " +
			"the WPCode snippet described in docs/SERMON-UPLOAD-DESIGN.md is what makes them work"
	case KindBadCredentials:
		return "the website did not accept the user name and application password"
	case KindForbidden:
		return "the account signed in but is not allowed to do this"
	case KindNotFound:
		return "the website no longer has it"
	case KindTooLarge:
		return "the file is larger than the website accepts"
	case KindTimeout:
		return "the website did not answer in time"
	case KindNetwork:
		return "the website could not be reached"
	default:
		return "the website refused"
	}
}

// Error is a request the site refused or that never completed. It never
// carries the application password: only the operation, the status, and what
// WordPress or the network said.
type Error struct {
	Op      string // what was being attempted, such as "creating the talk"
	Kind    Kind
	Status  int    // HTTP status; zero when no reply arrived
	Code    string // WordPress's error code, such as rest_cannot_create
	Message string // WordPress's message, or the network error
	Err     error  // the underlying error, when there was one
}

func (e *Error) Error() string {
	details := make([]string, 0, 3)
	if e.Status != 0 {
		details = append(details, fmt.Sprintf("HTTP %d", e.Status))
	}
	if e.Code != "" {
		details = append(details, e.Code)
	}
	if e.Message != "" {
		details = append(details, e.Message)
	}
	message := e.Op + " failed: " + e.Kind.advice()
	if len(details) > 0 {
		message += " (" + strings.Join(details, "; ") + ")"
	}
	return message
}

func (e *Error) Unwrap() error { return e.Err }

// IsKind reports whether err is a refusal of the given kind.
func IsKind(err error, kind Kind) bool {
	var refusal *Error
	return errors.As(err, &refusal) && refusal.Kind == kind
}

// credentialCodes are the codes WordPress gives when it did check the
// application password and rejected it, or the account cannot use one.
var credentialCodes = map[string]bool{
	"incorrect_password":                      true,
	"invalid_username":                        true,
	"invalid_email":                           true,
	"application_passwords_disabled":          true,
	"application_passwords_disabled_for_user": true,
}

// classify turns a refusal into an Error. Every request this client sends
// carries credentials, so a 401 that does not name a credential problem means
// WordPress never got as far as checking them: it answers 401 only when nobody
// is logged in, and 403 when someone is but may not do the thing asked.
func classify(op string, status int, body []byte) *Error {
	var reply struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	// A web server's own error page is not JSON; the status alone then speaks.
	_ = json.Unmarshal(body, &reply)
	refusal := &Error{Op: op, Status: status, Code: reply.Code, Message: html.UnescapeString(reply.Message)}

	switch {
	case credentialCodes[reply.Code]:
		refusal.Kind = KindBadCredentials
	case status == http.StatusUnauthorized:
		refusal.Kind = KindSignInIgnored
	case status == http.StatusRequestEntityTooLarge, reply.Code == "rest_upload_file_too_big":
		refusal.Kind = KindTooLarge
	case reply.Code == "rest_upload_no_data":
		// PHP discards a request body over post_max_size before WordPress sees
		// it, so an upload over that limit arrives empty. This is recalled
		// rather than checked against PHP's source.
		refusal.Kind = KindTooLarge
	case status == http.StatusForbidden:
		refusal.Kind = KindForbidden
	case reply.Code == "rest_no_route":
		// The route itself is missing, for instance because the plugin that
		// registered it was removed. That is not the same as a deleted post.
		refusal.Kind = KindSite
	case status == http.StatusNotFound, status == http.StatusGone:
		refusal.Kind = KindNotFound
	default:
		refusal.Kind = KindSite
	}
	return refusal
}

func transportError(op string, err error) *Error {
	kind := KindNetwork
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		kind = KindTimeout
	}
	return &Error{Op: op, Kind: kind, Message: err.Error(), Err: err}
}
