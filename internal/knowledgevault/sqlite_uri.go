package knowledgevault

import (
	"net/url"
	"strings"
)

// SQLiteFileDSN renders an absolute filesystem path as a SQLite file: URI and
// appends the given query string (with or without a leading "?").
//
// SQLite reads the text between "file://" and the next "/" as the URI
// authority. A Windows drive path has no leading separator in its filesystem
// form, so the naive "file://D:/x/a.db" makes SQLite reject "D:" as an invalid
// authority. Moving the drive letter into the path component by prefixing "/"
// produces "file:///D:/x/a.db", which SQLite parses as a local file. POSIX
// paths already start with "/" and keep their previous URI.
//
// The path is expected to be absolute; callers use filepath.Abs first. The
// conversion is string-only, so Windows paths can be tested on Linux.
func SQLiteFileDSN(absolutePath, query string) string {
	slashed := absolutePath
	if isWindowsDrivePath(slashed) || strings.HasPrefix(slashed, `\\`) {
		slashed = strings.ReplaceAll(slashed, `\`, "/")
	}
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	location := &url.URL{Scheme: "file", Path: slashed}
	dsn := location.String()
	query = strings.TrimPrefix(query, "?")
	if query == "" {
		return dsn
	}
	return dsn + "?" + query
}

// isWindowsDrivePath reports whether value looks like "C:" or "C:/...", which
// is also true for a Windows path read on a non-Windows host.
func isWindowsDrivePath(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	letter := value[0]
	return (letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z')
}
