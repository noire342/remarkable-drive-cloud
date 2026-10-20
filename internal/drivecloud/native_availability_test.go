// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockedDrive struct {
	*fakeRemote
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockedDrive) Get(ctx context.Context, id string) (File, error) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.release:
	case <-ctx.Done():
		return File{}, ctx.Err()
	}
	return b.fakeRemote.Get(ctx, id)
}

func TestNativeCachedReadsAndStagingDoNotWaitForDriveCycle(t *testing.T) {
	e, remote := setupEngine(t)
	doc := addFixture(t, e)
	root, generation := e.state.Current.Root, e.state.Current.Generation
	hash := doc.Entries[0].Hash
	expected, err := os.ReadFile(e.objectPath(hash))
	if err != nil {
		t.Fatal(err)
	}
	blocked := &blockedDrive{fakeRemote: remote, entered: make(chan struct{}), release: make(chan struct{})}
	e.remote = blocked
	cycleDone := make(chan error, 1)
	go func() { cycleDone <- e.Cycle(context.Background()) }()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		t.Fatal("cycle did not reach Drive")
	}
	defer func() {
		close(blocked.release)
		if err := <-cycleDone; err != nil {
			t.Error(err)
		}
	}()
	result := make(chan error, 1)
	go func() {
		reader, gen, _, _, err := e.LoadBlob("tablet", "root")
		if err != nil {
			result <- err
			return
		}
		data, _ := io.ReadAll(reader)
		reader.Close()
		if string(data) != root || gen != generation {
			result <- fmt.Errorf("uncommitted root returned")
			return
		}
		reader, _, _, _, err = e.LoadBlob("tablet", hash)
		if err != nil {
			result <- err
			return
		}
		data, _ = io.ReadAll(reader)
		reader.Close()
		if string(data) != string(expected) {
			result <- fmt.Errorf("cached content changed")
			return
		}
		incoming := "new native immutable payload"
		sum := sha256.Sum256([]byte(incoming))
		_, err = e.StoreBlob("tablet", fmt.Sprintf("%x", sum), strings.NewReader(incoming), 1)
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("native reads/staging blocked behind Drive")
	}
}

func TestFailedStateSaveCannotPublishFutureNativeRoot(t *testing.T) {
	e, _ := setupEngine(t)
	addFixture(t, e)
	root, generation := e.state.Current.Root, e.state.Current.Generation
	path := filepath.Join(e.dir, "state.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state.Current.Root = strings.Repeat("a", 64)
	e.state.Current.Generation++
	if e.save() == nil {
		t.Fatal("invalid state destination accepted")
	}
	reader, gen, _, _, err := e.LoadBlob("tablet", "root")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	body, _ := io.ReadAll(reader)
	if string(body) != root || gen != generation {
		t.Fatal("future root visible before durable state")
	}
}
