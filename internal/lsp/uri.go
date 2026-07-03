package lsp

import (
	"net/url"
	"path/filepath"
	"strings"
)

// uriToPath converts a file:// URI (as sent by LSP clients for
// textDocument URIs) to an absolute filesystem path using the OS's native
// separators. Non-file:// URIs return ok=false since this server only
// ever deals with local .sql files.
func uriToPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	p := u.Path
	if p == "" {
		return "", false
	}
	// On Windows, url.Parse of "file:///C:/foo" yields Path "/C:/foo"; strip
	// the leading slash before a drive letter. filepath.FromSlash handles
	// separator conversion for the rest.
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p), true
}

// pathToURI converts an absolute filesystem path to a file:// URI.
func pathToURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	return u.String()
}
