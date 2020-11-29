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

package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/dolthub/dolt/go/libraries/utils/filesys"

	"github.com/dolthub/dolt/go/cmd/dolt/cli"
	"github.com/dolthub/dolt/go/cmd/dolt/errhand"
	"github.com/dolthub/dolt/go/libraries/doltcore/diff"
	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/dolt/go/libraries/doltcore/env"
	"github.com/dolthub/dolt/go/libraries/doltcore/env/actions"
	"github.com/dolthub/dolt/go/libraries/utils/argparser"
)

const (
	SoftResetParam = "soft"
	HardResetParam = "hard"
)

var resetDocContent = cli.CommandDocumentationContent{
	ShortDesc: "Resets staged tables to their HEAD state",
	LongDesc: `Sets the state of a table in the staging area to be that table's value at HEAD

{{.EmphasisLeft}}dolt reset <tables>...{{.EmphasisRight}}"
	This form resets the values for all staged {{.LessThan}}tables{{.GreaterThan}} to their values at {{.EmphasisLeft}}HEAD{{.EmphasisRight}}. (It does not affect the working tree or
	the current branch.)

	This means that {{.EmphasisLeft}}dolt reset <tables>{{.EmphasisRight}} is the opposite of {{.EmphasisLeft}}dolt add <tables>{{.EmphasisRight}}.

	After running {{.EmphasisLeft}}dolt reset <tables>{{.EmphasisRight}} to update the staged tables, you can use {{.EmphasisLeft}}dolt checkout{{.EmphasisRight}} to check the
	contents out of the staged tables to the working tables.

dolt reset .
	This form resets {{.EmphasisLeft}}all{{.EmphasisRight}} staged tables to their values at HEAD. It is the opposite of {{.EmphasisLeft}}dolt add .{{.EmphasisRight}}`,

	Synopsis: []string{
		"{{.LessThan}}tables{{.GreaterThan}}...",
		"[--hard | --soft]",
	},
}

type ResetCmd struct{}

// Name is returns the name of the Dolt cli command. This is what is used on the command line to invoke the command
func (cmd ResetCmd) Name() string {
	return "reset"
}

// Description returns a description of the command
func (cmd ResetCmd) Description() string {
	return "Remove table changes from the list of staged table changes."
}

// CreateMarkdown creates a markdown file containing the helptext for the command at the given path
func (cmd ResetCmd) CreateMarkdown(fs filesys.Filesys, path, commandStr string) error {
	ap := cmd.createArgParser()
	return CreateMarkdown(fs, path, cli.GetCommandDocumentation(commandStr, resetDocContent, ap))
}

func (cmd ResetCmd) createArgParser() *argparser.ArgParser {
	ap := argparser.NewArgParser()
	ap.SupportsFlag(HardResetParam, "", "Resets the working tables and staged tables. Any changes to tracked tables in the working tree since {{.LessThan}}commit{{.GreaterThan}} are discarded.")
	ap.SupportsFlag(SoftResetParam, "", "Does not touch the working tables, but removes all tables staged to be committed.")
	return ap
}

// Exec executes the command
func (cmd ResetCmd) Exec(ctx context.Context, commandStr string, args []string, dEnv *env.DoltEnv) int {
	ap := cmd.createArgParser()
	help, usage := cli.HelpAndUsagePrinters(cli.GetCommandDocumentation(commandStr, resetDocContent, ap))
	apr := cli.ParseArgs(ap, args, help)

	if apr.ContainsArg(doltdb.DocTableName) {
		return HandleDocTableVErrAndExitCode()
	}

	var verr errhand.VerboseError
	if apr.ContainsAll(HardResetParam, SoftResetParam) {
		verr = errhand.BuildDError("error: --%s and --%s are mutually exclusive options.", HardResetParam, SoftResetParam).Build()
	} else if apr.Contains(HardResetParam) {
		verr = resetHard(ctx, dEnv, apr)
	} else {
		verr = resetSoft(ctx, dEnv, apr)
	}

	return HandleVErrAndExitCode(verr, usage)
}

