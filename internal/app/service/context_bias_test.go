package service

import (
	"reflect"
	"testing"
)

func TestBiasTermsFromNames_ExpandsMultiWordNamesToParts(t *testing.T) {
	got := biasTermsFromNames("Q3 Planning", []string{"Lukas Strickler"})
	want := []string{"Q3 Planning", "Lukas Strickler", "Lukas", "Strickler"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBiasTermsFromNames_SingleWordNameNotSplit(t *testing.T) {
	got := biasTermsFromNames("", []string{"Madonna"})
	if !reflect.DeepEqual(got, []string{"Madonna"}) {
		t.Errorf("a single-word name should appear once with no parts: %v", got)
	}
}

func TestBiasTermsFromNames_DropsShortParticles(t *testing.T) {
	got := biasTermsFromNames("", []string{"Ada de Vries"})
	// "Ada" (3) and "de" (2) are under the 4-char bar; only the full name and "Vries".
	want := []string{"Ada de Vries", "Vries"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBiasTermsFromNames_DedupsAcrossNamesAndTitle(t *testing.T) {
	got := biasTermsFromNames("Strickler sync", []string{"Lukas Strickler", "Lukas Meyer"})
	// "Lukas" appears once though it's in both names; "Strickler" the part dedups too.
	want := []string{"Strickler sync", "Lukas Strickler", "Lukas", "Strickler", "Lukas Meyer", "Meyer"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBiasTermsFromNames_SkipsUntitledAndEmpty(t *testing.T) {
	if got := biasTermsFromNames("Untitled meeting", nil); len(got) != 0 {
		t.Errorf("untitled + no names → empty, got %v", got)
	}
}
