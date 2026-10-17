package contacts

import (
	"strings"
	"testing"
)

const sampleAbook = `# abook addressbook file

[format]
program=abook
version=0.6.1

[0]
name=Ada Lovelace
email=ada@example.org
mobile=+1 (555) 010-0001
phone=555-010-0002

[1]
name=Charles Babbage
nick=Charlie
workphone=5550100003
fax=5550100009

[2]
name=No Number
email=none@example.org
`

func TestParseAbook(t *testing.T) {
	got, err := ParseAbook(strings.NewReader(sampleAbook))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("parsed %d contacts, want 2 (the entry without a number is discarded): %+v", len(got), got)
	}
	if got[0].Name != "Ada Lovelace" || len(got[0].Numbers) != 2 {
		t.Fatalf("first contact = %+v", got[0])
	}
	if got[0].Numbers[0].Label != "mobile" {
		t.Fatalf("mobile number must be listed first, got %+v", got[0].Numbers)
	}
	if got[1].Nickname != "Charlie" || len(got[1].Numbers) != 1 || got[1].Numbers[0].Label != "workphone" {
		t.Fatalf("second contact = %+v (fax must be excluded)", got[1])
	}
}

func TestMergePrefersAbookName(t *testing.T) {
	abook := []Contact{{Name: "Ada Lovelace", Numbers: []Number{{Value: "+1 555 010 0001", Label: "mobile"}}}}
	phone := []Contact{
		{Name: "Ada L", Numbers: []Number{{Value: "5550100001", Label: "phone"}}},
		{Name: "Grace Hopper", Numbers: []Number{{Value: "5550100004", Label: "phone"}}},
		{Name: "Grace Hopper", Numbers: []Number{{Value: "5550100005", Label: "phone"}}},
	}
	got := Merge(abook, phone)
	if len(got) != 2 {
		t.Fatalf("merged %d contacts, want 2: %+v", len(got), got)
	}
	if got[0].Name != "Ada Lovelace" {
		t.Fatalf("abook name must prevail, got %+v", got[0])
	}
	if got[1].Name != "Grace Hopper" || len(got[1].Numbers) != 2 {
		t.Fatalf("phone records sharing a name must combine, got %+v", got[1])
	}
}

func TestSearchRanking(t *testing.T) {
	list := []Contact{
		{Name: "Sam Smith", Numbers: []Number{{Value: "5550100010"}}},
		{Name: "Samantha Jones", Numbers: []Number{{Value: "5550100011"}, {Value: "5550100012"}}},
		{Name: "Pam Osam", Numbers: []Number{{Value: "5550100013"}}},
		{Name: "Susan Adams Miller", Numbers: []Number{{Value: "5550100014"}}},
	}

	got := Search(list, "sam", 0)
	var names []string
	for _, c := range got {
		names = append(names, c.Name)
	}
	want := "Sam Smith,Samantha Jones,Samantha Jones,Pam Osam,Susan Adams Miller"
	if strings.Join(names, ",") != want {
		t.Fatalf("Search(sam) = %v, want %s", names, want)
	}

	if got := Search(list, "jones", 1); len(got) != 1 || got[0].Name != "Samantha Jones" {
		t.Fatalf("word-prefix search with limit 1 = %+v", got)
	}
	if got := Search(list, "0014", 0); len(got) != 1 || got[0].Name != "Susan Adams Miller" {
		t.Fatalf("digit search = %+v", got)
	}
	if got := Search(list, "", 0); got != nil {
		t.Fatalf("empty query must match nothing, got %+v", got)
	}
}

func TestIsNumberLike(t *testing.T) {
	for s, want := range map[string]bool{
		"+1 (555) 010-0000": true,
		"5550100000":        true,
		"12":                false,
		"Sam":               false,
		"555 Sam":           false,
	} {
		if got := IsNumberLike(s); got != want {
			t.Errorf("IsNumberLike(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestSameNumber(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"+1 (555) 010-0000", "15550100000", true},
		{"+15550100000", "(555) 010-0000", true},
		{"555-010-0000", "555-010-0001", false},
		{"12345", "212345", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := SameNumber(c.a, c.b); got != c.want {
			t.Errorf("SameNumber(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
