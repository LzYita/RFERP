package ui

import "testing"

func TestParseIntTextRejectsMalformedAndAcceptsPositive(t *testing.T) {
	if _, err := parseIntText("abc"); err == nil {
		t.Fatal("malformed integer was accepted")
	}
	if _, err := parseIntText("-1"); err != nil {
		t.Fatalf("negative integer should parse before range validation: %v", err)
	}
	if got, err := parseIntText(" 12 "); err != nil || got != 12 {
		t.Fatalf("parseIntText(12) = %d, %v", got, err)
	}
}

func TestParseFloatTextRejectsMalformedAndNonFinite(t *testing.T) {
	for _, text := range []string{"abc", "NaN", "+Inf", "-Inf"} {
		if _, err := parseFloatText(text); err == nil {
			t.Errorf("parseFloatText(%q) accepted invalid value", text)
		}
	}
	if got, err := parseFloatText(" 1.25 "); err != nil || got != 1.25 {
		t.Fatalf("parseFloatText(1.25) = %v, %v", got, err)
	}
}
