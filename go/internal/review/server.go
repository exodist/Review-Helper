package review

// The HTTP API (standards/API.md): the routes the review page uses, served
// for one review file. access.go holds the access layer in front of them,
// api.go the read routes and api_post.go the write routes.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/user"
	"path"
	"strings"
	"sync"

	"github.com/exodist/Review-Helper/go/internal/git"
	"github.com/exodist/Review-Helper/go/internal/share"
)

// Server serves one review file.
type Server struct {
	db       *DB
	path     string // the review file, absolute
	repo     *git.Repo
	identity Identity
	access   *Access
	log      io.Writer // internal errors, with their details

	mu           sync.Mutex
	resolutions  map[resolutionKey][]*ResolutionFile
	contentKinds map[string]string
}

// Identity says whose review a server serves.
type Identity struct {
	User     *string `json:"user"`
	Host     *string `json:"host"`
	Repo     string  `json:"repo"`
	Reviewer string  `json:"reviewer"`
}

// NewServer serves the open review file db, at path in repo, behind the
// access layer.
func NewServer(db *DB, path string, repo *git.Repo, access *Access, log io.Writer) *Server {
	id := Identity{Repo: repo.Root, Reviewer: access.Reviewer}
	if login := loginName(); login != "" {
		id.User = &login
	}
	if host, err := os.Hostname(); err == nil {
		id.Host = &host
	}
	return &Server{
		db:           db,
		path:         path,
		repo:         repo,
		identity:     id,
		access:       access,
		log:          log,
		resolutions:  map[resolutionKey][]*ResolutionFile{},
		contentKinds: map[string]string{},
	}
}

// loginName is the user's login name, or "".
func loginName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	for _, v := range []string{"LOGNAME", "USER"} {
		if s := os.Getenv(v); s != "" {
			return s
		}
	}
	return ""
}

// Handler is the whole server: the access layer, then the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /{$}", s.handle(s.page))
	mux.Handle("GET /static/{path...}", s.handle(s.static))
	mux.Handle("GET /api/review", s.handle(s.getReview))
	mux.Handle("GET /api/file/{id}", s.handle(s.getFile))
	mux.Handle("GET /api/diff/{id}", s.handle(s.getDiff))
	mux.Handle("GET /api/resolution/{id}", s.handle(s.getResolution))
	mux.Handle("GET /api/content/{rev}/{path...}", s.handle(s.getContent))
	mux.Handle("GET /api/feedback", s.handle(s.getFeedback))
	mux.Handle("POST /api/feedback", s.handle(post(s.postFeedback)))
	mux.Handle("POST /api/feedback/{id}", s.handle(post(s.editFeedback)))
	mux.Handle("POST /api/request", s.handle(post(s.addRequest)))
	mux.Handle("POST /api/flag/{id}", s.handle(post(s.postFlagSeen)))
	mux.Handle("POST /api/close", s.handle(post(s.closeReview)))
	// Every other route, and a known one with another method, is unknown.
	mux.Handle("/", s.handle(func(w http.ResponseWriter, r *http.Request) error {
		return notFound("Not found")
	}))
	return s.access.Wrap(unsafePaths(mux))
}

// A handlerFunc answers a request or returns the error to answer with.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// apiError is an error with the status and message to answer with.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string { return e.message }

func badRequest(format string, args ...any) error {
	return &apiError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func notFound(format string, args ...any) error {
	return &apiError{http.StatusNotFound, fmt.Sprintf(format, args...)}
}

func forbidden(format string, args ...any) error {
	return &apiError{http.StatusForbidden, fmt.Sprintf(format, args...)}
}

// handle answers an error a handler returns. Any error but an apiError is
// internal: its details can name paths and internals, so only loopback
// clients see them, and the server's standard error always gets them.
func (s *Server) handle(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}
		var ae *apiError
		if errors.As(err, &ae) {
			writeError(w, ae.status, ae.message)
			return
		}
		fmt.Fprintf(s.log, "review-helper serve: %s %s: %s\n", r.Method, r.URL.Path, err)
		if isLoopback(r) {
			writeError(w, http.StatusInternalServerError, "Internal error: "+err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "Internal error; the details are in the server output")
		}
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	b, err := MarshalJSON(v)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	writeBody(w, b)
	return nil
}

