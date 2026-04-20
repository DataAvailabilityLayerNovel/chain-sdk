package cosmoswasm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMockDAClient_SubmitAndGet(t *testing.T) {
	da := NewMockDAClient()
	ctx := context.Background()
	ns := NamespaceFromString("test-app")

	blobs := [][]byte{[]byte("event-1"), []byte("event-2")}
	result, err := da.SubmitBlobs(ctx, ns, blobs, nil)
	if err != nil {
		t.Fatalf("SubmitBlobs: %v", err)
	}
	if result.BlobCount != 2 {
		t.Fatalf("expected 2 blobs, got %d", result.BlobCount)
	}
	if result.Height == 0 {
		t.Fatal("expected non-zero height")
	}

	// Retrieve by height + namespace.
	got, err := da.GetBlobs(ctx, ns, result.Height)
	if err != nil {
		t.Fatalf("GetBlobs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 blobs, got %d", len(got))
	}
	if string(got[0].Data) != "event-1" {
		t.Fatalf("blob[0] data mismatch: %q", got[0].Data)
	}
	if string(got[1].Data) != "event-2" {
		t.Fatalf("blob[1] data mismatch: %q", got[1].Data)
	}
}

func TestMockDAClient_NamespaceIsolation(t *testing.T) {
	da := NewMockDAClient()
	ctx := context.Background()
	nsA := NamespaceFromString("app-a")
	nsB := NamespaceFromString("app-b")

	resA, _ := da.SubmitBlobs(ctx, nsA, [][]byte{[]byte("from-a")}, nil)
	da.SubmitBlobs(ctx, nsB, [][]byte{[]byte("from-b")}, nil)

	// app-a should only see its own blobs.
	blobs, _ := da.GetBlobs(ctx, nsA, resA.Height)
	if len(blobs) != 1 || string(blobs[0].Data) != "from-a" {
		t.Fatal("namespace isolation violated")
	}

	// No blobs from app-b at app-a's height.
	blobsB, _ := da.GetBlobs(ctx, nsB, resA.Height)
	if len(blobsB) != 0 {
		t.Fatal("expected no blobs from other namespace at this height")
	}
}

func TestMockDAClient_GetBlobByCommitment(t *testing.T) {
	da := NewMockDAClient()
	ctx := context.Background()
	ns := NamespaceFromString("test")

	res, _ := da.SubmitBlobs(ctx, ns, [][]byte{[]byte("find-me")}, nil)
	blobs, _ := da.GetBlobs(ctx, ns, res.Height)

	found, err := da.GetBlobByCommitment(ctx, ns, res.Height, blobs[0].Commitment)
	if err != nil {
		t.Fatalf("GetBlobByCommitment: %v", err)
	}
	if string(found.Data) != "find-me" {
		t.Fatalf("wrong blob data: %q", found.Data)
	}

	// Non-existent commitment.
	_, err = da.GetBlobByCommitment(ctx, ns, res.Height, []byte("nonexistent"))
	if err == nil {
		t.Fatal("expected error for unknown commitment")
	}
}

