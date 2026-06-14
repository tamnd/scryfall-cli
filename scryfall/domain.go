package scryfall

import (
	"context"
	"strings"

	"github.com/tamnd/any-cli/kit"
	"github.com/tamnd/any-cli/kit/errs"
)

// domain.go exposes Scryfall as a kit Domain: a driver that a multi-domain
// host (ant) enables with a single blank import,
//
//	import _ "github.com/tamnd/scryfall-cli/scryfall"
//
// exactly as a database/sql program enables a driver with `import _
// "github.com/lib/pq"`. The init below registers it; the host then dereferences
// scryfall:// URIs by routing to the operations Register installs. The same
// Domain also builds the standalone scryfall binary (see cli.NewApp), so the
// binary and a host share one source of truth.
func init() { kit.Register(Domain{}) }

// Domain is the Scryfall driver. It carries no state; the per-run client is
// built by the factory Register hands kit.
type Domain struct{}

// Info describes the scheme, the hostnames a pasted link is matched against, and
// the identity reused for the binary's help and version.
func (Domain) Info() kit.DomainInfo {
	return kit.DomainInfo{
		Scheme: "scryfall",
		Hosts:  []string{Host},
		Identity: kit.Identity{
			Binary: "scryfall",
			Short:  "A command line for the Scryfall MTG card API.",
			Long: `A command line for the Scryfall MTG card API.

scryfall reads public Magic: The Gathering card data from api.scryfall.com
over plain HTTPS, shapes it into clean records, and prints output that pipes
into the rest of your tools. No API key required.`,
			Site: "scryfall.com",
			Repo: "https://github.com/tamnd/scryfall-cli",
		},
	}
}

// Register installs the client factory and every operation onto app.
func (Domain) Register(app *kit.App) {
	app.SetClient(newClient)

	// search: find cards by Scryfall query syntax
	kit.Handle(app, kit.OpMeta{Name: "search", Group: "read",
		Summary: "Search cards by Scryfall query syntax",
		Args:    []kit.Arg{{Name: "query", Help: "Scryfall search query (e.g. 'dragon power>5', 'set:cmr type:legendary')"}}}, searchCards)

	// card: fetch a single card by fuzzy name, the resolver for "card" URIs
	kit.Handle(app, kit.OpMeta{Name: "card", Group: "read", Single: true,
		Summary: "Fetch a card by name (fuzzy match)", URIType: "card", Resolver: true,
		Args: []kit.Arg{{Name: "name", Help: "card name (fuzzy match)"}}}, getCard)

	// random: fetch a random card
	kit.Handle(app, kit.OpMeta{Name: "random", Group: "read",
		Summary: "Fetch a random card"}, randomCard)

	// sets: list all MTG sets
	kit.Handle(app, kit.OpMeta{Name: "sets", Group: "read", List: true,
		Summary: "List Magic: The Gathering sets", URIType: "set"}, listSets)
}

// newClient builds the client from the host-resolved config.
func newClient(_ context.Context, cfg kit.Config) (any, error) {
	c := NewClient()
	if cfg.UserAgent != "" {
		c.UserAgent = cfg.UserAgent
	}
	if cfg.Rate > 0 {
		c.Rate = cfg.Rate
	}
	if cfg.Retries > 0 {
		c.Retries = cfg.Retries
	}
	if cfg.Timeout > 0 {
		c.HTTP.Timeout = cfg.Timeout
	}
	return c, nil
}

// --- inputs ---

type searchInput struct {
	Query  string  `kit:"arg" help:"Scryfall search query (e.g. 'dragon power>5', 'set:cmr type:legendary')"`
	Limit  int     `kit:"flag,inherit" help:"max cards" default:"20"`
	Client *Client `kit:"inject"`
}

type cardInput struct {
	Name   string  `kit:"arg" help:"card name (fuzzy match)"`
	Client *Client `kit:"inject"`
}

type randomInput struct {
	Client *Client `kit:"inject"`
}

type setsInput struct {
	Limit  int     `kit:"flag,inherit" help:"max sets" default:"20"`
	Client *Client `kit:"inject"`
}

// --- handlers ---

func searchCards(ctx context.Context, in searchInput, emit func(*Card) error) error {
	cards, err := in.Client.SearchCards(ctx, in.Query, in.Limit)
	if err != nil {
		return mapErr(err)
	}
	for i := range cards {
		if err := emit(&cards[i]); err != nil {
			return err
		}
	}
	return nil
}

func getCard(ctx context.Context, in cardInput, emit func(*Card) error) error {
	card, err := in.Client.GetCardByName(ctx, in.Name)
	if err != nil {
		return mapErr(err)
	}
	return emit(card)
}

func randomCard(ctx context.Context, in randomInput, emit func(*Card) error) error {
	card, err := in.Client.GetRandomCard(ctx)
	if err != nil {
		return mapErr(err)
	}
	return emit(card)
}

func listSets(ctx context.Context, in setsInput, emit func(*Set) error) error {
	sets, err := in.Client.ListSets(ctx, in.Limit)
	if err != nil {
		return mapErr(err)
	}
	for i := range sets {
		if err := emit(&sets[i]); err != nil {
			return err
		}
	}
	return nil
}

// --- Resolver: URI-native string functions, pure and network-free ---

// Classify turns any accepted input into the canonical (type, id), so
// `ant resolve` and `ant url` touch no network.
// A 36-character UUID (with dashes) is a "card-id"; anything that looks like a
// scryfall.com URL is a "card-id" too; otherwise it is a "card" (name lookup).
func (Domain) Classify(input string) (uriType, id string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", errs.Usage("empty scryfall reference")
	}
	// Full scryfall.com card URL: https://scryfall.com/card/<set>/<num>/<name>
	if strings.HasPrefix(input, "https://scryfall.com/card/") ||
		strings.HasPrefix(input, "http://scryfall.com/card/") {
		id = strings.TrimPrefix(strings.TrimPrefix(input, "https://"), "http://")
		id = strings.TrimPrefix(id, "scryfall.com/card/")
		return "card-id", id, nil
	}
	// 36-char UUID with dashes: card UUID from the API
	if len(input) == 36 && strings.Count(input, "-") == 4 {
		return "card-id", input, nil
	}
	// Everything else: treat as a card name for fuzzy lookup
	return "card", input, nil
}

// Locate is the inverse: the live https URL for a (type, id).
func (Domain) Locate(uriType, id string) (string, error) {
	switch uriType {
	case "card":
		return "https://scryfall.com/search?q=" + strings.ReplaceAll(id, " ", "+"), nil
	case "card-id":
		return "https://scryfall.com/card/" + id, nil
	case "set":
		return "https://scryfall.com/sets/" + id, nil
	default:
		return "", errs.Usage("scryfall has no resource type %q", uriType)
	}
}

// mapErr converts a library error into the kit error kind that carries the right
// exit code.
func mapErr(err error) error {
	return err
}
