package dbs

// DBS Migrate APIS
// Copyright (c) 2021 - Valentin Kuznetsov <vkuznet@gmail.com>
//
// DBS Migration service is responsible to migrate blocks from one
// DBS to another. This module provides the following APIs:
// - Submit to submit migration request, internall it prepare the request
// and calls via goroutine process request
// - Process to process migration request explicitly
// - Remove to remove migration request
// - Status to obtain status of migration request
// Internally the migration process injects all request details into
// MigrationRequest table. The request details resides in MigrationBlocks table.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vkuznet/dbs2go/utils"
)

/*

DBS Migration APIs, see Python counterpart here:
Server/Python/src/dbs/web/DBSMigrateModel.py
Server/Python/src/dbs/business/DBSMigrate.py
and various bisuness dao, e.g.
Server/Python/src/dbs/dao/Oracle/MigrationBlock

Yhe DBS migration server is here:
Server/Python/src/dbs/components/migration/DBSMigrationServer.py

Submit should submit migration request
(see insertMigrationRequest python API)

Status checks migration request
(see listMigrationRequests python API)

Remove removes migration request
(see removeMigrationRequest API)

DBS migration status codes:
        migration_status:
        0=PENDING
        1=IN PROGRESS
        2=COMPLETED
        3=FAILED (will be retried)
        9=Terminally FAILED
        status change:
        0 -> 1
        1 -> 2
        1 -> 3
        1 -> 9
        are only allowed changes for working through migration.
        3 -> 1 is allowed for retrying and retry count +1.
*/

// MigrationCodes represents all migration codes
const (
	PENDING = iota
	IN_PROGRESS
	COMPLETED
	FAILED
	TERM_FAILED
)

// MigrateURL holds URL of DBSMigrate server
var MigrateURL string

// MigrationReport represents migration report returned by the migration API
type MigrationReport struct {
	MigrationRequestIDs []int64 `json:"migration_request_ids"`
	Report              string  `json:"report"`
	Status              string  `json:"status"`
	Error               error   `json:"error"`
}

// GetBlocks returns list of blocks for a given url and block/dataset input
func GetBlocks(rurl, val string) ([]string, error) {
	var out []string
	open := "&open_for_writing=0"
	if strings.Contains(val, "#") {
		rurl = fmt.Sprintf("%s/blocks?block_name=%s%s", rurl, url.QueryEscape(val), open)
	} else {
		rurl = fmt.Sprintf("%s/blocks?dataset=%s%s", rurl, val, open)
	}
	data, err := getData(rurl)
	fmt.Println("GetBlocks", rurl, string(data))
	if err != nil {
		log.Printf("unable to get data for %s, error %v", rurl, err)
		return out, err
	}
	var rec []Blocks
	err = json.Unmarshal(data, &rec)
	if err != nil {
		return out, err
	}
	for _, v := range rec {
		out = append(out, v.BLOCK_NAME)
	}
	return out, nil
}

// GetParents returns list of parents for given block or dataset
func GetParents(rurl, val string) ([]string, error) {
	var out []string
	if strings.Contains(val, "#") {
		rurl = fmt.Sprintf("%s/blockparents?block_name=%s", rurl, url.QueryEscape(val))
	} else {
		rurl = fmt.Sprintf("%s/datasetparents?dataset=%s", rurl, val)
	}
	data, err := getData(rurl)
	if err != nil {
		return out, err
	}
	var rec []map[string]interface{}
	err = json.Unmarshal(data, &rec)
	if err != nil {
		log.Println("unable to unmarshal data", string(data), err)
		return out, err
	}
	for _, v := range rec {
		if strings.Contains(val, "#") {
			block := fmt.Sprintf("%v", v["parent_block_name"])
			out = append(out, block)
		} else {
			dataset := fmt.Sprintf("%v", v["parent_dataset"])
			out = append(out, dataset)
		}
	}
	return out, nil
}