func TestMockDAClient_Subscribe(t *testing.T) {
	da := NewMockDAClient()
	ns := NamespaceFromString("watched-app")
	nsOther := NamespaceFromString("other-app")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := da.Subscribe(ctx, ns)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Submit to the watched namespace.
	da.SubmitBlobs(ctx, ns, [][]byte{[]byte("watched-event")}, nil)

	// Submit to a different namespace — should NOT appear.
	da.SubmitBlobs(ctx, nsOther, [][]byte{[]byte("other-event")}, nil)

	// Should receive exactly one event.
	select {
	case event := <-ch:
		if len(event.Blobs) != 1 || string(event.Blobs[0].Data) != "watched-event" {
			t.Fatalf("unexpected event data: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for subscription event")
	}

	// Cancel should close the channel.
	cancel()
	time.Sleep(50 * time.Millisecond)
	_, open := <-ch
	if open {
		t.Fatal("expected channel to be closed after cancel")
	}
}

func TestMockDAClient_SubscribeConcurrent(t *testing.T) {
	da := NewMockDAClient()
	ns := NamespaceFromString("concurrent")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const numSubscribers = 3
	channels := make([]<-chan *DABlobEvent, numSubscribers)
	for i := range numSubscribers {
		ch, err := da.Subscribe(ctx, ns)
		if err != nil {
			t.Fatalf("Subscribe[%d]: %v", i, err)
		}
		channels[i] = ch
	}

	da.SubmitBlobs(ctx, ns, [][]byte{[]byte("broadcast")}, nil)

	// All subscribers should receive the event.
	var wg sync.WaitGroup
	for i, ch := range channels {
		wg.Add(1)
		go func(idx int, c <-chan *DABlobEvent) {
			defer wg.Done()
			select {
			case event := <-c:
				if string(event.Blobs[0].Data) != "broadcast" {
					t.Errorf("subscriber[%d]: wrong data", idx)
				}
			case <-time.After(time.Second):
				t.Errorf("subscriber[%d]: timeout", idx)
			}
		}(i, ch)
	}
	wg.Wait()
}

func TestMockDAClient_GetHeight(t *testing.T) {
	da := NewMockDAClient()
	ctx := context.Background()

	h, _ := da.GetHeight(ctx)
	if h != 1 {
		t.Fatalf("expected initial height 1, got %d", h)
	}

	ns := NamespaceFromString("test")
	da.SubmitBlobs(ctx, ns, [][]byte{[]byte("a")}, nil)

	h, _ = da.GetHeight(ctx)
	if h != 2 {
		t.Fatalf("expected height 2 after submit, got %d", h)
	}
}

func TestMockDAClient_InjectBlobs(t *testing.T) {
	da := NewMockDAClient()
	ctx := context.Background()
	ns := NamespaceFromString("injected")

	da.InjectBlobs(ns, 100, []*DABlob{
		{Namespace: ns, Data: []byte("injected-data"), Height: 100, Index: 0},
	})

	blobs, _ := da.GetBlobs(ctx, ns, 100)
	if len(blobs) != 1 || string(blobs[0].Data) != "injected-data" {
		t.Fatal("injected blob not found")
	}
}

func TestMockDAClient_EmptyNamespace(t *testing.T) {
	da := NewMockDAClient()
	_, err := da.SubmitBlobs(context.Background(), nil, [][]byte{[]byte("x")}, nil)
	if err == nil {
		t.Fatal("expected error for nil namespace")
	}
}

// --- DABridge tests ---

func TestDABridge_Submit(t *testing.T) {
	da := NewMockDAClient()
	ns := NamespaceFromString("bridge-test")
	bridge := NewDABridge(da, nil, ns)

	ctx := context.Background()
	result, err := bridge.Submit(ctx, [][]byte{[]byte("hello")}, nil)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.BlobCount != 1 {
		t.Fatalf("expected 1 blob, got %d", result.BlobCount)
	}

	// Retrieve through bridge.
	blobs, err := bridge.GetBlobs(ctx, result.Height)
	if err != nil {
		t.Fatalf("GetBlobs: %v", err)
	}
	if len(blobs) != 1 || string(blobs[0].Data) != "hello" {
		t.Fatal("blob data mismatch")
	}
}

func TestDABridge_Watch(t *testing.T) {
	da := NewMockDAClient()
	ns := NamespaceFromString("watch-test")
	bridge := NewDABridge(da, nil, ns)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := bridge.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	bridge.Submit(ctx, [][]byte{[]byte("watched")}, nil)

	select {
	case event := <-ch:
		if string(event.Blobs[0].Data) != "watched" {
			t.Fatal("wrong data from watch")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for watch event")
	}
}

func TestDABridge_SubmitAndCommit(t *testing.T) {
	da := NewMockDAClient()
	exec := NewMockClient()
	ns := NamespaceFromString("commit-test")
	bridge := NewDABridge(da, exec, ns)

	ctx := context.Background()
	receipt, err := bridge.SubmitAndCommit(ctx, SubmitAndCommitRequest{
		Blobs:    [][]byte{[]byte("event-1"), []byte("event-2")},
		Contract: "wasm1test",
		Tag:      "game-events",
	})
	if err != nil {
		t.Fatalf("SubmitAndCommit: %v", err)
	}

	// DA result.
	if receipt.DAResult.BlobCount != 2 {
		t.Fatalf("expected 2 blobs in DA, got %d", receipt.DAResult.BlobCount)
	}
	if receipt.DAResult.Height == 0 {
		t.Fatal("expected non-zero DA height")
	}

	// On-chain receipt.
	if receipt.OnChainReceipt.Root == "" {
		t.Fatal("expected non-empty Merkle root")
	}
	if receipt.OnChainReceipt.TxHash == "" {
		t.Fatal("expected non-empty tx hash")
	}
	if receipt.OnChainReceipt.Tag != "game-events" {
		t.Fatalf("tag mismatch: %q", receipt.OnChainReceipt.Tag)
	}
}

func TestDABridge_SubmitAndCommit_NoExec(t *testing.T) {
	da := NewMockDAClient()
	ns := NamespaceFromString("no-exec")
	bridge := NewDABridge(da, nil, ns)

	_, err := bridge.SubmitAndCommit(context.Background(), SubmitAndCommitRequest{
		Blobs: [][]byte{[]byte("x")},
	})
	if err == nil {
		t.Fatal("expected error when executor is nil")
	}
}

func TestDABridge_PollBlobs(t *testing.T) {
	da := NewMockDAClient()
	ns := NamespaceFromString("poll-test")
	bridge := NewDABridge(da, nil, ns)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Pre-submit some blobs.
	res, _ := bridge.Submit(ctx, [][]byte{[]byte("poll-data")}, nil)

	var received []*DABlobEvent
	var mu sync.Mutex

	go func() {
		bridge.PollBlobs(ctx, res.Height, 50*time.Millisecond, func(event *DABlobEvent) error {
			mu.Lock()
			received = append(received, event)
			mu.Unlock()
			return nil
		})
	}()

	// Wait for poller to pick it up.
	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("expected at least one polled event")
	}
	if string(received[0].Blobs[0].Data) != "poll-data" {
		t.Fatalf("wrong polled data: %q", received[0].Blobs[0].Data)
	}
}

func TestDABridge_PollBlobs_HandlerError(t *testing.T) {
	da := NewMockDAClient()
	ns := NamespaceFromString("poll-err")
	bridge := NewDABridge(da, nil, ns)

	ctx := context.Background()
	res, _ := bridge.Submit(ctx, [][]byte{[]byte("data")}, nil)

	err := bridge.PollBlobs(ctx, res.Height, 50*time.Millisecond, func(event *DABlobEvent) error {
		return errors.New("handler failed")
	})
	if err == nil {
		t.Fatal("expected error from handler")
	}
}

func TestDANamespaceConfig_Validate(t *testing.T) {
	// Missing namespace.
	cfg := &DANamespaceConfig{DANodeAddr: "http://localhost:26658"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for nil namespace")
	}

	// Missing address.
	cfg = &DANamespaceConfig{Namespace: NamespaceFromString("test")}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty address")
	}

	// Valid.
	cfg = &DANamespaceConfig{
		Namespace:  NamespaceFromString("test"),
		DANodeAddr: "http://localhost:26658",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
