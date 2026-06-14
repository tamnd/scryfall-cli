// Package scryfall is the library behind the scryfall command line:
// the HTTP client, request shaping, and the typed data models for the
// Scryfall MTG card API.
//
// The Client here is the spine every command shares. It sets a real
// User-Agent, paces requests so a busy session stays polite (Scryfall
// asks for 50-100ms between requests), and retries the transient
// failures (429 and 5xx) that any public API throws under load.
package scryfall

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DefaultUserAgent identifies the client to Scryfall. Scryfall's docs ask for a
// real, honest User-Agent so they can contact you if something goes wrong.
const DefaultUserAgent = "scryfall-cli/dev (+https://github.com/tamnd/scryfall-cli)"

// Host is the API host this client talks to, and the host the URI driver in
// domain.go claims.
const Host = "api.scryfall.com"

// BaseURL is the root every request is built from.
const BaseURL = "https://" + Host

// Prices holds the market prices for a card in common currencies.
type Prices struct {
	USD     string `json:"usd"`
	USDFoil string `json:"usd_foil"`
	EUR     string `json:"eur"`
}

// Card is a single Magic: The Gathering card as returned by the Scryfall API.
type Card struct {
	ID          string   `kit:"id" json:"id"`
	Name        string   `json:"name"`
	ManaCost    string   `json:"mana_cost"`
	CMC         float64  `json:"cmc"`
	TypeLine    string   `json:"type_line"`
	OracleText  string   `json:"oracle_text"`
	Power       string   `json:"power"`
	Toughness   string   `json:"toughness"`
	Colors      []string `json:"colors"`
	SetCode     string   `json:"set"`
	SetName     string   `json:"set_name"`
	Rarity      string   `json:"rarity"`
	Prices      Prices   `json:"prices"`
	ScryfallURL string   `json:"scryfall_uri"`
	ReleasedAt  string   `json:"released_at"`
}

// Set is a Magic: The Gathering card set as returned by the Scryfall API.
type Set struct {
	Code       string `kit:"id" json:"code"`
	Name       string `json:"name"`
	SetType    string `json:"set_type"`
	ReleasedAt string `json:"released_at"`
	CardCount  int    `json:"card_count"`
}

// cardListResponse wraps the paginated list returned by /cards/search.
type cardListResponse struct {
	TotalCards int    `json:"total_cards"`
	HasMore    bool   `json:"has_more"`
	Data       []Card `json:"data"`
}

// setListResponse wraps the list returned by /sets.
type setListResponse struct {
	Data []Set `json:"data"`
}

// Client talks to the Scryfall API over HTTPS.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	// Rate is the minimum gap between requests. Zero means no pacing.
	Rate    time.Duration
	Retries int

	last time.Time
}

// NewClient returns a Client with sensible defaults: a 30s timeout, a 100ms
// minimum gap between requests (Scryfall allows ~10 req/sec), and five retries
// on transient errors.
func NewClient() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: DefaultUserAgent,
		Rate:      100 * time.Millisecond,
		Retries:   5,
	}
}

// SearchCards searches for cards matching query. limit caps the results; the
// max per_page is 175 (one Scryfall page), so we do not paginate for simplicity.
func (c *Client) SearchCards(ctx context.Context, query string, limit int) ([]Card, error) {
	perPage := limit
	if perPage > 175 {
		perPage = 175
	}
	if perPage < 1 {
		perPage = 20
	}
	u := fmt.Sprintf("%s/cards/search?q=%s&per_page=%d", BaseURL, url.QueryEscape(query), perPage)
	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	var resp cardListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode cards: %w", err)
	}
	cards := resp.Data
	if limit > 0 && len(cards) > limit {
		cards = cards[:limit]
	}
	return cards, nil
}

// GetCardByName fetches a single card by fuzzy name match via /cards/named.
func (c *Client) GetCardByName(ctx context.Context, name string) (*Card, error) {
	u := fmt.Sprintf("%s/cards/named?fuzzy=%s", BaseURL, url.QueryEscape(name))
	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	var card Card
	if err := json.Unmarshal(body, &card); err != nil {
		return nil, fmt.Errorf("decode card: %w", err)
	}
	return &card, nil
}

// GetRandomCard fetches a random card from /cards/random.
func (c *Client) GetRandomCard(ctx context.Context) (*Card, error) {
	u := BaseURL + "/cards/random"
	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	var card Card
	if err := json.Unmarshal(body, &card); err != nil {
		return nil, fmt.Errorf("decode card: %w", err)
	}
	return &card, nil
}

// ListSets fetches all sets from /sets and returns up to limit results.
func (c *Client) ListSets(ctx context.Context, limit int) ([]Set, error) {
	u := BaseURL + "/sets"
	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	var resp setListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode sets: %w", err)
	}
	sets := resp.Data
	if limit > 0 && len(sets) > limit {
		sets = sets[:limit]
	}
	return sets, nil
}

// Get fetches url and returns the response body. It paces and retries according
// to the client's settings. The caller owns nothing extra; the body is read
// fully and closed here.
func (c *Client) Get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, url)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", url, lastErr)
}

func (c *Client) do(ctx context.Context, url string) (body []byte, retry bool, err error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	return b, false, nil
}

// pace blocks until at least Rate has passed since the previous request.
func (c *Client) pace() {
	if c.Rate <= 0 {
		return
	}
	if wait := c.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}
