package tags

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"  invoice ":            "invoice",
		"#Tax  2026":            "Tax 2026",
		"قرارداد  مهم":          "قرارداد مهم",
		strings.Repeat("a", 50): strings.Repeat("a", 50),
	} {
		if got, err := Normalize(in); err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "  ", "#", strings.Repeat("a", 51)} {
		if _, err := Normalize(in); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Normalize(%q) = %v, want ErrInvalidName", in, err)
		}
	}
}

func TestNormalizeAll(t *testing.T) {
	got, err := normalizeAll([]string{"Invoice", " invoice", "#tax"})
	if err != nil || !reflect.DeepEqual(got, []string{"Invoice", "tax"}) {
		t.Fatalf("normalizeAll = %v, %v", got, err)
	}
	many := make([]string, MaxPerRecord+1)
	for i := range many {
		many[i] = fmt.Sprint("t", i)
	}
	if _, err := normalizeAll(many); !errors.Is(err, ErrTooMany) {
		t.Fatalf("21 tags = %v, want ErrTooMany", err)
	}
	if _, err := normalizeAll([]string{"ok", " "}); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("a blank tag = %v, want ErrInvalidName", err)
	}
}

func TestSplitAndMatch(t *testing.T) {
	if got := Split(" a, ,b ,"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("Split = %v", got)
	}
	if Split("") != nil {
		t.Fatal("Split of nothing isn't empty")
	}
	if ParseMatch("all") != MatchAll || ParseMatch("") != MatchAny || ParseMatch("ALL") != MatchAny {
		t.Fatal("ParseMatch")
	}
}

func TestFilter(t *testing.T) {
	s := &Store{Tags: "tags", Links: "file_tags", LinkRecord: "file_id"}
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if got := s.Filter("f.id", nil, MatchAny, arg); got != "FALSE" {
		t.Fatalf("no tags = %q", got)
	}
	args = []any{"user"}
	if got := s.Filter("f.id", []string{"a", "b"}, MatchAny, arg); got !=
		"EXISTS (SELECT 1 FROM file_tags WHERE file_id = f.id AND tag_id IN ($2, $3))" {
		t.Fatalf("any = %q", got)
	}
	if got := s.Filter("f.id", []string{"a", "b"}, MatchAll, arg); got !=
		"(SELECT COUNT(DISTINCT tag_id) FROM file_tags WHERE file_id = f.id AND tag_id IN ($4, $5)) = 2" {
		t.Fatalf("all = %q", got)
	}
	if !reflect.DeepEqual(args, []any{"user", "a", "b", "a", "b"}) {
		t.Fatalf("args = %v", args)
	}

	if got := s.Named("f.id", "t.name ILIKE $6"); got !=
		"EXISTS (SELECT 1 FROM file_tags l JOIN tags t ON t.id = l.tag_id WHERE l.file_id = f.id AND t.name ILIKE $6)" {
		t.Fatalf("named = %q", got)
	}
}
