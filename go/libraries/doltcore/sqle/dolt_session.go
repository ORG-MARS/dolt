// Copyright 2020 Dolthub, Inc.
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

package sqle

import (
	"context"
	"fmt"

	"github.com/dolthub/go-mysql-server/sql"

	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/dolt/go/libraries/doltcore/env"
	"github.com/dolthub/dolt/go/libraries/doltcore/table/editor"
	"github.com/dolthub/dolt/go/store/hash"
)

type dbRoot struct {
	hashStr string
	root    *doltdb.RootValue
}

var _ sql.Session = &DoltSession{}

// DoltSession is the sql.Session implementation used by dolt.  It is accessible through a *sql.Context instance
type DoltSession struct {
	sql.Session
	dbRoots   map[string]dbRoot
	dbDatas   map[string]env.DbData
	dbEditors map[string]*editor.TableEditSession

	Username string
	Email    string
}

// DefaultDoltSession creates a DoltSession object with default values
func DefaultDoltSession() *DoltSession {
	sess := &DoltSession{
		Session:   sql.NewBaseSession(),
		dbRoots:   make(map[string]dbRoot),
		dbDatas:   make(map[string]env.DbData),
		dbEditors: make(map[string]*editor.TableEditSession),
		Username:  "",
		Email:     "",
	}
	return sess
}

// NewDoltSession creates a DoltSession object from a standard sql.Session and 0 or more Database objects.
func NewDoltSession(ctx context.Context, sqlSess sql.Session, username, email string, dbs ...Database) (*DoltSession, error) {
	dbRoots := make(map[string]dbRoot)
	dbDatas := make(map[string]env.DbData)
	dbEditors := make(map[string]*editor.TableEditSession)
	for _, db := range dbs {
		dbDatas[db.Name()] = env.DbData{Rsw: db.rsw, Ddb: db.ddb, Rsr: db.rsr, Drw: db.drw}
		dbEditors[db.Name()] = editor.CreateTableEditSession(nil, editor.TableEditSessionProps{})
	}

	sess := &DoltSession{sqlSess, dbRoots, dbDatas, dbEditors, username, email}
	for _, db := range dbs {
		err := sess.AddDB(ctx, db)

		if err != nil {
			return nil, err
		}
	}

	return sess, nil
}

// DSessFromSess retrieves a dolt session from a standard sql.Session
func DSessFromSess(sess sql.Session) *DoltSession {
	return sess.(*DoltSession)
}

func (sess *DoltSession) CommitTransaction(ctx *sql.Context) error {
	currentDb := sess.GetCurrentDatabase()
	if currentDb == "" {
		return sql.ErrNoDatabaseSelected.New()
	}

	dbRoot, ok := sess.dbRoots[currentDb]
	if !ok {
		return sql.ErrDatabaseNotFound.New(currentDb)
	}

	dbData := sess.dbDatas[currentDb]

	root := dbRoot.root
	h, err := dbData.Ddb.WriteRootValue(ctx, root)
	if err != nil {
		return err
	}

	return dbData.Rsw.SetWorkingHash(ctx, h)
}

// GetDoltDB returns the *DoltDB for a given database by name
func (sess *DoltSession) GetDoltDB(dbName string) (*doltdb.DoltDB, bool) {
	d, ok := sess.dbDatas[dbName]

	if !ok {
		return nil, false
	}

	return d.Ddb, true
}

func (sess *DoltSession) GetDoltDBRepoStateWriter(dbName string) (env.RepoStateWriter, bool) {
	d, ok := sess.dbDatas[dbName]

	if !ok {
		return nil, false
	}

	return d.Rsw, true
}

func (sess *DoltSession) GetDoltDBRepoStateReader(dbName string) (env.RepoStateReader, bool) {
	d, ok := sess.dbDatas[dbName]

	if !ok {
		return nil, false
	}

	return d.Rsr, true
}

func (sess *DoltSession) GetDoltDBDocsReadWriter(dbName string) (env.DocsReadWriter, bool) {
	d, ok := sess.dbDatas[dbName]

	if !ok {
		return nil, false
	}

	return d.Drw, true
}

func (sess *DoltSession) GetDbData(dbName string) (env.DbData, bool) {
	ddb, ok := sess.GetDoltDB(dbName)

	if !ok {
		return env.DbData{}, false
	}

	rsr, ok := sess.GetDoltDBRepoStateReader(dbName)

	if !ok {
		return env.DbData{}, false
	}

	rsw, ok := sess.GetDoltDBRepoStateWriter(dbName)

	if !ok {
		return env.DbData{}, false
	}

	drw, ok := sess.GetDoltDBDocsReadWriter(dbName)

	if !ok {
		return env.DbData{}, false
	}

	return env.DbData{
		Ddb: ddb,
		Rsr: rsr,
		Rsw: rsw,
		Drw: drw,
	}, true
}

