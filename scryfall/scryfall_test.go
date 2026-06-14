package scryfall_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tamnd/scryfall-cli/scryfall"
)

// fakeCard returns a minimal Card JSON object.
func fakeCard(id, name, set string) scryfall.Card {
	return scryfall.Card{
		ID:       id,
		Name:     name,
		TypeLine: "Creature — Dragon",
		CMC:      6,
		SetCode:  set,
		SetName:  "Test Set",
		Rarity:   "rare",
		Prices:   scryfall.Prices{USD: "1.00"},
	}
}

func TestGet_UserAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carried no User-Agent")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := scryfall.NewClient()
	c.Rate = 0

	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestGet_RetriesOn503(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("recovered"))
	}))
	defer srv.Close()

	c := scryfall.NewClient()
	c.Rate = 0
	c.Retries = 5

	start := time.Now()
	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "recovered" {
		t.Errorf("body = %q after retries", body)
	}
	if hits != 3 {
		t.Errorf("server saw %d hits, want 3", hits)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Error("retries did not back off")
	}
}

func TestSearchCards(t *testing.T) {
	card1 := fakeCard("aaa-111", "Dragon Whelp", "lea")
	card2 := fakeCard("bbb-222", "Shivan Dragon", "lea")
	resp := map[string]any{
		"object":      "list",
		"total_cards": 2,
		"has_more":    false,
		"data":        []scryfall.Card{card1, card2},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cards/search" {
			t.Errorf("path = %q, want /cards/search", r.URL.Path)
		}
		q := r.URL.Query().Get("q")
		if q != "dragon" {
			t.Errorf("q = %q, want dragon", q)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := scryfall.NewClient()
	c.Rate = 0
	// Override BaseURL by swapping the HTTP client to point to our server.
	// We do this by patching the request URL via a transport shim.
	c.HTTP = &http.Client{
		Transport: rebaseTransport{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	cards, err := c.SearchCards(context.Background(), "dragon", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	if cards[0].Name != "Dragon Whelp" {
		t.Errorf("cards[0].Name = %q, want Dragon Whelp", cards[0].Name)
	}
}

func TestGetCardByName(t *testing.T) {
	card := fakeCard("ccc-333", "Black Lotus", "lea")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cards/named" {
			t.Errorf("path = %q, want /cards/named", r.URL.Path)
		}
		if r.URL.Query().Get("fuzzy") != "black+lotus" && r.URL.Query().Get("fuzzy") != "black lotus" {
			t.Errorf("fuzzy = %q, want black lotus", r.URL.Query().Get("fuzzy"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	}))
	defer srv.Close()

	c := scryfall.NewClient()
	c.Rate = 0
	c.HTTP = &http.Client{
		Transport: rebaseTransport{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	got, err := c.GetCardByName(context.Background(), "black lotus")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Black Lotus" {
		t.Errorf("Name = %q, want Black Lotus", got.Name)
	}
	if got.ID != "ccc-333" {
		t.Errorf("ID = %q, want ccc-333", got.ID)
	}
}

func TestGetRandomCard(t *testing.T) {
	card := fakeCard("ddd-444", "Fireball", "lea")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cards/random" {
			t.Errorf("path = %q, want /cards/random", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	}))
	defer srv.Close()

	c := scryfall.NewClient()
	c.Rate = 0
	c.HTTP = &http.Client{
		Transport: rebaseTransport{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	got, err := c.GetRandomCard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Fireball" {
		t.Errorf("Name = %q, want Fireball", got.Name)
	}
}

func TestListSets(t *testing.T) {
	sets := []scryfall.Set{
		{Code: "lea", Name: "Limited Edition Alpha", SetType: "core", ReleasedAt: "1993-08-05", CardCount: 295},
		{Code: "leb", Name: "Limited Edition Beta", SetType: "core", ReleasedAt: "1993-10-04", CardCount: 302},
		{Code: "2ed", Name: "Unlimited Edition", SetType: "core", ReleasedAt: "1993-12-01", CardCount: 302},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sets" {
			t.Errorf("path = %q, want /sets", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": sets})
	}))
	defer srv.Close()

	c := scryfall.NewClient()
	c.Rate = 0
	c.HTTP = &http.Client{
		Transport: rebaseTransport{base: srv.URL},
		Timeout:   5 * time.Second,
	}

	got, err := c.ListSets(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sets, want 2 (limit applied)", len(got))
	}
	if got[0].Code != "lea" {
		t.Errorf("sets[0].Code = %q, want lea", got[0].Code)
	}
}

// rebaseTransport rewrites every outgoing request to hit base instead of the
// original host, while preserving the path and query string. This lets us test
// Client methods against a local httptest.Server without changing BaseURL.
type rebaseTransport struct {
	base string
}

func (t rebaseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = "http"
	cloned.URL.Host = t.base[len("http://"):]
	return http.DefaultTransport.RoundTrip(cloned)
}
