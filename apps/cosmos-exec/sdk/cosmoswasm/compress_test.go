package cosmoswasm

import (
	"bytes"
	"testing"
)

func TestCompressDecompressRoundTrip(t *testing.T) {
	original := []byte(`{"frame":42,"players":[{"id":"p1","x":100.5,"y":200.3,"score":9999}]}`)

	compressed, err := CompressGzip(original)
	if err != nil {
		t.Fatalf("CompressGzip: %v", err)
	}
	if len(compressed) >= len(original) {
		t.Logf("warning: compressed (%d) >= original (%d) for small input", len(compressed), len(original))
	}

	decompressed, err := DecompressGzip(compressed)
	if err != nil {
		t.Fatalf("DecompressGzip: %v", err)
	}
	if !bytes.Equal(decompressed, original) {
		t.Fatalf("round-trip mismatch")
	}
}

func TestIsGzipCompressed(t *testing.T) {
	compressed, _ := CompressGzip([]byte("hello"))
	if !IsGzipCompressed(compressed) {
		t.Fatal("expected true for gzip data")
	}
	if IsGzipCompressed([]byte("plain text")) {
		t.Fatal("expected false for plain text")
	}
}

func TestMaybeDecompress(t *testing.T) {
	plain := []byte("not compressed")
	out, err := MaybeDecompress(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatal("MaybeDecompress changed non-gzip data")
	}

	compressed, _ := CompressGzip(plain)
	out, err = MaybeDecompress(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, plain) {
		t.Fatal("MaybeDecompress failed on gzip data")
	}
}

func TestCompressIfBeneficial(t *testing.T) {
	// Structured data should compress well.
	structured := bytes.Repeat([]byte(`{"event":"move","x":123,"y":456}`), 100)
	out, ok := CompressIfBeneficial(structured)
	if !ok {
		t.Fatal("expected compression to be beneficial for structured data")
	}
	if len(out) >= len(structured) {
		t.Fatalf("compressed (%d) not smaller than original (%d)", len(out), len(structured))
	}

	// Random data should NOT compress beneficially.
	random := make([]byte, 256)
	for i := range random {
		random[i] = byte(i)
	}
	out2, ok2 := CompressIfBeneficial(random)
	if ok2 && len(out2) >= len(random) {
		t.Logf("random data: compressed=%d original=%d ok=%v", len(out2), len(random), ok2)
	}
}

func BenchmarkCompressGzip(b *testing.B) {
	data := bytes.Repeat([]byte(`{"frame":1,"players":[{"id":"p1","x":100.5,"y":200.3,"score":9999}]}`), 1000)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		CompressGzip(data) //nolint:errcheck
	}
}

func BenchmarkDecompressGzip(b *testing.B) {
	data := bytes.Repeat([]byte(`{"frame":1,"players":[{"id":"p1","x":100.5,"y":200.3,"score":9999}]}`), 1000)
	compressed, _ := CompressGzip(data)
	b.SetBytes(int64(len(compressed)))
	b.ResetTimer()
	for range b.N {
		DecompressGzip(compressed) //nolint:errcheck
	}
}
