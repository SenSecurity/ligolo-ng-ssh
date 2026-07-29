// Ligolo-ng
// Copyright (C) 2025 Nicolas Chatelain (nicocha30)
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package utils

import "testing"

func TestParseSSHURL(t *testing.T) {
	parsed, err := ParseLigoloURL("ssh://proxy.example:22")
	if err != nil {
		t.Fatalf("ParseLigoloURL() error = %v", err)
	}
	if !parsed.IsSSH() {
		t.Fatal("ssh URL was not recognized as SSH")
	}
	if !parsed.IsValid() {
		t.Fatal("ssh URL was not recognized as valid")
	}
	if parsed.Host != "proxy.example:22" {
		t.Fatalf("Host = %q, want %q", parsed.Host, "proxy.example:22")
	}
	if parsed.Hostname() != "proxy.example" {
		t.Fatalf("Hostname() = %q, want %q", parsed.Hostname(), "proxy.example")
	}
}
