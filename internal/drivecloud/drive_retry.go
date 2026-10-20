// SPDX-License-Identifier: AGPL-3.0-only
package drivecloud

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

func retryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}

func waitForDrive(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Rate-limit rejections can replay a body. An uncertain mutating 5xx must instead
// use the durable ID or resumable-session status on the next operation.
func (d *Drive) request(ctx context.Context, method, u string, body io.Reader, headers map[string]string) (*http.Response, error) {
	seeker, replayable := body.(io.Seeker)
	var start int64
	if replayable {
		var err error
		start, err = seeker.Seek(0, io.SeekCurrent)
		if err != nil {
			replayable = false
		}
	}
	wait := d.retryWait
	if wait == nil {
		wait = waitForDrive
	}
	for attempt := 0; ; attempt++ {
		response, err := d.requestOnce(ctx, method, u, body, headers)
		var failure *HTTPError
		if attempt == 5 || !errors.As(err, &failure) || (body != nil && !replayable) {
			return response, err
		}
		rateLimited := failure.Status == 429 || (failure.Status == 403 && (failure.Reason == "rateLimitExceeded" || failure.Reason == "userRateLimitExceeded"))
		readRetry := (method == http.MethodGet || method == http.MethodHead) && failure.Status >= 500 && failure.Status <= 599
		if !rateLimited && !readRetry {
			return response, err
		}
		delay := (time.Second << attempt) + time.Duration(rand.Intn(1000))*time.Millisecond
		if failure.RetryAfter > delay {
			delay = failure.RetryAfter
		}
		if err = wait(ctx, delay); err != nil {
			return nil, err
		}
		if replayable {
			if _, err = seeker.Seek(start, io.SeekStart); err != nil {
				return nil, err
			}
		}
	}
}
