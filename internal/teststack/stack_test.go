package teststack

import (
	"net/http"
	"testing"
	"time"
)

// TestStackSmokeHubBindsRandomPort verifies that New() spins up a hub
// on 127.0.0.1:0 and reports a real bound port back to the test.
func TestStackSmokeHubBindsRandomPort(t *testing.T) {
	stack := New(t)

	if stack.HubURL() == "" {
		t.Fatal("HubURL() empty after New()")
	}
	if stack.HubHTTP() == "" {
		t.Fatal("HubHTTP() empty after New()")
	}

	// /api/status should respond.
	resp, err := http.Get(stack.HubHTTP() + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("/api/status status = %d, want 200", resp.StatusCode)
	}

	// No machines registered yet.
	if got := stack.MachineCount(); got != 0 {
		t.Errorf("MachineCount on fresh stack = %d, want 0", got)
	}
}

// TestStackSmokeAgentRegisters verifies that an agent started by the
// stack registers a machine with the hub. This is the end-to-end
// smoke test that exercises both extracted packages together.
func TestStackSmokeAgentRegisters(t *testing.T) {
	stack := New(t)
	stack.AddMachine("barn", []uint16{22})
	stack.WaitAgentRegistered("barn", 5*time.Second)

	if got := stack.MachineCount(); got != 1 {
		t.Errorf("MachineCount after register = %d, want 1", got)
	}
}

// TestStackSmokeMultipleMachines verifies that the agent can register
// more than one machine in a single Run.
func TestStackSmokeMultipleMachines(t *testing.T) {
	stack := New(t)
	stack.AddMachine("barn", []uint16{22})
	stack.AddMachine("web01", []uint16{80, 443})
	stack.WaitAgentRegistered("barn", 5*time.Second)
	stack.WaitAgentRegistered("web01", 5*time.Second)

	if got := stack.MachineCount(); got != 2 {
		t.Errorf("MachineCount with two machines = %d, want 2", got)
	}
}

// TestStackResetBetweenRuns verifies that two stacks created in
// sequence in the same process do not see each other's state. This
// is the regression guard for the package-level reset path.
func TestStackResetBetweenRuns(t *testing.T) {
	{
		stack := New(t)
		stack.AddMachine("barn", []uint16{22})
		stack.WaitAgentRegistered("barn", 5*time.Second)
		stack.Close() // explicit, before the second New()
	}
	{
		stack := New(t)
		// Should NOT see barn from the previous stack.
		if got := stack.MachineCount(); got != 0 {
			t.Errorf("MachineCount after reset = %d, want 0", got)
		}
		stack.AddMachine("web01", []uint16{80})
		stack.WaitAgentRegistered("web01", 5*time.Second)
		if got := stack.MachineCount(); got != 1 {
			t.Errorf("MachineCount with web01 = %d, want 1", got)
		}
	}
}
