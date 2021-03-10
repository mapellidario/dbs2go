package dbs

import (
	"database/sql"
	"encoding/json"
	"io"
	"io/ioutil"
	"log"
	"net/http"
)

// PrimaryDSTypes DBS API
func (API) PrimaryDSTypes(params Record, w http.ResponseWriter) (int64, error) {
	var args []interface{}
	var conds []string

	conds, args = AddParam("primary_ds_type", "PDT.PRIMARY_DS_TYPE", params, conds, args)

	// get SQL statement from static area
	stm := getSQL("primarydstypes")
	stm = WhereClause(stm, conds)

	// use generic query API to fetch the results from DB
	return executeAll(w, stm, args...)
}

// PrimaryDSTypes
type PrimaryDSTypes struct {
	PRIMARY_DS_TYPE_ID int64  `json:"primary_ds_type_id"`
	PRIMARY_DS_TYPE    string `json:"primary_ds_type" validate:"required"`
}

// Insert implementation of PrimaryDSTypes
func (r *PrimaryDSTypes) Insert(tx *sql.Tx) error {
	if r.PRIMARY_DS_TYPE_ID == 0 {
		pid, err := LastInsertId(tx, "PRIMARY_DS_TYPES", "primary_ds_type_id")
		if err != nil {
			return err
		}
		r.PRIMARY_DS_TYPE_ID = pid + 1
	}
	// get SQL statement from static area
	stm := getSQL("insert_primary_ds_types")
	if DBOWNER == "sqlite" {
		stm = getSQL("insert_primary_ds_types_sqlite")
	}
	_, err := tx.Exec(stm, r.PRIMARY_DS_TYPE_ID, r.PRIMARY_DS_TYPE)
	return err
}

// Validate implementation of PrimaryDSTypes
func (r *PrimaryDSTypes) Validate() error {
	if err := RecordValidator.Struct(*r); err != nil {
		return DecodeValidatorError(r, err)
	}
	return nil
}

// Decode implementation for PrimaryDSTypes
func (r *PrimaryDSTypes) Decode(reader io.Reader) (int64, error) {
	// init record with given data record
	data, err := ioutil.ReadAll(reader)
	if err != nil {
		log.Println("fail to read data", err)
		return 0, err
	}
	err = json.Unmarshal(data, &r)

	//     decoder := json.NewDecoder(r)
	//     err := decoder.Decode(&rec)
	if err != nil {
		log.Println("fail to decode data", err)
		return 0, err
	}
	size := int64(len(data))
	return size, nil
}

// InsertPrimaryDSTypes DBS API
func (API) InsertPrimaryDSTypes(r io.Reader) (int64, error) {
	return insertRecord(&PrimaryDSTypes{}, r)
}
