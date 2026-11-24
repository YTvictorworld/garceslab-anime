package trackprobe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultCacheTTL: a file in a torrent never changes, so results are kept long.
const DefaultCacheTTL = 180 * 24 * time.Hour

// Cache keeps probe results on disk, one small JSON file per probed file
// (named by a hash of the key, so no release or file name is in the path).
type Cache struct {
	dir string
	ttl time.Duration
	now func() time.Time
	mu  sync.Mutex
}

type cacheEntry struct {
	Key      string  `json:"key"`
	ProbedAt int64   `json:"probedAt"`
	Tracks   *Tracks `json:"tracks"`
}

// NewCache returns a cache in dir ("" = memory-less no-op cache).
func NewCache(dir string, ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &Cache{dir: dir, ttl: ttl, now: time.Now}
}

// Key builds the cache key of one file of a torrent at a debrid provider.
func Key(provider, infoHash, filePath string) string {
	return strings.ToLower(provider) + "|" + strings.ToLower(infoHash) + "|" + filePath
}

func (c *Cache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:16])+".json")
}

// Get returns the cached tracks of key, if fresh.
func (c *Cache) Get(key string) (*Tracks, bool) {
	if c == nil || c.dir == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.path(key)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	var e cacheEntry
	if json.Unmarshal(b, &e) != nil || e.Key != key || e.Tracks == nil {
		return nil, false
	}
	if c.now().Sub(time.Unix(e.ProbedAt, 0)) > c.ttl {
		_ = os.Remove(p)
		return nil, false
	}
	return e.Tracks, true
}

// Put stores the tracks of key.
func (c *Cache) Put(key string, tracks *Tracks) error {
	if c == nil || c.dir == "" || tracks == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(cacheEntry{Key: key, ProbedAt: c.now().Unix(), Tracks: tracks})
	if err != nil {
		return err
	}
	p := c.path(key)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
