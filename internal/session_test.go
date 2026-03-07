package app

import "testing"

func TestSignificantEnergy(t *testing.T) {
	quiet := make([]byte, 640)
	if significantEnergy(quiet) {
		t.Fatalf("quiet frame should not trigger")
	}
	loud := make([]byte, 640)
	for i := 0; i+1 < len(loud); i += 2 {
		loud[i] = 0x60
		loud[i+1] = 0x09
	}
	if !significantEnergy(loud) {
		t.Fatalf("loud frame should trigger")
	}
}
