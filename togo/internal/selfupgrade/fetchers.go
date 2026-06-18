package selfupgrade

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("GitHub API %d for %s", resp.StatusCode, url)
		}
		return io.ReadAll(resp.Body)
	}
	fetchBytes := func(url string, onProgress DownloadProgress) ([]byte, error) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		header(req, "")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("download failed: HTTP %d for %s", resp.StatusCode, url)
		}
		buf := &progressBuffer{total: resp.ContentLength, onProgress: onProgress}
		if _, err := io.Copy(buf, resp.Body); err != nil {
			return nil, err
		}
		return buf.data, nil
	}
	return fetchJSON, fetchBytes
}

type progressBuffer struct {
	data       []byte
	total      int64
	onProgress DownloadProgress
}

func (p *progressBuffer) Write(b []byte) (int, error) {
	p.data = append(p.data, b...)
	if p.onProgress != nil {
		p.onProgress(int64(len(p.data)), p.total)
	}
	return len(b), nil
}