// GetRoot returns the current *RootValue for a given database associated with the session
func (sess *DoltSession) GetRoot(dbName string) (*doltdb.RootValue, bool) {
	dbRoot, ok := sess.dbRoots[dbName]

	if !ok {
		return nil, false
	}

	return dbRoot.root, true
}

// GetParentCommit returns the parent commit of the current session.
func (sess *DoltSession) GetParentCommit(ctx context.Context, dbName string) (*doltdb.Commit, hash.Hash, error) {
	dbd, dbFound := sess.dbDatas[dbName]

	if !dbFound {
		return nil, hash.Hash{}, sql.ErrDatabaseNotFound.New(dbName)
	}

	_, value := sess.Session.Get(dbName + HeadKeySuffix)
	valStr, isStr := value.(string)

	if !isStr || !hash.IsValid(valStr) {
		return nil, hash.Hash{}, doltdb.ErrInvalidHash
	}

	h := hash.Parse(valStr)
	cs, err := doltdb.NewCommitSpec(valStr)

	if err != nil {
		return nil, hash.Hash{}, err
	}

	cm, err := dbd.Ddb.Resolve(ctx, cs, nil)

	if err != nil {
		return nil, hash.Hash{}, err
	}

	return cm, h, nil
}

func (sess *DoltSession) Set(ctx context.Context, key string, typ sql.Type, value interface{}) error {
	if isHead, dbName := IsHeadKey(key); isHead {
		dbd, dbFound := sess.dbDatas[dbName]

		if !dbFound {
			return sql.ErrDatabaseNotFound.New(dbName)
		}

		valStr, isStr := value.(string)

		if !isStr || !hash.IsValid(valStr) {
			return doltdb.ErrInvalidHash
		}

		cs, err := doltdb.NewCommitSpec(valStr)

		if err != nil {
			return err
		}

		cm, err := dbd.Ddb.Resolve(ctx, cs, nil)

		if err != nil {
			return err
		}

		root, err := cm.GetRootValue()

		if err != nil {
			return err
		}

		h, err := root.HashOf()

		if err != nil {
			return err
		}

		err = sess.Session.Set(ctx, key, typ, value)

		if err != nil {
			return err
		}

		hashStr := h.String()
		err = sess.Session.Set(ctx, dbName+WorkingKeySuffix, sql.Text, hashStr)

		if err != nil {
			return err
		}

		sess.dbRoots[dbName] = dbRoot{hashStr, root}

		err = sess.dbEditors[dbName].SetRoot(ctx, root)
		if err != nil {
			return err
		}

		return nil
	}

	if key == "foreign_key_checks" {
		convertedVal, err := sql.Int64.Convert(value)
		if err != nil {
			return err
		}
		intVal := convertedVal.(int64)
		if intVal == 0 {
			for _, tableEditSession := range sess.dbEditors {
				tableEditSession.Props.ForeignKeyChecksDisabled = true
			}
		} else if intVal == 1 {
			for _, tableEditSession := range sess.dbEditors {
				tableEditSession.Props.ForeignKeyChecksDisabled = false
			}
		} else {
			return fmt.Errorf("variable 'foreign_key_checks' can't be set to the value of '%d'", intVal)
		}
	}

	return sess.Session.Set(ctx, key, typ, value)
}

func (sess *DoltSession) AddDB(ctx context.Context, db Database) error {
	name := db.Name()
	rsr := db.GetStateReader()
	rsw := db.GetStateWriter()
	drw := db.GetDocsReadWriter()
	ddb := db.GetDoltDB()

	sess.dbDatas[db.Name()] = env.DbData{Drw: drw, Rsr: rsr, Rsw: rsw, Ddb: ddb}

	sess.dbEditors[db.Name()] = editor.CreateTableEditSession(nil, editor.TableEditSessionProps{})

	cs := rsr.CWBHeadSpec()

	cm, err := ddb.Resolve(ctx, cs, rsr.CWBHeadRef())

	if err != nil {
		return err
	}

	h, err := cm.HashOf()

	if err != nil {
		return err
	}

	return sess.Set(ctx, name+HeadKeySuffix, sql.Text, h.String())
}
