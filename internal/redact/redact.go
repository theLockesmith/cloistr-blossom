// Package redact strips credentials from connection strings and errors before
// they reach a log line.
package redact

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// URL returns only the scheme, host and port of a connection URL, for safe
// logging. Userinfo, path, query and fragment are dropped, since any of them
// can carry a secret (postgres accepts ?password=...). Anything that does not
// parse as scheme://host is withheld entirely: a key=value DSN can hold a
// password and has no safe part to show.
func URL(raw string) string {
	if raw == "" {
		return "(not set)"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "(redacted)"
	}
	return u.Scheme + "://" + u.Host
}

// Err returns err with any URL that net/url embedded in its message replaced
// by its redacted form. net/url parse errors quote the full input, password
// included, and drivers such as go-redis and lib/pq return them unwrapped.
func Err(err error) error {
	if err == nil {
		return nil
	}
	var ue *url.Error
	if !errors.As(err, &ue) || ue.URL == "" {
		return err
	}
	msg := err.Error()
	safe := URL(ue.URL)
	msg = strings.ReplaceAll(msg, strconv.Quote(ue.URL), strconv.Quote(safe))
	msg = strings.ReplaceAll(msg, ue.URL, safe)
	return errors.New(msg)
}