// helper function to prepare the ordered lists of blocks based on input BLOCK
func prepareBlockMigrationList(rurl, block string) []string {
	out := []string{}

	// check if block exists at destination (this server)
	localhost := fmt.Sprintf("%s/%s", utils.Localhost, utils.BASE)
	dstblocks, err := GetBlocks(localhost, block)
	if err != nil {
		log.Print("unable to get blocks for %s at %s", block, localhost)
		return out
	}
	if len(dstblocks) > 0 {
		log.Printf("requested blocks %v is already at destination", dstblocks)
		return out
	}
	if utils.VERBOSE > 0 {
		log.Printf("found %d destination blocks at %s", len(dstblocks), localhost)
	}

	// check if block exists at a source location
	srcblocks, err := GetBlocks(rurl, block)
	if err != nil {
		log.Printf("unable to fetch blocks for %s from %s error %v", block, rurl, err)
		return out
	}
	if utils.VERBOSE > 0 {
		log.Printf("found %d source blocks at %s", len(srcblocks), rurl)
	}
	if len(srcblocks) == 0 {
		log.Printf("requested block %s is not found at %s", block, rurl)
		return out
	}
	// we need to migrate existing block
	var blocks []string
	blocks = append(blocks, block)
	parentBlocks, err := GetParentBlocks(rurl, block)
	if err != nil {
		log.Printf("unable to find parent blocks for %s, error %v", block, err)
		return out
	}
	for _, blk := range parentBlocks {
		out = append(out, blk)
	}
	if utils.VERBOSE > 0 {
		log.Printf("prepareBlockMigrationList yields %d blocks", len(out))
	}
	return out
}

// BlockResponse represents block response structure used in GetParentBlocks
type BlockResponse struct {
	Index   int
	Dataset string
	Block   string
	Blocks  []string
	Error   error
}

// GetParentBlocks returns parent blocks for given url and block name
func GetParentBlocks(rurl, block string) ([]string, error) {
	out := []string{}
	log.Println("call GetParentBlocks with", block)
	out = append(out, block)
	// get list of blocks from the source (remote url)
	//     srcblocks, err := GetBlocks(rurl, "blockparents", block)
	srcblocks, err := GetParents(rurl, block)
	if err != nil {
		log.Println("unable to get list of blocks at remote url", rurl, err)
		return out, err
	}
	// add block parents to final list
	for _, blk := range srcblocks {
		out = append(out, blk)
	}
	// get list of parent blocks of previous parents
	parentBlocks := []string{}
	ch := make(chan BlockResponse)
	umap := make(map[int]struct{})
	for idx, blk := range srcblocks {
		umap[idx] = struct{}{}
		go func(i int, b string) {
			//             blks, err := GetBlocks(rurl, "blockparents", b)
			blks, err := GetParents(rurl, b)
			ch <- BlockResponse{Index: i, Block: b, Blocks: blks, Error: err}
		}(idx, blk)
	}
	if len(umap) == 0 {
		// no parent blocks
		log.Printf("no parent blocks found for %s in %s", block, rurl)
		return out, nil
	}
	// collect results from goroutines
	exit := false
	for {
		select {
		case r := <-ch:
			if r.Error != nil {
				log.Printf("unable to fetch blocks for url=%s block=%s error=%v", rurl, r.Block, r.Error)
			} else {
				for _, blk := range r.Blocks {
					parentBlocks = append(parentBlocks, blk)
				}
			}
			delete(umap, r.Index)
		default:
			if len(umap) == 0 {
				exit = true
			}
			time.Sleep(time.Duration(100) * time.Millisecond) // wait for response
		}
		if exit {
			break
		}
	}

	// loop over parent blocks and get its parents
	for _, blk := range parentBlocks {
		out = append(out, blk)
		results, err := GetParentBlocks(rurl, blk)
		if err != nil {
			log.Printf("fail to get url=%s block=%s error=%v", rurl, blk, err)
			continue
		}
		for _, b := range results {
			out = append(out, b)
		}
	}

	if utils.VERBOSE > 0 {
		log.Printf("GetParentBlocks output yield %d blocks", len(out))
	}
	return out, nil
}

