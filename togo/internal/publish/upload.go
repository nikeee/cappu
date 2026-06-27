package publish

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/nikeee/cappu/internal/config"
	"github.com/nikeee/cappu/internal/httpx"
	"github.com/nikeee/cappu/internal/packages"
)

// ResolvePublishRegistry resolves the registry npm-style (highest wins): the
// --repo flag, then $CAPPU_PUBLISH_REGISTRY, then cappu.json's
// publishRepository, then the built-in default (Maven Central).
func ResolvePublishRegistry(flag, configRepo, envRegistry string) string {
	if flag != "" {
		return flag
	}
	if envRegistry != "" {
		return envRegistry
	}
	if configRepo != "" {
		return configRepo
	}
	return config.DefaultPublishRegistry
}

// BearerToken is a registry bearer credential, distinct from a plain string.
type BearerToken string

// Auth is publishing credentials: Basic (username+password) or Bearer (token).
type Auth struct {
	Basic    bool
	Username string
	Password string
	Token    BearerToken
}

// ResolvePublishAuth reads credentials from env: Basic when a username+password
// pair is set, else Bearer from a token, else ok=false (the CLI then errors).
func ResolvePublishAuth(username, password, token string) (Auth, bool) {
	if username != "" && password != "" {
		return Auth{Basic: true, Username: username, Password: password}, true
	}
	if token != "" {
		return Auth{Token: BearerToken(token)}, true
	}
	return Auth{}, false
}

func (a Auth) header() string {
	if a.Basic {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(a.Username+":"+a.Password))
	}
	return "Bearer " + string(a.Token)
}

// Maven2Path is the maven2 path for a file: group dots become directories.
func Maven2Path(c packages.Coordinates, filename string) string {
	segments := append(strings.Split(string(c.GroupID), "."), string(c.ArtifactID), string(c.Version), filename)
	return strings.Join(segments, "/")
}

// File is one artifact to upload.
type File struct {
	Filename string
	Bytes    []byte
}

// PutFn uploads bytes to a url with an optional Authorization header.
type PutFn func(url string, body []byte, authorization string) error

func defaultPut(url string, body []byte, authorization string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/octet-stream")
	if authorization != "" {
		req.Header.Set("authorization", authorization)
	}
	resp, err := httpx.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload failed: HTTP %d for %s", resp.StatusCode, url)
	}
	return nil
}

// Options configures a publish run.
type Options struct {
	Repo        string
	Coordinates packages.Coordinates
	Files       []File
	Auth        *Auth
	Put         PutFn
	OnUpload    func(url string)
}

// hexBytes returns the ASCII bytes of a hex digest (the sidecar file content).
func checksumSidecars(file File) []File {
	md5sum := md5.Sum(file.Bytes)
	sha1sum := sha1.Sum(file.Bytes)
	return []File{
		{Filename: file.Filename + ".md5", Bytes: []byte(hex.EncodeToString(md5sum[:]))},
		{Filename: file.Filename + ".sha1", Bytes: []byte(hex.EncodeToString(sha1sum[:]))},
	}
}

// PublishArtifacts PUTs every file (and its md5/sha1 sidecars) to repo under the
// maven2 layout for the coordinates. Returns the uploaded urls in order; stops
// on the first failure.
func PublishArtifacts(opts Options) ([]string, error) {
	put := opts.Put
	if put == nil {
		put = defaultPut
	}
	authorization := ""
	if opts.Auth != nil {
		authorization = opts.Auth.header()
	}
	base := strings.TrimSuffix(opts.Repo, "/") + "/"
	var uploaded []string
	for _, file := range opts.Files {
		for _, f := range append([]File{file}, checksumSidecars(file)...) {
			url := base + Maven2Path(opts.Coordinates, f.Filename)
			if opts.OnUpload != nil {
				opts.OnUpload(url)
			}
			if err := put(url, f.Bytes, authorization); err != nil {
				return uploaded, err
			}
			uploaded = append(uploaded, url)
		}
	}
	return uploaded, nil
}
