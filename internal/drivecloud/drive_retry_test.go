// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDriveRetriesRejectionsWithBoundedBackoff(t *testing.T) {
	for _, test := range []struct {
		name, method, reason string
		status, attempts     int
	}{
		{"user rate", "PATCH", "userRateLimitExceeded", 403, 3},
		{"project rate", "POST", "rateLimitExceeded", 403, 3},
		{"too many", "PUT", "", 429, 3},
		{"read server error", "GET", "backendError", 503, 3},
		{"uncertain mutation", "PUT", "backendError", 503, 1},
		{"quota", "POST", "storageQuotaExceeded", 403, 1},
		{"permission", "GET", "insufficientFilePermissions", 403, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts, waits := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				data, _ := io.ReadAll(r.Body)
				if string(data) != "payload" {
					t.Errorf("request body changed on attempt %d: %q", attempts, data)
				}
				if attempts <= 2 {
					w.WriteHeader(test.status)
					fmt.Fprintf(w, `{"error":{"errors":[{"reason":%q}]}}`, test.reason)
					return
				}
				w.WriteHeader(200)
			}))
			defer server.Close()
			drive := NewDrive(server.Client(), t.TempDir())
			drive.retryWait = func(_ context.Context, delay time.Duration) error {
				base := time.Second << waits
				if delay < base || delay >= base+time.Second {
					t.Errorf("backoff outside jitter range: %s", delay)
				}
				waits++
				return nil
			}
			// A resumable chunk has a fixed section and must replay from its start.
			body := io.NewSectionReader(strings.NewReader("prefixpayloadsuffix"), 6, 7)
			response, err := drive.request(context.Background(), test.method, server.URL, body, nil)
			if response != nil {
				response.Body.Close()
			}
			if attempts != test.attempts || waits != test.attempts-1 {
				t.Fatalf("attempts=%d waits=%d err=%v", attempts, waits, err)
			}
			if (err == nil) != (test.attempts > 1) {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}

func TestDriveRetryAfterLimitAndCancellation(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(429)
	}))
	defer server.Close()
	drive := NewDrive(server.Client(), t.TempDir())
	waits := 0
	drive.retryWait = func(_ context.Context, delay time.Duration) error {
		if delay < 7*time.Second {
			t.Errorf("ignored server delay: %s", delay)
		}
		waits++
		return nil
	}
	if _, err := drive.request(context.Background(), "GET", server.URL, nil, nil); err == nil || attempts != 6 || waits != 5 {
		t.Fatalf("retry was unbounded: attempts=%d waits=%d err=%v", attempts, waits, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	drive.retryWait = func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
	if _, err := drive.request(ctx, "GET", server.URL, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if attempts != 7 {
		t.Fatal("retried after cancellation")
	}
}

func TestDriveRetryAfterHTTPDate(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	if got := retryAfter(now.Add(9*time.Second).Format(http.TimeFormat), now); got != 9*time.Second {
		t.Fatal(got)
	}
	if got := retryAfter("-1", now); got != 0 {
		t.Fatal(got)
	}
}
