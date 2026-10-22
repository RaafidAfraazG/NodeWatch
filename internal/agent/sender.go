package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
)

type Sender struct {
	url        string
	client     *http.Client
	maxRetries int
	backoff    time.Duration
}

func NewSender(url string, timeout time.Duration, maxRetries int) *Sender {
	// Never follow redirects to avoid forwarding host metadata to another endpoint.
	return &Sender{url: url, client: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, maxRetries: maxRetries, backoff: 500 * time.Millisecond}
}

func (s *Sender) Send(ctx context.Context, snapshot metrics.Snapshot) error {
	body, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
		if err != nil {
			return errors.New("invalid collector request")
		}
		req.Header.Set("Content-Type", "application/json")
		resp, requestErr := s.client.Do(req)
		transient := requestErr != nil
		if requestErr != nil {
			// Avoid logging URL contents or server response bodies.
			err = errors.New("collector transport failed")
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			err = fmt.Errorf("collector returned HTTP %d", resp.StatusCode)
			transient = resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !transient || attempt == s.maxRetries {
			return err
		}
		timer := time.NewTimer(s.backoff * time.Duration(1<<attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errors.New("invalid retry configuration")
}
