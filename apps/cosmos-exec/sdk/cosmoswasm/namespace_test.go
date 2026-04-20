package cosmoswasm

import (
	"testing"
)

func TestNamespaceV0(t *testing.T) {
	data := []byte("myapp")
	ns, err := NewNamespaceV0(data)
	if err != nil {
		t.Fatalf("NewNamespaceV0: %v", err)
	}
	if ns.Version != 0 {
		t.Fatalf("expected version 0, got %d", ns.Version)
	}

	b := ns.Bytes()
	if len(b) != NamespaceSize {
		t.Fatalf("expected %d bytes, got %d", NamespaceSize, len(b))
	}
	if b[0] != 0 {
		t.Fatal("version byte should be 0")
	}

	// First 18 bytes of ID should be zero.
	for i := 1; i <= 18; i++ {
		if b[i] != 0 {
			t.Fatalf("byte %d should be 0, got %d", i, b[i])
		}
	}
}

func TestNamespaceV0_TooLong(t *testing.T) {
	data := make([]byte, 11) // exceeds 10 byte limit
	_, err := NewNamespaceV0(data)
	if err == nil {
		t.Fatal("expected error for data > 10 bytes")
	}
}

func TestNamespaceFromString(t *testing.T) {
	ns1 := NamespaceFromString("my-game")
	ns2 := NamespaceFromString("my-game")
	ns3 := NamespaceFromString("other-app")

	if !ns1.Equal(ns2) {
		t.Fatal("same string should produce same namespace")
	}
	if ns1.Equal(ns3) {
		t.Fatal("different strings should produce different namespaces")
	}
	if ns1.Version != 0 {
		t.Fatalf("expected version 0, got %d", ns1.Version)
	}
}

func TestNamespaceFromHex_Roundtrip(t *testing.T) {
	original := NamespaceFromString("test-roundtrip")
	hexStr := original.Hex()

	parsed, err := NamespaceFromHex(hexStr)
	if err != nil {
		t.Fatalf("NamespaceFromHex: %v", err)
	}
	if !original.Equal(parsed) {
		t.Fatalf("roundtrip failed: %s vs %s", original.Hex(), parsed.Hex())
	}
}

func TestNamespaceFromHex_Invalid(t *testing.T) {
	tests := []struct {
		name string
		hex  string
	}{
		{"bad hex", "0xZZZZ"},
		{"too short", "0x00"},
		{"too long", "0x" + "00" + "0000000000000000000000000000000000000000000000000000000000" + "FF"},
	}

	for _, tt := range tests {
		_, err := NamespaceFromHex(tt.hex)
		if err == nil {
			t.Errorf("%s: expected error", tt.name)
		}
	}
}

func TestNamespaceEqual_Nil(t *testing.T) {
	ns := NamespaceFromString("test")
	if ns.Equal(nil) {
		t.Fatal("non-nil should not equal nil")
	}

	var nilNs *Namespace
	if !nilNs.Equal(nil) {
		t.Fatal("nil should equal nil")
	}
}