// writeBody writes a response body. The status is sent by then, so a write
// that fails, the client having gone away, leaves nothing to answer: its
// error is dropped rather than answered a second time.
func writeBody(w http.ResponseWriter, b []byte) {
	_, _ = w.Write(b)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// idValue is the route's numeric id; anything else names no route.
func idValue(r *http.Request, name string) (int64, error) {
	id, ok := parseDigits(r.PathValue(name))
	if !ok {
		return 0, notFound("Not found")
	}
	return id, nil
}

// safePath reports whether rel is a relative path with no empty, "." or
// ".." segment, and no NUL or backslash.
func safePath(rel string) bool {
	if rel == "" || strings.ContainsAny(rel, "\x00\\") {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// unsafePaths answers 404 for a static or content path that safePath
// refuses. The mux would redirect such a path to its clean form instead.
func unsafePaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		var rel string
		if after, ok := strings.CutPrefix(p, "/static/"); ok {
			rel = after
		} else if after, ok := strings.CutPrefix(p, "/api/content/"); ok {
			_, rel, _ = strings.Cut(after, "/")
		} else {
			next.ServeHTTP(w, r)
			return
		}
		if !safePath(rel) {
			writeError(w, http.StatusNotFound, "Not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

var contentTypes = map[string]string{
	"css":  "text/css; charset=utf-8",
	"gif":  "image/gif",
	"html": "text/html; charset=utf-8",
	"ico":  "image/x-icon",
	"jpeg": "image/jpeg",
	"jpg":  "image/jpeg",
	"js":   "text/javascript; charset=utf-8",
	"json": "application/json; charset=utf-8",
	"md":   "text/plain; charset=utf-8",
	"png":  "image/png",
	"svg":  "image/svg+xml",
	"txt":  "text/plain; charset=utf-8",
	"webp": "image/webp",
}

// imageTypes are the types /api/content serves by extension.
var imageTypes = map[string]bool{"gif": true, "ico": true, "jpeg": true, "jpg": true, "png": true, "svg": true, "webp": true}

func extension(p string) string {
	return strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) error {
	return sendShared(w, "ux/html/index.html")
}

// static serves the page's own assets only: ux/ also holds its tests and
// agent notes.
func (s *Server) static(w http.ResponseWriter, r *http.Request) error {
	rel := r.PathValue("path")
	dir, _, _ := strings.Cut(rel, "/")
	if !safePath(rel) || (dir != "css" && dir != "js" && dir != "html") {
		return notFound("Not found")
	}
	return sendShared(w, "ux/"+rel)
}

func sendShared(w http.ResponseWriter, name string) error {
	if st, err := fs.Stat(share.FS, name); err != nil || st.IsDir() {
		return notFound("Not found")
	}
	b, err := fs.ReadFile(share.FS, name)
	if err != nil {
		return err
	}
	ct, ok := contentTypes[extension(name)]
	if !ok {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	writeBody(w, b)
	return nil
}

// decodeBody decodes a POST body, which must be a JSON object (an empty
// body is an empty object), into v.
func decodeBody(r *http.Request, v any) error {
	var raw json.RawMessage
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody))
	err := dec.Decode(&raw)
	switch {
	case errors.Is(err, io.EOF):
		raw = json.RawMessage("{}")
	case err != nil:
		return badRequest("The request body must be a JSON object")
	}
	if dec.More() || firstByte(raw) != '{' {
		return badRequest("The request body must be a JSON object")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) && te.Field != "" {
			return badRequest("'%s' has the wrong type", te.Field)
		}
		return badRequest("The request body must be a JSON object")
	}
	return nil
}

const maxBody = 16 << 20
