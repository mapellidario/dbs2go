package dbs

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/vkuznet/dbs2go/utils"
	"golang.org/x/exp/errors"
)

var datasetPattern = regexp.MustCompile(`^/(\*|[a-zA-Z\*][a-zA-Z0-9_\*\-]{0,100})(/(\*|[a-zA-Z0-9_\.\-\*]{1,199})){0,1}(/(\*|[A-Z\-\*]{1,50})){0,1}$`)
var blockPattern = regexp.MustCompile(`^/(\*|[a-zA-Z\*][a-zA-Z0-9_\*\-]{0,100})(/(\*|[a-zA-Z0-9_\.\-\*]{1,199})){0,1}(/(\*|[A-Z\-\*]{1,50})){0,1}#[a-zA-Z0-9\.\-_]+`)
var primDSPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9\-_]+[*]?$|^[*]$`)
var procDSPattern = regexp.MustCompile(`[a-zA-Z0-9\.\-_]+`)
var tierPattern = regexp.MustCompile(`[A-Z\-_]+`)
var eraPattern = regexp.MustCompile(`([a-zA-Z0-9\-_]+)`)
var releasePattern = regexp.MustCompile(`([a-zA-Z0-9\-_]+)`)
var appPattern = regexp.MustCompile(`([a-zA-Z0-9\-_]+)`)
var filePattern = regexp.MustCompile(`/([a-z]+)/([a-z0-9]+)/([a-z0-9]+)/([a-zA-Z0-9\-_]+)/([a-zA-Z0-9\-_]+)/([A-Z\-_]+)/([a-zA-Z0-9\-_]+)((/[0-9]+){3}){0,1}/([0-9]+)/([a-zA-Z0-9\-_]+).root`)
var lfnPattern = regexp.MustCompile(`/[a-zA-Z0-9_-]+.*/([a-zA-Z0-9\-_]+).root$`)

var unixTimePattern = regexp.MustCompile(`^[1-9][0-9]{9}$`)
var intPattern = regexp.MustCompile(`^\d+$`)
var runRangePattern = regexp.MustCompile(`^\d+-\d+$`)

// helper function to validate string parameters
func strType(key string, val interface{}) error {
	var v string
	switch vvv := val.(type) {
	case string:
		v = vvv
	default:
		return errors.New(fmt.Sprintf("invalid type of input parameter '%s' for value '%+v' type '%T'", key, val, val))
	}
	errMsg := fmt.Sprintf("unable to match '%s' value '%+v'", key, val)
	if key == "dataset" {
		if matched := datasetPattern.MatchString(v); !matched {
			return errors.New(errMsg)
		}
	}
	if key == "block_name" {
		if matched := blockPattern.MatchString(v); !matched {
			return errors.New(errMsg)
		}
	}
	if key == "logical_file_name" {
		if strings.Contains(v, "[") {
			if strings.Contains(v, "'") { // Python bad json, e.g. ['bla']
				v = strings.Replace(v, "'", "\"", -1)
			}
			var records []string
			err := json.Unmarshal([]byte(v), &records)
			if err != nil {
				return err
			}
			for _, r := range records {
				if matched := filePattern.MatchString(r); !matched {
					if matched := lfnPattern.MatchString(r); !matched {
						return errors.New(errMsg)
					}
				}
			}
			return nil
		}
		if matched := filePattern.MatchString(v); !matched {
			if matched := lfnPattern.MatchString(v); !matched {
				return errors.New(errMsg)
			}
		}
	}
	if key == "primary_ds_name" {
		if v == "" && val == "*" { // when someone passed wildcard
			return nil
		}
		if matched := primDSPattern.MatchString(v); !matched {
			return errors.New(errMsg)
		}
	}
	if key == "processed_ds_name" {
		if v == "" && val == "*" { // when someone passed wildcard
			return nil
		}
		if matched := procDSPattern.MatchString(v); !matched {
			return errors.New(errMsg)
		}
	}
	if key == "app_name" {
		if v == "" && val == "*" { // when someone passed wildcard
			return nil
		}
		if matched := appPattern.MatchString(v); !matched {
			return errors.New(errMsg)
		}
	}
	if key == "release_version" {
		if v == "" && val == "*" { // when someone passed wildcard
			return nil
		}
		if matched := releasePattern.MatchString(v); !matched {
			return errors.New(errMsg)
		}
	}
	return nil
	//     return errors.New(fmt.Sprintf("Invalid type of %s, should be string", k))
}

// helper function to validate int parameters
func intType(k string, v interface{}) error {
	// to be implemented
	return nil
}

// helper function to validate mix parameters
func mixType(k string, v interface{}) error {
	// to be implemented
	return nil
}

// Validate provides validation of all input parameters of HTTP request
func Validate(r *http.Request) error {
	strParameters := []string{"dataset", "parent_dataset", "release_version", "pset_hash", "app_name", "output_module_label", "global_tag", "processing_version", "acquisition_era_name", "physics_group_name", "logical_file_name", "primary_ds_name", "primary_ds_type", "processed_ds_name", "data_tier_name", "dataset_access_type", "create_by", "last_modified_by"}
	intParameters := []string{"cdate", "ldate", "min_cdate", "max_cdate", "min_ldate", "max_ldate", "datset_id", "prep_id"}
	mixParameters := []string{"run_num"} // can be different type
	if r.Method == "GET" {
		for k, vvv := range r.URL.Query() {
			// vvv here is []string{} type since all HTTP parameters are treated
			// as list of strings
			for _, v := range vvv {
				if utils.InList(k, strParameters) {
					if err := strType(k, v); err != nil {
						return err
					}
				}
				if utils.InList(k, intParameters) {
					if err := intType(k, v); err != nil {
						return err
					}
				}
				if utils.InList(k, mixParameters) {
					if err := mixType(k, v); err != nil {
						return err
					}
				}
			}
			log.Printf("query parameter key=%s values=%+v\n", k, vvv)
		}
	}
	return nil
}

// ValidatePostPayload function to validate POST request
func ValidatePostPayload(rec Record) error {
	// TODO: I need to implement validation of POST payload here
	// for instance, check that provided set of parameters has correct key-values
	return nil
}
