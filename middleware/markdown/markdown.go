// Package markdown is middleware to render markdown files as HTML
// on-the-fly.
package markdown

import (
	"net/http"
	"os"
	"path"
	"strings"
	"text/template"
	"time"

	"github.com/mholt/caddy/middleware"
	"github.com/russross/blackfriday"
)

// Markdown implements a layer of middleware that serves
// markdown as HTML.
type Markdown struct {
	// Server root
	Root string

	// Jail the requests to site root with a mock file system
	FileSys http.FileSystem

	// Next HTTP handler in the chain
	Next middleware.Handler

	// The list of markdown configurations
	Configs []*Config

	// The list of index files to try
	IndexFiles []string
}

// Config stores markdown middleware configurations.
type Config struct {
	// Markdown renderer
	Renderer blackfriday.Renderer

	// Base path to match
	PathScope string

	// List of extensions to consider as markdown files
	Extensions map[string]struct{}

	// List of style sheets to load for each markdown file
	Styles []string

	// List of JavaScript files to load for each markdown file
	Scripts []string

	// Template(s) to render with
	Template *template.Template
}

// ServeHTTP implements the http.Handler interface.
func (md Markdown) ServeHTTP(w http.ResponseWriter, r *http.Request) (int, error) {
	var cfg *Config
	for _, c := range md.Configs {
		if middleware.Path(r.URL.Path).Matches(c.PathScope) { // not negated
			cfg = c
			break // or goto
		}
	}
	if cfg == nil {
		return md.Next.ServeHTTP(w, r) // exit early
	}

	// We only deal with HEAD/GET
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	default:
		return http.StatusMethodNotAllowed, nil
	}

	var dirents []os.FileInfo
	var lastModTime time.Time
	fpath := r.URL.Path
	if idx, ok := middleware.IndexFile(md.FileSys, fpath, md.IndexFiles); ok {
		// We're serving a directory index file, which may be a markdown
		// file with a template.  Let's grab a list of files this directory
		// URL points to, and pass that in to any possible template invocations,
		// so that templates can customize the look and feel of a directory.
		fdp, err := md.FileSys.Open(fpath)
		switch {
		case err == nil: // nop
		case os.IsPermission(err):
			return http.StatusForbidden, err
		case os.IsExist(err):
			return http.StatusNotFound, nil
		default: // did we run out of FD?
			return http.StatusInternalServerError, err
		}
		defer fdp.Close()

		// Grab a possible set of directory entries.  Note, we do not check
		// for errors here (unreadable directory, for example).  It may
		// still be useful to have a directory template file, without the
		// directory contents being present.  Note, the directory's last
		// modification is also present here (entry ".").
		dirents, _ = fdp.Readdir(-1)
		for _, d := range dirents {
			lastModTime = latest(lastModTime, d.ModTime())
		}

		// Set path to found index file
		fpath = idx
	}

	// If not supported extension, pass on it
	if _, ok := cfg.Extensions[path.Ext(fpath)]; !ok {
		return md.Next.ServeHTTP(w, r)
	}

	// At this point we have a supported extension/markdown
	f, err := md.FileSys.Open(fpath)
	switch {
	case err == nil: // nop
	case os.IsPermission(err):
		return http.StatusForbidden, err
	case os.IsExist(err):
		return http.StatusNotFound, nil
	default: // did we run out of FD?
		return http.StatusInternalServerError, err
	}
	defer f.Close()

	if fs, err := f.Stat(); err != nil {
		return http.StatusGone, nil
	} else {
		lastModTime = latest(lastModTime, fs.ModTime())
	}

	ctx := middleware.Context{
		Root: md.FileSys,
		Req:  r,
		URL:  r.URL,
	}
	html, err := cfg.Markdown(title(fpath), f, dirents, ctx)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	middleware.SetLastModifiedHeader(w, lastModTime)
	if r.Method == http.MethodGet {
		w.Write(html)
	}
	return http.StatusOK, nil
}

// latest returns the latest time.Time
func latest(t ...time.Time) time.Time {
	var last time.Time

	for _, tt := range t {
		if tt.After(last) {
			last = tt
		}
	}

	return last
}

// title gives a backup generated title for a page
func title(p string) string {
	return strings.TrimRight(path.Base(p), path.Ext(p))
}
