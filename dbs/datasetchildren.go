package dbs

import (
	"errors"
	"fmt"
	"net/http"
)

// DatasetChildren API
func (API) DatasetChildren(params Record, w http.ResponseWriter) (int64, error) {
	var args []interface{}
	var conds []string

	// parse dataset argument
	datasetchildren := getValues(params, "dataset")
	if len(datasetchildren) > 1 {
		msg := "The datasetchildren API does not support list of datasetchildren"
		return 0, errors.New(msg)
	} else if len(datasetchildren) == 1 {
		op, val := OperatorValue(datasetchildren[0])
		cond := fmt.Sprintf(" D.DATASET %s %s", op, placeholder("dataset"))
		conds = append(conds, cond)
		args = append(args, val)
	} else {
		msg := fmt.Sprintf("No arguments for datasetchildren API")
		return 0, errors.New(msg)
	}

	// get SQL statement from static area
	stm := getSQL("datasetchildren")
	stm += WhereClause(conds)

	// use generic query API to fetch the results from DB
	return executeAll(w, stm, args...)
}

// InsertDatasetChildren DBS API
func (API) InsertDatasetChildren(values Record) error {
	return InsertValues("insert_dataset_children", values)
}
