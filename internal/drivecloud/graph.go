// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/ddvk/rmfakecloud/internal/storage/models"
)

var nativeHash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var documentUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Document struct {
	ID       string
	Hash     string
	Entries  []*models.HashEntry
	Metadata map[string]json.RawMessage
}

func (d Document) Text(key string) string {
	var s string
	json.Unmarshal(d.Metadata[key], &s)
	return s
}
func (d Document) Deleted() bool {
	var b bool
	json.Unmarshal(d.Metadata["deleted"], &b)
	return b || d.Text("parent") == "trash"
}
func (d Document) Folder() bool { return d.Text("type") == "CollectionType" }

type Graph struct {
	Documents   map[string]*Document
	Objects     map[string]bool
	RootEntries []*models.HashEntry
	Schema      string
	RawRoot     bool
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func parseIndex(data []byte) ([]*models.HashEntry, string, error) {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 0 || (lines[0] != "3" && lines[0] != "4") {
		return nil, "", fmt.Errorf("unsupported native index schema")
	}
	schema := lines[0]
	lines = lines[1:]
	expected := -1
	if schema == "4" {
		if len(lines) == 0 {
			return nil, "", fmt.Errorf("missing index summary")
		}
		fields := strings.Split(lines[0], ":")
		if len(fields) != 4 || fields[0] != "0" || (fields[1] != "." && !validDocumentID(fields[1])) {
			return nil, "", fmt.Errorf("invalid index summary")
		}
		n, e := strconv.Atoi(fields[2])
		if e != nil || n < 0 {
			return nil, "", fmt.Errorf("invalid index count")
		}
		expected = n
		lines = lines[1:]
	}
	var entries []*models.HashEntry
	seen := map[string]bool{}
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) != 5 || !nativeHash.MatchString(fields[0]) || seen[fields[2]] {
			return nil, "", fmt.Errorf("invalid or duplicate native index entry")
		}
		if fields[1] != "0" && fields[1] != "80000000" {
			return nil, "", fmt.Errorf("unknown index entry type")
		}
		size, e := strconv.ParseInt(fields[4], 10, 64)
		if e != nil || size < 0 {
			return nil, "", fmt.Errorf("invalid native object size")
		}
		sub, e := strconv.Atoi(fields[3])
		if e != nil || sub < 0 {
			return nil, "", fmt.Errorf("invalid native subfile count")
		}
		seen[fields[2]] = true
		entries = append(entries, &models.HashEntry{Hash: fields[0], Type: fields[1], EntryName: fields[2], Subfiles: sub, Size: size})
	}
	if expected >= 0 && expected != len(entries) {
		return nil, "", fmt.Errorf("native index count mismatch")
	}
	return entries, schema, nil
}
func (e *Engine) graph(root string) (*Graph, error) {
	if !nativeHash.MatchString(root) {
		return nil, fmt.Errorf("invalid native root")
	}
	data, err := e.readObject(root, 32<<20)
	if err != nil {
		return nil, err
	}
	entries, schema, err := parseIndex(data)
	if err != nil {
		return nil, err
	}
	hash, err := models.HashEntries(entries)
	rawRoot := digest(data) == root
	if err != nil || (hash != root && !rawRoot) {
		return nil, fmt.Errorf("native root structural hash mismatch")
	}
	graph := &Graph{Documents: map[string]*Document{}, Objects: map[string]bool{root: true}, RootEntries: entries, Schema: schema, RawRoot: rawRoot}
	for _, entry := range entries {
		if (entry.Type != "80000000" && !(schema == "4" && entry.Type == "0")) || !validDocumentID(entry.EntryName) {
			return nil, fmt.Errorf("invalid document in native root")
		}
		index, err := e.readObject(entry.Hash, 32<<20)
		if err != nil {
			return nil, err
		}
		parts, _, err := parseIndex(index)
		if err != nil {
			return nil, err
		}
		hash, err := models.HashEntries(parts)
		if err != nil || (hash != entry.Hash && digest(index) != entry.Hash) {
			return nil, fmt.Errorf("document index structural hash mismatch")
		}
		doc := &Document{ID: entry.EntryName, Hash: entry.Hash, Entries: parts}
		graph.Documents[doc.ID] = doc
		graph.Objects[doc.Hash] = true
		for _, part := range parts {
			if part.Type != "0" || strings.Contains(part.EntryName, "..") || strings.HasPrefix(part.EntryName, "/") {
				return nil, fmt.Errorf("invalid native document entry")
			}
			f, err := os.Open(e.objectPath(part.Hash))
			if err != nil {
				return nil, fmt.Errorf("missing referenced object: %s", part.Hash)
			}
			h := sha256.New()
			n, err := io.Copy(h, f)
			f.Close()
			if err != nil {
				return nil, err
			}
			if n != part.Size || hex.EncodeToString(h.Sum(nil)) != part.Hash {
				return nil, fmt.Errorf("native data hash or size mismatch")
			}
			graph.Objects[part.Hash] = true
			if part.EntryName == doc.ID+".metadata" {
				raw, err := e.readObject(part.Hash, 16<<20)
				if err != nil {
					return nil, err
				}
				if err = json.Unmarshal(raw, &doc.Metadata); err != nil {
					return nil, fmt.Errorf("invalid native document metadata")
				}
			}
		}
		if doc.Metadata == nil {
			return nil, fmt.Errorf("native document missing metadata")
		}
	}
	for _, doc := range graph.Documents {
		seen := map[string]bool{doc.ID: true}
		parent := doc.Text("parent")
		for parent != "" && parent != "trash" {
			folder, ok := graph.Documents[parent]
			if !ok || !folder.Folder() || seen[parent] {
				return nil, fmt.Errorf("incomplete or cyclic native folder hierarchy")
			}
			seen[parent] = true
			parent = folder.Text("parent")
		}
	}
	return graph, nil
}
func validDocumentID(id string) bool {
	return documentUUID.MatchString(id)
}
func (e *Engine) readObject(hash string, limit int64) ([]byte, error) {
	f, err := os.Open(e.objectPath(hash))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, fmt.Errorf("index or metadata too large")
	}
	return b, err
}
func indexBytes(entries []*models.HashEntry, schema string, root bool) []byte {
	var out bytes.Buffer
	fmt.Fprintln(&out, schema)
	if schema == "4" {
		size := int64(0)
		for _, entry := range entries {
			size += entry.Size
		}
		fmt.Fprintf(&out, "0:.:%d:%d\n", len(entries), size)
	}
	for _, entry := range entries {
		kind := "0"
		sub := 0
		if root {
			kind = entry.Type
			sub = entry.Subfiles
		}
		fmt.Fprintf(&out, "%s:%s:%s:%d:%d\n", entry.Hash, kind, entry.EntryName, sub, entry.Size)
	}
	return out.Bytes()
}
