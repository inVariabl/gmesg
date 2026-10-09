// Package contacts reads the abook address book, merges it with the
// contact list held by the paired phone, and ranks contacts against a
// partial name for recipient completion.
package contacts

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Number is one telephone number belonging to a contact. Label names the
// field it came from ("mobile", "phone", "workphone", or "phone" for a
// number supplied by the paired phone).
type Number struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Contact is a named person with one or more telephone numbers.
type Contact struct {
	Name     string   `json:"name"`
	Nickname string   `json:"nickname,omitempty"`
	Numbers  []Number `json:"numbers"`
}

// abookNumberFields lists the abook fields that hold telephone numbers, in
// the order a contact's numbers are presented. The fax field is omitted
// because a facsimile number cannot receive a text message.
var abookNumberFields = []string{"mobile", "phone", "workphone"}

// DefaultAbookPath returns the location abook itself uses by default,
// ~/.abook/addressbook.
func DefaultAbookPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".abook", "addressbook"), nil
}

// LoadAbook reads the address book at path. A missing file is not an
// error; it yields no contacts.
func LoadAbook(path string) ([]Contact, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseAbook(f)
}

// ParseAbook parses abook's addressbook format: an INI-style file with a
// [format] section followed by one numbered section per entry, each
// holding key=value fields. Entries without a name or without any
// telephone number are discarded.
func ParseAbook(r io.Reader) ([]Contact, error) {
	var contacts []Contact
	var fields map[string]string
	inEntry := false

	flush := func() {
		if !inEntry {
			return
		}
		if c, ok := abookEntry(fields); ok {
			contacts = append(contacts, c)
		}
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			section := line[1 : len(line)-1]
			inEntry = section != "format"
			fields = map[string]string{}
			continue
		}
		if !inEntry {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flush()
	return contacts, nil
}

func abookEntry(fields map[string]string) (Contact, bool) {
	c := Contact{Name: fields["name"], Nickname: fields["nick"]}
	if c.Name == "" {
		return Contact{}, false
	}
	for _, label := range abookNumberFields {
		// abook stores several values for one field separated by commas.
		for _, value := range strings.Split(fields[label], ",") {
			value = strings.TrimSpace(value)
			if NormalizeNumber(value) == "" {
				continue
			}
			c.Numbers = append(c.Numbers, Number{Value: value, Label: label})
		}
	}
	if len(c.Numbers) == 0 {
		return Contact{}, false
	}
	return c, true
}

// NormalizeNumber reduces a telephone number to its digits, so that
// "+1 (555) 010-0000" and "15550100000" compare equal. It returns "" for a
// value containing no digits.
func NormalizeNumber(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// numberKey is the comparison key for deduplicating numbers: the last ten
// digits, which disregards a leading country code on North American
// numbers written with and without one.
func numberKey(s string) string {
	digits := NormalizeNumber(s)
	if len(digits) > 10 {
		return digits[len(digits)-10:]
	}
	return digits
}

// Merge combines abook contacts with phone contacts. Every abook contact
// is retained. A phone contact is retained only for those of its numbers
// that no abook contact already lists, so the abook name prevails when the
// two sources disagree. The result is sorted by name.
func Merge(abook, phone []Contact) []Contact {
	seen := map[string]bool{}
	var merged []Contact
	for _, c := range abook {
		for _, n := range c.Numbers {
			seen[numberKey(n.Value)] = true
		}
		merged = append(merged, c)
	}

	byName := map[string]int{}
	for _, c := range phone {
		var numbers []Number
		for _, n := range c.Numbers {
			key := numberKey(n.Value)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			numbers = append(numbers, n)
		}
		if len(numbers) == 0 {
			continue
		}
		// The phone reports one record per number, so a person with two
		// numbers arrives as two records sharing a name.
		if i, ok := byName[c.Name]; ok {
			merged[i].Numbers = append(merged[i].Numbers, numbers...)
			continue
		}
		byName[c.Name] = len(merged)
		merged = append(merged, Contact{Name: c.Name, Nickname: c.Nickname, Numbers: numbers})
	}

	sort.SliceStable(merged, func(i, j int) bool {
		return strings.ToLower(merged[i].Name) < strings.ToLower(merged[j].Name)
	})
	return merged
}

// Candidate is one selectable completion: a single number of a single
// contact. A contact with several numbers yields one candidate per number.
type Candidate struct {
	Name   string `json:"name"`
	Number Number `json:"number"`
	score  int
}

// Search ranks every number of every contact against query and returns at
// most limit candidates, best first. A query consisting of digits is also
// matched against the numbers themselves. An empty query matches nothing.
// A limit of zero or less returns every match.
func Search(contacts []Contact, query string, limit int) []Candidate {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	queryDigits := NormalizeNumber(query)
	digitQuery := queryDigits != "" && isNumberLike(query)

	var out []Candidate
	for _, c := range contacts {
		score := nameScore(c.Name, query)
		if s := nameScore(c.Nickname, query); s > score {
			score = s
		}
		for _, n := range c.Numbers {
			s := score
			if digitQuery && strings.Contains(NormalizeNumber(n.Value), queryDigits) {
				s = max(s, scoreSubstring)
			}
			if s == 0 {
				continue
			}
			out = append(out, Candidate{Name: c.Name, Number: n, score: s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Match quality, highest first.
const (
	scoreNone        = 0
	scoreSubsequence = 1
	scoreSubstring   = 2
	scoreWordPrefix  = 3
	scoreNamePrefix  = 4
)

// nameScore grades how well query matches name, case-insensitively: as a
// prefix of the whole name, as a prefix of any word in it, as a substring,
// or as a subsequence of its letters (so "jsmi" matches "John Smith").
func nameScore(name, query string) int {
	if name == "" {
		return scoreNone
	}
	n := strings.ToLower(name)
	q := strings.ToLower(query)
	switch {
	case strings.HasPrefix(n, q):
		return scoreNamePrefix
	case hasWordPrefix(n, q):
		return scoreWordPrefix
	case strings.Contains(n, q):
		return scoreSubstring
	case isSubsequence(n, strings.ReplaceAll(q, " ", "")):
		return scoreSubsequence
	}
	return scoreNone
}

func hasWordPrefix(name, query string) bool {
	for _, word := range strings.FieldsFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || r == '-' || r == '.'
	}) {
		if strings.HasPrefix(word, query) {
			return true
		}
	}
	return false
}

func isSubsequence(s, sub string) bool {
	if sub == "" {
		return false
	}
	rs := []rune(sub)
	i := 0
	for _, r := range s {
		if r == rs[i] {
			i++
			if i == len(rs) {
				return true
			}
		}
	}
	return false
}

// isNumberLike reports whether s consists only of characters that appear
// in a written telephone number.
func isNumberLike(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !strings.ContainsRune("+-() .", r) {
			return false
		}
	}
	return true
}

// IsNumberLike reports whether s reads as a telephone number rather than a
// name, so a caller can accept it as a recipient directly.
func IsNumberLike(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && isNumberLike(s) && len(NormalizeNumber(s)) >= 3
}
