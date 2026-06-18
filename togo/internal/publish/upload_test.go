package publish

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"

	"github.com/nikeee/cappu/internal/packages"
)

var coords = packages.NewCoordinates("com.example", "my-lib", "1.2.0")

func TestMaven2Path(t *testing.T) {
	if got := Maven2Path(coords, "my-lib-1.2.0.jar"); got != "com/example/my-lib/1.2.0/my-lib-1.2.0.jar" {
		t.Errorf("Maven2Path = %q", got)
	}
}

func TestResolvePublishAuth(t *testing.T) {
	if a, ok := ResolvePublishAuth("u", "p", ""); !ok || !a.Basic || a.Username != "u" || a.Password != "p" {
		t.Errorf("basic = %+v ok=%v", a, ok)
	}
	if a, ok := ResolvePublishAuth("", "", "t"); !ok || a.Basic || a.Token != "t" {
		t.Errorf("bearer = %+v ok=%v", a, ok)
	}
	if _, ok := ResolvePublishAuth("", "", ""); ok {
		t.Error("no creds should be ok=false")
	}
}

func TestResolvePublishRegistry(t *testing.T) {
	flag, cfg, env := "https://flag.example/releases", "https://config.example/releases", "https://env.example/releases"
	if got := ResolvePublishRegistry(flag, cfg, env); got != flag {
		t.Errorf("--repo should win, got %q", got)
	}
	if got := ResolvePublishRegistry("", cfg, env); got != env {
		t.Errorf("env should win over config, got %q", got)
	}
	if got := ResolvePublishRegistry("", cfg, ""); got != cfg {
		t.Errorf("config should win, got %q", got)
	}
	if got := ResolvePublishRegistry("", "", ""); got != "https://repo.maven.apache.org/maven2" {
		t.Errorf("default should be Maven Central, got %q", got)
	}
}

func TestPublishArtifactsUploadsWithSidecars(t *testing.T) {
	jar := []byte("jar-bytes")
	type call struct {
		url, authorization string
		body               []byte
	}
	var calls []call
	put := func(url string, body []byte, authorization string) error {
		calls = append(calls, call{url, authorization, body})
		return nil
	}
	uploaded, err := PublishArtifacts(Options{
		Repo:        "https://maven.example.com/releases",
		Coordinates: coords,
		Files:       []File{{Filename: "my-lib-1.2.0.jar", Bytes: jar}},
		Auth:        &Auth{Basic: true, Username: "u", Password: "p"},
		Put:         put,
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := "https://maven.example.com/releases/com/example/my-lib/1.2.0"
	want := []string{dir + "/my-lib-1.2.0.jar", dir + "/my-lib-1.2.0.jar.md5", dir + "/my-lib-1.2.0.jar.sha1"}
	if !reflect.DeepEqual(uploaded, want) {
		t.Errorf("uploaded = %v", uploaded)
	}
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("u:p"))
	for _, c := range calls {
		if c.authorization != basic {
			t.Errorf("missing/incorrect auth header: %q", c.authorization)
		}
	}
	md5sum := md5.Sum(jar)
	sha1sum := sha1.Sum(jar)
	if string(calls[1].body) != hex.EncodeToString(md5sum[:]) {
		t.Errorf("md5 sidecar = %q", calls[1].body)
	}
	if string(calls[2].body) != hex.EncodeToString(sha1sum[:]) {
		t.Errorf("sha1 sidecar = %q", calls[2].body)
	}
}

func TestPublishArtifactsStopsOnFailure(t *testing.T) {
	put := func(url string, body []byte, authorization string) error {
		return errors.New("HTTP 401")
	}
	_, err := PublishArtifacts(Options{
		Repo:        "https://maven.example.com/releases",
		Coordinates: coords,
		Files:       []File{{Filename: "my-lib-1.2.0.jar", Bytes: []byte{1}}},
		Put:         put,
	})
	if err == nil || err.Error() != "HTTP 401" {
		t.Errorf("expected HTTP 401 error, got %v", err)
	}
}
