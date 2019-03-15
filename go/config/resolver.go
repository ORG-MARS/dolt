// Copyright 2016 Attic Labs, Inc. All rights reserved.
// Licensed under the Apache License, version 2.0:
// http://www.apache.org/licenses/LICENSE-2.0

package config

import (
	"fmt"
	"strings"

	"github.com/attic-labs/noms/go/chunks"
	"github.com/attic-labs/noms/go/datas"
	"github.com/attic-labs/noms/go/spec"
	"github.com/attic-labs/noms/go/types"
	"github.com/attic-labs/noms/go/util/verbose"
)

type Resolver struct {
	config      *Config
	dotDatapath string // set to the first datapath that was resolved
}

// A Resolver enables using db defaults, db aliases and dataset '.' replacement in command
// line arguments when a .nomsconfig file is present. To use it, create a config resolver
// before command line processing and use it to resolve each dataspec argument in
// succession.
func NewResolver() *Resolver {
	c, err := FindNomsConfig()
	if err != nil {
		if err != NoConfig {
			panic(fmt.Errorf("Failed to read .nomsconfig due to: %v", err))
		}
		return &Resolver{}
	}
	return &Resolver{c, ""}
}

// Print replacement if one occurred
func (r *Resolver) verbose(orig string, replacement string) string {
	if orig != replacement {
		if orig == "" {
			orig = `""`
		}
		verbose.Log("\tresolving %s -> %s\n", orig, replacement)
	}
	return replacement
}

// Resolve string to database name. And get the DbConfig if one exists.
// If config is defined:
//   - replace the empty string with the default db url
//   - replace any db alias with it's url
func (r *Resolver) DbConfigForDbSpec(str string) *DbConfig {
	if r.config != nil {
		if str == "" {
			dbc := r.config.Db[DefaultDbAlias]
			return &dbc
		}
		if val, ok := r.config.Db[str]; ok {
			return &val
		}
	}
	return &DbConfig{Url: str}
}

// See config.DbConfigForDbSpec which will retrieve the entire DbConfig
// object associated with the db (if one exists).  This method retrieves
// the Url from that DbConfig.
func (r *Resolver) ResolveDbSpec(str string) string {
	return r.DbConfigForDbSpec(str).Url
}

// Resolve string to dataset or path name and get the appropriate
// DbConfig if one exists.
//   - replace database name as described in ResolveDatabase
//   - if this is the first call to ResolvePath, remember the
//     datapath part for subsequent calls.
//   - if this is not the first call and a "." is used, replace
//     it with the first datapath.
func (r *Resolver) ResolvePathSpecAndGetDbConfig(str string) (string, *DbConfig) {
	iOfSep := strings.Index(str, spec.Separator)
	if r.config != nil && iOfSep > -1 {
		split := strings.SplitN(str, spec.Separator, 2)
		db, rest := "", split[0]
		if len(split) > 1 {
			db, rest = split[0], split[1]
		}

		dbc := r.DbConfigForDbSpec(db)
		if dbc.Url != "" {
			if r.dotDatapath == "" {
				r.dotDatapath = rest
			} else if rest == "." {
				rest = r.dotDatapath
			}

			return dbc.Url + spec.Separator + rest, dbc
		}
	}

	return str, &DbConfig{Url: str}
}

// See ResolvePathSpecAndGetDbConfig which does both path spec resolutioon
// and config retrieval
func (r *Resolver) ResolvePathSpec(str string) string {
	str, _ = r.ResolvePathSpecAndGetDbConfig(str)

	return str
}

// Resolve string to database spec. If a config is present,
//   - resolve a db alias to its db spec
//   - resolve "" to the default db spec
func (r *Resolver) GetDatabase(str string) (datas.Database, error) {
	dbc := r.DbConfigForDbSpec(str)
	sp, err := spec.ForDatabaseOpts(r.verbose(str, dbc.Url), specOptsForConfig(r.config, dbc))
	if err != nil {
		return nil, err
	}
	return sp.GetDatabase(), nil
}

// Resolve string to a chunkstore. Like ResolveDatabase, but returns the underlying ChunkStore
func (r *Resolver) GetChunkStore(str string) (chunks.ChunkStore, error) {
	dbc := r.DbConfigForDbSpec(str)
	sp, err := spec.ForDatabaseOpts(r.verbose(str, dbc.Url), specOptsForConfig(r.config, dbc))
	if err != nil {
		return nil, err
	}
	return sp.NewChunkStore(), nil
}

// Resolve string to a dataset. If a config is present,
//  - if no db prefix is present, assume the default db
//  - if the db prefix is an alias, replace it
func (r *Resolver) GetDataset(str string) (datas.Database, datas.Dataset, error) {
	specStr, dbc := r.ResolvePathSpecAndGetDbConfig(str)
	sp, err := spec.ForDatasetOpts(r.verbose(str, specStr), specOptsForConfig(r.config, dbc))
	if err != nil {
		return nil, datas.Dataset{}, err
	}
	return sp.GetDatabase(), sp.GetDataset(), nil
}

// Resolve string to a value path. If a config is present,
//  - if no db spec is present, assume the default db
//  - if the db spec is an alias, replace it
func (r *Resolver) GetPath(str string) (datas.Database, types.Value, error) {
	specStr, dbc := r.ResolvePathSpecAndGetDbConfig(str)
	sp, err := spec.ForPathOpts(r.verbose(str, specStr), specOptsForConfig(r.config, dbc))
	if err != nil {
		return nil, nil, err
	}
	return sp.GetDatabase(), sp.GetValue(), nil
}
