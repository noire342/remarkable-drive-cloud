// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"fmt"
	"testing"
)

type scopedRemote struct {
	*fakeRemote
	t *testing.T
}

func (r scopedRemote) Changes(context.Context, string) (Changes, error) {
	r.t.Fatal("sync enumerated the account-wide change feed")
	return Changes{}, nil
}

func TestUnrelatedMassDeletionDoesNotBlockPublishCommitOrRestore(t *testing.T) {
	e, remote := setupEngine(t)
	doc := addFixture(t, e)
	// Simulate a very large external deletion backlog. Our poll must never read it.
	for i := 0; i < 20000; i++ {
		remote.events = append(remote.events, Change{FileID: fmt.Sprintf("unrelated-%d", i), Removed: true})
	}
	e.remote = scopedRemote{remote, t}
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.Status().Pending != 0 || len(e.state.Current.Documents) != 1 {
		t.Fatal("unrelated deletions affected the native library")
	}
	if _, err := e.Restore(context.Background(), e.state.Current.ID, doc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.VerifyRemote(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestScopedPollStillAdoptsNestedFoldersAndAppliesMoves(t *testing.T) {
	e, remote := setupEngine(t)
	doc := addFixture(t, e)
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	outer := File{ID: "external-folder", Name: "Outer", MIME: FolderMIME, Parents: []string{e.state.Layout.Library}}
	inner := File{ID: "nested-folder", Name: "Inner", MIME: FolderMIME, Parents: []string{outer.ID}}
	if _, err := remote.Put(context.Background(), outer, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Put(context.Background(), inner, ""); err != nil {
		t.Fatal(err)
	}
	remote.edit(e.state.Mappings[doc.ID].DriveID, func(f *File, data []byte) []byte { f.Parents = []string{inner.ID}; f.Name = "Moved.pdf"; return nil })
	e.remote = scopedRemote{remote, t}
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	g, err := e.currentGraph()
	if err != nil {
		t.Fatal(err)
	}
	nativeParent, ok := e.nativeParent(inner.ID)
	if !ok || g.Documents[doc.ID].Text("parent") != nativeParent || g.Documents[doc.ID].Text("visibleName") != "Moved" {
		t.Fatal("scoped poll lost the folder move")
	}
}

func TestTrashedItemsAreNotPermanentPendingExports(t *testing.T) {
	e, _ := setupEngine(t)
	doc := addFixture(t, e)
	g, _ := e.currentGraph()
	if err := e.updateMetadata(doc, map[string]any{"parent": "trash"}); err != nil {
		t.Fatal(err)
	}
	g.Documents[doc.ID] = doc
	if err := e.commitGraph(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.Status().Pending != 0 {
		t.Fatal("a settled trash item remains pending forever")
	}
}
