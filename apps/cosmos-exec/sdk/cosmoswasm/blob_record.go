package cosmoswasm

// File này gộp luồng blob-first hai-tầng (DA + on-chain) thành một lời gọi tiện
// lợi, NHƯNG vẫn để dev tự dựng execute message qua callback — vì message ghi
// commitment là app-specific (mỗi contract một schema). Tránh ép convention cứng
// như BuildBlobCommitTx/BuildBatchRootTx.

import (
	"context"
	"errors"
	"fmt"
)

// StoreBlobAndRecord thực hiện trọn vòng blob-first cho MỘT blob:
//
//  1. Upload data lên Celestia DA (SubmitBlob) → commitment + height + namespace.
//  2. Gọi buildMsg(blob, nsHex) để dev tự dựng execute message của contract.
//  3. Ký (signer), submit on-chain, và chờ tx vào block.
//
// Trả về `*BlobSubmitResponse` **kể cả khi bước on-chain lỗi** (err != nil) — vì
// blob đã nằm trên DA, dev có thể retry phần ghi on-chain mà KHÔNG cần upload lại.
// txHash rỗng nếu chưa kịp submit.
//
// VI: dùng khi muốn "lưu data lớn off-chain + ghi cam kết on-chain" trong 1 call,
// nhưng vẫn kiểm soát message hợp đồng. Cần signer (đây là đường có ký).
func StoreBlobAndRecord(
	ctx context.Context,
	bc *BlobClient,
	client *Client,
	signer *Signer,
	contract string,
	data []byte,
	buildMsg func(blob *BlobSubmitResponse, nsHex string) any,
) (*BlobSubmitResponse, string, error) {
	if bc == nil || client == nil || signer == nil {
		return nil, "", errors.New("bc, client and signer are required")
	}
	if buildMsg == nil {
		return nil, "", errors.New("buildMsg is required")
	}

	blob, err := bc.SubmitBlob(ctx, data) // bước 1: lên DA.
	if err != nil {
		return nil, "", err
	}

	// bước 2: dev dựng message từ commitment/height/namespace.
	msg := buildMsg(blob, bc.Namespace())

	// bước 3: ký + gửi + chờ. Trả blob kèm theo để retry nếu on-chain lỗi.
	txHash, err := recordOnChain(ctx, client, signer, contract, msg)
	return blob, txHash, err
}

// StoreBatchAndRecord giống StoreBlobAndRecord nhưng cho N blob gộp một batch:
// SubmitBatch trả về MỘT Merkle root đại diện cả batch (chi phí on-chain = 1 lần),
// rồi buildMsg dựng message ghi root đó.
//
// VI: trả `*BlobBatchResponse` kể cả khi on-chain lỗi (root/commitments đã có).
func StoreBatchAndRecord(
	ctx context.Context,
	bc *BlobClient,
	client *Client,
	signer *Signer,
	contract string,
	blobs [][]byte,
	buildMsg func(batch *BlobBatchResponse, nsHex string) any,
) (*BlobBatchResponse, string, error) {
	if bc == nil || client == nil || signer == nil {
		return nil, "", errors.New("bc, client and signer are required")
	}
	if buildMsg == nil {
		return nil, "", errors.New("buildMsg is required")
	}

	batch, err := bc.SubmitBatch(ctx, blobs) // lên DA, build Merkle.
	if err != nil {
		return nil, "", err
	}

	msg := buildMsg(batch, bc.Namespace())
	txHash, err := recordOnChain(ctx, client, signer, contract, msg)
	return batch, txHash, err
}

// recordOnChain dựng execute tx đã ký từ message, submit và chờ vào block.
// Trả txHash (rỗng nếu chưa submit được) + error. Tx fail (code != 0) → error
// kèm log để dev biết contract từ chối vì lý do gì.
func recordOnChain(ctx context.Context, client *Client, signer *Signer, contract string, msg any) (string, error) {
	tx, err := BuildSignedExecuteTx(ctx, client, signer, ExecuteTxRequest{
		Sender:   signer.Address(),
		Contract: contract,
		Msg:      msg,
	})
	if err != nil {
		return "", fmt.Errorf("build record tx: %w", err)
	}
	sub, err := client.SubmitTxBytes(ctx, tx)
	if err != nil {
		return "", fmt.Errorf("submit record tx: %w", err)
	}
	res, err := client.WaitTxResult(ctx, sub.Hash, DefaultPollInterval)
	if err != nil {
		return sub.Hash, fmt.Errorf("wait record tx: %w", err)
	}
	if res.Code != 0 {
		return sub.Hash, fmt.Errorf("record tx failed: code=%d log=%s", res.Code, res.Log)
	}
	return sub.Hash, nil
}