// helper function to return lists of blocks based on input DATASET
func prepareDatasetMigrationList(rurl, dataset string) []string {
	out, err := processDatasetBlocks(rurl, dataset)
	if err != nil {
		log.Printf("unable to process dataset blocks for %s from %s, error %v", dataset, rurl, err)
		return out
	}
	if len(out) == 0 {
		log.Printf("requested dataset %s is already at destination", dataset)
		return out
	}
	pblocks, err := GetParentDatasets(rurl, dataset)
	if err != nil {
		log.Printf("unable to fetch parent daatasets from %s, error %v", rurl, err)
		return out
	}
	if utils.VERBOSE > 0 {
		log.Println("found %d parent dataset from %s", len(pblocks), rurl)
	}
	for _, blk := range pblocks {
		out = append(out, blk)
	}
	if utils.VERBOSE > 0 {
		log.Printf("prepareDatasetMigrationList yields %d blocks", len(out))
	}
	return out
}

// helper function, that comapares blocks of a dataset at source and dst
// and returns list of blocks not already at dst for migration
func processDatasetBlocks(rurl, dataset string) ([]string, error) {
	out := []string{}
	srcblks, err := GetBlocks(rurl, dataset)
	if err != nil {
		return out, err
	}
	if len(srcblks) == 0 {
		msg := fmt.Sprintf("No blocks in the required dataset %s found at source %s", dataset, rurl)
		return out, errors.New(msg)
	}
	localhost := fmt.Sprintf("%s/%s", utils.Localhost, utils.BASE)
	dstblks, err := GetBlocks(localhost, dataset)
	if err != nil {
		return out, err
	}
	for _, blk := range srcblks {
		if utils.InList(blk, dstblks) {
			out = append(out, blk)
		}
	}
	return out, nil
}

// DatasetResponse represents response of processDatasetBlocks API
type DatasetResponse struct {
	Dataset string
	Blocks  []string
	Error   error
}

// GetParentDatasets returns list of parent datasets
func GetParentDatasets(rurl, dataset string) ([]string, error) {
	out := []string{}
	parentDatasets, err := GetParents(rurl, dataset)
	if err != nil {
		return out, err
	}
	ch := make(chan DatasetResponse)
	umap := make(map[string]struct{})
	for _, dataset := range parentDatasets {
		umap[dataset] = struct{}{}
		go func() {
			if utils.VERBOSE > 0 {
				log.Printf("processDatasetBlocks for %s from %s", dataset, rurl)
			}
			blocks, err := processDatasetBlocks(rurl, dataset)
			if err != nil {
				log.Println("unable to process dataset blocks", err)
				ch <- DatasetResponse{Dataset: dataset, Blocks: blocks, Error: err}
				return
			}
			// get ordered map of parents
			pblocks, err := GetParentDatasets(rurl, dataset)
			if err != nil {
				log.Println("unable to process parent dataset blocks", err)
				ch <- DatasetResponse{Dataset: dataset, Blocks: blocks, Error: err}
				return
			}
			ch <- DatasetResponse{Dataset: dataset, Blocks: pblocks, Error: err}
		}()
	}
	if len(umap) == 0 {
		// no parent datasets
		log.Printf("no parent datasets found for %s in %s", dataset, rurl)
		return out, nil
	} else {
		log.Println("process %d dataset", len(umap))
	}
	// collect results from goroutines
	exit := false
	for {
		select {
		case r := <-ch:
			if r.Error != nil {
				log.Printf("unable to fetch blocks for url=%s dataset=%s error=%v", rurl, r.Dataset, r.Error)
			} else {
				for _, blk := range r.Blocks {
					out = append(out, blk)
				}
			}
			delete(umap, r.Dataset)
		default:
			if len(umap) == 0 {
				exit = true
			}
			time.Sleep(time.Duration(100) * time.Millisecond) // wait for response
		}
		if exit {
			break
		}
	}
	if utils.VERBOSE > 0 {
		log.Printf("GetParentDatasets yield %d", len(out))
	}

	return out, nil
}

// helper function to check if migration input is already queued
func alreadyQueued(input string) error {
	// TODO: check if given migration input is already queued
	return nil
}

