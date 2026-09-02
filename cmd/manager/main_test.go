package main

import (
	"io"
	"testing"
)

func TestParseRuntimeOptionsLeaderElection(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "disabled by default", want: false},
		{name: "enabled", args: []string{"--leader-elect"}, want: true},
		{name: "explicitly disabled", args: []string{"--leader-elect=false"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options, err := parseRuntimeOptions(tt.args, io.Discard)
			if err != nil {
				t.Fatalf("parseRuntimeOptions() error = %v", err)
			}
			if options.leaderElection != tt.want {
				t.Errorf("leaderElection = %t, want %t", options.leaderElection, tt.want)
			}
		})
	}
}

func TestParseRuntimeOptionsBindsZapFlags(t *testing.T) {
	options, err := parseRuntimeOptions([]string{
		"--zap-devel=false",
		"--zap-encoder=json",
		"--zap-log-level=debug",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseRuntimeOptions() error = %v", err)
	}
	if options.zapOptions.Development {
		t.Error("zap development mode remains enabled after --zap-devel=false")
	}
	if options.zapOptions.NewEncoder == nil {
		t.Error("zap encoder flag was not bound")
	}
	if options.zapOptions.Level == nil {
		t.Error("zap log-level flag was not bound")
	}
}

func TestParseRuntimeOptionsRejectsInvalidArguments(t *testing.T) {
	tests := [][]string{
		{"--unknown"},
		{"unexpected"},
	}
	for _, args := range tests {
		if _, err := parseRuntimeOptions(args, io.Discard); err == nil {
			t.Errorf("parseRuntimeOptions(%v) expected an error", args)
		}
	}
}

func TestNewManagerOptionsUsesParsedLeaderElection(t *testing.T) {
	enabled := newManagerOptions(runtimeOptions{leaderElection: true}, false, "")
	if !enabled.LeaderElection {
		t.Error("LeaderElection = false, want true")
	}
	if enabled.WebhookServer != nil {
		t.Error("WebhookServer is configured when webhooks are disabled")
	}

	disabled := newManagerOptions(runtimeOptions{}, true, "/certs")
	if disabled.LeaderElection {
		t.Error("LeaderElection = true, want false")
	}
	if disabled.WebhookServer == nil {
		t.Error("WebhookServer is nil when webhooks are enabled")
	}
}
