package dbs

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strings"

	"github.com/vkuznet/dbs2go/utils"
)

// FileLumis API
func (API) FileLumis(params Record, w http.ResponseWriter) (int64, error) {
	var args []interface{}
	var conds []string

	tmpl := make(Record)
	tmpl["Owner"] = DBOWNER
	tmpl["Lfn"] = false
	tmpl["LfnGenerator"] = ""
	tmpl["TokenGenerator"] = ""
	tmpl["LfnList"] = false
	tmpl["ValidFileOnly"] = false
	tmpl["BlockName"] = false
	tmpl["Migration"] = false

	lfns := getValues(params, "logical_file_name")
	if len(lfns) > 1 {
		token, binds := TokenGenerator(lfns, 100, "lfns_token") // 100 is max for # of allowed entries
		tmpl["LfnGenerator"] = token
		tmpl["Lfn"] = true
		tmpl["LfnList"] = true
		conds = append(conds, token)
		for _, v := range binds {
			args = append(args, v)
		}
	} else if len(lfns) == 1 {
		tmpl["Lfn"] = true
		tmpl["LfnList"] = false
		conds = append(conds, "F.LOGICAL_FILE_NAME = :logical_file_name")
		args = append(args, lfns[0])
	}

	validFileOnly := getValues(params, "validFileOnly")
	if len(validFileOnly) == 1 {
		tmpl["ValidFileOnly"] = true
		conds = append(conds, "F.IS_FILE_VALID = 1")
		conds = append(conds, "DT.DATASET_ACCESS_TYPE in ('VALID', 'PRODUCTION') ")
	}

	blocks := getValues(params, "block_name")
	if len(blocks) == 1 {
		tmpl["BlockName"] = true
		conds, args = AddParam("block_name", "B.BLOCK_NAME", params, conds, args)
	}

	stm, err := LoadTemplateSQL("filelumis", tmpl)
	log.Println("### stm", stm)
	if err != nil {
		return 0, err
	}

	// generate run_num token
	runs, err := ParseRuns(getValues(params, "run_num"))
	if err != nil {
		return 0, err
	}
	if len(runs) > 0 {
		token, condRuns, bindsRuns := runsClause("FL", runs)
		log.Println("### FileLumis", token, condRuns, bindsRuns)
		stm = fmt.Sprintf("%s %s", token, stm)
		conds = append(conds, condRuns)
		for _, v := range bindsRuns {
			args = append(args, v)
		}
	}

	stm = WhereClause(stm, conds)

	// fix binding variables
	for k, v := range params {
		key := fmt.Sprintf(":%s", strings.ToLower(k))
		if strings.Contains(stm, key) {
			stm = strings.Replace(stm, key, "?", -1)
			args = append(args, v)
		}
	}

	log.Println("### filelumis args", args)

	// use generic query API to fetch the results from DB
	return executeAll(w, stm, args...)
}

// FileLumis
type FileLumis struct {
	FILE_ID          int64 `json:"file_id validate:"required,number""`
	LUMI_SECTION_NUM int64 `json:"lumi_section_num" validate:"required,number"`
	RUN_NUM          int64 `json:"run_num" validate:"required,number"`
	EVENT_COUNT      int64 `json:"event_count"`
}

// Insert implementation of FileLumis
func (r *FileLumis) Insert(tx *sql.Tx) error {
	var err error
	err = r.Validate()
	if err != nil {
		log.Println("unable to validate record", err)
		return err
	}
	// get SQL statement from static area
	var stm string
	if r.EVENT_COUNT != 0 {
		stm = getSQL("insert_filelumis")
		_, err = tx.Exec(stm, r.RUN_NUM, r.LUMI_SECTION_NUM, r.FILE_ID, r.EVENT_COUNT)
	} else {
		stm = getSQL("insert_filelumis2")
		_, err = tx.Exec(stm, r.RUN_NUM, r.LUMI_SECTION_NUM, r.FILE_ID)
	}
	if utils.VERBOSE > 0 {
		log.Printf("Insert FileLumis\n%s\n%+v", stm, r)
	}
	return err
}

// Validate implementation of FileLumis
func (r *FileLumis) Validate() error {
	if err := RecordValidator.Struct(*r); err != nil {
		return DecodeValidatorError(r, err)
	}
	return nil
}

// SetDefaults implements set defaults for FileLumis
func (r *FileLumis) SetDefaults() {
}

// Decode implementation for FileLumis
func (r *FileLumis) Decode(reader io.Reader) error {
	// init record with given data record
	data, err := ioutil.ReadAll(reader)
	if err != nil {
		log.Println("fail to read data", err)
		return err
	}
	err = json.Unmarshal(data, &r)

	//     decoder := json.NewDecoder(r)
	//     err := decoder.Decode(&rec)
	if err != nil {
		log.Println("fail to decode data", err)
		return err
	}
	return nil
}

// InsertFileLumis DBS API
func (API) InsertFileLumisTx(tx *sql.Tx, r io.Reader, cby string) error {
	// read given input
	data, err := ioutil.ReadAll(r)
	if err != nil {
		log.Println("fail to read data", err)
		return err
	}
	rec := FileLumis{}
	err = json.Unmarshal(data, &rec)
	if err != nil {
		log.Println("fail to decode data", err)
		return err
	}
	err = rec.Insert(tx)
	if err != nil {
		log.Printf("unable to insert %+v, %v", rec, err)
		return err
	}
	return err
}
