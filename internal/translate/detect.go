// Package translate prepares the user's request for the decision provider (TASKS.md
// T15, В7/В13): the questions are asked in English, so a request written mostly in
// Cyrillic is translated by an LLM first. Detection is deterministic; the translation
// is checked for sanity, and any failure falls back to the original text instead of
// failing the run.
package translate

import (
	"regexp"
	"unicode"
)

// CyrillicThreshold is the share of Cyrillic among the prose letters at which a
// request is translated. Code in the request - identifiers, paths, `backticked` text -
// is left out of the count: it is Latin in any language and would otherwise dilute a
// Russian sentence around it below the threshold.
const CyrillicThreshold = 0.30

// maxTranslatedCyrillic is the share of Cyrillic a translation may keep: some is fine
// (a quoted UI string), a mostly Russian answer is not a translation.
const maxTranslatedCyrillic = 0.10

// Language codes recorded in the trace.
const (
	LanguageEnglish = "en"
	LanguageRussian = "ru"
)

// Detection is the result of looking at a request's letters.
type Detection struct {
	Language       string  `json:"language"`
	CyrillicShare  float64 `json:"cyrillic_share"`
	NeedsTranslate bool    `json:"-"`
}

// Detect decides whether text needs translating. Text with no letters at all needs no
// translation.
func Detect(text string) Detection {
	share := cyrillicShare(proseOnly(text))
	d := Detection{Language: LanguageEnglish, CyrillicShare: share}
	if share >= CyrillicThreshold {
		d.Language = LanguageRussian
		d.NeedsTranslate = true
	}
	return d
}

func cyrillicShare(text string) float64 {
	letters, cyrillic := 0, 0
	for _, r := range text {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if unicode.Is(unicode.Cyrillic, r) {
			cyrillic++
		}
	}
	if letters == 0 {
		return 0
	}
	return float64(cyrillic) / float64(letters)
}

var backtickRe = regexp.MustCompile("`[^`]*`")

// proseOnly blanks out the parts of text that are code rather than language.
func proseOnly(text string) string {
	text = backtickRe.ReplaceAllString(text, " ")
	text = pathRe.ReplaceAllString(text, " ")
	return identifierRe.ReplaceAllString(text, " ")
}
