package metrics

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Ship v1!", []string{"ship", "v1"}},
		{"  Hello,  WORLD ", []string{"hello", "world"}},
		{"we will ship v1", []string{"we", "will", "ship", "v1"}},
		{"", nil},
		{"...,-- ", nil},
		{"don't", []string{"don", "t"}}, // apostrophe is a boundary, matching normalizeForMatch
	}
	for _, c := range cases {
		if got := Normalize(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Normalize(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	s := "The Quick, Brown FOX! jumped (over) 3 lazy dogs."
	once := Normalize(s)
	twice := Normalize(strings.Join(once, " "))
	if !reflect.DeepEqual(once, twice) {
		t.Errorf("not idempotent: %v vs %v", once, twice)
	}
}
