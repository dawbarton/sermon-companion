// Tests for the WordPress REST client: that it signs every request, refuses to
// send credentials anywhere but HTTPS, streams uploads with the headers
// WordPress's media route needs, and classifies each likely refusal.
package publish

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	testUser     = "uploads@example.org"
	testPassword = "abcd EFGH ijkl MNOP qrst UVWX"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, testUser, testPassword, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client, server
}

func TestNewClientRefusesAnythingButHTTPS(t *testing.T) {
	for _, address := range []string{"http://example.org", "ftp://example.org", "example.org", ""} {
		if _, err := NewClient(address, testUser, testPassword, nil); err == nil {
			t.Errorf("NewClient(%q) accepted an address an application password must not be sent to", address)
		}
	}
	if _, err := NewClient("https://example.org", testUser, "  ", nil); err == nil {
		t.Error("NewClient accepted an empty application password")
	}
}

func TestMeSignsTheRequestAndDecodesTheAccount(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != testUser || pass != testPassword {
			t.Errorf("credentials = %q, %q, %v", user, pass, ok)
		}
		if r.URL.Path != "/wp-json/wp/v2/users/me" || r.URL.Query().Get("context") != "edit" {
			t.Errorf("request = %s", r.URL)
		}
		w.Write([]byte(`{"id":8,"slug":"uploads","roles":["author"],"capabilities":{"upload_files":true}}`))
	})
	account, err := client.Me(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.ID != 8 || account.Roles[0] != "author" || !account.Capabilities["upload_files"] {
		t.Errorf("account = %+v", account)
	}
}

func TestSendJSONSendsTheMethodAndBody(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || string(body) != `{"title":"The Lost Sheep"}` {
			t.Errorf("method %s, type %q, body %s", r.Method, r.Header.Get("Content-Type"), body)
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":42}`))
	})
	var created struct{ ID int }
	if err := client.SendJSON(context.Background(), "creating the talk", http.MethodPost, "/wp/v2/resource", nil, map[string]string{"title": "The Lost Sheep"}, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != 42 {
		t.Errorf("created = %+v", created)
	}
}

func TestUploadFileStreamsTheBodyWithItsLengthAndName(t *testing.T) {
	content := strings.Repeat("ID3", 1000)
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Header.Get("Content-Type") != "audio/mpeg":
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		case r.Header.Get("Content-Disposition") != `attachment; filename="2026-09-13-Emmanuel-Bishopston.mp3"`:
			t.Errorf("Content-Disposition = %q", r.Header.Get("Content-Disposition"))
		case r.ContentLength != int64(len(content)) || len(r.TransferEncoding) != 0:
			t.Errorf("length %d, transfer encoding %v: the length should be declared, not chunked", r.ContentLength, r.TransferEncoding)
		case string(body) != content:
			t.Error("the body arrived altered")
		case r.URL.Query().Get("post") != "42":
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":7}`))
	})
	// A plain io.Reader, as a file would be, so that the length cannot be
	// inferred by net/http and has to be declared.
	reader := io.MultiReader(strings.NewReader(content))
	var media struct{ ID int }
	err := client.UploadFile(context.Background(), "uploading the MP3", "/wp/v2/media", url.Values{"post": {"42"}},
		"2026-09-13-Emmanuel-Bishopston.mp3", "audio/mpeg", reader, int64(len(content)), &media)
	if err != nil {
		t.Fatal(err)
	}
	if media.ID != 7 {
		t.Errorf("media = %+v", media)
	}
}

