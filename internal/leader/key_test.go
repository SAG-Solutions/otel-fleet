package leader

import "testing"

func TestKeyStableAndDistinct(t *testing.T) {
	if Key("alerting") != Key("alerting") {
		t.Error("Key not stable across calls")
	}
	if Key("alerting") == Key("retention") {
		t.Error("distinct worker names produced the same advisory-lock key")
	}
}
