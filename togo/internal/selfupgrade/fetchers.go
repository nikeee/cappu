package selfupgrade

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nikeee/cappu/internal/httpx"
)

// githubFetchers builds authenticated GitHub API fetchers. The artifact-zip
// download follows a 302 to blob storage; Go's http client drops the
// Authorization header on the cross-host redirect, which the signed URL wants.
func githubFetchers(token string) (FetchJSON, FetchBytes) {
	header := func(req *http.Request, accept string) {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "cappu-self-upgrade")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
	}
	fetchJSON := func(url string) ([]byte, error) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		header(req, "application/vnd.github+json")
		resp, err := httpx.Client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("GitHub API %d for %s", resp.StatusCode, url)
		}
		return httpx.ReadAllCapped(resp.Body)
	}
	fetchBytes := func(url string, onProgress DownloadProgress) ([]byte, error) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		header(req, "")
		resp, err := httpx.Client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("download failed: HTTP %d for %s", resp.StatusCode, url)
		}
		return httpx.ReadAllCapped(&httpx.ProgressReader{R: resp.Body, Total: resp.ContentLength, OnProgress: onProgress})
	}
	return fetchJSON, fetchBytes
}
