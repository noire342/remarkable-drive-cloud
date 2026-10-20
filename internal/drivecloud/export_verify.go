// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LibraryRevisionVerifier reads the native files around an export. A PDF can
// only be labelled with a snapshot revision when the live document matches that
// revision both before and after rendering. Changes are retried on a later sync.
// This verifier never writes to Xochitl's library.
func LibraryRevisionVerifier(root string) func(*Document) error {
	return func(doc *Document) error {
		for _, entry := range doc.Entries {
			name := entry.EntryName
			if !validDocumentID(doc.ID) || !(strings.HasPrefix(name, doc.ID+".") || strings.HasPrefix(name, doc.ID+"/")) || strings.Contains(name, "\\") {
				return fmt.Errorf("native export has an invalid document path")
			}
			parts := strings.Split(name, "/")
			path := root
			for _, part := range parts {
				if part == "" || part == "." || part == ".." {
					return fmt.Errorf("native export has an invalid document path")
				}
				path = filepath.Join(path, part)
				st, err := os.Lstat(path)
				if err != nil || st.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("native export revision unavailable; waiting for synchronization")
				}
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			h := sha256.New()
			n, err := io.Copy(h, io.LimitReader(f, entry.Size+1))
			f.Close()
			if err != nil || n != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.Hash {
				return fmt.Errorf("native document changed; PDF export deferred until the next synchronization")
			}
		}
		return nil
	}
}
