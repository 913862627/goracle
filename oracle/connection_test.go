/*
   Copyright 2013 Tamás Gulácsi

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/
package oracle

import (
	"bytes"
	"flag"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
)

var dsn = flag.String("dsn", "", "Oracle DSN (user/passw@sid)")
var dbg = flag.Bool("debug", false, "print debug messages?")

func init() {
	flag.Parse()
	IsDebug = *dbg
}

func TestMakeDSN(t *testing.T) {
	dsn := MakeDSN("localhost", 1521, "sid", "")
	if dsn != ("(DESCRIPTION=(ADDRESS_LIST=(ADDRESS=" +
		"(PROTOCOL=TCP)(HOST=localhost)(PORT=1521)))(CONNECT_DATA=(SID=sid)))") {
		t.Logf(dsn)
		t.Fail()
	}
	dsn = MakeDSN("localhost", 1522, "", "service")
	if dsn != ("(DESCRIPTION=(ADDRESS_LIST=(ADDRESS=" +
		"(PROTOCOL=TCP)(HOST=localhost)(PORT=1522)))(CONNECT_DATA=" +
		"(SERVICE_NAME=service)))") {
		t.Logf(dsn)
		t.Fail()
	}
}

func TestClientVersion(t *testing.T) {
	t.Logf("ClientVersion=%+v", ClientVersion())
}

func TestIsConnected(t *testing.T) {
	if (Connection{}).IsConnected() {
		t.Fail()
	}
	conn := getConnection(t)
	if !conn.IsConnected() {
		t.Fail()
	}
	if err := conn.Ping(); err != nil {
		t.Logf("error with Ping: %s", err)
		t.Fail()
	}
}

func TestCursor(t *testing.T) {
	conn := getConnection(t)
	if !conn.IsConnected() {
		t.FailNow()
	}
	cur := conn.NewCursor()
	defer cur.Close()
	qry := `SELECT owner||'.'||object_name, object_id, object_id/EXP(1)
	          FROM all_objects
	          WHERE ROWNUM < 20
	          ORDER BY 3`
	if err := cur.Execute(qry, nil, nil); err != nil {
		t.Logf(`error with "%s": %s`, qry, err)
		t.Fail()
	}
	row, err := cur.FetchOne()
	if err != nil {
		t.Logf("error fetching: %s", err)
		t.Fail()
	}
	t.Logf("row: %+v", row)
	rows, err := cur.FetchMany(3)
	if err != nil {
		t.Logf("error fetching many: %s", err)
		t.Fail()
	}
	for i, row := range rows {
		t.Logf("%03d: %v", i, row)
	}
	rows, err = cur.FetchAll()
	if err != nil {
		t.Logf("error fetching remaining: %s", err)
		t.Fail()
	}
	for i, row := range rows {
		t.Logf("%03d: %v", i, row)
	}

	qry = `SELECT B.object_id, A.rn
	         FROM all_objects B, (SELECT :1 rn FROM DUAL) A
	         WHERE ROWNUM < GREATEST(2, A.rn)`
	params := []interface{}{2}
	if err = cur.Execute(qry, params, nil); err != nil {
		t.Logf(`error with "%s" %v: %s`, qry, params, err)
		t.Fail()
	}
	if rows, err = cur.FetchMany(3); err != nil {
		t.Logf("error fetching many: %s", err)
		t.Fail()
	}
	for i, row := range rows {
		t.Logf("%03d: %v", i, row)
	}

	qry = `SELECT TO_DATE('2006-01-02 15:04:05', 'YYYY-MM-DD HH24:MI:SS') FROM DUAL`
	if err = cur.Execute(qry, nil, nil); err != nil {
		t.Logf(`error with "%s": %s`, qry, err)
		t.Fail()
	}
	if row, err = cur.FetchOne(); err != nil {
		t.Logf("error fetching: %s", err)
		t.Fail()
	}
	t.Logf("%03d: %v", 0, row)

	if CTrace {
		qry = `SELECT TO_DSINTERVAL('2 10:20:30.456') FROM DUAL`
		if err = cur.Execute(qry, nil, nil); err != nil {
			t.Logf(`error with "%s": %s`, qry, err)
			t.Fail()
		}
		if row, err = cur.FetchOne(); err != nil {
			t.Logf("error fetching INTERVAL: %s", err)
			t.Fail()
		}
		t.Logf("%03d: %v", 0, row)
	}

	if err = cur.Execute("CREATE GLOBAL TEMPORARY TABLE w (x LONG)", nil, nil); err != nil {
		t.Logf("cannot check LONG: %s", err)
	} else {
		cur.Execute("INSERT INTO w VALUES ('a')", nil, nil)
		qry = `SELECT x FROM w`
		if err = cur.Execute(qry, nil, nil); err != nil {
			t.Logf(`error with "%s": %s`, qry, err)
			t.Fail()
		}
		if row, err = cur.FetchOne(); err != nil {
			t.Logf("error fetching: %s", err)
			t.Fail()
		}
		t.Logf("row: %v", row)
		cur.Execute("DROP TABLE w", nil, nil)
	}
}

var conn Connection

func getConnection(t *testing.T) Connection {
	if conn.handle != nil {
		return conn
	}

	if !(dsn != nil && *dsn != "") {
		t.Logf("cannot test connection without dsn!")
		return conn
	}
	user, passw, sid := SplitDSN(*dsn)
	var err error
	conn, err = NewConnection(user, passw, sid, false)
	if err != nil {
		log.Panicf("error creating connection to %s: %s", *dsn, err)
	}
	if err = conn.Connect(0, false); err != nil {
		log.Panicf("error connecting: %s", err)
	}
	return conn
}

var alloc uint64
var memkb int
var memcmd []string

func gcMem() {
	ms := new(runtime.MemStats)
	runtime.GC()
	runtime.ReadMemStats(ms)
	if alloc == 0 {
		alloc = ms.Alloc
		return
	}
	if memcmd == nil {
		memcmd = []string{"-o", "rss=", strconv.Itoa(os.Getpid())}
	}
	omemkb := memkb
	out, err := exec.Command("ps", memcmd...).Output()
	if err != nil {
		log.Printf("error running ps %s: %s", memcmd, err)
	} else {
		if x, err := strconv.Atoi(string(bytes.TrimSpace(out))); err != nil {
			log.Printf("not number: %q (%s)", out, err)
		} else {
			memkb = x
		}
	}
	log.Printf("mem += %.3db = %db - RSS += %dkb = %dkb", ms.Alloc-alloc, ms.Alloc,
		memkb-omemkb, memkb)
	alloc = ms.Alloc
}

func TestReConnect(t *testing.T) {
	var err error
	tick := time.Tick(100 * time.Millisecond)
	for i := 0; i < 300; i++ {
		<-tick
		log.Printf("%d. reconnection", i)
		conn = getConnection(t)
		if err = conn.Connect(0, false); err != nil {
			t.Errorf("error connection with 0 to db: %s", err)
			t.FailNow()
			break
		}
		conn.Close()
		if i%10 == 0 {
			gcMem()
		}
	}
	gcMem()
}
