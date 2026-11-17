package outbox_dispatcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sudarshan24-10/qa_user_upload_service/internal/outbox/model"
)

type fakeStore struct {
	events       []*model.OutboxEvent
	claimErr     error
	claimedLimit int
	claimedLease time.Duration
	processed    []string
	retried      []string
	failed       []string
	retryAt      time.Time
	processedErr error
	failErr      error
}

func (f *fakeStore) ClaimPending(ctx context.Context, limit int, lease time.Duration) ([]*model.OutboxEvent, error) {
	f.claimedLimit, f.claimedLease = limit, lease
	return f.events, f.claimErr
}

func (f *fakeStore) MarkProcessed(ctx context.Context, id string) error {
	f.processed = append(f.processed, id)
	return f.processedErr
}

func (f *fakeStore) MarkRetry(ctx context.Context, id string, at time.Time) error {
	f.retried = append(f.retried, id)
	f.retryAt = at
	return nil
}

func (f *fakeStore) MarkFailed(ctx context.Context, id string) error {
	f.failed = append(f.failed, id)
	return f.failErr
}

type fakePublisher struct {
	err       error
	failIDs   map[string]bool
	published []PublishRequest
}

func (p *fakePublisher) Publish(ctx context.Context, r PublishRequest) error {
	p.published = append(p.published, r)
	if p.failIDs != nil && p.failIDs[r.EventID] {
		return errors.New("publish failed")
	}
	return p.err
}

func (p *fakePublisher) Close(ctx context.Context) error { return nil }

func ev(id string, retries int) *model.OutboxEvent {
	return &model.OutboxEvent{
		ID: id, AggregateID: "doc-" + id, EventType: "DOCUMENT_UPLOADED",
		Payload: `{"k":"v"}`, RetryCount: retries,
	}
}

func TestDispatcher_ClaimsWithBatchSizeAndLease(t *testing.T) {
	store := &fakeStore{}
	d := NewOutboxDispatcher(store, &fakePublisher{})

	d.processBatch(context.Background())

	if store.claimedLimit != 100 {
		t.Fatalf("expected batch size 100, got %d", store.claimedLimit)
	}
	if store.claimedLease != 60*time.Second {
		t.Fatalf("expected lease 60s, got %s", store.claimedLease)
	}
}

func TestDispatcher_PublishSuccess_MarksProcessed(t *testing.T) {
	store := &fakeStore{events: []*model.OutboxEvent{ev("e1", 0)}}
	pub := &fakePublisher{}
	d := NewOutboxDispatcher(store, pub)

	d.processBatch(context.Background())

	if len(store.processed) != 1 || store.processed[0] != "e1" {
		t.Fatalf("expected e1 processed, got %v", store.processed)
	}
	if len(store.retried) != 0 || len(store.failed) != 0 {
		t.Fatalf("expected no retry/fail, got retried=%v failed=%v", store.retried, store.failed)
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected one publish, got %d", len(pub.published))
	}
	got := pub.published[0]
	if got.EventID != "e1" || got.AggregateID != "doc-e1" || got.EventType != "DOCUMENT_UPLOADED" || got.Payload != `{"k":"v"}` {
		t.Fatalf("unexpected publish request: %+v", got)
	}
}

func TestDispatcher_PublishFailure_SchedulesRetryWithBackoff(t *testing.T) {
	store := &fakeStore{events: []*model.OutboxEvent{ev("e1", 3)}}
	d := NewOutboxDispatcher(store, &fakePublisher{err: errors.New("redis down")})

	before := time.Now()
	d.processBatch(context.Background())

	if len(store.retried) != 1 || store.retried[0] != "e1" {
		t.Fatalf("expected e1 retried, got %v", store.retried)
	}
	if len(store.processed) != 0 || len(store.failed) != 0 {
		t.Fatalf("expected no processed/failed, got processed=%v failed=%v", store.processed, store.failed)
	}
	want := before.Add(retryDelay(3)) // 8s
	if diff := store.retryAt.Sub(want); diff < -time.Second || diff > 2*time.Second {
		t.Fatalf("retry time %s not near expected %s", store.retryAt, want)
	}
}

