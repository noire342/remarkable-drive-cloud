// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"testing"
)

type renameDuringPublication struct {
	*fakeRemote
	id   string
	gets int
}

func (r *renameDuringPublication) Get(ctx context.Context, id string) (File, error) {
	if id == r.id {
		r.gets++
		if r.gets == 2 {
			if _, err := r.fakeRemote.Patch(ctx, id, map[string]any{"name": "Renamed on Drive.pdf"}, "", ""); err != nil {
				return File{}, err
			}
		}
	}
	return r.fakeRemote.Get(ctx, id)
}

func TestPublicationDefersDriveChangeAfterReconciliation(t *testing.T) {
	e, remote := setupEngine(t)
	doc := addFixture(t, e)
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := e.state.Mappings[doc.ID].DriveID
	e.remote = &renameDuringPublication{fakeRemote: remote, id: id}
	if err := e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	file, err := remote.Get(context.Background(), id)
	if err != nil || file.Name != "Renamed on Drive.pdf" {
		t.Fatal("concurrent Drive rename overwritten")
	}
	if err = e.Cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	g, _ := e.currentGraph()
	if g.Documents[doc.ID].Text("visibleName") != "Renamed on Drive" {
		t.Fatal("deferred Drive rename not reconciled")
	}
}
