package task

import "testing"

func TestDisplayIDUsesShortID(t *testing.T) {
	record := Record{
		Spec: Spec{ID: "01KMFCH1G4JZXV0D0WZXNS5MEZ"},
	}

	if got := record.DisplayID(); got != "01KMFC" {
		t.Fatalf("expected short id %q, got %q", "01KMFC", got)
	}
}
