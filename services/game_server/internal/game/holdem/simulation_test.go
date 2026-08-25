package holdem

import "testing"

func TestSimulateHundredThousandHands(t *testing.T) {
	const seed int64 = 20260824
	result, err := SimulateHands(100_000, seed)
	if err != nil {
		t.Fatalf("SimulateHands seed=%d: %v", seed, err)
	}
	if result.Hands != 100_000 || result.Actions == 0 || result.MaximumHandActions <= 0 {
		t.Fatalf("unexpected simulation result: %#v", result)
	}
	t.Logf(
		"seed=%d hands=%d actions=%d maximumHandActions=%d",
		result.Seed,
		result.Hands,
		result.Actions,
		result.MaximumHandActions,
	)
}
