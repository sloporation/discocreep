package blizzard

import (
	"strings"
	"testing"
)

func TestParseRegions(t *testing.T) {
	got, err := ParseRegions(" US, eu ,,kr")
	if err != nil || strings.Join(got, ",") != "us,eu,kr" {
		t.Errorf("ParseRegions = %v, %v", got, err)
	}
	for _, bad := range []string{"", "  ,", "us,cn", "na"} {
		if _, err := ParseRegions(bad); err == nil {
			t.Errorf("ParseRegions(%q) accepted", bad)
		}
	}
}

func TestSortCharacters(t *testing.T) {
	chars := []Character{{Name: "Bob", Level: 10}, {Name: "Zed", Level: 80}, {Name: "Amy", Level: 80}}
	SortCharacters(chars)
	if chars[0].Name != "Amy" || chars[1].Name != "Zed" || chars[2].Name != "Bob" {
		t.Errorf("order = %v", chars)
	}
}

func TestGuildSlug(t *testing.T) {
	cases := map[string]string{"Macht Druck": "macht-druck", "  Loot Council ": "loot-council", "Élan Vital": "élan-vital"}
	for in, want := range cases {
		if got := GuildSlug(in); got != want {
			t.Errorf("GuildSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNamespaces(t *testing.T) {
	cases := map[string]string{
		Retail.namespace("profile", "us"):     "profile-us",
		Classic.namespace("profile", "eu"):    "profile-classic-eu",
		ClassicEra.namespace("dynamic", "us"): "dynamic-classic1x-us",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("namespace = %q, want %q", got, want)
		}
	}
	if _, err := ParseFlavours("retail, classic_era"); err != nil {
		t.Error(err)
	}
	if _, err := ParseFlavours("retail,forever"); err == nil {
		t.Error("unknown flavour accepted")
	}
}
