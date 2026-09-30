// Package jira reads one Jira issue for run --jira and turns it into task text
// (specs/jira/SPEC.md §6.1, §8, §9).
//
// It is read-only by construction: the only request it can make is the one GET in
// Client.Fetch, and it follows no redirects. Credentials are passed only to
// NewHTTPClient, sent only to Options.BaseURL, and redacted from every error.
//
// The package imports only domain, apperr and the standard library. It does not import
// config: Options mirrors the transport fields of config.JiraConfig, as jev.Options does
// for decision.jev. The wire types are unexported, so nothing outside can read a raw
// answer.
package jira
