package cosmoswasm

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
)

// SubmitBlob uploads raw data to the executor's blob store.
// Returns a BlobSubmitResponse containing the SHA-256 commitment that should
// be stored on-chain (e.g. via BuildBlobCommitTx + SubmitTxBytes).
//
// Pattern for data-heavy dApps (games, event logs, assets):
//
//	res, _ := client.SubmitBlob(ctx, largeData)          // cheap: data stays off-chain
//	tx, _ := BuildBlobCommitTx(BlobCommitTxRequest{      // tiny on-chain message
//	    Contract:    contractAddr,
//	    Commitment:  res.Commitment,
//	    Tag:         "snapshot",
//	})
//	client.SubmitTxBytes(ctx, tx)
func (c *Client) SubmitBlob(ctx context.Context, data []byte) (*BlobSubmitResponse, error) {
	if len(data) == 0 {
		return nil, errors.New("blob data cannot be empty")
	}

	res := BlobSubmitResponse{}
	err := c.doJSON(
		ctx,
		"POST",
		blobSubmitPath,
		map[string]string{"data_base64": base64.StdEncoding.EncodeToString(data)},
		&res,
	)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(res.Commitment) == "" {
		return nil, errors.New("blob submit response missing commitment")
	}

	return &res, nil
}

// RetrieveBlob fetches data previously stored via SubmitBlob using its
// SHA-256 commitment string.
func (c *Client) RetrieveBlob(ctx context.Context, commitment string) (*BlobRetrieveResponse, error) {
	commitment = strings.TrimSpace(commitment)
	if commitment == "" {
		return nil, errors.New("commitment is required")
	}

	query := url.Values{}
	query.Set("commitment", commitment)

	res := BlobRetrieveResponse{}
	err := c.doJSON(ctx, "GET", blobRetrievePath+"?"+query.Encode(), nil, &res)
	if err != nil {
		return nil, err
	}

	return &res, nil
}

// RetrieveBlobData is a convenience wrapper that decodes the base64 payload
// returned by RetrieveBlob and returns the raw bytes directly.
func (c *Client) RetrieveBlobData(ctx context.Context, commitment string) ([]byte, error) {
	res, err := c.RetrieveBlob(ctx, commitment)
	if err != nil {
		return nil, err
	}

	data, err := base64.StdEncoding.DecodeString(res.DataBase64)
	if err != nil {
		return nil, err
	}

	return data, nil
}
