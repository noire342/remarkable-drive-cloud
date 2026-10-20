// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"strings"
	"testing"
)

func TestNativeObserverWaitsForSuccessfulFinalRetry(t *testing.T) {
	failed := "Type=signal\nInterface=no.remarkable.sync.Synchronizer Member=batchFinished\nBYTE 1;\nBOOLEAN true;\n"
	partial := "Type=signal\nInterface=no.remarkable.sync.Synchronizer Member=batchFinished\nBYTE 0;\nBOOLEAN false;\n"
	unrelated := "Type=method_return\nBYTE 0;\nBOOLEAN true;\n"
	final := "Type=signal\nInterface=no.remarkable.sync.Synchronizer Member=batchFinished\nBYTE 0;\nBOOLEAN true;\n"
	for _, data := range []string{failed, partial, failed + unrelated, failed + partial + unrelated} {
		if successfulNativeBatch(strings.NewReader(data), nil) == nil {
			t.Fatal("failed or incomplete batch acknowledged")
		}
	}
	failures := 0
	if err := successfulNativeBatch(strings.NewReader(failed+unrelated+partial+final), func() { failures++ }); err != nil {
		t.Fatal(err)
	}
	if failures != 1 {
		t.Fatalf("retry signalled %d times", failures)
	}
}
