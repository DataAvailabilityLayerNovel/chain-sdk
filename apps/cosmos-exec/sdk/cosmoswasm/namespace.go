// package cosmoswasm: file định nghĩa Namespace — định danh "vùng" trên DA
// layer (Celestia) để các app-chain không đụng dữ liệu của nhau dù cùng dùng
// 1 DA. Mỗi chain CHỌN 1 namespace duy nhất.
package cosmoswasm

import (
	"crypto/sha256" // băm SHA-256 (tạo namespace từ chuỗi tên).
	"encoding/hex"  // mã hoá/giải mã hex (namespace dễ đọc dạng hex).
	"fmt"           // tạo & format lỗi.
	"strings"       // bỏ tiền tố "0x" trong chuỗi hex.
)

const (
	// NamespaceSize is the total byte length of a Celestia namespace (version + 28-byte ID).
	// VI: 29 byte tổng = 1 byte version + 28 byte ID.
	NamespaceSize = 29
	// namespaceV0PrefixSize is the number of leading zero bytes required in version-0 namespace IDs.
	// VI: namespace V0 BẮT BUỘC 18 byte đầu của ID là 0 (chuẩn Celestia).
	namespaceV0PrefixSize = 18
	// namespaceV0DataSize is the number of usable bytes in a version-0 namespace ID.
	// VI: 28 - 18 = 10 byte còn lại là vùng "tự do" cho data app-chain.
	namespaceV0DataSize = 10
)

// Namespace represents a Celestia namespace for blob isolation.
// Each app-chain should use a unique namespace so its blobs don't collide
// with other chains on the same DA layer.
//
// VI: struct namespace — version + ID 28 byte. Hai chain dùng cùng namespace
// thì blob của chúng sẽ trộn lẫn trên DA → mọi chain PHẢI có namespace riêng.
type Namespace struct {
	// Version is the namespace version (currently only 0 is standard).
	Version uint8
	// ID is the 28-byte namespace identifier.
	// VI: [28]byte là MẢNG cố định (không phải slice) — kích thước là 1 phần
	// của kiểu, không thể append.
	ID [28]byte
}

// NewNamespaceV0 creates a version-0 namespace from up to 10 bytes of data.
// The first 18 bytes of the ID are zero-padded per Celestia v0 rules.
//
// VI: tạo namespace V0 từ ≤10 byte data. 18 byte đầu auto = 0; data nằm
// trong 10 byte cuối.
func NewNamespaceV0(data []byte) (*Namespace, error) {
	if len(data) > namespaceV0DataSize {
		return nil, fmt.Errorf("namespace data too long: got %d bytes, max %d", len(data), namespaceV0DataSize)
	}

	ns := &Namespace{Version: 0}
	// copy(dst, src): chép byte sang slice/mảng đích. Lát ns.ID[18:] = 10
	// byte cuối — đây là vùng nhận data. ID đã được khởi tạo 0 tự động (zero
	// value của mảng) nên 18 byte đầu KHÔNG cần set thủ công.
	copy(ns.ID[namespaceV0PrefixSize:], data)
	return ns, nil
}

// NamespaceFromString deterministically derives a version-0 namespace from a
// human-readable string (e.g. "my-game-chain", "defi-app").
// This is the recommended way for app developers to pick a namespace.
//
// VI: cách KHUYẾN NGHỊ — truyền tên chain, hàm hash SHA-256 rồi lấy 10 byte
// đầu làm data namespace. Cùng tên → cùng namespace (deterministic).
func NamespaceFromString(s string) *Namespace {
	hash := sha256.Sum256([]byte(s)) // hash 32 byte.
	// hash[:namespaceV0DataSize] = lát 10 byte đầu của hash.
	// Bỏ qua error (`_`) vì hash[:10] chắc chắn <= 10 byte → không thể fail.
	ns, _ := NewNamespaceV0(hash[:namespaceV0DataSize])
	return ns
}

// NamespaceFromHex parses a hex-encoded namespace (with or without 0x prefix).
//
// VI: parse namespace từ chuỗi hex 58 ký tự (= 29 byte * 2). Chấp nhận cả
// có & không có tiền tố "0x".
func NamespaceFromHex(hexStr string) (*Namespace, error) {
	hexStr = strings.TrimPrefix(hexStr, "0x")

	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}

	if len(b) != NamespaceSize {
		return nil, fmt.Errorf("invalid namespace size: expected %d bytes, got %d", NamespaceSize, len(b))
	}

	ns := &Namespace{Version: b[0]}
	copy(ns.ID[:], b[1:]) // 28 byte còn lại = ID.

	// Validate V0: 18 byte đầu của ID phải là 0 (theo chuẩn Celestia).
	if ns.Version == 0 {
		// for i := range N : duyệt i từ 0 tới N-1.
		for i := range namespaceV0PrefixSize {
			if ns.ID[i] != 0 {
				return nil, fmt.Errorf("invalid v0 namespace: first %d bytes of ID must be zero", namespaceV0PrefixSize)
			}
		}
	}

	return ns, nil
}

// Bytes returns the 29-byte wire representation (version + ID).
//
// VI: serialize namespace ra []byte 29 bytes — dạng được gửi xuống DA.
func (n *Namespace) Bytes() []byte {
	result := make([]byte, NamespaceSize)
	result[0] = n.Version
	copy(result[1:], n.ID[:]) // copy mảng cố định → slice qua n.ID[:].
	return result
}

// Hex returns the 0x-prefixed hex encoding.
// VI: trả chuỗi hex có "0x" đầu (chuẩn hiển thị).
func (n *Namespace) Hex() string {
	return "0x" + hex.EncodeToString(n.Bytes())
}

// String returns the hex representation for display.
// VI: implement interface fmt.Stringer — fmt.Print sẽ tự gọi.
func (n *Namespace) String() string {
	return n.Hex()
}

// Equal checks if two namespaces are identical.
//
// VI: so sánh 2 namespace. Xử lý nil-safe: nil == nil = true, nil != x = false.
func (n *Namespace) Equal(other *Namespace) bool {
	if n == nil || other == nil {
		// Cả hai nil → true; chỉ 1 nil → false. So sánh con trỏ trực tiếp đủ.
		return n == other
	}
	// So sánh mảng cố định bằng `==` được (khác slice — slice không so sánh được).
	return n.Version == other.Version && n.ID == other.ID
}
