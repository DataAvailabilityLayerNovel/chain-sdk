package cosmoswasm

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"testing"

	libshare "github.com/celestiaorg/go-square/v3/share"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/merkle"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/pkg/da/jsonrpc"
)

const fakeDAHeight = 42

// newFakeBlobClient builds a BlobClient backed by an in-memory fake Celestia
// client. Submit stores the (real, NewBlobV0-computed) blobs keyed by their
// commitment; Get returns them. Round-trips use real commitments, so the test
// exercises the actual commitment logic — only the network is faked.
//
// Returns the store map so a test can tamper with stored data.
func newFakeBlobClient(t *testing.T) (*BlobClient, map[string]*jsonrpc.Blob) {
	t.Helper()
	nsObj := NamespaceFromString("test-game")
	ns, err := libshare.NewNamespaceFromBytes(nsObj.Bytes())
	if err != nil {
		t.Fatalf("resolve namespace: %v", err)
	}

	store := map[string]*jsonrpc.Blob{}
	var cl jsonrpc.Client
	cl.Blob.Internal.Submit = func(_ context.Context, blobs []*jsonrpc.Blob, _ *jsonrpc.SubmitOptions) (uint64, error) {
		for _, b := range blobs {
			store[hex.EncodeToString(b.Commitment)] = b
		}
		return fakeDAHeight, nil
	}
	cl.Blob.Internal.Get = func(_ context.Context, _ uint64, _ libshare.Namespace, com jsonrpc.Commitment) (*jsonrpc.Blob, error) {
		b, ok := store[hex.EncodeToString(com)]
		if !ok {
			return nil, fmt.Errorf("blob not found")
		}
		return b, nil
	}

	return &BlobClient{da: &cl, ns: ns, nsHex: nsObj.Hex()}, store
}

func TestBlobClient_SubmitRetrieveRoundTrip(t *testing.T) {
	bc, _ := newFakeBlobClient(t)
	ctx := context.Background()
	data := []byte(`{"player":"alice","score":9001,"map":"dust2"}`)

	res, err := bc.SubmitBlob(ctx, data)
	if err != nil {
		t.Fatalf("SubmitBlob: %v", err)
	}
	if res.Height != fakeDAHeight {
		t.Errorf("Height = %d, want %d", res.Height, fakeDAHeight)
	}
	if res.Commitment == "" {
		t.Error("commitment is empty")
	}
	if res.Namespace != bc.Namespace() {
		t.Errorf("Namespace = %q, want %q", res.Namespace, bc.Namespace())
	}
	if res.Size != len(data) {
		t.Errorf("Size = %d, want %d", res.Size, len(data))
	}

	got, err := bc.RetrieveBlob(ctx, res.Height, res.Commitment)
	if err != nil {
		t.Fatalf("RetrieveBlob: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("retrieved data mismatch:\n got=%s\nwant=%s", got, data)
	}
}

func TestBlobClient_VerifyBlob(t *testing.T) {
	bc, store := newFakeBlobClient(t)
	ctx := context.Background()
	data := []byte("telemetry-frame-0001")

	res, err := bc.SubmitBlob(ctx, data)
	if err != nil {
		t.Fatalf("SubmitBlob: %v", err)
	}

	ok, err := bc.VerifyBlob(ctx, res.Height, res.Commitment)
	if err != nil {
		t.Fatalf("VerifyBlob: %v", err)
	}
	if !ok {
		t.Error("VerifyBlob = false for intact blob, want true")
	}

	// Tamper: overwrite the stored blob with different data under the SAME
	// commitment key. Now retrieval returns data that no longer hashes to the
	// recorded commitment → verification must fail.
	tampered, err := jsonrpc.NewBlobV0(bc.ns, []byte("telemetry-frame-XXXX"))
	if err != nil {
		t.Fatalf("build tampered blob: %v", err)
	}
	store[res.Commitment] = tampered

	ok, err = bc.VerifyBlob(ctx, res.Height, res.Commitment)
	if err != nil {
		t.Fatalf("VerifyBlob (tampered): %v", err)
	}
	if ok {
		t.Error("VerifyBlob = true for tampered blob, want false")
	}
}

func TestBlobClient_SubmitBatch(t *testing.T) {
	bc, _ := newFakeBlobClient(t)
	ctx := context.Background()
	blobs := [][]byte{
		[]byte("event-1"),
		[]byte("event-2"),
		[]byte("event-3"),
	}

	res, err := bc.SubmitBatch(ctx, blobs)
	if err != nil {
		t.Fatalf("SubmitBatch: %v", err)
	}
	if res.Count != len(blobs) {
		t.Errorf("Count = %d, want %d", res.Count, len(blobs))
	}
	if res.Height != fakeDAHeight {
		t.Errorf("Height = %d, want %d", res.Height, fakeDAHeight)
	}
	if res.Root == "" {
		t.Error("Root is empty")
	}

	// Every commitment must be retrievable and a member of the recorded root.
	for i, com := range res.Commitments {
		got, err := bc.RetrieveBlob(ctx, res.Height, com)
		if err != nil {
			t.Fatalf("RetrieveBlob[%d]: %v", i, err)
		}
		if !bytes.Equal(got, blobs[i]) {
			t.Errorf("blob[%d] mismatch: got=%s want=%s", i, got, blobs[i])
		}

		root, path, err := merkle.BuildProof(res.Commitments, i)
		if err != nil {
			t.Fatalf("BuildProof[%d]: %v", i, err)
		}
		if root != res.Root {
			t.Errorf("proof root[%d] = %s, want %s", i, root, res.Root)
		}
		if err := merkle.Verify(res.Root, com, path); err != nil {
			t.Errorf("Verify[%d] failed: %v", i, err)
		}
	}
}

func TestBlobClient_SubmitBlob_Validation(t *testing.T) {
	bc, _ := newFakeBlobClient(t)
	ctx := context.Background()

	if _, err := bc.SubmitBlob(ctx, nil); err == nil {
		t.Error("SubmitBlob(nil) should error")
	}
	if _, err := bc.SubmitBlob(ctx, make([]byte, MaxBlobSize+1)); err == nil {
		t.Error("SubmitBlob(oversize) should error")
	}
}

func TestBlobClient_RetrieveBlob_RequiresHeight(t *testing.T) {
	bc, _ := newFakeBlobClient(t)
	if _, err := bc.RetrieveBlob(context.Background(), 0, "deadbeef"); err == nil {
		t.Error("RetrieveBlob(height=0) should error")
	}
}
