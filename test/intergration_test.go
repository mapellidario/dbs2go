package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/vkuznet/dbs2go/dbs"
	"github.com/vkuznet/dbs2go/web"

	limiter "github.com/ulule/limiter/v3"
	stdlib "github.com/ulule/limiter/v3/drivers/middleware/stdlib"
	memory "github.com/ulule/limiter/v3/drivers/store/memory"
)

// initializes the limiter middleware
func initTestLimiter(t *testing.T, period string) {
	rate, err := limiter.NewRateFromFormatted(period)
	if err != nil {
		t.Fatalf("Limiter Error")
	}
	store := memory.NewStore()
	instance := limiter.New(store, rate)
	web.LimiterMiddleware = stdlib.NewMiddleware(instance)
}

// configures the test server with basic parameters
func runTestServer(t *testing.T, serverType string, lexiconFile string) *httptest.Server {
	dbfile := os.Getenv("DBS_DB_FILE")
	if dbfile == "" {
		t.Fatal("no DBS_DB_FILE env variable, please define")
	}
	web.Config.Base = "/dbs"
	web.Config.DBFile = dbfile
	web.Config.LexiconFile = lexiconFile
	web.Config.ServerCrt = ""
	web.Config.ServerKey = ""
	web.Config.ServerType = serverType
	web.Config.LogFile = "/tmp/dbs2go-test.log"
	web.Config.Verbose = 0

	initTestLimiter(t, "100-S")

	ts := httptest.NewServer(web.Handlers())

	return ts
}

// injects dbs records
func injectDBRecord(t *testing.T, rec dbs.DBRecord, method string, url string, endpoint string, handler func(http.ResponseWriter, *http.Request)) []dbs.Record {
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err.Error())
	}
	reader := bytes.NewReader(data)
	req := newreq(t, method, url, endpoint, reader, nil)
	req.Header.Add("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()

	if r.StatusCode != http.StatusOK {
		t.Fatal(err)
	}

	rr, err := respRecorder("GET", endpoint, nil, handler)
	if err != nil {
		t.Error(err)
	}

	var records []dbs.Record
	data = rr.Body.Bytes()
	err = json.Unmarshal(data, &records)
	if err != nil {
		t.Fatal(err)
	}

	return records
}

// fetches data from url and endpoint
func getData(t *testing.T, url string, endpoint string, params url.Values) ([]dbs.Record, int) {
	r, err := http.DefaultClient.Do(newreq(t, "GET", url, endpoint, nil, params))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()

	if r.StatusCode != http.StatusOK {
		t.Fatalf("Bad status code: %v", r.StatusCode)
	}

	var d []dbs.Record
	err = json.NewDecoder(r.Body).Decode(&d)
	if err != nil {
		t.Fatalf("Failed to decode body, %v", err)
	}

	return d, r.StatusCode
}

// creates an http request for testing
func newreq(t *testing.T, method string, hostname string, endpoint string, body io.Reader, params url.Values) *http.Request {
	url2, err := url.Parse(hostname)
	if err != nil {
		t.Fatal(err)
	}
	url2.Path = endpoint
	url2.RawQuery = params.Encode()

	r, err := http.NewRequest(method, url2.String(), body)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// compares received response to expected
func verifyResponse(t *testing.T, received []dbs.Record, expected map[string]interface{}, fields []string) {
	for _, r := range received {
		for _, f := range fields {
			if r[f] != expected[f] {
				if strings.Contains(f, "id") || f == "creation_date" {
					if r[f] == nil {
						t.Fatalf("ID field empty")
					}
				} else {
					t.Fatalf("Incorrect %s: Expected %v, Received: %v", f, expected[f], r[f])
				}
			}
		}
	}
}

// run test workflow for a single endpoint
func runTestWorkflow(t *testing.T, tsR *httptest.Server, tsW *httptest.Server, endpoint string, hdlr func(http.ResponseWriter, *http.Request), dbrec dbs.DBRecord, params url.Values, fields []string) {
	emap := remapRecord(t, dbrec)

	t.Run("Test empty GET", func(t *testing.T) {
		d, _ := getData(t, tsR.URL, endpoint, nil)
		if len(d) != 0 {
			t.Fatal("Data exists")
		}
	})

	t.Run("Test POST", func(t *testing.T) {
		records := injectDBRecord(t, dbrec, "POST", tsW.URL, endpoint, hdlr)
		verifyResponse(t, records, emap, fields)
	})

	t.Run("Test GET after POST", func(t *testing.T) {
		d, _ := getData(t, tsR.URL, endpoint, nil)
		verifyResponse(t, d, emap, fields)
	})

	t.Run("Test GET with parameters", func(t *testing.T) {
		getData(t, tsR.URL, endpoint, params)
	})
}

// remap a DBRecord to a general map
func remapRecord(t *testing.T, record dbs.DBRecord) map[string]interface{} {
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err.Error())
	}
	var emap map[string]interface{}
	err = json.Unmarshal(data, &emap)
	if err != nil {
		t.Fatal(err.Error())
	}
	return emap
}

// TestDBSIntegration Tests both DBSReader and DBSWriter Endpoints
func TestDBSIntegration(t *testing.T) {
	db := initDB(false)
	defer db.Close()

	lexiconFileWriter := os.Getenv("DBS_WRITER_LEXICON_FILE")
	if lexiconFileWriter == "" {
		t.Fatal("no DBS_WRITER_LEXICON_FILE env variable, please define")
	}

	lexiconFileReader := os.Getenv("DBS_READER_LEXICON_FILE")
	if lexiconFileReader == "" {
		t.Fatal("no DBS_READER_LEXICON_FILE env variable, please define")
	}

	// start DBSWriter server
	tsW := runTestServer(t, "DBSWriter", lexiconFileWriter)
	defer tsW.Close()

	// start DBSReader server
	tsR := runTestServer(t, "DBSReader", lexiconFileReader)
	defer tsR.Close()

	t.Run("Test datatiers", func(t *testing.T) {
		dt := dbs.DataTiers{
			DATA_TIER_NAME: "GEN-SIM-RAW",
			CREATE_BY:      "tester",
		}
		// fields that are created thru api handler
		var fields = []string{
			"data_tier_id",
			"creation_date",
			"data_tier_name",
			"create_by",
		}

		params := url.Values{}
		params.Add("data_tier_name", "GEN-SIM-RAW")

		runTestWorkflow(t, tsR, tsW, "/dbs/datatiers", web.DatatiersHandler, &dt, params, fields)
	})

	t.Run("Test physicsgroups", func(t *testing.T) {
		pg := dbs.PhysicsGroups{
			PHYSICS_GROUP_NAME: "Tracker",
		}
		// fields that are created thru api handler
		fields := []string{"physics_group_name"}

		params := url.Values{}
		params.Add("physics_group_name", "Tracker")

		runTestWorkflow(t, tsR, tsW, "/dbs/physicsgroups", web.PhysicsGroupsHandler, &pg, params, fields)
	})
}
