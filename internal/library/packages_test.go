package library

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type packageTransport func(*http.Request) (*http.Response, error)

func (f packageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNPMArchiveIntegrityAndManifest(t *testing.T) {
	_, _, _ = ecosystemFixture(t)
	archive := testZip(t, map[string]string{
		"package/package.json":           `{"pi":{"skills":["skills"]},"scripts":{"postinstall":"exit 99"}}`,
		"package/skills/client/SKILL.md": "---\nname: client\ndescription: Fixture\n---\nFixture",
	})
	sum := sha512.Sum512(archive)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
	original := sourceHTTP
	defer func() { sourceHTTP = original }()
	sourceHTTP = &http.Client{Transport: packageTransport(func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.String() {
		case "https://registry.npmjs.org/fixture/1.0.0":
			body = fmt.Sprintf(`{"dist":{"tarball":"https://registry.npmjs.org/fixture/-/fixture.tgz","integrity":%q}}`, integrity)
		case "https://registry.npmjs.org/fixture/-/fixture.tgz":
			body = string(archive)
		default:
			t.Fatalf("unexpected request: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	c, e := OpenCollectionContext(context.Background(), "npm:fixture@1.0.0")
	must(t, e)
	if len(c.Skills) != 1 || c.Skills[0].Name != "client" {
		t.Fatal(c.Skills)
	}
	c.Close()
	integrity = "sha512-invalid"
	if c, e := OpenCollection("npm:fixture@1.0.0"); e == nil {
		c.Close()
		t.Fatal("invalid integrity accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := OpenCollectionContext(ctx, "npm:fixture@1.0.0"); e != context.Canceled {
		t.Fatal(e)
	}
}

func TestPackageGlobPaths(t *testing.T) {
	for _, value := range []string{"skills/client.md", "skills/nested/client.md"} {
		if !packagePathMatch("skills/**/*.md", value) {
			t.Fatal(value)
		}
	}
	if !packagePathMatch("skills/[ab]?", "skills/a1") || packagePathMatch("skills/[ab]?", "skills/c1") {
		t.Fatal("glob character classes")
	}
}
