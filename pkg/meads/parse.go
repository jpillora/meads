package meads

import (
	"regexp"
	"strings"
)

// TitleSplitRe matches the free-form `md add` title/description delimiter:
// ". " (a period followed by a single literal space) or a bare newline.
// The space after \. is literal and significant. A bare "." (e.g. v1.2,
// http://foo.com) or "." followed by non-space whitespace deliberately does
// NOT split, so titles stay intact.
var TitleSplitRe = regexp.MustCompile(`\. |\n`)

// SplitTitleDescription splits free-form add input into a title and description
// at the first TitleSplitRe match (". " or newline). Both halves are
// space-trimmed. With no match, the whole (trimmed) input is the title and the
// description is empty.
func SplitTitleDescription(input string) (title, desc string) {
	if loc := TitleSplitRe.FindStringIndex(input); loc != nil {
		return strings.TrimSpace(input[:loc[0]]), strings.TrimSpace(input[loc[1]:])
	}
	return strings.TrimSpace(input), ""
}