func TestDispatcher_FailureOnLastAllowedAttempt_MarksFailed(t *testing.T) {
	store := &fakeStore{events: []*model.OutboxEvent{ev("e1", maxPublishAttempts-1)}}
	d := NewOutboxDispatcher(store, &fakePublisher{err: errors.New("redis down")})

	d.processBatch(context.Background())

	if len(store.failed) != 1 || store.failed[0] != "e1" {
		t.Fatalf("expected e1 marked FAILED, got %v", store.failed)
	}
	if len(store.retried) != 0 || len(store.processed) != 0 {
		t.Fatalf("expected no retry/processed, got retried=%v processed=%v", store.retried, store.processed)
	}
}

func TestDispatcher_FailureBeforeLastAttempt_StillRetries(t *testing.T) {
	store := &fakeStore{events: []*model.OutboxEvent{ev("e1", maxPublishAttempts-2)}}
	d := NewOutboxDispatcher(store, &fakePublisher{err: errors.New("redis down")})

	d.processBatch(context.Background())

	if len(store.retried) != 1 || len(store.failed) != 0 {
		t.Fatalf("expected retry and no FAILED, got retried=%v failed=%v", store.retried, store.failed)
	}
}

func TestDispatcher_MarkFailedError_IsReturnedNotPanic(t *testing.T) {
	store := &fakeStore{failErr: errors.New("db down")}
	d := NewOutboxDispatcher(store, &fakePublisher{err: errors.New("redis down")})

	err := d.dispatchEvent(context.Background(), ev("e1", maxPublishAttempts-1))

	if err == nil || err.Error() != "db down" {
		t.Fatalf("expected MarkFailed error to be returned, got %v", err)
	}
}

func TestDispatcher_SuccessfulPublishAtMaxAttempts_StillMarksProcessed(t *testing.T) {
	store := &fakeStore{}
	d := NewOutboxDispatcher(store, &fakePublisher{})

	if err := d.dispatchEvent(context.Background(), ev("e1", maxPublishAttempts-1)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.processed) != 1 || len(store.failed) != 0 {
		t.Fatalf("expected processed and not failed, got processed=%v failed=%v", store.processed, store.failed)
	}
}

func TestDispatcher_MarkProcessedError_DoesNotRetry(t *testing.T) {
	store := &fakeStore{processedErr: errors.New("db blip")}
	d := NewOutboxDispatcher(store, &fakePublisher{})

	err := d.dispatchEvent(context.Background(), ev("e1", 0))

	if err == nil {
		t.Fatalf("expected the MarkProcessed error to be returned")
	}
	if len(store.retried) != 0 || len(store.failed) != 0 {
		t.Fatalf("a published event must not be retried/failed here (duplicate on next lease is expected), got retried=%v failed=%v", store.retried, store.failed)
	}
}

func TestDispatcher_ClaimError_PublishesNothing(t *testing.T) {
	store := &fakeStore{claimErr: errors.New("db down"), events: []*model.OutboxEvent{ev("e1", 0)}}
	pub := &fakePublisher{}
	d := NewOutboxDispatcher(store, pub)

	d.processBatch(context.Background())

	if len(pub.published) != 0 || len(store.processed) != 0 {
		t.Fatalf("expected nothing published, got published=%d processed=%v", len(pub.published), store.processed)
	}
}

func TestDispatcher_OneFailingEventDoesNotBlockOthers(t *testing.T) {
	store := &fakeStore{events: []*model.OutboxEvent{ev("e1", 0), ev("e2", 0)}}
	pub := &fakePublisher{failIDs: map[string]bool{"e1": true}}
	d := NewOutboxDispatcher(store, pub)

	d.processBatch(context.Background())

	if len(store.retried) != 1 || store.retried[0] != "e1" {
		t.Fatalf("expected e1 retried, got %v", store.retried)
	}
	if len(store.processed) != 1 || store.processed[0] != "e2" {
		t.Fatalf("expected e2 processed, got %v", store.processed)
	}
}

func TestRetryDelay_Table(t *testing.T) {
	cases := []struct {
		retries int
		want    time.Duration
	}{
		{-1, 1 * time.Second},
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{8, 256 * time.Second},
		{9, 5 * time.Minute},
		{100, 5 * time.Minute},
	}
	for _, c := range cases {
		if got := retryDelay(c.retries); got != c.want {
			t.Fatalf("retryDelay(%d): expected %s, got %s", c.retries, c.want, got)
		}
	}
}
