package review

// The local access layer (standards/API.md, "The local access layer"): the
// host check, the token beyond loopback, and the checks on every POST.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Access admits requests to a server and names their reviewer.
type Access struct {
	// Reviewer is the server's one reviewer.
	Reviewer string
	// Token, when set, is what clients other than loopback need.
	Token string
	// Port is the server's port, naming its cookie.
	Port int

	hosts map[string]bool
}

// NewAccess admits requests addressed to loopback or to the hosts given.
func NewAccess(reviewer, token string, port int, hosts []string) *Access {
	a := &Access{Reviewer: reviewer, Token: token, Port: port, hosts: map[string]bool{}}
	for _, h := range append([]string{"127.0.0.1", "localhost", "::1"}, hosts...) {
		a.hosts[hostKey(h)] = true
	}
	return a
}

// NewToken is a random launch token: 32 bytes, in hex.
func NewToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// hostKey is one spelling per host: lower case, and an IPv6 address
// without brackets in its shortest form.
func hostKey(name string) string {
	name = strings.ToLower(name)
	name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
	if strings.Contains(name, ":") {
		if addr, err := netip.ParseAddr(name); err == nil {
			return addr.String()
		}
	}
	return name
}

var hostHeader = regexp.MustCompile(`^(?:\[([0-9A-Fa-f:.]+)\]|([^:\[\]]+))(?::[0-9]+)?$`)

// HostAllowed reports whether a Host header names this machine as the
// server knows it.
func (a *Access) HostAllowed(host string) bool {
	m := hostHeader.FindStringSubmatch(host)
	if m == nil {
		return false
	}
	return a.hosts[hostKey(m[1]+m[2])]
}

// CookieName is the server's cookie: browsers share cookies across ports,
// so two servers need two names or each logs the other out.
func (a *Access) CookieName() string { return "rh_token_" + strconv.Itoa(a.Port) }

// Wrap puts the layer in front of next: the host check keeps out pages
// that rebind DNS; then, with a token, a client other than loopback needs
// it, in the URL once (traded for a cookie) or in that cookie.
func (a *Access) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.HostAllowed(r.Host) {
			writeError(w, http.StatusForbidden, "Requests must address 127.0.0.1, localhost or an allowed host")
			return
		}
		if a.Token == "" || isLoopback(r) {
			next.ServeHTTP(w, r)
			return
		}
		if a.sameToken(r.URL.Query().Get("token")) {
			a.login(w)
			return
		}
		if c, err := r.Cookie(a.CookieName()); err == nil && a.sameToken(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/plain; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("This review server needs a token from another machine.\n" +
			"Open the URL review-helper printed when it started, the one ending in ?token=...;\n" +
			"it sets a cookie so this browser can use the page.\n"))
	})
}

// sameToken compares in time that depends only on the length.
func (a *Access) sameToken(given string) bool {
	return given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(a.Token)) == 1
}

// login trades the token in the URL for a cookie and sends the browser to
// the page, dropping the token from the address bar and history. Nothing
// from the request reaches Location.
func (a *Access) login(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.CookieName(),
		Value:    a.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	h := w.Header()
	h.Set("Location", "/")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusSeeOther)
}

// isLoopback reports whether the client is on this machine's loopback.
func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}

// post checks a POST before its handler: a JSON body, which a form on
// another site cannot send without a CORS preflight this server never
// grants, and no Origin but the server's own.
func post(h handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ct, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
		if !strings.EqualFold(strings.TrimSpace(ct), "application/json") {
			return &apiError{http.StatusUnsupportedMediaType, "POST bodies must be application/json"}
		}
		if origin, ok := r.Header["Origin"]; ok && (len(origin) != 1 || !strings.EqualFold(origin[0], "http://"+r.Host)) {
			return forbidden("Cross-origin requests are refused")
		}
		return h(w, r)
	}
}
