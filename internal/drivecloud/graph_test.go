// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"bytes"
	"context"
	"fmt"
	"github.com/ddvk/rmfakecloud/internal/storage/models"
	"github.com/google/uuid"
	"testing"
)

func TestModernRootAcceptsRawHashAndLegacyDocumentIndices(t *testing.T) {
	e, _ := setupEngine(t)
	id := uuid.NewString()
	metadata := []byte(`{"type":"DocumentType","visibleName":"Example","parent":"","unknown":{"version":7}}`)
	mh, err := e.stage(metadata)
	if err != nil {
		t.Fatal(err)
	}
	parts := []*models.HashEntry{models.NewHashEntry(mh, id+".metadata", int64(len(metadata)))}
	dh, _ := models.HashEntries(parts)
	if err = e.putObject(dh, bytes.NewReader(indexBytes(parts, "3", false))); err != nil {
		t.Fatal(err)
	}
	entries := []*models.HashEntry{{Hash: dh, Type: "0", EntryName: id, Subfiles: 1, Size: int64(len(metadata))}}
	data := indexBytes(entries, "4", true)
	root := digest(data)
	if err = e.putObject(root, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	g, err := e.graph(root)
	if err != nil {
		t.Fatal(err)
	}
	if !g.RawRoot || len(g.Documents) != 1 {
		t.Fatal("modern root was not retained")
	}
	if err = e.updateMetadata(g.Documents[id], map[string]any{"visibleName": "Renamed"}); err != nil {
		t.Fatal(err)
	}
	if err = e.commitGraph(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	next, err := e.graph(e.state.Current.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !next.RawRoot || next.Documents[id].Text("visibleName") != "Renamed" {
		t.Fatal("root representation lost")
	}
}

func TestModernDocumentSummaryAndInvalidRoot(t *testing.T) {
	id := uuid.NewString()
	raw := []byte(fmt.Sprintf("4\n0:%s:0:0\n", id))
	if _, _, err := parseIndex(raw); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"4\n0:../bad:0:0\n", "4\n0:.:1:0\n", "3\ninvalid\n"} {
		if _, _, err := parseIndex([]byte(data)); err == nil {
			t.Fatalf("accepted invalid index %q", data)
		}
	}
	if validDocumentID("------------------------------------") {
		t.Fatal("invalid UUID accepted")
	}
}