// helper function to return string for status ID
func statusString(status int) string {
	var s string
	if status == IN_PROGRESS {
		s = "IN_PROGRESS"
	} else if status == PENDING {
		s = "PENDING"
	} else if status == COMPLETED {
		s = "COMPLETED"
	} else if status == FAILED {
		s = "FAILED"
	} else if status == TERM_FAILED {
		s = "TEERMINATED"
	}
	return s
}

// helper function to write Migration Report to http response writer
func writeReport(ids []int64, msg string, status int, err error, w http.ResponseWriter) {
	report := MigrationReport{
		MigrationRequestIDs: ids,
		Report:              msg,
		Error:               err,
		Status:              statusString(status),
	}
	var out []MigrationReport
	out = append(out, report)
	if data, e := json.Marshal(report); e == nil {
		w.Write(data)
	}
}

// SubmitMigration DBS API
func (a *API) SubmitMigration() error {

	// read given input
	data, err := io.ReadAll(a.Reader)
	if err != nil {
		log.Println("unable to read from reader", err)
		return err
	}
	tstamp := time.Now().Unix()
	rec := MigrationRequest{
		MIGRATION_STATUS:       PENDING,
		CREATE_BY:              a.CreateBy,
		CREATION_DATE:          tstamp,
		LAST_MODIFIED_BY:       a.CreateBy,
		LAST_MODIFICATION_DATE: tstamp,
	}
	err = json.Unmarshal(data, &rec)
	if err != nil {
		log.Println("unable to unmarshal migration request", err)
		return err
	}
	// check if migration input is already queued
	input := rec.MIGRATION_INPUT
	mid := rec.MIGRATION_REQUEST_ID
	mstr := fmt.Sprintf("Migration request %d", mid)
	if err := alreadyQueued(input); err != nil {
		msg := fmt.Sprintf("%s already queued error %v", mstr, err)
		log.Println(msg)
		return err
	}
	ids, err := startMigrationRequest(rec)
	if err != nil {
		log.Println("unable to start migration request", err)
		return err
	}
	msg := fmt.Sprintf("Migration request for %s has started", rec.MIGRATION_INPUT)
	writeReport(ids, msg, IN_PROGRESS, nil, a.Writer)
	return nil
}

