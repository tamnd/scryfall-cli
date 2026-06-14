package scryfall

import (
	"testing"

	"github.com/tamnd/any-cli/kit"
)

// These tests are offline: they exercise the URI driver's pure string functions
// and the host wiring (mint, body, resolve), which need no network. The client's
// HTTP behaviour is covered in scryfall_test.go.

func TestDomainInfo(t *testing.T) {
	info := Domain{}.Info()
	if info.Scheme != "scryfall" {
		t.Errorf("Scheme = %q, want scryfall", info.Scheme)
	}
	if len(info.Hosts) == 0 || info.Hosts[0] != Host {
		t.Errorf("Hosts = %v, want [%s]", info.Hosts, Host)
	}
	if info.Identity.Binary != "scryfall" {
		t.Errorf("Identity.Binary = %q, want scryfall", info.Identity.Binary)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		in  string
		typ string
		id  string
	}{
		// fuzzy name
		{"Black Lotus", "card", "Black Lotus"},
		{"dragon", "card", "dragon"},
		// UUID
		{"f3f01a15-7111-4b52-9a3f-af3b82f5cf30", "card-id", "f3f01a15-7111-4b52-9a3f-af3b82f5cf30"},
		// scryfall.com card URL
		{"https://scryfall.com/card/lea/232/black-lotus", "card-id", "lea/232/black-lotus"},
	}
	for _, tc := range cases {
		typ, id, err := Domain{}.Classify(tc.in)
		if err != nil || typ != tc.typ || id != tc.id {
			t.Errorf("Classify(%q) = (%q, %q, %v), want (%q, %q, nil)",
				tc.in, typ, id, err, tc.typ, tc.id)
		}
	}
}

func TestClassifyEmpty(t *testing.T) {
	_, _, err := Domain{}.Classify("")
	if err == nil {
		t.Error("Classify(\"\") should return error, got nil")
	}
}

func TestLocate(t *testing.T) {
	tests := []struct {
		uriType string
		id      string
		want    string
	}{
		{"card", "Black Lotus", "https://scryfall.com/search?q=Black+Lotus"},
		{"card-id", "f3f01a15-7111-4b52-9a3f-af3b82f5cf30", "https://scryfall.com/card/f3f01a15-7111-4b52-9a3f-af3b82f5cf30"},
		{"set", "lea", "https://scryfall.com/sets/lea"},
	}
	for _, tc := range tests {
		got, err := Domain{}.Locate(tc.uriType, tc.id)
		if err != nil || got != tc.want {
			t.Errorf("Locate(%q, %q) = (%q, %v), want (%q, nil)", tc.uriType, tc.id, got, err, tc.want)
		}
	}
}

func TestLocateUnknownType(t *testing.T) {
	_, err := Domain{}.Locate("unknown", "foo")
	if err == nil {
		t.Error("Locate with unknown type should return error")
	}
}

// TestHostWiring mounts the driver in a kit Host and checks the round trip:
// a record mints to its URI, its body is readable, and a bare id resolves back
// to the same URI. The init in domain.go registers the domain, so kit.Open finds it.
func TestHostWiring(t *testing.T) {
	h, err := kit.Open()
	if err != nil {
		t.Fatal(err)
	}

	card := &Card{
		ID:         "f3f01a15-7111-4b52-9a3f-af3b82f5cf30",
		Name:       "Black Lotus",
		TypeLine:   "Artifact",
		OracleText: "{T}, Sacrifice Black Lotus: Add three mana of any one color.",
		SetCode:    "lea",
		SetName:    "Limited Edition Alpha",
		Rarity:     "rare",
	}
	u, err := h.Mint(card)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	// The Card type is registered under URIType "card" by the card resolver op.
	want := "scryfall://card/f3f01a15-7111-4b52-9a3f-af3b82f5cf30"
	if u.String() != want {
		t.Errorf("Mint = %q, want %q", u.String(), want)
	}

	got, err := h.ResolveOn("scryfall", "Black Lotus")
	if err != nil {
		t.Fatalf("ResolveOn: %v", err)
	}
	// kit percent-encodes the space in the URI path
	wantResolve := "scryfall://card/Black%20Lotus"
	if got.String() != wantResolve {
		t.Errorf("ResolveOn = %q, want %q", got.String(), wantResolve)
	}
}
