package importer

// Phase 3: import a private AniList list with an access token, and the AniList
// favourites (public with the user name, or the viewer's with the token).
//
// The token is used for this one import and never stored, cached or logged: it
// only lives in the TokenClient of the request, which is dropped afterwards.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"seanime/internal/api/anilist"
	"seanime/internal/profiles/localplatform"
	"strings"

	"github.com/rs/zerolog"
)

// ErrInvalidToken: the token is malformed or AniList refused it.
var ErrInvalidToken = errors.New("PROFILE_IMPORT_INVALID_TOKEN")

const (
	// MaxTokenLength: AniList access tokens are JWTs of about 1 KB.
	MaxTokenLength = 4096
	// MaxFavouritePages caps the favourites pages read per import (50 per page and kind).
	MaxFavouritePages = 25
)

// AnilistFavourites are the ids of an AniList user's favourites, per kind.
type AnilistFavourites struct {
	Anime      []int
	Manga      []int
	Characters []int
	Staff      []int
	Studios    []int
}

func (f *AnilistFavourites) count() int {
	if f == nil {
		return 0
	}
	return len(f.Anime) + len(f.Manga) + len(f.Characters) + len(f.Staff) + len(f.Studios)
}

// ToProfile converts them to profile favourites, keeping AniList's order.
func (f *AnilistFavourites) ToProfile() []localplatform.ProfileFavourite {
	ret := make([]localplatform.ProfileFavourite, 0, f.count())
	if f == nil {
		return ret
	}
	add := func(kind string, ids []int) {
		for i, id := range ids {
			ret = append(ret, localplatform.ProfileFavourite{Kind: kind, RefID: id, Position: i})
		}
	}
	add(localplatform.FavouriteAnime, f.Anime)
	add(localplatform.FavouriteManga, f.Manga)
	add(localplatform.FavouriteCharacter, f.Characters)
	add(localplatform.FavouriteStaff, f.Staff)
	add(localplatform.FavouriteStudio, f.Studios)
	return ret
}

// FavouritesFetcher reads the public favourites of an AniList user.
type FavouritesFetcher interface {
	UserFavourites(ctx context.Context, userName string) (*AnilistFavourites, error)
}

// AccountFetcher reads the AniList account of a token's owner.
type AccountFetcher interface {
	ViewerName(ctx context.Context) (string, error)
	ViewerAnimeCollection(ctx context.Context, userName string) (*anilist.AnimeCollection, error)
	ViewerFavourites(ctx context.Context) (*AnilistFavourites, error)
}

// QueryFunc runs a raw AniList GraphQL query (anilist.CustomQuery; tests pass a fake).
type QueryFunc func(body map[string]interface{}, logger *zerolog.Logger, token string) (interface{}, error)

const favouritesSelection = `favourites {
  anime(page: $page, perPage: 50) { pageInfo { hasNextPage } nodes { id } }
  manga(page: $page, perPage: 50) { pageInfo { hasNextPage } nodes { id } }
  characters(page: $page, perPage: 50) { pageInfo { hasNextPage } nodes { id } }
  staff(page: $page, perPage: 50) { pageInfo { hasNextPage } nodes { id } }
  studios(page: $page, perPage: 50) { pageInfo { hasNextPage } nodes { id } }
}`

const userFavouritesQuery = `query ($name: String, $page: Int) { User(name: $name) { ` + favouritesSelection + ` } }`
const viewerFavouritesQuery = `query ($page: Int) { Viewer { ` + favouritesSelection + ` } }`

// fetchFavourites pages through a favourites query. root is "User" or "Viewer".
func fetchFavourites(query QueryFunc, logger *zerolog.Logger, gql string, vars map[string]interface{}, root string, token string) (*AnilistFavourites, error) {
	ret := &AnilistFavourites{}
	for page := 1; page <= MaxFavouritePages; page++ {
		v := map[string]interface{}{"page": page}
		for k, val := range vars {
			v[k] = val
		}
		data, err := query(map[string]interface{}{"query": gql, "variables": v}, logger, token)
		if err != nil {
			return nil, err
		}
		more, err := parseFavouritesPage(data, root, ret)
		if err != nil {
			return nil, err
		}
		if !more {
			break
		}
	}
	return ret, nil
}

// parseFavouritesPage adds one page of a favourites response to ret and reports
// whether any kind has another page.
func parseFavouritesPage(data interface{}, root string, ret *AnilistFavourites) (bool, error) {
	m, _ := data.(map[string]interface{})
	user, _ := m[root].(map[string]interface{})
	if user == nil {
		return false, fmt.Errorf("%w: no %s in the AniList response", ErrListUnavailable, root)
	}
	favs, _ := user["favourites"].(map[string]interface{})
	more := false
	for key, dst := range map[string]*[]int{"anime": &ret.Anime, "manga": &ret.Manga, "characters": &ret.Characters, "staff": &ret.Staff, "studios": &ret.Studios} {
		conn, _ := favs[key].(map[string]interface{})
		if conn == nil {
			continue
		}
		nodes, _ := conn["nodes"].([]interface{})
		for _, n := range nodes {
			node, _ := n.(map[string]interface{})
			if id, ok := node["id"].(float64); ok && id > 0 {
				*dst = append(*dst, int(id))
			}
		}
		if pi, _ := conn["pageInfo"].(map[string]interface{}); pi != nil {
			if next, _ := pi["hasNextPage"].(bool); next && len(nodes) > 0 {
				more = true
			}
		}
	}
	return more, nil
}

