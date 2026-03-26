package task

import "testing"

func TestDisplayIDKeepsFullStoredID(t *testing.T) {
	record := Record{
		Spec: Spec{ID: "01KMFCH1G4JZXV0D0WZXNS5MEZ"},
	}

	if got := record.DisplayID(); got != record.Spec.ID {
		t.Fatalf("expected full id %q, got %q", record.Spec.ID, got)
	}
}
