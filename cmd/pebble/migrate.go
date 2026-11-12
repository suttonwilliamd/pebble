package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/suttonwilliamd/pebble/internal/repository"
)

const migrateUsage = "usage: pebble migrate-v1 <source-root> <new-destination> [--branch <name>]... [--head <name>]"

func handleMigration(params []string, cwd string, w io.Writer) error {
	if len(params) < 2 {
		return fmt.Errorf("%s", migrateUsage)
	}
	var options repository.MigrationOptions
	for i := 2; i < len(params); i += 2 {
		if i+1 == len(params) {
			return fmt.Errorf("%s", migrateUsage)
		}
		switch params[i] {
		case "--branch":
			options.Branches = append(options.Branches, params[i+1])
		case "--head":
			if options.Head != "" {
				return fmt.Errorf("--head may be supplied only once")
			}
			options.Head = params[i+1]
		default:
			return fmt.Errorf("%s", migrateUsage)
		}
	}
	source, destination := params[0], params[1]
	if !filepath.IsAbs(source) {
		source = filepath.Join(cwd, source)
	}
	if !filepath.IsAbs(destination) {
		destination = filepath.Join(cwd, destination)
	}
	result, err := repository.MigrateV1(context.Background(), source, destination, options)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Migrated and verified %d branches and %d v1 object mappings into %s\nHEAD: %s\nMapping receipt: %s\n", len(result.Branches), len(result.Objects), destination, result.Head, filepath.Join(destination, ".pebble", "migration-v1.json"))
	return err
}
