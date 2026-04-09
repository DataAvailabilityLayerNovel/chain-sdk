package cosmoswasm

import (
	"fmt"
	"testing"
)

func TestEstimateCost_SmallPayload(t *testing.T) {
	est := EstimateCost(EstimateCostRequest{DataBytes: 1024})
	if est.BlobCommit.TotalGas >= est.DirectTx.TotalGas {
		t.Fatalf("blob-first should be cheaper: blob=%d direct=%d", est.BlobCommit.TotalGas, est.DirectTx.TotalGas)
	}
	if est.SavingsPercent <= 0 {
		t.Fatalf("expected positive savings, got %.2f%%", est.SavingsPercent)
	}
	if est.NumBatches != 1 {
		t.Fatalf("expected 1 batch, got %d", est.NumBatches)
	}
}

func TestEstimateCost_LargePayload(t *testing.T) {
	// 10 MB → should require multiple batches with 4 MB max.
	est := EstimateCost(EstimateCostRequest{DataBytes: 10 * 1024 * 1024, MaxBlobSize: 4 * 1024 * 1024})
	if est.NumBatches < 2 {
		t.Fatalf("expected multiple batches for 10 MB, got %d", est.NumBatches)
	}
	if est.SavingsPercent <= 0 {
		t.Fatalf("expected savings, got %.2f%%", est.SavingsPercent)
	}
}

func TestEstimateCost_ZeroData(t *testing.T) {
	est := EstimateCost(EstimateCostRequest{DataBytes: 0})
	if est.DataBytes != 1 {
		t.Fatalf("expected DataBytes clamped to 1, got %d", est.DataBytes)
	}
}

func TestEstimateCost_Scenarios(t *testing.T) {
	scenarios := []struct {
		name  string
		bytes int
	}{
		{"game-event-1KB", 1024},
		{"snapshot-100KB", 100 * 1024},
		{"snapshot-1MB", 1024 * 1024},
		{"batch-10MB", 10 * 1024 * 1024},
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			est := EstimateCost(EstimateCostRequest{DataBytes: s.bytes})
			t.Logf("%s: direct=%d gas (%.6f TIA) | blob=%d gas (%.6f TIA) | savings=%.1f%% batches=%d",
				s.name, est.DirectTx.TotalGas, est.DirectTx.EstFeeTIA,
				est.BlobCommit.TotalGas, est.BlobCommit.EstFeeTIA,
				est.SavingsPercent, est.NumBatches)
			if est.SavingsPercent < 0 {
				t.Errorf("expected non-negative savings for %s", s.name)
			}
		})
	}
}

func BenchmarkEstimateCost(b *testing.B) {
	for range b.N {
		EstimateCost(EstimateCostRequest{DataBytes: 1024 * 1024})
	}
}

// TestEstimateCost_PrintTable prints a human-readable cost comparison table.
func TestEstimateCost_PrintTable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping table print in short mode")
	}

	sizes := []int{
		1024,             // 1 KB
		10 * 1024,        // 10 KB
		100 * 1024,       // 100 KB
		512 * 1024,       // 512 KB
		1024 * 1024,      // 1 MB
		5 * 1024 * 1024,  // 5 MB
		10 * 1024 * 1024, // 10 MB
	}

	fmt.Println()
	fmt.Printf("%-12s | %-14s | %-14s | %-10s | %-7s\n",
		"Data Size", "Direct Gas", "Blob Gas", "Savings", "Batches")
	fmt.Println("-------------|----------------|----------------|------------|--------")
	for _, sz := range sizes {
		est := EstimateCost(EstimateCostRequest{DataBytes: sz})
		label := formatBytes(sz)
		fmt.Printf("%-12s | %14d | %14d | %9.1f%% | %7d\n",
			label, est.DirectTx.TotalGas, est.BlobCommit.TotalGas,
			est.SavingsPercent, est.NumBatches)
	}
	fmt.Println()
}

func formatBytes(b int) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%d MB", b/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%d KB", b/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}
