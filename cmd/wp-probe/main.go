// Command wp-probe reads a WordPress site's REST API and reports what an
// automatic sermon upload would have to work with: the post type talks are
// stored in, the taxonomies that hold a speaker or a series, the fields the
// site will accept, and whether a given application password can write.
//
// It is a design instrument, not part of Sermon Companion. It makes only read
// requests, and it is deliberately a separate command so that the church
// computer's recording path stays untouched while the website interface is
// being worked out.
//
// Usage:
//
//	WP_APP_PASSWORD='xxxx xxxx xxxx xxxx xxxx xxxx' \
//	  go run ./cmd/wp-probe -site https://example.org -user david -out work/wp-probe
//
// The password is taken from the environment, or from a file named by
// -password-file, so that it does not appear in the shell history or in the
// process list. Supplying no credentials at all is useful and safe: most of
// what the design needs is publicly readable.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	site := flag.String("site", "", "address of the WordPress site, for example https://example.org")
	user := flag.String("user", "", "WordPress username the application password belongs to")
	passwordFile := flag.String("password-file", "", "file holding the application password; the WP_APP_PASSWORD environment variable is used when this is empty")
	out := flag.String("out", filepath.Join("work", "wp-probe"), "directory the responses and the summary are written to")
	timeout := flag.Duration("timeout", 30*time.Second, "timeout for each request")
	flag.Parse()

	if strings.TrimSpace(*site) == "" {
		fmt.Fprintln(os.Stderr, "wp-probe: -site is required")
		flag.Usage()
		os.Exit(2)
	}
	base, err := parseSite(*site)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wp-probe: %v\n", err)
		os.Exit(2)
	}
	password, err := readPassword(*passwordFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wp-probe: %v\n", err)
		os.Exit(2)
	}
	if password != "" && *user == "" {
		fmt.Fprintln(os.Stderr, "wp-probe: a password was supplied without -user")
		os.Exit(2)
	}
	if password != "" && base.Scheme != "https" {
		// WordPress refuses application passwords over plain HTTP, and sending
		// one anyway would put it on the wire for nothing.
		fmt.Fprintln(os.Stderr, "wp-probe: refusing to send an application password over plain HTTP; use https")
		os.Exit(2)
	}

	probe := &Probe{
		Base:   base,
		User:   *user,
		Pass:   password,
		OutDir: *out,
		HTTP:   &http.Client{Timeout: *timeout},
	}
	summary := probe.Run()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "wp-probe: %v\n", err)
		os.Exit(1)
	}
	path := filepath.Join(*out, "SUMMARY.md")
	if err := os.WriteFile(path, []byte(summary), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "wp-probe: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(summary)
	fmt.Fprintf(os.Stderr, "\nwp-probe: summary written to %s\n", path)
}

// parseSite accepts an address with or without a scheme and returns the site
// root. A bare host is assumed to be HTTPS, because an application password
// may not travel over anything else.
func parseSite(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("could not read %q as an address: %w", raw, err)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("no host in %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

// readPassword takes the application password from a file when one is named
// and from the environment otherwise. WordPress displays the password in
// space-separated groups; the spaces are part of how it is shown rather than
// part of the secret, but WordPress accepts it either way, so only surrounding
// whitespace is removed.
func readPassword(file string) (string, error) {
	if strings.TrimSpace(file) != "" {
		content, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("could not read the password file: %w", err)
		}
		return strings.TrimSpace(string(content)), nil
	}
	return strings.TrimSpace(os.Getenv("WP_APP_PASSWORD")), nil
}
