package meads

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Tags is a task's tag set. Every backend stores it as a single CSV
// metadata value under the "tags" key - "* tags: api,backend" in TASKS.md,
// the "tags" column in TASKS.csv - so the type transcodes that form
// directly: ParseTags decodes it, String encodes it. In git mode the task
// JSON blob keeps the array form (and UnmarshalJSON accepts either), since
// there is no metadata line to squeeze it onto.
//
// A tag is lowercase letters, numbers and dashes (see ValidateTag). That is
// enforced at the input boundaries - the CLI flags, the MCP tools and the
// HTTP API all run values through Normalize - and NOT on the decode path,
// so an already-stored value that predates the rule (or was hand-edited
// into the file) still loads rather than failing the whole read.
type Tags []string

// tagRe is the whole rule: a tag is one or more lowercase letters, numbers
// and dashes.
var tagRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// ParseTags decodes the CSV storage form. It is lenient by design (see
// Tags): parts are trimmed, empties dropped and duplicates collapsed, but
// nothing is rejected - use Normalize to enforce the tag rule.
func ParseTags(s string) Tags {
	return Tags(splitCSV(s))
}

// NormalizeTags parses the CSV form and normalizes it in one step - the
// shape every input boundary that receives tags as a single string wants
// (the --tags/--add-tags/--rm-tags flags).
func NormalizeTags(s string) (Tags, error) {
	return ParseTags(s).Normalize()
}

// String encodes the tags back to the CSV storage form.
func (t Tags) String() string { return strings.Join(t, ",") }

// Has reports whether tag is present.
func (t Tags) Has(tag string) bool {
	for _, v := range t {
		if v == tag {
			return true
		}
	}
	return false
}

// HasAll reports whether every tag in want is present. An empty want
// matches everything, so a caller can pass an unset filter through
// unconditionally.
func (t Tags) HasAll(want Tags) bool {
	for _, w := range want {
		if !t.Has(w) {
			return false
		}
	}
	return true
}

// Add returns the tags plus add, ignoring any already present.
func (t Tags) Add(add Tags) Tags {
	out := append(Tags(nil), t...)
	for _, v := range add {
		if !out.Has(v) {
			out = append(out, v)
		}
	}
	return out
}

// Remove returns the tags minus rm, ignoring any not present.
func (t Tags) Remove(rm Tags) Tags {
	var out Tags
	for _, v := range t {
		if !rm.Has(v) {
			out = append(out, v)
		}
	}
	return out
}

// ValidateTag returns an error unless tag is lowercase letters, numbers and
// dashes.
func ValidateTag(tag string) error {
	if !tagRe.MatchString(tag) {
		return fmt.Errorf("invalid tag %q: must be lowercase letters, numbers and dashes", tag)
	}
	return nil
}

// Validate returns the first error among the tags, or nil if all are valid.
func (t Tags) Validate() error {
	for _, v := range t {
		if err := ValidateTag(v); err != nil {
			return err
		}
	}
	return nil
}

// Normalize returns the canonical form of the tags - trimmed, lowercased
// and de-duplicated, in first-seen order - or an error naming the first tag
// that is not lowercase letters, numbers and dashes once trimmed and
// lowercased. Case is folded rather than rejected because "API" and "api"
// are unambiguously the same tag; a space or a slash is not, so those are
// errors.
func (t Tags) Normalize() (Tags, error) {
	var out Tags
	for _, v := range t {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			continue
		}
		if err := ValidateTag(v); err != nil {
			return nil, err
		}
		if !out.Has(v) {
			out = append(out, v)
		}
	}
	return out, nil
}

// SanitizeTags coerces arbitrary labels into valid tags rather than
// rejecting them: lowercased, with every run of disallowed characters
// replaced by a single dash and leading/trailing dashes trimmed
// ("Area/API" -> "area-api"). It exists for bulk import from a tracker with
// no such rule (see import_beads.go), where erroring on one odd label would
// fail the whole import and dropping it would lose data silently. Interactive
// input uses Normalize, which reports the bad value instead.
func SanitizeTags(raw []string) Tags {
	var out Tags
	for _, v := range raw {
		var b strings.Builder
		for _, r := range strings.ToLower(strings.TrimSpace(v)) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				b.WriteRune(r)
			} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
		tag := strings.Trim(b.String(), "-")
		if tag != "" && !out.Has(tag) {
			out = append(out, tag)
		}
	}
	return out
}

// UnmarshalJSON accepts both the array form (["api","backend"], what
// MarshalJSON and the git-mode task blob write) and the CSV form
// ("api,backend", what the metadata line holds), so an API caller can send
// whichever it has without a conversion step. Decoding stays lenient - see
// Tags.
func (t *Tags) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*t = ParseTags(s)
		return nil
	}
	var raw []string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*t = Tags(cleanCSVParts(raw))
	return nil
}

// splitCSV decodes a comma-separated metadata value: parts are trimmed,
// empties dropped and duplicates collapsed, preserving first-seen order.
// Both CSV-valued task fields decode through it - tags and files-in-scope.
func splitCSV(s string) []string {
	return cleanCSVParts(strings.Split(s, ","))
}

// cleanCSVParts is splitCSV's tail, shared with Tags.UnmarshalJSON's array
// branch, which has the parts already split.
func cleanCSVParts(parts []string) []string {
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		dup := false
		for _, seen := range out {
			if seen == p {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, p)
		}
	}
	return out
}
