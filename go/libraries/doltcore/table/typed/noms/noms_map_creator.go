package noms

import (
	"errors"
	"fmt"
	"github.com/attic-labs/noms/go/types"
	"github.com/liquidata-inc/ld/dolt/go/libraries/doltcore/schema"
	"github.com/liquidata-inc/ld/dolt/go/libraries/doltcore/table"
)

// NomsMapUpdater is a TableWriter that creates a new noms types.Map.  It is backed by a StreamingMap which requires
// the rows to be written in order.  If the keys being written to WriteRow are not sorted an error will be returned from
// WriteRow.  Once all rows are written Close() should be called and GetMap will then return the new map.
type NomsMapCreator struct {
	sch *schema.Schema
	vrw types.ValueReadWriter

	lastPK  types.Value
	kvsChan chan<- types.Value
	mapChan <-chan types.Map

	result *types.Map
}

// NewNomsMapCreator creates a new NomsMapCreator.
func NewNomsMapCreator(vrw types.ValueReadWriter, sch *schema.Schema) *NomsMapCreator {
	kvsChan := make(chan types.Value)
	mapChan := types.NewStreamingMap(vrw, kvsChan)

	return &NomsMapCreator{sch, vrw, nil, kvsChan, mapChan, nil}
}

// GetSchema gets the schema of the rows that this writer writes
func (nmc *NomsMapCreator) GetSchema() *schema.Schema {
	return nmc.sch
}

// WriteRow will write a row to a table.  The primary key for each row must be greater than the primary key of the row
// written before it.
func (nmc *NomsMapCreator) WriteRow(row *table.Row) error {
	if nmc.kvsChan == nil {
		panic("Attempting to write after closing.")
	}

	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic occured when writing: %v", r)
			}
		}()

		pk := table.GetPKFromRow(row)
		fieldVals := table.GetNonPKFieldListFromRow(row, nmc.vrw)
		if nmc.lastPK == nil || nmc.lastPK.Less(pk) {
			nmc.kvsChan <- pk
			nmc.kvsChan <- fieldVals
			nmc.lastPK = pk
		} else {
			err = errors.New("Input was not sorted by the primary key")
		}
	}()

	return err
}

// Close should flush all writes, release resources being held.  After this call is made no more rows may be written,
// and the value of GetMap becomes valid.
func (nmc *NomsMapCreator) Close() error {
	if nmc.result == nil {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic occured during closing: %v", r)
				}
			}()

			close(nmc.kvsChan)

			result := <-nmc.mapChan
			nmc.result = &result

			nmc.kvsChan = nil
			nmc.mapChan = nil
		}()

		return err
	} else {
		return errors.New("Already closed.")
	}
}

// GetMap retrieves the resulting types.Map once close is called
func (nmc *NomsMapCreator) GetMap() *types.Map {
	// Might want to panic if this was never closed
	return nmc.result
}