// Package redact removes credential-like material from strings before they are
// displayed or persisted. It is a last line of defence; callers should avoid
// including secrets in errors in the first place.
package redact

import "regexp"

const replacement = "[REDACTED]"

var (
	assignment = regexp.MustCompile(`(?i)(mnemonic|private[_-]?key|passphrase|password|token|api[_-]?key|secret)(\s*[=:]\s*)("[^"\n]*"|'[^'\n]*'|[^\s,;]+)`)
	userinfo   = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)
	keyringDir = regexp.MustCompile(`(?i)(keyring[_-]?dir\s*[=:]\s*)("[^"\n]*"|'[^'\n]*'|[^\s,;]+)`)
)

// String redacts common secret assignments, URL user information, and keyring
// directory values. It never returns the original string unchanged when one of
// those patterns is present.
func String(value string) string {
	value = userinfo.ReplaceAllString(value, `${1}`+replacement+`@`)
	value = assignment.ReplaceAllString(value, `${1}${2}`+replacement)
	return keyringDir.ReplaceAllString(value, `${1}`+replacement)
}
