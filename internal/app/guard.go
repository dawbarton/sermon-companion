package app

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// refuseOtherSites keeps web pages elsewhere from using the local API through
// the operator's browser. Listening on loopback keeps other computers out, but
// not a page open in a browser on this one: a page can send a form-style POST
// to 127.0.0.1 without asking first, which is enough to stop a recording or
// change the capture device in the middle of a service. Two things are
// therefore checked.
//
// A request that changes something and says which page sent it must come from
// a page this server served. Browsers name the sending page in Origin on every
// cross-site POST, PATCH, and DELETE; the dock, the review pages, and the OBS
// script either send this server's own origin or none at all.
//
// The Host a request was addressed to must be this computer's: an IP address,
// localhost, or the host in the listen setting. A page whose own domain name is
// made to resolve to 127.0.0.1 after it has loaded (DNS rebinding) is otherwise
// the same origin as itself, and could read the API as well as change it.
func refuseOtherSites(listen string, next http.Handler) http.Handler {
	listenHost, _, err := net.SplitHostPort(listen)
	if err != nil {
		listenHost = ""
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !addressedToThisComputer(r.Host, listenHost) {
			writeError(w, http.StatusForbidden, errors.New("requests must be addressed to this computer, such as http://127.0.0.1:8765"))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !fromThisServer(r) {
			writeError(w, http.StatusForbidden, errors.New("requests from other websites are refused"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func addressedToThisComputer(hostPort, listenHost string) bool {
	host := hostPort
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	switch {
	case host == "":
		// HTTP/1.0 requests may carry no Host at all; no browser sends one.
		return true
	case net.ParseIP(host) != nil, strings.EqualFold(host, "localhost"):
		return true
	case listenHost != "" && strings.EqualFold(host, strings.Trim(listenHost, "[]")):
		return true
	}
	return false
}

func fromThisServer(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == "http" && strings.EqualFold(parsed.Host, r.Host)
}