func resetHard(ctx context.Context, dEnv *env.DoltEnv, apr *argparser.ArgParseResults) errhand.VerboseError {
	if apr.NArg() > 1 {
		return errhand.BuildDError("--%s supports at most one additional param", HardResetParam).SetPrintUsage().Build()
	}

	err := func() error {
		headRoot, err := dEnv.HeadRoot(ctx)
		if err != nil {
			return err
		}

		if apr.NArg() == 1 {
			cs, err := doltdb.NewCommitSpec(apr.Arg(0))
			if err != nil {
				return err
			}

			newHead, err := dEnv.DoltDB.Resolve(ctx, cs, dEnv.RepoState.CWBHeadRef())
			if err != nil {
				return err
			}

			err = dEnv.DoltDB.SetHeadToCommit(ctx, dEnv.RepoState.CWBHeadRef(), newHead)
			if err != nil {
				return err
			}

			headRoot, err = newHead.GetRootValue()
			if err != nil {
				return err
			}
		}

		return actions.ResetHard(ctx, dEnv, headRoot)
	}()
	if err != nil {
		return errhand.VerboseErrorFromError(err)
	}

	return nil
}

// RemoveDocsTbl takes a slice of table names and returns a new slice with DocTableName removed.
func RemoveDocsTbl(tbls []string) []string {
	var result []string
	for _, tblName := range tbls {
		if tblName != doltdb.DocTableName {
			result = append(result, tblName)
		}
	}
	return result
}

func resetSoft(ctx context.Context, dEnv *env.DoltEnv, apr *argparser.ArgParseResults) errhand.VerboseError {
	err := func() error {
		tables := apr.Args()

		if len(tables) == 0 || (len(tables) == 1 && tables[0] == ".") {
			stagedRoot, err := dEnv.StagedRoot(ctx)
			if err != nil {
				return err
			}

			headRoot, err := dEnv.HeadRoot(ctx)
			if err != nil {
				return err
			}

			tables, err = doltdb.UnionTableNames(ctx, stagedRoot, headRoot)
			if err != nil {
				return err
			}
		}

		return actions.ResetSoft(ctx, dEnv, tables)
	}()
	if err != nil {
		return errhand.VerboseErrorFromError(err)
	}

	printNotStaged(ctx, dEnv)
	return nil
}

func printNotStaged(ctx context.Context, dEnv *env.DoltEnv) {
	// Printing here is best effort.  Fail silently
	working, err := dEnv.WorkingRoot(ctx)
	if err != nil {
		return
	}

	staged, err := dEnv.StagedRoot(ctx)
	if err != nil {
		return
	}

	notStagedTbls, err := diff.GetTableDeltas(ctx, staged, working)
	if err != nil {
		return
	}

	notStagedDocs, err := diff.NewDocDiffs(ctx, working, nil, nil)
	if err != nil {
		return
	}

	removeModified := 0
	for _, td := range notStagedTbls {
		if !td.IsAdd() {
			removeModified++
		}
	}

	if removeModified+notStagedDocs.NumRemoved+notStagedDocs.NumModified > 0 {
		cli.Println("Unstaged changes after reset:")

		var lines []string
		for _, td := range notStagedTbls {
			if td.IsAdd() {
				//  per Git, unstaged new tables are untracked
				continue
			} else if td.IsDrop() {
				lines = append(lines, fmt.Sprintf("%s\t%s", tblDiffTypeToShortLabel[diff.RemovedTable], td.CurName()))
			} else if td.IsRename() {
				// per Git, unstaged renames are shown as drop + add
				lines = append(lines, fmt.Sprintf("%s\t%s", tblDiffTypeToShortLabel[diff.RemovedTable], td.FromName))
			} else {
				lines = append(lines, fmt.Sprintf("%s\t%s", tblDiffTypeToShortLabel[diff.ModifiedTable], td.CurName()))
			}
		}

		for _, docName := range notStagedDocs.Docs {
			ddt := notStagedDocs.DocToType[docName]
			if ddt != diff.AddedDoc {
				lines = append(lines, fmt.Sprintf("%s\t%s", docDiffTypeToShortLabel[ddt], docName))
			}
		}

		cli.Println(strings.Join(lines, "\n"))
	}
}