// helper function to start migration request and return list of migration ids
func startMigrationRequest(rec MigrationRequest) ([]int64, error) {
	var err error
	var out []int64
	input := rec.MIGRATION_INPUT
	mid := rec.MIGRATION_REQUEST_ID
	mstr := fmt.Sprintf("Migration request %d", mid)
	if utils.VERBOSE > 0 {
		log.Println(mstr)
	}

	var dstParentBlocks, srcParentBlocks []string
	rurl := rec.MIGRATION_URL
	localhost := fmt.Sprintf("%s/%s", utils.Localhost, utils.BASE)
	if strings.Contains(input, "#") {
		// get parent blocks at destination DBS instance for input block
		dstParentBlocks = prepareBlockMigrationList(rurl, input)
		// get parent blocks at source DBS instance for input block
		srcParentBlocks = prepareBlockMigrationList(localhost, input)
	} else {
		// get parent blocks at destination DBS instance for input dataset
		dstParentBlocks = prepareDatasetMigrationList(rurl, input)
		// get parent blocks at source DBS instance for input dataset
		srcParentBlocks = prepareDatasetMigrationList(localhost, input)
	}
	dstParentBlocks = utils.List2Set(dstParentBlocks)
	srcParentBlocks = utils.List2Set(srcParentBlocks)

	// get list of blocks required for migration
	var migBlocks []string
	for _, blk := range dstParentBlocks {
		if !utils.InList(blk, srcParentBlocks) {
			migBlocks = append(migBlocks, blk)
		}
	}

	// if no migration blocks found to process return immediately
	if len(migBlocks) == 0 {
		log.Printf("%s is already fulfilled", mstr)
		return []int64{}, nil
	}
	if utils.VERBOSE > 0 {
		log.Printf("%s will migrate %d blocks", mstr, len(migBlocks))
	}

	// reverse list of migration blocks such that we will start
	// migration from bottom parents
	sort.Sort(sort.Reverse(sort.StringSlice(migBlocks)))

	// start transaction
	tx, err := DB.Begin()
	if err != nil {
		return []int64{}, err
	}
	defer tx.Rollback()

	log.Println("migrationt input", input)
	for _, blk := range migBlocks {
		log.Println("migration block", blk)
	}

	// loop over migBlocks
	// and insert every chunk of blocks as MigrationBlocks objects
	for idx, blk := range migBlocks {

		// insert MigrationRequest object
		rec.MIGRATION_REQUEST_ID = 0
		rec.MIGRATION_INPUT = blk
		if utils.VERBOSE > 0 {
			log.Printf("%s insert MigrationRequest record %+v", mstr, rec)
		}
		err = rec.Insert(tx)
		if err != nil {
			log.Printf("unable to insert MigrationRequest record %+v, error %v", rec, err)
			return []int64{}, err
		}

		// get inserted migration ID
		rid, err := GetID(tx, "MIGRATION_REQUESTS", "MIGRATION_REQUEST_ID", "MIGRATION_INPUT", blk)
		if err != nil {
			log.Println("unable to get MIGRATION_REQUESTS id", err)
			return []int64{}, err
		}

		// set migration record
		mrec := MigrationBlocks{
			MIGRATION_REQUEST_ID:   rid,
			MIGRATION_BLOCK_NAME:   blk,
			MIGRATION_ORDER:        int64(idx),
			MIGRATION_STATUS:       PENDING,
			CREATE_BY:              rec.CREATE_BY,
			CREATION_DATE:          rec.CREATION_DATE,
			LAST_MODIFICATION_DATE: rec.LAST_MODIFICATION_DATE,
			LAST_MODIFIED_BY:       rec.LAST_MODIFIED_BY}
		if utils.VERBOSE > 0 {
			log.Printf("%s insert MigrationBlocks record %+v", mstr, mrec)
		}
		err = mrec.Insert(tx)
		if err != nil {
			log.Println("unable to insert MigrationBlocks record", err)
			return []int64{}, err
		}
		out = append(out, rid)
	}

	// commit transaction
	err = tx.Commit()
	if err != nil {
		log.Printf("%s unatble to commit transaction error %v", mstr, err)
		return []int64{}, err
	}

	if utils.VERBOSE > 0 {
		log.Printf("%s finished", mstr)
	}
	return out, nil
}

// ProcessMigration will process given migration request
// and inject data to source DBS
// It expects that client will provide migration_request_url and migration id
func (a *API) ProcessMigration(timeout int, writeReport bool) error {

	var status int
	var err error
	var msg string

	// setup context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	// create channel to report when operation will be completed
	ch := make(chan bool)

	// set default status
	status = FAILED

	// execute slow operation in background
	go a.processMigration(ch, &status)

	// the slow operation will either finish or timeout
	select {
	case <-ctx.Done():
		msg = fmt.Sprintf("Process migration function timeout")
		err = errors.New(msg)
	case <-ch:
		msg = fmt.Sprintf("migration request status %v", status)
	}
	report := MigrationReport{Report: msg, Status: statusString(status)}
	log.Println(report.Report)
	if writeReport {
		data, err := json.Marshal(report)
		if err == nil {
			a.Writer.Write(data)
		}
		return err
	}
	return err
}

