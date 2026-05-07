// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import "strings"

// parsedArgs holds the result of splitting raw CLI arguments into positionals
// (command, subcommand) and subcommand args (everything else).
type parsedArgs struct {
	// Positionals contains the non-flag arguments up to positionalLimit (command, subcommand).
	Positionals []string
	// SubArgs contains everything after both positionals, forwarded as-is to
	// the subcommand's own FlagSet.
	SubArgs []string
}

// splitArgs separates up to positionalLimit positional arguments from the rest.
// Non-flag args (no leading "-") are collected into Positionals until the limit
// is reached; everything else goes into SubArgs for the subcommand to parse.
func splitArgs(args []string, positionalLimit int) parsedArgs {
	var result parsedArgs
	for _, arg := range args {
		if len(result.Positionals) >= positionalLimit {
			result.SubArgs = append(result.SubArgs, arg)
			continue
		}
		if strings.HasPrefix(arg, "-") {
			result.SubArgs = append(result.SubArgs, arg)
			continue
		}
		result.Positionals = append(result.Positionals, arg)
	}
	return result
}

// hasHelpFlag reports whether args contains "--help" or "-h".
func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}
