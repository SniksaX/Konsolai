package main

import (
	"errors"
	"flag"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    invocation
		wantErr error
	}{
		{
			name: "do default",
			args: []string{"create a folder"},
			want: invocation{Mode: modeDo, Request: "create a folder"},
		},
		{
			name: "do explicit",
			args: []string{"do", "create a folder"},
			want: invocation{Mode: modeDo, Request: "create a folder"},
		},
		{
			name: "howto",
			args: []string{"howto", "check disk usage"},
			want: invocation{Mode: modeHowto, Request: "check disk usage"},
		},
		{
			name: "code",
			args: []string{"code", "add a /health endpoint"},
			want: invocation{Mode: modeCode, Request: "add a /health endpoint"},
		},
		{
			name: "yes then code",
			args: []string{"--yes", "code", "add tests"},
			want: invocation{Mode: modeCode, Request: "add tests", Yes: true},
		},
		{
			name: "new session",
			args: []string{"new", "what is my favourite number?"},
			want: invocation{Mode: modeDo, Request: "what is my favourite number?", New: true},
		},
		{
			name: "profile and new code",
			args: []string{"--profile", "home", "new", "code", "add tests"},
			want: invocation{Mode: modeCode, Request: "add tests", Profile: "home", New: true},
		},
		{
			name: "history",
			args: []string{"history"},
			want: invocation{Cmd: cmdHistory},
		},
		{
			name: "remember project",
			args: []string{"remember", "--project", "I use pnpm"},
			want: invocation{Cmd: cmdRemember, Fact: "I use pnpm", FactScope: "project"},
		},
		{
			name: "forget user",
			args: []string{"forget", "user", "3"},
			want: invocation{Cmd: cmdForget, FactScope: "user", FactID: 3},
		},
		{
			name: "double dash",
			args: []string{"--", "howto", "ss -l"},
			want: invocation{Mode: modeHowto, Request: "ss -l"},
		},
		{
			name:    "no args",
			args:    nil,
			wantErr: errRequest,
		},
		{
			name:    "empty request",
			args:    []string{"   "},
			wantErr: errRequest,
		},
		{
			name:    "mode without request",
			args:    []string{"code"},
			wantErr: errRequest,
		},
		{
			name:    "unquoted extra args",
			args:    []string{"howto", "check", "disk", "usage"},
			wantErr: errRequest,
		},
		{
			name:    "empty after mode",
			args:    []string{"do", ""},
			wantErr: errRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err=%v want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %+v want %+v", got, tt.want)
			}
		})
	}
}

func TestParseArgsHelp(t *testing.T) {
	_, err := parseArgs([]string{"-h"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("got %v", err)
	}
}
