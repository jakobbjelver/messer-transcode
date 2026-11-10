package config

import (
	"os"
	"strings"
	"testing"
)

func exampleJSON(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestQuarantineDefaultsToCopy(t *testing.T) {
	// A config written before the option existed must keep its safety net: an
	// absent key cannot mean "off".
	data := strings.Replace(exampleJSON(t), `,
    "quarantine": "copy"`, "", 1)
	c, err := Decode([]byte(data))
	if err != nil {
		t.Fatalf("decode without the key: %v", err)
	}
	if !c.Safety.QuarantineEnabled() {
		t.Fatal("a missing quarantine key must leave the copy enabled")
	}
}

func TestQuarantineOffIsAccepted(t *testing.T) {
	data := strings.Replace(exampleJSON(t), `"quarantine": "copy"`, `"quarantine": "off"`, 1)
	c, err := Decode([]byte(data))
	if err != nil {
		t.Fatalf("decode off: %v", err)
	}
	if c.Safety.QuarantineEnabled() {
		t.Fatal("quarantine off must disable the copy")
	}
}

func TestQuarantineRejectsUnknownMode(t *testing.T) {
	data := strings.Replace(exampleJSON(t), `"quarantine": "copy"`, `"quarantine": "sometimes"`, 1)
	if _, err := Decode([]byte(data)); err == nil {
		t.Fatal("an unknown quarantine mode must be rejected")
	}
}

func TestAudioConditionsAreValid(t *testing.T) {
	// The shipped example carries the audio-conditioned remux rules; decoding it
	// proves the schema accepts the two new condition fields.
	c := example(t)
	found := false
	for _, r := range c.Rules {
		if len(r.When.AudioCodecIn) > 0 && r.Do == "remux" {
			found = true
		}
	}
	if !found {
		t.Fatal("the example must carry an audio-conditioned remux rule")
	}
}

func TestAudioConditionsRejectUnknownCodec(t *testing.T) {
	data := strings.Replace(exampleJSON(t), `"audio_codec_in": ["truehd"`, `"audio_codec_in": ["atmos-x"`, 1)
	if _, err := Decode([]byte(data)); err == nil {
		t.Fatal("an unknown audio codec in a rule must be rejected")
	}
}
