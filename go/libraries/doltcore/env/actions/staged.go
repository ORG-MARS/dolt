// Copyright 2019 Dolthub, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package actions

import (
	"context"
	"errors"
	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/dolt/go/libraries/doltcore/env"
)

var ErrTablesInConflict = errors.New("table is in conflict")

func StageTables(ctx context.Context, dEnv *env.DoltEnv, names []string) error {
	tables, docs := splitTablesAndDocs(names)

	staged, working, err := getStagedAndWorking(ctx, dEnv)
	if err != nil {
		return err
	}

	staged, err = stageTables(ctx, working, staged, tables...)
	if err != nil {
		return err
	}

	staged, err = stageDocs(ctx, working, staged, docs...)
	if err != nil {
		return err
	}

	return saveRepoState(ctx, dEnv, working, staged)
}

func splitTablesAndDocs(names []string) (tbls, docs []string) {
	for _, nm := range names {
		if nm == doltdb.DocTableName {
			continue
		}
		if doltdb.DocSet.Contains(nm) {
			docs = append(docs, nm)
		} else {
			tbls = append(tbls, nm)
		}
	}
	return
}


// GetTblsAndDocDetails takes a slice of strings where valid doc names are replaced with doc table name. Doc names are
// appended to a docDetails slice. We return a tuple of tables, docDetails and error.
func GetTblsAndDocDetails(dEnv *env.DoltEnv, tbls []string) (tables []string, docDetails []doltdb.DocDetails, err error) {
	for i, tbl := range tbls {
		docDetail, err := dEnv.GetOneDocDetail(tbl)
		if err != nil {
			return nil, nil, err
		}
		if docDetail.DocPk != "" {
			docDetails = append(docDetails, docDetail)
			tbls[i] = doltdb.DocTableName
		}
	}
	return tbls, docDetails, nil
}

func StageAllTables(ctx context.Context, dEnv *env.DoltEnv) error {
	staged, working, err := getStagedAndWorking(ctx, dEnv)
	if err != nil {
		return err
	}

	tbls, err := doltdb.UnionTableNames(ctx, working, staged)
	if err != nil {
		return err
	}

	staged, err = stageTables(ctx, working, staged, tbls...)
	if err != nil {
		return err
	}

	docs, err := doltdb.UnionDocNames(ctx, working, staged)
	if err != nil {
		return err
	}

	staged, err = stageDocs(ctx, working, staged, docs...)
	if err != nil {
		return err
	}

	return saveRepoState(ctx, dEnv, working, staged)
}

func stageTables(ctx context.Context, working, staged *doltdb.RootValue, tbls ...string) (*doltdb.RootValue, error) {
	err := ValidateTables(ctx, tbls, staged, working)
	if err != nil {
		return nil, err
	}

	working, err = checkTablesForConflicts(ctx, tbls, working)
	if err != nil {
		return nil, err
	}

	return MoveTablesBetweenRoots(ctx, tbls, working, staged)
}

func stageDocs(ctx context.Context, working, staged *doltdb.RootValue, docs ...string) (*doltdb.RootValue, error) {
	return MoveDocsBetweenRoots(ctx, docs, working, staged)
}

func saveRepoState(ctx context.Context, dEnv *env.DoltEnv, working, staged *doltdb.RootValue) error {
	if wh, err := dEnv.DoltDB.WriteRootValue(ctx, working); err == nil {
		if sh, err := dEnv.DoltDB.WriteRootValue(ctx, staged); err == nil {
			dEnv.RepoState.Staged = sh.String()
			dEnv.RepoState.Working = wh.String()

			if err = dEnv.RepoState.Save(dEnv.FS); err != nil {
				return env.ErrStateUpdate
			}

			return nil
		}
	}

	return doltdb.ErrNomsIO
}

func checkTablesForConflicts(ctx context.Context, tbls []string, working *doltdb.RootValue) (*doltdb.RootValue, error) {
	var inConflict []string
	for _, tblName := range tbls {
		tbl, _, err := working.GetTable(ctx, tblName)
		if err != nil {
			return nil, err
		}

		has, err := tbl.HasConflicts()
		if err != nil {
			return nil, err
		}
		if has {
			num, err := tbl.NumRowsInConflict(ctx)
			if err != nil {
				return nil, err
			}

			if num == 0 {
				clrTbl, err := tbl.ClearConflicts()
				if err != nil {
					return nil, err
				}

				working, err = working.PutTable(ctx, tblName, clrTbl)
				if err != nil {
					return nil, err
				}
			}

			if num > 0 {
				inConflict = append(inConflict, tblName)
			}
		}
	}

	if len(inConflict) > 0 {
		return nil, NewTblInConflictError(inConflict)
	}

	return working, nil
}

// ValidateTables checks that all tables passed exist in at least one of the roots passed.
func ValidateTables(ctx context.Context, tbls []string, roots ...*doltdb.RootValue) error {
	var missing []string
	for _, tbl := range tbls {
		found := false
		for _, root := range roots {
			if has, err := root.HasTable(ctx, tbl); err != nil {
				return err
			} else if has {
				found = true
				break
			}
		}

		if !found {
			missing = append(missing, tbl)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	return NewTblNotExistError(missing)
}

func getStagedAndWorking(ctx context.Context, dEnv *env.DoltEnv) (*doltdb.RootValue, *doltdb.RootValue, error) {
	roots, err := getRoots(ctx, dEnv, StagedRoot, WorkingRoot)

	if err != nil {
		return nil, nil, err
	}

	return roots[StagedRoot], roots[WorkingRoot], nil
}

func getWorkingAndHead(ctx context.Context, dEnv *env.DoltEnv) (*doltdb.RootValue, *doltdb.RootValue, error) {
	roots, err := getRoots(ctx, dEnv, WorkingRoot, HeadRoot)

	if err != nil {
		return nil, nil, err
	}

	return roots[WorkingRoot], roots[HeadRoot], nil
}

func getRoots(ctx context.Context, dEnv *env.DoltEnv, rootTypes ...RootType) (map[RootType]*doltdb.RootValue, error) {
	roots := make(map[RootType]*doltdb.RootValue)
	for _, rt := range rootTypes {
		var err error
		var root *doltdb.RootValue
		switch rt {
		case StagedRoot:
			root, err = dEnv.StagedRoot(ctx)
		case WorkingRoot:
			root, err = dEnv.WorkingRoot(ctx)
		case HeadRoot:
			root, err = dEnv.HeadRoot(ctx)
		default:
			panic("Method does not support this root type.")
		}

		if err != nil {
			return nil, RootValueUnreadable{rt, err}
		}

		roots[rt] = root
	}

	return roots, nil
}
