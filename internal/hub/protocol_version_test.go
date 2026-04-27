package hub

import (
	"encoding/json"
	"testing"
)

// TestSignalingMsg_ProtocolVersionAbsent confirms that a register-shape
// JSON payload that omits the protocolVersion field decodes to
// signalingMsg.ProtocolVersion == 0 (the zero value), which the
// register handler then treats as 1 per the v1.x cross-version
// compatibility rule documented in DESIGN.md section 6.6.5. This
// test is the wire-level back-compat anchor: as long as it passes,
// pre-0.16 agents that did not send the field continue to work
// against any 1.x hub.
func TestSignalingMsg_ProtocolVersionAbsent(t *testing.T) {
	raw := []byte(`{
		"type": "register",
		"agentId": "00000000-0000-0000-0000-000000000001",
		"machineName": "barn",
		"token": "deadbeef"
	}`)
	var msg signalingMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if msg.ProtocolVersion != 0 {
		t.Errorf("ProtocolVersion absent from JSON: got %d, want 0 (the back-compat sentinel)", msg.ProtocolVersion)
	}
	if msg.Type != "register" || msg.MachineName != "barn" {
		t.Fatalf("other fields lost during decode: %+v", msg)
	}
}

// TestSignalingMsg_ProtocolVersionPresent confirms the field round-trips
// correctly when present.
func TestSignalingMsg_ProtocolVersionPresent(t *testing.T) {
	raw := []byte(`{
		"type": "register",
		"protocolVersion": 1,
		"agentId": "00000000-0000-0000-0000-000000000001",
		"machineName": "barn",
		"token": "deadbeef"
	}`)
	var msg signalingMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if msg.ProtocolVersion != 1 {
		t.Errorf("ProtocolVersion = %d, want 1", msg.ProtocolVersion)
	}
}

// TestSignalingMsg_ProtocolVersionEncodedOmitemptyZero confirms that
// re-encoding a signalingMsg with ProtocolVersion=0 omits the field
// entirely (the omitempty tag), so a hub that records 0 does not
// emit `"protocolVersion": 0` to clients reading /api/status.
func TestSignalingMsg_ProtocolVersionEncodedOmitemptyZero(t *testing.T) {
	msg := signalingMsg{Type: "register", MachineName: "barn"}
	out, err := json.Marshal(&msg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got := string(out); contains(got, `"protocolVersion"`) {
		t.Errorf("expected omitempty to drop protocolVersion=0; got: %s", got)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
