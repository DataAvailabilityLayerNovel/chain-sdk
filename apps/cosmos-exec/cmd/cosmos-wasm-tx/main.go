package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	txv1beta1 "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "default-sender":
		fmt.Println(defaultSender())
	case "store":
		if err := runStore(os.Args[2:]); err != nil {
			die(err)
		}
	case "instantiate":
		if err := runInstantiate(os.Args[2:]); err != nil {
			die(err)
		}
	case "execute":
		if err := runExecute(os.Args[2:]); err != nil {
			die(err)
		}
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "  cosmos-wasm-tx default-sender\n")
	fmt.Fprintf(os.Stderr, "  cosmos-wasm-tx store --wasm <file> [--sender <addr>] [--out base64|hex]\n")
	fmt.Fprintf(os.Stderr, "  cosmos-wasm-tx instantiate --code-id <id> --msg <json> [--label <label>] [--sender <addr>] [--admin <addr>] [--out base64|hex]\n")
	fmt.Fprintf(os.Stderr, "  cosmos-wasm-tx execute --contract <addr> --msg <json> [--sender <addr>] [--out base64|hex]\n")
}

func runStore(args []string) error {
	fs := flag.NewFlagSet("store", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	wasmPath := fs.String("wasm", "", "path to wasm bytecode")
	sender := fs.String("sender", defaultSender(), "sender address")
	out := fs.String("out", "base64", "output encoding: base64|hex")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *wasmPath == "" {
		return fmt.Errorf("--wasm is required")
	}

	bz, err := os.ReadFile(*wasmPath)
	if err != nil {
		return fmt.Errorf("read wasm file: %w", err)
	}

	tx, err := buildProtoTxBytes(&wasmtypes.MsgStoreCode{
		Sender:       *sender,
		WASMByteCode: bz,
	})
	if err != nil {
		return err
	}

	printTx(tx, *out)
	return nil
}

func runInstantiate(args []string) error {
	fs := flag.NewFlagSet("instantiate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	codeID := fs.Uint64("code-id", 0, "code id")
	msg := fs.String("msg", "{}", "init msg json")
	label := fs.String("label", "wasm-via-fullnode", "contract label")
	sender := fs.String("sender", defaultSender(), "sender address")
	admin := fs.String("admin", "", "admin address (optional)")
	out := fs.String("out", "base64", "output encoding: base64|hex")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *codeID == 0 {
		return fmt.Errorf("--code-id is required")
	}

	msgBz := []byte(strings.TrimSpace(*msg))
	if !json.Valid(msgBz) {
		return fmt.Errorf("--msg must be valid json")
	}

	instantiate := &wasmtypes.MsgInstantiateContract{
		Sender: *sender,
		CodeID: *codeID,
		Label:  *label,
		Msg:    msgBz,
	}
	if strings.TrimSpace(*admin) != "" {
		instantiate.Admin = strings.TrimSpace(*admin)
	}

	tx, err := buildProtoTxBytes(instantiate)
	if err != nil {
		return err
	}

	printTx(tx, *out)
	return nil
}

func runExecute(args []string) error {
	fs := flag.NewFlagSet("execute", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	contract := fs.String("contract", "", "contract address")
	msg := fs.String("msg", "", "execute msg json")
	sender := fs.String("sender", defaultSender(), "sender address")
	out := fs.String("out", "base64", "output encoding: base64|hex")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *contract == "" {
		return fmt.Errorf("--contract is required")
	}
	if *msg == "" {
		return fmt.Errorf("--msg is required")
	}

	msgBz := []byte(strings.TrimSpace(*msg))
	if !json.Valid(msgBz) {
		return fmt.Errorf("--msg must be valid json")
	}

	tx, err := buildProtoTxBytes(&wasmtypes.MsgExecuteContract{
		Sender:   *sender,
		Contract: *contract,
		Msg:      msgBz,
	})
	if err != nil {
		return err
	}

	printTx(tx, *out)
	return nil
}

func buildProtoTxBytes(msgs ...sdk.Msg) ([]byte, error) {
	packedMsgs := make([]*codectypes.Any, 0, len(msgs))
	for _, msg := range msgs {
		anyMsg, err := codectypes.NewAnyWithValue(msg)
		if err != nil {
			return nil, err
		}
		packedMsgs = append(packedMsgs, anyMsg)
	}

	bodyBytes, err := proto.Marshal(&txv1beta1.TxBody{Messages: packedMsgs})
	if err != nil {
		return nil, err
	}

	authInfoBytes, err := proto.Marshal(&txv1beta1.AuthInfo{})
	if err != nil {
		return nil, err
	}

	return proto.Marshal(&txv1beta1.TxRaw{
		BodyBytes:     bodyBytes,
		AuthInfoBytes: authInfoBytes,
		Signatures:    nil,
	})
}

func printTx(tx []byte, out string) {
	switch strings.ToLower(strings.TrimSpace(out)) {
	case "hex":
		fmt.Println(hex.EncodeToString(tx))
	default:
		fmt.Println(base64.StdEncoding.EncodeToString(tx))
	}
}

func defaultSender() string {
	return sdk.AccAddress(bytes.Repeat([]byte{0x11}, 20)).String()
}

func die(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
