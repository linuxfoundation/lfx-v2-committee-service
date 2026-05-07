// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		positionalLimit int
		wantPositionals []string
		wantFlagArgs    []string
	}{
		{
			name:            "command and subcommand only",
			args:            []string{"sync", "total-members-attribute"},
			positionalLimit: 2,
			wantPositionals: []string{"sync", "total-members-attribute"},
			wantFlagArgs:    nil,
		},
		{
			name:            "global flags before command",
			args:            []string{"--dry-run", "--debug", "sync", "total-members-attribute"},
			positionalLimit: 2,
			wantPositionals: []string{"sync", "total-members-attribute"},
			wantFlagArgs:    []string{"--dry-run", "--debug"},
		},
		{
			name:            "global flags after subcommand",
			args:            []string{"sync", "total-members-attribute", "--dry-run"},
			positionalLimit: 2,
			wantPositionals: []string{"sync", "total-members-attribute"},
			wantFlagArgs:    []string{"--dry-run"},
		},
		{
			name:            "subcommand flags after subcommand",
			args:            []string{"sync", "total-members-attribute", "--committee-uid=abc", "--sleep=200ms"},
			positionalLimit: 2,
			wantPositionals: []string{"sync", "total-members-attribute"},
			wantFlagArgs:    []string{"--committee-uid=abc", "--sleep=200ms"},
		},
		{
			name:            "flags mixed with positionals",
			args:            []string{"--nats-url=nats://localhost:4222", "sync", "--dry-run", "total-members-attribute", "--sleep=1s"},
			positionalLimit: 2,
			wantPositionals: []string{"sync", "total-members-attribute"},
			wantFlagArgs:    []string{"--nats-url=nats://localhost:4222", "--dry-run", "--sleep=1s"},
		},
		{
			name:            "help flag after subcommand",
			args:            []string{"sync", "total-members-attribute", "--help"},
			positionalLimit: 2,
			wantPositionals: []string{"sync", "total-members-attribute"},
			wantFlagArgs:    []string{"--help"},
		},
		{
			name:            "only one positional",
			args:            []string{"sync", "--help"},
			positionalLimit: 2,
			wantPositionals: []string{"sync"},
			wantFlagArgs:    []string{"--help"},
		},
		{
			name:            "no args",
			args:            []string{},
			positionalLimit: 2,
			wantPositionals: nil,
			wantFlagArgs:    nil,
		},
		{
			name:            "only flags, no positionals",
			args:            []string{"--dry-run", "--debug"},
			positionalLimit: 2,
			wantPositionals: nil,
			wantFlagArgs:    []string{"--dry-run", "--debug"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitArgs(tt.args, tt.positionalLimit)
			if !reflect.DeepEqual(got.Positionals, tt.wantPositionals) {
				t.Errorf("Positionals = %v, want %v", got.Positionals, tt.wantPositionals)
			}
			if !reflect.DeepEqual(got.FlagArgs, tt.wantFlagArgs) {
				t.Errorf("FlagArgs = %v, want %v", got.FlagArgs, tt.wantFlagArgs)
			}
		})
	}
}

func TestHasHelpFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{
			name: "long form --help",
			args: []string{"--help"},
			want: true,
		},
		{
			name: "short form -h",
			args: []string{"-h"},
			want: true,
		},
		{
			name: "help among other flags",
			args: []string{"--dry-run", "--help", "--debug"},
			want: true,
		},
		{
			name: "no help flag",
			args: []string{"--dry-run", "--committee-uid=abc"},
			want: false,
		},
		{
			name: "empty args",
			args: []string{},
			want: false,
		},
		{
			name: "nil args",
			args: nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasHelpFlag(tt.args); got != tt.want {
				t.Errorf("hasHelpFlag(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
