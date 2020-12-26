package dbs

import (
	"errors"
	"fmt"
	"net/http"
)

// BlockChildren DBS API
func (API) BlockChildren(params Record, w http.ResponseWriter) (int64, error) {
	// variables we'll use in where clause
	var args []interface{}
	where := "WHERE "

	// parse dataset argument
	blockchildren := getValues(params, "block_name")
	if len(blockchildren) > 1 {
		msg := "Unsupported list of blockchildren"
		return 0, errors.New(msg)
	} else if len(blockchildren) == 1 {
		op, val := OperatorValue(blockchildren[0])
		cond := fmt.Sprintf(" BP.BLOCK_NAME %s %s", op, placeholder("block_name"))
		where += addCond(where, cond)
		args = append(args, val)
	}
	// get SQL statement from static area
	stm := getSQL("blockchildren")
	// use generic query API to fetch the results from DB
	return executeAll(w, stm+where, args...)
}

// InsertBlockChildren DBS API
func (API) InsertBlockChildren(values Record) error {
	return InsertData("insert_block_children", values)
}
