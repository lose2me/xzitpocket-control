// Command covercheck enforces a statement-coverage floor on a Go cover
// profile. Profiles produced with -coverpkg=./... repeat the same block once
// per test package, so blocks are de-duplicated by position before counting;
// summing the raw lines would double-count and understate coverage.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type block struct {
	statements int
	covered    bool
}

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: covercheck <coverage.out> [min-percent]")
		os.Exit(2)
	}
	floor := 85.0
	if len(os.Args) == 3 {
		parsed, err := strconv.ParseFloat(os.Args[2], 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid floor %q: %v\n", os.Args[2], err)
			os.Exit(2)
		}
		floor = parsed
	}
	file, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "open profile: %v\n", err)
		os.Exit(2)
	}
	defer file.Close()

	blocks := make(map[string]*block)
	files := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			fmt.Fprintf(os.Stderr, "malformed profile line: %q\n", line)
			os.Exit(2)
		}
		statements, err := strconv.Atoi(fields[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "malformed statement count: %q\n", line)
			os.Exit(2)
		}
		count, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "malformed hit count: %q\n", line)
			os.Exit(2)
		}
		position := fields[0]
		files[strings.SplitN(position, ":", 2)[0]] = true
		entry, ok := blocks[position]
		if !ok {
			entry = &block{statements: statements}
			blocks[position] = entry
		}
		if count > 0 {
			entry.covered = true
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "read profile: %v\n", err)
		os.Exit(2)
	}
	if len(blocks) == 0 || len(files) == 0 {
		fmt.Fprintln(os.Stderr, "profile contains no coverage data")
		os.Exit(2)
	}
	total, covered := 0, 0
	for _, entry := range blocks {
		total += entry.statements
		if entry.covered {
			covered += entry.statements
		}
	}
	percent := float64(covered) / float64(total) * 100
	fmt.Printf("go statement coverage: %d/%d = %.2f%% (floor %.2f%%, %d files)\n", covered, total, percent, floor, len(files))
	if percent+1e-9 < floor {
		fmt.Fprintln(os.Stderr, "go coverage is below the floor")
		os.Exit(1)
	}
}