func TestAttachmentDispositionSuitsWordPresssParser(t *testing.T) {
	for _, test := range []struct{ name, want string }{
		{"2026-09-13-Emmanuel-Bishopston.mp3", `attachment; filename="2026-09-13-Emmanuel-Bishopston.mp3"`},
		// WordPress splits on semicolons and ignores filename*, so neither a
		// semicolon nor an accented letter may reach it.
		{`2026-09-13-Saint-Étienne;"x".mp3`, `attachment; filename="2026-09-13-Saint-tienne-x.mp3"`},
	} {
		got, err := attachmentDisposition(test.name)
		if err != nil || got != test.want {
			t.Errorf("attachmentDisposition(%q) = %q, %v; want %q", test.name, got, err, test.want)
		}
	}
	for _, name := range []string{"", "Église", ";;;"} {
		if _, err := attachmentDisposition(name); err == nil {
			t.Errorf("attachmentDisposition(%q) accepted a name with nothing usable in it", name)
		}
	}
}

func TestRefusalsAreClassifiedByWhatWouldHaveToBeDone(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   Kind
	}{
		{"credentials never checked", 401, `{"code":"rest_not_logged_in","message":"You are not currently logged in."}`, KindSignInIgnored},
		{"anonymous create refused", 401, `{"code":"rest_cannot_create","message":"Sorry, you are not allowed to create posts as this user."}`, KindSignInIgnored},
		{"wrong password", 401, `{"code":"incorrect_password","message":"The provided password is an invalid application password."}`, KindBadCredentials},
		{"unknown user", 401, `{"code":"invalid_email","message":"Unknown email address."}`, KindBadCredentials},
		{"signed in but not allowed", 403, `{"code":"rest_cannot_create","message":"Sorry, you are not allowed to create terms."}`, KindForbidden},
		{"post deleted", 404, `{"code":"rest_post_invalid_id","message":"Invalid post ID."}`, KindNotFound},
		{"route missing", 404, `{"code":"rest_no_route","message":"No route was found."}`, KindSite},
		{"web server limit", 413, `<html><body>413 Request Entity Too Large</body></html>`, KindTooLarge},
		{"PHP body limit", 400, `{"code":"rest_upload_no_data","message":"No data supplied."}`, KindTooLarge},
		{"anything else", 500, `{"code":"db_error","message":"Could not save &amp; retry."}`, KindSite},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				w.Write([]byte(test.body))
			})
			_, err := client.Get(context.Background(), "reading the talk", "/wp/v2/resource/42", nil, nil)
			if !IsKind(err, test.want) {
				t.Fatalf("error = %v, want kind %d", err, test.want)
			}
			if strings.Contains(err.Error(), testPassword) || strings.Contains(err.Error(), "abcd") {
				t.Errorf("the error carries the application password: %v", err)
			}
			if !strings.HasPrefix(err.Error(), "reading the talk failed: ") {
				t.Errorf("the error should say what was being attempted: %v", err)
			}
		})
	}
}

func TestWordPressMessageIsShownUnescaped(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"code":"db_error","message":"Could not save &amp; retry."}`))
	})
	_, err := client.Get(context.Background(), "reading the talk", "/wp/v2/resource/42", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Could not save & retry.") {
		t.Errorf("error = %v", err)
	}
}

func TestRejectedParametersAreNamed(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"code":"rest_invalid_param","message":"Invalid parameter(s): acf","data":{"status":400,"params":{"acf":"resource_type is a required property of acf."}}}`))
	})
	_, err := client.Get(context.Background(), "finishing the talk", "/wp/v2/resource/500", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "acf: resource_type is a required property of acf.") {
		t.Errorf("the rejected field and its reason should be shown: %v", err)
	}
}

func TestRedirectAwayFromHTTPSIsRefused(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/wp-json/wp/v2/users/me", http.StatusFound)
	})
	_, err := client.Me(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not HTTPS") {
		t.Errorf("a redirect to plain HTTP should be refused before the credentials follow it: %v", err)
	}
}

func TestTimeoutIsReportedAsATimeout(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.Me(ctx)
	if !IsKind(err, KindTimeout) {
		t.Errorf("error = %v, want a timeout", err)
	}
	var refusal *Error
	if !errors.As(err, &refusal) || !errors.Is(refusal, context.DeadlineExceeded) {
		t.Errorf("the underlying deadline should still be reachable: %v", err)
	}
}