// ClientQuery runs queries through an anonymous AniList client (its CustomQuery).
func ClientQuery(client anilist.AnilistClient) QueryFunc {
	return func(body map[string]interface{}, logger *zerolog.Logger, token string) (interface{}, error) {
		if token != "" || client == nil || client.IsAuthenticated() {
			return nil, errors.New("importer: public queries must be anonymous")
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return client.CustomQuery(raw, logger)
	}
}

// PublicFavourites reads public favourites anonymously.
type PublicFavourites struct {
	Query  QueryFunc // nil = anilist.CustomQuery
	Logger *zerolog.Logger
}

func (f PublicFavourites) UserFavourites(ctx context.Context, userName string) (*AnilistFavourites, error) {
	query := f.Query
	if query == nil {
		query = anilist.CustomQuery
	}
	return fetchFavourites(query, loggerOr(f.Logger), userFavouritesQuery, map[string]interface{}{"name": userName}, "User", "")
}

func loggerOr(l *zerolog.Logger) *zerolog.Logger {
	if l != nil {
		return l
	}
	nop := zerolog.Nop()
	return &nop
}

// TokenClient reads the account of an AniList access token. Create one per import.
type TokenClient struct {
	token  string
	client *anilist.AnilistClientImpl
	query  QueryFunc
	logger *zerolog.Logger
}

// NormalizeToken trims a pasted token (with or without "Bearer ") and checks its shape.
func NormalizeToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	token = strings.TrimSpace(strings.TrimPrefix(token, "Bearer "))
	if len(token) < 20 || len(token) > MaxTokenLength || strings.ContainsAny(token, " \t\r\n\"'") {
		return "", ErrInvalidToken
	}
	return token, nil
}

// NewTokenClient builds a client for one import. No cache directory: nothing it
// fetches is written to disk.
func NewTokenClient(token string, logger *zerolog.Logger) (*TokenClient, error) {
	token, err := NormalizeToken(token)
	if err != nil {
		return nil, err
	}
	return &TokenClient{token: token, client: anilist.NewAnilistClient(token, ""), query: anilist.CustomQuery, logger: loggerOr(logger)}, nil
}

func (c *TokenClient) ViewerName(ctx context.Context) (string, error) {
	v, err := c.client.GetViewer(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if v.GetViewer() == nil || v.GetViewer().GetName() == "" {
		return "", ErrInvalidToken
	}
	return v.GetViewer().GetName(), nil
}

func (c *TokenClient) ViewerAnimeCollection(ctx context.Context, userName string) (*anilist.AnimeCollection, error) {
	// With the owner's token AniList includes the private entries and lists.
	return c.client.Client.AnimeCollection(ctx, &userName)
}

func (c *TokenClient) ViewerFavourites(ctx context.Context) (*AnilistFavourites, error) {
	return fetchFavourites(c.query, c.logger, viewerFavouritesQuery, nil, "Viewer", c.token)
}

// AnilistSources are the AniList readers of an import (fakes in tests).
type AnilistSources struct {
	Public           AnimeCollectionFetcher
	PublicFavourites FavouritesFetcher
	// NewAccount builds the reader for a token (NewTokenClient).
	NewAccount func(token string) (AccountFetcher, error)
}

// AnilistImportOptions selects what to import. With Token, the token owner's list
// (private entries included) is imported and Username is ignored.
type AnilistImportOptions struct {
	Username   string
	Token      string
	Mode       localplatform.ImportMode
	Favourites bool
}

// ImportAnilist imports an AniList anime list (public by user name, or private with
// a token) and optionally the user's favourites.
func ImportAnilist(ctx context.Context, p *localplatform.Platform, src AnilistSources, opts AnilistImportOptions) (*localplatform.ProfileImportResult, error) {
	var (
		col     *anilist.AnimeCollection
		favs    *AnilistFavourites
		favErr  error
		source  string
		hasList bool
	)
	if strings.TrimSpace(opts.Token) != "" {
		if src.NewAccount == nil {
			return nil, errors.New("importer: token imports are not available")
		}
		acc, err := src.NewAccount(opts.Token)
		if err != nil {
			return nil, err
		}
		name, err := acc.ViewerName(ctx)
		if err != nil {
			return nil, err
		}
		col, err = acc.ViewerAnimeCollection(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrListUnavailable, err)
		}
		if opts.Favourites {
			favs, favErr = acc.ViewerFavourites(ctx)
		}
		source = "anilist-token:" + name
	} else {
		username := strings.TrimSpace(opts.Username)
		if !usernameRe.MatchString(username) {
			return nil, ErrInvalidUsername
		}
		if src.Public == nil {
			return nil, errors.New("importer: no AniList client")
		}
		var err error
		col, err = src.Public.AnimeCollection(ctx, &username)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrListUnavailable, err)
		}
		if opts.Favourites && src.PublicFavourites != nil {
			favs, favErr = src.PublicFavourites.UserFavourites(ctx, username)
		}
		source = "anilist:" + username
	}

	entries := localplatform.FromAnilistCollection(col)
	hasList = len(entries) > 0
	if !hasList && favs.count() == 0 {
		if favErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrListUnavailable, favErr)
		}
		return nil, ErrNothingToImport
	}
	if len(entries) > MaxImportEntries {
		return nil, fmt.Errorf("%w: too many entries", ErrInvalidFile)
	}

	res := &localplatform.ProfileImportResult{}
	if hasList {
		var err error
		if res, err = p.Import(ctx, entries, opts.Mode, source); err != nil {
			return nil, err
		}
	}
	if favErr != nil {
		res.Errors = append(res.Errors, "favourites: "+favErr.Error())
	} else if favs.count() > 0 {
		n, err := p.ImportFavourites(ctx, favs.ToProfile(), opts.Mode)
		if err != nil {
			return nil, err
		}
		res.Favourites = n
	}
	return res, nil
}
