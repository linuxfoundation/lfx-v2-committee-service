// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package main

import "strings"

// parsedArgs holds the result of splitting raw CLI arguments into positionals
// (command, subcommand) and the remaining flag arguments passed to flag.Parse.
type parsedArgs struct {
	// Positionals contains the non-flag arguments up to positionalLimit (command, subcommand).
	Positionals []string
	// FlagArgs contains all flag-like arguments (starting with "-") plus any
	// arguments that appear after both positionals have been collected.
	FlagArgs []string
}

// splitArgs separates up to positionalLimit positional arguments from flag
// arguments. Flags (any arg starting with "-") are always collected into
// FlagArgs regardless of their position. Once positionalLimit positionals have
// been collected, all remaining arguments are treated as flag arguments so they
// can be forwarded to a subcommand's FlagSet.
func splitArgs(args []string, positionalLimit int) parsedArgs {
	var result parsedArgs
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") || len(result.Positionals) >= positionalLimit {
			result.FlagArgs = append(result.FlagArgs, arg)
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
