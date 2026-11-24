package torbox

// Garceslab Anime: direct links to files TorBox already has cached, for the
// subtitle-aware auto-select (it reads the MKV header of a few candidates).
// Nothing here starts a download: a torrent that isn't in the account yet is
// added with add_only_if_cached, and removed again unless the caller keeps it.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"seanime/internal/debrid/debrid"
	"strconv"
	"strings"
	"sync"
	"time"
)

var _ debrid.CachedFileLinker = (*TorBox)(nil)

// readyPolls is how many times a just-added cached torrent is checked before
// giving up (TorBox usually marks it ready at once).
const readyPolls = 3

func (t *TorBox) CachedFileLink(ctx context.Context, opts debrid.CachedFileLinkOptions) (*debrid.CachedFileLink, error) {
	if opts.InfoHash == "" || opts.FileId == "" {
		return nil, fmt.Errorf("torbox: missing info hash or file")
	}
	apiKey, found := t.apiKey.Get()
	if !found {
		return nil, debrid.ErrNotAuthenticated
	}

	id, created, err := t.findOrAddCached(ctx, opts)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	release := func(keep bool) {
		once.Do(func() {
			if !created || keep {
				return
			}
			go func() {
				if err := t.DeleteTorrent(strconv.Itoa(id)); err != nil {
					t.logger.Warn().Str("error", cleanErr(err).Error()).Msg("torbox: Could not remove a probed torrent")
				}
			}()
		})
	}
	fail := func(err error) (*debrid.CachedFileLink, error) {
		release(false)
		return nil, err
	}

	torrent, err := t.waitReady(ctx, id)
	if err != nil {
		return fail(err)
	}
	fileId := ""
	for _, f := range torrent.Files {
		if f.ShortName == opts.FileId || strings.HasSuffix(f.Name, "/"+opts.FileId) || f.Name == opts.FileId {
			fileId = strconv.Itoa(f.ID)
			break
		}
	}
	if fileId == "" {
		return fail(fmt.Errorf("torbox: file not found in torrent"))
	}

	q := url.Values{}
	q.Set("token", apiKey)
	q.Set("torrent_id", strconv.Itoa(id))
	q.Set("file_id", fileId)
	resp, err := t.doQueryCtx(ctx, "GET", t.baseUrl+"/torrents/requestdl?"+q.Encode(), nil, "application/json")
	if err != nil {
		return fail(cleanErr(err))
	}
	var link string
	b, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(b, &link); err != nil || link == "" {
		return fail(fmt.Errorf("torbox: no download link"))
	}
	if link, err = normalizeDownloadUrl(link); err != nil {
		return fail(fmt.Errorf("torbox: bad download link"))
	}
	return &debrid.CachedFileLink{URL: link, Release: release}, nil
}

// findOrAddCached returns the account's torrent with this hash, or adds it
// only if TorBox has it cached. created tells whether it was added here.
func (t *TorBox) findOrAddCached(ctx context.Context, opts debrid.CachedFileLinkOptions) (id int, created bool, err error) {
	resp, err := t.doQueryCtx(ctx, "GET", t.baseUrl+"/torrents/mylist?bypass_cache=true", nil, "application/json")
	if err != nil {
		return 0, false, cleanErr(err)
	}
	var list []*Torrent
	b, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(b, &list); err != nil {
		return 0, false, fmt.Errorf("torbox: could not parse the torrent list")
	}
	for _, tr := range list {
		if strings.EqualFold(tr.Hash, opts.InfoHash) {
			return tr.ID, false, nil
		}
	}

	magnet := opts.MagnetLink
	if magnet == "" {
		magnet = "magnet:?xt=urn:btih:" + opts.InfoHash
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("magnet", magnet)
	_ = w.WriteField("add_only_if_cached", "true")
	_ = w.Close()
	resp, err = t.doQueryCtx(ctx, "POST", t.baseUrl+"/torrents/createtorrent", &body, w.FormDataContentType())
	if err != nil {
		err = cleanErr(err)
		if errors.Is(err, debrid.ErrRateLimited) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return 0, false, err
		}
		return 0, false, fmt.Errorf("%w: %s", debrid.ErrNotCached, err.Error())
	}
	var d struct {
		ID int `json:"torrent_id"`
	}
	b, _ = json.Marshal(resp.Data)
	if err := json.Unmarshal(b, &d); err != nil || d.ID == 0 {
		// Queued (account limit) or not added: nothing usable.
		return 0, false, debrid.ErrNotCached
	}
	return d.ID, true, nil
}

func (t *TorBox) waitReady(ctx context.Context, id int) (*Torrent, error) {
	for i := 0; i < readyPolls; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(600 * time.Millisecond):
			}
		}
		resp, err := t.doQueryCtx(ctx, "GET", t.baseUrl+fmt.Sprintf("/torrents/mylist?bypass_cache=true&id=%d", id), nil, "application/json")
		if err != nil {
			return nil, cleanErr(err)
		}
		var tr Torrent
		b, _ := json.Marshal(resp.Data)
		if err := json.Unmarshal(b, &tr); err != nil {
			return nil, fmt.Errorf("torbox: could not parse the torrent")
		}
		if tr.DownloadPresent || tr.DownloadFinished {
			return &tr, nil
		}
	}
	return nil, debrid.ErrNotCached
}

// cleanErr drops the request URL (it can carry the API token) from errors.
func cleanErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if errors.Is(ue.Err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		if errors.Is(ue.Err, context.Canceled) {
			return context.Canceled
		}
		return fmt.Errorf("torbox: request failed: %s", ue.Err.Error())
	}
	return err
}
