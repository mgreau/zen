package prcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"fmt"

	"github.com/mgreau/zen/internal/config"
)

// PRMeta holds cached PR metadata for display purposes.
type PRMeta struct {
	Title  string `json:"title"`
	Author string `json:"author"`
}

func cacheFile() string {
	return filepath.Join(config.StateDir(), "pr_cache.json")
}

// Load reads the PR cache from disk. Returns an empty map on any error.
func Load() map[string]PRMeta {
	data, err := os.ReadFile(cacheFile())
	if err != nil {
		return make(map[string]PRMeta)
	}
	var cache map[string]PRMeta
	if err := json.Unmarshal(data, &cache); err != nil {
		return make(map[string]PRMeta)
	}
	return cache
}

// Save writes the PR cache to disk (best-effort).
func Save(cache map[string]PRMeta) {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(cacheFile()), 0o755)
	os.WriteFile(cacheFile(), data, 0o644)
}

// Get looks up PR metadata by repo short name and PR number.
func Get(repo string, pr int) (PRMeta, bool) {
	cache := Load()
	key := fmt.Sprintf("%s/%d", repo, pr)
	meta, ok := cache[key]
	return meta, ok
}

// Set stores PR metadata for the given repo and PR number.
func Set(repo string, pr int, title, author string) {
	cache := Load()
	key := fmt.Sprintf("%s/%d", repo, pr)
	cache[key] = PRMeta{Title: title, Author: author}
	Save(cache)
}
