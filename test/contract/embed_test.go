package contract_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/httpapi"
	"github.com/gulcint/smart-campaign-composer/test/testenv/apispec"
)

// contractFiles are the files go-interfaces.md §httpapi says are copied under
// internal/httpapi/openapi/ with the same relative layout as docs/contracts/.
var contractFiles = []string{
	"openapi.yaml",
	"schemas/segment-rule.schema.json",
	"schemas/content.schema.json",
}

// embeddedContract returns httpapi.OpenAPI rooted at the directory that holds openapi.yaml. A
// //go:embed of the package directory "openapi" keeps that prefix; a root-level layout is accepted too.
func embeddedContract(t *testing.T) fs.FS {
	t.Helper()
	if _, err := fs.Stat(httpapi.OpenAPI, "openapi/openapi.yaml"); err == nil {
		sub, err := fs.Sub(httpapi.OpenAPI, "openapi")
		if err != nil {
			t.Fatalf("fs.Sub(httpapi.OpenAPI, openapi): %v", err)
		}
		return sub
	}
	if _, err := fs.Stat(httpapi.OpenAPI, "openapi.yaml"); err == nil {
		return httpapi.OpenAPI
	}
	t.Fatal("httpapi.OpenAPI contains no openapi.yaml (neither openapi/openapi.yaml nor openapi.yaml)")
	return nil
}

// REQ-AUD-00, REQ-AI-01
// The embedded contract copy (httpapi.OpenAPI) is byte-identical to docs/contracts/ — the same
// property `make check-contract-copies` enforces with cmp, here observed through the exported
// embed.FS so a stale copy also fails `make test`.
func TestEmbeddedOpenAPI_IsByteIdenticalToContracts(t *testing.T) {
	t.Parallel()
	spec := loadSpec(t)
	docsDir := filepath.Dir(spec.Path())
	emb := embeddedContract(t)
	for _, name := range contractFiles {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want, err := os.ReadFile(filepath.Join(docsDir, filepath.FromSlash(name)))
			if err != nil {
				t.Fatalf("read docs/contracts/%s: %v", name, err)
			}
			got, err := fs.ReadFile(emb, name)
			if err != nil {
				t.Fatalf("embedded copy of %s is missing: %v", name, err)
			}
			if !bytes.Equal(want, got) {
				t.Fatalf("embedded %s (%d bytes) differs from docs/contracts/%s (%d bytes): copy the contract file, never edit the copy", name, len(got), name, len(want))
			}
		})
	}
}

// REQ-AUD-00
// The embedded copy, unpacked to disk (os.CopyFS, as go-interfaces.md §httpapi prescribes; never
// loaded through an fs.FS), resolves with libopenapi and defines the same operations as the original.
func TestEmbeddedOpenAPI_LoadsWithTheSameOperations(t *testing.T) {
	t.Parallel()
	orig := loadSpec(t)
	dir := t.TempDir()
	if err := os.CopyFS(dir, embeddedContract(t)); err != nil {
		t.Fatalf("os.CopyFS(embedded contract): %v", err)
	}
	copied, err := apispec.Load(filepath.Join(dir, "openapi.yaml"))
	if err != nil {
		t.Fatalf("load the embedded copy from disk: %v", err)
	}
	if diff := cmp.Diff(orig.Operations(), copied.Operations()); diff != "" {
		t.Fatalf("embedded copy operations differ from docs/contracts (-docs +embedded):\n%s", diff)
	}
}
