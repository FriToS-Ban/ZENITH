//go:build mage

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Quick runs the benchmark on the first 100K passages (~20 min on a modern laptop).
func Quick(ctx context.Context) error {
	return run(ctx, "--scale=100k")
}

// Full runs the benchmark on the first 1M passages (~2.5 hours).
func Full(ctx context.Context) error {
	return run(ctx, "--scale=1m")
}

// NoSQLite runs Quick but skips the SQLite runner (no CGo needed).
func NoSQLite(ctx context.Context) error {
	return run(ctx, "--scale=100k", "--skip=sqlite")
}

// Clean removes the downloaded dataset cache and generated result files.
func Clean(_ context.Context) error {
	for _, dir := range []string{".cache", "results"} {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("clean %s: %w", dir, err)
		}
		fmt.Printf("removed %s/\n", dir)
	}
	return nil
}

func run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "go", append([]string{"run", "./cmd/benchmark"}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
