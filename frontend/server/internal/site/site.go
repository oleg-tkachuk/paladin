// Package site serves the console's static export (ADR-0049).
//
// The export holds one page per route, a dynamic segment built once under a
// placeholder: /tenants/[id]/billing is tenants/_/billing.html. A request is
// resolved segment by segment, a segment matching a literal entry first and
// the placeholder otherwise, the way the router prefers a static route to a
// dynamic one. A path no route matches is not found.
//
// The client router asks for a page's RSC payload at the page's path plus
// ".txt", and marks the request with the RSC header; an id that itself ends in
// ".txt" is a page like any other, so the header, not the suffix, decides.
//
// The router also strips a trailing ".txt" from the address it loads on
// (Next 16, route-params.js urlToUrlWithoutFlightMarker), so a page whose last
// segment ends in ".txt" — an object named report.txt — would read the wrong
// id. Such a page is redirected to the same address with the dot encoded
// (TxtRedirect), which the router leaves alone and the page decodes.
package site

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	// RSCHeader marks the client router's request for a page's payload.
	RSCHeader = "RSC"

	htmlExt = ".html"
	rscExt  = ".txt"
	// encodedRSCExt is rscExt with its dot percent-encoded.
	encodedRSCExt = "%2Etxt"
	indexDoc      = "index"
	// notFoundDoc is the export's own 404 page.
	notFoundDoc = "404.html"

	// immutablePrefix holds content-hashed files, which never change under a
	// name.
	immutablePrefix   = "_next/static/"
	cacheImmutable    = "public, max-age=31536000, immutable"
	cacheRevalidate   = "no-cache"
	etagHashBytes     = 16
	defaultMediaType  = "application/octet-stream"
	segmentSeparator  = "/"
	headerCache       = "Cache-Control"
	headerContentType = "Content-Type"
	headerETag        = "ETag"
)

// mediaTypes covers what the export holds that Go's built-in table may not
// know where the image carries no mime.types.
var mediaTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".txt":   "text/plain; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json",
	".svg":   "image/svg+xml",
	".ico":   "image/x-icon",
	".png":   "image/png",
	".woff2": "font/woff2",
	".map":   "application/json",
}

// Site is an export ready to serve.
type Site struct {
	files       fs.FS
	placeholder string
	etags       map[string]string
}

// New indexes the export: every file's ETag is its content hash, computed
// once here. It fails when the export has no index page, which means the
// build did not reach the image.
func New(files fs.FS, placeholder string) (*Site, error) {
	s := &Site{files: files, placeholder: placeholder, etags: map[string]string{}}
	err := fs.WalkDir(files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		s.etags[name] = `"` + hex.EncodeToString(sum[:etagHashBytes]) + `"`
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("index the export: %w", err)
	}
	if _, ok := s.etags[indexDoc+htmlExt]; !ok {
		return nil, fmt.Errorf("the export has no %s%s", indexDoc, htmlExt)
	}
	return s, nil
}

// Files is how many files the export holds.
func (s *Site) Files() int { return len(s.etags) }

// segment is one path segment: the entry it may name in the export, and
// whether its value ends in the RSC suffix.
type segment struct {
	// name is the decoded segment, or "" when it cannot name an entry: only
	// the placeholder can stand for it.
	name string
	txt  bool
}

func segments(escapedPath string) []segment {
	var segs []segment
	for _, raw := range strings.Split(escapedPath, segmentSeparator) {
		if raw == "" {
			continue
		}
		value, err := url.PathUnescape(raw)
		if err != nil {
			value = raw
		}
		seg := segment{name: value, txt: strings.HasSuffix(value, rscExt)}
		if err != nil || strings.Contains(value, segmentSeparator) || value == "." || value == ".." {
			seg.name = ""
		}
		segs = append(segs, seg)
	}
	return segs
}

// Resolve names the file a request path is served from, or false.
func (s *Site) Resolve(escapedPath string, rsc bool) (string, bool) {
	segs := segments(escapedPath)
	if len(segs) == 0 {
		return s.leafAtRoot(rsc)
	}
	return s.walk(".", segs, rsc)
}

// TxtRedirect is where a page request whose last segment ends in ".txt" goes
// instead: the same address with that dot encoded. False when the path is no
// page, already encoded, or a file the export holds under that name.
func (s *Site) TxtRedirect(escapedPath string) (string, bool) {
	trimmed := strings.TrimRight(escapedPath, segmentSeparator)
	if !strings.HasSuffix(trimmed, rscExt) {
		return "", false
	}
	file, ok := s.Resolve(escapedPath, false)
	if !ok || !strings.HasSuffix(file, htmlExt) {
		return "", false
	}
	return strings.TrimSuffix(trimmed, rscExt) + encodedRSCExt, true
}

func (s *Site) leafAtRoot(rsc bool) (string, bool) {
	name := indexDoc + htmlExt
	if rsc {
		name = indexDoc + rscExt
	}
	_, ok := s.etags[name]
	return name, ok
}

func (s *Site) walk(dir string, segs []segment, rsc bool) (string, bool) {
	seg, rest := segs[0], segs[1:]
	if len(rest) == 0 {
		return s.leaf(dir, seg, rsc)
	}
	for _, name := range s.candidates(seg.name) {
		if found, ok := s.walk(path.Join(dir, name), rest, rsc); ok {
			return found, true
		}
	}
	return "", false
}

// leaf is the file for the last segment: the file it names, else its page.
func (s *Site) leaf(dir string, seg segment, rsc bool) (string, bool) {
	var tries []string
	if seg.name != "" {
		tries = append(tries, path.Join(dir, seg.name))
	}
	switch {
	case !rsc:
		for _, name := range s.candidates(seg.name) {
			tries = append(tries, path.Join(dir, name+htmlExt))
		}
	case seg.txt:
		tries = append(tries, path.Join(dir, s.placeholder+rscExt))
	}
	for _, name := range tries {
		if _, ok := s.etags[name]; ok {
			return name, true
		}
	}
	return "", false
}

// candidates are the entries a segment may match, the literal first.
func (s *Site) candidates(seg string) []string {
	if seg == "" || seg == s.placeholder {
		return []string{s.placeholder}
	}
	return []string{seg, s.placeholder}
}

// Serve writes the file a request resolves to, or the export's 404 page.
func (s *Site) Serve(w http.ResponseWriter, r *http.Request) (name string, found bool) {
	name, found = s.Resolve(r.URL.EscapedPath(), r.Header.Get(RSCHeader) != "")
	status := http.StatusOK
	if !found {
		name, status = notFoundDoc, http.StatusNotFound
		if _, ok := s.etags[name]; !ok {
			http.NotFound(w, r)
			return "", false
		}
	}
	body, err := fs.ReadFile(s.files, name)
	if err != nil {
		// Indexed at start from the same embedded tree: unreachable unless the
		// tree changed under the process.
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return name, false
	}
	h := w.Header()
	h.Set(headerContentType, mediaType(name))
	if strings.HasPrefix(name, immutablePrefix) {
		h.Set(headerCache, cacheImmutable)
	} else {
		h.Set(headerCache, cacheRevalidate)
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return name, false
	}
	h.Set(headerETag, s.etags[name])
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	return name, true
}

func mediaType(name string) string {
	ext := path.Ext(name)
	if t, ok := mediaTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return defaultMediaType
}
