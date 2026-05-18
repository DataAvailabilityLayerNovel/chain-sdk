package app

import (
	"testing"
)

func TestFeePolicyDisabledByDefault(t *testing.T) {
	t.Setenv("COSMOS_EXEC_MIN_GAS_PRICE", "")
	t.Setenv("COSMOS_EXEC_GAS_DENOM", "")

	p := feePolicyFromEnv()
	if p.enforced() {
		t.Fatal("fee policy must be OFF when COSMOS_EXEC_MIN_GAS_PRICE is unset")
	}
	if p.denom != "ustake" {
		t.Fatalf("default denom = %q, want ustake", p.denom)
	}
	if !p.requiredFee(1_000_000).IsZero() {
		t.Fatal("required fee must be 0 when policy is off")
	}
}

func TestFeePolicyEnforcedMath(t *testing.T) {
	t.Setenv("COSMOS_EXEC_MIN_GAS_PRICE", "0.000001")
	t.Setenv("COSMOS_EXEC_GAS_DENOM", "uatom")

	p := feePolicyFromEnv()
	if !p.enforced() {
		t.Fatal("fee policy must be ON when COSMOS_EXEC_MIN_GAS_PRICE is positive")
	}
	if p.denom != "uatom" {
		t.Fatalf("denom = %q, want uatom", p.denom)
	}

	// 200000 gas * 0.000001 = 0.2 → ceil → 1
	if got := p.requiredFee(200000); got.Int64() != 1 {
		t.Fatalf("requiredFee(200000) = %s, want 1 (ceil of 0.2)", got)
	}
	// 2_000_000 gas * 0.000001 = 2 → 2
	if got := p.requiredFee(2_000_000); got.Int64() != 2 {
		t.Fatalf("requiredFee(2_000_000) = %s, want 2", got)
	}
}

func TestFeePolicyInvalidPriceIsOff(t *testing.T) {
	t.Setenv("COSMOS_EXEC_MIN_GAS_PRICE", "not-a-number")
	if feePolicyFromEnv().enforced() {
		t.Fatal("garbage min gas price must leave policy OFF, not panic/enforce")
	}
}
