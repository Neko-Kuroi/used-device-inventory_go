package util

// SafeNext validates a post-login redirect target: only an internal
// path (starts with "/", but not "//" or "/\", which browsers would
// treat as another host) is accepted; anything else becomes "/".
// The Python version checks the first two conditions; rejecting the
// backslash form as well is the design's stated rule for the port.
func SafeNext(next string) string {
	if len(next) > 0 && next[0] == '/' &&
		(len(next) == 1 || (next[1] != '/' && next[1] != '\\')) {
		return next
	}
	return "/"
}
