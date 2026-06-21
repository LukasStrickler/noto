package server

import "testing"

// TestPercentDecodeHeader pins the server's decode of the upload client's
// url.PathEscape: real percent-encoding is reversed exactly, and anything that
// isn't valid percent-encoding is returned verbatim so a stray "%" in a raw title
// can never drop or mangle it.
func TestPercentDecodeHeader(t *testing.T) {
	cases := []struct{ in, want string }{
		{"R%C3%A9union", "Réunion"},                // encoded UTF-8 round-trips
		{"%E4%BC%9A%E8%AD%B0.m4a", "会議.m4a"},       // CJK filename
		{"Q3%20Planning", "Q3 Planning"},           // encoded space
		{"plain ascii title", "plain ascii title"}, // no escapes → unchanged (spaces kept)
		{"a+b", "a+b"},     // '+' is NOT a space here (PathUnescape, not Query)
		{"50%", "50%"},     // stray '%' (invalid) → raw, not dropped
		{"a%zzb", "a%zzb"}, // invalid hex → raw
		{"", ""},
	}
	for _, c := range cases {
		if got := percentDecodeHeader(c.in); got != c.want {
			t.Errorf("percentDecodeHeader(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
