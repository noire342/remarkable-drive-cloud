// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandwritingDuringExportCannotBePublishedAsAnOlderRevision(t *testing.T) {
	e, remote := setupEngine(t)
	doc := addFixture(t, e)
	library := t.TempDir()
	for _, entry := range doc.Entries {
		path := filepath.Join(library, filepath.FromSlash(entry.EntryName))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(e.objectPath(entry.Hash))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	e.SetExportVerifier(LibraryRevisionVerifier(library))
	changed := filepath.Join(library, doc.ID, "page.rm")
	e.SetExporter(func(string, string) (io.ReadCloser, error) {
		if err := os.WriteFile(changed, []byte("new handwriting"), 0600); err != nil {
			t.Fatal(err)
		}
		return io.NopCloser(strings.NewReader("%PDF-1.7\n%%EOF\n")), nil
	})
	if err := e.PublishLibrary(context.Background()); err == nil {
		t.Fatal("published an export while the native revision changed")
	}
	for _, file := range remote.files {
		if file.MIME == "application/pdf" {
			t.Fatal("unverified PDF was uploaded")
		}
	}
	if e.state.Mappings[doc.ID].ExportHash != "" || e.Status().Pending != 1 {
		t.Fatal("deferred export was acknowledged")
	}
	called := false
	e.SetExporter(func(string, string) (io.ReadCloser, error) {
		called = true
		return nil, nil
	})
	if err := e.PublishLibrary(context.Background()); err == nil || called {
		t.Fatal("rendered a revision that already differed before export")
	}
}

func TestNativeRevisionVerifierRejectsPathsOutsideDocument(t *testing.T) {
	e, _ := setupEngine(t)
	doc := addFixture(t, e)
	doc.Entries[0].EntryName = "../other-document.rm"
	if err := LibraryRevisionVerifier(t.TempDir())(doc); err == nil {
		t.Fatal("accepted a path outside the native document")
	}
}
