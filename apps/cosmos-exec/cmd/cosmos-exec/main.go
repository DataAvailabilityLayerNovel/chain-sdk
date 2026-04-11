package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	db "github.com/cometbft/cometbft-db"
	abciserver "github.com/cometbft/cometbft/abci/server"
	"github.com/cometbft/cometbft/libs/log"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/app"
)

func main() {
	listenAddr := flag.String("address", "tcp://0.0.0.0:26658", "ABCI listen address")
	home := flag.String("home", ".cosmos-exec", "home directory")
	inMemory := flag.Bool("in-memory", false, "Use in-memory DB (avoids file lock, non-persistent)")
	flag.Parse()

	if err := os.MkdirAll(*home, 0o755); err != nil {
		die("failed to create home directory", err)
	}

	database, err := openDatabase(filepath.Join(*home, "data"), *inMemory)
	if err != nil {
		die("failed to open database", err)
	}
	defer func() {
		_ = database.Close()
	}()

	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))
	application := app.New(logger, database)

	srv, err := abciserver.NewServer(*listenAddr, "socket", application)
	if err != nil {
		die("failed to create ABCI server", err)
	}

	srv.SetLogger(logger)

	if err := srv.Start(); err != nil {
		die("failed to start ABCI server", err)
	}
	defer srv.Stop()

	fmt.Printf("cosmos-exec ABCI server listening on %s\n", *listenAddr)

	select {}
}

func openDatabase(dataDir string, inMemory bool) (db.DB, error) {
	if inMemory {
		return db.NewMemDB(), nil
	}

	database, err := db.NewGoLevelDB("application", dataDir)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "resource temporarily unavailable") {
			return nil, fmt.Errorf("database lock detected at %s (another cosmos-exec process may still be running). stop the other process or run with --in-memory: %w", dataDir, err)
		}
		return nil, err
	}

	return database, nil
}

func die(msg string, err error) {
	if err == nil {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "%s: %v\n", msg, err)

	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		fmt.Fprintf(os.Stderr, "path: %s\n", pathErr.Path)
	}

	os.Exit(1)
}