// processMigration will process given migration report
// and inject data to source DBS
func (a *API) processMigration(ch chan<- bool, status *int) {
	// report on channel that we are done with this workflow
	defer func() {
		ch <- true
	}()

	// backward compatibility with DBS migration server which uses migration_rqst_id
	if v, ok := a.Params["migration_rqst_id"]; ok {
		a.Params["migration_request_id"] = v
	}

	// obtain migration request record
	val, err := getSingleValue(a.Params, "migration_request_id")
	midint, err := strconv.Atoi(val)
	if err != nil {
		log.Printf("unable to convert mid", err)
	}
	mid := int64(midint)
	if utils.VERBOSE > 0 {
		log.Println("process migration request", mid)
	}
	records, err := MigrationRequests(mid)
	if utils.VERBOSE > 0 {
		log.Println("found process migration request records", records)
	}
	if err != nil {
		log.Printf("fail to fetch migration request %d, error", mid, err)
		return
	}
	if len(records) != 1 {
		log.Printf("found %d requests for mid=%d, stop processing", len(records), mid)
		return
	}
	mrec := records[0]
	// update migration status
	updateMigrationStatus(mid, IN_PROGRESS)

	// find block name for our migration id
	stm := getSQL("migration_block")
	stm = CleanStatement(stm)
	var args []interface{}
	args = append(args, mid)
	if utils.VERBOSE > 0 {
		utils.PrintSQL(stm, args, "execute")
	}
	var bid, bOrder, bStatus int64
	var block string
	err = DB.QueryRow(stm, args...).Scan(
		&bid, &block, &bOrder, &bStatus,
	)
	if err != nil {
		log.Printf("query='%s' args='%v' error=%v", stm, args, err)
		return
	}

	// obtain block details from destination DBS
	rurl := fmt.Sprintf("%s/blockdump?block_name=%s", mrec.MIGRATION_URL, url.QueryEscape(block))
	data, err := getData(rurl)
	if err != nil {
		log.Printf("unable to query %s/blockdump, error %v", rurl, err)
	}
	// NOTE: /blockdump API returns BulkBlocks record used in /bulkblocks API
	//     var rec BlockDumpRecord
	var brec BulkBlocks
	err = json.Unmarshal(data, &brec)
	if err != nil {
		log.Println("blockdump data", string(data))
		log.Printf("unable to unmarshal BulkBlocks, error %v", err)
		return
	}
	cby := a.CreateBy
	if brec.Dataset.CreateBy != "" {
		cby = brec.Dataset.CreateBy
	}
	var rec Record
	err = json.Unmarshal(data, &rec)
	if err != nil {
		log.Println("blockdump data", string(data))
		log.Printf("unable to unmarshal Record, error %v", err)
		return
	}
	reader := bytes.NewReader(data)
	writer := utils.StdoutWriter("")

	// insert block dump record into source DBS
	//     err = rec.InsertBlockDump()
	api := &API{
		Params:    rec,
		Api:       "bulkblocks",
		Writer:    writer,
		Reader:    reader,
		CreateBy:  cby,
		Separator: a.Separator,
	}
	err = api.InsertBulkBlocks()
	if utils.VERBOSE > 1 {
		log.Printf("Insert bulkblocks %+v", api)
	}
	if err != nil {
		log.Println("insert block dump record failed with", err)
		updateMigrationStatus(mid, FAILED)
	} else {
		*status = COMPLETED
		updateMigrationStatus(mid, COMPLETED)
	}
}

// updateMigrationStatus updates migration status
func updateMigrationStatus(mid int64, status int) error {
	tmplData := make(Record)
	tmplData["Owner"] = DBOWNER
	stm, err := LoadTemplateSQL("update_migration_status", tmplData)
	if err != nil {
		log.Println("unable to load update_migration_status template", err)
		return err
	}

	// start transaction
	tx, err := DB.Begin()
	if err != nil {
		log.Println("unable to get DB transaction", err)
		return err
	}
	defer tx.Rollback()
	stm = CleanStatement(stm)
	if utils.VERBOSE > 0 {
		var args []interface{}
		args = append(args, status)
		args = append(args, mid)
		utils.PrintSQL(stm, args, "execute")
	}

	_, err = tx.Exec(stm, status, mid)
	if err != nil {
		log.Printf("unable to execute %s, error %v", stm, err)
		return err
	}

	// commit transaction
	err = tx.Commit()
	if err != nil {
		log.Println("unable to commit transaction", err)
		return err
	}
	return nil
}

// MigrationRemoveRequest represents migration remove request object
type MigrationRemoveRequest struct {
	MIGRATION_REQUEST_ID int64  `json:"migration_rqst_id"`
	CREATE_BY            string `json:"create_by"`
}

