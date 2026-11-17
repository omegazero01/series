package server

import "testing"

func TestTriggerLLMProfilePinUsesTaskActiveProfile(t *testing.T) {
	active := int64(20)
	taskProfile := int64(18)
	s := &Server{m: &Manager{tasks: map[string]*Task{
		"185": {ID: "185", LLMProfileID: &taskProfile, ActiveLLMProfileID: &active},
	}}}
	got := s.triggerLLMProfilePin(185)
	if got == nil || *got != active {
		t.Fatalf("pin=%v, want active profile %d", got, active)
	}
}

func TestTriggerLLMProfilePinFallsBackToTaskProfile(t *testing.T) {
	taskProfile := int64(20)
	s := &Server{m: &Manager{tasks: map[string]*Task{
		"185": {ID: "185", LLMProfileID: &taskProfile},
	}}}
	got := s.triggerLLMProfilePin(185)
	if got == nil || *got != taskProfile {
		t.Fatalf("pin=%v, want task profile %d", got, taskProfile)
	}
}

func TestTriggerLLMProfilePinNoTaskKeepsGlobalFallback(t *testing.T) {
	active := int64(20)
	s := &Server{m: &Manager{tasks: map[string]*Task{
		"185": {ID: "185", ActiveLLMProfileID: &active},
	}}}
	if got := s.triggerLLMProfilePin(0); got != nil {
		t.Fatalf("pin=%v, want nil for interval trigger", got)
	}
	if got := s.triggerLLMProfilePin(999); got != nil {
		t.Fatalf("pin=%v, want nil for unknown task", got)
	}
}
