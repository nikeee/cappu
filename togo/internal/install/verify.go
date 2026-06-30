package install

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikeee/cappu/internal/lockfile"
)

// CacheVerifyResult is the outcome of VerifyCache; paths are relative to the
// package store. Port of CacheVerifyResult in src/install.ts.
type CacheVerifyResult struct {
	// OK: cache files whose bytes match their recorded hash.
	OK []string
	// Modified: present but the bytes do not match the recorded hash.
	Modified []string
	// Missing: a hash is recorded but the file it covers is gone.
	Missing []string
}

// VerifyCache checks every cached artifact in the package store against the hash
// recorded beside it: each jar against its `.sha256` sidecar, each cached POM
// against the `pomSha256` in its metadata.json. Read-only: nothing is downloaded
// or removed. Files with no recorded hash are skipped. Port of verifyCache.
func VerifyCache() CacheVerifyResult {
	root := packageStoreDir()
	result := CacheVerifyResult{}
	rel := func(p string) string {
		r, err := filepath.Rel(root, p)
		if err != nil {
			return p
		}
		return r
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch {
		case strings.HasSuffix(path, ".jar.sha256"):
			// an orphaned sidecar: its jar was deleted out from under the cache
			jar := strings.TrimSuffix(path, ".sha256")
			if _, statErr := os.Stat(jar); os.IsNotExist(statErr) {
				result.Missing = append(result.Missing, rel(jar))
			}
		case strings.HasSuffix(path, ".jar"):
			want, rerr := os.ReadFile(path + ".sha256")
			if rerr != nil {
				return nil // unverifiable: no recorded hash
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			if string(lockfile.Sha256Of(data)) == string(want) {
				result.OK = append(result.OK, rel(path))
			} else {
				result.Modified = append(result.Modified, rel(path))
			}
		case strings.HasSuffix(path, "metadata.json"):
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			var e struct {
				PomSha256 string `json:"pomSha256"`
			}
			if json.Unmarshal(data, &e) != nil || e.PomSha256 == "" {
				return nil
			}
			// The POM sits beside its metadata.json; the version dir and artifact
			// dir name it (<artifact>-<version>.pom).
			dir := filepath.Dir(path)
			pom := filepath.Join(dir, filepath.Base(filepath.Dir(dir))+"-"+filepath.Base(dir)+".pom")
			pdata, rerr := os.ReadFile(pom)
			if os.IsNotExist(rerr) {
				result.Missing = append(result.Missing, rel(pom))
				return nil
			}
			if rerr != nil {
				return nil
			}
			if string(lockfile.Sha256Of(pdata)) == e.PomSha256 {
				result.OK = append(result.OK, rel(pom))
			} else {
				result.Modified = append(result.Modified, rel(pom))
			}
		}
		return nil
	})
	return result
}