// RemoveMigration DBS API
func (a *API) RemoveMigration() error {
	data, err := io.ReadAll(a.Reader)
	if err != nil {
		log.Println("unable to readl data", err)
		return err
	}
	rec := MigrationRemoveRequest{}
	err = json.Unmarshal(data, &rec)
	if err != nil {
		log.Println("unable to decode data", err)
		return err
	}

	// start transaction
	tx, err := DB.Begin()
	if err != nil {
		log.Println("unable to get DB transaction", err)
		return err
	}
	defer tx.Rollback()

	stm := getSQL("count_migration_requests")
	stm = CleanStatement(stm)
	if utils.VERBOSE > 0 {
		var args []interface{}
		args = append(args, rec.MIGRATION_REQUEST_ID)
		args = append(args, rec.CREATE_BY)
		utils.PrintSQL(stm, args, "execute")
	}
	var tid float64
	err = tx.QueryRow(stm, rec.MIGRATION_REQUEST_ID, rec.CREATE_BY).Scan(&tid)
	if err != nil {
		msg := fmt.Sprintf("unable to query statement:\n%v\nerror=%v", stm, err)
		log.Println(msg)
		return errors.New(msg)
	}
	if utils.VERBOSE > 0 {
		log.Println("found request ID", tid, "to remove")
	}

	if tid > 0 {
		stm = getSQL("remove_migration_requests")
		_, err = tx.Exec(stm, rec.MIGRATION_REQUEST_ID, rec.CREATE_BY)
		if err != nil {
			msg := fmt.Sprintf("fail to execute SQL statement '%s'", stm)
			log.Println(msg)
			return errors.New(msg)
		}
		err = tx.Commit()
		if err != nil {
			log.Println("unable to commit transaction", err)
			return err
		}
	}
	return nil
}

// MigrationStatusRequest defines status request structure
type MigrationStatusRequest struct {
	BLOCK_NAME string `json:"block_name"`
	DATASET    string `json:"dataset"`
	USER       string `json:"user"`
}

// StatusMigration DBS API
func (a *API) StatusMigration() error {
	var args []interface{}
	var conds []string
	tmpl := make(Record)
	tmpl["Owner"] = DBOWNER

	// backward compatibility with DBS migration server which uses migration_rqst_id
	val, ok := a.Params["migration_rqst_id"]
	if ok {
		a.Params["migration_request_id"] = val
	}

	oldest, _ := getSingleValue(a.Params, "oldest")
	if oldest == "true" {
		tmpl["Oldest"] = true
	}
	if _, e := getSingleValue(a.Params, "migration_request_id"); e == nil {
		conds, args = AddParam("migration_request_id", "MR.MIGRATION_REQUEST_ID", a.Params, conds, args)
	}
	if _, e := getSingleValue(a.Params, "migration_input"); e == nil {
		conds, args = AddParam("migration_input", "MR.MIGRATION_INPUT", a.Params, conds, args)
	}
	if _, e := getSingleValue(a.Params, "migration_url"); e == nil {
		conds, args = AddParam("migration_url", "MR.MIGRATION_URL", a.Params, conds, args)
	}
	if _, e := getSingleValue(a.Params, "dataset"); e == nil {
		conds, args = AddParam("dataset", "MR.DATASET", a.Params, conds, args)
	}
	if _, e := getSingleValue(a.Params, "block_name"); e == nil {
		conds, args = AddParam("block_name", "MR.BLOCK_NAME", a.Params, conds, args)
	}
	if _, e := getSingleValue(a.Params, "user"); e == nil {
		conds, args = AddParam("user", "MR.USER", a.Params, conds, args)
	}
	if _, e := getSingleValue(a.Params, "create_by"); e == nil {
		conds, args = AddParam("create_by", "MR.CREATE_BY", a.Params, conds, args)
	}

	// get SQL statement from static area
	stm := getSQL("migration_requests")
	stm = WhereClause(stm, conds)

	// use generic query API to fetch the results from DB
	return executeAll(a.Writer, a.Separator, stm, args...)
}

// TotalMigration DBS API
func (a *API) TotalMigration() error {
	var args []interface{}
	// get SQL statement from static area
	stm := getSQL("migration_total_count")

	// use generic query API to fetch the results from DB
	return executeAll(a.Writer, a.Separator, stm, args...)
}
